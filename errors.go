package godynamo

import "errors"

// ErrNotFound is returned (wrapped, with context) when a Get finds no item
// for the computed key.
var ErrNotFound = errors.New("godynamo: item not found")

// ErrOptimisticLock is returned (wrapped, with context) when a conditional
// write (Put on an existing item, or Update with IfVersion) fails its
// version check — i.e. DynamoDB reports a ConditionalCheckFailedException.
var ErrOptimisticLock = errors.New("godynamo: optimistic lock failed (version mismatch)")

// ErrNonDefaultKey is returned (wrapped, with context) by [Get], [Delete],
// [UpdateBuilder.Run], [BatchGetBuilder.Run], and [BatchWriteBuilder.Delete]
// when called for a type T whose dynamo tag has a pk:/sk: clause referencing
// fields other than T's default (ID-only) key. Those functions compute
// their DynamoDB key from a zero-valued T with only Model.ID populated,
// which is only correct when the key is derived solely from ID — for models
// with a tag-templated key, use [Query] or [TransactGet]/[TransactWrite]
// instead, both of which key off caller-populated field values.
var ErrNonDefaultKey = errors.New("godynamo: T requires the default (ID-only) PK/SK")
