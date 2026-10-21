package api_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// deviceEnv is a server on a fake clock that trusts 127.0.0.1 as a proxy, so tests choose the
// client address with X-Forwarded-For.
type deviceEnv struct {
	env   *testkit.Env
	clock *testkit.Clock
	alice *testkit.Client
}

var deviceT0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func newDeviceEnv(t *testing.T) *deviceEnv {
	t.Helper()
	clock := testkit.NewClock(deviceT0)
	env := testkit.New(t, testkit.WithClock(clock.Now), testkit.WithEnv(map[string]string{"PARTYLINE_TRUSTED_PROXIES": "127.0.0.1"}))
	return &deviceEnv{env: env, clock: clock, alice: env.NewUser("alice")}
}

// cli is an anonymous client from the given address, like `partyline setup --browser`.
func (e *deviceEnv) cli(ip string) *testkit.Client {
	return e.env.Anon().WithHeader("X-Forwarded-For", ip)
}

func (e *deviceEnv) create(t *testing.T, machine string) dto.DeviceRequestCreated {
	t.Helper()
	return testkit.Decode[dto.DeviceRequestCreated](e.cli("203.0.113.10").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: machine}).RequireStatus(http.StatusCreated))
}

func (e *deviceEnv) poll(secret string) *testkit.Response {
	return e.cli("203.0.113.10").Post("/api/v1/devices/poll", dto.DevicePollRequest{PollSecret: secret})
}

// decide posts the typed code to /devices/<action> (lookup, approve or deny) as c.
func decide(c *testkit.Client, action, code string) *testkit.Response {
	return c.Post("/api/v1/devices/"+action, dto.DeviceCode{Code: code})
}

// DEV-01: a request has an 8-character code valid for 10 minutes and the approval URL
// <instance>/devices, which does not carry the code (the user types it).
func TestDeviceCreate(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	require.Regexp(t, regexp.MustCompile(`^[A-HJ-NP-Z2-9]{8}$`), c.Code)
	require.Equal(t, e.env.URL+"/devices", c.ApproveURL)
	require.True(t, deviceT0.Add(10*time.Minute).Equal(c.ExpiresAt), "expires_at %s", c.ExpiresAt)
	require.NotEmpty(t, c.PollSecret)

	d := testkit.Decode[dto.DeviceRequest](decide(e.alice, "lookup", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceRequest{
		Code: c.Code, Machine: "laptop-01", IP: "203.0.113.10", Status: dto.DeviceStatusPending,
		CreatedAt: deviceT0, ExpiresAt: deviceT0.Add(10 * time.Minute),
	}, d)

	p := testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DevicePoll{Status: dto.DeviceStatusPending}, p)
}

// DEV-01: the machine name is required and has at most 64 characters.
func TestDeviceCreateValidatesMachine(t *testing.T) {
	e := newDeviceEnv(t)
	for _, m := range []string{"", "   ", strings.Repeat("m", 65), "bad\nname"} {
		er := e.cli("203.0.113.10").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: m}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"machine": "must have 1 to 64 characters"}, er.Fields, "machine %q", m)
	}
	e.cli("203.0.113.10").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: strings.Repeat("m", 64)}).RequireStatus(http.StatusCreated)
}

// DEV-02: approving creates an agent token named after the machine for the approver; the
// polling CLI receives it with the instance URL once, then never again.
func TestDeviceApproveDeliversTokenOnce(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	d := testkit.Decode[dto.DeviceRequest](decide(e.alice, "approve", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusApproved, d.Status)

	p := testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusApproved, p.Status)
	require.True(t, strings.HasPrefix(p.Token, "pl_agent_"), "token %q", p.Token)
	require.Equal(t, e.env.URL, p.URL)
	require.Equal(t, "alice", p.ApprovedBy, "DEV-06: the CLI names who approved")

	tokens := testkit.Decode[dto.List[dto.Token]](e.alice.Get("/api/v1/tokens").RequireStatus(http.StatusOK))
	require.Len(t, tokens.Items, 1)
	require.Equal(t, "laptop-01 (via setup)", tokens.Items[0].Name)
	require.Equal(t, dto.TokenKindAgent, tokens.Items[0].Kind)
	conn, resp := e.env.Anon().WithBearer(p.Token).DialAgent("setup-check")
	require.NotNil(t, conn, "the delivered token is an agent token: %d %s", resp.Status, resp.Body)
	require.Equal(t, "alice", conn.Welcome(wsWait).Agent.Owner)

	e.clock.Advance(3 * time.Second)
	again := testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DevicePoll{Status: dto.DeviceStatusApproved}, again, "the token is delivered only once")
}

