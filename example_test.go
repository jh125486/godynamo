package godynamo_test

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/jh125486/godynamo"
)

// Task is a small single-table-design entity. It embeds [godynamo.Model]
// for the ID/Type/audit/Version bookkeeping fields, and its dynamo tag
// groups Tasks by Project (the partition key) ordered by Status (the sort
// key, with ID always appended last).
type Task struct {
	godynamo.Model `dynamo:"pk:Project;sk:Status"`
	Project        string
	Status         string
	Title          string
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

	task := &Task{Project: "roadmap", Status: "open", Title: "Write README"}
	if err := godynamo.Put(ctx, db, task); err != nil {
		panic(err)
	}

	got, err := godynamo.Get[Task](ctx, db, task.ID)
	if err != nil {
		panic(err)
	}
	_ = got

	openTasks, err := godynamo.Query[Task](ctx, db).
		WherePK(godynamo.PK(&Task{Project: "roadmap"})).
		SKBeginsWith("Task#open").
		All()
	if err != nil {
		panic(err)
	}
	_ = openTasks

	updated, err := godynamo.Update[Task](ctx, db, task.ID).
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
