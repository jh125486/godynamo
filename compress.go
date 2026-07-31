package godynamo

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// compressJSON gzip-compresses data (typically the JSON encoding of a
// single field's value) and returns the compressed bytes.
func compressJSON(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		return nil, fmt.Errorf("godynamo: gzip compress: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("godynamo: gzip compress: %w", err)
	}
	return buf.Bytes(), nil
}

// decompressJSON reverses [compressJSON].
func decompressJSON(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("godynamo: gzip decompress: %w", err)
	}
	defer gr.Close()

	out, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("godynamo: gzip decompress: %w", err)
	}
	return out, nil
}

// marshalItem marshals item (a pointer to, or value of, a struct embedding
// [Model]) into a DynamoDB attribute map, identically to
// attributevalue.MarshalMap for any type with no `dynamo:"compress"`
// fields. For each field named in the type's parsed [modelTags.compressFields],
// marshalItem overwrites attributevalue.MarshalMap's native encoding of that
// field with a Binary ([types.AttributeValueMemberB]) attribute holding the
// gzip-compressed JSON encoding of the field's value, stored under the
// field's normally-resolved attribute name (see [resolveAttrName] — a
// dynamodbav tag override on the same field still controls naming; the two
// tags are orthogonal).
func marshalItem(item any) (map[string]types.AttributeValue, error) {
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return nil, err
	}

	sv := structValueOf(item)
	t := sv.Type()
	mt := parseTags(t)
	for _, fieldName := range mt.compressFields {
		fv := sv.FieldByName(fieldName)
		if !fv.IsValid() {
			continue
		}
		raw, err := json.Marshal(fv.Interface())
		if err != nil {
			return nil, fmt.Errorf("godynamo: marshaling compressed field %s: %w", fieldName, err)
		}
		compressed, err := compressJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("godynamo: compressing field %s: %w", fieldName, err)
		}
		av[resolveAttrName(t, fieldName)] = &types.AttributeValueMemberB{Value: compressed}
	}
	return av, nil
}

// unmarshalItemInto unmarshals av into dst (a non-nil pointer to a struct
// embedding [Model]), identically to attributevalue.UnmarshalMap for any
// destination type with no `dynamo:"compress"` fields.
//
// dst must be a non-nil pointer; if it isn't, unmarshalItemInto panics
// (rather than returning an error), mirroring [modelFieldOf]'s
// panic-on-misuse convention for the same kind of structural precondition
// failure. Every internal call site always passes a genuine, freshly
// constructed, non-nil *T, so this panic is only reachable from
// [TransactGetBuilder.Get]'s externally caller-supplied dst.
//
// For each field named in dst's parsed [modelTags.compressFields] whose
// resolved attribute name is present in av as a Binary
// ([types.AttributeValueMemberB]) value, unmarshalItemInto sets that
// attribute aside and removes it from a shallow copy of av (the caller's av
// is never mutated) before delegating the rest to attributevalue.UnmarshalMap
// — removing it first is necessary, since attributevalue.UnmarshalMap would
// otherwise try (and fail, or produce garbage) to unmarshal raw gzip bytes
// into the field's real Go type. After the main unmarshal succeeds, each
// set-aside attribute is gunzipped, JSON-unmarshaled into the field's real
// type, and set on dst via reflection.
//
// A compressFields entry with no matching Binary attribute in av (e.g. an
// older item written before the field existed, or a legitimately-empty
// value that was never written) is left at its zero value rather than
// treated as an error.
//
// unmarshalItemInto works for both generic call sites (var item T;
// unmarshalItemInto(av, &item)) and heterogeneous any-typed destinations
// (e.g. [TransactGetBuilder.Run]'s b.dsts[i]), since it operates purely via
// reflection on dst's dynamic type.
func unmarshalItemInto(av map[string]types.AttributeValue, dst any) error {
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		panic(fmt.Sprintf("godynamo: unmarshalItemInto requires a non-nil pointer, got %T", dst))
	}
	sv := dv.Elem()
	t := sv.Type()
	mt := parseTags(t)

	if len(mt.compressFields) == 0 {
		if err := attributevalue.UnmarshalMap(av, dst); err != nil {
			return fmt.Errorf("godynamo: unmarshal: %w", err)
		}
		return nil
	}

	filtered, pending := extractCompressedFields(t, mt, av)

	if err := attributevalue.UnmarshalMap(filtered, dst); err != nil {
		return fmt.Errorf("godynamo: unmarshal: %w", err)
	}

	return applyPendingCompressedFields(sv, pending)
}

// compressedField is a compressFields entry set aside by
// [extractCompressedFields], still carrying its raw gzip bytes.
type compressedField struct {
	fieldName string
	data      []byte
}

// extractCompressedFields builds a shallow copy of av (the caller's av is
// never mutated) and, for each of t's compressFields entries whose resolved
// attribute name is present in that copy as a Binary
// ([types.AttributeValueMemberB]) value, removes it from the copy and
// appends it to the returned pending slice. Removing it first is necessary,
// since attributevalue.UnmarshalMap would otherwise try (and fail, or
// produce garbage) to unmarshal raw gzip bytes into the field's real Go
// type.
//
// A compressFields entry with no matching Binary attribute in av (e.g. an
// older item written before the field existed, or a legitimately-empty
// value that was never written) is simply absent from pending rather than
// treated as an error.
func extractCompressedFields(
	t reflect.Type, mt *modelTags, av map[string]types.AttributeValue,
) (filtered map[string]types.AttributeValue, pending []compressedField) {
	filtered = make(map[string]types.AttributeValue, len(av))
	for k, v := range av {
		filtered[k] = v
	}

	for _, fieldName := range mt.compressFields {
		attr := resolveAttrName(t, fieldName)
		raw, ok := filtered[attr]
		if !ok {
			continue
		}
		b, ok := raw.(*types.AttributeValueMemberB)
		if !ok {
			// Not the Binary shape a compressed field produces (e.g. an
			// older, pre-compression item still carrying its native
			// representation) -- leave it for attributevalue.UnmarshalMap
			// to handle/fail on normally.
			continue
		}
		pending = append(pending, compressedField{fieldName: fieldName, data: b.Value})
		delete(filtered, attr)
	}
	return filtered, pending
}

// applyPendingCompressedFields gunzips and JSON-unmarshals each pending
// field's set-aside data back into the field's real type, and sets it on sv
// (the addressable struct value backing the unmarshal destination) via
// reflection.
func applyPendingCompressedFields(sv reflect.Value, pending []compressedField) error {
	for _, p := range pending {
		decompressed, err := decompressJSON(p.data)
		if err != nil {
			return fmt.Errorf("godynamo: decompressing field %s: %w", p.fieldName, err)
		}
		fv := sv.FieldByName(p.fieldName)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}
		newVal := reflect.New(fv.Type())
		if err := json.Unmarshal(decompressed, newVal.Interface()); err != nil {
			return fmt.Errorf("godynamo: unmarshaling compressed field %s: %w", p.fieldName, err)
		}
		fv.Set(newVal.Elem())
	}
	return nil
}
