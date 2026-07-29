package godynamo

import (
	"fmt"
	"reflect"
	"strings"
)

// PK computes the partition key for v, a pointer to (or value of) a struct
// embedding [Model].
//
// If the struct's dynamo tag has a pk clause (e.g. `dynamo:"pk:Field1,Field2"`),
// the PK is "StructName#{Field1}#{Field2}", with values pulled from the
// named sibling fields via reflection and formatted with fmt.Sprint. If no
// pk clause is present, the default PK is "StructName#{ID}".
//
// PK panics if v is not a struct (or pointer to struct) embedding Model, or
// if a named field does not exist on the struct.
func PK(v any) string {
	sv := structValueOf(v)
	mt := parseTags(sv.Type())

	fields := mt.pkFields
	if len(fields) == 0 {
		fields = []string{idFieldName}
	}
	return buildKey(mt.structName, sv, fields)
}

// SK computes the sort key for v, a pointer to (or value of) a struct
// embedding [Model].
//
// If the struct's dynamo tag has an sk clause (e.g.
// `dynamo:"sk:Field1,Field2"`), the SK is built as
// "StructName#{Field1}#{Field2}#{ID}" — the ID field is always appended as
// the final component. If the sk clause's last listed field is already
// literally "ID", it is not appended a second time. If no sk clause is
// present, the default SK is "StructName#{ID}".
//
// SK panics under the same conditions as PK.
func SK(v any) string {
	sv := structValueOf(v)
	mt := parseTags(sv.Type())

	fields := mt.skFields
	if len(fields) == 0 {
		fields = []string{idFieldName}
	} else if fields[len(fields)-1] != idFieldName {
		fields = append(append([]string{}, fields...), idFieldName)
	}
	return buildKey(mt.structName, sv, fields)
}

// Keys returns both the partition key and sort key for v. It is equivalent
// to calling PK(v) and SK(v), computed together for convenience.
func Keys(v any) (pk, sk string) {
	return PK(v), SK(v)
}

// structValueOf normalizes v (a pointer to, or value of, a struct) to its
// addressable-or-not struct reflect.Value. It panics if v is not ultimately
// a struct.
func structValueOf(v any) reflect.Value {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			panic("godynamo: nil pointer passed to key builder")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		panic("godynamo: value must be a struct or pointer to struct")
	}
	return rv
}

// buildKey joins structName and the values of the named fields (resolved
// against sv via reflection, including promoted fields from embedded
// Model) with "#".
func buildKey(structName string, sv reflect.Value, fields []string) string {
	parts := make([]string, 0, len(fields)+1)
	parts = append(parts, structName)
	for _, name := range fields {
		fv := sv.FieldByName(name)
		if !fv.IsValid() {
			panic("godynamo: field " + name + " not found on struct " + structName)
		}
		parts = append(parts, fmt.Sprint(fv.Interface()))
	}
	return strings.Join(parts, "#")
}
