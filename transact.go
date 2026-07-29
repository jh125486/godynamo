package godynamo

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// transactMaxItems is AWS's hard per-call limit on the number of items a
// single TransactGetItems or TransactWriteItems request may carry.
const transactMaxItems = 100

// conditionalCheckFailedCode is the [types.CancellationReason.Code] value
// DynamoDB uses when a TransactWriteItems entry's ConditionExpression
// evaluates false.
const conditionalCheckFailedCode = "ConditionalCheckFailed"

// TransactGetBuilder is a fluent builder for a heterogeneous transactional
// read of up to 100 items — possibly of different Go types embedding
// [Model] — obtained via [TransactGet]. Unlike [BatchGetBuilder], it
// operates on any pointer to a struct embedding Model rather than a single
// generic T, since one DynamoDB transaction can span multiple item types in
// a single atomic call.
type TransactGetBuilder struct {
	ctx  context.Context
	db   *DB
	dsts []any
}

// TransactGet returns a fluent builder for a transactional multi-item get.
// ctx is captured for use by [TransactGetBuilder.Run] — it is not re-passed
// to it.
func TransactGet(ctx context.Context, db *DB) *TransactGetBuilder {
	return &TransactGetBuilder{ctx: ctx, db: db}
}

// Get queues dst, a pointer to a struct embedding [Model], to be fetched.
// dst's ID field (and any other fields its dynamo tag's pk:/sk: clauses
// reference) must already be populated by the caller — the key is computed
// via [PK](dst)/[SK](dst).
//
// This is an advantage over [Get][T]/[BatchGetBuilder]: PK/SK read dst's
// real field values, so — unlike the zero-value-plus-ID approach those use
// — Get here works correctly for ANY pk/sk tag configuration, not just
// models using the default (ID-only) PK/SK.
//
// On [TransactGetBuilder.Run], dst is unmarshaled in place with the
// fetched item's attributes; see Run's doc comment for what happens when
// the underlying key doesn't exist.
func (b *TransactGetBuilder) Get(dst any) *TransactGetBuilder {
	b.dsts = append(b.dsts, dst)
	return b
}

// Run issues a single TransactGetItems call covering every queued
// [TransactGetBuilder.Get], then unmarshals each result back into its
// corresponding dst pointer, in the order queued (dst is mutated in
// place).
//
// AWS caps TransactGetItems at 100 items per call, and the entire point of
// a transactional read is a single consistent snapshot — silently
// splitting the request across multiple calls would turn it into a
// non-atomic, inconsistent read. So if more than 100 Get calls have been
// queued, Run returns an error immediately WITHOUT calling
// TransactGetItems at all; it never auto-chunks.
//
// If a queued key doesn't exist, AWS returns an empty response slot for it
// (no error raised at the transaction level) — Run leaves that dst exactly
// as the caller passed it in (only whatever key fields they set before
// calling Get); it is not treated as a package-level error. Callers that
// need to detect "not found" for a specific queued item should check
// whether that dst's Model.CreatedAt (or another field they know should be
// non-zero once persisted) is still zero after Run returns.
func (b *TransactGetBuilder) Run() error {
	if len(b.dsts) == 0 {
		return nil
	}
	if len(b.dsts) > transactMaxItems {
		return fmt.Errorf(
			"godynamo: transact get: %d items queued, exceeds the %d-item TransactGetItems limit (no auto-chunking — would break atomicity)",
			len(b.dsts), transactMaxItems,
		)
	}

	transactItems := make([]types.TransactGetItem, len(b.dsts))
	for i, dst := range b.dsts {
		pk, sk := PK(dst), SK(dst)
		transactItems[i] = types.TransactGetItem{
			Get: &types.Get{
				TableName: aws.String(b.db.table),
				Key:       itemKey(pk, sk),
			},
		}
	}

	out, err := b.db.client.TransactGetItems(b.ctx, &dynamodb.TransactGetItemsInput{
		TransactItems: transactItems,
	})
	if err != nil {
		return fmt.Errorf("godynamo: transact get: %w", err)
	}

	for i := range b.dsts {
		if i >= len(out.Responses) {
			break
		}
		item := out.Responses[i].Item
		if len(item) == 0 {
			continue // no such key; leave dst untouched, per doc comment above.
		}
		if err := unmarshalItemInto(item, b.dsts[i]); err != nil {
			return fmt.Errorf("godynamo: transact get: unmarshal item %d: %w", i, err)
		}
	}
	return nil
}

