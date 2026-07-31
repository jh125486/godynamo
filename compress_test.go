package godynamo

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// compressWidget is a plain string-compressed-field fixture.
type compressWidget struct {
	Model
	Name  string
	Notes string `dynamo:"compress"`
}

// compressMeta is a nested struct type used by compressStructItem below, to
// exercise a non-string compressed field.
type compressMeta struct {
	Author string
	Words  int
}

// compressStructItem exercises a struct-typed compressed field.
type compressStructItem struct {
	Model
	Meta compressMeta `dynamo:"compress"`
}

// compressSliceItem exercises a slice-typed compressed field.
type compressSliceItem struct {
	Model
	Tags []string `dynamo:"compress"`
}

// compressMapItem exercises a map-typed compressed field.
type compressMapItem struct {
	Model
	Attrs map[string]string `dynamo:"compress"`
}

// compressRenamedItem exercises the orthogonality of dynamo:"compress" and a
// dynamodbav name override on the same field: dynamodbav controls the
// attribute name, dynamo:"compress" controls the compression behavior.
type compressRenamedItem struct {
	Model
	Notes string `dynamo:"compress" dynamodbav:"notes_blob"`
}

func TestCompressJSON_DecompressJSON_RoundTrip(t *testing.T) {
	original := []byte(`{"hello":"world","n":42,"list":[1,2,3]}`)

	compressed, err := compressJSON(original)
	if err != nil {
		t.Fatalf("compressJSON() error = %v", err)
	}
	if len(compressed) == 0 {
		t.Fatal("compressJSON() returned empty output")
	}

	got, err := decompressJSON(compressed)
	if err != nil {
		t.Fatalf("decompressJSON() error = %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("decompressJSON() = %q, want %q", got, original)
	}
}

func TestDecompressJSON_MalformedInput_ReturnsError(t *testing.T) {
	_, err := decompressJSON([]byte("not gzip data at all"))
	if err == nil {
		t.Fatal("expected error for malformed gzip input, got nil")
	}
}

func TestMarshalItem_CompressedField_ProducesBinaryAttribute(t *testing.T) {
	item := &compressWidget{Model: Model{ID: uuid.New()}, Name: "gizmo", Notes: "a fairly long note about this gizmo"}

	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}

	attr, ok := av["Notes"]
	if !ok {
		t.Fatal(`marshalItem() output missing "Notes" attribute`)
	}
	bin, ok := attr.(*types.AttributeValueMemberB)
	if !ok {
		t.Fatalf("Notes attribute = %T, want *types.AttributeValueMemberB", attr)
	}
	if len(bin.Value) == 0 {
		t.Fatal("Notes Binary attribute is empty")
	}

	// Sanity: the Name field, not compressed, marshals natively as a String.
	if _, ok := av["Name"].(*types.AttributeValueMemberS); !ok {
		t.Fatalf("Name attribute = %T, want *types.AttributeValueMemberS", av["Name"])
	}
}

func TestMarshalUnmarshalItem_CompressedField_RoundTrips(t *testing.T) {
	item := &compressWidget{
		Model: Model{ID: uuid.New()},
		Name:  "gizmo",
		Notes: "the quick brown fox jumps over the lazy dog, repeatedly, for testing purposes",
	}

	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}

	var got compressWidget
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}

	if got.Notes != item.Notes {
		t.Errorf("Notes = %q, want %q", got.Notes, item.Notes)
	}
	if got.Name != item.Name {
		t.Errorf("Name = %q, want %q", got.Name, item.Name)
	}
	if got.ID != item.ID {
		t.Errorf("ID = %v, want %v", got.ID, item.ID)
	}
}

func TestMarshalUnmarshalItem_StructTypedCompressedField_RoundTrips(t *testing.T) {
	item := &compressStructItem{
		Model: Model{ID: uuid.New()},
		Meta:  compressMeta{Author: "alice", Words: 1200},
	}

	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	if _, ok := av["Meta"].(*types.AttributeValueMemberB); !ok {
		t.Fatalf("Meta attribute = %T, want *types.AttributeValueMemberB", av["Meta"])
	}

	var got compressStructItem
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}
	if !reflect.DeepEqual(got.Meta, item.Meta) {
		t.Errorf("Meta = %+v, want %+v", got.Meta, item.Meta)
	}
}

func TestMarshalUnmarshalItem_SliceTypedCompressedField_RoundTrips(t *testing.T) {
	item := &compressSliceItem{
		Model: Model{ID: uuid.New()},
		Tags:  []string{"alpha", "beta", "gamma"},
	}

	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	if _, ok := av["Tags"].(*types.AttributeValueMemberB); !ok {
		t.Fatalf("Tags attribute = %T, want *types.AttributeValueMemberB", av["Tags"])
	}

	var got compressSliceItem
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}
	if !reflect.DeepEqual(got.Tags, item.Tags) {
		t.Errorf("Tags = %v, want %v", got.Tags, item.Tags)
	}
}

