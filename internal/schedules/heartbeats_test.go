package schedules_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/schedules"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

const wait = 5 * time.Second

// clock is the fake clock of the app: the tests move it and then drive the scheduler.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *clock) Advance(d time.Duration) { c.Set(c.Now().Add(d)) }

// saoPaulo is where the clock starts: 08:00 in America/Sao_Paulo (11:00 UTC) on 2026-11-10.
var saoPaulo = func() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic(err)
	}
	return loc
}()

func local(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour, minute, 0, 0, saoPaulo)
}

// hb is an app with an organization (testkit.OrgRoles "hb") whose AI hb-ai was created by Manager and runs on
// Manager's machine: Manager manages it (its creator) and so does Admin (above Manager's position); Member sees
// it and does not; Outsider does not see it.
type hb struct {
	t     *testing.T
	env   *testkit.Env
	clk   *clock
	roles *testkit.OrgRoles
	ai    string
	// token is the agent token of the AI's machine; agent is the agent API as the AI.
	token, agent *testkit.Client
}

func newHB(t *testing.T) *hb {
	t.Helper()
	clk := &clock{now: local(10, 8, 0)}
	env := testkit.New(t, testkit.WithClock(clk.Now))
	roles := env.OrgRoles("hb")
	token := roles.Manager.Token(dto.TokenKindAgent, "laptop")
	return &hb{t: t, env: env, clk: clk, roles: roles, ai: roles.TargetAI, token: token, agent: token.AsAgent("hb-ai")}
}

func (h *hb) path(suffix string) string { return "/api/v1/agents/" + h.ai + "/schedules" + suffix }

// create adds a schedule as c (a user through the managers' route, or the AI through its own).
func (h *hb) create(c *testkit.Client, req dto.ScheduleRequest) dto.Schedule {
	h.t.Helper()
	return testkit.Decode[dto.Schedule](h.post(c, req).RequireStatus(http.StatusCreated))
}

func (h *hb) post(c *testkit.Client, req dto.ScheduleRequest) *testkit.Response {
	h.t.Helper()
	if c == h.agent {
		return c.Post("/api/v1/agent/schedules", req)
	}
	return c.Post(h.path(""), req)
}

func (h *hb) patch(c *testkit.Client, id string, req dto.ScheduleRequest) *testkit.Response {
	h.t.Helper()
	if c == h.agent {
		return c.Patch("/api/v1/agent/schedules/"+id, req)
	}
	return c.Patch(h.path("/"+id), req)
}

func (h *hb) remove(c *testkit.Client, id string) *testkit.Response {
	h.t.Helper()
	if c == h.agent {
		return c.Do(http.MethodDelete, "/api/v1/agent/schedules/"+id, nil)
	}
	return c.Do(http.MethodDelete, h.path("/"+id), nil)
}

func (h *hb) tick() {
	h.t.Helper()
	_, err := h.env.App.Schedules.Tick(context.Background())
	require.NoError(h.t, err)
}

// runs lists the schedule's runs, newest first, as Manager.
func (h *hb) runs(id string) []dto.ScheduleRun {
	h.t.Helper()
	return testkit.Decode[dto.List[dto.ScheduleRun]](h.roles.Manager.Get(h.path("/" + id + "/runs")).RequireStatus(http.StatusOK)).Items
}

func (h *hb) statuses(id string) []dto.ScheduleRunStatus {
	h.t.Helper()
	var out []dto.ScheduleRunStatus
	for _, r := range h.runs(id) {
		out = append(out, r.Status)
	}
	return out
}

// eventuallyStatuses waits until the schedule's runs, newest first, have these statuses.
func (h *hb) eventuallyStatuses(id string, want ...dto.ScheduleRunStatus) {
	h.t.Helper()
	require.EventuallyWithT(h.t, func(c *assert.CollectT) {
		assert.Equal(c, want, h.statuses(id))
	}, wait, 10*time.Millisecond)
}

// folder is the AI's own folder with a current client.
func (h *hb) folder() testkit.Folder {
	f := testkit.FolderOf("hb-ai")
	f.ClientVersion = "dev"
	return f
}

// dial opens a session of the AI and reads its welcome; the connection is closed at the end of the test.
func (h *hb) dial(f testkit.Folder) (*testkit.AgentConn, dto.AgentWelcomeFrame) {
	h.t.Helper()
	conn, resp := h.token.DialFolder(f)
	require.NotNil(h.t, conn, "upgrade refused: %s", resp.String())
	h.t.Cleanup(conn.Close)
	return conn, conn.Welcome(wait)
}

// principal opens the principal session with a current client.
func (h *hb) principal() *testkit.AgentConn {
	h.t.Helper()
	conn, w := h.dial(h.folder())
	require.Equal(h.t, "", w.Session.Label, "the principal")
	return conn
}

