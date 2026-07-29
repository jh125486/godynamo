package godynamo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// widget is the fixture type used throughout crud_test.go.
type widget struct {
	Model
	Name string
}

// stubClient is a hand-rolled dynamoAPI implementation controlled per-test
// via function fields, so each test can assert on exactly what request was
// built and control exactly what "AWS" returns.
type stubClient struct {
	getFn    func(ctx context.Context, in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error)
	putFn    func(ctx context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error)
	updateFn func(ctx context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
	deleteFn func(ctx context.Context, in *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error)
	queryFn  func(ctx context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error)
}

func (s *stubClient) GetItem(ctx context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return s.getFn(ctx, in)
}

func (s *stubClient) PutItem(ctx context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return s.putFn(ctx, in)
}

func (s *stubClient) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return s.updateFn(ctx, in)
}

func (s *stubClient) DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return s.deleteFn(ctx, in)
}

func (s *stubClient) Query(ctx context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return s.queryFn(ctx, in)
}

var fixedNow = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func testDB(client dynamoAPI) *DB {
	return &DB{
		client:   client,
		table:    "test-table",
		gsi1Name: defaultGSI1Name,
		clock:    func() time.Time { return fixedNow },
		actor:    func(context.Context) string { return "tester" },
	}
}

// attrS/attrN extract the string/number value of an attribute for
// assertions, failing the test if the attribute is missing or the wrong
// type.
func attrS(t *testing.T, m map[string]types.AttributeValue, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("attribute %q missing", key)
	}
	s, ok := v.(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("attribute %q is %T, want *AttributeValueMemberS", key, v)
	}
	return s.Value
}

func attrN(t *testing.T, m map[string]types.AttributeValue, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("attribute %q missing", key)
	}
	n, ok := v.(*types.AttributeValueMemberN)
	if !ok {
		t.Fatalf("attribute %q is %T, want *AttributeValueMemberN", key, v)
	}
	return n.Value
}

func TestPut_NewItem(t *testing.T) {
	id := uuid.New()
	item := &widget{Model: Model{ID: id}, Name: "gizmo"}

	var captured *dynamodb.PutItemInput
	db := testDB(&stubClient{
		putFn: func(_ context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			captured = in
			return &dynamodb.PutItemOutput{}, nil
		},
	})

	if err := Put(context.Background(), db, item); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// Audit fields / Type / Version stamped on the in-memory item.
	if item.Type != "widget" {
		t.Errorf("Type = %q, want %q", item.Type, "widget")
	}
	if !item.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v", item.CreatedAt, fixedNow)
	}
	if item.CreatedBy != "tester" {
		t.Errorf("CreatedBy = %q, want %q", item.CreatedBy, "tester")
	}
	if !item.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", item.UpdatedAt, fixedNow)
	}
	if item.UpdatedBy != "tester" {
		t.Errorf("UpdatedBy = %q, want %q", item.UpdatedBy, "tester")
	}
	if item.Version != 1 {
		t.Errorf("Version = %d, want 1", item.Version)
	}

	if captured == nil {
		t.Fatal("PutItem was not called")
	}
	if captured.ConditionExpression == nil {
		t.Fatal("ConditionExpression is nil, want attribute_not_exists(PK) condition")
	}
	// The condition should reference the PK attribute name via an alias.
	foundPK := false
	for _, name := range captured.ExpressionAttributeNames {
		if name == "PK" {
			foundPK = true
		}
	}
	if !foundPK {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for \"PK\"", captured.ExpressionAttributeNames)
	}

	wantPK := PK(item)
	wantSK := SK(item)
	if got := attrS(t, captured.Item, "PK"); got != wantPK {
		t.Errorf("Item[PK] = %q, want %q", got, wantPK)
	}
	if got := attrS(t, captured.Item, "SK"); got != wantSK {
		t.Errorf("Item[SK] = %q, want %q", got, wantSK)
	}
	if got := attrS(t, captured.Item, "GSI1PK"); got != "widget" {
		t.Errorf("Item[GSI1PK] = %q, want %q", got, "widget")
	}
}