// TransactWriteBuilder is a fluent builder for a heterogeneous
// transactional write of up to 100 combined Put/Delete/ConditionCheck
// entries — possibly of different Go types embedding [Model] — obtained
// via [TransactWrite]. Unlike [BatchWriteBuilder], it operates on any
// pointer to a struct embedding Model, since one DynamoDB transaction can
// span multiple item types in a single atomic call; and unlike
// [BatchWriteBuilder.Put], its Put preserves full optimistic-lock /
// create-vs-update conditional semantics, since TransactWriteItems (unlike
// BatchWriteItem) supports a per-item ConditionExpression.
type TransactWriteBuilder struct {
	ctx   context.Context
	db    *DB
	items []types.TransactWriteItem
	err   error
}

// TransactWrite returns a fluent builder for a transactional multi-item
// write. ctx is captured for use by [TransactWriteBuilder.Run] — it is not
// re-passed to it.
func TransactWrite(ctx context.Context, db *DB) *TransactWriteBuilder {
	return &TransactWriteBuilder{ctx: ctx, db: db}
}

// Put queues item, a pointer to a struct embedding [Model], to be created
// or updated as part of the transaction, using the identical
// create-vs-update audit-stamping logic as single-item [Put]: zero
// Model.CreatedAt means a new item ([SetType] is called, CreatedAt/
// CreatedBy/UpdatedAt/UpdatedBy are stamped, Version is set to 1, and the
// entry's ConditionExpression is attribute_not_exists(PK)); non-zero means
// an update (CreatedAt/CreatedBy are preserved, UpdatedAt/UpdatedBy are
// bumped, Version is incremented, and the entry's ConditionExpression is
// Version = :expected).
//
// UNLIKE [BatchWriteBuilder.Put], this condition IS attached to the queued
// entry — TransactWriteItems supports a per-item ConditionExpression on
// each types.Put, so full optimistic-lock protection is preserved, exactly
// as with single-item [Put].
func (b *TransactWriteBuilder) Put(item any) *TransactWriteBuilder {
	if b.err != nil {
		return b
	}
	av, cond, err := stampAndMarshal(b.ctx, b.db, item)
	if err != nil {
		b.err = err
		return b
	}
	expr, err := expression.NewBuilder().WithCondition(cond).Build()
	if err != nil {
		b.err = fmt.Errorf("godynamo: building put condition: %w", err)
		return b
	}
	b.items = append(b.items, types.TransactWriteItem{
		Put: &types.Put{
			TableName:                 aws.String(b.db.table),
			Item:                      av,
			ConditionExpression:       expr.Condition(),
			ExpressionAttributeNames:  expr.Names(),
			ExpressionAttributeValues: expr.Values(),
		},
	})
	return b
}

// Delete queues keyItem, a pointer to a struct embedding [Model] with ID
// (and any other fields its dynamo tag's pk:/sk: clauses reference)
// already set, to be deleted as part of the transaction. The key is
// computed via [PK]/[SK] — same as [TransactGetBuilder.Get]'s dst. No
// condition is attached.
func (b *TransactWriteBuilder) Delete(keyItem any) *TransactWriteBuilder {
	pk, sk := PK(keyItem), SK(keyItem)
	b.items = append(b.items, types.TransactWriteItem{
		Delete: &types.Delete{
			TableName: aws.String(b.db.table),
			Key:       itemKey(pk, sk),
		},
	})
	return b
}

