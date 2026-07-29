package godynamo

import (
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
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
	mt := parseTags(reflect.TypeFor[internalNoTags]())
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
	mt := parseTags(reflect.TypeFor[internalOrder]())

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
	typ := reflect.TypeFor[internalOrder]()

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
	parseTags(reflect.TypeFor[noModel]())
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
	mt := parseTags(reflect.TypeFor[internalMalformedClauses]())

	wantPK := []string{"CustomerID"}
	wantSK := []string{"Status"}
	if !reflect.DeepEqual(mt.pkFields, wantPK) {
		t.Fatalf("pkFields = %v, want %v", mt.pkFields, wantPK)
	}
	if !reflect.DeepEqual(mt.skFields, wantSK) {
		t.Fatalf("skFields = %v, want %v", mt.skFields, wantSK)
	}
}

// resolverFixture exercises resolveAttrName's tag-parsing branches: a
// plain untagged field, a field with a name-override tag (with and without
// trailing options), a field tagged with an empty name, and a field
// explicitly excluded via "-".
type resolverFixture struct {
	Model
	Plain      string
	Renamed    string `dynamodbav:"renamed_attr"`
	WithOption string `dynamodbav:"opt_attr,omitempty"`
	EmptyName  string `dynamodbav:",omitempty"`
	Excluded   string `dynamodbav:"-"`
}

func TestResolveAttrName_NoTag_PassesThroughFieldName(t *testing.T) {
	got := resolveAttrName(reflect.TypeFor[resolverFixture](), "Plain")
	if got != "Plain" {
		t.Fatalf("resolveAttrName = %q, want %q", got, "Plain")
	}
}

func TestResolveAttrName_TagOverridesName(t *testing.T) {
	got := resolveAttrName(reflect.TypeFor[resolverFixture](), "Renamed")
	if got != "renamed_attr" {
		t.Fatalf("resolveAttrName = %q, want %q", got, "renamed_attr")
	}
}

func TestResolveAttrName_TagWithOptions_UsesNamePortionOnly(t *testing.T) {
	got := resolveAttrName(reflect.TypeFor[resolverFixture](), "WithOption")
	if got != "opt_attr" {
		t.Fatalf("resolveAttrName = %q, want %q", got, "opt_attr")
	}
}

func TestResolveAttrName_EmptyTagName_FallsBackToFieldName(t *testing.T) {
	got := resolveAttrName(reflect.TypeFor[resolverFixture](), "EmptyName")
	if got != "EmptyName" {
		t.Fatalf("resolveAttrName = %q, want %q", got, "EmptyName")
	}
}

func TestResolveAttrName_DashTag_FallsBackToFieldName(t *testing.T) {
	got := resolveAttrName(reflect.TypeFor[resolverFixture](), "Excluded")
	if got != "Excluded" {
		t.Fatalf("resolveAttrName = %q, want %q", got, "Excluded")
	}
}

func TestResolveAttrName_FieldNotFound_PassesThroughUnchanged(t *testing.T) {
	got := resolveAttrName(reflect.TypeFor[resolverFixture](), "DoesNotExist")
	if got != "DoesNotExist" {
		t.Fatalf("resolveAttrName = %q, want %q", got, "DoesNotExist")
	}
}

func TestResolveAttrName_MatchesMarshalMapBehavior(t *testing.T) {
	// Regression guard: resolveAttrName's resolution must agree with what
	// attributevalue.MarshalMap actually names each attribute, for every
	// non-excluded field on resolverFixture (Model's own fields aside).
	v := resolverFixture{
		Plain:      "p",
		Renamed:    "r",
		WithOption: "w",
		EmptyName:  "e",
	}
	av, err := attributevalue.MarshalMap(v)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	cases := map[string]string{ // Go field name -> resolved attribute name.
		"Plain":      "Plain",
		"Renamed":    "renamed_attr",
		"WithOption": "opt_attr",
		"EmptyName":  "EmptyName",
	}
	typ := reflect.TypeFor[resolverFixture]()
	for goName, wantAttr := range cases {
		if got := resolveAttrName(typ, goName); got != wantAttr {
			t.Errorf("resolveAttrName(%q) = %q, want %q", goName, got, wantAttr)
		}
		if _, ok := av[wantAttr]; !ok {
			t.Errorf("MarshalMap() output %v missing key %q for field %q", av, wantAttr, goName)
		}
	}
	if _, ok := av["Excluded"]; ok {
		t.Errorf("MarshalMap() output %v should not contain the dynamodbav:\"-\" field", av)
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields(" A , B ,C")
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitFields = %v, want %v", got, want)
	}
}
