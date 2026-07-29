package godynamo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// namesContain reports whether m (an ExpressionAttributeNames map, alias ->
// attribute name) contains attr as one of its values.
func namesContain(m map[string]string, attr string) bool {
	for _, v := range m {
		if v == attr {
			return true
		}
	}
	return false
}

// valuesContainS reports whether m (an ExpressionAttributeValues map)
// contains a string-typed value equal to want.
func valuesContainS(m map[string]types.AttributeValue, want string) bool {
	for _, v := range m {
		if s, ok := v.(*types.AttributeValueMemberS); ok && s.Value == want {
			return true
		}
	}
	return false
}

func TestQuery_TypeIndexMode_DefaultIndex(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}

	if captured == nil {
		t.Fatal("Query was not called")
	}
	if got := *captured.IndexName; got != defaultGSI1Name {
		t.Errorf("IndexName = %q, want %q", got, defaultGSI1Name)
	}
	if !namesContain(captured.ExpressionAttributeNames, "GSI1PK") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for GSI1PK", captured.ExpressionAttributeNames)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "widget") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "widget")
	}
}

func TestQuery_TypeIndexMode_IndexOverride(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).Index("CustomIndex").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if got := *captured.IndexName; got != "CustomIndex" {
		t.Errorf("IndexName = %q, want %q", got, "CustomIndex")
	}
}

func TestQuery_BaseTableMode(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	pk := "widget#" + uuid.NewString()
	if _, err := Query[widget](context.Background(), db).WherePK(pk).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}

	if captured.IndexName != nil {
		t.Errorf("IndexName = %q, want unset (base-table mode)", *captured.IndexName)
	}
	if !namesContain(captured.ExpressionAttributeNames, "PK") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for PK", captured.ExpressionAttributeNames)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, pk) {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, pk)
	}
}

func TestQuery_SKBeginsWith(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).WherePK("widget#x").SKBeginsWith("order#").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "SK") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for SK", captured.ExpressionAttributeNames)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "order#") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "order#")
	}
	if captured.KeyConditionExpression == nil || !containsAnd(*captured.KeyConditionExpression) {
		t.Errorf("KeyConditionExpression = %v, want an AND of PK and SK conditions", captured.KeyConditionExpression)
	}
}

func TestQuery_SKEquals(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).WherePK("widget#x").SKEquals("widget#x").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "SK") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for SK", captured.ExpressionAttributeNames)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "widget#x") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "widget#x")
	}
}

func TestQuery_SKBetween(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).WherePK("widget#x").SKBetween("a", "z").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "a") || !valuesContainS(captured.ExpressionAttributeValues, "z") {
		t.Errorf("ExpressionAttributeValues = %v, want values %q and %q", captured.ExpressionAttributeValues, "a", "z")
	}
}

func TestQuery_Filter(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).Filter("Name", "gizmo").All(); err != nil {
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

func TestQuery_FilterNotEqual(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterNotEqual("Name", "gizmo").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "<>") {
		t.Errorf("FilterExpression = %v, want a <> condition", captured.FilterExpression)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "gizmo") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "gizmo")
	}
}

func TestQuery_FilterGreaterThan(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterGreaterThan("Name", "m").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, ">") {
		t.Errorf("FilterExpression = %v, want a > condition", captured.FilterExpression)
	}
}

func TestQuery_FilterGreaterOrEqual(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterGreaterOrEqual("Name", "m").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, ">=") {
		t.Errorf("FilterExpression = %v, want a >= condition", captured.FilterExpression)
	}
}

func TestQuery_FilterLessThan(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterLessThan("Name", "m").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "<") {
		t.Errorf("FilterExpression = %v, want a < condition", captured.FilterExpression)
	}
}

func TestQuery_FilterLessOrEqual(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterLessOrEqual("Name", "m").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "<=") {
		t.Errorf("FilterExpression = %v, want a <= condition", captured.FilterExpression)
	}
}

func TestQuery_FilterBeginsWith(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterBeginsWith("Name", "giz").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "begins_with") {
		t.Errorf("FilterExpression = %v, want a begins_with condition", captured.FilterExpression)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "giz") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "giz")
	}
}

func TestQuery_FilterContains(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterContains("Name", "izm").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "contains") {
		t.Errorf("FilterExpression = %v, want a contains condition", captured.FilterExpression)
	}
	if !valuesContainS(captured.ExpressionAttributeValues, "izm") {
		t.Errorf("ExpressionAttributeValues = %v, want a value of %q", captured.ExpressionAttributeValues, "izm")
	}
}

func TestQuery_FilterExists(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterExists("Name").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "attribute_exists") {
		t.Errorf("FilterExpression = %v, want an attribute_exists condition", captured.FilterExpression)
	}
}

