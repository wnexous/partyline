package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// MaxWaitSeconds bounds wait_for_messages (TURN-01).
const MaxWaitSeconds = 300

// defaultWaitSeconds is the wait when the model gives no timeout_seconds.
const defaultWaitSeconds = 60

// worktreeNoteSaid remembers, per tools-only process, that the worktree note was already part of
// an answer (SESS-06): the key is the process's API client, which Run creates once.
var worktreeNoteSaid sync.Map

// isLinkedWorktree is whether the process runs in a linked git worktree: it sent a main folder
// different from its own (SESS-01). Only a tools-only process there has no live channel (SESS-06).
func isLinkedWorktree(id Identity) bool { return id.MainFolder != "" && id.MainFolder != id.Folder }

// pending fetches the agent's undelivered room messages and platform events (GET
// /agent/pending?platform=1), waiting up to wait seconds for the first of either or for a count of
// dropped platform messages; they count as delivered once returned. A server from before platform
// events answers with the room messages alone.
func (c *client) pending(ctx context.Context, wait int) (dto.AgentPending, error) {
	q := url.Values{"platform": {"1"}}
	if wait > 0 {
		q.Set("wait", strconv.Itoa(wait))
	}
	var p dto.AgentPending
	if err := c.json(ctx, http.MethodGet, "agent/pending", q, nil, &p); err != nil {
		return dto.AgentPending{}, err
	}
	return p, nil
}

// registerWait adds wait_for_messages, the way a session without pushed messages (Codex,
// Gemini, Antigravity) receives them (TURN-01).
func (t *tools) registerWait(srv *mcp.Server) {
	srv.HandleTool(mcp.ToolDef{
		Name:        "wait_for_messages",
		Description: "Wait for new messages, from your rooms and from the connections you operate; this session does not receive them on its own. Returns what arrived (each counts as delivered), or says that nothing arrived within timeout_seconds.",
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
	if isLinkedWorktree(t.api.identity) && t.api.identity.LiveID == "" {
		// A tools-only process in a linked worktree holds no live channel (no live id): nothing is addressed to
		// its label and the queue is the principal's, so it never asks the server (SESS-06). It
		// says why once, then answers as it always does.
		if _, said := worktreeNoteSaid.LoadOrStore(t.api, true); !said {
			return "no new messages: " + WorktreeToolsOnlyNote, nil
		}
		return "no new messages", nil
	}
	p, err := t.api.pending(ctx, wait)
	if err != nil {
		return "", err
	}
	if text := FramePending(p); text != "" {
		return text, nil
	}
	return fmt.Sprintf("no new messages in %d seconds", wait), nil
}

// PendingOptions configure Pending.
type PendingOptions struct {
	URL, Token string
	// Identity is the session's machine and folder: the AI whose undelivered messages are
	// returned (created on first sight, like for any call of its folder).
	Identity   Identity
	HTTPClient *http.Client
}

// Pending returns the agent's undelivered room messages and platform events, and the count of the
// platform messages the server dropped, without waiting; they count as delivered (the turn-end
// hooks, TURN-02, CID-06, CID-10).
func Pending(ctx context.Context, o PendingOptions) (dto.AgentPending, error) {
	base, err := parseBase(o.URL)
	if err != nil {
		return dto.AgentPending{}, err
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	c := &client{base: base, token: o.Token, identity: o.Identity, http: o.HTTPClient}
	return c.pending(ctx, 0)
}
