package godynamo

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// ScanBuilder is a fluent builder for a DynamoDB Scan, obtained via [Scan].
//
// Unlike [QueryBuilder], Scan always targets the base table directly — no
// IndexName is ever set on the built ScanInput, so scanning a GSI is not
// supported.
//
// [ScanBuilder.Filter] and its operator-specific siblings (FilterNotEqual,
// FilterGreaterThan, FilterBeginsWith, ...) optionally AND FilterExpression
// clauses on non-key attributes, using the same semantics as
// [QueryBuilder.Filter] and friends. [ScanBuilder.Parallel] optionally
// enables a parallel scan across multiple DynamoDB scan segments.
//
// No I/O happens until the terminal method ([ScanBuilder.All]) is called;
// ctx is required directly on that call, not captured at construction.
//
// There is intentionally no .Page(cursor) method here, unlike
// [QueryBuilder.Page]: a single opaque cursor doesn't compose cleanly with
// parallel segments' independent LastEvaluatedKey chains, so per-page
// pagination is not supported — not an oversight.
type ScanBuilder[T any] struct {
	db *DB

	filters       []filterCondition
	limit         *int32
	totalSegments int // <= 1 => single-segment scan.
}

// Scan returns a fluent builder for scanning the base table for items of
// type T. No I/O happens until the terminal method ([ScanBuilder.All]) is
// called, so Scan itself takes no context.Context — ctx is required
// directly on All instead.
func Scan[T any](db *DB) *ScanBuilder[T] {
	return &ScanBuilder[T]{db: db}
}

// Filter adds an equality FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. It is shorthand
// for [ScanBuilder.FilterEqual] — the two are identical. Same semantics as
// [QueryBuilder.Filter], including field-name resolution (see
// [resolveAttrName]).
func (b *ScanBuilder[T]) Filter(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterEqual, field, value)
}

// FilterEqual adds an "=" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Identical to
// [ScanBuilder.Filter]. Same semantics as [QueryBuilder.FilterEqual].
func (b *ScanBuilder[T]) FilterEqual(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterEqual, field, value)
}

// FilterNotEqual adds a "<>" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Same semantics
// as [QueryBuilder.FilterNotEqual].
func (b *ScanBuilder[T]) FilterNotEqual(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterNotEqual, field, value)
}

// FilterGreaterThan adds a ">" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Same semantics
// as [QueryBuilder.FilterGreaterThan].
func (b *ScanBuilder[T]) FilterGreaterThan(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterGreaterThan, field, value)
}

// FilterGreaterOrEqual adds a ">=" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Same semantics
// as [QueryBuilder.FilterGreaterOrEqual].
func (b *ScanBuilder[T]) FilterGreaterOrEqual(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterGreaterOrEqual, field, value)
}

// FilterLessThan adds a "<" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Same semantics
// as [QueryBuilder.FilterLessThan].
func (b *ScanBuilder[T]) FilterLessThan(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterLessThan, field, value)
}

// FilterLessOrEqual adds a "<=" FilterExpression condition on a non-key
// attribute, ANDed with any previous Filter/Filter* calls. Same semantics
// as [QueryBuilder.FilterLessOrEqual].
func (b *ScanBuilder[T]) FilterLessOrEqual(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterLessOrEqual, field, value)
}

// FilterBeginsWith adds a begins_with(field, prefix) FilterExpression
// condition on a non-key attribute, ANDed with any previous Filter/Filter*
// calls. Same semantics as [QueryBuilder.FilterBeginsWith].
func (b *ScanBuilder[T]) FilterBeginsWith(field, prefix string) *ScanBuilder[T] {
	return b.addFilter(filterBeginsWith, field, prefix)
}

// FilterContains adds a contains(field, value) FilterExpression condition
// on a non-key attribute, ANDed with any previous Filter/Filter* calls.
// Same semantics as [QueryBuilder.FilterContains].
func (b *ScanBuilder[T]) FilterContains(field string, value any) *ScanBuilder[T] {
	return b.addFilter(filterContains, field, value)
}

// FilterExists adds an attribute_exists(field) FilterExpression condition
// on a non-key attribute, ANDed with any previous Filter/Filter* calls.
// Same semantics as [QueryBuilder.FilterExists].
func (b *ScanBuilder[T]) FilterExists(field string) *ScanBuilder[T] {
	return b.addFilter(filterExists, field, nil)
}

// FilterNotExists adds an attribute_not_exists(field) FilterExpression
// condition on a non-key attribute, ANDed with any previous Filter/Filter*
// calls. Same semantics as [QueryBuilder.FilterNotExists].
func (b *ScanBuilder[T]) FilterNotExists(field string) *ScanBuilder[T] {
	return b.addFilter(filterNotExists, field, nil)
}

// addFilter resolves field (a Go field name on T) to its DynamoDB
// attribute name and queues a filterCondition for it. Shared by
// [ScanBuilder.Filter] and all its operator-specific siblings.
func (b *ScanBuilder[T]) addFilter(op filterOp, field string, value any) *ScanBuilder[T] {
	attr := resolveAttrName(reflect.TypeFor[T](), field)
	b.filters = append(b.filters, filterCondition{op: op, field: attr, value: value})
	return b
}

