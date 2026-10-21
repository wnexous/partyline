// Package testkit boots a complete Partyline app for end-to-end tests: a temp data dir, a
// migrated database and a real HTTP server, plus clients that act as anonymous visitors,
// logged-in users or token holders.
//
//	env := testkit.New(t)
//	admin := env.SetupAdmin()
//	bob := env.NewUser("bob")
//	var me dto.Me
//	admin.Get("/api/v1/me").RequireStatus(http.StatusOK).Decode(&me)
package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/users"
)

// Password is the password of every account created by the kit.
const Password = "testkit-password-1"

// AdminUsername is the username SetupAdmin creates.
const AdminUsername = "admin"

// Env is a running app with its HTTP server.
type Env struct {
	T      testing.TB
	App    *app.App
	Server *httptest.Server
	// URL is the server base URL. It is also the public URL unless WithEnv overrides
	// PARTYLINE_PUBLIC_URL.
	URL string
	// Origin is the public URL's origin; clients send it as their Origin header.
	Origin string

	admin   *Client
	cfg     *config.Config
	appOpts app.Options
	handler atomic.Pointer[http.Handler]
}

type options struct {
	env              map[string]string
	logger           *slog.Logger
	now              func() time.Time
	liveSessionCheck time.Duration
	kinds            []connectors.Kind
	outbox           bool
	version          string
	database         string
}

// Option customizes New.
type Option func(*options)

// WithEnv sets PARTYLINE_* variables. The kit always sets the data dir and defaults the
// public URL to the server URL.
func WithEnv(vars map[string]string) Option {
	return func(o *options) {
		for k, v := range vars {
			o.env[k] = v
		}
	}
}

// WithLogger sends the app logs to logger instead of discarding them.
func WithLogger(logger *slog.Logger) Option { return func(o *options) { o.logger = logger } }

// WithLiveSessionCheck sets how often live sockets re-check their session.
func WithLiveSessionCheck(d time.Duration) Option { return func(o *options) { o.liveSessionCheck = d } }

// WithClock makes the app use now as its clock.
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// WithOutbox runs the outbox worker with the connectors' sender. Without it, outbound
// deliveries stay pending so tests can inspect them.
func WithOutbox() Option { return func(o *options) { o.outbox = true } }

// WithConnectorKinds makes the app support exactly kinds (e.g. a fake kind from
// connectors/connectortest) instead of the built-in ones.
func WithConnectorKinds(kinds ...connectors.Kind) Option {
	return func(o *options) { o.kinds = append(o.kinds, kinds...) }
}

// WithDatabase starts the app on a copy of the SQLite database at path (for example a database
// of an older schema, which the app migrates on start).
func WithDatabase(path string) Option { return func(o *options) { o.database = path } }

// WithVersion sets the server build version (default "dev").
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// New starts an app on a temp data dir behind an httptest server; both stop at test cleanup.
func New(t testing.TB, opts ...Option) *Env {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	o := options{
		env:    map[string]string{"PARTYLINE_PUBLIC_URL": base},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(&o)
	}
	o.env["PARTYLINE_DATA_DIR"] = t.TempDir()
	if o.database != "" {
		copyDatabase(t, o.database, filepath.Join(o.env["PARTYLINE_DATA_DIR"], app.DatabaseFile))
	}
	cfg, err := config.Load(func(k string) string { return o.env[k] })
	require.NoError(t, err)

	e := &Env{
		T: t, Server: srv, URL: base, Origin: cfg.PublicURL.Scheme + "://" + cfg.PublicURL.Host, cfg: cfg,
		appOpts: app.Options{Logger: o.logger, Now: o.now, LiveSessionCheck: o.liveSessionCheck, ConnectorKinds: o.kinds, DisableOutbox: !o.outbox, Version: o.version},
	}
	e.startApp()
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*e.handler.Load()).ServeHTTP(w, r) })
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		_ = e.App.Shutdown(context.Background())
	})
	return e
}

