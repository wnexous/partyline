package schedules

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
)

// Run is the scheduler: one goroutine that sleeps until the earliest due schedule (at most MaxSleep, or until a
// write kicks it) and fires what is due (Tick), and one that reads the answers to the manager notices (HB-09).
// One server owns a data directory, so nothing else fires the same runs; after a restart the first tick fires
// what came due meanwhile, at most one catch-up run per schedule (HB-06).
func (s *Service) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	if s.d.Bus != nil {
		sub, cancel := s.d.Bus.Subscribe(256)
		defer cancel()
		wg.Go(func() { s.watchAnswers(ctx, sub) })
	}
	defer wg.Wait()
	var fails tickFailures
	for {
		next, err := s.Tick(ctx)
		wait := MaxSleep
		switch {
		case err != nil && ctx.Err() == nil:
			// A failing tick (a locked or full database) backs off, and says so at most once a minute.
			wait = fails.failed(time.Now(), func(n int) { s.d.Logger.Error("schedules: tick failed", "err", err, "failures", n) })
		case err == nil:
			if n := fails.recovered(); n > 0 {
				s.d.Logger.Info("schedules: tick works again", "failures", n)
			}
			if !next.IsZero() {
				wait = min(wait, max(next.Sub(s.d.Now()), 10*time.Millisecond))
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		case <-s.kick:
			timer.Stop()
		}
	}
}

