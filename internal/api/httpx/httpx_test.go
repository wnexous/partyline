package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

type body struct {
	Name string `json:"name"`
}

// decodeHandler mirrors how API handlers use the helpers.
func decodeHandler(w http.ResponseWriter, r *http.Request) {
	var b body
	if err := DecodeJSON(w, r, &b); err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, b)
}

func do(t *testing.T, h http.HandlerFunc, payload string) (*httptest.ResponseRecorder, dto.ErrorBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	var env dto.ErrorBody
	if rec.Code >= 400 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	}
	return rec, env
}

func TestDecodeJSONAcceptsValidBody(t *testing.T) {
	rec, _ := do(t, decodeHandler, `{"name":"room"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"name":"room"}`, rec.Body.String())
}

func TestDecodeJSONOversizedBodyIs413TooLarge(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", MaxJSONBody) + `"}`
	rec, env := do(t, decodeHandler, big)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, dto.ErrorCodeTooLarge, env.Error.Code)
}

func TestDecodeJSONMalformedBodyIs400Invalid(t *testing.T) {
	for _, payload := range []string{`{"name":`, ``, `{"name":"a"} {"name":"b"}`, `{"name":1}`} {
		rec, env := do(t, decodeHandler, payload)
		require.Equal(t, http.StatusBadRequest, rec.Code, payload)
		require.Equal(t, dto.ErrorCodeInvalid, env.Error.Code, payload)
	}
}

func TestInvalidIs422WithFields(t *testing.T) {
	rec, env := do(t, func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, Invalid(map[string]string{"username": "must match ^[a-z0-9_-]{3,32}$"}))
	}, `{}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, dto.ErrorCodeInvalid, env.Error.Code)
	require.Equal(t, map[string]string{"username": "must match ^[a-z0-9_-]{3,32}$"}, env.Error.Fields)
}

func TestWriteErrorUsesStatusAndCodeOfHTTPError(t *testing.T) {
	rec, env := do(t, func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "room not found"))
	}, `{}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, dto.ErrorCodeNotFound, env.Error.Code)
	require.Equal(t, "room not found", env.Error.Message)
}

func TestWriteErrorHidesUnknownErrors(t *testing.T) {
	rec, env := do(t, func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, errors.New("sql: database is locked at /data/partyline.db"))
	}, `{}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, dto.ErrorCodeInternal, env.Error.Code)
	require.NotContains(t, rec.Body.String(), "partyline.db")
}

func page(t *testing.T, query string) (PageQuery, error) {
	t.Helper()
	return ParsePage(httptest.NewRequest(http.MethodGet, "/?"+query, nil), 50, 100)
}

func TestParsePageDefaultsAndValues(t *testing.T) {
	p, err := page(t, "")
	require.NoError(t, err)
	require.Equal(t, PageQuery{Limit: 50}, p)

	p, err = page(t, "limit=10&before_seq=40&after_seq=3&before=abc")
	require.NoError(t, err)
	require.Equal(t, PageQuery{Limit: 10, BeforeSeq: 40, AfterSeq: 3, Before: "abc"}, p)

	p, err = page(t, "limit=500")
	require.NoError(t, err)
	require.Equal(t, 100, p.Limit, "limit is capped at the maximum")
}

func TestParsePageRejectsBadNumbers(t *testing.T) {
	for query, field := range map[string]string{
		"limit=0":       "limit",
		"limit=abc":     "limit",
		"before_seq=-1": "before_seq",
		"after_seq=x":   "after_seq",
	} {
		_, err := page(t, query)
		var he *Error
		require.ErrorAs(t, err, &he, query)
		require.Equal(t, http.StatusUnprocessableEntity, he.Status)
		require.Contains(t, he.Fields, field, query)
	}
}
