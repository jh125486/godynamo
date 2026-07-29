// Package godynamo is an opinionated, fluent, generics-based library for
// modeling AWS DynamoDB single-table design in Go. It provides a common
// embeddable [Model] base struct, struct-tag driven partition/sort key
// generation, and generic CRUD/Query/Scan/Batch/Transact helpers built on
// top of the AWS SDK for Go v2.
package godynamo

import (
	"reflect"
	"time"

	"github.com/google/uuid"
)

// Model is the common base struct every DynamoDB-backed entity embeds. It
// carries the bookkeeping fields godynamo uses to build partition/sort keys
// and to support optimistic locking and auditing.
//
// Struct tag syntax: the `dynamo` tag, placed on the embedded Model field of
// the owning struct (not on Model's own fields), describes how the
// partition key (PK) and sort key (SK) are built. See tags.go and keys.go
// for the full syntax and templating rules.
type Model struct {
	// ID uniquely identifies the entity. It drives the default PK
	// (StructName#{ID}) and is always appended as the final SK component.
	ID uuid.UUID

	// Type is an entity discriminator, auto-set to the owning struct's type
	// name (e.g. "User"). It also doubles as the GSI1 partition key:
	// [stampAndMarshal] sets the "GSI1PK" attribute from it on create, which
	// is what [Query]'s type-index mode queries against.
	Type string

	// CreatedAt records when the entity was first persisted.
	CreatedAt time.Time
	// CreatedBy records who (or what) first persisted the entity.
	CreatedBy string

	// UpdatedAt records when the entity was last modified.
	UpdatedAt time.Time
	// UpdatedBy records who (or what) last modified the entity.
	UpdatedBy string

	// Version is an optimistic-locking counter. [Put] and [UpdateBuilder.Run]
	// set it to 1 on create and increment it on every subsequent write,
	// enforcing a Version = :expected ConditionExpression whenever the
	// caller opts into the check (Put's re-Put path, or Update's
	// [UpdateBuilder.IfVersion]).
	Version int
}

// SetType populates the Type field of v's embedded Model with the Go type
// name of v (e.g. "User" for a struct `type User struct { godynamo.Model }`).
//
// v must be a pointer to a struct that embeds Model (directly, by value),
// otherwise SetType panics. Passing a non-pointer or a struct without an
// embedded Model is a programmer error, not a runtime condition to recover
// from gracefully.
func SetType(v any) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		panic("godynamo: SetType requires a non-nil pointer to a struct embedding Model")
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		panic("godynamo: SetType requires a pointer to a struct")
	}

	m := elem.FieldByName("Model")
	if !m.IsValid() || m.Type() != reflect.TypeFor[Model]() {
		panic("godynamo: SetType requires a struct embedding godynamo.Model")
	}

	typeField := m.FieldByName("Type")
	typeField.SetString(elem.Type().Name())
}
