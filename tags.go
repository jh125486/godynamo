package godynamo

import (
	"reflect"
	"strings"
	"sync"
)

// Tag syntax
//
// The `dynamo` struct tag lives on the embedded Model field of the owning
// struct (Model's key is built from several sibling fields, so it can't
// live on any single one of them). It holds one or two clauses separated
// by ';':
//
//	pk:Field1,Field2   - fields (by Go name) used to build the partition key
//	sk:Field1,Field2   - fields (by Go name) used to build the sort key
//
// Example:
//
//	type Order struct {
//	    godynamo.Model `dynamo:"pk:CustomerID;sk:Status,CreatedDate"`
//	    CustomerID  string
//	    Status      string
//	    CreatedDate string
//	}
//
// Rules:
//   - Either clause may be omitted; order of clauses does not matter.
//   - If no pk clause is present, the default PK is "StructName#{ID}".
//   - If no sk clause is present, the default SK is "StructName#{ID}".
//   - The ID field is always appended as the final SK component, unless the
//     sk clause's last listed field is already literally "ID" (avoids a
//     double append).
//   - Field names refer to other fields of the same struct by their Go
//     field name (not their json/dynamodbav name).
const (
	tagKey      = "dynamo"
	clauseSep   = ";"
	fieldSep    = ","
	kvSep       = ":"
	pkClauseKey = "pk"
	skClauseKey = "sk"
	idFieldName = "ID"
)

// compressTagValue is the exact (trimmed) `dynamo` tag value that marks a
// field for transparent gzip compression. Unlike the Model field's
// `pk:`/`sk:` clause grammar, this is a different tag *target* (any field
// OTHER than the embedded Model field) with a simpler, bare-keyword
// grammar — see the "The compress tag" section of README.md.
const compressTagValue = "compress"

// modelTags holds the parsed pk/sk field-name lists for a struct type, plus
// the struct's own type name (used as the key template prefix) and the list
// of fields marked `dynamo:"compress"`.
type modelTags struct {
	structName     string
	pkFields       []string // nil => no explicit pk clause; caller applies default.
	skFields       []string // nil => no explicit sk clause; caller applies default.
	compressFields []string // Go field names tagged `dynamo:"compress"`.
}

// tagCache caches parsed modelTags per struct reflect.Type so the (small)
// cost of tag parsing is paid at most once per type, not once per call.
//
//nolint:gochecknoglobals // process-lifetime memoization cache, not mutable config/state.
var tagCache sync.Map // map[reflect.Type]*modelTags

// parseTags returns the cached (or freshly parsed) modelTags for struct
// type t. It panics if t does not embed godynamo.Model.
func parseTags(t reflect.Type) *modelTags {
	if cached, ok := tagCache.Load(t); ok {
		return cached.(*modelTags)
	}

	field, ok := t.FieldByName("Model")
	if !ok || field.Type != reflect.TypeFor[Model]() {
		panic("godynamo: struct " + t.Name() + " must embed godynamo.Model")
	}

	mt := &modelTags{structName: t.Name()}
	parseModelClauses(mt, &field)
	mt.compressFields = parseCompressFields(t)

	actual, _ := tagCache.LoadOrStore(t, mt)
	return actual.(*modelTags)
}

// parseModelClauses parses the embedded Model field's `dynamo` tag (its
// pk:/sk: clauses) into mt.pkFields/mt.skFields.
func parseModelClauses(mt *modelTags, field *reflect.StructField) {
	tag, ok := field.Tag.Lookup(tagKey)
	if !ok || strings.TrimSpace(tag) == "" {
		return
	}
	for clause := range strings.SplitSeq(tag, clauseSep) {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		key, val, found := strings.Cut(clause, kvSep)
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		fields := splitFields(val)
		switch key {
		case pkClauseKey:
			mt.pkFields = fields
		case skClauseKey:
			mt.skFields = fields
		}
	}
}

// parseCompressFields scans every field of t OTHER than the embedded Model
// field for a `dynamo:"compress"` tag, returning the Go field names (in
// declaration order) of those that carry it.
func parseCompressFields(t reflect.Type) []string {
	modelType := reflect.TypeFor[Model]()
	var compressFields []string
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.Type == modelType {
			continue // the embedded Model field itself; handled separately.
		}
		val, ok := sf.Tag.Lookup(tagKey)
		if !ok {
			continue
		}
		if strings.TrimSpace(val) == compressTagValue {
			compressFields = append(compressFields, sf.Name)
		}
	}
	return compressFields
}

// splitFields splits a comma-separated clause value into trimmed,
// non-empty field names.
func splitFields(val string) []string {
	parts := strings.Split(val, fieldSep)
	fields := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			fields = append(fields, p)
		}
	}
	return fields
}

// dynamodbavTagKey is the struct tag key attributevalue.MarshalMap/
// UnmarshalMap read to resolve a Go field to its DynamoDB attribute name.
const dynamodbavTagKey = "dynamodbav"

// resolveAttrName resolves a Go field name on t to the DynamoDB attribute
// name attributevalue.MarshalMap actually marshals it under: the value of a
// `dynamodbav:"..."` struct tag on the field (the substring before the
// first comma — e.g. `dynamodbav:"custom_name,omitempty"` resolves to
// "custom_name"), or — if the field isn't found on t, has no such tag, or
// the tag's name portion is empty or "-" — goFieldName itself, unchanged.
//
// This mirrors attributevalue's own tag resolution (aws-sdk-go-v2's
// feature/dynamodb/attributevalue package: see field.go's buildField,
// which sets a field's marshaled Name from tag.Name only when
// len(tag.Name) != 0, and tag.go's parseTagStr, which treats a "-" tag
// name as Ignore rather than a literal override — both leave the Go field
// name as the marshaled name in every other case), so callers that pass a
// Go field name into a DynamoDB expression (e.g. [QueryBuilder.Filter],
// [UpdateBuilder.Set]) get exactly the attribute name their item was
// actually marshaled under.
//
// resolveAttrName never panics: a field name that doesn't exist on t falls
// straight through unchanged, so existing error paths (e.g.
// expression.Name("") failing to build) still trigger exactly as they did
// before this resolution step existed.
func resolveAttrName(t reflect.Type, goFieldName string) string {
	field, ok := t.FieldByName(goFieldName)
	if !ok {
		return goFieldName
	}
	tagStr, ok := field.Tag.Lookup(dynamodbavTagKey)
	if !ok {
		return goFieldName
	}
	name, _, _ := strings.Cut(tagStr, ",")
	if name == "" || name == "-" {
		return goFieldName
	}
	return name
}
