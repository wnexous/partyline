package dto

import "time"

// ScheduleOrigin tells who wrote a schedule's prompt (HB-02, HB-08): the AI itself or someone who manages it.
type ScheduleOrigin string

// Schedule origins.
const (
	ScheduleOriginSelf    ScheduleOrigin = "self"
	ScheduleOriginManager ScheduleOrigin = "manager"
)

// ScheduleCatchUp is what becomes of a run that is due while the AI's principal session is not live (HB-06):
// once waits for it, within the run's window; skip records it missed.
type ScheduleCatchUp string

// Catch-up policies.
const (
	ScheduleCatchUpOnce ScheduleCatchUp = "once"
	ScheduleCatchUpSkip ScheduleCatchUp = "skip"
)

// ScheduleRunStatus is the state of one run (HB-11). pending is a run that is due and not delivered yet; every
// other status is final.
type ScheduleRunStatus string

// Run statuses.
const (
	ScheduleRunPending        ScheduleRunStatus = "pending"
	ScheduleRunDelivered      ScheduleRunStatus = "delivered"
	ScheduleRunSuperseded     ScheduleRunStatus = "superseded"
	ScheduleRunMissed         ScheduleRunStatus = "missed"
	ScheduleRunExpired        ScheduleRunStatus = "expired"
	ScheduleRunClientOutdated ScheduleRunStatus = "client_outdated"
	// ScheduleRunCancelled is a run that was pending when its routine was disabled.
	ScheduleRunCancelled ScheduleRunStatus = "cancelled"
)

// The limits of a schedule (HB-01, HB-03). The web app imports them through the generated types.

// MaxScheduleName is the longest schedule name in characters.
const MaxScheduleName = 60

// MaxSchedulePrompt is the longest schedule prompt in characters.
const MaxSchedulePrompt = 4000

// MinScheduleEveryMinutes is the shortest time between two runs of one rule, in minutes (HB-03).
const MinScheduleEveryMinutes = 15

// MaxScheduleEveryMinutes is the longest interval of an every_minutes rule: a year.
const MaxScheduleEveryMinutes = 525600

// MaxScheduleRunsPerDay is the most runs the enabled schedules of one AI may make in any 24 hours (HB-03).
const MaxScheduleRunsPerDay = 48

// MaxSchedulesPerAgent is the most schedules one AI has (HB-03).
const MaxSchedulesPerAgent = 20

// DefaultScheduleTimezone is the timezone of a schedule that names none (HB-01).
const DefaultScheduleTimezone = "America/Sao_Paulo"

// Schedule is one routine of an AI (HB-01): a prompt delivered to the AI's principal session on a rule, every
// EveryMinutes minutes or on Cron, in Timezone. Exactly one of EveryMinutes and Cron is set. NextRunAt is the
// next run in the schedule's timezone (its offset is the zone's then); absent while the schedule is disabled
// or its rule never fires again. SetBy is who wrote the prompt and Origin says whether that is the AI itself.
// CanEdit tells whether the caller may change it: whoever manages the AI may change any schedule of it, the AI
// only its own (HB-02).
type Schedule struct {
	ID           string            `json:"id"`
	AgentID      string            `json:"agent_id"`
	Name         string            `json:"name"`
	Prompt       string            `json:"prompt"`
	EveryMinutes *int              `json:"every_minutes,omitempty"`
	Cron         string            `json:"cron,omitempty"`
	Timezone     string            `json:"tz"`
	CatchUp      ScheduleCatchUp   `json:"catch_up"`
	Enabled      bool              `json:"enabled"`
	Origin       ScheduleOrigin    `json:"origin"`
	SetBy        IntelligenceRef   `json:"set_by"`
	NextRunAt    *time.Time        `json:"next_run_at,omitempty"`
	LastRunAt    *time.Time        `json:"last_run_at,omitempty"`
	LastStatus   ScheduleRunStatus `json:"last_status,omitempty"`
	CanEdit      bool              `json:"can_edit"`
	// PausedByManager tells that someone who manages the AI disabled it: only they enable it again.
	PausedByManager bool      `json:"paused_by_manager"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ScheduleList is GET /agents/{id}/schedules and GET /agent/schedules. Count is how many schedules the AI has.
// Only the AI and whoever manages it read them (HB-12): for anyone else who sees the AI, Items is empty and
// Count still tells that it has routines. CanManage tells whether the caller manages the AI.
type ScheduleList struct {
	Items     []Schedule `json:"items"`
	Count     int        `json:"count"`
	CanManage bool       `json:"can_manage"`
}

// ScheduleRequest is the body of a schedule create (POST, every field but the optional ones required) or
// update (PATCH, only the fields given change). Give exactly one of EveryMinutes (at least 15) and Cron (a
// standard 5-field expression); Timezone is an IANA zone, America/Sao_Paulo when left out; CatchUp defaults to
// once and Enabled to true.
type ScheduleRequest struct {
	Name         *string `json:"name,omitempty"`
	Prompt       *string `json:"prompt,omitempty"`
	EveryMinutes *int    `json:"every_minutes,omitempty"`
	Cron         *string `json:"cron,omitempty"`
	Timezone     *string `json:"tz,omitempty"`
	CatchUp      *string `json:"catch_up,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

// ScheduleRun is one run of a schedule as GET /agents/{id}/schedules/{sid}/runs lists it, newest first (HB-11).
// FiredAt is when it was written to the principal session, AckedAt when the session acknowledged it, Session the
// key of that session (its harness session id, or "principal" for one without); Manual marks a run started with
// "run now".
type ScheduleRun struct {
	ID         string            `json:"id"`
	ScheduleID string            `json:"schedule_id"`
	DueAt      time.Time         `json:"due_at"`
	FiredAt    *time.Time        `json:"fired_at,omitempty"`
	AckedAt    *time.Time        `json:"acked_at,omitempty"`
	Status     ScheduleRunStatus `json:"status"`
	Session    string            `json:"session,omitempty"`
	Manual     bool              `json:"manual"`
}

// HeartbeatSetter is who wrote a heartbeat's prompt.
type HeartbeatSetter struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
}

// AgentHeartbeatFrame delivers one run of a schedule to the principal session (HB-04): the session hands
// Prompt to the model as a turn and acknowledges the frame with an AgentAckFrame carrying RunID. Origin tells
// whether the AI set the routine itself or someone who manages it did (SetBy), and Trusted is true only when
// the setter manages the AI in its home organization at delivery time (HB-08).
type AgentHeartbeatFrame struct {
	Type       AgentFrameType  `json:"type" tstype:"typeof AgentFrameTypeHeartbeat"`
	RunID      string          `json:"run_id"`
	ScheduleID string          `json:"schedule_id"`
	Name       string          `json:"name"`
	Prompt     string          `json:"prompt"`
	DueAt      time.Time       `json:"due_at"`
	Origin     ScheduleOrigin  `json:"origin"`
	SetBy      HeartbeatSetter `json:"set_by"`
	Trusted    bool            `json:"trusted"`
}

// AgentSchedulesData is the live event agent.schedules: the schedules of the AI changed. It carries no schedule:
// whoever may read them asks again (HB-12).
type AgentSchedulesData struct {
	AgentID string `json:"agent_id"`
}
