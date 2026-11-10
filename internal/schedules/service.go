// Package schedules is the AIs' scheduled heartbeats (ai-supervisor feature, HB-01..HB-12): routines an AI or
// whoever manages it sets, such as "every hour check the deploys" or "every day at 9:00 send the summary". A
// scheduler fires each due routine as a run, and the agent gateway delivers the pending runs of an AI to its
// principal session as heartbeat frames. A routine is a prompt, never a command: the server runs nothing.
package schedules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// The limits and defaults of heartbeats. They are the options the owner was recommended and took pending
// review (spec "Decisions", 2026-11-10); each is one constant so it is easy to change.
const (
	// DefaultTimezone is the timezone of a schedule that names none (HB-01).
	DefaultTimezone = "America/Sao_Paulo"
	// MinInterval is the shortest time between two runs of one rule (HB-03).
	MinInterval = 15 * time.Minute
	// MaxRunsPerDay is the most runs the enabled schedules of one AI may make in any 24 hours (HB-03).
	MaxRunsPerDay = 48
	// MaxSchedules is the most schedules one AI has (HB-03).
	MaxSchedules = 20
	// MaxEveryMinutes is the longest interval: a year.
	MaxEveryMinutes = 365 * 24 * 60
	// MaxCatchUp bounds the window of a run that waits for the principal (HB-06): the shorter of the rule's
	// period and this.
	MaxCatchUp = 12 * time.Hour
	// WriteLimit schedule writes an AI makes at most per WriteWindow (HB-10).
	WriteLimit  = 20
	WriteWindow = time.Hour
	// NoticeEvery is how often at most the nearest manager is told about one schedule the AI set (HB-09).
	NoticeEvery = time.Hour
	// RunRetention is how long runs are kept (HB-11).
	RunRetention = 30 * 24 * time.Hour
	// RunsPageSize is how many runs GET .../runs returns.
	RunsPageSize = 50
	// MaxSleep is the longest the scheduler sleeps between two looks at the due schedules.
	MaxSleep = 30 * time.Second
	// runLimit is how many due schedules one tick takes.
	runLimit = 500
	// limitHorizon is how far ahead the 24-hour cap expands the rules: a week, so weekly rules count.
	limitHorizon = 8 * 24 * time.Hour
)

// The quick replies of the manager notice (HB-09): Pausar disables the schedule.
const (
	ReplyKeep  = "Manter"
	ReplyPause = "Pausar"
)

// Audit actions of schedules (HB-11): the entry names the actor, the AI, the schedule and its rule, never
// the prompt.
const (
	ActionCreate  = "agent.schedule_create"
	ActionUpdate  = "agent.schedule_update"
	ActionEnable  = "agent.schedule_enable"
	ActionDisable = "agent.schedule_disable"
	ActionDelete  = "agent.schedule_delete"
	ActionRunNow  = "agent.schedule_run"
)

// Errors of the writes.
var (
	// ErrRateLimited is an AI over WriteLimit writes in WriteWindow (HB-10).
	ErrRateLimited = httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited,
		fmt.Sprintf("rate limited: at most %d schedule changes an hour", WriteLimit))
	// ErrTooMany is a new schedule of an AI that has MaxSchedules (HB-03).
	ErrTooMany = httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeTooMany,
		fmt.Sprintf("an AI has at most %d schedules", MaxSchedules))
	// ErrNoManager is a routine an AI would make active while nobody manages it: nobody can be told (HB-09).
	ErrNoManager = httpx.NewError(http.StatusConflict, dto.ErrorCodeNoManager,
		"nobody manages you in your organization, so a routine you set cannot be shown to a manager: ask someone who manages you to set it")
	// ErrNoticeFailed is a routine an AI would make active whose manager notice could not be delivered (HB-09).
	ErrNoticeFailed = httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable,
		"your nearest manager could not be told about the routine, so nothing changed: try again later")
	// errPausedByManager is an AI enabling again a routine someone who manages it paused.
	errPausedByManager = httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden,
		"someone who manages you paused this routine: only they can enable it again")
	// errSelfOnly is an AI's write of a schedule someone who manages it set (HB-02).
	errSelfOnly = httpx.NewError(http.StatusForbidden, dto.ErrorCodeForbidden,
		"this schedule was set by someone who manages you: you may read it, not change it")
)

