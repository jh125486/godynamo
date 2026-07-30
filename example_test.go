package godynamo_test

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/jh125486/godynamo"
)

// Task is a small single-table-design entity. It embeds [godynamo.Model]
// with no dynamo tag, so it uses the default (ID-only) PK/SK
// ("Task#{ID}"/"Task#{ID}") and is discoverable via Query's type-index mode.
//
// Notes is tagged `dynamo:"compress"`: it's transparently gzip-compressed
// on write and decompressed on read, since it can be a large free-text blob
// -- see the "The compress tag" section of README.md.
type Task struct {
	godynamo.Model
	Project string
	Status  string
	Title   string
	Notes   string `dynamo:"compress"`
}

// Example demonstrates constructing a *godynamo.DB and the fluent call
// shapes for Put, Get, Query, and Update.
//
// This example has no "// Output:" comment, so `go test` compiles it (as a
// check that the code below still matches the real API) but does not
// execute it — see the "Example functions without output comments are
// compiled but not executed" rule in the testing package docs. That's
// intentional here: running this for real requires a live DynamoDB
// endpoint (AWS or DynamoDB Local), which this package's default test
// suite must not depend on.
func Example() {
	ctx := context.Background()

	// A real program builds *dynamodb.Client from AWS config (e.g.
	// config.LoadDefaultConfig) or, for local development, points it at
	// DynamoDB Local via dynamodb.Options.BaseEndpoint. Not shown here
	// since it isn't part of godynamo's own API surface.
	var client *dynamodb.Client

	db := godynamo.New(client, "my-table")

	task := &Task{Project: "roadmap", Status: "open", Title: "Write README", Notes: "Needs a section on compression."}
	if err := godynamo.Put(ctx, db, task); err != nil {
		panic(err)
	}
	// Put writes an item shaped roughly like this (a simplified/logical
	// illustration of the item's values -- NOT DynamoDB's literal
	// {"S": "..."}-style typed wire format):
	//
	//	{
	//	  "PK":        "Task#5b1f...",       // godynamo.PK(task)
	//	  "SK":        "Task#5b1f...",       // godynamo.SK(task)
	//	  "GSI1PK":    "Task",                // set on create; drives Query's type-index mode
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
	// plain string, because of its `dynamo:"compress"` tag -- see the
	// "The compress tag" section of README.md.

	got, err := godynamo.Get[Task](ctx, db, task.ID)
	if err != nil {
		panic(err)
	}
	_ = got

	openTasks, err := godynamo.Query[Task](db).
		Filter("Status", "open").
		All(ctx)
	if err != nil {
		panic(err)
	}
	_ = openTasks

	updated, err := godynamo.Update[Task](db, task.ID).
		Set("Status", "done").
		IfVersion(task.Version).
		Run(ctx)
	if err != nil {
		panic(err)
	}
	_ = updated

	if err := godynamo.Delete[Task](ctx, db, task.ID); err != nil {
		panic(err)
	}
}
