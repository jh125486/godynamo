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

func TestStatement_All_Basic(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	av1, _ := attributevalue.MarshalMap(item1)

	var captured *dynamodb.ExecuteStatementInput
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, in *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			captured = in
			return &dynamodb.ExecuteStatementOutput{Items: []map[string]types.AttributeValue{av1}}, nil
		},
	})

	stmt := `SELECT * FROM "widgets" WHERE PK = ? AND SK = ?`
	got, err := Statement[widget](context.Background(), db, stmt, "widget#pk", "widget#sk").All()
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}

	if captured == nil {
		t.Fatal("ExecuteStatement was not called")
	}
	if captured.Statement == nil || *captured.Statement != stmt {
		t.Errorf("Statement = %v, want %q", captured.Statement, stmt)
	}
	if len(captured.Parameters) != 2 {
		t.Fatalf("Parameters = %v, want 2 entries", captured.Parameters)
	}
	if s, ok := captured.Parameters[0].(*types.AttributeValueMemberS); !ok || s.Value != "widget#pk" {
		t.Errorf("Parameters[0] = %v, want S(widget#pk)", captured.Parameters[0])
	}
	if s, ok := captured.Parameters[1].(*types.AttributeValueMemberS); !ok || s.Value != "widget#sk" {
		t.Errorf("Parameters[1] = %v, want S(widget#sk)", captured.Parameters[1])
	}

	if len(got) != 1 || got[0].Name != "one" {
		t.Errorf("All() = %+v, want [one]", got)
	}
}

func TestStatement_All_NoParams(t *testing.T) {
	var captured *dynamodb.ExecuteStatementInput
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, in *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			captured = in
			return &dynamodb.ExecuteStatementOutput{}, nil
		},
	})

	if _, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(captured.Parameters) != 0 {
		t.Errorf("Parameters = %v, want empty", captured.Parameters)
	}
}

func TestStatement_All_Pagination(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	item2 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "two"}
	av1, _ := attributevalue.MarshalMap(item1)
	av2, _ := attributevalue.MarshalMap(item2)

	token := "opaque-next-token"

	var calls int
	var secondCallToken *string
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, in *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			calls++
			if calls == 1 {
				return &dynamodb.ExecuteStatementOutput{
					Items:     []map[string]types.AttributeValue{av1},
					NextToken: &token,
				}, nil
			}
			secondCallToken = in.NextToken
			return &dynamodb.ExecuteStatementOutput{
				Items: []map[string]types.AttributeValue{av2},
			}, nil
		},
	})

	got, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).All()
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("ExecuteStatement called %d times, want 2", calls)
	}
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "two" {
		t.Errorf("All() = %+v, want [one, two]", got)
	}
	if secondCallToken == nil || *secondCallToken != token {
		t.Errorf("second call NextToken = %v, want %q (passed straight through, no decoding)", secondCallToken, token)
	}
}

func TestStatement_Page_NoMorePages(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	av1, _ := attributevalue.MarshalMap(item1)

	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			return &dynamodb.ExecuteStatementOutput{Items: []map[string]types.AttributeValue{av1}}, nil
		},
	})

	items, nextCursor, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).Page("")
	if err != nil {
		t.Fatalf("Page() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != "one" {
		t.Errorf("Page() items = %+v, want [one]", items)
	}
	if nextCursor != "" {
		t.Errorf("nextCursor = %q, want empty (no NextToken in stub response)", nextCursor)
	}
}

func TestStatement_Page_MorePagesAndCursorPassthrough(t *testing.T) {
	item1 := widget{Model: Model{ID: uuid.New(), Type: "widget"}, Name: "one"}
	av1, _ := attributevalue.MarshalMap(item1)
	token := "opaque-token-abc"

	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			return &dynamodb.ExecuteStatementOutput{
				Items:     []map[string]types.AttributeValue{av1},
				NextToken: &token,
			}, nil
		},
	})

	items, nextCursor, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).Page("")
	if err != nil {
		t.Fatalf("Page() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Page() items = %+v, want 1 item", items)
	}
	if nextCursor != token {
		t.Errorf("nextCursor = %q, want %q (dereferenced NextToken)", nextCursor, token)
	}

	// A supplied cursor must be passed straight through as NextToken,
	// unmodified -- no decoding, unlike QueryBuilder.Page.
	var capturedToken *string
	db2 := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, in *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			capturedToken = in.NextToken
			return &dynamodb.ExecuteStatementOutput{}, nil
		},
	})
	if _, _, err := Statement[widget](context.Background(), db2, `SELECT * FROM "widgets"`).Page(nextCursor); err != nil {
		t.Fatalf("Page(cursor) error = %v", err)
	}
	if capturedToken == nil || *capturedToken != nextCursor {
		t.Errorf("NextToken = %v, want %q passed straight through", capturedToken, nextCursor)
	}
}

