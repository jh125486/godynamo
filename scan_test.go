package godynamo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

func TestScan_Basic(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	av1, _ := attributevalue.MarshalMap(item1)

	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{av1}}, nil
		},
	})

	got, err := Scan[widget](db).All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured == nil {
		t.Fatal("Scan was not called")
	}
	if captured.IndexName != nil {
		t.Errorf("IndexName = %q, want unset", *captured.IndexName)
	}
	if got := *captured.TableName; got != "test-table" {
		t.Errorf("TableName = %q, want %q", got, "test-table")
	}
	if len(got) != 1 || got[0].Name != "one" {
		t.Errorf("All() = %+v, want [one]", got)
	}
}

func TestScan_Filter(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).Filter("Name", "gizmo").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil {
		t.Fatal("FilterExpression is nil, want a Name = :val filter")
	}
	if !namesContain(captured.ExpressionAttributeNames, "Name") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for Name", captured.ExpressionAttributeNames)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "gizmo") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "gizmo")
	}
}

func TestScan_MultiFilter_ANDed(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).Filter("Name", "gizmo").Filter("Type", "widget").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !containsAnd(*captured.FilterExpression) {
		t.Errorf("FilterExpression = %v, want an AND of both filters", captured.FilterExpression)
	}
}

func TestScan_FilterNotEqual(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterNotEqual("Name", "gizmo").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "<>") {
		t.Errorf("FilterExpression = %v, want a <> condition", captured.FilterExpression)
	}
}

func TestScan_FilterGreaterThan(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterGreaterThan("Name", "m").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, ">") {
		t.Errorf("FilterExpression = %v, want a > condition", captured.FilterExpression)
	}
}

func TestScan_FilterGreaterOrEqual(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterGreaterOrEqual("Name", "m").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, ">=") {
		t.Errorf("FilterExpression = %v, want a >= condition", captured.FilterExpression)
	}
}

func TestScan_FilterLessThan(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterLessThan("Name", "m").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "<") {
		t.Errorf("FilterExpression = %v, want a < condition", captured.FilterExpression)
	}
}

func TestScan_FilterLessOrEqual(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterLessOrEqual("Name", "m").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "<=") {
		t.Errorf("FilterExpression = %v, want a <= condition", captured.FilterExpression)
	}
}

func TestScan_FilterBeginsWith(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterBeginsWith("Name", "giz").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "begins_with") {
		t.Errorf("FilterExpression = %v, want a begins_with condition", captured.FilterExpression)
	}
}

func TestScan_FilterContains(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterContains("Name", "izm").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "contains") {
		t.Errorf("FilterExpression = %v, want a contains condition", captured.FilterExpression)
	}
}

func TestScan_FilterExists(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterExists("Name").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "attribute_exists") {
		t.Errorf("FilterExpression = %v, want an attribute_exists condition", captured.FilterExpression)
	}
}

func TestScan_FilterNotExists(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).FilterNotExists("Name").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "attribute_not_exists") {
		t.Errorf("FilterExpression = %v, want an attribute_not_exists condition", captured.FilterExpression)
	}
}

func TestScan_MultiFilter_EqualityAndOperator_ANDed(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).
		Filter("Name", "gizmo").
		FilterLessThan("Name", "z").
		All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !containsAnd(*captured.FilterExpression) {
		t.Errorf("FilterExpression = %v, want an AND of both filters", captured.FilterExpression)
	}
}

func TestScan_Filter_TaggedField_ResolvesToDynamodbavName(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[taggedWidget](db).Filter("NickName", "Bob").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "custom_name") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for %q", captured.ExpressionAttributeNames, "custom_name")
	}
	if namesContain(captured.ExpressionAttributeNames, "NickName") {
		t.Errorf("ExpressionAttributeNames = %v, want no alias for the Go field name %q", captured.ExpressionAttributeNames, "NickName")
	}
}