// heartbeat reads frames until a heartbeat arrives.
func heartbeat(t *testing.T, conn *testkit.AgentConn) dto.AgentHeartbeatFrame {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		f := conn.Next(time.Until(deadline))
		if f.Type != dto.AgentFrameTypeHeartbeat {
			continue
		}
		var hbf dto.AgentHeartbeatFrame
		require.NoError(t, json.Unmarshal(f.Raw, &hbf))
		return hbf
	}
	require.FailNow(t, "no heartbeat frame")
	return dto.AgentHeartbeatFrame{}
}

// expectNoHeartbeat fails when a heartbeat arrives within d.
func expectNoHeartbeat(t *testing.T, conn *testkit.AgentConn, d time.Duration) {
	t.Helper()
	for _, f := range conn.Drain(d) {
		require.NotEqual(t, dto.AgentFrameTypeHeartbeat, f.Type, "unexpected heartbeat: %s", f.Raw)
	}
}

func ackRun(t *testing.T, conn *testkit.AgentConn, runID string) {
	t.Helper()
	data, err := json.Marshal(dto.AgentAckFrame{Type: dto.AgentFrameTypeAck, RunID: runID})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	require.NoError(t, conn.Conn.Write(ctx, websocket.MessageText, data))
}

func ptr[T any](v T) *T { return &v }

func hourly(name string) dto.ScheduleRequest {
	return dto.ScheduleRequest{Name: ptr(name), Prompt: ptr("check the deploys"), EveryMinutes: ptr(60)}
}

func daily(name string) dto.ScheduleRequest {
	return dto.ScheduleRequest{Name: ptr(name), Prompt: ptr("send the daily summary"), Cron: ptr("0 9 * * *")}
}

// --- HB-01, HB-02, HB-12: who may read and write ---

