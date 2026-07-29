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

func TestTransactGet_Empty_NoOp(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, _ *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			called = true
			return &dynamodb.TransactGetItemsOutput{}, nil
		},
	})

	if err := TransactGet(context.Background(), db).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called {
		t.Error("TransactGetItems was called, want no call when nothing was queued")
	}
}

func TestTransactGet_ClientError(t *testing.T) {
	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, _ *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			return nil, errors.New("aws: throttled")
		},
	})

	dst := &widget{Model: Model{ID: uuid.New()}}
	if err := TransactGet(context.Background(), db).Get(dst).Run(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTransactGet_FewerResponsesThanQueued(t *testing.T) {
	// A malformed/short Responses slice (fewer entries than TransactItems
	// queued) must not panic -- Run should just stop filling in dsts.
	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, _ *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			return &dynamodb.TransactGetItemsOutput{Responses: []types.ItemResponse{}}, nil
		},
	})

	dst1 := &widget{Model: Model{ID: uuid.New()}, Name: "untouched1"}
	dst2 := &widget{Model: Model{ID: uuid.New()}, Name: "untouched2"}
	if err := TransactGet(context.Background(), db).Get(dst1).Get(dst2).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if dst1.Name != "untouched1" || dst2.Name != "untouched2" {
		t.Errorf("dst1=%+v dst2=%+v, want both left untouched", dst1, dst2)
	}
}

func TestTransactGet_UnmarshalError(t *testing.T) {
	src := badUnmarshalItem{Model: Model{ID: uuid.New(), Type: "badUnmarshalItem", CreatedAt: fixedNow, Version: 1}}
	av, err := attributevalue.MarshalMap(src)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	db := testDB(&stubClient{
		transactGetItemsFn: func(_ context.Context, _ *dynamodb.TransactGetItemsInput) (*dynamodb.TransactGetItemsOutput, error) {
			return &dynamodb.TransactGetItemsOutput{
				Responses: []types.ItemResponse{{Item: av}},
			}, nil
		},
	})

	dst := &badUnmarshalItem{Model: Model{ID: uuid.New()}}
	if err := TransactGet(context.Background(), db).Get(dst).Run(); err == nil {
		t.Fatal("expected unmarshal error, got nil")
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

func TestTransactWrite_Put_MarshalError_SkipsSubsequentCalls(t *testing.T) {
	bad := &badMarshalItem{Model: Model{ID: uuid.New()}}
	good := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}

	var called bool
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			called = true
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	// The first Put records a marshal error; the second Put call must
	// short-circuit ("if b.err != nil { return b }") rather than attempt to
	// stamp/marshal good.
	err := TransactWrite(context.Background(), db).Put(bad).Put(good).Run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if called {
		t.Error("TransactWriteItems was called, want no call once Put has recorded an error")
	}
}

func TestTransactWrite_ConditionCheck_SkipsWhenErrAlreadySet(t *testing.T) {
	bad := &badMarshalItem{Model: Model{ID: uuid.New()}}
	keyItem := &widget{Model: Model{ID: uuid.New()}}

	var called bool
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			called = true
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	// Put records an error first; ConditionCheck must short-circuit
	// ("if b.err != nil { return b }") rather than queue another entry.
	err := TransactWrite(context.Background(), db).Put(bad).ConditionCheck(keyItem, 1).Run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if called {
		t.Error("TransactWriteItems was called, want no call once Put has recorded an error")
	}
}

func TestTransactWrite_Empty_NoOp(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			called = true
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
	})

	if err := TransactWrite(context.Background(), db).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called {
		t.Error("TransactWriteItems was called, want no call when nothing was queued")
	}
}

func TestTransactWrite_GenericError_NotTransactionCanceled(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}
	db := testDB(&stubClient{
		transactWriteItemsFn: func(_ context.Context, _ *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
			return nil, errors.New("aws: internal server error")
		},
	})

	err := TransactWrite(context.Background(), db).Put(item).Run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrOptimisticLock) {
		t.Errorf("error = %v, should NOT wrap ErrOptimisticLock for a non-TransactionCanceledException error", err)
	}
}

func TestCancellationReasonCodes_NilCode_MapsToNone(t *testing.T) {
	code := "ConditionalCheckFailed"
	codes := cancellationReasonCodes([]types.CancellationReason{
		{Code: nil},
		{Code: &code},
	})
	if len(codes) != 2 {
		t.Fatalf("codes = %v, want 2 entries", codes)
	}
	if codes[0] != "None" {
		t.Errorf("codes[0] = %q, want %q for a nil Code", codes[0], "None")
	}
	if codes[1] != code {
		t.Errorf("codes[1] = %q, want %q", codes[1], code)
	}
}
