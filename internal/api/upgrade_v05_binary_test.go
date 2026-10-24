package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/testkit"
)

// The upgrade end to end, on a database that the real v0.5.0 binary wrote. It is opt-in, because it needs that
// binary: build it from the tag in a temporary worktree and point PARTYLINE_V05_BINARY at it.
//
//	git worktree add --detach /tmp/pl-v05-src v0.5.0
//	(cd /tmp/pl-v05-src && CGO_ENABLED=0 go build -o /tmp/pl-v05 ./cmd/partyline)
//	PARTYLINE_V05_BINARY=/tmp/pl-v05 go test -count=1 -run TestUpgradedRealV05 ./internal/api/
//	git worktree remove --force /tmp/pl-v05-src
//
// The test drives that binary over HTTP, as people and their AIs do (users, AIs, rooms, messages, and every kind of
// grant), stops it, and hands the database file to the app of this branch, which upgrades it; then it runs the same
// checks as the upgrade test that builds its database from the store's fixtures.
const v05BinaryEnv = "PARTYLINE_V05_BINARY"

// v05Key is the secret key the v0.5.0 server and the app under test share, so that what the old server sealed (the
// secret of a connection) opens: 32 bytes of base64, synthetic.
var v05Key = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

// v05Server is the v0.5.0 binary, run in a data directory of its own with a home directory of its own, so that
// it reads and writes nothing of the machine's.
type v05Server struct {
	t         *testing.T
	bin, home string
	dataDir   string
	addr      string
	cmd       *exec.Cmd
	log       *bytes.Buffer // what the server printed; read once it has stopped
}

func newV05Server(t *testing.T, bin string) *v05Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	home := t.TempDir()
	return &v05Server{t: t, bin: bin, home: home, dataDir: filepath.Join(home, "data"), addr: addr, log: &bytes.Buffer{}}
}

func (s *v05Server) url() string { return "http://" + s.addr }

func (s *v05Server) environ() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + s.home, "XDG_CONFIG_HOME=" + s.home,
		"PARTYLINE_DATA_DIR=" + s.dataDir, "PARTYLINE_ADDR=" + s.addr, "PARTYLINE_PUBLIC_URL=" + s.url(),
		"PARTYLINE_SECRET_KEY=" + v05Key, "PARTYLINE_LOG_FORMAT=text",
	}
}

// run executes one command of the binary, with stdin as its input, and fails the test when it fails.
func (s *v05Server) run(stdin string, args ...string) string {
	s.t.Helper()
	cmd := exec.Command(s.bin, args...)
	cmd.Env = s.environ()
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	require.NoError(s.t, err, "%v: %s", args, out)
	return string(out)
}

// createUser makes an account the way an operator does, offline, with testkit.Password.
func (s *v05Server) createUser(username string, admin bool) {
	s.t.Helper()
	args := []string{"admin", "create-user", "-username", username}
	if admin {
		args = append(args, "-admin")
	}
	s.run(testkit.Password+"\n", args...)
}

