package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/users"
)

// syncBuffer is a bytes.Buffer safe for the server goroutine and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// logLines decodes every JSON log line written so far.
func logLines(t *testing.T, b *syncBuffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), "log line: %s", sc.Text())
		out = append(out, m)
	}
	return out
}

func findLog(lines []map[string]any, msg string) map[string]any {
	for _, l := range lines {
		if l["msg"] == msg {
			return l
		}
	}
	return nil
}

type server struct {
	logs *syncBuffer
	base string
	stop func() int
}

// serveDeadline bounds the start and the shutdown of `partyline serve` in tests.
const serveDeadline = 2 * time.Minute

// startServe runs `partyline serve` on a random local port until stop is called.
func startServe(t *testing.T, dataDir string) *server {
	t.Helper()
	return startServeWith(t, dataDir, nil)
}

// startServeWith is startServe with more variables in the environment of the server.
func startServeWith(t *testing.T, dataDir string, extra map[string]string) *server {
	t.Helper()
	env := map[string]string{
		"PARTYLINE_ADDR":       "127.0.0.1:0",
		"PARTYLINE_DATA_DIR":   dataDir,
		"PARTYLINE_LOG_FORMAT": "json",
		"PARTYLINE_LOG_LEVEL":  "debug",
	}
	maps.Copy(env, extra)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- execute(ctx, []string{"partyline", "serve"}, stdio{stdin: strings.NewReader(""), stdout: logs, stderr: logs, getenv: func(k string) string { return env[k] }})
	}()
	// Wait for the listening line, failing at once when serve exits early. The deadline is
	// generous: on a loaded machine opening and migrating the database can take many seconds.
	var addr string
	deadline := time.After(serveDeadline)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for addr == "" {
		select {
		case code := <-done:
			cancel()
			t.Fatalf("serve exited with %d before listening: %s", code, logs.String())
		case <-deadline:
			cancel()
			t.Fatalf("server did not start within %s: %s", serveDeadline, logs.String())
		case <-tick.C:
			if l := findLog(logLines(t, logs), msgListening); l != nil {
				addr, _ = l["addr"].(string)
			}
		}
	}
	s := &server{logs: logs, base: "http://" + addr}
	s.stop = func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(serveDeadline):
			t.Fatal("serve did not shut down")
			return -1
		}
	}
	return s
}

