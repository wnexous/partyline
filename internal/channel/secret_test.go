package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// exampleValue is the secret the tests give: a value with a newline, as a PEM key or a pasted list has.
const exampleValue = "example-secret-value-7f3a\nsecond line"

// secretOptions start a channel of the AI "builder" whose client knows the secret frame ("dev", SR-10) and
// whose secrets go to a folder of the test's own (never the user's config directory).
func (e *chanEnv) secretOptions() Options {
	o := e.options()
	o.Version = "dev"
	o.SecretsDir = filepath.Join(e.t.TempDir(), "partyline", "secrets")
	return o
}

// giveSecret posts value as alice for the secret request messageID and returns the answer.
func (e *chanEnv) giveSecret(messageID, value string) *testkit.Response {
	e.t.Helper()
	return e.alice.Post("/api/v1/rooms/"+e.room.ID+"/messages/"+messageID+"/secret", dto.GiveSecretRequest{Value: value})
}

// requestSecret calls request_secret through the session and returns the tool's answer, decoded.
func requestSecret(t *testing.T, s *session, room, name, text string) map[string]string {
	t.Helper()
	out, isErr := s.call("request_secret", toolArgs("room", room, "name", name, "text", text))
	require.False(t, isErr, out)
	var res map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	return res
}

// SR-01: request_secret posts the reason as the text and the name as the interactive part, and returns at
// once with the message id and the file the value will be written to: the AI's own folder of secrets.
func TestRequestSecretPostsTheRequestAndNamesTheFile(t *testing.T) {
	e := newChanEnv(t)
	o := e.secretOptions()
	s := e.connected(o)

	res := requestSecret(t, s, e.room.ID, "EXAMPLE_API_KEY", "To send the weekly report by email.")
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, stored.ID, res["message_id"])
	require.Equal(t, filepath.Join(o.SecretsDir, e.agentID, "EXAMPLE_API_KEY"), res["path"])
	require.Contains(t, res["status"], "never print it")
	require.Equal(t, "To send the weekly report by email.", stored.Text)
	require.Equal(t, e.agentID, stored.AuthorID)
	require.Equal(t, &dto.MessageInteractive{V: 1, Secret: &dto.MessageSecret{Name: "EXAMPLE_API_KEY"}}, stored.Interactive)
	_, err := os.Stat(res["path"])
	require.ErrorIs(t, err, os.ErrNotExist, "nothing is written before someone gives the value")
}

// SR-02: request_secret refuses a connection target and a name outside the pattern, and a text that says
// nothing or too much, each with a sentence that says why; nothing is posted.
func TestRequestSecretRefusals(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.secretOptions())
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a connection":          {toolArgs("connection", "conn-1", "chat", "chat-1", "name", "EXAMPLE_API_KEY", "text", "why"), secretOnAChat},
		"a chat alone":          {toolArgs("room", e.room.ID, "chat", "chat-1", "name", "EXAMPLE_API_KEY", "text", "why"), secretOnAChat},
		"lower case":            {toolArgs("room", e.room.ID, "name", "example_api_key", "text", "why"), secretBadName},
		"a leading digit":       {toolArgs("room", e.room.ID, "name", "1KEY", "text", "why"), secretBadName},
		"a path":                {toolArgs("room", e.room.ID, "name", "../KEY", "text", "why"), secretBadName},
		"65 characters":         {toolArgs("room", e.room.ID, "name", "K"+strings.Repeat("E", 64), "text", "why"), secretBadName},
		"no text":               {toolArgs("room", e.room.ID, "name", "EXAMPLE_API_KEY", "text", "  "), secretNoText},
		"more than 500 of text": {toolArgs("room", e.room.ID, "name", "EXAMPLE_API_KEY", "text", strings.Repeat("é", 501)), secretLongText},
		"no room":               {toolArgs("name", "EXAMPLE_API_KEY", "text", "why"), "room is required"},
	} {
		text, isErr := s.call("request_secret", tc.args)
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, tc.want, text, name)
	}
	require.Empty(t, e.messages(e.room.ID), "a refused request is not posted")

	// 64 characters and 500 of text are the limits, not refusals.
	res := requestSecret(t, s, e.room.ID, "K"+strings.Repeat("E", 63), strings.Repeat("é", 500))
	require.NotEmpty(t, res["message_id"])
}

