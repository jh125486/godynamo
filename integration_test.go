//go:build integration

// This file exercises the godynamo package end-to-end against a real
// DynamoDB-compatible backend (Gopherstack, run through testcontainers-go).
// It is excluded from the default `go test ./...` run by the "integration"
// build tag, since it requires a working Docker daemon. Run it explicitly
// with:
//
//	go test -tags=integration ./...
//
// If Docker isn't available, every test here skips (via t.Skipf) rather
// than failing the run.
package godynamo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/blackbirdworks/gopherstack/modules/gopherstack"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"

	"github.com/jh125486/godynamo"
)

// Product is a root-entity fixture: no dynamo tag, so it uses the default
// PK/SK ("Product#{ID}"/"Product#{ID}") and is discoverable via the type
// index (GSI1PK = "Product").
type Product struct {
	godynamo.Model
	Name  string
	Price int
}

// CustomerOrder is a parent-grouped fixture exercising the tag-templated key
// path: PK is built from CustomerID, SK from Status (plus the
// always-appended ID), so orders for the same customer share a partition
// and can be range queried by status prefix. (Named CustomerOrder, not
// Order, to avoid colliding with the unrelated Order fixture in
// keys_test.go — both files end up in the same test binary under
// -tags=integration.)
type CustomerOrder struct {
	godynamo.Model `dynamo:"pk:CustomerID;sk:Status"`
	CustomerID     string
	Status         string
}

// startDynamoDBLocal starts a Gopherstack container (a Go-native AWS
// emulator with a DynamoDB implementation) and returns a *dynamodb.Client
// pointed at it, plus a cleanup func. It calls t.Skipf (never t.Fatal) if
// Docker/container startup fails for any reason, since this environment may
// not have a Docker daemon running.
func startDynamoDBLocal(t *testing.T) *dynamodb.Client {
	t.Helper()
	ctx := context.Background()

	container, err := gopherstack.Run(ctx, gopherstack.DefaultImage)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	endpoint, err := container.BaseURL(ctx)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("dummy", "dummy", "")),
	)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}

	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

// createTestTable creates the single-table-design table (PK/SK base table
// plus the GSI1 type index, using the default "GSI1PK" partition-key
// attribute name) that godynamo requires, and waits for it to become
// ACTIVE. It returns the table name.
func createTestTable(t *testing.T, client *dynamodb.Client) string {
	t.Helper()
	return createTestTableWithGSI1PKAttr(t, client, "GSI1PK")
}

// createTestTableWithGSI1PKAttr is [createTestTable] parameterized by the
// GSI1 partition-key attribute name, to exercise [godynamo.WithGSI1PKAttr]
// against a real backend whose GSI schema actually uses a non-default
// attribute name.
func createTestTableWithGSI1PKAttr(t *testing.T, client *dynamodb.Client, gsi1PKAttr string) string {
	t.Helper()
	ctx := context.Background()
	table := "godynamo-integration-" + uuid.NewString()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String(gsi1PKAttr), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String("GSI1"),
				KeySchema: []types.KeySchemaElement{
					{AttributeName: aws.String(gsi1PKAttr), KeyType: types.KeyTypeHash},
					{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
				},
				Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
			},
		},
	})
	if err != nil {
		t.Skipf("docker unavailable (create table failed): %v", err)
	}

	waiter := dynamodb.NewTableExistsWaiter(client, func(o *dynamodb.TableExistsWaiterOptions) {
		o.MinDelay = 200 * time.Millisecond
		o.MaxDelay = 2 * time.Second
	})
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 30*time.Second); err != nil {
		t.Skipf("docker unavailable (table never became active): %v", err)
	}

	return table
}

// TaggedItem exercises the dynamodbav-tag resolution path for real: its
// Nickname Go field is marshaled under the "nick_name" attribute (per its
// struct tag), so filtering by the Go field name "Nickname" only matches
// end-to-end if QueryBuilder.Filter/ScanBuilder.Filter actually resolve it
// to "nick_name" before the request is sent.
type TaggedItem struct {
	godynamo.Model
	Nickname string `dynamodbav:"nick_name"`
	Score    int
}

// CompressibleDoc exercises dynamo:"compress" end-to-end: Notes is
// transparently gzip-compressed on write and decompressed on read.
type CompressibleDoc struct {
	godynamo.Model
	Title string
	Notes string `dynamo:"compress"`
}