func (s *server) do(t *testing.T, method, path, cookie, bearer string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(method, s.base+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080") // the default public URL
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "pl_session", Value: cookie})
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func sessionCookie(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "pl_session" {
			return c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// AUTH-01, SEC-05: serve logs the setup token only while setup is needed, and the log of a
// session that sets up, logs in, creates a token and posts a message holds neither the
// message text nor passwords nor token secrets. Shutdown is graceful (exit 0).
func TestServeSetupTokenAndRedactedLogs(t *testing.T) {
	dataDir := t.TempDir()
	srv := startServe(t, dataDir)

	setupLine := findLog(logLines(t, srv.logs), msgSetupToken)
	require.NotNil(t, setupLine, "setup token line missing: %s", srv.logs.String())
	token, _ := setupLine["setup_token"].(string)
	require.NotEmpty(t, token)
	require.Equal(t, "WARN", setupLine["level"], "the setup line stands out")

	const password = "very-secret-password-1"
	const wrongPassword = "wrong-guess-password-2"
	const text = "launch codes are 0000"
	resp := srv.do(t, http.MethodPost, "/api/v1/setup", "", "", dto.SetupRequest{Token: token, Username: "admin", Password: password})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	cookie := sessionCookie(t, resp)
	require.Equal(t, http.StatusUnauthorized, srv.do(t, http.MethodPost, "/api/v1/auth/login", "", "", dto.LoginRequest{Username: "admin", Password: wrongPassword}).StatusCode)
	resp = srv.do(t, http.MethodPost, "/api/v1/auth/login", "", "", dto.LoginRequest{Username: "admin", Password: password})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = srv.do(t, http.MethodPost, "/api/v1/tokens", cookie, "", dto.CreateTokenRequest{Kind: dto.TokenKindUser, Name: "script"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var tok dto.CreatedToken
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))

	resp = srv.do(t, http.MethodPost, "/api/v1/rooms", "", tok.Secret, dto.CreateRoomRequest{Name: "ops"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var room dto.Room
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&room))
	resp = srv.do(t, http.MethodPost, "/api/v1/rooms/"+room.ID+"/messages", "", tok.Secret, dto.PostMessageRequest{Text: text})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	// A bad bearer header must not end up in the log either.
	require.Equal(t, http.StatusUnauthorized, srv.do(t, http.MethodGet, "/api/v1/me", "", "pl_user_notarealtoken0000", nil).StatusCode)

	require.Equal(t, 0, srv.stop())
	logs := srv.logs.String()
	require.NotNil(t, findLog(logLines(t, srv.logs), msgStopped), "graceful shutdown logged: %s", logs)
	for _, secret := range []string{password, wrongPassword, text, tok.Secret, cookie, "pl_user_notarealtoken0000"} {
		require.NotContains(t, logs, secret)
	}

	// Once an account exists the token is no longer printed.
	srv = startServe(t, dataDir)
	require.Nil(t, findLog(logLines(t, srv.logs), msgSetupToken))
	require.NotContains(t, srv.logs.String(), token)
	require.Equal(t, 0, srv.stop())
}

// oldDatabase writes a database with one applied migration to dir, so the next serve has
// pending migrations and data to back up.
func oldDatabase(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, app.DatabaseFile))
	require.NoError(t, err)
	require.NoError(t, st.MigrateBefore(context.Background(), "0002"))
	require.NoError(t, st.Close())
}

// OPS-07: `partyline serve` copies the database before it applies pending migrations, logs the
// version it starts from and the path, writes nothing when nothing is pending, and
// PARTYLINE_SKIP_MIGRATION_BACKUP=1 turns the copy off.
func TestServeBacksUpBeforeMigrations(t *testing.T) {
	dataDir := t.TempDir()
	oldDatabase(t, dataDir)

	srv := startServeWith(t, dataDir, nil)
	backups, err := filepath.Glob(filepath.Join(dataDir, store.BackupsDirName, "partyline-*.db"))
	require.NoError(t, err)
	require.Len(t, backups, 1)
	line := findLog(logLines(t, srv.logs), "migration backup written")
	require.NotNil(t, line, "the backup is logged: %s", srv.logs.String())
	require.Equal(t, "0001_init", line["from"])
	require.Equal(t, backups[0], line["path"])
	require.Equal(t, 0, srv.stop())

	// The database is up to date now: a second start writes no second backup.
	srv = startServe(t, dataDir)
	backups, err = filepath.Glob(filepath.Join(dataDir, store.BackupsDirName, "partyline-*.db"))
	require.NoError(t, err)
	require.Len(t, backups, 1)
	require.Equal(t, 0, srv.stop())

	skipped := t.TempDir()
	oldDatabase(t, skipped)
	srv = startServeWith(t, skipped, map[string]string{"PARTYLINE_SKIP_MIGRATION_BACKUP": "1"})
	backups, err = filepath.Glob(filepath.Join(skipped, store.BackupsDirName, "partyline-*.db"))
	require.NoError(t, err)
	require.Empty(t, backups)
	require.NotNil(t, findLog(logLines(t, srv.logs), "migration backup skipped by PARTYLINE_SKIP_MIGRATION_BACKUP"),
		"the bypass is logged: %s", srv.logs.String())
	require.Equal(t, 0, srv.stop())
}

