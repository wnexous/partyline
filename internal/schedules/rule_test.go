package schedules

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func mustCron(t *testing.T, expr string) *Cron {
	t.Helper()
	c, err := ParseCron(expr)
	require.NoError(t, err, expr)
	return c
}

// HB-01: the standard 5-field syntax, its names and descriptors parse; anything else is refused with the field
// that is wrong.
func TestParseCron(t *testing.T) {
	for _, ok := range []string{
		"0 9 * * *", "*/15 * * * *", "0 9 * * 1-5", "0 9 * * MON-FRI", "30 8 1,15 * *", "0 0 1 JAN *",
		"5/20 * * * *", "0 12 ? * SUN", "0 12 * * 7", "@daily", "@hourly", "@weekly", "@monthly", "@yearly", " 0 9 * * * ",
	} {
		_, err := ParseCron(ok)
		require.NoError(t, err, ok)
	}
	for expr, field := range map[string]string{
		"0 9 * *":       "5 fields",
		"60 * * * *":    "minute",
		"0 24 * * *":    "hour",
		"0 9 0 * *":     "day of month",
		"0 9 * 13 *":    "month",
		"0 9 * * 8":     "day of week",
		"0 9 * * MON-X": "day of week",
		"*/0 * * * *":   "minute",
		"5-1 * * * *":   "minute",
		"0,,5 * * * *":  "minute",
		"@every 1h":     "5 fields",
	} {
		_, err := ParseCron(expr)
		require.Error(t, err, expr)
		require.Contains(t, err.Error(), field, expr)
	}
}

// Scheduler (tests to write first): every 60, anchored at creation, does not drift across three runs that
// were fired late: each next run is anchor + k·60 minutes, never "fired + 60".
func TestEveryIsAnchoredAndDoesNotDrift(t *testing.T) {
	anchor := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	r := Rule{EveryMinutes: 60, Anchor: anchor}
	due := r.Next(anchor)
	require.Equal(t, anchor.Add(time.Hour), due)
	for k := 2; k <= 4; k++ {
		firedLate := due.Add(7 * time.Minute)
		due = r.Next(firedLate)
		require.Equal(t, anchor.Add(time.Duration(k)*time.Hour), due, "run %d", k)
	}
	require.Equal(t, anchor, r.Next(anchor.Add(-time.Minute)), "before the anchor the first run is the anchor")
}

// Scheduler: "0 9 * * *" in America/Sao_Paulo fires at 12:00 UTC (UTC-3, no daylight saving time since 2019).
func TestCronFiresOnTheWallClockOfItsTimezone(t *testing.T) {
	r := Rule{Cron: mustCron(t, "0 9 * * *"), Loc: mustLoc(t, "America/Sao_Paulo")}
	next := r.Next(time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), next.UTC())
	require.Equal(t, time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), r.Next(next).UTC(), "strictly after")
	require.Equal(t, 9, next.Hour(), "in its own zone")
}

// DST: in a zone with daylight saving time a time that does not exist on the day the clocks go forward is
// skipped that day, and a time that happens twice on the day they go back fires once.
func TestCronIsSafeAcrossDaylightSavingTime(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	forward := Rule{Cron: mustCron(t, "30 2 * * *"), Loc: ny} // 2026-03-08 02:00 → 03:00
	next := forward.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, ny))
	require.Equal(t, time.Date(2026, 3, 9, 2, 30, 0, 0, ny), next, "02:30 does not exist on March 8")

	back := Rule{Cron: mustCron(t, "30 1 * * *"), Loc: ny} // 2026-11-01 02:00 → 01:00
	first := back.Next(time.Date(2026, 10, 31, 12, 0, 0, 0, ny))
	require.Equal(t, time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), first.UTC(), "01:30 EDT")
	second := back.Next(first)
	require.Equal(t, time.Date(2026, 11, 2, 6, 30, 0, 0, time.UTC), second.UTC(), "the repeated 01:30 EST does not fire")

	hourly := Rule{Cron: mustCron(t, "0 * * * *"), Loc: ny}
	var runs []time.Time
	for at := time.Date(2026, 11, 1, 0, 30, 0, 0, ny); len(runs) < 4; {
		at = hourly.Next(at)
		runs = append(runs, at)
	}
	for i := 1; i < len(runs); i++ {
		require.Greater(t, runs[i].Sub(runs[i-1]), time.Duration(0))
		require.GreaterOrEqual(t, runs[i].Sub(runs[i-1]), time.Hour, "no run twice in one hour: %v", runs)
	}
}

// Vixie cron: with both day fields restricted, a day matches either; with one of them "*", the other decides.
func TestCronDayFields(t *testing.T) {
	utc := time.UTC
	either := Rule{Cron: mustCron(t, "0 9 13 * FRI"), Loc: utc} // the 13th or a Friday
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, utc)            // a Tuesday
	require.Equal(t, time.Date(2026, 11, 13, 9, 0, 0, 0, utc), either.Next(from), "Friday the 13th")
	require.Equal(t, time.Date(2026, 11, 20, 9, 0, 0, 0, utc), either.Next(time.Date(2026, 11, 13, 9, 0, 0, 0, utc)), "the next Friday")
	weekdays := Rule{Cron: mustCron(t, "0 9 * * 1-5"), Loc: utc}
	require.Equal(t, time.Date(2026, 11, 16, 9, 0, 0, 0, utc), weekdays.Next(time.Date(2026, 11, 13, 10, 0, 0, 0, utc)), "Friday after 9 → Monday")
	never := Rule{Cron: mustCron(t, "0 0 30 2 *"), Loc: utc}
	require.True(t, never.Next(from).IsZero(), "30 February never comes")
}