func TestQuery_FilterNotExists(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).FilterNotExists("Name").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !strings.Contains(*captured.FilterExpression, "attribute_not_exists") {
		t.Errorf("FilterExpression = %v, want an attribute_not_exists condition", captured.FilterExpression)
	}
}

func TestQuery_MultiFilter_EqualityAndOperator_ANDed(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).
		Filter("Name", "gizmo").
		FilterGreaterThan("Name", "a").
		All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.FilterExpression == nil || !containsAnd(*captured.FilterExpression) {
		t.Errorf("FilterExpression = %v, want an AND of both filters", captured.FilterExpression)
	}
}

func TestQuery_Filter_TaggedField_ResolvesToDynamodbavName(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[taggedWidget](context.Background(), db).Filter("NickName", "Bob").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "custom_name") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for %q", captured.ExpressionAttributeNames, "custom_name")
	}
	if namesContain(captured.ExpressionAttributeNames, "NickName") {
		t.Errorf("ExpressionAttributeNames = %v, want no alias for the Go field name %q", captured.ExpressionAttributeNames, "NickName")
	}
}

func TestQuery_Filter_NoTag_ResolvesToFieldNameUnchanged(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[taggedWidget](context.Background(), db).Filter("Name", "gizmo").All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "Name") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for the untagged field name %q", captured.ExpressionAttributeNames, "Name")
	}
}

func TestQuery_TypeIndexMode_CustomGSI1PKAttr(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDBWithOptions(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	}, WithGSI1PKAttr("CustomGSI1PK"))

	if _, err := Query[widget](context.Background(), db).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if !namesContain(captured.ExpressionAttributeNames, "CustomGSI1PK") {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for %q", captured.ExpressionAttributeNames, "CustomGSI1PK")
	}
	if namesContain(captured.ExpressionAttributeNames, "GSI1PK") {
		t.Errorf("ExpressionAttributeNames = %v, want no alias for the default %q", captured.ExpressionAttributeNames, "GSI1PK")
	}
}

func TestQuery_All_Pagination(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	item2 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "two"}
	av1, _ := attributevalue.MarshalMap(item1)
	av2, _ := attributevalue.MarshalMap(item2)

	lastKey := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "widget#cursor"},
	}

	var calls int
	var secondCallStartKey map[string]types.AttributeValue
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			calls++
			if calls == 1 {
				return &dynamodb.QueryOutput{
					Items:            []map[string]types.AttributeValue{av1},
					LastEvaluatedKey: lastKey,
				}, nil
			}
			secondCallStartKey = in.ExclusiveStartKey
			return &dynamodb.QueryOutput{
				Items: []map[string]types.AttributeValue{av2},
			}, nil
		},
	})

	got, err := Query[widget](context.Background(), db).All()
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("Query called %d times, want 2", calls)
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
}

func TestQuery_Page(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	av1, _ := attributevalue.MarshalMap(item1)
	lastKey := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "widget#cursor"},
	}

	var calls int
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			calls++
			return &dynamodb.QueryOutput{
				Items:            []map[string]types.AttributeValue{av1},
				LastEvaluatedKey: lastKey,
			}, nil
		},
	})

	items, nextCursor, err := Query[widget](context.Background(), db).Page("")
	if err != nil {
		t.Fatalf("Page() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("Query called %d times, want 1", calls)
	}
	if len(items) != 1 || items[0].Name != "one" {
		t.Errorf("Page() items = %+v, want [one]", items)
	}
	if nextCursor == "" {
		t.Fatal("nextCursor is empty, want non-empty (LastEvaluatedKey was set)")
	}

	// Feeding nextCursor back in should decode to an equivalent
	// ExclusiveStartKey on the next call.
	var capturedStartKey map[string]types.AttributeValue
	db2 := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			capturedStartKey = in.ExclusiveStartKey
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{av1}}, nil
		},
	})
	_, nextCursor2, err := Query[widget](context.Background(), db2).Page(nextCursor)
	if err != nil {
		t.Fatalf("Page(cursor) error = %v", err)
	}
	if nextCursor2 != "" {
		t.Errorf("nextCursor2 = %q, want empty (no LastEvaluatedKey in stub response)", nextCursor2)
	}
	if attrS(t, capturedStartKey, "PK") != "widget#cursor" {
		t.Errorf("ExclusiveStartKey = %v, want decoded cursor with PK=widget#cursor", capturedStartKey)
	}
}

func TestQuery_Page_BadCursor(t *testing.T) {
	db := testDB(&stubClient{})
	_, _, err := Query[widget](context.Background(), db).Page("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for malformed cursor, got nil")
	}
}

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	key := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "widget#abc"},
		"SK": &types.AttributeValueMemberS{Value: "widget#abc"},
	}

	cursor, err := encodeCursor(key)
	if err != nil {
		t.Fatalf("encodeCursor() error = %v", err)
	}
	if cursor == "" {
		t.Fatal("encodeCursor() returned empty string")
	}

	got, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("decodeCursor() error = %v", err)
	}
	if attrS(t, got, "PK") != "widget#abc" {
		t.Errorf("decoded PK = %v, want %q", got["PK"], "widget#abc")
	}
	if attrS(t, got, "SK") != "widget#abc" {
		t.Errorf("decoded SK = %v, want %q", got["SK"], "widget#abc")
	}
}

