package godynamo

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// modelFieldOf returns the addressable reflect.Value of item's embedded
// Model field. item must be a non-nil pointer to a struct embedding
// [Model]; otherwise modelFieldOf panics, mirroring the panic-on-misuse
// convention of the Phase 1 key builders.
func modelFieldOf(item any) reflect.Value {
	rv := reflect.ValueOf(item)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		panic("godynamo: expected a non-nil pointer to a struct embedding Model")
	}
	elem := rv.Elem()
	mf := elem.FieldByName("Model")
	if !mf.IsValid() || mf.Type() != reflect.TypeFor[Model]() {
		panic("godynamo: struct must embed godynamo.Model")
	}
	return mf
}

// zeroKeys computes the PK/SK for a zero-value T with only its Model.ID
// field populated. See the doc comments on [Get], [Delete], and [Update]
// for the limitation this implies for models with a tag-driven PK/SK. Every
// caller of zeroKeys must first call [requiresDefaultKey] so that limitation
// is enforced rather than silently producing a wrong key.
func zeroKeys[T any](id uuid.UUID) (pk, sk string) {
	var zero T
	mf := modelFieldOf(&zero)
	mf.FieldByName("ID").Set(reflect.ValueOf(id))
	return PK(&zero), SK(&zero)
}

// requiresDefaultKey checks that T uses the default (ID-only) PK/SK — i.e.
// its dynamo tag has no pk:/sk: clause referencing other fields. It exists
// because [zeroKeys] computes a key from a zero-valued T with only Model.ID
// populated: that's only correct when the key is derived solely from ID,
// since any other referenced field would be zero-valued (and thus wrong) on
// a freshly zeroed T.
//
// op names the calling function/method (e.g. "Get[T]") for the error
// message. requiresDefaultKey returns nil for a default-keyed T, or an
// error wrapping [ErrNonDefaultKey] otherwise.
func requiresDefaultKey[T any](op string) error {
	mt := parseTags(reflect.TypeFor[T]())
	if len(mt.pkFields) == 0 && len(mt.skFields) == 0 {
		return nil
	}
	var zero T
	return fmt.Errorf(
		"godynamo: %s requires T to use the default (ID-only) PK/SK; "+
			"%T's dynamo tag references other fields — use Query or TransactGet instead: %w",
		op, zero, ErrNonDefaultKey,
	)
}

// itemKey builds the DynamoDB key map ({"PK": ..., "SK": ...}) used by
// GetItem/UpdateItem/DeleteItem.
func itemKey(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: pk},
		"SK": &types.AttributeValueMemberS{Value: sk},
	}
}

// asOptimisticLockErr translates a DynamoDB ConditionalCheckFailedException
// into ErrOptimisticLock (wrapped with context), leaving other errors
// (also wrapped with context) as-is.
func asOptimisticLockErr(err error, action, typeName string, id uuid.UUID) error {
	if _, ok := errors.AsType[*types.ConditionalCheckFailedException](err); ok {
		return fmt.Errorf("godynamo: %s %s (id=%s): %w", action, typeName, id, ErrOptimisticLock)
	}
	return fmt.Errorf("godynamo: %s %s (id=%s): %w", action, typeName, id, err)
}

