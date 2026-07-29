package godynamo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// skConditionKind identifies which sort-key condition a [QueryBuilder] has
// queued.
type skConditionKind int

const (
	skNone skConditionKind = iota
	skEquals
	skBeginsWith
	skBetween
)

// skCondition is the queued sort-key condition on a [QueryBuilder]. Only one
// may be active at a time; see [QueryBuilder.SKEquals].
type skCondition struct {
	kind   skConditionKind
	lo, hi string // hi is only used for skBetween
}

// filterOp identifies which comparison operator a queued [filterCondition]
// uses when [filterExpression] builds it into an
// expression.ConditionBuilder.
type filterOp int

const (
	filterEqual filterOp = iota
	filterNotEqual
	filterGreaterThan
	filterGreaterOrEqual
	filterLessThan
	filterLessOrEqual
	filterBeginsWith
	filterContains
	filterExists
	filterNotExists
)

// filterCondition is a single queued [QueryBuilder.Filter] (or
// [ScanBuilder.Filter], or one of their operator-specific siblings —
// FilterNotEqual, FilterGreaterThan, etc.) clause. value is unused for
// filterExists/filterNotExists.
type filterCondition struct {
	op    filterOp
	field string
	value any
}

// buildCondition builds the expression.ConditionBuilder for a single
// filterCondition, dispatching on f.op.
func (f filterCondition) buildCondition() expression.ConditionBuilder {
	name := expression.Name(f.field)
	switch f.op {
	case filterEqual:
		return name.Equal(expression.Value(f.value))
	case filterNotEqual:
		return name.NotEqual(expression.Value(f.value))
	case filterGreaterThan:
		return name.GreaterThan(expression.Value(f.value))
	case filterGreaterOrEqual:
		return name.GreaterThanEqual(expression.Value(f.value))
	case filterLessThan:
		return name.LessThan(expression.Value(f.value))
	case filterLessOrEqual:
		return name.LessThanEqual(expression.Value(f.value))
	case filterBeginsWith:
		prefix, _ := f.value.(string)
		return name.BeginsWith(prefix)
	case filterContains:
		return name.Contains(f.value)
	case filterExists:
		return name.AttributeExists()
	case filterNotExists:
		return name.AttributeNotExists()
	default:
		// Unreachable: every filterOp constant is handled above, and
		// filterCondition values are only ever constructed by this
		// package's own Filter*/FilterNotEqual/... methods.
		panic(fmt.Sprintf("godynamo: unknown filterOp %d", f.op))
	}
}

// filterExpression ANDs together a condition for each filter in filters, in
// order: filters[0]'s condition AND filters[1]'s condition AND ... Shared
// by [QueryBuilder.buildInput] and [ScanBuilder.buildInput].
//
// filters must be non-empty — callers are expected to check len(filters) > 0
// before calling this (a zero-value expression.ConditionBuilder is not a
// valid filter condition to hand to WithFilter).
func filterExpression(filters []filterCondition) expression.ConditionBuilder {
	cond := filters[0].buildCondition()
	for _, f := range filters[1:] {
		cond = cond.And(f.buildCondition())
	}
	return cond
}

// QueryBuilder is a fluent builder for a DynamoDB Query, obtained via
// [Query]. It operates in one of two modes:
//
//   - Type-index mode (the default): queries the GSI1 index (or whichever
//     index [QueryBuilder.Index] selects) for db.gsi1PKAttr (default
//     "GSI1PK", configurable via [WithGSI1PKAttr]) = the Go type name of
//     T, i.e. every item of type T in the table.
//   - Base-table mode: enabled by calling [QueryBuilder.WherePK], queries
//     the base table for PK = the given partition key.
//
// In either mode, [QueryBuilder.SKEquals]/[QueryBuilder.SKBeginsWith]/
// [QueryBuilder.SKBetween] optionally AND a condition on the "SK" attribute
// (the base table's sort key, and — in type-index mode — the GSI1 index's
// reused range key), and [QueryBuilder.Filter] and its operator-specific
// siblings (FilterNotEqual, FilterGreaterThan, FilterBeginsWith, ...)
// optionally AND FilterExpression clauses on non-key attributes.
//
// No I/O happens until a terminal method ([QueryBuilder.All] or
// [QueryBuilder.Page]) is called; both reuse the context captured by
// [Query] rather than requiring it again.
type QueryBuilder[T any] struct {
	ctx context.Context
	db  *DB

	pk      *string // nil => type-index mode; non-nil => base-table mode, value is the PK.
	sk      skCondition
	filters []filterCondition
	index   string // GSI name override for type-index mode; "" => db.gsi1Name.
	limit   *int32
}

// Query returns a fluent builder for querying items of type T. ctx is
// captured for use by the builder's terminal methods ([QueryBuilder.All],
// [QueryBuilder.Page]) — it is not re-passed to them.
func Query[T any](ctx context.Context, db *DB) *QueryBuilder[T] {
	return &QueryBuilder[T]{ctx: ctx, db: db}
}