func TestScan_Filter_NoTag_ResolvesToFieldNameUnchanged(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[taggedWidget](db).Filter("Name", "gizmo").All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "Name") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for the untagged field name %q", captured.ExpressionAttributeNames, "Name")
	}
}

func TestScan_NoFilter_NoFilterExpression(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression != nil {
		t.Errorf("FilterExpression = %v, want nil (no Filter calls)", captured.FilterExpression)
	}
}

func TestScan_Limit(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).Limit(25).All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.Limit == nil || *captured.Limit != 25 {
		t.Errorf("Limit = %v, want 25", captured.Limit)
	}
}

func TestScan_Filter_EmptyFieldName_BuildError(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			called = true
			return &dynamodb.ScanOutput{}, nil
		},
	})

	_, err := Scan[widget](db).Filter("", "x").All(context.Background())
	if err == nil {
		t.Fatal("expected error building scan expression with an empty filter field name, got nil")
	}
	if called {
		t.Error("Scan was called, want no call when building the expression fails")
	}
}

func TestScan_UnmarshalError(t *testing.T) {
	src := badUnmarshalItem{Model: Model{ID: uuid.New(), Type: "badUnmarshalItem", CreatedAt: fixedNow, Version: 1}}
	av, err := attributevalue.MarshalMap(src)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	db := testDB(&stubClient{
		scanFn: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	if _, err := Scan[badUnmarshalItem](db).All(context.Background()); err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestScan_All_Pagination(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	item2 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "two"}
	av1, _ := attributevalue.MarshalMap(item1)
	av2, _ := attributevalue.MarshalMap(item2)

	lastKey := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "widget#cursor"},
	}

	var calls int
	var secondCallStartKey map[string]types.AttributeValue
	var secondCallIndexName *string
	indexNameSeen := false
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			calls++
			if in.IndexName != nil {
				indexNameSeen = true
			}
			if calls == 1 {
				return &dynamodb.ScanOutput{
					Items:            []map[string]types.AttributeValue{av1},
					LastEvaluatedKey: lastKey,
				}, nil
			}
			secondCallStartKey = in.ExclusiveStartKey
			secondCallIndexName = in.IndexName
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{av2},
			}, nil
		},
	})

	got, err := Scan[widget](db).All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("Scan called %d times, want 2", calls)
	}
	if len(got) != 2 {
		t.Fatalf("All() returned %d items, want 2", len(got))
	}
	if got[0].Name != "one" || got[1].Name != "two" {
		t.Errorf("All() = %+v, want [one, two]", got)
	}
	if attrS(t, secondCallStartKey, "PK") != "widget#cursor" {
		t.Errorf("second call ExclusiveStartKey = %v, want the first response's LastEvaluatedKey", secondCallStartKey)
	}
	if indexNameSeen {
		t.Error("IndexName was set on some ScanInput, want it never set")
	}
	if secondCallIndexName != nil {
		t.Errorf("second call IndexName = %q, want nil", *secondCallIndexName)
	}
}

func TestScan_Parallel_SegmentsSetCorrectly(t *testing.T) {
	const n = 3

	var mu sync.Mutex
	var inputs []*dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			mu.Lock()
			inputs = append(inputs, in)
			mu.Unlock()
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).Parallel(n).All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}

	if len(inputs) != n {
		t.Fatalf("Scan called %d times, want %d", len(inputs), n)
	}

	seen := make(map[int32]bool)
	for _, in := range inputs {
		if in.TotalSegments == nil || *in.TotalSegments != n {
			t.Errorf("TotalSegments = %v, want %d", in.TotalSegments, n)
		}
		if in.Segment == nil {
			t.Fatal("Segment is nil, want set")
		}
		seen[*in.Segment] = true
		if in.IndexName != nil {
			t.Errorf("IndexName = %q, want unset", *in.IndexName)
		}
	}
	for i := range int32(n) {
		if !seen[i] {
			t.Errorf("segment %d never scanned; seen = %v", i, seen)
		}
	}
}