// stampAndMarshal implements the create-vs-update audit-stamping logic
// shared by [Put], [BatchWriteBuilder.Put], and [TransactWriteBuilder.Put]:
// it determines create vs. update from whether item's Model.CreatedAt is
// the zero time.Time, stamps the appropriate fields in place (mutating
// item), and marshals the result into a DynamoDB attribute map with
// "PK"/"SK" (and, on create, "GSI1PK") injected.
//
//   - Zero (new item): if item's Model.ID is the zero uuid.UUID, generates a
//     new random ID (so callers can construct a new entity without setting
//     ID themselves, matching the README/Example usage pattern), calls
//     [SetType], stamps CreatedAt/CreatedBy and UpdatedAt/UpdatedBy with
//     db's clock/actor, sets Version to 1, and returns a condition of
//     attribute_not_exists(PK) so an accidental double-create can be
//     rejected by callers that support per-item conditions.
//   - Non-zero (re-write of an existing item, e.g. after a Get+mutate+Put
//     round trip): CreatedAt/CreatedBy are left untouched, UpdatedAt/
//     UpdatedBy are stamped with db's clock/actor, Version is incremented
//     by 1 in the written item, and the returned condition is
//     Version = :expected (the item's version before this call).
//
// item's PK/SK are computed via [PK]/[SK] (after Type and the audit fields
// are set, since key templates may reference sibling fields including
// Type) and written under the literal attribute names "PK"/"SK". On the
// new-item path, a db.gsi1PKAttr attribute (default "GSI1PK", configurable
// via [WithGSI1PKAttr]) is also set to item's Type (the same struct-name
// string [SetType] stamps).
//
// The returned cond is always populated, but it is only meaningful to
// callers whose underlying DynamoDB API supports a per-item
// ConditionExpression (PutItem, and TransactWriteItems' types.Put).
// BatchWriteItem has no such support at all — callers going through
// BatchWriteItem (see [BatchWriteBuilder.Put]) must discard cond.
func stampAndMarshal(
	ctx context.Context, db *DB, item any,
) (av map[string]types.AttributeValue, cond expression.ConditionBuilder, err error) {
	typeName := reflect.TypeOf(item).Elem().Name()
	mf := modelFieldOf(item)
	model := mf.Interface().(Model)

	now := db.clock()
	actor := db.actor(ctx)

	var newItem bool
	if model.CreatedAt.IsZero() {
		newItem = true
		if model.ID == uuid.Nil {
			mf.FieldByName("ID").Set(reflect.ValueOf(uuid.New()))
		}
		SetType(item)
		mf.FieldByName("CreatedAt").Set(reflect.ValueOf(now))
		mf.FieldByName("CreatedBy").SetString(actor)
		mf.FieldByName("UpdatedAt").Set(reflect.ValueOf(now))
		mf.FieldByName("UpdatedBy").SetString(actor)
		mf.FieldByName("Version").SetInt(1)

		cond = expression.Name("PK").AttributeNotExists()
	} else {
		expectedVersion := model.Version
		mf.FieldByName("UpdatedAt").Set(reflect.ValueOf(now))
		mf.FieldByName("UpdatedBy").SetString(actor)
		mf.FieldByName("Version").SetInt(int64(expectedVersion + 1))

		cond = expression.Name("Version").Equal(expression.Value(expectedVersion))
	}

	av, err = attributevalue.MarshalMap(item)
	if err != nil {
		return nil, cond, fmt.Errorf("godynamo: marshaling %s (id=%s): %w", typeName, model.ID, err)
	}
	pk, sk := PK(item), SK(item)
	av["PK"] = &types.AttributeValueMemberS{Value: pk}
	av["SK"] = &types.AttributeValueMemberS{Value: sk}
	if newItem {
		av[db.gsi1PKAttr] = &types.AttributeValueMemberS{Value: typeName}
	}

	return av, cond, nil
}

// Put creates or updates item, an entity of type T embedding [Model].
//
// Create vs. update is determined by whether item's Model.CreatedAt is the
// zero time.Time:
//
//   - Zero (new item): Put calls [SetType], stamps CreatedAt/CreatedBy and
//     UpdatedAt/UpdatedBy with the DB's clock/actor, sets Version to 1, and
//     writes with a ConditionExpression of attribute_not_exists(PK) so an
//     accidental double-create fails instead of silently overwriting.
//   - Non-zero (re-Put of an existing item, e.g. after a Get+mutate+Put
//     round trip): CreatedAt/CreatedBy are left untouched, UpdatedAt/
//     UpdatedBy are stamped with the DB's clock/actor, and the write uses a
//     ConditionExpression of Version = :expected (the item's version before
//     this call), then increments Version by 1 in the written item. If the
//     condition fails, Put returns an error wrapping [ErrOptimisticLock].
//
// item's PK/SK are computed via [PK]/[SK] (after Type and the audit fields
// are set, since key templates may reference sibling fields including
// Type) and written under the literal attribute names "PK"/"SK".
//
// On the new-item path, Put also writes a GSI1 partition-key attribute
// (default name "GSI1PK", configurable via [WithGSI1PKAttr]) set to the
// item's Type (the same struct-name string [SetType] stamps). Type is
// immutable once set — Update never changes it — so re-Puts of existing
// items don't need to touch that attribute again.
//
// For type-index queries ([Query] with no [QueryBuilder.WherePK] call) to
// work, the DynamoDB table must have a GSI (default name "GSI1",
// configurable via [WithGSI1Name]) with partition key db.gsi1PKAttr
// (default "GSI1PK", configurable via [WithGSI1PKAttr]; String) and sort
// key SK (String) — the GSI reuses the base table's SK attribute as its
// own range key, which is valid: a GSI's key schema may reference an
// attribute the base table already writes. Creating the table/index is out
// of scope for this package.
func Put[T any](ctx context.Context, db *DB, item *T) error {
	typeName := reflect.TypeFor[T]().Name()

	av, cond, err := stampAndMarshal(ctx, db, item)
	if err != nil {
		return err
	}
	model := modelFieldOf(item).Interface().(Model)

	expr, err := expression.NewBuilder().WithCondition(cond).Build()
	if err != nil {
		return fmt.Errorf("godynamo: building put condition for %s (id=%s): %w", typeName, model.ID, err)
	}

	_, err = db.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                 aws.String(db.table),
		Item:                      av,
		ConditionExpression:       expr.Condition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	if err != nil {
		return asOptimisticLockErr(err, "put", typeName, model.ID)
	}
	return nil
}

