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

// gadget is a second fixture type, alongside widget, used for
// transact_test.go's heterogeneous-transaction coverage. It has a
// non-default pk clause, to exercise TransactGet/TransactWrite's advantage
// over the zero-value-plus-ID key computation Get/BatchGet use.
type gadget struct {
	Model `dynamo:"pk:Owner"`
	Owner string
	Label string
}

func TestTransactGet_MultipleTypes(t *testing.T) {
	wID := uuid.New()
	want := widget{Model: Model{ID: wID, Type: "widget", CreatedAt: fixedNow, Version: 1}, Name: "gizmo"}
	wantAV, err := attributevalue.MarshalMap(want)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	gID := uuid.New()
	wantG := gadget{Model: Model{ID: gID, Type: "gadget", CreatedAt: fixedNow, Version: 1}, Owner: "alice", Label: "thing"}
	wantGAV, err := attributevalue.MarshalMap(wantG)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	var captured *dynamodb.TransactGetItemsInput
	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, in *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			captured = in
			return &dynamodb.TransactGetItemsOutput{
				Responses: []types.ItemResponse{
					{Item: wantAV},
					{Item: wantGAV},
				},
			}, nil
		},
	})

	dstW := &widget{Model: Model{ID: wID}}
	dstG := &gadget{Model: Model{ID: gID}, Owner: "alice"}

	err = TransactGet(context.Background(), db).
		Get(dstW).
		Get(dstG).
		Run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if captured == nil || len(captured.TransactItems) != 2 {
		t.Fatalf("TransactGetItems TransactItems = %v, want 2 entries", captured)
	}
	if got := *captured.TransactItems[0].Get.TableName; got != "test-table" {
		t.Errorf("TableName[0] = %q, want %q", got, "test-table")
	}

	if dstW.Name != "gizmo" {
		t.Errorf("dstW.Name = %q, want %q", dstW.Name, "gizmo")
	}
	if dstG.Label != "thing" {
		t.Errorf("dstG.Label = %q, want %q", dstG.Label, "thing")
	}
}

func TestTransactGet_MissingItemLeavesDstUntouched(t *testing.T) {
	id := uuid.New()
	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, in *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			return &dynamodb.TransactGetItemsOutput{
				Responses: []types.ItemResponse{
					{}, // empty slot: key didn't exist.
				},
			}, nil
		},
	})

	dst := &widget{Model: Model{ID: id}, Name: "untouched"}
	if err := TransactGet(context.Background(), db).Get(dst).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if dst.Name != "untouched" || dst.ID != id {
		t.Errorf("dst = %+v, want unchanged (Name=untouched, ID=%v)", dst, id)
	}
	if !dst.CreatedAt.IsZero() {
		t.Errorf("dst.CreatedAt = %v, want zero", dst.CreatedAt)
	}
}

func TestTransactGet_TooManyItems_NoAPICall(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, _ *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			called = true
			return &dynamodb.TransactGetItemsOutput{}, nil
		},
	})

	b := TransactGet(context.Background(), db)
	for range 101 {
		b.Get(&widget{Model: Model{ID: uuid.New()}})
	}

	if err := b.Run(); err == nil {
		t.Fatal("Run() error = nil, want non-nil for >100 queued items")
	}
	if called {
		t.Error("TransactGetItems was called, want no call when over the limit")
	}
}

func TestTransactWrite_Put_NewItem_ConditionMatchesSinglePut(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}

	var captured *dynamodb.TransactWriteItemsInput
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, in *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			captured = in
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	if err := TransactWrite(context.Background(), db).Put(item).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(captured.TransactItems) != 1 {
		t.Fatalf("TransactItems len = %d, want 1", len(captured.TransactItems))
	}
	putEntry := captured.TransactItems[0].Put
	if putEntry == nil {
		t.Fatal("TransactItems[0].Put is nil")
	}
	if putEntry.ConditionExpression == nil {
		t.Fatal("Put.ConditionExpression is nil, want attribute_not_exists(PK) condition")
	}
	foundPK := false
	for _, name := range putEntry.ExpressionAttributeNames {
		if name == "PK" {
			foundPK = true
		}
	}
	if !foundPK {
		t.Errorf("ExpressionAttributeNames = %v, want an alias for \"PK\"", putEntry.ExpressionAttributeNames)
	}
	if got := attrN(t, putEntry.Item, "Version"); got != "1" {
		t.Errorf("Item[Version] = %q, want %q", got, "1")
	}
	if item.Version != 1 {
		t.Errorf("item.Version = %d, want 1 (stamped in place)", item.Version)
	}
}