// start runs the server and waits until it answers.
func (s *v05Server) start() {
	s.t.Helper()
	s.cmd = exec.Command(s.bin, "serve")
	s.cmd.Env = s.environ()
	s.cmd.Stdout, s.cmd.Stderr = s.log, s.log
	require.NoError(s.t, s.cmd.Start())
	s.t.Cleanup(func() {
		if s.cmd.ProcessState == nil { // a test that failed before stop
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(s.url() + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
			require.FailNow(s.t, "the v0.5.0 server did not start", "%s", s.log.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stop ends the server the way a deployment does (SIGTERM), so that its database is checkpointed.
func (s *v05Server) stop() {
	s.t.Helper()
	require.NoError(s.t, s.cmd.Process.Signal(syscall.SIGTERM))
	require.NoError(s.t, s.cmd.Wait(), "%s", s.log.String())
}

// v05User is a person logged in to the v0.5.0 server, or an AI token, with the requests the scenario makes.
type v05User struct {
	t      *testing.T
	s      *v05Server
	http   *http.Client
	bearer string
	id     string
}

func (s *v05Server) login(username string) *v05User {
	s.t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(s.t, err)
	u := &v05User{t: s.t, s: s, http: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
	var me struct {
		ID string `json:"id"`
	}
	u.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": testkit.Password}, &me, nil)
	u.id = me.ID
	require.NotEmpty(s.t, u.id)
	return u
}

// do sends a request, with body as JSON, and decodes the answer into out; any answer but 2xx fails the test.
func (u *v05User) do(method, path string, body, out any, headers map[string]string) {
	u.t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(u.t, err)
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, u.s.url()+path, rd)
	require.NoError(u.t, err)
	req.Header.Set("Origin", u.s.url())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if u.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+u.bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := u.http.Do(req)
	require.NoError(u.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(u.t, err)
	require.True(u.t, resp.StatusCode >= 200 && resp.StatusCode < 300, "%s %s: %d %s", method, path, resp.StatusCode, data)
	if out != nil {
		require.NoError(u.t, json.Unmarshal(data, out), "%s %s: %s", method, path, data)
	}
}

// agentToken mints an agent token and returns a client that sends it.
func (u *v05User) agentToken() *v05User {
	u.t.Helper()
	var created struct {
		Secret string `json:"secret"`
	}
	u.do(http.MethodPost, "/api/v1/tokens", map[string]string{"kind": "agent", "name": "laptop"}, &created, nil)
	return &v05User{t: u.t, s: u.s, http: &http.Client{Timeout: 20 * time.Second}, bearer: created.Secret, id: u.id}
}

// folderHeaders are what a session sends to say which folder it works in.
func folderHeaders(path string) map[string]string {
	return map[string]string{
		"X-Partyline-Machine": "devbox", "X-Partyline-Folder": url.PathEscape(path),
		"X-Partyline-Home": url.PathEscape("/home/alice"), "X-Partyline-Harness": "claude",
	}
}

// registerAI creates (or finds) the AI of a folder, as a tools-only session does, and returns its id.
func (u *v05User) registerAI(path string) string {
	u.t.Helper()
	var ai struct {
		ID string `json:"id"`
	}
	u.do(http.MethodPost, "/api/v1/agent/register", nil, &ai, folderHeaders(path))
	require.NotEmpty(u.t, ai.ID)
	return ai.ID
}

func (u *v05User) createRoom(name string) string {
	u.t.Helper()
	var room struct {
		ID string `json:"id"`
	}
	u.do(http.MethodPost, "/api/v1/rooms", map[string]string{"name": name}, &room, nil)
	return room.ID
}

func (u *v05User) addMember(room, kind, id string) {
	u.t.Helper()
	u.do(http.MethodPost, "/api/v1/rooms/"+room+"/members", map[string]string{"kind": kind, "member_id": id}, nil, nil)
}

func (u *v05User) post(room, text string) {
	u.t.Helper()
	u.do(http.MethodPost, "/api/v1/rooms/"+room+"/messages", map[string]string{"text": text}, nil, nil)
}

// postAs has the AI of the folder (this client holds an agent token) write in the room.
func (u *v05User) postAs(path, room, text string) {
	u.t.Helper()
	u.do(http.MethodPost, "/api/v1/agent/rooms/"+room+"/messages", map[string]string{"text": text}, nil, folderHeaders(path))
}

// share grants the user the level on a room, an agent or a connection ("rooms", "agents", "connectors"), the way v0.5
// shared things one by one; "" shares with everyone.
func (u *v05User) share(kind, id, userID, level string) {
	u.t.Helper()
	body := map[string]any{"mode": "public"}
	if userID != "" {
		body = map[string]any{"mode": "shared", "users": []map[string]string{{"user_id": userID, "level": level}}}
	}
	u.do(http.MethodPut, "/api/v1/"+kind+"/"+id+"/sharing", body, nil, nil)
}

// createWebhook makes a disabled webhook connection operated by the AI.
func (u *v05User) createWebhook(name, operator string) string {
	u.t.Helper()
	var conn struct {
		ID string `json:"id"`
	}
	u.do(http.MethodPost, "/api/v1/connectors", map[string]any{
		"kind": "webhook", "name": name, "config": map[string]string{"secret": "synthetic-secret-not-real"}, "enabled": false,
	}, &conn, nil)
	u.do(http.MethodPatch, "/api/v1/connectors/"+conn.ID, map[string]string{"operator_id": operator}, nil, nil)
	return conn.ID
}

// readHistory reads the messages of the rooms from the database file, oldest first.
func readHistory(t *testing.T, path string) map[string][]historyEntry {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	rows, err := raw.QueryContext(context.Background(), `SELECT room_id, id, seq, author_kind, author_id, text FROM messages ORDER BY room_id, seq`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string][]historyEntry{}
	for rows.Next() {
		var room string
		var e historyEntry
		require.NoError(t, rows.Scan(&room, &e.ID, &e.Seq, &e.AuthorKind, &e.AuthorID, &e.Text))
		out[room] = append(out[room], e)
	}
	require.NoError(t, rows.Err())
	return out
}

// OUPG-01, OUPG-02, POS-10, ORG-04 (independent test of the upgrade, on a database the real v0.5.0 binary wrote): the
// installation of a team - alice, who administers the instance and hosts two AIs, bob, who was shared one of them
// for use, a room for posting and another for viewing, and a connection, and root, a second administrator - is
// upgraded by the app of this branch, and the same checks hold as on the database built from the store's fixtures.
func TestUpgradedRealV05InstallationBecomesOrganizations(t *testing.T) {
	bin := os.Getenv(v05BinaryEnv)
	if bin == "" {
		t.Skipf("set %s to a partyline v0.5.0 binary to run it (see the comment above the test)", v05BinaryEnv)
	}
	srv := newV05Server(t, bin)
	require.Contains(t, srv.run("", "version"), "v0.5.", "the binary is a v0.5 one")

	// The installation, written by the v0.5.0 server.
	for _, u := range []struct {
		name  string
		admin bool
	}{{"alice", true}, {"bob", false}, {"root", true}} {
		srv.createUser(u.name, u.admin)
	}
	srv.start()
	alice, bob := srv.login("alice"), srv.login("bob")
	atlasPath, notesPath := "/home/alice/atlas", "/home/alice/notes"
	sessions := alice.agentToken()
	atlas, notes := sessions.registerAI(atlasPath), sessions.registerAI(notesPath)
	ops, inbox, team, mine := alice.createRoom("ops"), alice.createRoom("inbox"), alice.createRoom("team"), bob.createRoom("bob-room")
	alice.addMember(ops, "user", bob.id)
	alice.addMember(ops, "agent", atlas)
	alice.addMember(team, "agent", atlas)
	alice.addMember(team, "agent", notes)
	alice.post(ops, "deploy at five")
	bob.post(ops, "ok, I will watch it")
	alice.post(team, "welcome to the team")
	sessions.postAs(atlasPath, team, "atlas here")
	sessions.postAs(notesPath, team, "notes here")
	alice.post(inbox, "for the record")
	bob.post(mine, "just for me")
	alice.share("agents", atlas, bob.id, "use") // the former use grantee
	alice.share("agents", notes, "", "")        // everyone may use notes
	alice.share("rooms", ops, bob.id, "post")   // keeps the room as a member
	alice.share("rooms", inbox, bob.id, "view") // loses it: the read-only view is not carried over
	connection := alice.createWebhook("hook", atlas)
	alice.share("connectors", connection, bob.id, "use")
	srv.stop()

	// What the old database holds, read before the new app sees it, and a copy of it for the app.
	dbPath := filepath.Join(srv.dataDir, "partyline.db")
	require.NoFileExists(t, dbPath+"-wal", "the server shut down cleanly: nothing is left in the log")
	history := readHistory(t, dbPath)
	require.Len(t, history[team], 3, "fixture sanity")
	data, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	copied := filepath.Join(t.TempDir(), "partyline.db")
	require.NoError(t, os.WriteFile(copied, data, 0o600))

	// The app of this branch opens it.
	hook := hookKind()
	hook.Name = "webhook"
	env := testkit.New(t, testkit.WithDatabase(copied), testkit.WithConnectorKinds(hook), testkit.WithEnv(map[string]string{"PARTYLINE_SECRET_KEY": v05Key}))
	require.Equal(t, 2, countRows(t, env, `SELECT COUNT(*) FROM schema_migrations WHERE version IN ('0019_organizations', '0020_drop_grants')`), "the app applied 0019 and 0020")
	a, b, r := env.LoginAs("alice", testkit.Password), env.LoginAs("bob", testkit.Password), env.LoginAs("root", testkit.Password)
	rooms := func(reader *testkit.Client, ids ...string) []roomHistory {
		var out []roomHistory
		for _, id := range ids {
			out = append(out, roomHistory{room: id, reader: reader, entries: history[id]})
		}
		return out
	}
	folder := func(path string) testkit.Folder {
		return testkit.Folder{Machine: "devbox", Path: path, Home: "/home/alice", Harness: "claude"}
	}
	w := &upgradeWorld{
		env: env, alice: a, bob: b, root: r, password: testkit.Password,
		aliceSlug: "alice", bobSlug: "bob", bobHandle: "bob",
		atlas: upgradedAI{"atlas", folder(atlasPath)}, other: upgradedAI{"notes", folder(notesPath)},
		team:     team,
		history:  slices.Concat(rooms(a, ops, inbox, team), rooms(b, mine)),
		bobReads: []string{ops}, bobLoses: []string{inbox},
		connection: connection,
	}
	checkUpgradedInstallation(t, w)
}