// Get retrieves the item of type T identified by id, or [ErrNotFound]
// (wrapped with context) if no item exists at the computed key.
//
// Get only computes the correct key for models using the default PK/SK
// (no pk:/sk: tag clause referencing other fields): it builds the key from
// a zero-value T with only Model.ID populated. For any other model, Get
// returns an error wrapping [ErrNonDefaultKey] instead of silently computing
// a wrong key from that zero value — use [Query] or [TransactGet] for such
// models.
func Get[T any](ctx context.Context, db *DB, id uuid.UUID) (*T, error) {
	if err := requiresDefaultKey[T]("Get[T]"); err != nil {
		return nil, err
	}
	typeName := reflect.TypeFor[T]().Name()
	pk, sk := zeroKeys[T](id)

	out, err := db.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(db.table),
		Key:       itemKey(pk, sk),
	})
	if err != nil {
		return nil, fmt.Errorf("godynamo: get %s (id=%s): %w", typeName, id, err)
	}
	if len(out.Item) == 0 {
		return nil, fmt.Errorf("godynamo: get %s (id=%s): %w", typeName, id, ErrNotFound)
	}

	var item T
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, fmt.Errorf("godynamo: unmarshal %s (id=%s): %w", typeName, id, err)
	}
	return &item, nil
}

// Delete removes the item of type T identified by id. Deleting a
// non-existent item is treated as success, matching DynamoDB's default
// DeleteItem behavior (no error on a missing key).
//
// Delete has the same key-computation constraint as [Get]: it only works
// correctly for models using the default PK/SK, and returns an error
// wrapping [ErrNonDefaultKey] for any other model rather than silently
// computing a wrong key.
func Delete[T any](ctx context.Context, db *DB, id uuid.UUID) error {
	if err := requiresDefaultKey[T]("Delete[T]"); err != nil {
		return err
	}
	typeName := reflect.TypeFor[T]().Name()
	pk, sk := zeroKeys[T](id)

	_, err := db.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(db.table),
		Key:       itemKey(pk, sk),
	})
	if err != nil {
		return fmt.Errorf("godynamo: delete %s (id=%s): %w", typeName, id, err)
	}
	return nil
}

// updateOp records a single queued .Set/.Add call on an [UpdateBuilder].
type updateOp struct {
	name string
	val  any
	add  bool
}

// UpdateBuilder builds and, via [UpdateBuilder.Run], executes an UpdateItem
// call for a single item of type T. Obtain one via [Update].
type UpdateBuilder[T any] struct {
	db      *DB
	id      uuid.UUID
	ops     []updateOp
	version *int
}

// Update returns a fluent builder for updating the item of type T
// identified by id. No I/O happens until [UpdateBuilder.Run] is called, so
// Update itself takes no context.Context — unlike the rest of this package's
// builders, which capture ctx once at construction (even though they too do
// no I/O until a terminal method), Update is honest that it does no I/O at
// all outside Run and asks for ctx there instead.
//
// Chain [UpdateBuilder.Set] / [UpdateBuilder.Add] / [UpdateBuilder.IfVersion]
// to configure the update.
func Update[T any](db *DB, id uuid.UUID) *UpdateBuilder[T] {
	return &UpdateBuilder[T]{db: db, id: id}
}