// TestIntegration_EndToEnd walks the full godynamo surface against a real
// DynamoDB Local container: CRUD (incl. optimistic locking), Update,
// Delete, Query (both type-index and base-table/tag-templated modes),
// Scan, Batch, and Transact.
func TestIntegration_EndToEnd(t *testing.T) {
	client := startDynamoDBLocal(t)
	table := createTestTable(t, client)
	db := godynamo.New(client, table)
	ctx := context.Background()

	t.Run("PutGetVersionOptimisticLock", func(t *testing.T) {
		p := &Product{Name: "Widget", Price: 100}
		if err := godynamo.Put(ctx, db, p); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if p.Version != 1 {
			t.Fatalf("expected Version 1 after create, got %d", p.Version)
		}
		if p.CreatedAt.IsZero() {
			t.Fatal("expected CreatedAt to be stamped")
		}

		got, err := godynamo.Get[Product](ctx, db, p.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Name != "Widget" || got.Version != 1 {
			t.Fatalf("unexpected item after Get: %+v", got)
		}

		// Keep a stale copy (Version 1) before the re-Put bumps the live
		// item to Version 2.
		stale := *got

		got.Price = 150
		if err := godynamo.Put(ctx, db, got); err != nil {
			t.Fatalf("re-Put: %v", err)
		}
		if got.Version != 2 {
			t.Fatalf("expected Version 2 after re-Put, got %d", got.Version)
		}

		after, err := godynamo.Get[Product](ctx, db, p.ID)
		if err != nil {
			t.Fatalf("Get after re-Put: %v", err)
		}
		if after.Price != 150 || after.Version != 2 {
			t.Fatalf("unexpected item after re-Put: %+v", after)
		}

		// The stale (Version 1) copy should now fail its optimistic-lock
		// condition (live Version is 2).
		err = godynamo.Put(ctx, db, &stale)
		if !errors.Is(err, godynamo.ErrOptimisticLock) {
			t.Fatalf("expected ErrOptimisticLock from stale Put, got %v", err)
		}
	})

	t.Run("Update", func(t *testing.T) {
		p := &Product{Name: "Gadget", Price: 10}
		if err := godynamo.Put(ctx, db, p); err != nil {
			t.Fatalf("Put: %v", err)
		}

		updated, err := godynamo.Update[Product](db, p.ID).
			Set("Name", "Gadget Pro").
			Add("Price", 5).
			IfVersion(p.Version).
			Run(ctx)
		if err != nil {
			t.Fatalf("Update.Run: %v", err)
		}
		if updated.Name != "Gadget Pro" || updated.Price != 15 {
			t.Fatalf("unexpected item after Update: %+v", updated)
		}
		if updated.Version != p.Version+1 {
			t.Fatalf("expected Version %d, got %d", p.Version+1, updated.Version)
		}

		again, err := godynamo.Get[Product](ctx, db, p.ID)
		if err != nil {
			t.Fatalf("Get after Update: %v", err)
		}
		if again.Name != "Gadget Pro" || again.Price != 15 {
			t.Fatalf("Get after Update disagrees with Update's return value: %+v", again)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		p := &Product{Name: "Doomed", Price: 1}
		if err := godynamo.Put(ctx, db, p); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := godynamo.Delete[Product](ctx, db, p.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := godynamo.Get[Product](ctx, db, p.ID)
		if !errors.Is(err, godynamo.ErrNotFound) {
			t.Fatalf("expected ErrNotFound after Delete, got %v", err)
		}
	})

	t.Run("QueryTypeIndex", func(t *testing.T) {
		want := make([]string, 0, 3)
		for i := range 3 {
			p := &Product{Name: fmt.Sprintf("QP-%d", i), Price: i}
			if err := godynamo.Put(ctx, db, p); err != nil {
				t.Fatalf("Put: %v", err)
			}
			want = append(want, p.ID.String())
		}

		items, err := godynamo.Query[Product](db).All(ctx)
		if err != nil {
			t.Fatalf("Query.All: %v", err)
		}
		if len(items) < len(want) {
			t.Fatalf("expected at least %d Products from type-index query, got %d", len(want), len(items))
		}
		seen := make(map[string]bool, len(items))
		for _, it := range items {
			seen[it.ID.String()] = true
		}
		for _, id := range want {
			if !seen[id] {
				t.Fatalf("Product %s missing from type-index query results", id)
			}
		}
	})

	t.Run("QueryBaseTableTagTemplated", func(t *testing.T) {
		customerID := uuid.NewString()
		orders := []*CustomerOrder{
			{CustomerID: customerID, Status: "pending"},
			{CustomerID: customerID, Status: "pending"},
			{CustomerID: customerID, Status: "shipped"},
		}
		for _, o := range orders {
			if err := godynamo.Put(ctx, db, o); err != nil {
				t.Fatalf("Put order: %v", err)
			}
		}

		pk := godynamo.PK(&CustomerOrder{CustomerID: customerID})
		items, err := godynamo.Query[CustomerOrder](db).
			WherePK(pk).
			SKBeginsWith("CustomerOrder#pending").
			All(ctx)
		if err != nil {
			t.Fatalf("Query.All: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 pending orders, got %d", len(items))
		}
		for _, it := range items {
			if it.Status != "pending" {
				t.Fatalf("expected only pending orders, got status %q", it.Status)
			}
		}
	})

	t.Run("Scan", func(t *testing.T) {
		p := &Product{Name: "Scannable", Price: 42}
		if err := godynamo.Put(ctx, db, p); err != nil {
			t.Fatalf("Put: %v", err)
		}

		items, err := godynamo.Scan[Product](db).All(ctx)
		if err != nil {
			t.Fatalf("Scan.All: %v", err)
		}
		found := false
		for _, it := range items {
			if it.ID == p.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected Scan.All to include Product %s", p.ID)
		}
	})

	t.Run("BatchWriteAndGet", func(t *testing.T) {
		p1 := &Product{Name: "Batch1", Price: 1}
		p2 := &Product{Name: "Batch2", Price: 2}

		if err := godynamo.BatchWrite[Product](db).Put(p1, p2).Run(ctx); err != nil {
			t.Fatalf("BatchWrite.Run: %v", err)
		}

		items, err := godynamo.BatchGet[Product](db).IDs(p1.ID, p2.ID).Run(ctx)
		if err != nil {
			t.Fatalf("BatchGet.Run: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items from BatchGet, got %d", len(items))
		}
	})

	t.Run("TransactWriteAndGet", func(t *testing.T) {
		guard := &Product{Name: "Guard", Price: 1}
		if err := godynamo.Put(ctx, db, guard); err != nil {
			t.Fatalf("Put guard: %v", err)
		}

		newItem := &Product{Name: "Transacted", Price: 99}
		err := godynamo.TransactWrite(db).
			Put(newItem).
			ConditionCheck(guard, guard.Version).
			Run(ctx)
		if err != nil {
			t.Fatalf("TransactWrite.Run: %v", err)
		}

		var got Product
		got.ID = newItem.ID
		if err := godynamo.TransactGet(db).Get(&got).Run(ctx); err != nil {
			t.Fatalf("TransactGet.Run: %v", err)
		}
		if got.Name != "Transacted" || got.Price != 99 {
			t.Fatalf("unexpected item from TransactGet: %+v", got)
		}
	})

	t.Run("FilterOperatorsAndTaggedFieldResolution", func(t *testing.T) {
		items := []*TaggedItem{
			{Nickname: "alpha", Score: 10},
			{Nickname: "beta", Score: 20},
			{Nickname: "gamma", Score: 30},
		}
		for _, it := range items {
			if err := godynamo.Put(ctx, db, it); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}

		// Smoke-test a new non-equality Filter operator for real.
		high, err := godynamo.Query[TaggedItem](db).FilterGreaterThan("Score", 15).All(ctx)
		if err != nil {
			t.Fatalf("Query.All (FilterGreaterThan): %v", err)
		}
		gotScores := make(map[int]bool, len(high))
		for _, it := range high {
			gotScores[it.Score] = true
		}
		if !gotScores[20] || !gotScores[30] || gotScores[10] {
			t.Fatalf("FilterGreaterThan(Score, 15) = %+v, want scores {20,30} present and 10 absent", high)
		}

		// Filter("Nickname", ...) exercises dynamodbav resolution for real:
		// Nickname is marshaled under "nick_name", so this only matches if
		// QueryBuilder.Filter resolved the Go field name correctly.
		matched, err := godynamo.Query[TaggedItem](db).Filter("Nickname", "beta").All(ctx)
		if err != nil {
			t.Fatalf("Query.All (tagged Filter): %v", err)
		}
		if len(matched) != 1 || matched[0].Nickname != "beta" {
			t.Fatalf("Filter(Nickname, beta) = %+v, want exactly one item with Nickname=beta", matched)
		}

		// Same dynamodbav-resolution smoke test through ScanBuilder, using a
		// different new operator (FilterBeginsWith).
		scanned, err := godynamo.Scan[TaggedItem](db).FilterBeginsWith("Nickname", "al").All(ctx)
		if err != nil {
			t.Fatalf("Scan.All (FilterBeginsWith, tagged): %v", err)
		}
		if len(scanned) != 1 || scanned[0].Nickname != "alpha" {
			t.Fatalf("Scan FilterBeginsWith(Nickname, al) = %+v, want exactly one item with Nickname=alpha", scanned)
		}
	})

	t.Run("CustomGSI1PKAttr", func(t *testing.T) {
		// Requires its own table whose GSI's partition key uses a
		// non-default attribute name, to prove WithGSI1PKAttr changes both
		// what Put writes and what Query's type-index mode reads, for real.
		table2 := createTestTableWithGSI1PKAttr(t, client, "TypeKey")
		db2 := godynamo.New(client, table2, godynamo.WithGSI1PKAttr("TypeKey"))

		p := &Product{Name: "CustomAttrWidget", Price: 5}
		if err := godynamo.Put(ctx, db2, p); err != nil {
			t.Fatalf("Put: %v", err)
		}

		items, err := godynamo.Query[Product](db2).All(ctx)
		if err != nil {
			t.Fatalf("Query.All: %v", err)
		}
		found := false
		for _, it := range items {
			if it.ID == p.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected Query.All (custom GSI1PK attr) to include Product %s", p.ID)
		}
	})

	t.Run("Statement", func(t *testing.T) {
		p := &Product{Name: "PartiQL'd", Price: 7}
		if err := godynamo.Put(ctx, db, p); err != nil {
			t.Fatalf("Put: %v", err)
		}

		pk, sk := godynamo.Keys(p)
		stmt := fmt.Sprintf(`SELECT * FROM %q WHERE PK = ? AND SK = ?`, table)
		items, err := godynamo.Statement[Product](db, stmt, pk, sk).All(ctx)
		if err != nil {
			t.Fatalf("Statement.All: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item from Statement.All, got %d", len(items))
		}
		if items[0].ID != p.ID || items[0].Name != "PartiQL'd" || items[0].Price != 7 {
			t.Fatalf("unexpected item from Statement.All: %+v", items[0])
		}
	})

	t.Run("CompressTag", func(t *testing.T) {
		// A long, repetitive string: gzip needs redundancy to actually
		// shrink something, so this isn't just random/high-entropy text.
		notes := strings.Repeat(
			"The quick brown fox jumps over the lazy dog. godynamo's dynamo:\"compress\" tag saves space. ",
			200,
		)

		doc := &CompressibleDoc{Title: "Compressible", Notes: notes}
		if err := godynamo.Put(ctx, db, doc); err != nil {
			t.Fatalf("Put: %v", err)
		}

		// Round-trip correctness: Get must return the original,
		// decompressed value transparently.
		got, err := godynamo.Get[CompressibleDoc](ctx, db, doc.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Notes != notes {
			t.Fatalf("Get: Notes did not round-trip correctly (len got=%d, want=%d)", len(got.Notes), len(notes))
		}
		if got.Title != "Compressible" {
			t.Fatalf("Get: Title = %q, want %q", got.Title, "Compressible")
		}

		// Actual size reduction: fetch the raw stored item directly (not
		// through godynamo's Get, which would decompress it) and inspect
		// the Notes attribute's real shape and byte length.
		pk, sk := godynamo.Keys(doc)
		raw, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: sk},
			},
		})
		if err != nil {
			t.Fatalf("raw GetItem: %v", err)
		}

		notesAttr, ok := raw.Item["Notes"].(*types.AttributeValueMemberB)
		if !ok {
			t.Fatalf("raw Notes attribute = %T, want *types.AttributeValueMemberB (Binary)", raw.Item["Notes"])
		}

		uncompressedJSON, err := json.Marshal(notes)
		if err != nil {
			t.Fatalf("json.Marshal(notes): %v", err)
		}

		if len(notesAttr.Value) >= len(uncompressedJSON) {
			t.Fatalf(
				"compression did not reduce size: stored Binary attribute is %d bytes, uncompressed JSON would be %d bytes",
				len(notesAttr.Value), len(uncompressedJSON),
			)
		}
		t.Logf(
			"CompressTag: uncompressed JSON = %d bytes, stored (compressed) = %d bytes (%.1f%% of original)",
			len(uncompressedJSON), len(notesAttr.Value),
			100*float64(len(notesAttr.Value))/float64(len(uncompressedJSON)),
		)
	})
}