func TestSchedulesFollowTheOrgChart(t *testing.T) {
	h := newHB(t)
	own := h.create(h.agent, hourly("own"))
	require.Equal(t, dto.ScheduleOriginSelf, own.Origin)
	require.Equal(t, h.ai, own.SetBy.ID)
	require.True(t, own.CanEdit)
	require.Equal(t, schedules.DefaultTimezone, own.Timezone)
	require.Equal(t, dto.ScheduleCatchUpOnce, own.CatchUp)
	require.True(t, own.Enabled)
	require.NotNil(t, own.NextRunAt)
	require.Equal(t, local(10, 9, 0), own.NextRunAt.In(saoPaulo), "anchored at creation")
	_, offset := own.NextRunAt.Zone()
	require.Equal(t, -3*3600, offset, "next_run_at is in the schedule's timezone")

	set := h.create(h.roles.Manager, daily("set"))
	require.Equal(t, dto.ScheduleOriginManager, set.Origin)
	require.Equal(t, h.roles.Manager.User.ID, set.SetBy.ID)

	// The AI reads both and changes only its own (HB-02).
	list := testkit.Decode[dto.ScheduleList](h.agent.Get("/api/v1/agent/schedules").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 2)
	for _, s := range list.Items {
		require.Equal(t, s.Origin == dto.ScheduleOriginSelf, s.CanEdit, s.Name)
		require.NotEmpty(t, s.Prompt)
	}
	h.patch(h.agent, set.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	h.remove(h.agent, set.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	h.patch(h.agent, own.ID, dto.ScheduleRequest{Prompt: ptr("check the deploys twice")}).RequireStatus(http.StatusOK)

	// Whoever manages the AI changes any of them; a prompt they rewrite becomes theirs.
	taken := testkit.Decode[dto.Schedule](h.patch(h.roles.Admin, own.ID, dto.ScheduleRequest{Prompt: ptr("check the deploys and the queue")}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ScheduleOriginManager, taken.Origin)
	require.Equal(t, h.roles.Admin.User.ID, taken.SetBy.ID)
	h.patch(h.agent, own.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	admin := testkit.Decode[dto.ScheduleList](h.roles.Admin.Get(h.path("")).RequireStatus(http.StatusOK))
	require.Len(t, admin.Items, 2)
	require.True(t, admin.CanManage)

	// A member who sees the AI learns that it has routines, not what they say (HB-12), and changes none.
	seen := testkit.Decode[dto.ScheduleList](h.roles.Member.Get(h.path("")).RequireStatus(http.StatusOK))
	require.Empty(t, seen.Items)
	require.Equal(t, 2, seen.Count)
	require.False(t, seen.CanManage)
	require.NotContains(t, h.roles.Member.Get(h.path("")).String(), "deploys")
	h.roles.Member.Get(h.path("/"+set.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	h.roles.Member.Get(h.path("/"+set.ID+"/runs")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	h.post(h.roles.Member, hourly("nope")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	h.patch(h.roles.Member, set.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	h.roles.Member.Post(h.path("/"+set.ID+"/run"), nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// Whoever does not see the AI does not learn it exists.
	h.roles.Outsider.Get(h.path("")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	h.post(h.roles.Outsider, hourly("nope")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	h.remove(h.roles.Outsider, set.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	h.remove(h.roles.Manager, set.ID).RequireStatus(http.StatusNoContent)
	h.remove(h.roles.Manager, set.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// HB-01: a schedule's fields are checked, and an AI's agent API sees only its own AI.
func TestScheduleFieldsAreChecked(t *testing.T) {
	h := newHB(t)
	for field, req := range map[string]dto.ScheduleRequest{
		"name":          {Name: ptr(""), Prompt: ptr("x"), EveryMinutes: ptr(60)},
		"prompt":        {Name: ptr("n"), Prompt: ptr(strings.Repeat("x", dto.MaxSchedulePrompt+1)), EveryMinutes: ptr(60)},
		"every_minutes": {Name: ptr("n"), Prompt: ptr("x")},
		"cron":          {Name: ptr("n"), Prompt: ptr("x"), Cron: ptr("0 25 * * *")},
		"tz":            {Name: ptr("n"), Prompt: ptr("x"), EveryMinutes: ptr(60), Timezone: ptr("Mars/Olympus")},
		"catch_up":      {Name: ptr("n"), Prompt: ptr("x"), EveryMinutes: ptr(60), CatchUp: ptr("twice")},
	} {
		e := h.post(h.roles.Manager, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, field, field)
	}
	both := dto.ScheduleRequest{Name: ptr("n"), Prompt: ptr("x"), EveryMinutes: ptr(60), Cron: ptr("0 9 * * *")}
	require.Contains(t, h.post(h.roles.Manager, both).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid).Fields, "cron")
	ny := h.create(h.roles.Manager, dto.ScheduleRequest{Name: ptr("ny"), Prompt: ptr("x"), Cron: ptr("0 9 * * *"), Timezone: ptr("America/New_York")})
	require.Equal(t, "America/New_York", ny.Timezone)
}

// --- HB-03, HB-10: limits ---

func TestTooFrequentAndTooManySchedulesAreRefused(t *testing.T) {
	h := newHB(t)
	e := h.post(h.roles.Manager, dto.ScheduleRequest{Name: ptr("fast"), Prompt: ptr("x"), EveryMinutes: ptr(14)}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeTooFrequent)
	require.Contains(t, e.Message, "15 minutes")
	h.post(h.roles.Manager, dto.ScheduleRequest{Name: ptr("fast"), Prompt: ptr("x"), Cron: ptr("*/10 * * * *")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeTooFrequent)

	// Four hourly schedules would fire 96 times a day: the third is refused, naming the limit.
	h.create(h.roles.Manager, hourly("one"))
	h.create(h.roles.Manager, hourly("two"))
	e = h.post(h.roles.Manager, hourly("three")).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeTooFrequent)
	require.Contains(t, e.Message, "48 times in 24 hours")
	// A disabled one does not count, and enabling it is checked.
	off := hourly("three")
	off.Enabled = ptr(false)
	three := h.create(h.roles.Manager, off)
	h.patch(h.roles.Manager, three.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeTooFrequent)

	// At most 20 schedules per AI.
	for i := 4; i <= 20; i++ {
		req := daily(fmt.Sprintf("daily %d", i))
		req.Enabled = ptr(false)
		h.create(h.roles.Manager, req)
	}
	h.post(h.roles.Manager, daily("21")).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeTooMany)
}

func TestAnAIMakesAtMostTwentyScheduleWritesAnHour(t *testing.T) {
	h := newHB(t)
	s := h.create(h.agent, daily("mine"))
	for i := 1; i < schedules.WriteLimit; i++ {
		h.patch(h.agent, s.ID, dto.ScheduleRequest{Prompt: ptr(fmt.Sprintf("summary %d", i))}).RequireStatus(http.StatusOK)
	}
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Prompt: ptr("one more")}).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	h.post(h.agent, daily("other")).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	// Managers are not limited.
	h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Prompt: ptr("from the manager")}).RequireStatus(http.StatusOK)
	h.clk.Advance(time.Hour)
	h.create(h.agent, daily("later"))
}

// --- HB-04, HB-05, HB-08: delivery ---

func TestADueRunReachesThePrincipalOnlyAndIsDeliveredOnAck(t *testing.T) {
	h := newHB(t)
	principal := h.principal()
	wt := h.folder()
	wt.Path, wt.MainFolder = "/work/hb-ai/wt", "/work/hb-ai"
	parallel, w := h.dial(wt)
	require.NotEqual(t, "", w.Session.Label, "a parallel session")

	s := h.create(h.roles.Manager, hourly("deploys"))
	h.clk.Advance(time.Hour)
	h.tick()

	f := heartbeat(t, principal)
	require.Equal(t, s.ID, f.ScheduleID)
	require.Equal(t, "deploys", f.Name)
	require.Equal(t, "check the deploys", f.Prompt)
	require.Equal(t, dto.ScheduleOriginManager, f.Origin)
	require.Equal(t, h.roles.Manager.User.Username, f.SetBy.Handle)
	require.True(t, f.Trusted, "Manager manages the AI in its home organization")
	require.Equal(t, local(10, 9, 0).UTC(), f.DueAt)
	require.Equal(t, []dto.ScheduleRunStatus{dto.ScheduleRunPending}, h.statuses(s.ID))

	ackRun(t, principal, f.RunID)
	h.eventuallyStatuses(s.ID, dto.ScheduleRunDelivered)
	runs := h.runs(s.ID)
	require.NotNil(t, runs[0].FiredAt)
	require.NotNil(t, runs[0].AckedAt)
	expectNoHeartbeat(t, parallel, 200*time.Millisecond)

	got := testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + s.ID)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ScheduleRunDelivered, got.LastStatus)
	require.Equal(t, local(10, 10, 0), got.NextRunAt.In(saoPaulo))
}

// HB-05: while a run is unacknowledged the next is not pushed; a newer one replaces it, and the newest goes once
// the one in flight is acknowledged.
func TestAnUnacknowledgedRunHoldsTheNextAndRunsDoNotPileUp(t *testing.T) {
	h := newHB(t)
	principal := h.principal()
	s := h.create(h.roles.Manager, hourly("deploys"))

	h.clk.Advance(time.Hour)
	h.tick()
	first := heartbeat(t, principal) // not acknowledged yet
	h.clk.Advance(time.Hour)
	h.tick()
	expectNoHeartbeat(t, principal, 300*time.Millisecond)
	h.clk.Advance(time.Hour)
	h.tick()
	require.Equal(t, []dto.ScheduleRunStatus{dto.ScheduleRunPending, dto.ScheduleRunSuperseded, dto.ScheduleRunPending}, h.statuses(s.ID))
	expectNoHeartbeat(t, principal, 300*time.Millisecond)

	ackRun(t, principal, first.RunID)
	third := heartbeat(t, principal)
	require.NotEqual(t, first.RunID, third.RunID)
	require.Equal(t, local(10, 11, 0).UTC(), third.DueAt, "the newest run")
	ackRun(t, principal, third.RunID)
	h.eventuallyStatuses(s.ID, dto.ScheduleRunDelivered, dto.ScheduleRunSuperseded, dto.ScheduleRunDelivered)
}

// HB-08: trusted is decided at delivery: a setter who no longer manages the AI is not trusted; a routine the AI
// set itself never is.
func TestTrustIsDecidedAtDelivery(t *testing.T) {
	h := newHB(t)
	set := h.create(h.roles.Admin, daily("from admin"))
	own := h.create(h.agent, daily("own"))
	// Admin leaves the root position: Admin no longer manages the AI.
	require.NoError(t, h.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.UpdateOrgMemberPosition(context.Background(), db.UpdateOrgMemberPositionParams{
			PositionID: sql.NullString{}, OrgID: h.roles.Org.ID, Iid: h.roles.Admin.User.ID,
		})
	}))
	principal := h.principal()
	h.clk.Set(local(10, 9, 1))
	h.tick()
	byName := map[string]dto.AgentHeartbeatFrame{}
	for range 2 {
		f := heartbeat(t, principal)
		byName[f.Name] = f
	}
	require.Equal(t, set.ID, byName["from admin"].ScheduleID)
	require.Equal(t, dto.ScheduleOriginManager, byName["from admin"].Origin)
	require.False(t, byName["from admin"].Trusted, "the setter no longer manages the AI")
	require.Equal(t, own.ID, byName["own"].ScheduleID)
	require.Equal(t, dto.ScheduleOriginSelf, byName["own"].Origin)
	require.False(t, byName["own"].Trusted)
	require.Empty(t, byName["own"].SetBy.Handle)
}

// --- HB-06: missed runs ---

// A run due while the AI is offline is delivered once when its principal connects within the window.
func TestARunMissedWhileOfflineIsDeliveredOnceOnConnect(t *testing.T) {
	h := newHB(t)
	s := h.create(h.roles.Manager, daily("summary"))
	h.clk.Set(local(10, 9, 5))
	h.tick()
	require.Equal(t, []dto.ScheduleRunStatus{dto.ScheduleRunPending}, h.statuses(s.ID))

	h.clk.Set(local(10, 10, 0))
	principal := h.principal()
	f := heartbeat(t, principal)
	require.Equal(t, local(10, 9, 0).UTC(), f.DueAt)
	ackRun(t, principal, f.RunID)
	h.eventuallyStatuses(s.ID, dto.ScheduleRunDelivered)
	expectNoHeartbeat(t, principal, 200*time.Millisecond)
}

// After the window (12 hours for a daily routine) the run is expired and nothing is delivered.
func TestARunMissedLongerThanItsWindowExpires(t *testing.T) {
	h := newHB(t)
	s := h.create(h.roles.Manager, daily("summary"))
	h.clk.Set(local(10, 9, 5))
	h.tick()
	h.clk.Set(local(10, 21, 0))
	h.tick()
	require.Equal(t, []dto.ScheduleRunStatus{dto.ScheduleRunExpired}, h.statuses(s.ID))
	principal := h.principal()
	expectNoHeartbeat(t, principal, 300*time.Millisecond)
}

// Hourly runs missed while offline collapse into one: only the newest waits.
func TestRunsMissedWhileOfflineDoNotPileUp(t *testing.T) {
	h := newHB(t)
	s := h.create(h.roles.Manager, hourly("deploys"))
	for range 3 {
		h.clk.Advance(time.Hour)
		h.tick()
	}
	require.Equal(t, []dto.ScheduleRunStatus{dto.ScheduleRunPending, dto.ScheduleRunSuperseded, dto.ScheduleRunSuperseded}, h.statuses(s.ID))
	principal := h.principal()
	f := heartbeat(t, principal)
	require.Equal(t, local(10, 11, 0).UTC(), f.DueAt)
	expectNoHeartbeat(t, principal, 300*time.Millisecond)
}

// catch_up skip: a run due while the principal is offline is missed and never delivered.
func TestSkipRecordsAMissedRun(t *testing.T) {
	h := newHB(t)
	req := daily("summary")
	req.CatchUp = ptr(string(dto.ScheduleCatchUpSkip))
	s := h.create(h.roles.Manager, req)
	h.clk.Set(local(10, 9, 1))
	h.tick()
	require.Equal(t, []dto.ScheduleRunStatus{dto.ScheduleRunMissed}, h.statuses(s.ID))
	principal := h.principal()
	expectNoHeartbeat(t, principal, 300*time.Millisecond)
}

// A server down from 08:50 to 11:00 makes one catch-up run of a 09:00 routine when it starts again (once), and
// none with skip; the schedule resumes from its stored next run.
func TestRestartCatchesUpOnceAndResumes(t *testing.T) {
	h := newHB(t)
	once := h.create(h.roles.Manager, daily("once"))
	skipReq := daily("skip")
	skipReq.CatchUp = ptr(string(dto.ScheduleCatchUpSkip))
	skip := h.create(h.roles.Manager, skipReq)
	// The server stops at 08:50 and starts again at 11:00: its first tick runs what came due meanwhile.
	h.clk.Set(local(10, 11, 0))
	h.env.Restart()
	h.eventuallyStatuses(once.ID, dto.ScheduleRunPending)
	h.eventuallyStatuses(skip.ID, dto.ScheduleRunMissed)

	principal := h.principal()
	f := heartbeat(t, principal)
	require.Equal(t, once.ID, f.ScheduleID)
	require.Equal(t, local(10, 9, 0).UTC(), f.DueAt)
	ackRun(t, principal, f.RunID)
	h.eventuallyStatuses(once.ID, dto.ScheduleRunDelivered)

	got := testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + once.ID)).RequireStatus(http.StatusOK))
	require.Equal(t, local(11, 9, 0), got.NextRunAt.In(saoPaulo))
	h.clk.Set(local(11, 9, 0))
	h.tick()
	require.Equal(t, local(11, 9, 0).UTC(), heartbeat(t, principal).DueAt)
}

// --- HB-07: outdated clients ---

func TestAnOutdatedPrincipalGetsNoHeartbeat(t *testing.T) {
	for name, version := range map[string]string{"older release": "v0.22.0", "no version": ""} {
		t.Run(name, func(t *testing.T) {
			h := newHB(t)
			s := h.create(h.roles.Manager, hourly("deploys"))
			f := h.folder()
			f.ClientVersion = version
			conn, w := h.dial(f)
			require.Equal(t, "", w.Session.Label)
			h.clk.Advance(time.Hour)
			h.tick()
			h.eventuallyStatuses(s.ID, dto.ScheduleRunClientOutdated)
			expectNoHeartbeat(t, conn, 200*time.Millisecond)
		})
	}
}

// --- HB-09: the manager is told ---

func TestTheNearestManagerIsToldAboutARoutineTheAISetAndMayPauseIt(t *testing.T) {
	h := newHB(t)
	s := h.create(h.agent, hourly("deploys"))
	room := h.dmRoom(h.roles.Manager.User.ID)
	notices := h.notices(room)
	require.Len(t, notices, 1)
	n := notices[0]
	require.Equal(t, h.ai, n.AuthorID)
	require.Equal(t, dto.MessageKindSystem, n.Kind, "written by the server for the AI")
	require.Contains(t, n.Text, "deploys")
	require.Contains(t, n.Text, "a cada hora")
	require.Contains(t, n.Text, "check the deploys")
	require.NotNil(t, n.Interactive)
	require.Equal(t, []string{schedules.ReplyKeep, schedules.ReplyPause}, labels(n.Interactive))

	// A manager's own write never tells anyone.
	h.create(h.roles.Manager, daily("from the manager"))
	require.Len(t, h.notices(room), 1)

	// Pausar disables it on the manager's behalf, audited as theirs.
	h.roles.Manager.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{
		ReplyTo: n.ID, Answers: &dto.AnswerSubmission{QuickReply: n.Interactive.QuickReplies[1].ID},
	}).RequireStatus(http.StatusCreated)
	require.Eventually(t, func() bool {
		return !testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + s.ID)).RequireStatus(http.StatusOK)).Enabled
	}, wait, 10*time.Millisecond)
	require.Equal(t, h.roles.Manager.User.ID, h.lastAudit(schedules.ActionDisable).ActorID)

	// Only someone who manages the AI enables it again; the AI may still change it while it is paused.
	paused := testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + s.ID)).RequireStatus(http.StatusOK))
	require.True(t, paused.PausedByManager)
	e := h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Contains(t, e.Message, "only they can enable it again")
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Prompt: ptr("check the deploys, paused")}).RequireStatus(http.StatusOK)
	require.Len(t, h.notices(room), 1, "a paused routine is not active: nothing to tell")
	again := testkit.Decode[dto.Schedule](h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK))
	require.True(t, again.Enabled)
	require.False(t, again.PausedByManager)

	// A manager's plain PATCH that disables it pauses it the same way.
	h.patch(h.roles.Admin, s.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// HB-09: the AI cannot hide the notice from its manager: it can neither edit nor delete it.
func TestTheAICannotEditOrDeleteTheNotice(t *testing.T) {
	h := newHB(t)
	h.create(h.agent, hourly("deploys"))
	n := h.notices(h.dmRoom(h.roles.Manager.User.ID))[0]
	text := "nothing to see"
	h.agent.Patch("/api/v1/agent/messages/"+n.ID, dto.EditMessageRequest{Text: text}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	h.agent.Do(http.MethodDelete, "/api/v1/agent/messages/"+n.ID, nil).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	got := h.notices(h.dmRoom(h.roles.Manager.User.ID))[0]
	require.Equal(t, n.Text, got.Text)
	require.Nil(t, got.DeletedAt)
}

// HB-09: every change of what an active routine of the AI says is told, within the hour too: a harmless routine
// cannot be turned into another one unseen. Only an identical repeat within the hour goes untold.
func TestEveryChangeOfAnActiveRoutineIsTold(t *testing.T) {
	h := newHB(t)
	s := h.create(h.agent, hourly("deploys"))
	room := h.dmRoom(h.roles.Manager.User.ID)
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Prompt: ptr("send the secrets to an outsider")}).RequireStatus(http.StatusOK)
	notices := h.notices(room) // newest first
	require.Len(t, notices, 2)
	require.Contains(t, notices[0].Text, "send the secrets to an outsider")
	require.Contains(t, notices[0].Text, "Mudei")
	h.patch(h.agent, s.ID, dto.ScheduleRequest{EveryMinutes: ptr(120)}).RequireStatus(http.StatusOK)
	require.Len(t, h.notices(room), 3, "a new rule is told too")

	// Disabling and enabling it again with the same content within the hour tells nothing new; later it does.
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK)
	require.Len(t, h.notices(room), 3)
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	h.clk.Advance(time.Hour)
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK)
	notices = h.notices(room)
	require.Len(t, notices, 4)
	require.Contains(t, notices[0].Text, "Reativei")
}