// copyDatabase copies a closed SQLite database with its WAL file, when there is one.
func copyDatabase(t testing.TB, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(from + suffix) // #nosec G304 -- test fixture path chosen by the test
		if suffix != "" && os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(to+suffix, data, 0o600)) // #nosec G703 -- the kit's own temp data dir
	}
}

func (e *Env) startApp() {
	a, err := app.New(e.cfg, e.appOpts)
	require.NoError(e.T, err)
	require.NoError(e.T, a.Start(context.Background()))
	h := a.Handler()
	e.App = a
	e.handler.Store(&h)
}

// Restart shuts the app down and starts a new one on the same data dir and config behind
// the same server, like a server restart. Sessions and tokens stay valid.
func (e *Env) Restart() {
	e.T.Helper()
	require.NoError(e.T, e.App.Shutdown(context.Background()))
	e.startApp()
}

// Anon returns a client without credentials (it keeps cookies it receives).
func (e *Env) Anon() *Client {
	jar, err := cookiejar.New(nil)
	require.NoError(e.T, err)
	return &Client{T: e.T, env: e, http: &http.Client{Jar: jar}, header: http.Header{}}
}

// SetupAdmin creates the first account ("admin", Password) with the setup token and returns
// its logged-in client. Later calls return the same client.
func (e *Env) SetupAdmin() *Client {
	e.T.Helper()
	if e.admin == nil {
		_, err := e.App.Users.Setup(context.Background(), e.App.SetupToken(),
			users.AccountInput{Username: AdminUsername, Password: Password, DisplayName: "Admin"})
		require.NoError(e.T, err)
		e.admin = e.LoginAs(AdminUsername, Password)
	}
	return e.admin
}

// NewUser creates a member account with Password and returns its logged-in client.
func (e *Env) NewUser(username string) *Client {
	e.T.Helper()
	return e.newAccount(username, dto.RoleMember)
}

// NewAdmin creates an additional admin account with Password and returns its logged-in client.
func (e *Env) NewAdmin(username string) *Client {
	e.T.Helper()
	return e.newAccount(username, dto.RoleAdmin)
}

func (e *Env) newAccount(username string, role dto.Role) *Client {
	e.T.Helper()
	_, err := e.App.Users.CreateUser(context.Background(),
		users.AccountInput{Username: username, Password: Password}, role)
	require.NoError(e.T, err)
	return e.LoginAs(username, Password)
}

// LoginAs opens a session for an existing account and returns a client carrying its cookie.
func (e *Env) LoginAs(username, password string) *Client {
	e.T.Helper()
	u, sess, err := e.App.Users.Login(context.Background(),
		users.LoginInput{Username: username, Password: password, IP: "127.0.0.1", UserAgent: "testkit"})
	require.NoError(e.T, err)
	c := e.SessionClient(sess.Secret)
	c.User = u
	return c
}

// SessionClient returns a client that sends secret as its session cookie, e.g. to replay a
// cookie after logout. Its User is not filled in.
func (e *Env) SessionClient(secret string) *Client {
	e.T.Helper()
	c := e.Anon()
	c.Session = secret
	base, err := url.Parse(e.URL)
	require.NoError(e.T, err)
	cookie := &http.Cookie{Name: api.SessionCookie, Value: secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode} // #nosec G124 -- jar entry for the plain-HTTP test server
	c.http.Jar.SetCookies(base, []*http.Cookie{cookie})
	return c
}

// Client sends requests to the Env server as one identity.
type Client struct {
	T testing.TB
	// User is the account behind the client (zero for anonymous clients).
	User db.User
	// Session is the session cookie value of a logged-in client.
	Session string

	env    *Env
	http   *http.Client
	header http.Header
	bearer string
}

// Token creates an API token of kind for the client's user and returns a client that
// authenticates with it (no cookies).
func (c *Client) Token(kind dto.TokenKind, name string) *Client {
	c.T.Helper()
	require.NotEmpty(c.T, c.User.ID, "Token needs a logged-in client")
	_, secret, err := c.env.App.Users.CreateToken(context.Background(), c.User.ID,
		users.TokenInput{Kind: kind, Name: name})
	require.NoError(c.T, err)
	return c.WithBearer(secret)
}

