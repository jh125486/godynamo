package godynamo

import (
	"context"
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
