package godynamo

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// TestNew_Defaults verifies New populates a *DB with the real client, table
// name, and the documented defaults (GSI1, time.Now, and an actor that
// always returns "") when no options are passed.
//
// dynamodb.New never makes a network call -- constructing a client is pure
// local setup -- so this is safe to do in a unit test with no mocking.
func TestNew_Defaults(t *testing.T) {
	client := dynamodb.New(dynamodb.Options{Region: "us-east-1"})

	db := New(client, "my-table")

	if db.client != client {
		t.Errorf("client = %v, want the *dynamodb.Client passed in", db.client)
	}
	if db.table != "my-table" {
		t.Errorf("table = %q, want %q", db.table, "my-table")
	}
	if db.gsi1Name != defaultGSI1Name {
		t.Errorf("gsi1Name = %q, want default %q", db.gsi1Name, defaultGSI1Name)
	}
	if db.clock == nil {
		t.Fatal("clock is nil, want time.Now by default")
	}
	before := time.Now()
	got := db.clock()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("clock() = %v, want a value between %v and %v (time.Now)", got, before, after)
	}
	if db.actor == nil {
		t.Fatal("actor is nil, want a default actor func")
	}
	if got := db.actor(context.Background()); got != "" {
		t.Errorf("actor() = %q, want empty string by default", got)
	}
}

// TestNew_WithOptions verifies WithGSI1Name/WithClock/WithActor override
// their respective defaults.
func TestNew_WithOptions(t *testing.T) {
	client := dynamodb.NewFromConfig(aws.Config{Region: "us-east-1"})

	fixed := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	db := New(client, "my-table",
		WithGSI1Name("CustomGSI"),
		WithClock(func() time.Time { return fixed }),
		WithActor(func(context.Context) string { return "svc-account" }),
	)

	if db.gsi1Name != "CustomGSI" {
		t.Errorf("gsi1Name = %q, want %q", db.gsi1Name, "CustomGSI")
	}
	if got := db.clock(); !got.Equal(fixed) {
		t.Errorf("clock() = %v, want %v", got, fixed)
	}
	if got := db.actor(context.Background()); got != "svc-account" {
		t.Errorf("actor() = %q, want %q", got, "svc-account")
	}
}