func TestMarshalUnmarshalItem_MapTypedCompressedField_RoundTrips(t *testing.T) {
	item := &compressMapItem{
		Model: Model{ID: uuid.New()},
		Attrs: map[string]string{"color": "blue", "size": "large"},
	}

	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	if _, ok := av["Attrs"].(*types.AttributeValueMemberB); !ok {
		t.Fatalf("Attrs attribute = %T, want *types.AttributeValueMemberB", av["Attrs"])
	}

	var got compressMapItem
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}
	if !reflect.DeepEqual(got.Attrs, item.Attrs) {
		t.Errorf("Attrs = %v, want %v", got.Attrs, item.Attrs)
	}
}

func TestUnmarshalItemInto_MissingCompressedAttribute_LeavesZeroValue(t *testing.T) {
	item := &compressWidget{Model: Model{ID: uuid.New()}, Name: "gizmo", Notes: "would-be notes"}
	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	// Simulate an older item written before the Notes field existed: no
	// "Notes" attribute present at all.
	delete(av, "Notes")

	var got compressWidget
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}
	if got.Notes != "" {
		t.Errorf("Notes = %q, want zero value \"\"", got.Notes)
	}
	if got.Name != "gizmo" {
		t.Errorf("Name = %q, want %q", got.Name, "gizmo")
	}
}

func TestUnmarshalItemInto_MalformedGzipPayload_ReturnsWrappedError(t *testing.T) {
	av := map[string]types.AttributeValue{
		"Notes": &types.AttributeValueMemberB{Value: []byte("not valid gzip data")},
	}

	var got compressWidget
	err := unmarshalItemInto(av, &got)
	if err == nil {
		t.Fatal("expected error for malformed gzip payload, got nil")
	}
}

func TestMarshalItem_NoCompressFields_MatchesMarshalMap(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "plain"}

	got, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	want, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("attributevalue.MarshalMap() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("marshalItem() = %v, want %v (identical to attributevalue.MarshalMap for a type with no compressed fields)", got, want)
	}
}

func TestUnmarshalItemInto_NoCompressFields_MatchesUnmarshalMap(t *testing.T) {
	src := widget{Model: Model{ID: uuid.New()}, Name: "plain"}
	av, err := attributevalue.MarshalMap(src)
	if err != nil {
		t.Fatalf("attributevalue.MarshalMap() error = %v", err)
	}

	var got widget
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}

	var want widget
	if err := attributevalue.UnmarshalMap(av, &want); err != nil {
		t.Fatalf("attributevalue.UnmarshalMap() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unmarshalItemInto() = %+v, want %+v", got, want)
	}
}

func TestMarshalUnmarshalItem_DynamodbavOverride_UsesOverriddenName(t *testing.T) {
	item := &compressRenamedItem{Model: Model{ID: uuid.New()}, Notes: "renamed compressed field"}

	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	if _, ok := av["Notes"]; ok {
		t.Errorf(`av contains "Notes"; want it stored under the dynamodbav-overridden name "notes_blob"`)
	}
	bin, ok := av["notes_blob"].(*types.AttributeValueMemberB)
	if !ok {
		t.Fatalf("notes_blob attribute = %T, want *types.AttributeValueMemberB", av["notes_blob"])
	}
	if len(bin.Value) == 0 {
		t.Fatal("notes_blob Binary attribute is empty")
	}

	var got compressRenamedItem
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}
	if got.Notes != item.Notes {
		t.Errorf("Notes = %q, want %q", got.Notes, item.Notes)
	}
}

func TestUnmarshalItemInto_NilPointer_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil destination pointer")
		}
	}()
	var dst *compressWidget
	_ = unmarshalItemInto(map[string]types.AttributeValue{}, dst)
}

func TestUnmarshalItemInto_NonPointer_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for non-pointer destination")
		}
	}()
	_ = unmarshalItemInto(map[string]types.AttributeValue{}, compressWidget{})
}

func TestUnmarshalItemInto_DoesNotMutateCallerMap(t *testing.T) {
	item := &compressWidget{Model: Model{ID: uuid.New()}, Name: "gizmo", Notes: "some notes"}
	av, err := marshalItem(item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}
	before := len(av)

	var got compressWidget
	if err := unmarshalItemInto(av, &got); err != nil {
		t.Fatalf("unmarshalItemInto() error = %v", err)
	}

	if len(av) != before {
		t.Errorf("caller's av map was mutated: len = %d, want %d", len(av), before)
	}
	if _, ok := av["Notes"]; !ok {
		t.Error(`caller's av map lost its "Notes" attribute -- unmarshalItemInto must operate on a copy`)
	}
}