// SR-06, SR-08, SR-09: a person who manages the AI gives the value; the session that asked writes it to its
// file and the server records the answer; the reply reaches the AI saying where the file is and by whom, and
// to read it and never print it. The value reaches the harness in no notification at all.
func TestAGivenSecretIsWrittenByTheSessionThatAsked(t *testing.T) {
	e := newChanEnv(t)
	o := e.secretOptions()
	s := e.connected(o)
	res := requestSecret(t, s, e.room.ID, "EXAMPLE_API_KEY", "To send the weekly report.")

	answer := testkit.Decode[dto.Message](e.giveSecret(res["message_id"], exampleValue).RequireStatus(http.StatusCreated))
	require.Equal(t, "🔑 EXAMPLE_API_KEY saved", answer.Text)
	require.Equal(t, res["message_id"], answer.ReplyTo)
	require.Equal(t, e.alice.User.ID, answer.AuthorID)
	require.Equal(t, &dto.MessageAnswers{V: 1, Secret: &dto.MessageSecretAnswer{Name: "EXAMPLE_API_KEY", Saved: true}}, answer.Answers)

	data, err := os.ReadFile(res["path"])
	require.NoError(t, err)
	require.Equal(t, exampleValue, string(data))

	n := s.notification()
	require.Equal(t, answer.ID, n.Meta["message_id"])
	require.Equal(t, res["path"], n.Meta["secret_path"])
	require.Contains(t, n.Content, "🔑 EXAMPLE_API_KEY saved")
	require.Contains(t, n.Content, "The secret EXAMPLE_API_KEY was saved to "+res["path"]+" by alice (@alice).")
	require.Contains(t, n.Content, "never print it, and never paste it in a room")
	require.NotContains(t, n.Content+n.Meta["answers"], "example-secret-value")
	s.expectNoNotification(200 * time.Millisecond)
	require.NotContains(t, s.stderr.String(), "example-secret-value", "nothing of the value on stderr")
}

// SR-06, SR-08: a session that cannot write the file answers with the reason; the person gets 504 not_saved
// with it, and nothing is recorded.
func TestASecretTheSessionCannotWriteIsNotSaved(t *testing.T) {
	e := newChanEnv(t)
	o := e.secretOptions()
	require.NoError(t, os.MkdirAll(filepath.Dir(o.SecretsDir), 0o700))
	require.NoError(t, os.WriteFile(o.SecretsDir, []byte("a file where the folder should be"), 0o600))
	s := e.connected(o)
	res := requestSecret(t, s, e.room.ID, "EXAMPLE_API_KEY", "To send the weekly report.")

	resp := e.giveSecret(res["message_id"], exampleValue)
	resp.RequireStatus(http.StatusGatewayTimeout)
	body := testkit.Decode[dto.ErrorBody](resp).Error
	require.Equal(t, dto.ErrorCodeNotSaved, body.Code)
	require.Contains(t, body.Message, o.SecretsDir, "the session's reason names the folder")
	require.NotContains(t, body.Message, "example-secret-value")
	require.Len(t, e.messages(e.room.ID), 1, "only the request: no answer is recorded")
	s.expectNoNotification(200 * time.Millisecond)
}

// SR-08: `partyline listen`, which holds the socket of pi and OpenCode, writes the secret too, and prints
// no line for it; the answer's line says where the file is.
func TestListenWritesTheSecretAndPrintsNoLineOfIt(t *testing.T) {
	e := newChanEnv(t)
	dir := filepath.Join(t.TempDir(), "secrets")
	r := &listenRun{err: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		r.err <- Listen(ctx, ListenOptions{
			URL: e.env.URL, Token: e.secret, Machine: builderFolder.Machine, Folder: builderFolder.Folder, Harness: "pi",
			Version: "dev", SecretsDir: dir, Stdout: r, Stderr: &syncBuffer{}, timing: fastTiming, zone: personZone,
		})
	}()
	require.Eventually(t, e.online, wait, 10*time.Millisecond)
	ai := e.alice.Token(dto.TokenKindAgent, "tools").AsFolder(testkit.Folder{Machine: builderFolder.Machine, Path: builderFolder.Folder, Harness: "pi"})
	request := testkit.Decode[dto.Message](ai.Post("/api/v1/agent/rooms/"+e.room.ID+"/messages", dto.PostMessageRequest{
		Text: "To send the weekly report.", Secret: &dto.MessageSecret{Name: "EXAMPLE_API_KEY"},
	}).RequireStatus(http.StatusCreated))

	e.giveSecret(request.ID, exampleValue).RequireStatus(http.StatusCreated)
	path := filepath.Join(dir, e.agentID, "EXAMPLE_API_KEY")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, exampleValue, string(data))
	require.Eventually(t, func() bool { return len(r.lines()) > 0 }, wait, 10*time.Millisecond)
	lines := r.lines()
	require.Equal(t, path, lines[len(lines)-1]["secret_path"])
	r.mu.Lock()
	out := r.out.String()
	r.mu.Unlock()
	require.NotContains(t, out, "example-secret-value")
}
