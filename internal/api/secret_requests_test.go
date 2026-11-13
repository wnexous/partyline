package api_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/testkit"
)

// Secret requests over the room routes and the agent socket (SR-01..SR-07, SR-10): an AI posts the request,
// a person who manages it gives the value, the server hands it to the session that asked and records only
// that it was saved. Here the session is the test, over the raw agent socket, so every answer it can give is
// tested; the channel's own handling of the frame is tested in package channel.

// secretValue is the value the tests give: synthetic, with a newline, and unique enough to search for in
// every table, log line, frame and event.
const secretValue = "EXAMPLE-VALUE-4d9c1f\nline two"

// secretFixture is a group "ops" of alice with bob in it and alice's AI "atlas", which alice manages (she
// created it) and bob does not. ai is atlas's agent client acting as the process live; dial opens its socket.
type secretFixture struct {
	env        *testkit.Env
	alice, bob *testkit.Client
	aiID       string
	token      *testkit.Client
	live       string
	room       dto.Room
}

func newSecretFixture(t *testing.T, opts ...testkit.Option) *secretFixture {
	t.Helper()
	env := testkit.New(t, opts...)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	f := &secretFixture{env: env, alice: alice, bob: bob, live: testkit.NewLiveID()}
	f.aiID = env.NewAI(alice, "atlas")
	f.room = createRoom(alice, "ops")
	addToRoom(t, env, alice, f.room.ID, bob)
	alice.Post("/api/v1/rooms/"+f.room.ID+"/members",
		dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.aiID}).RequireStatus(http.StatusCreated)
	f.token = alice.Token(dto.TokenKindAgent, "laptop")
	return f
}

// folder is atlas's session as the process f.live, with the client version given ("" sends none).
func (f *secretFixture) folder(version string) testkit.Folder {
	folder := testkit.FolderOf("atlas")
	folder.Live, folder.ClientVersion = f.live, version
	return folder
}

// dial opens atlas's live session with the client version given and reads its welcome.
func (f *secretFixture) dial(t *testing.T, version string) *testkit.AgentConn {
	t.Helper()
	conn, resp := f.token.DialFolder(f.folder(version))
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(conn.Close)
	conn.Welcome(testkit.IOTimeout)
	conn.Drain(100 * time.Millisecond)
	return conn
}

// ask posts a secret request as atlas, from the process f.live, and returns it.
func (f *secretFixture) ask(t *testing.T, name string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.token.AsFolder(f.folder("dev")).Post("/api/v1/agent/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{Text: "To send the weekly report.", Secret: &dto.MessageSecret{Name: name}}).RequireStatus(http.StatusCreated))
}

func (f *secretFixture) secretPath(messageID string) string {
	return "/api/v1/rooms/" + f.room.ID + "/messages/" + messageID + "/secret"
}

// give sends the value as c in the background, since the server answers only once the session did; the
// answer arrives on the channel returned.
func (f *secretFixture) give(c *testkit.Client, messageID, value string) <-chan *testkit.Response {
	out := make(chan *testkit.Response, 1)
	go func() { out <- c.Post(f.secretPath(messageID), dto.GiveSecretRequest{Value: value}) }()
	return out
}

func answerOf(t *testing.T, ch <-chan *testkit.Response) *testkit.Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(testkit.IOTimeout):
		require.FailNow(t, "no answer to the secret")
		return nil
	}
}

func errorCode(t *testing.T, r *testkit.Response, status int) dto.Error {
	t.Helper()
	return testkit.Decode[dto.ErrorBody](r.RequireStatus(status)).Error
}

// SR-01, SR-03: an AI's secret request carries the name as its interactive part and the session it came
// from; a person's request is refused, as are a bad name, a text too long and a request with another shape.
func TestOnlyAnAIPostsASecretRequest(t *testing.T) {
	f := newSecretFixture(t)
	f.dial(t, "dev")
	request := f.ask(t, "EXAMPLE_API_KEY")
	require.Equal(t, "To send the weekly report.", request.Text)
	require.Equal(t, &dto.MessageInteractive{V: 1, Secret: &dto.MessageSecret{Name: "EXAMPLE_API_KEY"}}, request.Interactive)
	require.Empty(t, request.AuthorLabel, "posted by the principal session")

	person := f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{
		Text: "Give me a key", Secret: &dto.MessageSecret{Name: "EXAMPLE_API_KEY"},
	})
	require.Contains(t, errorCode(t, person, http.StatusUnprocessableEntity).Fields["secret"], "only an AI")

	ai := f.token.AsFolder(f.folder("dev"))
	for name, body := range map[string]dto.PostMessageRequest{
		"lower case": {Text: "why", Secret: &dto.MessageSecret{Name: "api_key"}},
		"a path":     {Text: "why", Secret: &dto.MessageSecret{Name: "../KEY"}},
		"no text":    {Text: " ", Secret: &dto.MessageSecret{Name: "KEY"}},
		"long text":  {Text: strings.Repeat("a", dto.MaxSecretReason+1), Secret: &dto.MessageSecret{Name: "KEY"}},
		"a question": {Text: "why", Secret: &dto.MessageSecret{Name: "KEY"}, QuickReplies: []string{"Sim"}},
	} {
		t.Run(name, func(*testing.T) {
			ai.Post("/api/v1/agent/rooms/"+f.room.ID+"/messages", body).RequireStatus(http.StatusUnprocessableEntity)
		})
	}
}