func tooFrequentInterval() error {
	return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeTooFrequent,
		fmt.Sprintf("a schedule runs at most every %d minutes", int(MinInterval/time.Minute)))
}

func tooFrequentDaily() error {
	return httpx.NewError(http.StatusUnprocessableEntity, dto.ErrorCodeTooFrequent,
		fmt.Sprintf("the enabled schedules of an AI run at most %d times in 24 hours", MaxRunsPerDay))
}

// ManagerNotifier writes, as the AI, in its DM with one of its managers (HB-09): the text and the quick
// replies of a question. It returns the id of the message. The app implements it with the DM use case.
type ManagerNotifier interface {
	NotifyManager(ctx context.Context, agentID, managerIID, text string, quickReplies []string) (string, error)
}

// Deps are the collaborators of the Service.
type Deps struct {
	Store  *store.Store
	Access *access.Authorizer
	Authz  *orgs.Authz
	Audit  *audit.Log
	Bus    *events.Bus
	// PrincipalLive reports whether the AI's principal session is live; nil is never.
	PrincipalLive func(agentID string) bool
	// Notifier tells the nearest manager about a schedule the AI set (HB-09); nil tells nobody.
	Notifier ManagerNotifier
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service is the schedules use cases, the scheduler and the delivery side the agent gateway reads.
type Service struct {
	d      Deps
	writes *limiter
	kick   chan struct{}
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.PrincipalLive == nil {
		d.PrincipalLive = func(string) bool { return false }
	}
	return &Service{d: d, writes: newLimiter(WriteLimit, WriteWindow, d.Now), kick: make(chan struct{}, 1)}
}

// role is what the caller is to the AI.
type role int

const (
	roleViewer  role = iota + 1 // sees the AI, does not manage it
	roleManager                 // manages it (POS-04)
	roleSelf                    // the AI itself
)

// roleOf is the caller's role for the AI: the AI itself (an agent principal acting as it), whoever manages it
// in an organization they share, or whoever sees it. Nobody else learns that the AI exists
// (access.ErrNotFound).
func (s *Service) roleOf(ctx context.Context, p authn.Principal, agentID string) (role, error) {
	if _, err := s.d.Store.R().GetAgent(ctx, agentID); errors.Is(err, sql.ErrNoRows) {
		return 0, access.ErrNotFound
	} else if err != nil {
		return 0, err
	}
	switch p.Kind {
	case authn.KindAgent:
		switch p.AgentID {
		case agentID:
			return roleSelf, nil
		case "":
			return 0, access.ErrForbidden // an agent token on the managers' routes (TOK-03)
		}
		return 0, access.ErrNotFound
	case authn.KindUser:
	default:
		return 0, access.ErrNotFound
	}
	see, err := s.d.Access.CanSeeAI(ctx, p, agentID)
	if err != nil {
		return 0, err
	}
	if !see {
		return 0, access.ErrNotFound
	}
	managed, err := s.d.Authz.ManagesAnywhere(ctx, p.UserID, agentID)
	if err != nil {
		return 0, err
	}
	if managed {
		return roleManager, nil
	}
	return roleViewer, nil
}

// Input is a create or an update: only the fields given change; a create needs Name, Prompt and one rule.
type Input struct {
	Name         *string
	Prompt       *string
	EveryMinutes *int
	Cron         *string
	Timezone     *string
	CatchUp      *string
	Enabled      *bool
}

// InputFrom reads the request body.
func InputFrom(r dto.ScheduleRequest) Input {
	return Input{Name: r.Name, Prompt: r.Prompt, EveryMinutes: r.EveryMinutes, Cron: r.Cron, Timezone: r.Timezone, CatchUp: r.CatchUp, Enabled: r.Enabled}
}

// spec is a schedule's content once checked.
type spec struct {
	name, prompt string
	every        int
	cron         string
	tz           string
	catchUp      string
	enabled      bool
}

func (sp spec) rule(anchor time.Time) (Rule, error) {
	loc, err := time.LoadLocation(sp.tz)
	if err != nil {
		return Rule{}, err
	}
	r := Rule{EveryMinutes: sp.every, Loc: loc, Anchor: anchor}
	if sp.cron != "" {
		if r.Cron, err = ParseCron(sp.cron); err != nil {
			return Rule{}, err
		}
	}
	return r, nil
}

func specOf(row db.AgentSchedule) spec {
	return spec{
		name: row.Name, prompt: row.Prompt, every: int(row.EveryMinutes.Int64), cron: row.Cron.String,
		tz: row.Tz, catchUp: row.CatchUp, enabled: row.Enabled,
	}
}

// ruleOf is the rule of a stored schedule.
func ruleOf(row db.AgentSchedule) (Rule, error) {
	return specOf(row).rule(time.UnixMilli(row.AnchorAt))
}

// merge applies the input to base and checks the result (HB-01, the interval part of HB-03).
func merge(base spec, in Input) (spec, error) {
	out := base
	check := validation.Check{}
	if in.Name != nil {
		out.name = strings.TrimSpace(*in.Name)
	}
	if in.Prompt != nil {
		out.prompt = strings.TrimSpace(*in.Prompt)
	}
	switch {
	case in.EveryMinutes != nil && in.Cron != nil:
		check.Add("cron", "give either every_minutes or cron, not both")
	case in.EveryMinutes != nil:
		out.every, out.cron = *in.EveryMinutes, ""
	case in.Cron != nil:
		out.every, out.cron = 0, strings.TrimSpace(*in.Cron)
	}
	if in.Timezone != nil {
		out.tz = strings.TrimSpace(*in.Timezone)
	}
	if out.tz == "" {
		out.tz = DefaultTimezone
	}
	if in.CatchUp != nil {
		out.catchUp = strings.TrimSpace(*in.CatchUp)
	}
	if out.catchUp == "" {
		out.catchUp = string(dto.ScheduleCatchUpOnce)
	}
	if in.Enabled != nil {
		out.enabled = *in.Enabled
	}
	if n := utf8.RuneCountInString(out.name); n < 1 || n > dto.MaxScheduleName {
		check.Add("name", fmt.Sprintf("must be 1 to %d characters", dto.MaxScheduleName))
	}
	if n := utf8.RuneCountInString(out.prompt); n < 1 || n > dto.MaxSchedulePrompt {
		check.Add("prompt", fmt.Sprintf("must be 1 to %d characters", dto.MaxSchedulePrompt))
	}
	if out.catchUp != string(dto.ScheduleCatchUpOnce) && out.catchUp != string(dto.ScheduleCatchUpSkip) {
		check.Add("catch_up", "must be once or skip")
	}
	if _, err := time.LoadLocation(out.tz); err != nil || strings.EqualFold(out.tz, "local") {
		check.Add("tz", "must be an IANA timezone such as America/Sao_Paulo")
	}
	switch {
	case out.cron == "" && out.every == 0:
		check.Add("every_minutes", "give every_minutes or cron")
	case out.cron != "":
		if _, err := ParseCron(out.cron); err != nil {
			check.Add("cron", err.Error())
		}
	case out.every < 1 || out.every > MaxEveryMinutes:
		check.Add("every_minutes", fmt.Sprintf("must be %d to %d", int(MinInterval/time.Minute), MaxEveryMinutes))
	}
	if err := check.Err(); err != nil {
		return out, err
	}
	r, err := out.rule(time.Time{})
	if err != nil {
		return out, err
	}
	if _, err := r.NextChecked(time.Now()); err != nil {
		check.Add("cron", err.Error())
		return out, check.Err()
	}
	if r.MinGap() < MinInterval {
		return out, tooFrequentInterval()
	}
	if r.Cron != nil && r.Cron.perDay() > MaxRunsPerDay {
		return out, tooFrequentDaily()
	}
	return out, nil
}

// checkDaily refuses a set of enabled schedules that fire more than MaxRunsPerDay times in any 24 hours of the
// next limitHorizon (HB-03). candidate replaces the stored schedule with its id (a create has a new id).
func checkDaily(now time.Time, stored []db.AgentSchedule, candidateID string, candidate Rule, candidateEnabled bool) error {
	end := now.Add(limitHorizon)
	perRule := int(limitHorizon/MinInterval) + 1
	var times []time.Time
	for _, row := range stored {
		if row.ID == candidateID || !row.Enabled {
			continue
		}
		r, err := ruleOf(row)
		if err != nil {
			continue // a rule this build cannot read again never fires (Tick skips it too)
		}
		times = append(times, r.runsIn(now, end, perRule)...)
	}
	if candidateEnabled {
		times = append(times, candidate.runsIn(now, end, perRule)...)
	}
	if maxIn24h(times) > MaxRunsPerDay {
		return tooFrequentDaily()
	}
	return nil
}

// cancelPending cancels the pending runs of a routine that was disabled: a run waiting for the principal, or in
// flight, is never delivered afterwards.
func cancelPending(ctx context.Context, q *db.Queries, id string) error {
	if _, err := q.CancelPendingRuns(ctx, id); err != nil {
		return err
	}
	return q.RefreshScheduleLastRun(ctx, id)
}

// checkLimits applies HB-03 to a write of the schedule id: a new one (create) must not be over MaxSchedules, and
// an enabled rule (enabled) must keep the AI's routines within MaxRunsPerDay.
func checkLimits(ctx context.Context, q *db.Queries, agentID, id string, r Rule, enabled, create bool, now time.Time) error {
	if create {
		n, err := q.CountSchedulesByAgent(ctx, agentID)
		if err != nil {
			return err
		}
		if n >= MaxSchedules {
			return ErrTooMany
		}
	}
	if !enabled {
		return nil
	}
	stored, err := q.ListSchedulesByAgent(ctx, agentID)
	if err != nil {
		return err
	}
	return checkDaily(now, stored, id, r, true)
}

// nullInt and nullStr build nullable columns.
func nullInt(v int64, ok bool) sql.NullInt64   { return sql.NullInt64{Int64: v, Valid: ok} }
func nullStr(v string, ok bool) sql.NullString { return sql.NullString{String: v, Valid: ok} }

// nextRun is the next run of an enabled rule after now, as a nullable column.
func nextRun(r Rule, enabled bool, now time.Time) sql.NullInt64 {
	if !enabled {
		return sql.NullInt64{}
	}
	t := r.Next(now)
	return nullInt(t.UnixMilli(), !t.IsZero())
}

// List returns the AI's schedules for the caller (HB-12): the AI and whoever manages it read them, anyone else
// who sees the AI only how many there are.
func (s *Service) List(ctx context.Context, p authn.Principal, agentID string) (dto.ScheduleList, error) {
	rl, err := s.roleOf(ctx, p, agentID)
	if err != nil {
		return dto.ScheduleList{}, err
	}
	rows, err := s.d.Store.R().ListSchedulesByAgent(ctx, agentID)
	if err != nil {
		return dto.ScheduleList{}, err
	}
	out := dto.ScheduleList{Items: []dto.Schedule{}, Count: len(rows), CanManage: rl == roleManager}
	if rl == roleViewer {
		return out, nil
	}
	refs, err := s.setters(ctx, rows...)
	if err != nil {
		return dto.ScheduleList{}, err
	}
	for _, row := range rows {
		out.Items = append(out.Items, toDTO(row, refs, rl))
	}
	return out, nil
}

// Get returns one schedule of the AI to the AI or whoever manages it.
func (s *Service) Get(ctx context.Context, p authn.Principal, agentID, id string) (dto.Schedule, error) {
	rl, row, err := s.readable(ctx, p, agentID, id)
	if err != nil {
		return dto.Schedule{}, err
	}
	return s.render(ctx, row, rl)
}

// readable loads a schedule the caller may read.
func (s *Service) readable(ctx context.Context, p authn.Principal, agentID, id string) (role, db.AgentSchedule, error) {
	rl, err := s.roleOf(ctx, p, agentID)
	if err != nil {
		return 0, db.AgentSchedule{}, err
	}
	if rl == roleViewer {
		return 0, db.AgentSchedule{}, access.ErrForbidden
	}
	row, err := s.d.Store.R().GetScheduleForAgent(ctx, db.GetScheduleForAgentParams{ID: id, AgentID: agentID})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.AgentSchedule{}, access.ErrNotFound
	}
	return rl, row, err
}

