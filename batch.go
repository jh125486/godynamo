package godynamo

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

const (
	// batchGetMaxKeys is AWS's hard per-call limit on the number of keys a
	// single BatchGetItem request may carry.
	batchGetMaxKeys = 100
	// batchWriteMaxRequests is AWS's hard per-call limit on the number of
	// PutRequest/DeleteRequest entries a single BatchWriteItem request may
	// carry.
	batchWriteMaxRequests = 25
	// batchMaxRetries bounds how many times [BatchGetBuilder.Run] /
	// [BatchWriteBuilder.Run] will retry a chunk's UnprocessedKeys /
	// UnprocessedItems before giving up and returning an error.
	batchMaxRetries = 5
)

// batchBackoff returns a small linearly-growing backoff duration for retry
// attempt (0-indexed), used between UnprocessedKeys/UnprocessedItems
// retries. It's intentionally simple (no jitter, no external backoff
// library) since batchMaxRetries bounds the total wait to a fraction of a
// second.
func batchBackoff(attempt int) time.Duration {
	return time.Duration(attempt+1) * 10 * time.Millisecond
}

// BatchGetBuilder is a fluent builder for a batched multi-item fetch of a
// single type T, obtained via [BatchGet].
type BatchGetBuilder[T any] struct {
	ctx context.Context
	db  *DB
	ids []uuid.UUID
}

// BatchGet returns a fluent builder for fetching multiple items of type T
// via one or more BatchGetItem calls. ctx is captured for use by
// [BatchGetBuilder.Run] — it is not re-passed to it.
func BatchGet[T any](ctx context.Context, db *DB) *BatchGetBuilder[T] {
	return &BatchGetBuilder[T]{ctx: ctx, db: db}
}

// IDs queues ids to be fetched, appending to any previously queued IDs.
func (b *BatchGetBuilder[T]) IDs(ids ...uuid.UUID) *BatchGetBuilder[T] {
	b.ids = append(b.ids, ids...)
	return b
}

// Run executes the batched fetch. Each id's key is computed via
// [zeroKeys][T], the same zero-value-plus-ID approach [Get] uses — so, like
// Get, Run only computes the correct key for models using the default
// (ID-only) PK/SK; models whose dynamo tag references other sibling fields
// will get a wrong key.
//
// Queued ids are chunked into groups of at most 100 (AWS's BatchGetItem
// hard per-call limit) and issued as separate BatchGetItem calls,
// sequentially.
//
// DynamoDB's BatchGetItem silently omits items that don't exist in the
// table — a missing id is not an error, it's simply absent from the
// returned slice. BatchGetItem also does not guarantee the result order
// matches the order ids were queued in. Callers needing either "did every
// id resolve" or a specific ordering must post-process the returned slice
// themselves (e.g. compare its length against the number of ids queued, or
// index results by their unmarshaled ID).
//
// If a chunk's response has non-empty UnprocessedKeys for the table, Run
// retries with just those keys, up to batchMaxRetries times with a short
// linearly-growing backoff between attempts. If keys are still unprocessed
// once retries are exhausted, Run returns an error.
func (b *BatchGetBuilder[T]) Run() ([]T, error) {
	typeName := reflect.TypeFor[T]().Name()
	if len(b.ids) == 0 {
		return nil, nil
	}

	keys := make([]map[string]types.AttributeValue, len(b.ids))
	for i, id := range b.ids {
		pk, sk := zeroKeys[T](id)
		keys[i] = itemKey(pk, sk)
	}

	var items []T
	for start := 0; start < len(keys); start += batchGetMaxKeys {
		end := min(start+batchGetMaxKeys, len(keys))
		chunkItems, err := b.runChunk(keys[start:end])
		if err != nil {
			return nil, fmt.Errorf("godynamo: batch get %s: %w", typeName, err)
		}
		items = append(items, chunkItems...)
	}
	return items, nil
}

// runChunk issues BatchGetItem calls for one chunk of at most 100 keys,
// retrying UnprocessedKeys as described on [BatchGetBuilder.Run].
func (b *BatchGetBuilder[T]) runChunk(keys []map[string]types.AttributeValue) ([]T, error) {
	var items []T
	request := keys

	for attempt := 0; ; attempt++ {
		out, err := b.db.client.BatchGetItem(b.ctx, &dynamodb.BatchGetItemInput{
			RequestItems: map[string]types.KeysAndAttributes{
				b.db.table: {Keys: request},
			},
		})
		if err != nil {
			return nil, err
		}

		for _, av := range out.Responses[b.db.table] {
			var item T
			if err := attributevalue.UnmarshalMap(av, &item); err != nil {
				return nil, fmt.Errorf("unmarshal: %w", err)
			}
			items = append(items, item)
		}

		unprocessed := out.UnprocessedKeys[b.db.table]
		if len(unprocessed.Keys) == 0 {
			return items, nil
		}
		if attempt >= batchMaxRetries {
			return nil, fmt.Errorf("%d keys still unprocessed after %d retries", len(unprocessed.Keys), batchMaxRetries)
		}
		request = unprocessed.Keys
		time.Sleep(batchBackoff(attempt))
	}
}