// DEV-02: the token belongs to whoever approves (a second user here), not to anyone else.
func TestDeviceTokenBelongsToApprover(t *testing.T) {
	e := newDeviceEnv(t)
	bob := e.env.NewUser("bob")
	c := e.create(t, "desktop")
	decide(bob, "approve", c.Code).RequireStatus(http.StatusOK)
	p := testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, "bob", p.ApprovedBy)
	conn, _ := e.env.Anon().WithBearer(p.Token).DialAgent("setup-check")
	require.NotNil(t, conn)
	require.Equal(t, "bob", conn.Welcome(wsWait).Agent.Owner)
	require.Empty(t, testkit.Decode[dto.List[dto.Token]](e.alice.Get("/api/v1/tokens?include_revoked=true").RequireStatus(http.StatusOK)).Items)
}

// DEV-03: a denied request polls as denied and can no longer be approved.
func TestDeviceDeny(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	d := testkit.Decode[dto.DeviceRequest](decide(e.alice, "deny", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusDenied, d.Status)

	p := testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DevicePoll{Status: dto.DeviceStatusDenied}, p)
	decide(e.alice, "approve", c.Code).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.Empty(t, testkit.Decode[dto.List[dto.Token]](e.alice.Get("/api/v1/tokens?include_revoked=true").RequireStatus(http.StatusOK)).Items)

	// An approved request cannot be denied afterwards either.
	c2 := e.create(t, "laptop-02")
	decide(e.alice, "approve", c2.Code).RequireStatus(http.StatusOK)
	decide(e.alice, "deny", c2.Code).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// DEV-01, DEV-03: a request can be approved strictly within 10 minutes; afterwards it reads
// and polls as expired and approving answers 410.
func TestDeviceExpiry(t *testing.T) {
	e := newDeviceEnv(t)
	inside := e.create(t, "inside")
	outside := e.create(t, "outside")

	e.clock.Advance(10*time.Minute - time.Second)
	decide(e.alice, "approve", inside.Code).RequireStatus(http.StatusOK)

	e.clock.Advance(2 * time.Second) // 10 min + 1 s
	decide(e.alice, "approve", outside.Code).RequireError(http.StatusGone, dto.ErrorCodeGone)
	decide(e.alice, "deny", outside.Code).RequireError(http.StatusGone, dto.ErrorCodeGone)
	d := testkit.Decode[dto.DeviceRequest](decide(e.alice, "lookup", outside.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusExpired, d.Status)
	p := testkit.Decode[dto.DevicePoll](e.poll(outside.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DevicePoll{Status: dto.DeviceStatusExpired}, p)

	// The request approved in time still delivers its token.
	got := testkit.Decode[dto.DevicePoll](e.poll(inside.PollSecret).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusApproved, got.Status)
	require.NotEmpty(t, got.Token)
}

// DEV-04: at most 10 requests per hour per address: the 11th answers 429; another address and
// the same address an hour later are accepted.
func TestDeviceCreateRateLimit(t *testing.T) {
	e := newDeviceEnv(t)
	for range 10 {
		e.cli("203.0.113.20").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireStatus(http.StatusCreated)
	}
	e.cli("203.0.113.20").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.cli("203.0.113.21").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireStatus(http.StatusCreated)

	e.clock.Advance(59 * time.Minute)
	e.cli("203.0.113.20").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.clock.Advance(2 * time.Minute)
	e.cli("203.0.113.20").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireStatus(http.StatusCreated)
}

// DEV-04: addresses of one IPv6 /64 share the limit.
func TestDeviceCreateRateLimitGroupsIPv6Prefix(t *testing.T) {
	e := newDeviceEnv(t)
	for i := range 10 {
		e.cli(fmt.Sprintf("2001:db8:1:2::%x", i+1)).Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireStatus(http.StatusCreated)
	}
	e.cli("2001:db8:1:2:ffff::1").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.cli("2001:db8:1:3::1").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireStatus(http.StatusCreated)
}

// DEV-04: the limit holds under a concurrent burst: exactly 10 of 16 parallel requests pass.
func TestDeviceCreateRateLimitConcurrentBurst(t *testing.T) {
	e := newDeviceEnv(t)
	var (
		mu       sync.Mutex
		statuses = map[int]int{}
		wg       sync.WaitGroup
	)
	for range 16 {
		wg.Go(func() {
			resp := e.cli("203.0.113.30").Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"})
			mu.Lock()
			statuses[resp.Status]++
			mu.Unlock()
		})
	}
	wg.Wait()
	require.Equal(t, map[int]int{http.StatusCreated: 10, http.StatusTooManyRequests: 6}, statuses)
}

// DEV-04: one poll per request every 2 seconds: a poll 1.9 s after the last one answers 429,
// one 2 s after it is answered; another request is not affected.
func TestDevicePollRateLimit(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	other := e.create(t, "laptop-02")

	e.poll(c.PollSecret).RequireStatus(http.StatusOK)
	e.poll(c.PollSecret).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.poll(other.PollSecret).RequireStatus(http.StatusOK)

	e.clock.Advance(1900 * time.Millisecond)
	e.poll(c.PollSecret).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.clock.Advance(100 * time.Millisecond)
	e.poll(c.PollSecret).RequireStatus(http.StatusOK)
}

// A refused (too fast) poll does not consume the token: the next allowed poll delivers it.
func TestDeviceTooFastPollKeepsToken(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	e.poll(c.PollSecret).RequireStatus(http.StatusOK)
	decide(e.alice, "approve", c.Code).RequireStatus(http.StatusOK)
	e.poll(c.PollSecret).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.clock.Advance(2 * time.Second)
	p := testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK))
	require.NotEmpty(t, p.Token)
}

// Unknown codes and poll secrets answer 404.
func TestDeviceUnknown(t *testing.T) {
	e := newDeviceEnv(t)
	for _, action := range []string{"lookup", "approve", "deny"} {
		decide(e.alice, action, "ABCDEFGH").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	e.poll("not-a-secret").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// WIZ-02, DEV-01: the code never travels in a URL: the routes that took it in the path are
// gone, so a forwarded link cannot read or approve a request.
func TestDeviceCodeNotInPath(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	require.NotEqual(t, http.StatusOK, e.alice.Get("/api/v1/devices/"+c.Code).Status)
	for _, action := range []string{"/approve", "/deny"} {
		require.NotEqual(t, http.StatusOK, e.alice.Post("/api/v1/devices/"+c.Code+action, nil).Status)
	}
	d := testkit.Decode[dto.DeviceRequest](decide(e.alice, "lookup", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusPending, d.Status)
	require.Empty(t, testkit.Decode[dto.List[dto.Token]](e.alice.Get("/api/v1/tokens?include_revoked=true").RequireStatus(http.StatusOK)).Items)
}

// DEV-05: a user may type 5 unknown codes per minute; the 6th attempt answers 429 even with a
// valid code. Known codes do not count, other users are not affected, and the attempts are
// counted again once a minute has passed since them.
func TestDeviceWrongCodeLimit(t *testing.T) {
	e := newDeviceEnv(t)
	bob := e.env.NewUser("bob")
	c := e.create(t, "laptop-01")
	for range 3 {
		decide(e.alice, "lookup", c.Code).RequireStatus(http.StatusOK)
	}
	for i, action := range []string{"lookup", "approve", "deny", "lookup", "approve"} {
		decide(e.alice, action, fmt.Sprintf("WRONG%03d", i)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	for _, action := range []string{"lookup", "approve", "deny"} {
		decide(e.alice, action, c.Code).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	}
	decide(bob, "lookup", c.Code).RequireStatus(http.StatusOK)

	e.clock.Advance(59 * time.Second)
	decide(e.alice, "lookup", c.Code).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	e.clock.Advance(2 * time.Second)
	d := testkit.Decode[dto.DeviceRequest](decide(e.alice, "approve", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusApproved, d.Status)
}

// DEV-05: the limit holds under a concurrent burst of wrong codes: exactly 5 of 12 are looked
// up, the others answer 429.
func TestDeviceWrongCodeLimitConcurrentBurst(t *testing.T) {
	e := newDeviceEnv(t)
	var (
		mu       sync.Mutex
		statuses = map[int]int{}
		wg       sync.WaitGroup
	)
	for i := range 12 {
		wg.Go(func() {
			resp := decide(e.alice, "lookup", fmt.Sprintf("BURST%03d", i))
			mu.Lock()
			statuses[resp.Status]++
			mu.Unlock()
		})
	}
	wg.Wait()
	require.Equal(t, map[int]int{http.StatusNotFound: 5, http.StatusTooManyRequests: 7}, statuses)
}

// Auth matrix: reading, approving and denying need a logged-in user (401 anonymous, 403 for
// agent tokens); creating and polling are anonymous but refuse agent tokens (403).
func TestDeviceAuthMatrix(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	anon := e.env.Anon()
	agent := e.alice.Token(dto.TokenKindAgent, "laptop")
	for _, action := range []string{"lookup", "approve", "deny"} {
		decide(anon, action, c.Code).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		decide(agent, action, c.Code).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	}
	agent.Post("/api/v1/devices", dto.CreateDeviceRequest{Machine: "m"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	agent.Post("/api/v1/devices/poll", dto.DevicePollRequest{PollSecret: c.PollSecret}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// A user token may approve (scripts), and nothing above changed the request.
	d := testkit.Decode[dto.DeviceRequest](decide(e.alice.Token(dto.TokenKindUser, "script"), "lookup", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusPending, d.Status)
}

// SEC-02 on the device routes: a logged-in browser session cannot be made to look up,
// approve or deny a code from another site (foreign Origin or Sec-Fetch-Site) or without
// proving its origin, and none of those attempts changes the request; a same-origin request
// of the same session goes through.
func TestDeviceDecisionsRefuseCrossOrigin(t *testing.T) {
	e := newDeviceEnv(t)
	c := e.create(t, "laptop-01")
	foreign := map[string]*testkit.Client{
		"foreign origin":             e.alice.WithHeader("Origin", "https://evil.example.test"),
		"cross-site fetch":           e.alice.WithHeader("Sec-Fetch-Site", "cross-site"),
		"same-site, not same-origin": e.alice.WithHeader("Sec-Fetch-Site", "same-site"),
		"no origin proof":            e.alice.WithHeader("Origin", ""),
	}
	for name, client := range foreign {
		for _, action := range []string{"lookup", "approve", "deny"} {
			err := decide(client, action, c.Code).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			require.Equal(t, "cross-origin request refused", err.Message, "%s %s", name, action)
		}
	}
	require.Equal(t, dto.DevicePoll{Status: dto.DeviceStatusPending},
		testkit.Decode[dto.DevicePoll](e.poll(c.PollSecret).RequireStatus(http.StatusOK)), "nothing was approved or denied")

	d := testkit.Decode[dto.DeviceRequest](decide(e.alice.WithHeader("Sec-Fetch-Site", "same-origin"), "approve", c.Code).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeviceStatusApproved, d.Status)
}

// Audit: creation, approval, denial and delivery are recorded without secrets.
func TestDeviceAudit(t *testing.T) {
	e := newDeviceEnv(t)
	admin := e.env.NewAdmin("root")
	approved := e.create(t, "laptop-01")
	denied := e.create(t, "laptop-02")
	decide(e.alice, "approve", approved.Code).RequireStatus(http.StatusOK)
	decide(e.alice, "deny", denied.Code).RequireStatus(http.StatusOK)
	p := testkit.Decode[dto.DevicePoll](e.poll(approved.PollSecret).RequireStatus(http.StatusOK))

	page := auditPage(admin, "")
	create := findAudit(t, page.Items, "device.create")
	require.Equal(t, "203.0.113.10", create.IP)
	require.Equal(t, "laptop-02", create.Detail["machine"])
	approve := findAudit(t, page.Items, "device.approve")
	require.Equal(t, "alice", approve.Actor.Username)
	require.Equal(t, "laptop-01", approve.Detail["machine"])
	deny := findAudit(t, page.Items, "device.deny")
	require.Equal(t, "alice", deny.Actor.Username)
	deliver := findAudit(t, page.Items, "device.deliver")
	require.Equal(t, approve.TargetID, deliver.TargetID)
	require.Equal(t, "device", deliver.TargetKind)

	raw := string(admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK).Body)
	for _, secret := range []string{p.Token, approved.PollSecret, approved.Code, denied.Code} {
		require.NotContains(t, raw, secret)
	}
}
