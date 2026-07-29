package godynamo

import (
	"reflect"
	"testing"
)

// internalOrder mirrors the exported Order test fixture but lives in this
// (internal) test file so we can exercise parseTags directly.
type internalOrder struct {
	Model       `dynamo:"pk:CustomerID;sk:Status,CreatedDate"`
	CustomerID  string
	Status      string
	CreatedDate string
}

type internalNoTags struct {
	Model
	Name string
}

func TestParseTags_NoClauses(t *testing.T) {
	mt := parseTags(reflect.TypeOf(internalNoTags{}))
	if mt.pkFields != nil {
		t.Fatalf("pkFields = %v, want nil", mt.pkFields)
	}
	if mt.skFields != nil {
		t.Fatalf("skFields = %v, want nil", mt.skFields)
	}
	if mt.structName != "internalNoTags" {
		t.Fatalf("structName = %q, want %q", mt.structName, "internalNoTags")
	}
}

func TestParseTags_BothClauses(t *testing.T) {
	mt := parseTags(reflect.TypeOf(internalOrder{}))

	wantPK := []string{"CustomerID"}
	wantSK := []string{"Status", "CreatedDate"}

	if !reflect.DeepEqual(mt.pkFields, wantPK) {
		t.Fatalf("pkFields = %v, want %v", mt.pkFields, wantPK)
	}
	if !reflect.DeepEqual(mt.skFields, wantSK) {
		t.Fatalf("skFields = %v, want %v", mt.skFields, wantSK)
	}
}

func TestParseTags_CachedAcrossCalls(t *testing.T) {
	typ := reflect.TypeOf(internalOrder{})

	first := parseTags(typ)
	second := parseTags(typ)

	if first != second {
		t.Fatal("expected parseTags to return the same cached *modelTags pointer")
	}
}

func TestParseTags_PanicsWithoutModel(t *testing.T) {
	type noModel struct {
		Name string
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for struct without embedded Model")
		}
	}()
	parseTags(reflect.TypeOf(noModel{}))
}

// internalMalformedClauses has a tag with an empty clause (from the
// trailing ";") and a clause with no "key:value" separator ("garbage"),
// both of which parseTags must silently skip.
type internalMalformedClauses struct {
	Model      `dynamo:"pk:CustomerID;;garbage;sk:Status"`
	CustomerID string
	Status     string
}

func TestParseTags_SkipsEmptyAndMalformedClauses(t *testing.T) {
	mt := parseTags(reflect.TypeOf(internalMalformedClauses{}))

	wantPK := []string{"CustomerID"}
	wantSK := []string{"Status"}
	if !reflect.DeepEqual(mt.pkFields, wantPK) {
		t.Fatalf("pkFields = %v, want %v", mt.pkFields, wantPK)
	}
	if !reflect.DeepEqual(mt.skFields, wantSK) {
		t.Fatalf("skFields = %v, want %v", mt.skFields, wantSK)
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields(" A , B ,C")
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitFields = %v, want %v", got, want)
	}
}