func TestScan_Parallel_MergesResultsAcrossSegments(t *testing.T) {
	const n = 3

	names := map[int32]string{0: "zero", 1: "one", 2: "two"}
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			seg := *in.Segment
			item := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: names[seg]}
			av, _ := attributevalue.MarshalMap(item)
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	got, err := Scan[widget](db).Parallel(n).All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(got) != n {
		t.Fatalf("All() returned %d items, want %d", len(got), n)
	}

	gotNames := make(map[string]bool, n)
	for _, item := range got {
		gotNames[item.Name] = true
	}
	for _, want := range names {
		if !gotNames[want] {
			t.Errorf("All() = %+v, missing item %q", got, want)
		}
	}
}

func TestScan_Parallel_MultiPagePerSegment(t *testing.T) {
	const n = 2

	// Segment 0 returns two pages; segment 1 returns one page.
	lastKey := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "widget#cursor"},
	}

	var mu sync.Mutex
	seg0Calls := 0
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			seg := *in.Segment
			if seg == 0 {
				mu.Lock()
				seg0Calls++
				call := seg0Calls
				mu.Unlock()
				item := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: fmt.Sprintf("seg0-page%d", call)}
				av, _ := attributevalue.MarshalMap(item)
				if call == 1 {
					return &dynamodb.ScanOutput{
						Items:            []map[string]types.AttributeValue{av},
						LastEvaluatedKey: lastKey,
					}, nil
				}
				return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{av}}, nil
			}
			item := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "seg1-page1"}
			av, _ := attributevalue.MarshalMap(item)
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	got, err := Scan[widget](db).Parallel(n).All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("All() returned %d items, want 3 (2 from segment 0, 1 from segment 1); got %+v", len(got), got)
	}

	names := make(map[string]bool, len(got))
	for _, item := range got {
		names[item.Name] = true
	}
	for _, want := range []string{"seg0-page1", "seg0-page2", "seg1-page1"} {
		if !names[want] {
			t.Errorf("All() = %+v, missing item %q", got, want)
		}
	}
}

func TestScan_Parallel_ErrorPath(t *testing.T) {
	const n = 3
	wantErr := errors.New("boom")

	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			if *in.Segment == 1 {
				return nil, wantErr
			}
			return &dynamodb.ScanOutput{}, nil
		},
	})

	got, err := Scan[widget](db).Parallel(n).All(context.Background())
	if err == nil {
		t.Fatal("All() error = nil, want non-nil")
	}
	if got != nil {
		t.Errorf("All() results = %+v, want nil", got)
	}
}

func TestScan_SingleSegment_NoParallelCall_LeavesSegmentUnset(t *testing.T) {
	var captured *dynamodb.ScanInput
	db := testDB(&stubClient{
		scanFn: func(_ context.Context, in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			captured = in
			return &dynamodb.ScanOutput{}, nil
		},
	})

	if _, err := Scan[widget](db).Parallel(1).All(context.Background()); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.Segment != nil || captured.TotalSegments != nil {
		t.Errorf("Segment/TotalSegments = %v/%v, want both nil for Parallel(1)", captured.Segment, captured.TotalSegments)
	}
}

// TestScan_All_CompressedFieldSurvivesRoundTrip confirms scanSegment is
// wired through unmarshalItemInto (not a raw attributevalue.UnmarshalMap)
// by round-tripping a compressWidget (defined in compress_test.go) whose
// Notes field is tagged dynamo:"compress".
func TestScan_All_CompressedFieldSurvivesRoundTrip(t *testing.T) {
	item := compressWidget{Model: Model{ID: uuid.New(), Type: "compressWidget"}, Name: "gizmo", Notes: "a long compressible note"}
	av, err := marshalItem(&item)
	if err != nil {
		t.Fatalf("marshalItem() error = %v", err)
	}

	db := testDB(&stubClient{
		scanFn: func(_ context.Context, _ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	got, err := Scan[compressWidget](db).All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(got) != 1 || got[0].Notes != item.Notes {
		t.Fatalf("All() = %+v, want a single item with Notes = %q", got, item.Notes)
	}
}