// writable loads a schedule the caller may change (HB-02): whoever manages the AI any of them, the AI its own.
// An AI's write counts against its allowance (HB-10).
func (s *Service) writable(ctx context.Context, p authn.Principal, agentID, id string) (role, db.AgentSchedule, error) {
	rl, row, err := s.readable(ctx, p, agentID, id)
	if err != nil {
		return 0, row, err
	}
	if rl == roleSelf {
		if row.Origin != string(dto.ScheduleOriginSelf) {
			return 0, row, errSelfOnly
		}
		if !s.writes.allow(agentID) {
			return 0, row, ErrRateLimited
		}
	}
	return rl, row, nil
}

// actorOf is the intelligence that acts: the AI or the user.
func actorOf(p authn.Principal) string { return p.IID() }

// Create adds a schedule to the AI (HB-01): the AI's own (origin self) or one set by whoever manages it
// (origin manager). An enabled routine the AI sets is told to its nearest manager first, and is refused when
// that cannot be done (HB-09, notifyFirst).
func (s *Service) Create(ctx context.Context, p authn.Principal, agentID string, in Input) (dto.Schedule, error) {
	rl, err := s.roleOf(ctx, p, agentID)
	if err != nil {
		return dto.Schedule{}, err
	}
	if rl == roleViewer {
		return dto.Schedule{}, access.ErrForbidden
	}
	enabled := true
	sp, err := merge(spec{enabled: enabled}, in)
	if err != nil {
		return dto.Schedule{}, err
	}
	if rl == roleSelf && !s.writes.allow(agentID) {
		return dto.Schedule{}, ErrRateLimited
	}
	now := s.d.Now()
	r, err := sp.rule(now)
	if err != nil {
		return dto.Schedule{}, err
	}
	actor := actorOf(p)
	origin := dto.ScheduleOriginManager
	if rl == roleSelf {
		origin = dto.ScheduleOriginSelf
	}
	id := store.NewID()
	next := nextRun(r, sp.enabled, now) // outside the write transaction, like every rule computation
	// The limits on a read first: a notice goes out only for a write that can be made.
	if err := checkLimits(ctx, s.d.Store.R(), agentID, id, r, sp.enabled, true, now); err != nil {
		return dto.Schedule{}, err
	}
	var nt *notice
	if rl == roleSelf && sp.enabled {
		if nt, err = s.notifyFirst(ctx, agentID, id, nil, sp, noticeCreated, now); err != nil {
			return dto.Schedule{}, err
		}
	}
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := checkLimits(ctx, q, agentID, id, r, sp.enabled, true, now); err != nil {
			return err
		}
		if err := q.CreateSchedule(ctx, db.CreateScheduleParams{
			ID: id, AgentID: agentID, Name: sp.name, Prompt: sp.prompt, RuleKind: ruleKind(sp),
			EveryMinutes: nullInt(int64(sp.every), sp.cron == ""), Cron: nullStr(sp.cron, sp.cron != ""), Tz: sp.tz,
			CatchUp: sp.catchUp, Enabled: sp.enabled, Origin: string(origin), SetBy: actor, CreatedBy: actor, UpdatedBy: actor,
			AnchorAt: now.UnixMilli(), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(), NextRunAt: next,
		}); err != nil {
			return err
		}
		if err := recordNotice(ctx, q, nt, id, now); err != nil {
			return err
		}
		return s.record(ctx, q, actor, ActionCreate, agentID, id, sp)
	})
	if err != nil {
		return dto.Schedule{}, err
	}
	row, err := s.d.Store.R().GetSchedule(ctx, id)
	if err != nil {
		return dto.Schedule{}, err
	}
	s.changed(agentID)
	return s.render(ctx, row, rl)
}