func TestPut_ExistingItem(t *testing.T) {
	id := uuid.New()
	created := fixedNow.Add(-24 * time.Hour)
	item := &widget{
		Model: Model{
			ID:        id,
			Type:      "widget",
			CreatedAt: created,
			CreatedBy: "alice",
			UpdatedAt: created,
			UpdatedBy: "alice",
			Version:   3,
		},
		Name: "gizmo",
	}

	var captured *dynamodb.PutItemInput
	db := testDB(&stubClient{
		putFn: func(_ context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			captured = in
			return &dynamodb.PutItemOutput{}, nil
		},
	})

	if err := Put(context.Background(), db, item); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if !item.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want unchanged %v", item.CreatedAt, created)
	}
	if item.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want unchanged %q", item.CreatedBy, "alice")
	}
	if !item.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", item.UpdatedAt, fixedNow)
	}
	if item.UpdatedBy != "tester" {
		t.Errorf("UpdatedBy = %q, want %q", item.UpdatedBy, "tester")
	}
	if item.Version != 4 {
		t.Errorf("Version = %d, want 4", item.Version)
	}

	if captured == nil {
		t.Fatal("PutItem was not called")
	}
	if captured.ConditionExpression == nil {
		t.Fatal("ConditionExpression is nil, want Version = :expected condition")
	}
	// The condition's value should be the PRE-increment version (3), not 4.
	foundExpected := false
	for _, v := range captured.ExpressionAttributeValues {
		if n, ok := v.(*types.AttributeValueMemberN); ok && n.Value == "3" {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Errorf("ExpressionAttributeValues = %v, want a value of 3 (pre-increment version)", captured.ExpressionAttributeValues)
	}
	if got := attrN(t, captured.Item, "Version"); got != "4" {
		t.Errorf("Item[Version] = %q, want %q", got, "4")
	}
	if _, ok := captured.Item["GSI1PK"]; ok {
		t.Errorf("Item[GSI1PK] = %v, want absent on re-Put of existing item", captured.Item["GSI1PK"])
	}
}

func TestPut_ConditionalCheckFailed_TranslatesToOptimisticLock(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}

	db := testDB(&stubClient{
		putFn: func(_ context.Context, _ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, &types.ConditionalCheckFailedException{Message: awsString("nope")}
		},
	})

	err := Put(context.Background(), db, item)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("error = %v, want wrapping ErrOptimisticLock", err)
	}
}

