package httpapi

import (
	"errors"
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// ValidationBody is returned for 422 responses so forms can mark the field.
type ValidationBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// extraErrors lets feature files map their own sentinel errors to HTTP status
// codes without errors.go importing every feature package's internals.
var extraErrors []func(error) (status int, code string, ok bool)

// HandleError maps domain errors to HTTP responses. Unknown errors become a
// generic 500 so internal details are never leaked.
func (s *Server) HandleError(w http.ResponseWriter, err error) {
	var ve *runtime.ValidationError
	switch {
	case errors.As(err, &ve):
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]ValidationBody{"error": {Code: ve.Code, Message: ve.Message, Field: ve.Field}})
	case errors.Is(err, store.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, workspace.ErrNameTaken):
		WriteJSON(w, http.StatusConflict, map[string]ValidationBody{"error": {Code: "name_taken", Message: err.Error(), Field: "name"}})
	case errors.Is(err, workspace.ErrConfirmName):
		WriteError(w, http.StatusUnprocessableEntity, "confirm_name_mismatch", err.Error())
	case errors.Is(err, workspace.ErrBadState), errors.Is(err, store.ErrConflict):
		WriteError(w, http.StatusConflict, "conflict", err.Error())
	default:
		for _, f := range extraErrors {
			if status, code, ok := f(err); ok {
				WriteError(w, status, code, err.Error())
				return
			}
		}
		s.log.Error("unhandled error", "err", err)
		WriteError(w, http.StatusInternalServerError, "internal", "Something went wrong on our side.")
	}
}

// DecodeJSON reads a bounded JSON body.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return DecodeJSONLimit(w, r, v, 1<<20)
}

// DecodeJSONLimit is DecodeJSON with a custom body size cap.
func DecodeJSONLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := jsonDecoder(r)
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "Request body is not valid JSON for this endpoint.")
		return false
	}
	return true
}