// ConditionCheck queues a no-op check (no data is written) asserting that
// the item identified by keyItem's computed key (via [PK]/[SK], same as
// [TransactWriteBuilder.Delete]) has Version == expectedVersion. If the
// assertion fails, the entire transaction is canceled. This lets callers
// guard a write on some OTHER, unrelated item's version not having changed
// concurrently.
func (b *TransactWriteBuilder) ConditionCheck(keyItem any, expectedVersion int) *TransactWriteBuilder {
	if b.err != nil {
		return b
	}
	cond := expression.Name("Version").Equal(expression.Value(expectedVersion))
	expr, err := expression.NewBuilder().WithCondition(cond).Build()
	if err != nil {
		b.err = fmt.Errorf("godynamo: building condition check: %w", err)
		return b
	}
	pk, sk := PK(keyItem), SK(keyItem)
	b.items = append(b.items, types.TransactWriteItem{
		ConditionCheck: &types.ConditionCheck{
			TableName:                 aws.String(b.db.table),
			Key:                       itemKey(pk, sk),
			ConditionExpression:       expr.Condition(),
			ExpressionAttributeNames:  expr.Names(),
			ExpressionAttributeValues: expr.Values(),
		},
	})
	return b
}

// Run issues a single TransactWriteItems call covering every queued
// Put/Delete/ConditionCheck entry.
//
// AWS caps TransactWriteItems at 100 combined entries per call, and — as
// with [TransactGetBuilder.Run] — atomicity would be silently broken by
// chunking across multiple calls. So if more than 100 entries have been
// queued, Run returns an error immediately WITHOUT calling
// TransactWriteItems at all; it never auto-chunks.
//
// A failed condition (from a Put's implicit condition, or an explicit
// ConditionCheck) surfaces from the AWS SDK as a
// *types.TransactionCanceledException — NOT the single-item
// *types.ConditionalCheckFailedException [Put]/[UpdateBuilder.Run]
// translate — carrying a CancellationReasons slice with one entry per
// queued item. Run inspects CancellationReasons: if any entry's Code is
// "ConditionalCheckFailed", Run returns an error wrapping
// [ErrOptimisticLock] (detectable with errors.Is). Other cancellation
// reasons are wrapped with the cancellation codes for context but are NOT
// mapped to ErrOptimisticLock.
func (b *TransactWriteBuilder) Run() error {
	if b.err != nil {
		return fmt.Errorf("godynamo: transact write: %w", b.err)
	}
	if len(b.items) == 0 {
		return nil
	}
	if len(b.items) > transactMaxItems {
		return fmt.Errorf(
			"godynamo: transact write: %d entries queued, exceeds the %d-entry TransactWriteItems limit (no auto-chunking — would break atomicity)",
			len(b.items), transactMaxItems,
		)
	}

	_, err := b.db.client.TransactWriteItems(b.ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: b.items,
	})
	if err != nil {
		return translateTransactWriteErr(err)
	}
	return nil
}

// translateTransactWriteErr inspects err for a
// *types.TransactionCanceledException with a "ConditionalCheckFailed"
// cancellation reason, translating it into an error wrapping
// [ErrOptimisticLock]. Other errors — including
// TransactionCanceledExceptions whose cancellation reasons don't include a
// conditional-check failure — are wrapped with context but not mapped to
// ErrOptimisticLock.
func translateTransactWriteErr(err error) error {
	if txErr, ok := errors.AsType[*types.TransactionCanceledException](err); ok {
		codes := cancellationReasonCodes(txErr.CancellationReasons)
		for _, reason := range txErr.CancellationReasons {
			if reason.Code != nil && *reason.Code == conditionalCheckFailedCode {
				return fmt.Errorf("godynamo: transact write (reasons=%v): %w", codes, ErrOptimisticLock)
			}
		}
		return fmt.Errorf("godynamo: transact write: transaction canceled (reasons=%v): %w", codes, err)
	}
	return fmt.Errorf("godynamo: transact write: %w", err)
}

// cancellationReasonCodes extracts the Code of each cancellation reason
// (or "None" for a nil Code, matching DynamoDB's own convention for
// unaffected items) for inclusion in error messages.
func cancellationReasonCodes(reasons []types.CancellationReason) []string {
	codes := make([]string, len(reasons))
	for i, r := range reasons {
		if r.Code != nil {
			codes[i] = *r.Code
		} else {
			codes[i] = "None"
		}
	}
	return codes
}