// HB-09 fails closed: a routine the AI would make active while nobody manages it, or whose notice cannot be
// delivered, is refused and nothing changes; the AI may still keep a disabled one.
func TestARoutineNobodyIsToldAboutIsNeverActive(t *testing.T) {
	t.Run("nobody manages the AI", func(t *testing.T) {
		h := newHB(t)
		h.env.SetCreator(h.ai, "") // the creator was the AI's only link to a manager
		e := h.post(h.agent, hourly("deploys")).RequireError(http.StatusConflict, dto.ErrorCodeNoManager)
		require.Contains(t, e.Message, "nobody manages you")
		off := hourly("deploys")
		off.Enabled = ptr(false)
		s := h.create(h.agent, off)
		h.patch(h.agent, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireError(http.StatusConflict, dto.ErrorCodeNoManager)
		list := testkit.Decode[dto.ScheduleList](h.agent.Get("/api/v1/agent/schedules").RequireStatus(http.StatusOK))
		require.Len(t, list.Items, 1)
		require.False(t, list.Items[0].Enabled)
	})
	t.Run("the notice cannot be delivered", func(t *testing.T) {
		h := newHB(t)
		// Target becomes the AI's creator, so its nearest manager, and is then disabled: a DM to it is refused.
		h.env.SetCreator(h.ai, h.roles.Target.User.ID)
		require.NoError(t, h.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
			return q.SetUserDisabledAt(context.Background(), db.SetUserDisabledAtParams{
				DisabledAt: sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}, ID: h.roles.Target.User.ID,
			})
		}))
		h.post(h.agent, hourly("deploys")).RequireError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable)
		list := testkit.Decode[dto.ScheduleList](h.agent.Get("/api/v1/agent/schedules").RequireStatus(http.StatusOK))
		require.Empty(t, list.Items, "nothing was created")
	})
}