func TestStatement_Limit(t *testing.T) {
	var captured *dynamodb.ExecuteStatementInput
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, in *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			captured = in
			return &dynamodb.ExecuteStatementOutput{}, nil
		},
	})

	if _, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).Limit(25).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.Limit == nil || *captured.Limit != 25 {
		t.Errorf("Limit = %v, want 25", captured.Limit)
	}
}

func TestStatement_ConsistentRead(t *testing.T) {
	var captured *dynamodb.ExecuteStatementInput
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, in *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			captured = in
			return &dynamodb.ExecuteStatementOutput{}, nil
		},
	})

	if _, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).ConsistentRead(true).All(); err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if captured.ConsistentRead == nil || !*captured.ConsistentRead {
		t.Errorf("ConsistentRead = %v, want true", captured.ConsistentRead)
	}
}

func TestStatement_ParamMarshalError_SurfacesLazily(t *testing.T) {
	var called bool
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			called = true
			return &dynamodb.ExecuteStatementOutput{}, nil
		},
	})

	// Statement itself must not error -- it returns *StatementBuilder[T] per
	// its signature, so a marshal failure has to be deferred to a terminal
	// method.
	b := Statement[widget](context.Background(), db, `SELECT * FROM "widgets" WHERE Bad = ?`, badMarshalField{})

	if _, err := b.All(); err == nil {
		t.Fatal("All() expected error from bad marshal param, got nil")
	}
	if called {
		t.Error("ExecuteStatement was called, want no call when a param fails to marshal")
	}

	_, _, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets" WHERE Bad = ?`, badMarshalField{}).Page("")
	if err == nil {
		t.Fatal("Page() expected error from bad marshal param, got nil")
	}
}

func TestStatement_All_ClientError(t *testing.T) {
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			return nil, errors.New("aws: internal server error")
		},
	})

	if _, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).All(); err == nil {
		t.Fatal("All() expected error, got nil")
	}
}

func TestStatement_Page_ClientError(t *testing.T) {
	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			return nil, errors.New("aws: internal server error")
		},
	})

	if _, _, err := Statement[widget](context.Background(), db, `SELECT * FROM "widgets"`).Page(""); err == nil {
		t.Fatal("Page() expected error, got nil")
	}
}

func TestStatement_All_UnmarshalError(t *testing.T) {
	src := badUnmarshalItem{Model: Model{ID: uuid.New(), Type: "badUnmarshalItem"}}
	av, _ := attributevalue.MarshalMap(src)

	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			return &dynamodb.ExecuteStatementOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	if _, err := Statement[badUnmarshalItem](context.Background(), db, `SELECT * FROM "widgets"`).All(); err == nil {
		t.Fatal("All() expected unmarshal error, got nil")
	}
}

func TestStatement_Page_UnmarshalError(t *testing.T) {
	src := badUnmarshalItem{Model: Model{ID: uuid.New(), Type: "badUnmarshalItem"}}
	av, _ := attributevalue.MarshalMap(src)

	db := testDB(&stubClient{
		executeStatementFn: func(_ context.Context, _ *dynamodb.ExecuteStatementInput) (*dynamodb.ExecuteStatementOutput, error) {
			return &dynamodb.ExecuteStatementOutput{Items: []map[string]types.AttributeValue{av}}, nil
		},
	})

	if _, _, err := Statement[badUnmarshalItem](context.Background(), db, `SELECT * FROM "widgets"`).Page(""); err == nil {
		t.Fatal("Page() expected unmarshal error, got nil")
	}
}