func TestGet_Success(t *testing.T) {
	id := uuid.New()
	want := widget{
		Model: Model{ID: id, Type: "widget", CreatedAt: fixedNow, Version: 1},
		Name:  "gizmo",
	}
	av, err := attributevalue.MarshalMap(want)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	av["PK"] = &types.AttributeValueMemberS{Value: "widget#" + id.String()}
	av["SK"] = &types.AttributeValueMemberS{Value: "widget#" + id.String()}

	var capturedKey map[string]types.AttributeValue
	db := testDB(&stubClient{
		getFn: func(_ context.Context, in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			capturedKey = in.Key
			return &dynamodb.GetItemOutput{Item: av}, nil
		},
	})

	got, err := Get[widget](context.Background(), db, id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != id || got.Name != "gizmo" {
		t.Errorf("Get() = %+v, want ID=%v Name=gizmo", got, id)
	}
	if got := attrS(t, capturedKey, "PK"); got != "widget#"+id.String() {
		t.Errorf("Key[PK] = %q, want %q", got, "widget#"+id.String())
	}
}

func TestGet_NotFound(t *testing.T) {
	id := uuid.New()
	db := testDB(&stubClient{
		getFn: func(_ context.Context, _ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{}, nil
		},
	})

	_, err := Get[widget](context.Background(), db, id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want wrapping ErrNotFound", err)
	}
}

func TestDelete_Success(t *testing.T) {
	id := uuid.New()
	var called bool
	db := testDB(&stubClient{
		deleteFn: func(_ context.Context, in *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			called = true
			if got := attrS(t, in.Key, "PK"); got != "widget#"+id.String() {
				t.Errorf("Key[PK] = %q, want %q", got, "widget#"+id.String())
			}
			return &dynamodb.DeleteItemOutput{}, nil
		},
	})

	if err := Delete[widget](context.Background(), db, id); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !called {
		t.Fatal("DeleteItem was not called")
	}
}

func TestUpdate_SetAndAdd(t *testing.T) {
	id := uuid.New()
	var captured *dynamodb.UpdateItemInput
	db := testDB(&stubClient{
		updateFn: func(_ context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			captured = in
			out, _ := attributevalue.MarshalMap(widget{Model: Model{ID: id}, Name: "updated"})
			return &dynamodb.UpdateItemOutput{Attributes: out}, nil
		},
	})

	got, err := Update[widget](context.Background(), db, id).
		Set("Name", "updated").
		Add("ViewCount", 5).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Name != "updated" {
		t.Errorf("Name = %q, want %q", got.Name, "updated")
	}

	if captured == nil {
		t.Fatal("UpdateItem was not called")
	}
	if captured.ConditionExpression != nil {
		t.Errorf("ConditionExpression = %v, want nil (no IfVersion called)", *captured.ConditionExpression)
	}

	names := map[string]bool{}
	for _, n := range captured.ExpressionAttributeNames {
		names[n] = true
	}
	for _, want := range []string{"UpdatedAt", "UpdatedBy", "Version", "Name", "ViewCount"} {
		if !names[want] {
			t.Errorf("ExpressionAttributeNames %v missing alias for %q", captured.ExpressionAttributeNames, want)
		}
	}

	if captured.UpdateExpression == nil {
		t.Fatal("UpdateExpression is nil")
	}

	foundViewCountDelta := false
	for _, v := range captured.ExpressionAttributeValues {
		if n, ok := v.(*types.AttributeValueMemberN); ok && n.Value == "5" {
			foundViewCountDelta = true
		}
	}
	if !foundViewCountDelta {
		t.Errorf("ExpressionAttributeValues = %v, want a value of 5 (ViewCount delta)", captured.ExpressionAttributeValues)
	}
}

func TestUpdate_IfVersion_AddsCondition(t *testing.T) {
	id := uuid.New()
	var captured *dynamodb.UpdateItemInput
	db := testDB(&stubClient{
		updateFn: func(_ context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			captured = in
			out, _ := attributevalue.MarshalMap(widget{Model: Model{ID: id}})
			return &dynamodb.UpdateItemOutput{Attributes: out}, nil
		},
	})

	_, err := Update[widget](context.Background(), db, id).
		Set("Name", "v2").
		IfVersion(7).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if captured.ConditionExpression == nil {
		t.Fatal("ConditionExpression is nil, want Version = :expectedVersion condition")
	}
	foundExpected := false
	for _, v := range captured.ExpressionAttributeValues {
		if n, ok := v.(*types.AttributeValueMemberN); ok && n.Value == "7" {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Errorf("ExpressionAttributeValues = %v, want a value of 7", captured.ExpressionAttributeValues)
	}
}

func TestUpdate_ConditionalCheckFailed_TranslatesToOptimisticLock(t *testing.T) {
	id := uuid.New()
	db := testDB(&stubClient{
		updateFn: func(_ context.Context, _ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, &types.ConditionalCheckFailedException{Message: awsString("nope")}
		},
	})

	_, err := Update[widget](context.Background(), db, id).
		IfVersion(1).
		Run(context.Background())
	if !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("error = %v, want wrapping ErrOptimisticLock", err)
	}
}

func awsString(s string) *string { return &s }
