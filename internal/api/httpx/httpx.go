// Package httpx holds HTTP helpers shared by API handlers: JSON decoding with a size limit,
// the error envelope and pagination parameters.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/wnexous/partyline/internal/api/dto"
)

// MaxJSONBody is the largest accepted JSON request body (SEC-03).
const MaxJSONBody = 1 << 20

// Error is an API error with its HTTP status. Handlers return it and WriteError renders it.
type Error struct {
	Status  int
	Code    dto.ErrorCode
	Message string
	Fields  map[string]string
	Details map[string]string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// NewError returns an API error.
func NewError(status int, code dto.ErrorCode, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Invalid returns a 422 validation error naming each offending field.
func Invalid(fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: dto.ErrorCodeInvalid, Message: "validation failed", Fields: fields}
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError renders err as the error envelope. Errors that are not *Error become a
// generic 500 so internal details never reach the client.
func WriteError(w http.ResponseWriter, err error) {
	var he *Error
	if !errors.As(err, &he) {
		he = NewError(http.StatusInternalServerError, dto.ErrorCodeInternal, "internal error")
	}
	WriteJSON(w, he.Status, dto.ErrorBody{Error: dto.Error{Code: he.Code, Message: he.Message, Fields: he.Fields, Details: he.Details}})
}

// DecodeJSON decodes a single JSON value from the request body into dst. Bodies over
// MaxJSONBody yield 413 too_large; malformed JSON yields 400 invalid.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return NewError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge, "request body too large")
		}
		return NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "cannot read request body")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return NewError(http.StatusBadRequest, dto.ErrorCodeInvalid, "malformed JSON body")
	}
	return nil
}

// PageQuery holds the pagination parameters of a list request. Zero means "not set"
// for the cursors.
type PageQuery struct {
	Limit     int
	BeforeSeq int64
	AfterSeq  int64
	Before    string
}

// ParsePage reads limit, before_seq, after_seq and before from the query string.
// limit defaults to def and is capped at maxLimit.
func ParsePage(r *http.Request, def, maxLimit int) (PageQuery, error) {
	q := r.URL.Query()
	p := PageQuery{Limit: def, Before: q.Get("before")}
	fields := map[string]string{}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fields["limit"] = "must be a positive integer"
		}
		p.Limit = min(n, maxLimit)
	}
	for name, dst := range map[string]*int64{"before_seq": &p.BeforeSeq, "after_seq": &p.AfterSeq} {
		if v := q.Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				fields[name] = "must be a non-negative integer"
			}
			*dst = n
		}
	}
	if len(fields) > 0 {
		return PageQuery{}, Invalid(fields)
	}
	return p, nil
}