// Tick fires the runs that are due now and expires the pending runs whose window ended before they were written
// to a session (HB-04..HB-06; a run written and not acknowledged waits for its acknowledgement, HB-05). A due
// schedule makes one run, whose due time is the latest of the times it was due since its last run (the runs
// missed while the server was down collapse into it); its next run is the first one after now. A run whose
// window already ended is recorded expired (catch_up once) or missed (skip); a skip run that finds no live
// principal is missed. A new pending run replaces the earlier pending runs of its schedule that were never
// written to a session (HB-05). It returns the earliest next run of any enabled schedule, zero for none.
func (s *Service) Tick(ctx context.Context) (time.Time, error) {
	now := s.d.Now()
	queued, touched := map[string]bool{}, map[string]bool{}
	// The rules are computed before the write transaction, which holds the only writer connection: no search
	// of a run time ever runs while it blocks the server's other writes.
	due, err := s.d.Store.R().ListDueSchedules(ctx, db.ListDueSchedulesParams{Now: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, Limit: runLimit})
	if err != nil {
		return time.Time{}, err
	}
	plans := make([]tickPlan, 0, len(due))
	for _, row := range due {
		plans = append(plans, s.plan(row, now))
	}
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		// The due schedules first: a new run supersedes the unwritten one before it, whose window ends now.
		for _, p := range plans {
			n, err := q.AdvanceDueSchedule(ctx, db.AdvanceDueScheduleParams{NextRunAt: p.next, ID: p.row.ID, Prev: p.row.NextRunAt})
			if err != nil {
				return err
			}
			if n == 0 || p.stop {
				continue // changed since it was read (the next tick sees it as it is now), or a rule that stops
			}
			touched[p.row.AgentID] = true
			if err := s.insertRun(ctx, q, p.row, newID(), p.dueAt, p.expires, p.status, false, now); err != nil {
				return err
			}
			if p.status == dto.ScheduleRunPending {
				queued[p.row.AgentID] = true
			}
		}
		expired, err := q.ListExpiredPendingRuns(ctx, db.ListExpiredPendingRunsParams{Now: now.UnixMilli(), Limit: runLimit})
		if err != nil {
			return err
		}
		for _, r := range expired {
			if err := s.settle(ctx, q, r.ID, r.ScheduleID, dto.ScheduleRunExpired); err != nil {
				return err
			}
			touched[r.AgentID] = true
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	for agentID := range queued {
		s.queued(agentID)
	}
	if s.d.Bus != nil {
		for agentID := range touched {
			s.d.Bus.Publish(events.SchedulesChanged{AgentID: agentID})
		}
	}
	next, err := s.d.Store.R().NextScheduleDue(ctx)
	if err != nil || next == 0 {
		return time.Time{}, err
	}
	return time.UnixMilli(next), nil
}

// tickPlan is what a tick does with one due schedule, decided before the write transaction.
type tickPlan struct {
	row            db.AgentSchedule
	dueAt, expires time.Time
	status         dto.ScheduleRunStatus
	next           sql.NullInt64
	// stop is a schedule whose rule cannot be read or computed any more: it never fires again.
	stop bool
}

// plan decides the run of a due schedule: its due time is the latest of the times it was due since its last
// run, its next run the first one after now.
func (s *Service) plan(row db.AgentSchedule, now time.Time) tickPlan {
	r, err := ruleOf(row)
	var next time.Time
	if err == nil {
		next, err = r.NextChecked(now)
	}
	if err != nil {
		// A timezone gone from the database, or a rule whose next run cannot be computed, never fires again.
		s.d.Logger.Warn("schedules: the rule cannot be computed; the schedule stops", "schedule_id", row.ID, "err", err)
		return tickPlan{row: row, stop: true}
	}
	dueAt := latestDue(r, time.UnixMilli(row.NextRunAt.Int64), now)
	p := tickPlan{row: row, dueAt: dueAt, expires: dueAt.Add(r.window(dueAt)), status: dto.ScheduleRunPending,
		next: nullInt(next.UnixMilli(), !next.IsZero())}
	switch {
	case !now.Before(p.expires) && row.CatchUp == string(dto.ScheduleCatchUpSkip):
		p.status = dto.ScheduleRunMissed
	case !now.Before(p.expires):
		p.status = dto.ScheduleRunExpired
	case row.CatchUp == string(dto.ScheduleCatchUpSkip) && !s.d.PrincipalLive(row.AgentID):
		p.status = dto.ScheduleRunMissed
	}
	return p
}

// latestDue is the latest run of r at or before now, starting from first, the run that came due. Runs older
// than the longest window are not walked one by one: such a run is expired whichever it is.
func latestDue(r Rule, first, now time.Time) time.Time {
	due := first
	if horizon := now.Add(-MaxCatchUp - time.Minute); due.Before(horizon) {
		if c := r.Next(horizon); !c.IsZero() && !c.After(now) {
			due = c
		}
	}
	for range 10_000 {
		n := r.Next(due)
		if n.IsZero() || n.After(now) {
			break
		}
		due = n
	}
	return due
}

// insertRun stores a run of the schedule. A pending run first supersedes the pending runs of the schedule that
// were never written to a session (HB-05).
func (s *Service) insertRun(ctx context.Context, q *db.Queries, row db.AgentSchedule, id string, due, expires time.Time, status dto.ScheduleRunStatus, manual bool, now time.Time) error {
	if status == dto.ScheduleRunPending {
		if _, err := q.SupersedeUnfiredRuns(ctx, row.ID); err != nil {
			return err
		}
	}
	if err := q.InsertScheduleRun(ctx, db.InsertScheduleRunParams{
		ID: id, ScheduleID: row.ID, AgentID: row.AgentID, DueAt: due.UnixMilli(), ExpiresAt: expires.UnixMilli(),
		Status: string(status), Manual: manual, CreatedAt: now.UnixMilli(),
	}); err != nil {
		return err
	}
	return q.RefreshScheduleLastRun(ctx, row.ID)
}

// settle gives a pending run its final status and the schedule its last status.
func (s *Service) settle(ctx context.Context, q *db.Queries, runID, scheduleID string, status dto.ScheduleRunStatus) error {
	if _, err := q.SetRunStatus(ctx, db.SetRunStatusParams{Status: string(status), ID: runID}); err != nil {
		return err
	}
	return q.RefreshScheduleLastRun(ctx, scheduleID)
}

// Purge deletes the runs and the manager notices older than RunRetention (HB-11); the retention job runs it.
func (s *Service) Purge(ctx context.Context) (int64, error) {
	cutoff := s.d.Now().Add(-RunRetention).UnixMilli()
	var n int64
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		runs, err := q.PurgeScheduleRuns(ctx, cutoff)
		if err != nil {
			return err
		}
		notices, err := q.PurgeScheduleNotices(ctx, cutoff)
		n = runs + notices
		return err
	})
	return n, err
}

// ErrNotDelivered is a heartbeat acknowledgement that matched no run written to the session.
var ErrNotDelivered = errors.New("schedules: no such run in flight")

// tickFailures counts the ticks that failed in a row: each waits longer before the next (1 s doubling to
// MaxSleep), and the failure is logged on the first and then at most once a minute.
type tickFailures struct {
	n        int
	loggedAt time.Time
}

// failed records a failure at now, logs it when it is time and returns how long to wait.
func (f *tickFailures) failed(now time.Time, log func(failures int)) time.Duration {
	f.n++
	if f.n == 1 || now.Sub(f.loggedAt) >= time.Minute {
		log(f.n)
		f.loggedAt = now
	}
	return min(time.Second<<min(f.n-1, 5), MaxSleep)
}

// recovered ends a run of failures and returns how long it was.
func (f *tickFailures) recovered() int {
	n := f.n
	*f = tickFailures{}
	return n
}
