# godynamo

[![Go Reference](https://pkg.go.dev/badge/github.com/jh125486/godynamo)](https://pkg.go.dev/github.com/jh125486/godynamo)
[![Tests](https://github.com/jh125486/godynamo/actions/workflows/test.yaml/badge.svg)](https://github.com/jh125486/godynamo/actions/workflows/test.yaml)
[![CodeQL](https://github.com/jh125486/godynamo/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/jh125486/godynamo/actions/workflows/codeql-analysis.yml)
[![Codecov](https://codecov.io/gh/jh125486/godynamo/branch/main/graph/badge.svg)](https://codecov.io/gh/jh125486/godynamo)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=jh125486_godynamo&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=jh125486_godynamo)
[![Sonar Coverage](https://sonarcloud.io/api/project_badges/measure?project=jh125486_godynamo&metric=coverage)](https://sonarcloud.io/summary/overall?id=jh125486_godynamo)

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
// (ID-only) PK/SK and is discoverable via Query's type-index mode. Notes is
// tagged `dynamo:"compress"`: it's transparently gzip-compressed on write
// and decompressed on read, since it can be a large free-text blob -- see
// "The compress tag" below.
type Task struct {
	godynamo.Model
	Project string
	Status  string
	Title   string
	Notes   string `dynamo:"compress"`
}

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	client := dynamodb.NewFromConfig(cfg)
	db := godynamo.New(client, "my-table")

	task := &Task{Project: "roadmap", Status: "open", Title: "Write README", Notes: "Needs a section on compression."}
	if err := godynamo.Put(ctx, db, task); err != nil {
		log.Fatal(err)
	}
	// Put writes an item shaped roughly like this. This is a
	// simplified/logical illustration of the item's values -- NOT
	// DynamoDB's literal {"S": "..."}-style typed wire format:
	//
	//	{
	//	  "PK":        "Task#5b1f...",   // godynamo.PK(task)
	//	  "SK":        "Task#5b1f...",   // godynamo.SK(task)
	//	  "GSI1PK":    "Task",           // set on create; drives Query's type-index mode
	//	  "ID":        "5b1f...",
	//	  "Type":      "Task",
	//	  "Project":   "roadmap",
	//	  "Status":    "open",
	//	  "Title":     "Write README",
	//	  "Notes":     "<gzip-compressed JSON, shown decompressed here for illustration>",
	//	  "CreatedAt": "2026-07-28T15:04:05Z",
	//	  "CreatedBy": "",
	//	  "UpdatedAt": "2026-07-28T15:04:05Z",
	//	  "UpdatedBy": "",
	//	  "Version":   1
	//	}
	//
	// Notes is stored as DynamoDB Binary (gzip-compressed JSON), not a
	// plain string, because of its `dynamo:"compress"` tag.

	got, err := godynamo.Get[Task](ctx, db, task.ID)
	if err != nil {
		log.Fatal(err)
	}

	openTasks, err := godynamo.Query[Task](db).
		Filter("Status", "open").
		All(ctx)
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

## The `compress` tag

Any field OTHER than the embedded `Model` field, on any struct embedding
`Model`, can carry a `dynamo:"compress"` tag — a different tag *target* from
the Model field's `pk:`/`sk:` clause grammar, with a simpler, bare-keyword
syntax: the tag value, trimmed, must equal exactly `"compress"`.

```go
type Task struct {
    godynamo.Model
    Project string
    Status  string
    Notes   string `dynamo:"compress"`
}
```

On write, instead of DynamoDB's native representation for the field's type,
godynamo JSON-marshals the field's Go value, gzip-compresses the resulting
bytes (`compress/gzip`, stdlib), and stores the compressed bytes as a
DynamoDB Binary attribute under the field's normally-resolved attribute name
(a `dynamodbav` tag on the same field still controls the name — the two tags
are orthogonal). On read, this is reversed transparently: the Binary
attribute is gunzipped, JSON-unmarshaled, and set back on the field — the
caller never sees the compressed bytes. This works for any
JSON-serializable Go field type (string, struct, slice, map, ...), not just
strings.

**Once a field is compressed, it's opaque binary in DynamoDB.** It can no
longer be used in `Filter`, key conditions (`pk:`/`sk:` clauses, `WherePK`,
`SKEquals`/`SKBeginsWith`/`SKBetween`), or `UpdateBuilder.Set`/`.Add`
expressions — you can't query, sort, or update-in-place on a compressed
field's contents. Don't tag a field you need to query, sort, or filter on.

**gzip has fixed per-blob overhead (roughly 18-20 bytes).** Compressing a
small or already-compact field can make it *larger*, not smaller. This is an
opt-in optimization for genuinely large, JSON-serializable, low-query-need
fields — large text blobs, nested documents, logs — not a blanket default
for every string field.

## Raw PartiQL with `Statement`

`Statement[T]` is a raw-PartiQL escape hatch for callers who want SQL-like
`SELECT` semantics, or a query shape the fluent `Query[T]` builder can't
express. Unlike `Query`, it has no type-index/base-table mode distinction —
you write the whole statement (including the table name) yourself:

```go
pk, sk := godynamo.Keys(task)
items, err := godynamo.Statement[Task](db,
	`SELECT * FROM "my-table" WHERE PK = ? AND SK = ?`,
	pk, sk,
).All(ctx)
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

Type-index queries (`Query[T](db)` with no `WherePK` call) list every
item of a given Go type by querying a global secondary index for
`GSI1PK = "TypeName"`. Your table must have a GSI (default name `"GSI1"`,
overridable via `WithGSI1Name`) with partition key `GSI1PK` (String,
overridable via `WithGSI1PKAttr`) and sort key `SK` (String) — the GSI
reuses the base table's `SK` attribute as its own range key rather than
requiring a separate `GSI1SK` attribute. Creating the table and index is
out of scope for this package; see `integration_test.go` for a
`CreateTable` call that sets one up correctly.

## What's supported

- **CRUD**: `Put` (create-or-update with optimistic locking via `Version`),
  `Get`, `Delete`, `Update` (fluent `Set`/`Add`/`IfVersion`).
- **Query**: type-index mode and base-table mode, `SKEquals` /
  `SKBeginsWith` / `SKBetween`, `Filter`/`FilterEqual`/`FilterNotEqual`/
  `FilterGreaterThan`/`FilterGreaterOrEqual`/`FilterLessThan`/
  `FilterLessOrEqual`/`FilterBeginsWith`/`FilterContains`/`FilterExists`/
  `FilterNotExists` (ANDed together), `Limit`, exhaustive `All()`, and
  cursor-based `Page(cursor)`.
- **Scan**: base-table scan with the same `Filter*` operator family as
  `Query`, `Limit`, and `Parallel(totalSegments)` for concurrent segment
  scans.
- **Batch**: `BatchGet`/`BatchWrite`, chunked to AWS's per-call limits (100
  keys / 25 write requests) with automatic retry of
  `UnprocessedKeys`/`UnprocessedItems`.
- **Transact**: `TransactGet`/`TransactWrite` for heterogeneous, atomic
  multi-item reads and writes (`Put`/`Delete`/`ConditionCheck`), with
  `ErrOptimisticLock` translated from `TransactionCanceledException`.
- **Statement**: `Statement[T]`, a raw-PartiQL `SELECT` escape hatch with
  `Limit`, `ConsistentRead`, exhaustive `All()`, and token-based `Page(cursor)`.
- **Compression**: `dynamo:"compress"` transparently gzip-compresses a
  field's JSON representation on write and decompresses it on read — see
  "The `compress` tag" above.

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
- `Query`/`Scan` `Filter*` methods, and `UpdateBuilder.Set`/`Add`, take a Go
  field name on `T` — the same vocabulary the `pk:`/`sk:` tag clauses use —
  not necessarily the marshaled DynamoDB attribute name; a field's
  `dynamodbav:"..."` struct tag override, if present, is resolved
  automatically.
- `TransactGet`/`TransactWrite` hard-error if more than 100 items/entries
  are queued rather than silently chunking — chunking a transaction would
  break its atomicity guarantee, so callers must keep transactions at or
  under AWS's 100-item limit themselves.
- A `dynamo:"compress"` field is opaque binary in DynamoDB once written —
  it cannot be used in `Filter`, key conditions, or `UpdateBuilder.Set`/`Add`
  expressions. gzip also has fixed per-blob overhead (~18-20 bytes), so
  compressing small fields can make them larger, not smaller — see "The
  `compress` tag" above.