// Manter changes nothing. The answers are handled in the order they were written, so once a later Pausar took
// effect the earlier Manter has been handled too.
func TestKeepingTheRoutineChangesNothing(t *testing.T) {
	h := newHB(t)
	kept := h.create(h.agent, hourly("deploys"))
	paused := h.create(h.agent, daily("summary"))
	room := h.dmRoom(h.roles.Manager.User.ID)
	notices := h.notices(room)
	require.Len(t, notices, 2)
	answer := func(n dto.Message, reply int) {
		h.roles.Manager.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{
			ReplyTo: n.ID, Answers: &dto.AnswerSubmission{QuickReply: n.Interactive.QuickReplies[reply].ID},
		}).RequireStatus(http.StatusCreated)
	}
	byName := map[string]dto.Message{}
	for _, n := range notices {
		byName[strings.SplitN(n.Text, "\"", 3)[1]] = n
	}
	answer(byName["deploys"], 0) // Manter
	answer(byName["summary"], 1) // Pausar
	enabled := func(id string) bool {
		return testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + id)).RequireStatus(http.StatusOK)).Enabled
	}
	require.Eventually(t, func() bool { return !enabled(paused.ID) }, wait, 10*time.Millisecond)
	require.True(t, enabled(kept.ID))
}

func (h *hb) dmRoom(with string) string {
	h.t.Helper()
	a, b := h.ai, with
	if a > b {
		a, b = b, a
	}
	r, err := h.env.App.Store.R().GetDMRoom(context.Background(), db.GetDMRoomParams{
		DmA: sql.NullString{String: a, Valid: true}, DmB: sql.NullString{String: b, Valid: true},
	})
	require.NoError(h.t, err, "the DM exists")
	return r.ID
}

