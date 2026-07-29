package godynamo

import (
	"context"
	"fmt"
	"reflect"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// StatementBuilder is a fluent builder for a raw PartiQL ExecuteStatement
// call, obtained via [Statement]. It is a raw-PartiQL escape hatch for
// callers who want SQL-like SELECT semantics, or a query shape the fluent
// [Query] builder can't express — the stmt string decides everything,
// including the table name, entirely by hand; StatementBuilder does not
// generate PartiQL from a model type, and it has no notion of [Query]'s
// type-index/base-table mode distinction.
//
// Only PartiQL SELECT (read) statements are supported. PartiQL write
// statements (INSERT/UPDATE/DELETE), BatchExecuteStatement, and
// ExecuteTransaction are all out of scope — see [BatchWrite] and
// [TransactWrite] for this package's non-PartiQL equivalents of batched
// writes and transactions.
//
// No I/O happens until a terminal method ([StatementBuilder.All] or
// [StatementBuilder.Page]) is called; both reuse the context captured by
// [Statement] rather than requiring it again.
type StatementBuilder[T any] struct {
	ctx  context.Context
	db   *DB
	stmt string

	params []types.AttributeValue
	err    error // captures an attributevalue.Marshal failure from Statement's params, surfaced lazily.

	limit          *int32
	consistentRead *bool
}

// Statement returns a fluent builder for running a raw PartiQL SELECT
// statement stmt (e.g. `SELECT * FROM "my-table" WHERE PK = ? AND SK = ?`)
// against DynamoDB and unmarshaling matching items into T. ctx is captured
// for use by the builder's terminal methods ([StatementBuilder.All],
// [StatementBuilder.Page]) — it is not re-passed to them.
//
// params are positional values for stmt's "?" placeholders, marshaled
// individually via attributevalue.Marshal (not MarshalMap — these are
// scalar/composite values, not a struct-to-map marshal) in the order given.
// If any param fails to marshal, the error is captured and returned lazily
// from the first terminal method call, rather than from Statement itself —
// see [BatchWriteBuilder.Put] for the same accumulate-then-check-at-Run()
// pattern.
//
// See [Query] for this package's fluent, non-PartiQL alternative — prefer
// it when its type-index/base-table query shapes suffice; reach for
// Statement when they don't.
func Statement[T any](ctx context.Context, db *DB, stmt string, params ...any) *StatementBuilder[T] {
	b := &StatementBuilder[T]{ctx: ctx, db: db, stmt: stmt}

	if len(params) > 0 {
		b.params = make([]types.AttributeValue, len(params))
		for i, p := range params {
			av, err := attributevalue.Marshal(p)
			if err != nil {
				b.err = fmt.Errorf("godynamo: marshaling statement parameter %d: %w", i, err)
				return b
			}
			b.params[i] = av
		}
	}
	return b
}

// Limit sets ExecuteStatementInput.Limit, a per-DynamoDB-request page-size
// hint. It does NOT cap the total results returned by [StatementBuilder.All],
// which paginates through everything regardless of Limit — it only affects
// page size / read-throughput per request. Same semantics as
// [QueryBuilder.Limit]/[ScanBuilder.Limit].
func (b *StatementBuilder[T]) Limit(n int32) *StatementBuilder[T] {
	b.limit = &n
	return b
}

// ConsistentRead sets ExecuteStatementInput.ConsistentRead. PartiQL's
// ExecuteStatement supports strongly-consistent reads directly — unlike
// this package's [Query]/[Scan], which don't currently expose this knob at
// all; ConsistentRead is PartiQL-specific to StatementBuilder.
func (b *StatementBuilder[T]) ConsistentRead(v bool) *StatementBuilder[T] {
	b.consistentRead = &v
	return b
}

// buildInput constructs the ExecuteStatementInput shared by All and Page:
// the statement, its parameters, and Limit/ConsistentRead as configured.
func (b *StatementBuilder[T]) buildInput() *dynamodb.ExecuteStatementInput {
	input := &dynamodb.ExecuteStatementInput{
		Statement:      aws.String(b.stmt),
		Parameters:     b.params,
		Limit:          b.limit,
		ConsistentRead: b.consistentRead,
	}
	return input
}

// All is a terminal method that runs the built statement to exhaustion,
// following NextToken until it's empty (empty/nil NextToken means no more
// pages — PartiQL's pagination token is a ready-to-use opaque string from
// AWS itself, so unlike [QueryBuilder.Page]'s hand-rolled cursor encoding,
// no cursor encoding/decoding is needed here). It returns every matched
// item unmarshaled into T. It uses the context captured by [Statement].
func (b *StatementBuilder[T]) All() ([]T, error) {
	if b.err != nil {
		return nil, b.err
	}
	typeName := reflect.TypeFor[T]().Name()
	input := b.buildInput()

	var items []T
	for {
		out, err := b.db.client.ExecuteStatement(b.ctx, input)
		if err != nil {
			return nil, fmt.Errorf("godynamo: execute statement %s: %w", typeName, err)
		}
		for _, av := range out.Items {
			var item T
			if err := unmarshalItemInto(av, &item); err != nil {
				return nil, fmt.Errorf("godynamo: unmarshal %s: %w", typeName, err)
			}
			items = append(items, item)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return items, nil
}

// Page is a terminal method that runs the built statement for exactly one
// ExecuteStatement call. If cursor is non-empty, it is passed straight
// through as ExecuteStatementInput.NextToken with no decoding — unlike
// [QueryBuilder.Page], AWS's token is already an opaque string safe to hand
// back to the caller as-is. It returns out.NextToken (dereferenced, or ""
// if nil) as nextCursor. It uses the context captured by [Statement].
func (b *StatementBuilder[T]) Page(cursor string) (items []T, nextCursor string, err error) {
	if b.err != nil {
		return nil, "", b.err
	}
	typeName := reflect.TypeFor[T]().Name()
	input := b.buildInput()
	if cursor != "" {
		input.NextToken = aws.String(cursor)
	}

	out, err := b.db.client.ExecuteStatement(b.ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("godynamo: execute statement %s: %w", typeName, err)
	}

	items = make([]T, 0, len(out.Items))
	for _, av := range out.Items {
		var item T
		if err := unmarshalItemInto(av, &item); err != nil {
			return nil, "", fmt.Errorf("godynamo: unmarshal %s: %w", typeName, err)
		}
		items = append(items, item)
	}

	if out.NextToken != nil {
		nextCursor = *out.NextToken
	}
	return items, nextCursor, nil
}