// Set queues a "SET field = value" action. field is a Go field name on T
// (not necessarily its DynamoDB attribute name — a `dynamodbav:"..."`
// struct tag override, if present, is resolved automatically; see
// [resolveAttrName]).
func (b *UpdateBuilder[T]) Set(field string, value any) *UpdateBuilder[T] {
	attr := resolveAttrName(reflect.TypeFor[T](), field)
	b.ops = append(b.ops, updateOp{name: attr, val: value})
	return b
}

// Add queues a numeric "ADD field delta" action. field is a Go field name
// on T (not necessarily its DynamoDB attribute name — a
// `dynamodbav:"..."` struct tag override, if present, is resolved
// automatically; see [resolveAttrName]).
func (b *UpdateBuilder[T]) Add(field string, delta any) *UpdateBuilder[T] {
	attr := resolveAttrName(reflect.TypeFor[T](), field)
	b.ops = append(b.ops, updateOp{name: attr, val: delta, add: true})
	return b
}

// IfVersion enables an optimistic-lock ConditionExpression (Version = v) on
// Run. If IfVersion is never called, Run performs a blind update with no
// condition — callers that need the lock must opt in explicitly.
func (b *UpdateBuilder[T]) IfVersion(v int) *UpdateBuilder[T] {
	b.version = &v
	return b
}

// Run executes the update and returns the item's new state (ReturnValues:
// ALL_NEW).
//
// Run always adds, on top of whatever [UpdateBuilder.Set]/[UpdateBuilder.Add]
// calls were chained: SET UpdatedAt = :now, SET UpdatedBy = :actor, and ADD
// Version 1 (i.e. Version = Version + 1). If [UpdateBuilder.IfVersion] was
// called, Run also adds a ConditionExpression of Version = :expectedVersion
// and, on a ConditionalCheckFailedException, returns an error wrapping
// [ErrOptimisticLock]. Without IfVersion, the increment is unconditional
// ("blind") — document this tradeoff to callers that need the lock.
//
// Run has the same key-computation constraint as [Get]: it only works
// correctly for models using the default PK/SK, and returns an error
// wrapping [ErrNonDefaultKey] for any other model rather than silently
// computing a wrong key.
func (b *UpdateBuilder[T]) Run(ctx context.Context) (*T, error) {
	if err := requiresDefaultKey[T]("UpdateBuilder.Run"); err != nil {
		return nil, err
	}
	typeName := reflect.TypeFor[T]().Name()
	pk, sk := zeroKeys[T](b.id)

	now := b.db.clock()
	actor := b.db.actor(ctx)

	upd := expression.Set(expression.Name("UpdatedAt"), expression.Value(now)).
		Set(expression.Name("UpdatedBy"), expression.Value(actor))
	for _, op := range b.ops {
		if op.add {
			upd = upd.Add(expression.Name(op.name), expression.Value(op.val))
		} else {
			upd = upd.Set(expression.Name(op.name), expression.Value(op.val))
		}
	}
	upd = upd.Add(expression.Name("Version"), expression.Value(1))

	eb := expression.NewBuilder().WithUpdate(upd)
	if b.version != nil {
		eb = eb.WithCondition(expression.Name("Version").Equal(expression.Value(*b.version)))
	}
	expr, err := eb.Build()
	if err != nil {
		return nil, fmt.Errorf("godynamo: building update expression for %s (id=%s): %w", typeName, b.id, err)
	}

	out, err := b.db.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(b.db.table),
		Key:                       itemKey(pk, sk),
		UpdateExpression:          expr.Update(),
		ConditionExpression:       expr.Condition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ReturnValues:              types.ReturnValueAllNew,
	})
	if err != nil {
		return nil, asOptimisticLockErr(err, "update", typeName, b.id)
	}

	var item T
	if err := attributevalue.UnmarshalMap(out.Attributes, &item); err != nil {
		return nil, fmt.Errorf("godynamo: unmarshal %s (id=%s): %w", typeName, b.id, err)
	}
	return &item, nil
}