func ruleKind(sp spec) string {
	if sp.cron != "" {
		return "cron"
	}
	return "every"
}

// Update changes a schedule (HB-01, HB-02). A new interval, a new timezone or rule, or a schedule enabled again
// starts its series from now. When whoever manages the AI rewrites the prompt of one the AI set, it becomes
// theirs (origin manager). A change of content the AI makes to an enabled routine, and its enabling again, is
// told to its nearest manager first (HB-09, notifyFirst). A routine someone who manages the AI disabled is enabled
// again only by someone who manages it.
func (s *Service) Update(ctx context.Context, p authn.Principal, agentID, id string, in Input) (dto.Schedule, error) {
	rl, row, err := s.writable(ctx, p, agentID, id)
	if err != nil {
		return dto.Schedule{}, err
	}
	base := specOf(row)
	sp, err := merge(base, in)
	if err != nil {
		return dto.Schedule{}, err
	}
	now := s.d.Now()
	actor := actorOf(p)
	ruleChanged := sp.every != base.every || sp.cron != base.cron || sp.tz != base.tz
	reEnabled := sp.enabled && !base.enabled
	anchor := time.UnixMilli(row.AnchorAt)
	if ruleChanged || reEnabled {
		anchor = now
	}
	r, err := sp.rule(anchor)
	if err != nil {
		return dto.Schedule{}, err
	}
	next := row.NextRunAt
	if ruleChanged || reEnabled || !sp.enabled || !next.Valid {
		next = nextRun(r, sp.enabled, now)
	}
	pausedBy := row.PausedBy
	switch {
	case rl == roleSelf && reEnabled && row.PausedBy != "":
		return dto.Schedule{}, errPausedByManager
	case rl == roleManager && !sp.enabled && base.enabled:
		pausedBy = actor // only someone who manages the AI enables it again
	case rl == roleManager && sp.enabled:
		pausedBy = ""
	}
	origin, setBy := row.Origin, row.SetBy
	if sp.prompt != base.prompt {
		setBy = actor
		origin = string(dto.ScheduleOriginManager)
		if rl == roleSelf {
			origin = string(dto.ScheduleOriginSelf)
		}
	}
	contentChanged := sp.name != base.name || sp.prompt != base.prompt || ruleChanged || sp.catchUp != base.catchUp
	action := ActionUpdate
	if !contentChanged && sp.enabled != base.enabled {
		action = ActionDisable
		if sp.enabled {
			action = ActionEnable
		}
	}
	checked := sp.enabled && (ruleChanged || reEnabled)
	if err := checkLimits(ctx, s.d.Store.R(), agentID, id, r, checked, false, now); err != nil {
		return dto.Schedule{}, err
	}
	var nt *notice
	if rl == roleSelf && sp.enabled && (contentChanged || reEnabled) {
		verb := noticeChanged
		if !contentChanged {
			verb = noticeReenabled
		}
		if nt, err = s.notifyFirst(ctx, agentID, id, &row, sp, verb, now); err != nil {
			return dto.Schedule{}, err
		}
	}
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := checkLimits(ctx, q, agentID, id, r, checked, false, now); err != nil {
			return err
		}
		if err := q.UpdateSchedule(ctx, db.UpdateScheduleParams{
			Name: sp.name, Prompt: sp.prompt, RuleKind: ruleKind(sp),
			EveryMinutes: nullInt(int64(sp.every), sp.cron == ""), Cron: nullStr(sp.cron, sp.cron != ""), Tz: sp.tz,
			CatchUp: sp.catchUp, Enabled: sp.enabled, Origin: origin, SetBy: setBy, UpdatedBy: actor,
			AnchorAt: anchor.UnixMilli(), UpdatedAt: now.UnixMilli(), NextRunAt: next, PausedBy: pausedBy, ID: id,
		}); err != nil {
			return err
		}
		if err := recordNotice(ctx, q, nt, id, now); err != nil {
			return err
		}
		if !sp.enabled && base.enabled {
			if err := cancelPending(ctx, q, id); err != nil {
				return err
			}
		}
		return s.record(ctx, q, actor, action, agentID, id, sp)
	})
	if err != nil {
		return dto.Schedule{}, err
	}
	updated, err := s.d.Store.R().GetSchedule(ctx, id)
	if err != nil {
		return dto.Schedule{}, err
	}
	s.changed(agentID)
	return s.render(ctx, updated, rl)
}

