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
	for {
		next, err := s.Tick(ctx)
		wait := MaxSleep
		switch {
		case err != nil && ctx.Err() == nil:
			s.d.Logger.Error("schedules: tick failed", "err", err)
			wait = time.Second
		case !next.IsZero():
			wait = min(wait, max(next.Sub(s.d.Now()), 10*time.Millisecond))
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
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		// The due schedules first: a new run supersedes the unwritten one before it, whose window ends now.
		due, err := q.ListDueSchedules(ctx, db.ListDueSchedulesParams{Now: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, Limit: runLimit})
		if err != nil {
			return err
		}
		for _, row := range due {
			touched[row.AgentID] = true
			r, err := ruleOf(row)
			if err != nil {
				// A rule this build cannot read (a timezone gone from the database) never fires.
				s.d.Logger.Warn("schedules: unreadable rule; the schedule stops", "schedule_id", row.ID, "err", err)
				if err := q.SetScheduleNextRun(ctx, db.SetScheduleNextRunParams{ID: row.ID}); err != nil {
					return err
				}
				continue
			}
			dueAt := latestDue(r, time.UnixMilli(row.NextRunAt.Int64), now)
			expires := dueAt.Add(r.window(dueAt))
			status := dto.ScheduleRunPending
			switch {
			case !now.Before(expires) && row.CatchUp == string(dto.ScheduleCatchUpSkip):
				status = dto.ScheduleRunMissed
			case !now.Before(expires):
				status = dto.ScheduleRunExpired
			case row.CatchUp == string(dto.ScheduleCatchUpSkip) && !s.d.PrincipalLive(row.AgentID):
				status = dto.ScheduleRunMissed
			}
			if err := s.insertRun(ctx, q, row, newID(), dueAt, expires, status, false, now); err != nil {
				return err
			}
			if err := q.SetScheduleNextRun(ctx, db.SetScheduleNextRunParams{NextRunAt: nextRun(r, true, now), ID: row.ID}); err != nil {
				return err
			}
			if status == dto.ScheduleRunPending {
				queued[row.AgentID] = true
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