// WherePK switches the builder into base-table query mode, with an
// exact-match condition against the base table's "PK" attribute. pk is a
// raw string the caller builds however it likes — e.g. via [PK] applied to
// a representative struct value, or a literal.
func (b *QueryBuilder[T]) WherePK(pk string) *QueryBuilder[T] {
	b.pk = &pk
	return b
}

// SKEquals sets an exact-match condition on the "SK" attribute, ANDed with
// the partition-key condition. Only one of SKEquals/SKBeginsWith/SKBetween
// may be active at a time; the most recently called one wins.
func (b *QueryBuilder[T]) SKEquals(sk string) *QueryBuilder[T] {
	b.sk = skCondition{kind: skEquals, lo: sk}
	return b
}

// SKBeginsWith sets a begins_with condition on the "SK" attribute, ANDed
// with the partition-key condition. Only one of
// SKEquals/SKBeginsWith/SKBetween may be active at a time; the most
// recently called one wins.
func (b *QueryBuilder[T]) SKBeginsWith(prefix string) *QueryBuilder[T] {
	b.sk = skCondition{kind: skBeginsWith, lo: prefix}
	return b
}

// SKBetween sets a between(lo, hi) condition (inclusive) on the "SK"
// attribute, ANDed with the partition-key condition. Only one of
// SKEquals/SKBeginsWith/SKBetween may be active at a time; the most
// recently called one wins.
func (b *QueryBuilder[T]) SKBetween(lo, hi string) *QueryBuilder[T] {
	b.sk = skCondition{kind: skBetween, lo: lo, hi: hi}
	return b
}

// Filter adds an equality FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. It is shorthand
// for [QueryBuilder.FilterEqual] — the two are identical. field is a Go
// field name on T (not necessarily its DynamoDB attribute name — a
// `dynamodbav:"..."` struct tag override, if present, is resolved
// automatically; see [resolveAttrName]).
func (b *QueryBuilder[T]) Filter(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterEqual, field, value)
}

// FilterEqual adds an "=" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Identical to
// [QueryBuilder.Filter]. See [QueryBuilder.Filter] for the field-name
// resolution rule.
func (b *QueryBuilder[T]) FilterEqual(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterEqual, field, value)
}

// FilterNotEqual adds a "<>" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterNotEqual(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterNotEqual, field, value)
}

// FilterGreaterThan adds a ">" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterGreaterThan(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterGreaterThan, field, value)
}

// FilterGreaterOrEqual adds a ">=" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterGreaterOrEqual(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterGreaterOrEqual, field, value)
}

// FilterLessThan adds a "<" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterLessThan(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterLessThan, field, value)
}

// FilterLessOrEqual adds a "<=" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterLessOrEqual(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterLessOrEqual, field, value)
}

// FilterBeginsWith adds a begins_with(field, prefix) FilterExpression
// condition on a non-key attribute, ANDed with any previous Filter/Filter*
// calls. See [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterBeginsWith(field, prefix string) *QueryBuilder[T] {
	return b.addFilter(filterBeginsWith, field, prefix)
}

// FilterContains adds a contains(field, value) FilterExpression condition
// on a non-key attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterContains(field string, value any) *QueryBuilder[T] {
	return b.addFilter(filterContains, field, value)
}

// FilterExists adds an attribute_exists(field) FilterExpression condition
// on a non-key attribute, ANDed with any previous Filter/Filter* calls. See
// [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterExists(field string) *QueryBuilder[T] {
	return b.addFilter(filterExists, field, nil)
}

// FilterNotExists adds an attribute_not_exists(field) FilterExpression
// condition on a non-key attribute, ANDed with any previous Filter/Filter*
// calls. See [QueryBuilder.Filter] for the field-name resolution rule.
func (b *QueryBuilder[T]) FilterNotExists(field string) *QueryBuilder[T] {
	return b.addFilter(filterNotExists, field, nil)
}

// addFilter resolves field (a Go field name on T) to its DynamoDB
// attribute name and queues a filterCondition for it. Shared by
// [QueryBuilder.Filter] and all its operator-specific siblings.
func (b *QueryBuilder[T]) addFilter(op filterOp, field string, value any) *QueryBuilder[T] {
	attr := resolveAttrName(reflect.TypeFor[T](), field)
	b.filters = append(b.filters, filterCondition{op: op, field: attr, value: value})
	return b
}

// Index overrides which GSI name is used in type-index mode (default
// db.gsi1Name). It has no effect in base-table mode (after [QueryBuilder.WherePK]).
// This does not provide general arbitrary-index querying — that is out of
// scope for this phase and left to a future generalization.
func (b *QueryBuilder[T]) Index(name string) *QueryBuilder[T] {
	b.index = name
	return b
}

// Limit sets QueryInput.Limit, a per-DynamoDB-request page-size hint. It
// does NOT cap the total results returned by [QueryBuilder.All], which
// paginates through everything regardless of Limit — it only affects page
// size / read-throughput per request. Callers who want a hard cap on total
// results should use [QueryBuilder.Page] themselves.
func (b *QueryBuilder[T]) Limit(n int32) *QueryBuilder[T] {
	b.limit = &n
	return b
}

