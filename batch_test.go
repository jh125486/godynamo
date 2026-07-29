package godynamo

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// widgetAV marshals a widget with the given id into a DynamoDB attribute
// map, injecting PK/SK the way Put would, for use as canned BatchGetItem
// responses.
func widgetAV(t *testing.T, id uuid.UUID) map[string]types.AttributeValue {
	t.Helper()
	w := widget{Model: Model{ID: id, Type: "widget", CreatedAt: fixedNow, Version: 1}, Name: "gizmo"}
	av, err := attributevalue.MarshalMap(w)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}
	pk, sk := PK(&w), SK(&w)
	av["PK"] = &types.AttributeValueMemberS{Value: pk}
	av["SK"] = &types.AttributeValueMemberS{Value: sk}
	return av
}

func TestBatchGet_Basic(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	var calls int
	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, in *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			calls++
			keys := in.RequestItems["test-table"].Keys
			if len(keys) != len(ids) {
				t.Errorf("keys len = %d, want %d", len(keys), len(ids))
			}
			resp := make([]map[string]types.AttributeValue, 0, len(ids))
			for _, id := range ids {
				resp = append(resp, widgetAV(t, id))
			}
			return &dynamodb.BatchGetItemOutput{
				Responses: map[string][]map[string]types.AttributeValue{"test-table": resp},
			}, nil
		},
	})

	got, err := BatchGet[widget](context.Background(), db).IDs(ids...).Run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 1 {
		t.Errorf("BatchGetItem calls = %d, want 1", calls)
	}
	if len(got) != len(ids) {
		t.Fatalf("Run() returned %d items, want %d", len(got), len(ids))
	}
}

func TestBatchGet_Chunking(t *testing.T) {
	const n = 150
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}

	var calls int
	var chunkSizes []int
	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, in *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			calls++
			keys := in.RequestItems["test-table"].Keys
			chunkSizes = append(chunkSizes, len(keys))
			if len(keys) > 100 {
				t.Errorf("chunk size = %d, want <= 100", len(keys))
			}
			resp := make([]map[string]types.AttributeValue, 0, len(keys))
			for range keys {
				resp = append(resp, widgetAV(t, uuid.New()))
			}
			return &dynamodb.BatchGetItemOutput{
				Responses: map[string][]map[string]types.AttributeValue{"test-table": resp},
			}, nil
		},
	})

	got, err := BatchGet[widget](context.Background(), db).IDs(ids...).Run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("BatchGetItem calls = %d, want 2", calls)
	}
	if chunkSizes[0] != 100 || chunkSizes[1] != 50 {
		t.Errorf("chunkSizes = %v, want [100 50]", chunkSizes)
	}
	if len(got) != n {
		t.Errorf("Run() returned %d items, want %d", len(got), n)
	}
}

func TestBatchGet_UnprocessedKeysRetry(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()

	var calls int
	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, in *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			calls++
			keys := in.RequestItems["test-table"].Keys
			if calls == 1 {
				if len(keys) != 2 {
					t.Errorf("call 1 keys len = %d, want 2", len(keys))
				}
				// Resolve only the first key; leave the second unprocessed.
				return &dynamodb.BatchGetItemOutput{
					Responses: map[string][]map[string]types.AttributeValue{
						"test-table": {widgetAV(t, id1)},
					},
					UnprocessedKeys: map[string]types.KeysAndAttributes{
						"test-table": {Keys: keys[1:]},
					},
				}, nil
			}
			// Retry: resolve the remaining key.
			if len(keys) != 1 {
				t.Errorf("retry keys len = %d, want 1", len(keys))
			}
			return &dynamodb.BatchGetItemOutput{
				Responses: map[string][]map[string]types.AttributeValue{
					"test-table": {widgetAV(t, id2)},
				},
			}, nil
		},
	})

	got, err := BatchGet[widget](context.Background(), db).IDs(id1, id2).Run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 2 {
		t.Errorf("BatchGetItem calls = %d, want 2 (initial + 1 retry)", calls)
	}
	if len(got) != 2 {
		t.Fatalf("Run() returned %d items, want 2", len(got))
	}
}

func TestBatchGet_RetryExhaustion(t *testing.T) {
	id := uuid.New()

	var calls int
	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, in *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			calls++
			keys := in.RequestItems["test-table"].Keys
			// Never resolve the key -- always report it unprocessed.
			return &dynamodb.BatchGetItemOutput{
				UnprocessedKeys: map[string]types.KeysAndAttributes{
					"test-table": {Keys: keys},
				},
			}, nil
		},
	})

	_, err := BatchGet[widget](context.Background(), db).IDs(id).Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil after retry exhaustion")
	}
	if calls != batchMaxRetries+1 {
		t.Errorf("BatchGetItem calls = %d, want %d (1 initial + %d retries)", calls, batchMaxRetries+1, batchMaxRetries)
	}
}

