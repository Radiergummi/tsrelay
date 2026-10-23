package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// decodeRequest reads a JSON request body into v. An unknown field is not
// malformed JSON but a newer client talking to an older relay, so it is
// answered 422 unsupported_field, naming the field the way invalid does.
// The body may be at most limit bytes. It returns false once it has written a response.
func decodeRequest(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		return true
	}
	// encoding/json offers no typed error for this one.
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		if field, uerr := strconv.Unquote(name); uerr == nil {
			errs := hooks.ValidationError{{Field: field, Message: "not supported by this relay"}}
			p := problem.New(http.StatusUnprocessableEntity, problem.UnsupportedField, errs.Error())
			p.Errors = errs
			p.Write(w)
			return false
		}
	}
	problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
	return false
}

// queryBool reads a boolean query parameter as strconv.ParseBool does
// ("1", "true", "TRUE", ...); anything else, or absent, is false.
func queryBool(r *http.Request, name string) bool {
	b, _ := strconv.ParseBool(r.URL.Query().Get(name))
	return b
}
