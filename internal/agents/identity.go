package agents

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/store/db"
)

// Limits of identity fields sent by sessions.
const (
	// MaxMachineLength is the longest machine name kept, in characters.
	MaxMachineLength = 64
	// DefaultHarness is the harness of sessions that name none, or an unknown one.
	DefaultHarness = "agent"
)

// harnessLabels are the readable names of the known harnesses (ID-01). The keys are the
// launcher ids (`partyline <harness>`). codex, gemini and antigravity are no longer supported (AD-034): their
// labels stay so that an AI created with one keeps its app name; a new session of one is refused
// (RemovedHarness).
var harnessLabels = map[string]string{
	"claude":      "Claude Code",
	"opencode":    "OpenCode",
	"pi":          "pi",
	"omp":         "oh-my-pi",
	"codex":       "Codex",
	"gemini":      "Gemini",
	"antigravity": "Antigravity",
	"agent":       "Agent",
}

// harnessAliases map other launcher names to their harness id.
var harnessAliases = map[string]string{"claudecode": "claude", "claude-code": "claude"}

// removedHarnesses are the harness ids, and other names for them, of the harnesses Partyline dropped (AD-034).
var removedHarnesses = map[string]string{"codex": "codex", "gemini": "gemini", "antigravity": "antigravity", "agy": "antigravity"}

// RemovedHarness returns the readable name of a harness Partyline no longer supports when a header value
// names one ("Codex"), else "" and false.
func RemovedHarness(v string) (label string, removed bool) {
	id, ok := removedHarnesses[strings.ToLower(strings.TrimSpace(v))]
	if !ok {
		return "", false
	}
	return harnessLabels[id], true
}

// HarnessID returns the harness id for a header value: a known id or alias, else "agent".
func HarnessID(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if id, ok := harnessAliases[v]; ok {
		return id
	}
	if _, ok := harnessLabels[v]; ok {
		return v
	}
	return DefaultHarness
}

// PushedHarness reports whether a harness gets its messages pushed by a separate `partyline listen`
// process (pi and OpenCode): the tools half it runs holds no socket and never reads a queue (SESS-46).
func PushedHarness(v string) bool {
	id := HarnessID(v)
	return id == "pi" || id == "opencode"
}

// HarnessLabel is the readable name of a harness id ("Claude Code"); unknown ids read "Agent".
func HarnessLabel(id string) string {
	if l, ok := harnessLabels[id]; ok {
		return l
	}
	return harnessLabels[DefaultHarness]
}

// cleanText trims s, drops control characters and cuts it to limit characters.
func cleanText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > limit {
		s = strings.TrimSpace(string([]rune(s)[:limit]))
	}
	return s
}

// MarkConnected stamps the agent's last connect.
func (r *Registry) MarkConnected(ctx context.Context, agentID string) error {
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.MarkAgentConnected(ctx, db.MarkAgentConnectedParams{ConnectedAt: nullMillis(r.now()), ID: agentID})
	})
}
