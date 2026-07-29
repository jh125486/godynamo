package godynamo

import "errors"

// ErrNotFound is returned (wrapped, with context) when a Get finds no item
// for the computed key.
var ErrNotFound = errors.New("godynamo: item not found")

// ErrOptimisticLock is returned (wrapped, with context) when a conditional
// write (Put on an existing item, or Update with IfVersion) fails its
// version check — i.e. DynamoDB reports a ConditionalCheckFailedException.
var ErrOptimisticLock = errors.New("godynamo: optimistic lock failed (version mismatch)")
