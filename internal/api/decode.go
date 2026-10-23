package api

import (
	"encoding"
	"encoding/json"
	"fmt"
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
// path the way invalid does. Field names match exactly, unlike
// encoding/json's, which would take "PATH" for "path"; and nothing may
// follow the value. The body may be at most limit bytes. It returns false
// once it has written a response.
func decodeRequest(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var raw any
	if err == nil {
		err = json.Unmarshal(body, &raw) // also refuses trailing data
	}
	if err == nil {
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
		err = json.Unmarshal(body, v)
	}
	if err != nil {
		problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
		return false
	}
	return true
}

// valueTyped is a field that decodes into another type, such as hooks.Opt.
type valueTyped interface{ ValueType() reflect.Type }

var (
	valueTypedType      = reflect.TypeFor[valueTyped]()
	unmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// unknownFields returns the dotted paths of the object keys in raw that
// name no field of t exactly, in sorted order.
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

// jsonField finds the field of struct t whose JSON name is key, exactly.
func jsonField(t reflect.Type, key string) (field, bool) {
	for _, sf := range reflect.VisibleFields(t) {
		if !sf.IsExported() || sf.Anonymous && sf.Tag.Get("json") == "" {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "" {
			name = sf.Name
		}
		if name == key && name != "-" {
			return field{name, sf.Type}, true
		}
	}
	return field{}, false
}

// queryBool reads the boolean query parameter name as strconv.ParseBool
// does ("1", "true", "TRUE", ...); absent is false. Anything else, or the
// parameter given twice, is answered 400: a misspelled dryRun must not
// write. ok is false once it has written a response.
func queryBool(w http.ResponseWriter, r *http.Request, name string) (value, ok bool) {
	vs, given := r.URL.Query()[name]
	if !given {
		return false, true
	}
	b, err := strconv.ParseBool(vs[0])
	if err != nil || len(vs) > 1 {
		problem.New(http.StatusBadRequest, problem.BadRequest,
			fmt.Sprintf("query parameter %s must be given once, as true or false", name)).Write(w)
		return false, false
	}
	return b, true
}

// onlyQuery refuses, as 422 unsupported_field, a write with query
// parameters other than names. A write can't ignore one: a newer client
// may mean it to change the write, as dryRun does. Reads ignore unknown
// parameters instead. It returns false once it has written a response.
func onlyQuery(w http.ResponseWriter, r *http.Request, names ...string) bool {
	var errs hooks.ValidationError
	for _, name := range slices.Sorted(maps.Keys(r.URL.Query())) {
		if !slices.Contains(names, name) {
			errs = append(errs, hooks.FieldError{Field: "query." + name, Message: "not supported by this relay"})
		}
	}
	if len(errs) == 0 {
		return true
	}
	p := problem.New(http.StatusUnprocessableEntity, problem.UnsupportedField, errs.Error())
	p.Errors = errs
	p.Write(w)
	return false
}
