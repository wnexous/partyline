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

// client calls the agent REST API (Authorization: Bearer + X-Partyline-Agent).
type client struct {
	base  *url.URL
	token string
	agent func() string // the agent name in use
	http  *http.Client
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
	req.Header.Set(dto.HeaderAgent, c.agent())
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
	if resp.StatusCode >= 500 {
		return fmt.Errorf("partyline: server error (%d): %s", resp.StatusCode, body.Error.Message)
	}
	msg := body.Error.Message
	if len(body.Error.Fields) > 0 {
		parts := make([]string, 0, len(body.Error.Fields))
		for _, f := range slices.Sorted(maps.Keys(body.Error.Fields)) {
			parts = append(parts, f+": "+body.Error.Fields[f])
		}
		msg += " (" + strings.Join(parts, "; ") + ")"
	}
	return errors.New(msg)
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

// register registers the agent without a WebSocket (tools-only channel, PUSH-02); a failure
// is reported on stderr and the tools answer with the server's error later.
func (c *client) register(ctx context.Context, stderr io.Writer) {
	var info dto.AgentInfo
	if err := c.json(ctx, http.MethodPost, "agent/register", nil, nil, &info); err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "partyline: cannot register agent %s: %v\n", c.agent(), err)
	}
}
