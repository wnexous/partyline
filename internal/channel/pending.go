package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// MaxWaitSeconds bounds wait_for_messages (TURN-01).
const MaxWaitSeconds = 300

// defaultWaitSeconds is the wait when the model gives no timeout_seconds.
const defaultWaitSeconds = 60

// pending fetches the agent's undelivered messages (GET /agent/pending), waiting up to wait
// seconds for the first one; they count as delivered once returned.
func (c *client) pending(ctx context.Context, wait int) ([]dto.AgentMessage, error) {
	q := url.Values{}
	if wait > 0 {
		q.Set("wait", strconv.Itoa(wait))
	}
	var page dto.List[dto.AgentMessage]
	if err := c.json(ctx, http.MethodGet, "agent/pending", q, nil, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// registerWait adds wait_for_messages, the way a session without pushed messages (Codex,
// Gemini) receives them (TURN-01).
func (t *tools) registerWait(srv *mcp.Server) {
	srv.HandleTool(mcp.ToolDef{
		Name:        "wait_for_messages",
		Description: "Wait for new room messages; this session does not receive them on its own. Returns the messages that arrived (each counts as delivered), or says that none arrived within timeout_seconds.",
		InputSchema: schema(`{"type":"object","properties":{"timeout_seconds":{"type":"integer","minimum":0,"maximum":300,"description":"how long to wait for the first message (default 60, at most 300)"}}}`),
	}, t.guardFor(MaxWaitSeconds*time.Second+toolTimeout, t.waitForMessages))
}

func (t *tools) waitForMessages(ctx context.Context, args json.RawMessage) (string, error) {
	a := struct {
		Timeout *int `json:"timeout_seconds"`
	}{}
	if len(args) > 0 {
		if err := decode(args, &a); err != nil {
			return "", err
		}
	}
	wait := defaultWaitSeconds
	if a.Timeout != nil {
		wait = min(max(*a.Timeout, 0), MaxWaitSeconds)
	}
	msgs, err := t.api.pending(ctx, wait)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return fmt.Sprintf("no new messages in %d seconds", wait), nil
	}
	return FrameAll(msgs), nil
}

// PendingOptions configure Pending.
type PendingOptions struct {
	URL, Token, Agent string
	HTTPClient        *http.Client
}

// Pending returns the agent's undelivered messages without waiting and counts them as
// delivered (the turn-end hooks, TURN-02).
func Pending(ctx context.Context, o PendingOptions) ([]dto.AgentMessage, error) {
	base, err := parseBase(o.URL)
	if err != nil {
		return nil, err
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	name := agents.Slug(o.Agent)
	c := &client{base: base, token: o.Token, agent: func() string { return name }, http: o.HTTPClient}
	return c.pending(ctx, 0)
}