func TestDecodeCursor_Invalid(t *testing.T) {
	if _, err := decodeCursor("!!!not-base64!!!"); err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
	// Valid base64, but not valid JSON.
	if _, err := decodeCursor("bm90LWpzb24"); err == nil {
		t.Fatal("expected error for base64-valid-but-non-JSON payload, got nil")
	}
}

func TestQuery_Limit(t *testing.T) {
	var captured *dynamodb.QueryInput
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			captured = in
			return &dynamodb.QueryOutput{}, nil
		},
	})

	if _, err := Query[widget](context.Background(), db).Limit(25).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.Limit == nil || *captured.Limit != 25 {
		t.Errorf("Limit = %v, want 25", captured.Limit)
	}
}

func TestQuery_Filter_EmptyFieldName_BuildError(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			called = true
			return &dynamodb.QueryOutput{}, nil
		},
	})

	_, err := Query[widget](context.Background(), db).Filter("", "x").All()
	if err == nil {
		t.Fatal("expected error building query expression with an empty filter field name, got nil")
	}
	if called {
		t.Error("Query was called, want no call when building the expression fails")
	}
}

func TestQuery_Page_Filter_EmptyFieldName_BuildError(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			called = true
			return &dynamodb.QueryOutput{}, nil
		},
	})

	_, _, err := Query[widget](context.Background(), db).Filter("", "x").Page("")
	if err == nil {
		t.Fatal("expected error building query expression with an empty filter field name, got nil")
	}
	if called {
		t.Error("Query was called, want no call when building the expression fails")
	}
}

func TestQuery_All_ClientError(t *testing.T) {
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			return nil, errors.New("aws: throttled")
		},
	})

	if _, err := Query[widget](context.Background(), db).All(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_All_UnmarshalError(t *testing.T) {
	src := badUnmarshalItem{Model: Model{ID: uuid.New(), Type: "badUnmarshalItem", CreatedAt: fixedNow, Version: 1}}
	av, err := attributevalue.MarshalMap(src)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	if _, err := Query[badUnmarshalItem](context.Background(), db).All(); err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestQuery_Page_ClientError(t *testing.T) {
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			return nil, errors.New("aws: throttled")
		},
	})

	_, _, err := Query[widget](context.Background(), db).Page("")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_Page_UnmarshalError(t *testing.T) {
	src := badUnmarshalItem{Model: Model{ID: uuid.New(), Type: "badUnmarshalItem", CreatedAt: fixedNow, Version: 1}}
	av, err := attributevalue.MarshalMap(src)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	_, _, err = Query[badUnmarshalItem](context.Background(), db).Page("")
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestQuery_Page_EncodeCursorError(t *testing.T) {
	// A LastEvaluatedKey with a malformed N (Number) value round-trips fine
	// through attributevalue.UnmarshalMap (it decodes to math.NaN() -- Go's
	// strconv happily parses the literal "NaN"), but then fails
	// encodeCursor's json.Marshal step, since encoding/json rejects
	// NaN/Inf float64 values. This is how a real (if byzantine/corrupted)
	// LastEvaluatedKey could realistically trip this path.
	lastKey := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberN{Value: "NaN"},
	}
	db := testDB(&stubClient{
		queryFn: func(_ context.Context, _ *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
			return &dynamodb.QueryOutput{LastEvaluatedKey: lastKey}, nil
		},
	})

	_, _, err := Query[widget](context.Background(), db).Page("")
	if err == nil {
		t.Fatal("expected encodeCursor error, got nil")
	}
}

func TestEncodeCursor_UnmarshalError(t *testing.T) {
	// A Number attribute whose Value isn't actually parseable as a number
	// fails attributevalue.UnmarshalMap -- the SDK's AttributeValue types
	// are just plain structs, so this is constructible even though DynamoDB
	// itself would never really produce such a value.
	key := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberN{Value: "not-a-number"},
	}
	if _, err := encodeCursor(key); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestEncodeCursor_MarshalJSONError(t *testing.T) {
	key := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberN{Value: "NaN"},
	}
	if _, err := encodeCursor(key); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// containsAnd is a loose check that the built key condition expression
// combines two conditions (i.e. contains the literal " AND " DynamoDB
// expression syntax uses between conditions).
func containsAnd(expr string) bool {
	for i := 0; i+5 <= len(expr); i++ {
		if expr[i:i+5] == " AND " {
			return true
		}
	}
	return false
}
