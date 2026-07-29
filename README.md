# godynamo

`godynamo` is an opinionated, fluent, generics-based library for modeling AWS
DynamoDB single-table designs in Go. You embed a common `Model` struct in
your entity types, describe their partition/sort keys with a single struct
tag, and get generic CRUD, Query, Scan, Batch, and Transact helpers built on
top of the AWS SDK for Go v2 — with optimistic locking and audit fields
(`CreatedAt`/`CreatedBy`/`UpdatedAt`/`UpdatedBy`/`Version`) handled for you
on every write.

## Installation

```sh
go get github.com/jh125486/godynamo
```

## Quickstart

```go
package main

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/jh125486/godynamo"
)

// Task embeds godynamo.Model with no dynamo tag, so it uses the default
// (ID-only) PK/SK and is discoverable via Query's type-index mode.
type Task struct {
	godynamo.Model
	Project string
	Status  string
	Title   string
}

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	client := dynamodb.NewFromConfig(cfg)
	db := godynamo.New(client, "my-table")

	task := &Task{Project: "roadmap", Status: "open", Title: "Write README"}
	if err := godynamo.Put(ctx, db, task); err != nil {
		log.Fatal(err)
	}

	got, err := godynamo.Get[Task](ctx, db, task.ID)
	if err != nil {
		log.Fatal(err)
	}

	openTasks, err := godynamo.Query[Task](ctx, db).
		Filter("Status", "open").
		All()
	if err != nil {
		log.Fatal(err)
	}

	updated, err := godynamo.Update[Task](db, task.ID).
		Set("Status", "done").
		IfVersion(got.Version).
		Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if err := godynamo.Delete[Task](ctx, db, task.ID); err != nil {
		log.Fatal(err)
	}

	_ = openTasks
	_ = updated
}
```

## The `dynamo` tag

The `dynamo` struct tag lives on the embedded `Model` field (Model's key is
built from several sibling fields, so it can't live on any single one of
them). It holds one or two `;`-separated clauses:

```go
type Order struct {
    godynamo.Model `dynamo:"pk:CustomerID;sk:Status,CreatedDate"`
    CustomerID  string
    Status      string
    CreatedDate string
}
```

- `pk:Field1,Field2` — Go field names (comma-separated) used to build the
  partition key: `"StructName#{Field1}#{Field2}"`.
- `sk:Field1,Field2` — Go field names used to build the sort key:
  `"StructName#{Field1}#{Field2}#{ID}"`.
- Either clause may be omitted; if omitted, the default is
  `"StructName#{ID}"`.
- `ID` is always appended as the sort key's final component, unless the
  `sk` clause's last listed field is already literally `ID` (avoids a
  double append).

`PK(v)`, `SK(v)`, and `Keys(v)` compute these strings directly from a struct
value (or pointer) for callers that need a raw key, e.g. for `WherePK`.

## Raw PartiQL with `Statement`

`Statement[T]` is a raw-PartiQL escape hatch for callers who want SQL-like
`SELECT` semantics, or a query shape the fluent `Query[T]` builder can't
express. Unlike `Query`, it has no type-index/base-table mode distinction —
you write the whole statement (including the table name) yourself:

```go
pk, sk := godynamo.Keys(task)
items, err := godynamo.Statement[Task](ctx, db,
	`SELECT * FROM "my-table" WHERE PK = ? AND SK = ?`,
	pk, sk,
).All()
```

`params` are positional values for the statement's `?` placeholders,
marshaled individually (not as a struct-to-map). `.Limit(n)` and
`.ConsistentRead(v)` set the matching `ExecuteStatementInput` fields —
`ConsistentRead` is PartiQL-specific and has no `Query`/`Scan` equivalent.
`.All()` paginates to exhaustion using DynamoDB's own opaque `NextToken`
(no cursor encoding needed); `.Page(cursor)` runs a single call and hands
that token back to the caller as-is.

Only PartiQL `SELECT` is supported — PartiQL writes, `BatchExecuteStatement`,
and `ExecuteTransaction` are out of scope; use `Put`/`Update`/`Delete`,
`BatchWrite`, and `TransactWrite` instead.

## The type-index GSI

Type-index queries (`Query[T](ctx, db)` with no `WherePK` call) list every
item of a given Go type by querying a global secondary index for
`GSI1PK = "TypeName"`. Your table must have a GSI (default name `"GSI1"`,
overridable via `WithGSI1Name`) with partition key `GSI1PK` (String) and
sort key `SK` (String) — the GSI reuses the base table's `SK` attribute as
its own range key rather than requiring a separate `GSI1SK` attribute.
Creating the table and index is out of scope for this package; see
`integration_test.go` for a `CreateTable` call that sets one up correctly.

## What's supported

- **CRUD**: `Put` (create-or-update with optimistic locking via `Version`),
  `Get`, `Delete`, `Update` (fluent `Set`/`Add`/`IfVersion`).
- **Query**: type-index mode and base-table mode, `SKEquals` /
  `SKBeginsWith` / `SKBetween`, equality `Filter`, `Limit`, exhaustive
  `All()`, and cursor-based `Page(cursor)`.
- **Scan**: base-table scan with equality `Filter`, `Limit`, and
  `Parallel(totalSegments)` for concurrent segment scans.
- **Batch**: `BatchGet`/`BatchWrite`, chunked to AWS's per-call limits (100
  keys / 25 write requests) with automatic retry of
  `UnprocessedKeys`/`UnprocessedItems`.
- **Transact**: `TransactGet`/`TransactWrite` for heterogeneous, atomic
  multi-item reads and writes (`Put`/`Delete`/`ConditionCheck`), with
  `ErrOptimisticLock` translated from `TransactionCanceledException`.
- **Statement**: `Statement[T]`, a raw-PartiQL `SELECT` escape hatch with
  `Limit`, `ConsistentRead`, exhaustive `All()`, and token-based `Page(cursor)`.

## Known limitations

- `Get`, `Delete`, single-item `Update`, and `BatchGet`/`BatchWrite`'s
  `Delete` all compute their key from a zero-value `T` with only `Model.ID`
  populated, which only produces the correct key for models using the
  default (ID-only) PK/SK. Calling any of them for a model whose `dynamo`
  tag references other sibling fields returns an error wrapping
  `ErrNonDefaultKey` rather than silently computing a wrong key — such
  models need `TransactGet`/`TransactWrite`, which key off a
  caller-populated value instead, or a `Query`/`Scan`.
- `BatchWrite` has no per-item `ConditionExpression` support — this is a
  hard AWS API limitation. Puts through `BatchWrite` are unconditional: no
  `attribute_not_exists` protection against a double-create, no
  optimistic-lock check against a concurrent update.
- `Query` and `Scan` filters (`Filter(field, value)`) only support
  equality; other operators are out of scope.
- `TransactGet`/`TransactWrite` hard-error if more than 100 items/entries
  are queued rather than silently chunking — chunking a transaction would
  break its atomicity guarantee, so callers must keep transactions at or
  under AWS's 100-item limit themselves.