func runWithStdin(t *testing.T, dataDir, stdin string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	env := map[string]string{"PARTYLINE_DATA_DIR": dataDir, "PARTYLINE_LOG_FORMAT": "text"}
	code := execute(context.Background(), append([]string{"partyline"}, args...), stdio{
		stdin: strings.NewReader(stdin), stdout: &out, stderr: &out, getenv: func(k string) string { return env[k] },
	})
	return code, out.String()
}

func login(t *testing.T, dataDir, username, password string) (string, error) {
	t.Helper()
	cfg, err := config.Load(func(k string) string { return map[string]string{"PARTYLINE_DATA_DIR": dataDir}[k] })
	require.NoError(t, err)
	a, err := app.New(cfg, app.Options{Logger: slog.New(slog.DiscardHandler)})
	require.NoError(t, err)
	defer func() { _ = a.Shutdown(context.Background()) }()
	require.NoError(t, a.Store.Migrate(context.Background()))
	u, _, err := a.Users.Login(context.Background(), users.LoginInput{Username: username, Password: password, IP: "127.0.0.1"})
	return u.Role, err
}

// AUTH-01 alternative: `admin create-user` creates an account usable for login; the password
// is read from stdin, never from the command line.
func TestAdminCreateUserAndResetPassword(t *testing.T) {
	dataDir := t.TempDir()

	code, out := runWithStdin(t, dataDir, "first-password-1\n", "admin", "create-user", "-username", "alice", "-admin")
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "alice")
	role, err := login(t, dataDir, "alice", "first-password-1")
	require.NoError(t, err)
	require.Equal(t, "admin", role)

	code, out = runWithStdin(t, dataDir, "second-password-2\n", "admin", "create-user", "-username", "bob")
	require.Equal(t, 0, code, out)
	role, err = login(t, dataDir, "bob", "second-password-2")
	require.NoError(t, err)
	require.Equal(t, "member", role)

	code, out = runWithStdin(t, dataDir, "third-password-3\n", "admin", "reset-password", "-username", "alice")
	require.Equal(t, 0, code, out)
	_, err = login(t, dataDir, "alice", "first-password-1")
	require.ErrorIs(t, err, users.ErrInvalidCredentials)
	_, err = login(t, dataDir, "alice", "third-password-3")
	require.NoError(t, err)

	// Rule violations and unknown users fail with exit code 1 and a reason.
	code, out = runWithStdin(t, dataDir, "short\n", "admin", "create-user", "-username", "carol")
	require.Equal(t, 1, code)
	require.Contains(t, out, "password")
	code, out = runWithStdin(t, dataDir, "fourth-password-4\n", "admin", "create-user", "-username", "alice")
	require.Equal(t, 1, code)
	require.Contains(t, out, "taken")
	code, out = runWithStdin(t, dataDir, "fourth-password-4\n", "admin", "reset-password", "-username", "nobody")
	require.Equal(t, 1, code)
	require.Contains(t, out, "not found")
	code, _ = runWithStdin(t, dataDir, "", "admin", "create-user")
	require.Equal(t, 2, code, "missing -username is a usage error")
	code, _ = runWithStdin(t, dataDir, "", "admin", "nope")
	require.Equal(t, 2, code)
}

// SEC-05: the logger redacts secret-looking attributes wherever they are logged.
func TestLoggerRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	cfg, err := config.Load(func(k string) string { return map[string]string{"PARTYLINE_LOG_FORMAT": "json"}[k] })
	require.NoError(t, err)
	log := newLogger(cfg, &buf)
	log.Info("event", "password", "p1", "token", "t1", "secret", "s1", "text", "m1",
		"authorization", "Bearer x", "cookie", "c1", slog.Group("req", "api_key", "k1"), "user_id", "u1")
	out := buf.String()
	for _, v := range []string{"p1", "t1", "s1", "m1", "Bearer x", "c1", "k1"} {
		require.NotContains(t, out, fmt.Sprintf("%q", v))
	}
	require.Contains(t, out, `"user_id":"u1"`)
	require.Equal(t, 7, strings.Count(out, redacted))
}
