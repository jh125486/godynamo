package godynamo_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jh125486/godynamo"
)

// NoTags has neither a pk nor sk clause: everything defaults to
// "StructName#{ID}".
type NoTags struct {
	godynamo.Model
	Name string
}

// SinglePK has a pk clause with one field, no sk clause.
type SinglePK struct {
	godynamo.Model `dynamo:"pk:Tenant"`
	Tenant         string
}

// MultiPK has a pk clause with multiple fields, no sk clause.
type MultiPK struct {
	godynamo.Model `dynamo:"pk:Tenant,Region"`
	Tenant         string
	Region         string
}

// SingleSK has an sk clause with one field, no pk clause.
type SingleSK struct {
	godynamo.Model `dynamo:"sk:Status"`
	Status         string
}

// MultiSK has an sk clause with multiple fields, no pk clause.
type MultiSK struct {
	godynamo.Model `dynamo:"sk:Status,CreatedDate"`
	Status         string
	CreatedDate    string
}

// SKWithExplicitID lists ID as the last sk field explicitly; it must not be
// double-appended.
type SKWithExplicitID struct {
	godynamo.Model `dynamo:"sk:Status,ID"`
	Status         string
}

// Order has both pk and sk clauses together, matching the design sketch.
type Order struct {
	godynamo.Model `dynamo:"pk:CustomerID;sk:Status,CreatedDate"`
	CustomerID     string
	Status         string
	CreatedDate    string
}

func TestPK_Default(t *testing.T) {
	id := uuid.New()
	v := &NoTags{Model: godynamo.Model{ID: id}}

	want := fmt.Sprintf("NoTags#%s", id)
	if got := godynamo.PK(v); got != want {
		t.Fatalf("PK = %q, want %q", got, want)
	}
}

func TestSK_Default(t *testing.T) {
	id := uuid.New()
	v := &NoTags{Model: godynamo.Model{ID: id}}

	want := fmt.Sprintf("NoTags#%s", id)
	if got := godynamo.SK(v); got != want {
		t.Fatalf("SK = %q, want %q", got, want)
	}
}

func TestPK_SingleField(t *testing.T) {
	v := &SinglePK{Tenant: "acme"}

	want := "SinglePK#acme"
	if got := godynamo.PK(v); got != want {
		t.Fatalf("PK = %q, want %q", got, want)
	}
}

func TestPK_MultiField(t *testing.T) {
	v := &MultiPK{Tenant: "acme", Region: "us-east-1"}

	want := "MultiPK#acme#us-east-1"
	if got := godynamo.PK(v); got != want {
		t.Fatalf("PK = %q, want %q", got, want)
	}
}

func TestSK_SingleFieldAppendsID(t *testing.T) {
	id := uuid.New()
	v := &SingleSK{Model: godynamo.Model{ID: id}, Status: "open"}

	want := fmt.Sprintf("SingleSK#open#%s", id)
	if got := godynamo.SK(v); got != want {
		t.Fatalf("SK = %q, want %q", got, want)
	}
}

func TestSK_MultiFieldAppendsID(t *testing.T) {
	id := uuid.New()
	v := &MultiSK{
		Model:       godynamo.Model{ID: id},
		Status:      "open",
		CreatedDate: "2026-07-28",
	}

	want := fmt.Sprintf("MultiSK#open#2026-07-28#%s", id)
	if got := godynamo.SK(v); got != want {
		t.Fatalf("SK = %q, want %q", got, want)
	}
}

func TestSK_ExplicitIDNotDoubleAppended(t *testing.T) {
	id := uuid.New()
	v := &SKWithExplicitID{Model: godynamo.Model{ID: id}, Status: "open"}

	want := fmt.Sprintf("SKWithExplicitID#open#%s", id)
	if got := godynamo.SK(v); got != want {
		t.Fatalf("SK = %q, want %q", got, want)
	}
}

func TestPKAndSK_Together(t *testing.T) {
	id := uuid.New()
	v := &Order{
		Model:       godynamo.Model{ID: id},
		CustomerID:  "cust-1",
		Status:      "shipped",
		CreatedDate: "2026-07-28",
	}

	wantPK := "Order#cust-1"
	wantSK := fmt.Sprintf("Order#shipped#2026-07-28#%s", id)

	if got := godynamo.PK(v); got != wantPK {
		t.Fatalf("PK = %q, want %q", got, wantPK)
	}
	if got := godynamo.SK(v); got != wantSK {
		t.Fatalf("SK = %q, want %q", got, wantSK)
	}
}

func TestKeys_ReturnsBoth(t *testing.T) {
	id := uuid.New()
	v := &SinglePK{Model: godynamo.Model{ID: id}, Tenant: "acme"}

	pk, sk := godynamo.Keys(v)
	if got, want := pk, "SinglePK#acme"; got != want {
		t.Fatalf("pk = %q, want %q", got, want)
	}
	if got, want := sk, fmt.Sprintf("SinglePK#%s", id); got != want {
		t.Fatalf("sk = %q, want %q", got, want)
	}
}

func TestKeys_WorksWithValueNotPointer(t *testing.T) {
	id := uuid.New()
	v := SinglePK{Model: godynamo.Model{ID: id}, Tenant: "acme"}

	if got, want := godynamo.PK(v), "SinglePK#acme"; got != want {
		t.Fatalf("PK = %q, want %q", got, want)
	}
}

func TestPK_PanicsWithoutModel(t *testing.T) {
	type NoModel struct {
		Name string
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for struct without embedded Model")
		}
	}()
	godynamo.PK(&NoModel{Name: "x"})
}
