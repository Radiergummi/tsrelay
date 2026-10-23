package api

import (
	"bytes"
	"encoding"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// decodeRequest reads a JSON request body into v. An unknown field is not
// malformed JSON but a newer client talking to an older relay, so it is
// answered 422 unsupported_field, naming every unknown field by its dotted
// path the way invalid does. The body may be at most limit bytes. It
// returns false once it has written a response.
func decodeRequest(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err = dec.Decode(v); err == nil {
			return true
		}
		// encoding/json names only the first unknown field, and without its
		// path, so find them all by walking the body against v's type.
		var raw any
		if json.Unmarshal(body, &raw) == nil {
			var errs hooks.ValidationError
			for _, field := range unknownFields(reflect.TypeOf(v), raw, "") {
				errs = append(errs, hooks.FieldError{Field: field, Message: "not supported by this relay"})
			}
			if len(errs) > 0 {
				p := problem.New(http.StatusUnprocessableEntity, problem.UnsupportedField, errs.Error())
				p.Errors = errs
				p.Write(w)
				return false
			}
		}
	}
	problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
	return false
}

// valueTyped is a field that decodes into another type, such as hooks.Opt.
type valueTyped interface{ ValueType() reflect.Type }

var (
	valueTypedType      = reflect.TypeFor[valueTyped]()
	unmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// unknownFields returns the dotted paths of the object keys in raw that
// encoding/json would find no field of t for, in sorted order.
func unknownFields(t reflect.Type, raw any, path string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Implements(valueTypedType) {
		return unknownFields(reflect.Zero(t).Interface().(valueTyped).ValueType(), raw, path)
	}
	pt := reflect.PointerTo(t)
	if pt.Implements(unmarshalerType) || pt.Implements(textUnmarshalerType) {
		return nil // decodes itself
	}
	var unknown []string
	switch raw := raw.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(raw)) {
			switch t.Kind() {
			case reflect.Map:
				unknown = append(unknown, unknownFields(t.Elem(), raw[key], join(path, key))...)
			case reflect.Struct:
				if f, ok := jsonField(t, key); ok {
					// A known field is named as declared, as invalid does.
					unknown = append(unknown, unknownFields(f.typ, raw[key], join(path, f.name))...)
				} else {
					unknown = append(unknown, join(path, key))
				}
			}
		}
	case []any:
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			for i, e := range raw {
				unknown = append(unknown, unknownFields(t.Elem(), e, path+"["+strconv.Itoa(i)+"]")...)
			}
		}
	}
	return unknown
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

type field struct {
	name string
	typ  reflect.Type
}

// jsonField finds the field of struct t that encoding/json decodes key
// into: an exact name match first, then a case-insensitive one.
func jsonField(t reflect.Type, key string) (field, bool) {
	var fold *field
	for _, sf := range reflect.VisibleFields(t) {
		if !sf.IsExported() || sf.Anonymous && sf.Tag.Get("json") == "" {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if name == key {
			return field{name, sf.Type}, true
		}
		if fold == nil && strings.EqualFold(name, key) {
			fold = &field{name, sf.Type}
		}
	}
	if fold != nil {
		return *fold, true
	}
	return field{}, false
}

// queryBool reads a boolean query parameter as strconv.ParseBool does
// ("1", "true", "TRUE", ...); anything else, or absent, is false.
func queryBool(r *http.Request, name string) bool {
	b, _ := strconv.ParseBool(r.URL.Query().Get(name))
	return b
}