// WithBearer returns a cookie-less copy of the client that sends "Authorization: Bearer secret".
func (c *Client) WithBearer(secret string) *Client {
	cp := c.env.Anon()
	cp.User = c.User
	cp.bearer = secret
	cp.header = c.header.Clone()
	return cp
}

// WithHeader returns a copy of the client (sharing its cookies) that also sends header k: v.
func (c *Client) WithHeader(k, v string) *Client {
	cp := *c
	cp.header = c.header.Clone()
	cp.header.Set(k, v)
	return &cp
}

// Get sends a GET request.
func (c *Client) Get(path string) *Response { return c.Do(http.MethodGet, path, nil) }

// Post sends body as JSON.
func (c *Client) Post(path string, body any) *Response { return c.Do(http.MethodPost, path, body) }

// Put sends body as JSON.
func (c *Client) Put(path string, body any) *Response { return c.Do(http.MethodPut, path, body) }

// Patch sends body as JSON.
func (c *Client) Patch(path string, body any) *Response { return c.Do(http.MethodPatch, path, body) }

// Delete sends a DELETE request.
func (c *Client) Delete(path string) *Response { return c.Do(http.MethodDelete, path, nil) }

// Do sends a request with body encoded as JSON (nil for none). Requests carry the instance's
// own Origin (Env.Origin), as a browser would, so cookie writes pass the CSRF check; override
// it with WithHeader("Origin", ...).
func (c *Client) Do(method, path string, body any) *Response {
	c.T.Helper()
	var rd io.Reader
	contentType := ""
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(c.T, err)
		rd = bytes.NewReader(data)
		contentType = "application/json"
	}
	return c.DoRaw(method, path, contentType, rd)
}

// DoRaw sends a request with a raw body and content type (e.g. multipart).
func (c *Client) DoRaw(method, path, contentType string, body io.Reader) *Response {
	c.T.Helper()
	req, err := http.NewRequest(method, c.env.URL+path, body)
	require.NoError(c.T, err)
	req.Header.Set("Origin", c.env.Origin)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, vs := range c.header {
		req.Header[k] = vs
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	require.NoError(c.T, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(c.T, err)
	return &Response{T: c.T, Status: resp.StatusCode, Header: resp.Header, Body: data, Cookies: resp.Cookies()}
}

// Response is a fully read HTTP response.
type Response struct {
	T       testing.TB
	Status  int
	Header  http.Header
	Body    []byte
	Cookies []*http.Cookie
}

// RequireStatus fails the test unless the response has the given status.
func (r *Response) RequireStatus(status int) *Response {
	r.T.Helper()
	require.Equal(r.T, status, r.Status, "body: %s", r.Body)
	return r
}

// RequireError fails the test unless the response is an error envelope with status and code.
func (r *Response) RequireError(status int, code dto.ErrorCode) dto.Error {
	r.T.Helper()
	r.RequireStatus(status)
	var body dto.ErrorBody
	r.Decode(&body)
	require.Equal(r.T, code, body.Error.Code, "body: %s", r.Body)
	return body.Error
}

// Decode unmarshals the JSON body into v.
func (r *Response) Decode(v any) {
	r.T.Helper()
	require.NoError(r.T, json.Unmarshal(r.Body, v), "body: %s", r.Body)
}

// Cookie returns the Set-Cookie with the given name, or nil.
func (r *Response) Cookie(name string) *http.Cookie {
	for _, c := range r.Cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Decode unmarshals the JSON body of r into a new T.
func Decode[T any](r *Response) T {
	r.T.Helper()
	var v T
	r.Decode(&v)
	return v
}

// String returns the body as text, trimmed.
func (r *Response) String() string { return strings.TrimSpace(string(r.Body)) }

// Clock is a manually advanced clock for WithClock. It is safe for concurrent use.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock stopped at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now returns the clock's current time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