// BatchWriteBuilder is a fluent builder for a batched multi-item write
// (create/update/delete) of a single type T, obtained via [BatchWrite].
type BatchWriteBuilder[T any] struct {
	ctx    context.Context
	db     *DB
	writes []types.WriteRequest
	err    error
}

// BatchWrite returns a fluent builder for writing multiple items of type T
// via one or more BatchWriteItem calls. ctx is captured for use by
// [BatchWriteBuilder.Run] — it is not re-passed to it.
func BatchWrite[T any](ctx context.Context, db *DB) *BatchWriteBuilder[T] {
	return &BatchWriteBuilder[T]{ctx: ctx, db: db}
}

// Put queues items to be created or updated, using the same
// create-vs-update audit-stamping logic as single-item [Put] (zero
// Model.CreatedAt => new item: [SetType], stamp
// CreatedAt/CreatedBy/UpdatedAt/UpdatedBy, Version=1; non-zero => preserve
// CreatedAt/CreatedBy, bump UpdatedAt/UpdatedBy, increment Version).
//
// UNLIKE single-item [Put] (and unlike [TransactWriteBuilder.Put]),
// BatchWrite's puts are unconditional: BatchWriteItem has no
// ConditionExpression support at all — this is a hard AWS API limitation,
// not a design choice made here. There is no attribute_not_exists(PK)
// protection against an accidental double-create, and no optimistic-lock
// enforcement against a concurrent update; Version is still
// stamped/incremented in the written item, it's just not checked against
// the item's previous value. Callers that need either guarantee must use
// [Put] or [TransactWrite] instead.
func (b *BatchWriteBuilder[T]) Put(items ...*T) *BatchWriteBuilder[T] {
	for _, item := range items {
		if b.err != nil {
			return b
		}
		av, _, err := stampAndMarshal(b.ctx, b.db, item)
		if err != nil {
			b.err = err
			return b
		}
		b.writes = append(b.writes, types.WriteRequest{PutRequest: &types.PutRequest{Item: av}})
	}
	return b
}

// Delete queues ids to be deleted. Each id's key is computed via
// [zeroKeys][T], the same zero-value-plus-ID limitation as single-item
// [Delete]: only correct for models using the default (ID-only) PK/SK.
func (b *BatchWriteBuilder[T]) Delete(ids ...uuid.UUID) *BatchWriteBuilder[T] {
	for _, id := range ids {
		pk, sk := zeroKeys[T](id)
		b.writes = append(b.writes, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: itemKey(pk, sk)}})
	}
	return b
}

// Run executes the batched write. Queued Put/Delete entries are combined
// (in the order queued) and chunked into groups of at most 25 (AWS's
// BatchWriteItem hard per-call limit), issued as separate BatchWriteItem
// calls, sequentially.
//
// If a chunk's response has non-empty UnprocessedItems for the table, Run
// retries with just those write requests, up to batchMaxRetries times with
// a short linearly-growing backoff between attempts. If items are still
// unprocessed once retries are exhausted, Run returns an error.
func (b *BatchWriteBuilder[T]) Run() error {
	typeName := reflect.TypeFor[T]().Name()
	if b.err != nil {
		return fmt.Errorf("godynamo: batch write %s: %w", typeName, b.err)
	}
	if len(b.writes) == 0 {
		return nil
	}

	for start := 0; start < len(b.writes); start += batchWriteMaxRequests {
		end := min(start+batchWriteMaxRequests, len(b.writes))
		if err := b.runChunk(b.writes[start:end]); err != nil {
			return fmt.Errorf("godynamo: batch write %s: %w", typeName, err)
		}
	}
	return nil
}

// runChunk issues BatchWriteItem calls for one chunk of at most 25 write
// requests, retrying UnprocessedItems as described on
// [BatchWriteBuilder.Run].
func (b *BatchWriteBuilder[T]) runChunk(writes []types.WriteRequest) error {
	request := writes

	for attempt := 0; ; attempt++ {
		out, err := b.db.client.BatchWriteItem(b.ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{
				b.db.table: request,
			},
		})
		if err != nil {
			return err
		}

		unprocessed := out.UnprocessedItems[b.db.table]
		if len(unprocessed) == 0 {
			return nil
		}
		if attempt >= batchMaxRetries {
			return fmt.Errorf("%d write requests still unprocessed after %d retries", len(unprocessed), batchMaxRetries)
		}
		request = unprocessed
		time.Sleep(batchBackoff(attempt))
	}
}
