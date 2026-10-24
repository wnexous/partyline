package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
)

// errUnreachable is the tool error when the server cannot be reached.
var errUnreachable = errors.New("partyline: server unreachable")

// client calls the agent REST API (Authorization: Bearer plus the identity headers: the AI is
// the one of the token's owner, machine and folder).
type client struct {
	base     *url.URL
	token    string
	identity Identity
	http     *http.Client
}

// do sends a request to path (escaped segments under /api/v1) with query q and returns the
// response for 2xx statuses. Other statuses become errors whose text the tools show (the
// server's own message for 4xx, e.g. "not a member of room <room>" or "rate limited: ...").
func (c *client) do(ctx context.Context, method, path string, q url.Values, contentType string, body io.Reader) (*http.Response, error) {
	u := c.base.JoinPath("api/v1", path)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	c.identity.setHeaders(req.Header)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errUnreachable
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, apiError(resp)
}

func apiError(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return errInvalidToken
	}
	var body dto.ErrorBody
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(data, &body) != nil || body.Error.Message == "" {
		return fmt.Errorf("partyline: server answered %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusBadGateway {
		// The platform behind a connection refused what was sent through it (CID-32): the message
		// says what, in words for the AI, and is no failure of Partyline's.
		return &statusError{status: resp.StatusCode, msg: body.Error.Message}
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("partyline: server error (%d): %s", resp.StatusCode, body.Error.Message)
	}
	msg := withCode(body.Error.Code, body.Error.Message)
	if len(body.Error.Fields) > 0 {
		parts := make([]string, 0, len(body.Error.Fields))
		for _, f := range slices.Sorted(maps.Keys(body.Error.Fields)) {
			parts = append(parts, f+": "+body.Error.Fields[f])
		}
		msg += " (" + strings.Join(parts, "; ") + ")"
	}
	if len(body.Error.Refused) > 0 {
		// A refused group is all or nothing (AGRP-02): say which members and that nothing exists.
		parts := make([]string, 0, len(body.Error.Refused))
		for _, m := range body.Error.Refused {
			parts = append(parts, m.ID+" ("+string(m.Reason)+")")
		}
		msg += "; nothing was created (refused: " + strings.Join(parts, ", ") + ")"
	}
	return &statusError{status: resp.StatusCode, msg: msg}
}

// statusError is a refusal of the server that is the caller's to act on (a 4xx other than 401): its text is what the
// tools show, and the status lets a tool say more about the refusals it knows the reasons of.
type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

// namedCodes are the server codes a tool error names, so that the AI can tell the refusals of the
// conversation and profile tools apart and act on them. The other errors show the server's
// message alone.
var namedCodes = map[dto.ErrorCode]bool{
	dto.ErrorCodeContactNotFound:     true,
	dto.ErrorCodeNoRouteToPerson:     true,
	dto.ErrorCodeDMSelf:              true,
	dto.ErrorCodeCounterpartDisabled: true,
	dto.ErrorCodeMembersRefused:      true,
	dto.ErrorCodeHandleTaken:         true,
	dto.ErrorCodeAvatarInvalid:       true,
	dto.ErrorCodeRateLimited:         true,
	dto.ErrorCodeNotOperator:         true,
	dto.ErrorCodeUnknownChat:         true,
	dto.ErrorCodeUnsupported:         true,
}

// withCode is the message prefixed with its code when the code is one of namedCodes ("code:
// message"). A message that already leads with its code in words, as the post limit's "rate
// limited: at most 30 messages per minute" does, is left as it is.
func withCode(code dto.ErrorCode, message string) string {
	if !namedCodes[code] || strings.HasPrefix(message, strings.ReplaceAll(string(code), "_", " ")+":") {
		return message
	}
	return string(code) + ": " + message
}

// json sends v as JSON (nil for no body) and decodes a 2xx answer into out (nil to discard).
func (c *client) json(ctx context.Context, method, path string, q url.Values, v, out any) error {
	var body io.Reader
	ct := ""
	if v != nil {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		body, ct = strings.NewReader(string(data)), "application/json"
	}
	resp, err := c.do(ctx, method, path, q, ct, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("partyline: unexpected answer: %w", err)
	}
	return nil
}