// Limit sets ScanInput.Limit, a per-DynamoDB-request page-size hint. It
// does NOT cap the total results returned by [ScanBuilder.All], which
// paginates through everything regardless of Limit — it only affects page
// size / read-throughput per request. Same semantics as [QueryBuilder.Limit].
func (b *ScanBuilder[T]) Limit(n int32) *ScanBuilder[T] {
	b.limit = &n
	return b
}

// Parallel enables a parallel scan across totalSegments DynamoDB scan
// segments: [ScanBuilder.All] launches totalSegments goroutines, each
// running its own full paginated Scan loop for one segment (Segment: i,
// TotalSegments: totalSegments on every ScanInput that segment issues).
// totalSegments <= 1 (or never calling Parallel) means a normal
// single-segment scan.
func (b *ScanBuilder[T]) Parallel(totalSegments int) *ScanBuilder[T] {
	b.totalSegments = totalSegments
	return b
}

// buildInput constructs the ScanInput for one page of one segment: any
// Filter conditions and Limit as configured, plus Segment/TotalSegments
// when running in parallel mode (b.totalSegments > 1). segment is ignored
// otherwise. IndexName is never set — Scan always targets the base table.
func (b *ScanBuilder[T]) buildInput(segment int) (*dynamodb.ScanInput, error) {
	input := &dynamodb.ScanInput{
		TableName: aws.String(b.db.table),
	}

	if len(b.filters) > 0 {
		expr, err := expression.NewBuilder().WithFilter(filterExpression(b.filters)).Build()
		if err != nil {
			return nil, fmt.Errorf("godynamo: building scan expression: %w", err)
		}
		input.FilterExpression = expr.Filter()
		input.ExpressionAttributeNames = expr.Names()
		input.ExpressionAttributeValues = expr.Values()
	}

	if b.limit != nil {
		input.Limit = b.limit
	}
	if b.totalSegments > 1 {
		if b.totalSegments > math.MaxInt32 {
			return nil, fmt.Errorf("godynamo: scan: totalSegments %d exceeds int32 range", b.totalSegments)
		}
		// segment is always in [0, b.totalSegments), and b.totalSegments was
		// just bounds-checked above, so both conversions below are safe.
		seg := int32(segment) //nolint:gosec // bounds-checked above
		total := int32(b.totalSegments)
		input.Segment = &seg
		input.TotalSegments = &total
	}
	return input, nil
}

// All is a terminal method that runs the built scan to exhaustion and
// returns every matched item unmarshaled into T.
//
// Single-segment mode (the default — [ScanBuilder.Parallel] never called,
// or called with totalSegments <= 1): All loops issuing Scan calls,
// following LastEvaluatedKey → ExclusiveStartKey until exhausted, identical
// to [QueryBuilder.All]'s pagination pattern.
//
// Parallel mode ([ScanBuilder.Parallel](n) with n > 1): All launches n
// goroutines, one per segment, each running its own full paginated scan
// loop to exhaustion (a single segment may span multiple pages, each
// following that segment's own LastEvaluatedKey chain), all sharing the
// same ctx. Results from all segments are merged into one slice in no
// particular order.
//
// Error handling in parallel mode: if any segment's Scan call returns an
// error, All returns (nil, err) — it always waits for every goroutine to
// finish first (no goroutine is leaked), but does not proactively cancel
// sibling segments still in flight. If more than one segment errors, All
// may return any one of them (whichever is recorded first under its
// internal mutex); which one is unspecified.
func (b *ScanBuilder[T]) All(ctx context.Context) ([]T, error) {
	if b.totalSegments > 1 {
		return b.allParallel(ctx)
	}
	return b.scanSegment(ctx, 0)
}

// scanSegment runs one segment's Scan loop to exhaustion (following
// LastEvaluatedKey until exhausted) and unmarshals every returned item into
// T. segment is meaningless (ignored by buildInput) unless b.totalSegments >
// 1. It is called both from [ScanBuilder.All]'s single-segment path and
// concurrently (once per goroutine, sharing the same ctx) from
// [ScanBuilder.allParallel].
func (b *ScanBuilder[T]) scanSegment(ctx context.Context, segment int) ([]T, error) {
	typeName := reflect.TypeFor[T]().Name()
	input, err := b.buildInput(segment)
	if err != nil {
		return nil, err
	}

	var items []T
	for {
		out, err := b.db.client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("godynamo: scan %s: %w", typeName, err)
		}
		for _, av := range out.Items {
			var item T
			if err := unmarshalItemInto(av, &item); err != nil {
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

// allParallel implements the totalSegments > 1 branch of All. See All's doc
// comment for the concurrency and error-handling contract; it only uses the
// standard library (sync.WaitGroup + a sync.Mutex-guarded slice/error) — no
// golang.org/x/sync dependency. Every goroutine shares the same ctx, passed
// through explicitly to [ScanBuilder.scanSegment] rather than read from a
// struct field, so there's no data race on ctx itself (it's an immutable
// interface value, safe to read concurrently from multiple goroutines).
func (b *ScanBuilder[T]) allParallel(ctx context.Context) ([]T, error) {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		items    []T
		firstErr error
	)

	for segment := range b.totalSegments {
		wg.Add(1)
		go func(segment int) {
			defer wg.Done()
			segItems, err := b.scanSegment(ctx, segment)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			items = append(items, segItems...)
		}(segment)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return items, nil
}