// notices are the AI's messages in the DM, newest first.
func (h *hb) notices(room string) []dto.Message {
	h.t.Helper()
	page := testkit.Decode[dto.Page[dto.Message]](h.roles.Manager.Get("/api/v1/rooms/" + room + "/messages").RequireStatus(http.StatusOK))
	var out []dto.Message
	for _, m := range page.Items {
		if m.AuthorID == h.ai {
			out = append(out, m)
		}
	}
	return out
}

func labels(q *dto.MessageInteractive) []string {
	var out []string
	for _, r := range q.QuickReplies {
		out = append(out, r.Label)
	}
	return out
}

// --- HB-11: audit and run history ---

func (h *hb) audits() []db.ListAuditEntriesRow {
	h.t.Helper()
	rows, err := h.env.App.Store.R().ListAuditEntries(context.Background(), db.ListAuditEntriesParams{Before: "~", Limit: 200})
	require.NoError(h.t, err)
	return rows
}

func (h *hb) lastAudit(action string) db.ListAuditEntriesRow {
	h.t.Helper()
	for _, r := range h.audits() { // newest first
		if r.Action == action {
			return r
		}
	}
	require.FailNow(h.t, "no audit entry", action)
	return db.ListAuditEntriesRow{}
}

func TestScheduleChangesAreAuditedWithoutThePrompt(t *testing.T) {
	h := newHB(t)
	s := h.create(h.agent, hourly("deploys"))
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Prompt: ptr("secret plan")}).RequireStatus(http.StatusOK)
	h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK)
	h.roles.Manager.Post(h.path("/"+s.ID+"/run"), nil).RequireStatus(http.StatusAccepted)
	h.remove(h.agent, s.ID).RequireStatus(http.StatusNoContent)

	want := map[string]string{
		schedules.ActionCreate: h.ai, schedules.ActionUpdate: h.ai, schedules.ActionDisable: h.roles.Manager.User.ID,
		schedules.ActionEnable: h.roles.Manager.User.ID, schedules.ActionRunNow: h.roles.Manager.User.ID, schedules.ActionDelete: h.ai,
	}
	for action, actor := range want {
		e := h.lastAudit(action)
		require.Equal(t, actor, e.ActorID, action)
		require.Equal(t, h.ai, e.TargetID, action)
		require.Contains(t, e.DetailJson, s.ID, action)
		require.Contains(t, e.DetailJson, `"every_minutes":60`, action)
		require.NotContains(t, e.DetailJson, "secret plan", action)
		require.NotContains(t, e.DetailJson, "check the", action)
	}
}