// SR-05, SR-06: the manager's value reaches the session that asked in one secret frame; once the session
// confirms, the answer is the reply "🔑 NAME saved" by the manager with its structured answer, and the AI
// gets it as a reply to its request. A second value is 409 already_answered and is never sent.
func TestAManagerGivesASecretOnce(t *testing.T) {
	f := newSecretFixture(t)
	conn := f.dial(t, "dev")
	request := f.ask(t, "EXAMPLE_API_KEY")
	conn.Drain(100 * time.Millisecond)

	answer := f.give(f.alice, request.ID, secretValue)
	frame := conn.Secret(testkit.IOTimeout)
	require.Equal(t, request.ID, frame.MessageID)
	require.Equal(t, f.room.ID, frame.RoomID)
	require.Equal(t, "EXAMPLE_API_KEY", frame.Name)
	require.Equal(t, secretValue, frame.Value)
	require.NotEmpty(t, frame.RequestID)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, OK: true, Path: "/home/a/.config/partyline/secrets/x/EXAMPLE_API_KEY"})

	saved := testkit.Decode[dto.Message](answerOf(t, answer).RequireStatus(http.StatusCreated))
	require.Equal(t, "🔑 EXAMPLE_API_KEY saved", saved.Text)
	require.Equal(t, request.ID, saved.ReplyTo)
	require.Equal(t, dto.AuthorKindUser, saved.AuthorKind)
	require.Equal(t, f.alice.User.ID, saved.AuthorID)
	require.Equal(t, &dto.MessageAnswers{V: 1, Secret: &dto.MessageSecretAnswer{Name: "EXAMPLE_API_KEY", Saved: true}}, saved.Answers)

	got := conn.Message(testkit.IOTimeout)
	require.Equal(t, saved.ID, got.Message.ID)
	require.True(t, got.Trusted, "the manager's answer is trusted")

	again := f.give(f.alice, request.ID, "another value")
	require.Equal(t, dto.ErrorCodeAlreadyAnswered, errorCode(t, answerOf(t, again), http.StatusConflict).Code)
	conn.ExpectNone(200 * time.Millisecond)
}

// SR-05: while one value is on its way to the session, a second one for the same request is refused (409
// conflict) and never sent; a deleted request asks for nothing any more (404).
func TestASecretIsSavedByOnePersonAtATime(t *testing.T) {
	f := newSecretFixture(t)
	conn := f.dial(t, "dev")
	request := f.ask(t, "EXAMPLE_API_KEY")
	conn.Drain(100 * time.Millisecond)

	first := f.give(f.alice, request.ID, secretValue)
	frame := conn.Secret(testkit.IOTimeout)
	second := f.alice.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: "another value"})
	require.Equal(t, dto.ErrorCodeConflict, errorCode(t, second, http.StatusConflict).Code)
	conn.ExpectNone(100 * time.Millisecond)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, OK: true, Path: "/p"})
	answerOf(t, first).RequireStatus(http.StatusCreated)

	deleted := f.ask(t, "OTHER_KEY")
	conn.Drain(100 * time.Millisecond)
	f.token.AsFolder(f.folder("dev")).Delete("/api/v1/agent/messages/" + deleted.ID).RequireStatus(http.StatusOK)
	errorCode(t, f.alice.Post(f.secretPath(deleted.ID), dto.GiveSecretRequest{Value: secretValue}), http.StatusNotFound)
	conn.Drain(100 * time.Millisecond)
}