// Delete deletes a schedule with its runs (HB-01, HB-02).
func (s *Service) Delete(ctx context.Context, p authn.Principal, agentID, id string) error {
	_, row, err := s.writable(ctx, p, agentID, id)
	if err != nil {
		return err
	}
	actor := actorOf(p)
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.DeleteSchedule(ctx, db.DeleteScheduleParams{ID: id, AgentID: agentID}); err != nil {
			return err
		}
		return s.record(ctx, q, actor, ActionDelete, agentID, id, specOf(row))
	})
	if err != nil {
		return err
	}
	s.changed(agentID)
	return nil
}

// RunNow fires a run of the schedule at once, on behalf of whoever manages the AI (HB-11, HB-13). It waits for
// the principal like any run, within the rule's window.
func (s *Service) RunNow(ctx context.Context, p authn.Principal, agentID, id string) (dto.ScheduleRun, error) {
	rl, row, err := s.readable(ctx, p, agentID, id)
	if err != nil {
		return dto.ScheduleRun{}, err
	}
	if rl != roleManager {
		return dto.ScheduleRun{}, access.ErrForbidden
	}
	r, err := ruleOf(row)
	if err != nil {
		return dto.ScheduleRun{}, err
	}
	now := s.d.Now()
	runID := store.NewID()
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := s.insertRun(ctx, q, row, runID, now, now.Add(r.window(now)), dto.ScheduleRunPending, true, now); err != nil {
			return err
		}
		return s.record(ctx, q, actorOf(p), ActionRunNow, agentID, id, specOf(row))
	})
	if err != nil {
		return dto.ScheduleRun{}, err
	}
	s.queued(agentID)
	s.changed(agentID)
	return dto.ScheduleRun{ID: runID, ScheduleID: id, DueAt: now.UTC(), Status: dto.ScheduleRunPending, Manual: true}, nil
}