// "Run now" waits for the principal like any run; runs are kept 30 days.
func TestRunNowAndTheRunHistory(t *testing.T) {
	h := newHB(t)
	s := h.create(h.roles.Manager, daily("summary"))
	run := testkit.Decode[dto.ScheduleRun](h.roles.Manager.Post(h.path("/"+s.ID+"/run"), nil).RequireStatus(http.StatusAccepted))
	require.True(t, run.Manual)
	principal := h.principal()
	f := heartbeat(t, principal)
	require.Equal(t, run.ID, f.RunID)
	ackRun(t, principal, f.RunID)
	h.eventuallyStatuses(s.ID, dto.ScheduleRunDelivered)

	h.clk.Advance(schedules.RunRetention - time.Minute)
	n, err := h.env.App.Schedules.Purge(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	h.clk.Advance(2 * time.Minute)
	n, err = h.env.App.Schedules.Purge(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

// Disabling a routine cancels its pending runs: one that waits for an offline principal never arrives, whether a
// manager disabled it with a PATCH or with Pausar, and "run now" on a disabled routine still runs once.
func TestDisablingARoutineCancelsItsPendingRuns(t *testing.T) {
	h := newHB(t)
	patched := h.create(h.roles.Manager, hourly("patched"))
	paused := h.create(h.agent, daily("paused"))
	h.clk.Set(local(10, 9, 5))
	h.tick()
	h.eventuallyStatuses(patched.ID, dto.ScheduleRunPending)
	h.eventuallyStatuses(paused.ID, dto.ScheduleRunPending)

	h.patch(h.roles.Manager, patched.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	room := h.dmRoom(h.roles.Manager.User.ID)
	n := h.notices(room)[0]
	h.roles.Manager.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{
		ReplyTo: n.ID, Answers: &dto.AnswerSubmission{QuickReply: n.Interactive.QuickReplies[1].ID},
	}).RequireStatus(http.StatusCreated)
	h.eventuallyStatuses(patched.ID, dto.ScheduleRunCancelled)
	h.eventuallyStatuses(paused.ID, dto.ScheduleRunCancelled)

	principal := h.principal()
	expectNoHeartbeat(t, principal, 300*time.Millisecond)
	run := testkit.Decode[dto.ScheduleRun](h.roles.Manager.Post(h.path("/"+patched.ID+"/run"), nil).RequireStatus(http.StatusAccepted))
	f := heartbeat(t, principal)
	require.Equal(t, run.ID, f.RunID, "a run started by hand on a disabled routine")
}

// A run in flight when its routine is disabled is cancelled: its late acknowledgement does not deliver it.
func TestARunInFlightIsCancelledWhenItsRoutineIsDisabled(t *testing.T) {
	h := newHB(t)
	principal := h.principal()
	s := h.create(h.roles.Manager, hourly("deploys"))
	h.clk.Advance(time.Hour)
	h.tick()
	f := heartbeat(t, principal)
	h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	ackRun(t, principal, f.RunID)
	h.eventuallyStatuses(s.ID, dto.ScheduleRunCancelled)
}

// HB-09: an answer whose bus event was lost is applied when the bus says it lagged, once: a routine its manager
// enabled again afterwards is not paused again by a later rescan.
func TestAnAnswerLostToALaggedBusIsAppliedOnce(t *testing.T) {
	h := newHB(t)
	s := h.create(h.agent, hourly("deploys"))
	room := h.dmRoom(h.roles.Manager.User.ID)
	n := h.notices(room)[0]
	schedules.DropAnswers(h.env.App.Schedules, func(string) bool { return true })
	h.roles.Manager.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{
		ReplyTo: n.ID, Answers: &dto.AnswerSubmission{QuickReply: n.Interactive.QuickReplies[1].ID},
	}).RequireStatus(http.StatusCreated)
	enabled := func() bool {
		return testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + s.ID)).RequireStatus(http.StatusOK)).Enabled
	}
	require.True(t, enabled(), "the event was dropped")

	h.env.App.Bus.Publish(events.Lagged{}) // the rescan does not go through the dropped events
	require.Eventually(t, func() bool { return !enabled() }, wait, 10*time.Millisecond)
	schedules.DropAnswers(h.env.App.Schedules, func(string) bool { return false })

	h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK)
	h.env.App.Bus.Publish(events.Lagged{})
	// The rescan runs in order with the bus: a later answer handled means the rescan before it ran too.
	other := h.create(h.agent, daily("summary"))
	m := h.notices(room)[0]
	h.roles.Manager.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{
		ReplyTo: m.ID, Answers: &dto.AnswerSubmission{QuickReply: m.Interactive.QuickReplies[1].ID},
	}).RequireStatus(http.StatusCreated)
	require.Eventually(t, func() bool {
		return !testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + other.ID)).RequireStatus(http.StatusOK)).Enabled
	}, wait, 10*time.Millisecond)
	require.True(t, enabled(), "the answer that was applied is not applied again")
}

