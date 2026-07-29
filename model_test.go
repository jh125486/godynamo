package godynamo_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jh125486/godynamo"
)

// User has no dynamo tag at all, exercising the all-defaults path.
type User struct {
	godynamo.Model
	Name string
}

func TestSetType(t *testing.T) {
	u := &User{Model: godynamo.Model{ID: uuid.New()}}
	godynamo.SetType(u)

	if got, want := u.Type, "User"; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
}

func TestSetType_PanicsOnNonPointer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for non-pointer argument")
		}
	}()
	godynamo.SetType(User{})
}

func TestSetType_PanicsWithoutModel(t *testing.T) {
	type NoModel struct {
		Name string
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for struct without embedded Model")
		}
	}()
	godynamo.SetType(&NoModel{})
}

func TestSetType_PanicsOnPointerToNonStruct(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for a pointer to a non-struct value")
		}
	}()
	var x int
	godynamo.SetType(&x)
}