// Runs lists the newest runs of a schedule (HB-11) to the AI or whoever manages it.
func (s *Service) Runs(ctx context.Context, p authn.Principal, agentID, id string) ([]dto.ScheduleRun, error) {
	if _, _, err := s.readable(ctx, p, agentID, id); err != nil {
		return nil, err
	}
	rows, err := s.d.Store.R().ListRunsBySchedule(ctx, db.ListRunsByScheduleParams{ScheduleID: id, Limit: RunsPageSize})
	if err != nil {
		return nil, err
	}
	out := make([]dto.ScheduleRun, len(rows))
	for i, r := range rows {
		out[i] = dto.ScheduleRun{
			ID: r.ID, ScheduleID: r.ScheduleID, DueAt: time.UnixMilli(r.DueAt).UTC(), FiredAt: optTime(r.FiredAt),
			AckedAt: optTime(r.AckedAt), Status: dto.ScheduleRunStatus(r.Status), Session: r.SessionKey.String, Manual: r.Manual,
		}
	}
	return out, nil
}

func optTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

// record writes the audit entry of a change (HB-11): the actor, the AI, the schedule and its rule, never the
// prompt.
func (s *Service) record(ctx context.Context, q *db.Queries, actor, action, agentID, id string, sp spec) error {
	return s.recordAs(ctx, q, actor, action, agentID, id, sp, nil)
}