// HB-03: the shortest gap of a rule, within a day and across midnight.
func TestMinGap(t *testing.T) {
	for expr, want := range map[string]time.Duration{
		"*/15 * * * *":    15 * time.Minute,
		"*/14 * * * *":    4 * time.Minute, // 00:56 → 01:00
		"0 9 * * *":       24 * time.Hour,
		"0,50 0,23 * * *": 10 * time.Minute, // 23:50 → 00:00
		"0 */2 * * *":     2 * time.Hour,
	} {
		require.Equal(t, want, Rule{Cron: mustCron(t, expr), Loc: time.UTC}.MinGap(), expr)
	}
	require.Equal(t, 15*time.Minute, Rule{EveryMinutes: 15}.MinGap())
}

// HB-03: the most runs a rule makes in any 24 hours, whatever the dates: a monthly cron counts the runs of the day
// it fires on, so routines that fire on the same rare day cannot add up past the cap unseen.
func TestMaxPer24h(t *testing.T) {
	require.Equal(t, 24, Rule{EveryMinutes: 60}.maxPer24h())
	require.Equal(t, 48, Rule{EveryMinutes: 30}.maxPer24h())
	require.Equal(t, 32, Rule{EveryMinutes: 45}.maxPer24h(), "ceil(1440/45)")
	require.Equal(t, 1, Rule{EveryMinutes: 7 * 24 * 60}.maxPer24h())
	for expr, want := range map[string]int{
		"0 9 * * *": 1, "0,30 0-11 1 * *": 24, "*/15 9-10 * * 1": 8, "0 9 31 2 *": 1,
	} {
		require.Equal(t, want, Rule{Cron: mustCron(t, expr), Loc: time.UTC}.maxPer24h(), expr)
	}
	monthly := func(id string) db.AgentSchedule {
		return db.AgentSchedule{ID: id, Enabled: true, RuleKind: "cron", Cron: sql.NullString{String: "0,30 0-11 1 * *", Valid: true}, Tz: "UTC"}
	}
	candidate := Rule{Cron: mustCron(t, "0,30 0-11 1 * *"), Loc: time.UTC}
	require.NoError(t, checkDaily([]db.AgentSchedule{monthly("a")}, "new", candidate))
	require.Error(t, checkDaily([]db.AgentSchedule{monthly("a"), monthly("b")}, "new", candidate), "72 on the 1st of the month")
	require.NoError(t, checkDaily([]db.AgentSchedule{monthly("a"), monthly("b")}, "b", candidate), "b is the one being changed")
}

// HB-06: the window of a run is the shorter of the rule's period and 12 hours, and a server down from 08:50 to
// 11:00 makes one catch-up run of a 09:00 schedule, due at 09:00.
func TestLatestDueAndWindow(t *testing.T) {
	sp := mustLoc(t, "America/Sao_Paulo")
	daily := Rule{Cron: mustCron(t, "0 9 * * *"), Loc: sp}
	due := time.Date(2026, 11, 10, 9, 0, 0, 0, sp)
	require.Equal(t, MaxCatchUp, daily.window(due))
	require.Equal(t, due, latestDue(daily, due, time.Date(2026, 11, 10, 11, 0, 0, 0, sp)))

	anchor := time.Date(2026, 11, 10, 8, 0, 0, 0, sp)
	hourly := Rule{EveryMinutes: 60, Anchor: anchor}
	require.Equal(t, time.Hour, hourly.window(anchor.Add(time.Hour)))
	got := latestDue(hourly, anchor.Add(time.Hour), anchor.Add(3*time.Hour+10*time.Minute))
	require.Equal(t, anchor.Add(3*time.Hour), got, "the missed runs collapse into the latest")

	// Two days down: the latest run is found without walking every one.
	quarter := Rule{EveryMinutes: 15, Anchor: anchor}
	now := anchor.Add(48*time.Hour + 5*time.Minute)
	require.Equal(t, anchor.Add(48*time.Hour), latestDue(quarter, anchor.Add(15*time.Minute), now))
}

// A tick that keeps failing backs off from 1 s to MaxSleep and is logged on the first failure and then at most
// once a minute; a tick that works again ends the run.
func TestTickFailuresBackOffAndLogRarely(t *testing.T) {
	var f tickFailures
	logged := 0
	log := func(int) { logged++ }
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	var waits []time.Duration
	for i := range 8 {
		waits = append(waits, f.failed(start.Add(time.Duration(i)*time.Second), log))
	}
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, MaxSleep, MaxSleep, MaxSleep}, waits)
	require.Equal(t, 1, logged, "once within the minute")
	f.failed(start.Add(time.Minute), log)
	require.Equal(t, 2, logged)
	require.Equal(t, 9, f.recovered())
	require.Equal(t, time.Second, f.failed(start.Add(2*time.Minute), log), "a new run starts over")
	require.Equal(t, 3, logged)
}
