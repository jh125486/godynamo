package godynamo

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// defaultGSI1Name is the default name for the first global secondary index,
// carried in DB config for use by later phases (queries).
const defaultGSI1Name = "GSI1"

// dynamoAPI is the narrow subset of *dynamodb.Client this package needs. It
// exists so tests can substitute a hand-written stub instead of talking to
// real AWS/DynamoDB Local.
type dynamoAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	BatchGetItem(
		ctx context.Context, params *dynamodb.BatchGetItemInput, optFns ...func(*dynamodb.Options),
	) (*dynamodb.BatchGetItemOutput, error)
	BatchWriteItem(
		ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options),
	) (*dynamodb.BatchWriteItemOutput, error)
	TransactGetItems(
		ctx context.Context, params *dynamodb.TransactGetItemsInput, optFns ...func(*dynamodb.Options),
	) (*dynamodb.TransactGetItemsOutput, error)
	TransactWriteItems(
		ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options),
	) (*dynamodb.TransactWriteItemsOutput, error)
}

// DB wraps a DynamoDB client plus the single-table-design config (table
// name, GSI1 index name) and the clock/actor hooks used to populate audit
// fields on writes.
type DB struct {
	client   dynamoAPI
	table    string
	gsi1Name string
	clock    func() time.Time
	actor    func(ctx context.Context) string
}

// New constructs a DB backed by a real *dynamodb.Client. table is the
// DynamoDB table name used for all CRUD operations. Options may override
// the GSI1 index name, clock, and actor-provider defaults; see
// [WithGSI1Name], [WithClock], and [WithActor].
func New(client *dynamodb.Client, table string, opts ...Option) *DB {
	db := &DB{
		client:   client,
		table:    table,
		gsi1Name: defaultGSI1Name,
		clock:    time.Now,
		actor:    func(context.Context) string { return "" },
	}
	for _, opt := range opts {
		opt(db)
	}
	return db
}