// changed tells the scheduler and the live sockets that the AI's schedules changed.
func (s *Service) changed(agentID string) {
	s.Kick()
	if s.d.Bus != nil {
		s.d.Bus.Publish(events.SchedulesChanged{AgentID: agentID})
	}
}

// queued tells the AI's session that a run is pending.
func (s *Service) queued(agentID string) {
	if s.d.Bus != nil {
		s.d.Bus.Publish(events.HeartbeatQueued{AgentID: agentID})
	}
}

// Kick wakes the scheduler, whose next due time may have changed. It never blocks.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// setters reads the intelligences that wrote the prompts.
func (s *Service) setters(ctx context.Context, rows ...db.AgentSchedule) (map[string]db.Intelligence, error) {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SetBy)
	}
	list, err := s.d.Store.R().ListIntelligencesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]db.Intelligence, len(list))
	for _, i := range list {
		out[i.ID] = i
	}
	return out, nil
}

func (s *Service) render(ctx context.Context, row db.AgentSchedule, rl role) (dto.Schedule, error) {
	refs, err := s.setters(ctx, row)
	if err != nil {
		return dto.Schedule{}, err
	}
	return toDTO(row, refs, rl), nil
}

func toDTO(row db.AgentSchedule, refs map[string]db.Intelligence, rl role) dto.Schedule {
	out := dto.Schedule{
		ID: row.ID, AgentID: row.AgentID, Name: row.Name, Prompt: row.Prompt, Cron: row.Cron.String, Timezone: row.Tz,
		CatchUp: dto.ScheduleCatchUp(row.CatchUp), Enabled: row.Enabled, Origin: dto.ScheduleOrigin(row.Origin),
		SetBy: dto.IntelligenceRef{ID: row.SetBy}, LastStatus: dto.ScheduleRunStatus(row.LastStatus.String),
		CanEdit:   rl == roleManager || (rl == roleSelf && row.Origin == string(dto.ScheduleOriginSelf)),
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		LastRunAt: optTime(row.LastRunAt), PausedByManager: row.PausedBy != "",
	}
	if row.EveryMinutes.Valid {
		n := int(row.EveryMinutes.Int64)
		out.EveryMinutes = &n
	}
	if i, ok := refs[row.SetBy]; ok {
		out.SetBy = setterRef(i)
	}
	if row.NextRunAt.Valid {
		t := time.UnixMilli(row.NextRunAt.Int64).UTC()
		if loc, err := time.LoadLocation(row.Tz); err == nil {
			t = t.In(loc)
		}
		out.NextRunAt = &t
	}
	return out
}

// limiter allows at most limit events per key within a sliding window.
type limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	events map[string][]time.Time
}

func newLimiter(limit int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{limit: limit, window: window, now: now, events: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.events[key][:0]
	for _, t := range l.events[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.limit {
		l.events[key] = recent
		return false
	}
	l.events[key] = append(recent, now)
	return true
}

// setterRef renders the intelligence that wrote a prompt, a user or an AI, as a manager ref is rendered (the
// gateway's convert package belongs to the app side).
func setterRef(i db.Intelligence) dto.IntelligenceRef {
	role := dto.IntelligenceRoleUser
	if dto.IntelligenceKind(i.Kind) == dto.IntelligenceKindAI {
		role = dto.IntelligenceRoleAI
	}
	ref := dto.IntelligenceRef{ID: i.ID, Kind: dto.IntelligenceKind(i.Kind), Role: role, DisplayName: i.DisplayName, Handle: i.Handle}
	if i.AvatarSha256 != "" {
		ref.AvatarURL = "/api/v1/intelligences/" + i.ID + "/avatar?v=" + i.AvatarSha256
	}
	return ref
}
