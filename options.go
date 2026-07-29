package godynamo

import (
	"context"
	"time"
)

// Option configures a [DB] constructed by [New].
type Option func(*DB)

// WithGSI1Name overrides the default GSI1 index name ("GSI1"). It's used by
// [Query]'s type-index mode, which queries this GSI for GSI1PK = the Go type
// name of T.
func WithGSI1Name(name string) Option {
	return func(db *DB) {
		db.gsi1Name = name
	}
}

// WithGSI1PKAttr overrides the default attribute name for GSI1's partition
// key ("GSI1PK"). [Put] writes this attribute on new items, and
// [QueryBuilder]'s type-index mode reads it as the key condition's
// partition-key attribute; both use the same configured name.
func WithGSI1PKAttr(name string) Option {
	return func(db *DB) {
		db.gsi1PKAttr = name
	}
}

// WithClock overrides the function used to obtain the current time when
// stamping CreatedAt/UpdatedAt. Defaults to time.Now. Primarily useful for
// tests that need deterministic timestamps.
func WithClock(clock func() time.Time) Option {
	return func(db *DB) {
		db.clock = clock
	}
}

// WithActor overrides the function used to determine "who" is performing a
// write, for CreatedBy/UpdatedBy. Defaults to a function that always
// returns "". Since this package has no concept of auth, callers supply
// their own extraction of an actor identity from ctx.
func WithActor(actor func(ctx context.Context) string) Option {
	return func(db *DB) {
		db.actor = actor
	}
}