func TestBatchGet_Empty_NoOp(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, _ *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			called = true
			return &dynamodb.BatchGetItemOutput{}, nil
		},
	})

	got, err := BatchGet[widget](context.Background(), db).Run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != nil {
		t.Errorf("Run() = %v, want nil", got)
	}
	if called {
		t.Error("BatchGetItem was called, want no call when no IDs were queued")
	}
}

func TestBatchGet_ClientError(t *testing.T) {
	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, _ *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			return nil, errors.New("aws: throttled")
		},
	})

	_, err := BatchGet[widget](context.Background(), db).IDs(uuid.New()).Run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBatchGet_UnmarshalError(t *testing.T) {
	id := uuid.New()
	src := badUnmarshalItem{Model: Model{ID: id, Type: "badUnmarshalItem", CreatedAt: fixedNow, Version: 1}}
	av, err := attributevalue.MarshalMap(src)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	db := testDB(&stubClient{
		batchGetItemFn: func(_ context.Context, _ *dynamodb.BatchGetItemInput) (*dynamodb.BatchGetItemOutput, error) {
			return &dynamodb.BatchGetItemOutput{
				Responses: map[string][]map[string]types.AttributeValue{"test-table": {av}},
			}, nil
		},
	})

	_, err = BatchGet[badUnmarshalItem](context.Background(), db).IDs(id).Run()
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestBatchGet_NonDefaultKey_ReturnsErrNonDefaultKey(t *testing.T) {
	db := testDB(&stubClient{})

	_, err := BatchGet[internalOrder](context.Background(), db).IDs(uuid.New()).Run()
	if !errors.Is(err, ErrNonDefaultKey) {
		t.Fatalf("error = %v, want wrapping ErrNonDefaultKey", err)
	}
}

func TestBatchWrite_Delete_NonDefaultKey_ReturnsErrNonDefaultKey(t *testing.T) {
	db := testDB(&stubClient{})

	err := BatchWrite[internalOrder](context.Background(), db).Delete(uuid.New()).Run()
	if !errors.Is(err, ErrNonDefaultKey) {
		t.Fatalf("error = %v, want wrapping ErrNonDefaultKey", err)
	}
}

func TestBatchWrite_PutAndDelete(t *testing.T) {
	putItem := &widget{Model: Model{ID: uuid.New()}, Name: "new-gizmo"}
	deleteID := uuid.New()

	var captured []types.WriteRequest
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			captured = in.RequestItems["test-table"]
			return &dynamodb.BatchWriteItemOutput{}, nil
		},
	})

	err := BatchWrite[widget](context.Background(), db).
		Put(putItem).
		Delete(deleteID).
		Run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("write requests = %d, want 2", len(captured))
	}

	var sawPut, sawDelete bool
	for _, wr := range captured {
		if wr.PutRequest != nil {
			sawPut = true
			// Audit fields should be stamped, same as single-item Put.
			if got := attrS(t, wr.PutRequest.Item, "PK"); got != PK(putItem) {
				t.Errorf("PutRequest.Item[PK] = %q, want %q", got, PK(putItem))
			}
			if got := attrN(t, wr.PutRequest.Item, "Version"); got != "1" {
				t.Errorf("PutRequest.Item[Version] = %q, want %q", got, "1")
			}
		}
		if wr.DeleteRequest != nil {
			sawDelete = true
			wantPK, wantSK := zeroKeys[widget](deleteID)
			if got := attrS(t, wr.DeleteRequest.Key, "PK"); got != wantPK {
				t.Errorf("DeleteRequest.Key[PK] = %q, want %q", got, wantPK)
			}
			if got := attrS(t, wr.DeleteRequest.Key, "SK"); got != wantSK {
				t.Errorf("DeleteRequest.Key[SK] = %q, want %q", got, wantSK)
			}
		}
	}
	if !sawPut || !sawDelete {
		t.Errorf("sawPut=%v sawDelete=%v, want both true", sawPut, sawDelete)
	}

	// Audit fields stamped in-memory too.
	if putItem.Version != 1 {
		t.Errorf("putItem.Version = %d, want 1", putItem.Version)
	}
}

