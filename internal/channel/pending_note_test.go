package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// worktreeNoChannelNote is what a tools-only process in a linked worktree is told once: it has no
// live channel, so nothing is addressed to it and it only writes and reads history (SESS-06).
const worktreeNoChannelNote = "This worktree session has no live channel: it only writes and reads history."

// calls records every request a fake API receives, so a test can prove that the worktree reader
// never asks the server at all.
type calls struct {
	mu   sync.Mutex
	list []string
}

func (c *calls) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, r.Method+" "+r.URL.RequestURI())
}

func (c *calls) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.list...)
}

// pendingRecorder is a fake API that answers GET /agent/pending with body and records every call.
func pendingRecorder(t *testing.T, body string) (string, *calls) {
	t.Helper()
	c := &calls{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, c
}

// oneMessageBody is a pending answer with one room message, as a server wrote it.
func oneMessageBody(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(dto.AgentPending{
		Items:    []dto.AgentMessage{pendingMessage("m1", "hello from the room", 0)},
		Platform: []dto.PlatformEvent{},
	})
	require.NoError(t, err)
	return string(data)
}

// SESS-06: wait_for_messages of a tools-only process in a linked worktree never asks the server,
// so it cannot drain the principal's queue even against an older server; the first answer says
// once that the session has no live channel, and later ones do not.
func TestWorktreeToolsOnlyWaitForMessagesDoesNotAskTheServer(t *testing.T) {
	url, rec := pendingRecorder(t, oneMessageBody(t))
	s := startSession(t, Options{
		URL: url, Token: "pl_agent_x", Machine: "testbox", Folder: "/repo/wt", MainFolder: "/repo",
		ToolsOnly: true, TempDir: t.TempDir(), timing: fastTiming,
	})
	s.handshake()

	first, isErr := s.call("wait_for_messages", map[string]any{"timeout_seconds": 7})
	require.False(t, isErr, first)
	require.Contains(t, first, worktreeNoChannelNote)
	second, isErr := s.call("wait_for_messages", map[string]any{"timeout_seconds": 7})
	require.False(t, isErr, second)
	require.NotContains(t, second, worktreeNoChannelNote, "the note is said once per process")
	require.Empty(t, rec.all(), "the worktree reader never asks the server")
}

// SESS-06: a tools-only process in the AI's own folder is the principal's reader and asks the
// server as before, whether it sends no main folder or a main folder equal to its folder; the
// note is not part of its answers.
func TestPrincipalFolderToolsOnlyWaitForMessagesAsksTheServer(t *testing.T) {
	for name, main := range map[string]string{"no main folder": "", "the same main folder": "/repo"} {
		t.Run(name, func(t *testing.T) {
			url, rec := pendingRecorder(t, oneMessageBody(t))
			s := startSession(t, Options{
				URL: url, Token: "pl_agent_x", Machine: "testbox", Folder: "/repo", MainFolder: main,
				ToolsOnly: true, TempDir: t.TempDir(), timing: fastTiming,
			})
			s.handshake()

			text, isErr := s.call("wait_for_messages", map[string]any{"timeout_seconds": 7})
			require.False(t, isErr, text)
			require.Contains(t, text, "hello from the room", "the principal's queue is read as today")
			require.NotContains(t, text, worktreeNoChannelNote)
			require.Equal(t, []string{"GET /api/v1/agent/pending?platform=1&wait=7"}, rec.all())
		})
	}
}

// A process that never runs wait_for_messages asks nothing either, and one call is enough to say
// the note: the bound below only proves the answer is prompt, not that it waited.
func TestWorktreeToolsOnlyWaitForMessagesAnswersAtOnce(t *testing.T) {
	url, rec := pendingRecorder(t, oneMessageBody(t))
	s := startSession(t, Options{
		URL: url, Token: "pl_agent_x", Machine: "testbox", Folder: "/repo/wt", MainFolder: "/repo",
		ToolsOnly: true, TempDir: t.TempDir(), timing: fastTiming,
	})
	s.handshake()
	start := time.Now()
	text, isErr := s.call("wait_for_messages", map[string]any{"timeout_seconds": 300})
	require.False(t, isErr, text)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Empty(t, rec.all())
}