// SR-05: only a person who manages the asking AI may give it a secret; a member who does not is 403, the AI
// itself is 403, a message that asks for nothing is 404, and a value outside 1..16384 bytes is 422. None of
// them sends a frame.
func TestOnlyAManagerGivesASecret(t *testing.T) {
	f := newSecretFixture(t)
	conn := f.dial(t, "dev")
	request := f.ask(t, "EXAMPLE_API_KEY")
	plain := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated))
	conn.Drain(100 * time.Millisecond)

	require.Equal(t, dto.ErrorCodeForbidden, errorCode(t, f.bob.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: secretValue}), http.StatusForbidden).Code)
	ai := f.token.AsFolder(f.folder("dev"))
	ai.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: secretValue}).RequireStatus(http.StatusForbidden)
	require.Equal(t, dto.ErrorCodeNotFound, errorCode(t, f.alice.Post(f.secretPath(plain.ID), dto.GiveSecretRequest{Value: secretValue}), http.StatusNotFound).Code)
	errorCode(t, f.alice.Post(f.secretPath("no-such-message"), dto.GiveSecretRequest{Value: secretValue}), http.StatusNotFound)
	f.alice.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: ""}).RequireStatus(http.StatusUnprocessableEntity)
	f.alice.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: strings.Repeat("k", dto.MaxSecretValue+1)}).RequireStatus(http.StatusUnprocessableEntity)
	conn.ExpectNone(200 * time.Millisecond)

	// 16384 bytes is the limit, not a refusal.
	answer := f.give(f.alice, request.ID, strings.Repeat("k", dto.MaxSecretValue))
	frame := conn.Secret(testkit.IOTimeout)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, OK: true, Path: "/p"})
	answerOf(t, answer).RequireStatus(http.StatusCreated)
}

// SR-06: with no live session, 409 ai_offline; the session that asked is the one the value goes to, so
// another session of the AI does not receive it.
func TestASecretForAnOfflineSessionIsRefused(t *testing.T) {
	f := newSecretFixture(t)
	request := f.ask(t, "EXAMPLE_API_KEY") // a tools-only call: no session is live
	require.Equal(t, dto.ErrorCodeAIOffline, errorCode(t, f.alice.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: secretValue}), http.StatusConflict).Code)

	// A parallel session (another worktree) is live, the principal that asked is not.
	worktree := testkit.FolderOf("atlas")
	worktree.Path, worktree.MainFolder, worktree.ClientVersion = "/work/atlas-feature", "/work/atlas", "dev"
	other, resp := f.token.DialFolder(worktree)
	require.NotNil(t, other, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(other.Close)
	other.Welcome(testkit.IOTimeout)
	require.Equal(t, dto.ErrorCodeAIOffline, errorCode(t, f.alice.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: secretValue}), http.StatusConflict).Code)
	for _, fr := range other.Drain(200 * time.Millisecond) {
		require.NotEqual(t, dto.AgentFrameTypeSecret, fr.Type)
	}
	require.Len(t, testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/"+f.room.ID+"/messages")).Items, 1, "nothing recorded")
}

// SR-10: a session whose client is older than MinSecretClientVersion, or sends no version, is never sent the
// value: 409 client_outdated before anything goes out.
func TestASecretForAnOutdatedClientIsRefused(t *testing.T) {
	for _, version := range []string{"v0.31.2", ""} {
		t.Run("version "+version, func(t *testing.T) {
			f := newSecretFixture(t)
			conn := f.dial(t, version)
			request := f.ask(t, "EXAMPLE_API_KEY")
			conn.Drain(100 * time.Millisecond)
			require.Equal(t, dto.ErrorCodeClientOutdated, errorCode(t, f.alice.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: secretValue}), http.StatusConflict).Code)
			conn.ExpectNone(200 * time.Millisecond)
		})
	}
}

// SR-06: no acknowledgement in time, or one that reports a failure, is 504 not_saved with the reason, and
// nothing is recorded: the request can be answered again.
func TestASecretTheSessionDoesNotConfirmIsNotSaved(t *testing.T) {
	f := newSecretFixture(t, testkit.WithSecretTimeout(300*time.Millisecond))
	conn := f.dial(t, "dev")
	request := f.ask(t, "EXAMPLE_API_KEY")
	conn.Drain(100 * time.Millisecond)

	silent := f.give(f.alice, request.ID, secretValue)
	conn.Secret(testkit.IOTimeout) // never answered
	timedOut := errorCode(t, answerOf(t, silent), http.StatusGatewayTimeout)
	require.Equal(t, dto.ErrorCodeNotSaved, timedOut.Code)
	require.Contains(t, timedOut.Message, "did not confirm")

	failed := f.give(f.alice, request.ID, secretValue)
	frame := conn.Secret(testkit.IOTimeout)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, Error: "write the secret file /x/EXAMPLE_API_KEY: no space left on device"})
	failure := errorCode(t, answerOf(t, failed), http.StatusGatewayTimeout)
	require.Equal(t, dto.ErrorCodeNotSaved, failure.Code)
	require.Contains(t, failure.Message, "no space left on device")

	// A session that echoes the value in its reason does not get it into the error text (SR-07).
	echoed := f.give(f.alice, request.ID, secretValue)
	frame = conn.Secret(testkit.IOTimeout)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, Error: "cannot save " + secretValue})
	require.NotContains(t, errorCode(t, answerOf(t, echoed), http.StatusGatewayTimeout).Message, "EXAMPLE-VALUE")

	require.Len(t, testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/"+f.room.ID+"/messages")).Items, 1, "nothing recorded")
	ok := f.give(f.alice, request.ID, secretValue)
	frame = conn.Secret(testkit.IOTimeout)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, OK: true, Path: "/p"})
	answerOf(t, ok).RequireStatus(http.StatusCreated)
}

