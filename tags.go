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

// modelTags holds the parsed pk/sk field-name lists for a struct type, plus
// the struct's own type name (used as the key template prefix).
type modelTags struct {
	structName string
	pkFields   []string // nil => no explicit pk clause; caller applies default.
	skFields   []string // nil => no explicit sk clause; caller applies default.
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

	tag, ok := field.Tag.Lookup(tagKey)
	if ok && strings.TrimSpace(tag) != "" {
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

	actual, _ := tagCache.LoadOrStore(t, mt)
	return actual.(*modelTags)
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