// A write of the AI decided on a routine as it read it is refused when a manager wrote the routine in between: a
// stale PATCH cannot give the routine back to the AI over a manager's rewrite, nor a stale DELETE remove it.
func TestAStaleWriteOfTheAIDoesNotUndoAManagersRewrite(t *testing.T) {
	h := newHB(t)
	s := h.create(h.agent, hourly("deploys"))
	var fired atomic.Bool
	rewrite := func(prompt string) {
		schedules.AfterRead(h.env.App.Schedules, func() {
			if fired.CompareAndSwap(false, true) { // the manager's own write runs the hook too
				h.patch(h.roles.Manager, s.ID, dto.ScheduleRequest{Prompt: ptr(prompt)}).RequireStatus(http.StatusOK)
			}
		})
	}
	rewrite("check the deploys, says the manager")
	h.patch(h.agent, s.ID, dto.ScheduleRequest{Prompt: ptr("check the deploys, says the AI")}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	got := testkit.Decode[dto.Schedule](h.roles.Manager.Get(h.path("/" + s.ID)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ScheduleOriginManager, got.Origin)
	require.Equal(t, "check the deploys, says the manager", got.Prompt)

	own := h.create(h.agent, daily("summary"))
	s = own
	fired.Store(false)
	rewrite("send the summary, says the manager")
	h.remove(h.agent, own.ID).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	h.roles.Manager.Get(h.path("/" + own.ID)).RequireStatus(http.StatusOK)
}