func TestTransactWrite_Put_ExistingItem_ConditionMatchesSinglePut(t *testing.T) {
	id := uuid.New()
	created := fixedNow.Add(-24 * time.Hour)
	item := &widget{
		Model: Model{ID: id, Type: "widget", CreatedAt: created, CreatedBy: "alice", UpdatedAt: created, UpdatedBy: "alice", Version: 3},
		Name:  "gizmo",
	}

	var captured *dynamodb.TransactWriteItemsInput
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, in *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			captured = in
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	if err := TransactWrite(context.Background(), db).Put(item).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	putEntry := captured.TransactItems[0].Put
	if putEntry.ConditionExpression == nil {
		t.Fatal("Put.ConditionExpression is nil, want Version = :expected condition")
	}
	foundExpected := false
	for _, v := range putEntry.ExpressionAttributeValues {
		if n, ok := v.(*types.AttributeValueMemberN); ok && n.Value == "3" {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Errorf("ExpressionAttributeValues = %v, want a value of 3 (pre-increment version)", putEntry.ExpressionAttributeValues)
	}
	if got := attrN(t, putEntry.Item, "Version"); got != "4" {
		t.Errorf("Item[Version] = %q, want %q", got, "4")
	}
	if item.CreatedBy != "alice" {
		t.Errorf("item.CreatedBy = %q, want unchanged %q", item.CreatedBy, "alice")
	}
}

func TestTransactWrite_Delete(t *testing.T) {
	id := uuid.New()
	keyItem := &widget{Model: Model{ID: id}}

	var captured *dynamodb.TransactWriteItemsInput
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, in *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			captured = in
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	if err := TransactWrite(context.Background(), db).Delete(keyItem).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	delEntry := captured.TransactItems[0].Delete
	if delEntry == nil {
		t.Fatal("TransactItems[0].Delete is nil")
	}
	if delEntry.ConditionExpression != nil {
		t.Errorf("Delete.ConditionExpression = %v, want nil", *delEntry.ConditionExpression)
	}
	wantPK, wantSK := PK(keyItem), SK(keyItem)
	if got := attrS(t, delEntry.Key, "PK"); got != wantPK {
		t.Errorf("Delete.Key[PK] = %q, want %q", got, wantPK)
	}
	if got := attrS(t, delEntry.Key, "SK"); got != wantSK {
		t.Errorf("Delete.Key[SK] = %q, want %q", got, wantSK)
	}
}

func TestTransactWrite_ConditionCheck(t *testing.T) {
	id := uuid.New()
	keyItem := &widget{Model: Model{ID: id}}

	var captured *dynamodb.TransactWriteItemsInput
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, in *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			captured = in
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	if err := TransactWrite(context.Background(), db).ConditionCheck(keyItem, 5).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	ccEntry := captured.TransactItems[0].ConditionCheck
	if ccEntry == nil {
		t.Fatal("TransactItems[0].ConditionCheck is nil")
	}
	wantPK, wantSK := PK(keyItem), SK(keyItem)
	if got := attrS(t, ccEntry.Key, "PK"); got != wantPK {
		t.Errorf("ConditionCheck.Key[PK] = %q, want %q", got, wantPK)
	}
	if got := attrS(t, ccEntry.Key, "SK"); got != wantSK {
		t.Errorf("ConditionCheck.Key[SK] = %q, want %q", got, wantSK)
	}
	foundExpected := false
	for _, v := range ccEntry.ExpressionAttributeValues {
		if n, ok := v.(*types.AttributeValueMemberN); ok && n.Value == "5" {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Errorf("ExpressionAttributeValues = %v, want a value of 5", ccEntry.ExpressionAttributeValues)
	}
}

func TestTransactWrite_ConditionalCheckFailed_TranslatesToOptimisticLock(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}
	code := "ConditionalCheckFailed"

	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			return nil, &types.TransactionCanceledException{
				Message: awsString("cancelled"),
				CancellationReasons: []types.CancellationReason{
					{Code: awsString("None")},
					{Code: &code},
				},
			}
		},
	})

	err := TransactWrite(context.Background(), db).Put(item).Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("error = %v, want wrapping ErrOptimisticLock", err)
	}
}

func TestTransactWrite_NonConditionCancellation_NotOptimisticLock(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}
	code := "ItemCollectionSizeLimitExceeded"

	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			return nil, &types.TransactionCanceledException{
				Message:             awsString("cancelled"),
				CancellationReasons: []types.CancellationReason{{Code: &code}},
			}
		},
	})

	err := TransactWrite(context.Background(), db).Put(item).Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	if errors.Is(err, ErrOptimisticLock) {
		t.Errorf("error = %v, should NOT wrap ErrOptimisticLock", err)
	}
}

func TestTransactWrite_TooManyItems_NoAPICall(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			called = true
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	b := TransactWrite(context.Background(), db)
	for range 101 {
		b.Delete(&widget{Model: Model{ID: uuid.New()}})
	}

	if err := b.Run(); err == nil {
		t.Fatal("Run() error = nil, want non-nil for >100 queued entries")
	}
	if called {
		t.Error("TransactWriteItems was called, want no call when over the limit")
	}
}
