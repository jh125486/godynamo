package godynamo

import (
	"context"
	"time"
)

// Option configures a [DB] constructed by [New].
type Option func(*DB)

// WithGSI1Name overrides the default GSI1 index name ("GSI1"). Later phases
// use this for queries; Phase 2 only carries the config.
func WithGSI1Name(name string) Option {
	return func(db *DB) {
		db.gsi1Name = name
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
