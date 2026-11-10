package schedules

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

func newID() string { return store.NewID() }

// Claim takes the runs to write now to the AI's principal session (HB-04..HB-07) and returns them as frames.
// Per schedule only its newest pending run goes, and none while one of its runs is in flight on this session
// (inflight reports the run ids written to it and not acknowledged): the older pending runs are superseded, so
// runs never pile up (HB-05). A run whose window ended is expired (HB-06). A session whose client is older than
// the first that renders a heartbeat (current false) is sent none: the run is recorded client_outdated (HB-07).
// A run that is claimed is recorded as fired at this session; a run fired on an earlier connection that never
// acknowledged it is claimed again. Trusted is decided now: the setter must manage the AI at delivery time
// (HB-08).
func (s *Service) Claim(ctx context.Context, agentID, sessionKey string, current bool, inflight func(runID string) bool) ([]dto.AgentHeartbeatFrame, error) {
	// Most connections and wake-ups find nothing: a read first, so that they take no write transaction.
	if n, err := s.d.Store.R().CountPendingRunsForAgent(ctx, agentID); err != nil || n == 0 {
		return nil, err
	}
	now := s.d.Now()
	var claimed []db.ListPendingRunsForAgentRow
	settled := false
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		claimed, settled = nil, false
		rows, err := q.ListPendingRunsForAgent(ctx, agentID)
		if err != nil {
			return err
		}
		for start := 0; start < len(rows); {
			end := start + 1
			for end < len(rows) && rows[end].ScheduleID == rows[start].ScheduleID {
				end++
			}
			group := rows[start:end]
			start = end
			busy := false
			for _, r := range group {
				busy = busy || inflight(r.ID)
			}
			if busy {
				continue // HB-05: the next run waits for the acknowledgement of the one in flight
			}
			newest := group[0]
			for _, r := range group[1:] {
				if err := s.settle(ctx, q, r.ID, r.ScheduleID, dto.ScheduleRunSuperseded); err != nil {
					return err
				}
				settled = true
			}
			switch {
			case !now.Before(time.UnixMilli(newest.ExpiresAt)):
				if err := s.settle(ctx, q, newest.ID, newest.ScheduleID, dto.ScheduleRunExpired); err != nil {
					return err
				}
				settled = true
			case !current:
				if err := s.settle(ctx, q, newest.ID, newest.ScheduleID, dto.ScheduleRunClientOutdated); err != nil {
					return err
				}
				settled = true
			default:
				if err := q.FireRun(ctx, db.FireRunParams{
					FiredAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, SessionKey: sql.NullString{String: sessionKey, Valid: sessionKey != ""}, ID: newest.ID,
				}); err != nil {
					return err
				}
				claimed = append(claimed, newest)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if settled {
		s.changedQuiet(agentID)
	}
	out := make([]dto.AgentHeartbeatFrame, 0, len(claimed))
	for _, r := range claimed {
		f := dto.AgentHeartbeatFrame{
			Type: dto.AgentFrameTypeHeartbeat, RunID: r.ID, ScheduleID: r.ScheduleID, Name: r.Name, Prompt: r.Prompt,
			DueAt: time.UnixMilli(r.DueAt).UTC(), Origin: dto.ScheduleOrigin(r.Origin),
		}
		if r.Origin == string(dto.ScheduleOriginManager) {
			i, err := s.d.Store.R().GetIntelligence(ctx, r.SetBy)
			switch {
			case err == nil:
				f.SetBy = dto.HeartbeatSetter{Handle: i.Handle, DisplayName: i.DisplayName}
			case !errors.Is(err, sql.ErrNoRows):
				return nil, err
			}
			if f.Trusted, err = s.d.Authz.Trusts(ctx, r.SetBy, agentID); err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// Ack records that the principal session handed the run to the model (HB-04): the run is delivered. A run that
// is not in flight (acknowledged already, superseded meanwhile, or not the AI's) is ErrNotDelivered.
func (s *Service) Ack(ctx context.Context, agentID, runID string) error {
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		scheduleID, err := q.AckRun(ctx, db.AckRunParams{
			AckedAt: sql.NullInt64{Int64: s.d.Now().UnixMilli(), Valid: true}, ID: runID, AgentID: agentID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotDelivered
		}
		if err != nil {
			return err
		}
		return q.RefreshScheduleLastRun(ctx, scheduleID)
	})
	if err == nil {
		s.changedQuiet(agentID)
	}
	return err
}

// changedQuiet tells the live sockets that the AI's schedules changed (their last runs), without waking the
// scheduler.
func (s *Service) changedQuiet(agentID string) {
	if s.d.Bus != nil {
		s.d.Bus.Publish(events.SchedulesChanged{AgentID: agentID})
	}
}