// syncLog is a goroutine-safe buffer for the server's log.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// SR-07: the value is in the secret frame to the session and nowhere else: in no row of any table and no byte
// of the data directory, no log line (at debug level), no live frame to the people of the room, no frame to
// another session and no event on the bus. Failures and refusals are part of the run, so their paths are
// searched too.
func TestASecretValueIsKeptNowhere(t *testing.T) {
	logs := &syncLog{}
	f := newSecretFixture(t, testkit.WithLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))),
		testkit.WithSecretTimeout(300*time.Millisecond))
	var mu sync.Mutex
	var published []string
	sub, unsubscribe := f.env.App.Bus.Subscribe(1024)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range sub {
			mu.Lock()
			published = append(published, fmt.Sprintf("%#v", e))
			mu.Unlock()
		}
	}()
	aliceLive, _ := f.alice.DialLive("")
	bobLive, _ := f.bob.DialLive("")
	conn := f.dial(t, "dev")
	request := f.ask(t, "EXAMPLE_API_KEY")
	conn.Drain(100 * time.Millisecond)

	bobTry := f.bob.Post(f.secretPath(request.ID), dto.GiveSecretRequest{Value: secretValue})
	bobTry.RequireStatus(http.StatusForbidden)
	silent := f.give(f.alice, request.ID, secretValue)
	conn.Secret(testkit.IOTimeout)
	timedOut := answerOf(t, silent)
	timedOut.RequireStatus(http.StatusGatewayTimeout)
	given := f.give(f.alice, request.ID, secretValue)
	frame := conn.Secret(testkit.IOTimeout)
	conn.AckSecret(dto.AgentSecretAckFrame{RequestID: frame.RequestID, OK: true, Path: "/p"})
	saved := answerOf(t, given)
	saved.RequireStatus(http.StatusCreated)
	conn.Message(testkit.IOTimeout)

	needle := "EXAMPLE-VALUE-4d9c1f"
	for _, r := range []*testkit.Response{bobTry, timedOut, saved} {
		require.NotContains(t, string(r.Body), needle, "an answer of the route")
	}
	for _, page := range []string{"/api/v1/rooms/" + f.room.ID + "/messages", "/api/v1/activity"} {
		require.NotContains(t, string(f.alice.Get(page).RequireStatus(http.StatusOK).Body), needle, page)
	}
	for name, live := range map[string]*testkit.LiveConn{"alice": aliceLive, "bob": bobLive} {
		frames := live.Drain(300 * time.Millisecond)
		require.NotEmpty(t, frames, "%s saw the request and the answer", name)
		for _, fr := range frames {
			require.NotContains(t, string(fr.Data), needle, "a live frame to %s", name)
		}
	}
	for _, fr := range conn.Drain(200 * time.Millisecond) {
		require.NotContains(t, string(fr.Raw), needle, "a frame after the secret one")
	}
	require.NotContains(t, logs.String(), needle, "the server log")
	unsubscribe()
	<-done
	mu.Lock()
	require.NotEmpty(t, published)
	for _, e := range published {
		require.NotContains(t, e, needle, "an event")
	}
	mu.Unlock()
	requireNowhereInTheDatabase(t, f.env, needle)
}

// requireNowhereInTheDatabase fails when any column of any table holds needle, or any file of the data
// directory (the database, its WAL, the blobs) contains its bytes. The tables are read from the schema, so a
// table a later migration adds is searched too.
func requireNowhereInTheDatabase(t *testing.T, env *testkit.Env, needle string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(env.App.Config.DataDir, app.DatabaseFile)+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	rows, err := raw.Query(`SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type = 'table'`)
	require.NoError(t, err)
	var columns [][2]string
	for rows.Next() {
		var c [2]string
		require.NoError(t, rows.Scan(&c[0], &c[1]))
		columns = append(columns, c)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.NotEmpty(t, columns)
	for _, c := range columns {
		var n int
		require.NoError(t, raw.QueryRow(`SELECT COUNT(*) FROM "`+c[0]+`" WHERE instr(CAST("`+c[1]+`" AS BLOB), CAST(? AS BLOB)) > 0`, needle).Scan(&n))
		require.Zero(t, n, "%s.%s holds the value", c[0], c[1])
	}
	require.NoError(t, filepath.Walk(env.App.Config.DataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		file, err := os.Open(path) // #nosec G304 -- the test's own temp data dir
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		require.False(t, bytes.Contains(data, []byte(needle)), "%s holds the value", path)
		return nil
	}))
}