// buildInput constructs the QueryInput shared by All and Page: the
// key condition (base-table or type-index mode), any SK condition, any
// Filter conditions, and IndexName/Limit as configured.
func (b *QueryBuilder[T]) buildInput() (*dynamodb.QueryInput, error) {
	var keyCond expression.KeyConditionBuilder
	var indexName *string

	if b.pk != nil {
		keyCond = expression.Key("PK").Equal(expression.Value(*b.pk))
	} else {
		typeName := reflect.TypeFor[T]().Name()
		idx := b.db.gsi1Name
		if b.index != "" {
			idx = b.index
		}
		indexName = aws.String(idx)
		keyCond = expression.Key(b.db.gsi1PKAttr).Equal(expression.Value(typeName))
	}

	switch b.sk.kind {
	case skNone:
		// No SK condition queued; keyCond stays as the PK-only condition.
	case skEquals:
		keyCond = keyCond.And(expression.Key("SK").Equal(expression.Value(b.sk.lo)))
	case skBeginsWith:
		keyCond = keyCond.And(expression.Key("SK").BeginsWith(b.sk.lo))
	case skBetween:
		keyCond = keyCond.And(expression.Key("SK").Between(expression.Value(b.sk.lo), expression.Value(b.sk.hi)))
	}

	eb := expression.NewBuilder().WithKeyCondition(keyCond)

	if len(b.filters) > 0 {
		eb = eb.WithFilter(filterExpression(b.filters))
	}

	expr, err := eb.Build()
	if err != nil {
		return nil, fmt.Errorf("godynamo: building query expression: %w", err)
	}

	input := &dynamodb.QueryInput{
		TableName:                 aws.String(b.db.table),
		IndexName:                 indexName,
		KeyConditionExpression:    expr.KeyCondition(),
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}
	if b.limit != nil {
		input.Limit = b.limit
	}
	return input, nil
}

// All is a terminal method that runs the built query to exhaustion,
// following LastEvaluatedKey → ExclusiveStartKey until every page has been
// fetched, and returns every matched item unmarshaled into T. It uses the
// context captured by [Query].
func (b *QueryBuilder[T]) All() ([]T, error) {
	typeName := reflect.TypeFor[T]().Name()
	input, err := b.buildInput()
	if err != nil {
		return nil, err
	}

	var items []T
	for {
		out, err := b.db.client.Query(b.ctx, input)
		if err != nil {
			return nil, fmt.Errorf("godynamo: query %s: %w", typeName, err)
		}
		for _, av := range out.Items {
			var item T
			if err := attributevalue.UnmarshalMap(av, &item); err != nil {
				return nil, fmt.Errorf("godynamo: unmarshal %s: %w", typeName, err)
			}
			items = append(items, item)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return items, nil
}

// Page is a terminal method that runs the built query for exactly one
// DynamoDB Query call. If cursor is non-empty it is decoded and used as
// ExclusiveStartKey. If the response has a LastEvaluatedKey, nextCursor is
// its encoded form; otherwise nextCursor is "" (no more pages). It uses the
// context captured by [Query].
func (b *QueryBuilder[T]) Page(cursor string) (items []T, nextCursor string, err error) {
	typeName := reflect.TypeFor[T]().Name()
	input, err := b.buildInput()
	if err != nil {
		return nil, "", err
	}
	if cursor != "" {
		key, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("godynamo: decoding cursor: %w", err)
		}
		input.ExclusiveStartKey = key
	}

	out, err := b.db.client.Query(b.ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("godynamo: query %s: %w", typeName, err)
	}

	items = make([]T, 0, len(out.Items))
	for _, av := range out.Items {
		var item T
		if err := attributevalue.UnmarshalMap(av, &item); err != nil {
			return nil, "", fmt.Errorf("godynamo: unmarshal %s: %w", typeName, err)
		}
		items = append(items, item)
	}

	if len(out.LastEvaluatedKey) > 0 {
		nextCursor, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("godynamo: encoding cursor: %w", err)
		}
	}
	return items, nextCursor, nil
}

// encodeCursor turns a DynamoDB LastEvaluatedKey into an opaque, URL-safe
// string: the key is unmarshaled into a JSON-safe map[string]any,
// JSON-marshaled, then base64 (URL-safe, no padding) encoded.
func encodeCursor(key map[string]types.AttributeValue) (string, error) {
	m := make(map[string]any, len(key))
	if err := attributevalue.UnmarshalMap(key, &m); err != nil {
		return "", fmt.Errorf("godynamo: unmarshaling cursor key: %w", err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("godynamo: marshaling cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor reverses [encodeCursor]. It returns a clear error (never
// panics) if cursor is malformed, since cursors cross a trust boundary —
// they may be round-tripped through a URL or an untrusted client.
func decodeCursor(cursor string) (map[string]types.AttributeValue, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("godynamo: invalid cursor encoding: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("godynamo: invalid cursor payload: %w", err)
	}
	key, err := attributevalue.MarshalMap(m)
	if err != nil {
		return nil, fmt.Errorf("godynamo: marshaling cursor key: %w", err)
	}
	return key, nil
}