func TestBatchWrite_Put_NoConditionExpression(t *testing.T) {
	// BatchWriteItem's types.Put has no ConditionExpression field at all, so
	// there's nothing to assert is absent on the wire -- this test instead
	// documents/verifies that Put's write request is built from the plain
	// stamped item with no conditional protection baked in, by round
	// tripping through stampAndMarshal directly and comparing.
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}
	wantAV, _, err := stampAndMarshal(context.Background(), testDB(nil), item)
	if err != nil {
		t.Fatalf("stampAndMarshal() error = %v", err)
	}

	item2 := &widget{Model: Model{ID: item.ID}, Name: "gizmo"}
	var captured map[string]types.AttributeValue
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			captured = in.RequestItems["test-table"][0].PutRequest.Item
			return &dynamodb.BatchWriteItemOutput{}, nil
		},
	})
	if err := BatchWrite[widget](context.Background(), db).Put(item2).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := attrS(t, captured, "PK"), attrS(t, wantAV, "PK"); got != want {
		t.Errorf("Item[PK] = %q, want %q", got, want)
	}
	if got, want := attrN(t, captured, "Version"), attrN(t, wantAV, "Version"); got != want {
		t.Errorf("Item[Version] = %q, want %q", got, want)
	}
}

func TestBatchWrite_Chunking(t *testing.T) {
	const n = 30
	items := make([]*widget, n)
	for i := range items {
		items[i] = &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}
	}

	var calls int
	var chunkSizes []int
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			calls++
			reqs := in.RequestItems["test-table"]
			chunkSizes = append(chunkSizes, len(reqs))
			if len(reqs) > 25 {
				t.Errorf("chunk size = %d, want <= 25", len(reqs))
			}
			return &dynamodb.BatchWriteItemOutput{}, nil
		},
	})

	if err := BatchWrite[widget](context.Background(), db).Put(items...).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("BatchWriteItem calls = %d, want 2", calls)
	}
	if chunkSizes[0] != 25 || chunkSizes[1] != 5 {
		t.Errorf("chunkSizes = %v, want [25 5]", chunkSizes)
	}
}

func TestBatchWrite_UnprocessedItemsRetry(t *testing.T) {
	item1 := &widget{Model: Model{ID: uuid.New()}, Name: "a"}
	item2 := &widget{Model: Model{ID: uuid.New()}, Name: "b"}

	var calls int
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			calls++
			reqs := in.RequestItems["test-table"]
			if calls == 1 {
				if len(reqs) != 2 {
					t.Errorf("call 1 reqs len = %d, want 2", len(reqs))
				}
				return &dynamodb.BatchWriteItemOutput{
					UnprocessedItems: map[string][]types.WriteRequest{
						"test-table": reqs[1:],
					},
				}, nil
			}
			if len(reqs) != 1 {
				t.Errorf("retry reqs len = %d, want 1", len(reqs))
			}
			return &dynamodb.BatchWriteItemOutput{}, nil
		},
	})

	if err := BatchWrite[widget](context.Background(), db).Put(item1, item2).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 2 {
		t.Errorf("BatchWriteItem calls = %d, want 2 (initial + 1 retry)", calls)
	}
}

func TestBatchWrite_RetryExhaustion(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}

	var calls int
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			calls++
			reqs := in.RequestItems["test-table"]
			return &dynamodb.BatchWriteItemOutput{
				UnprocessedItems: map[string][]types.WriteRequest{"test-table": reqs},
			}, nil
		},
	})

	err := BatchWrite[widget](context.Background(), db).Put(item).Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil after retry exhaustion")
	}
	if calls != batchMaxRetries+1 {
		t.Errorf("BatchWriteItem calls = %d, want %d", calls, batchMaxRetries+1)
	}
	if errors.Is(err, ErrOptimisticLock) {
		t.Errorf("error = %v, should not wrap ErrOptimisticLock", err)
	}
}

func TestBatchWrite_Put_MarshalError_SkipsSubsequentItems(t *testing.T) {
	// Two separate Put(...) calls: the first sets b.err; the second call's
	// loop must immediately hit Put's "if b.err != nil { return b }"
	// early-skip rather than attempt to stamp/marshal bad2.
	bad1 := &badMarshalItem{Model: Model{ID: uuid.New()}}
	bad2 := &badMarshalItem{Model: Model{ID: uuid.New()}}

	var called bool
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, _ *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			called = true
			return &dynamodb.BatchWriteItemOutput{}, nil
		},
	})

	err := BatchWrite[badMarshalItem](context.Background(), db).Put(bad1).Put(bad2).Run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if called {
		t.Error("BatchWriteItem was called, want no call once Put has recorded a marshal error")
	}
}

func TestBatchWrite_Empty_NoOp(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, _ *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			called = true
			return &dynamodb.BatchWriteItemOutput{}, nil
		},
	})

	if err := BatchWrite[widget](context.Background(), db).Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called {
		t.Error("BatchWriteItem was called, want no call for an empty batch")
	}
}

func TestBatchWrite_ClientError(t *testing.T) {
	item := &widget{Model: Model{ID: uuid.New()}, Name: "gizmo"}
	db := testDB(&stubClient{
		batchWriteItemFn: func(_ context.Context, _ *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
			return nil, errors.New("aws: throttled")
		},
	})

	if err := BatchWrite[widget](context.Background(), db).Put(item).Run(); err == nil {
		t.Fatal("expected error, got nil")
	}
}
