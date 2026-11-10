package schedules

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
)

// notice is a manager notice sent for a write of the AI, recorded with the write (HB-09).
type notice struct {
	hash, manager, messageID string
}

// noticeVerb is what the notice says the AI did.
type noticeVerb string

const (
	noticeCreated   noticeVerb = "Criei a rotina"
	noticeChanged   noticeVerb = "Mudei a rotina"
	noticeReenabled noticeVerb = "Reativei a rotina"
)

// fingerprint is the content of a routine its manager is told: a different one is always told again.
func fingerprint(sp spec) string {
	h := sha256.New()
	for _, part := range []string{sp.name, sp.prompt, strconv.Itoa(sp.every), sp.cron, sp.tz, sp.catchUp} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// notifyFirst tells the AI's nearest manager, in a DM from the AI that the AI cannot edit or delete, about a
// routine the AI is about to make active: its name, rule and prompt, with the quick replies Manter and Pausar
// (HB-09). It runs before the write, and fails closed: an AI nobody manages, or a notice that cannot be
// delivered, refuses the write, so no routine of the AI is ever active without its manager having been told
// what it says. Only an identical repeat of what the manager was told within NoticeEvery goes untold (nil
// notice, no error). prev is the stored routine, nil for a create.
func (s *Service) notifyFirst(ctx context.Context, agentID, scheduleID string, prev *db.AgentSchedule, sp spec, verb noticeVerb, now time.Time) (*notice, error) {
	hash := fingerprint(sp)
	if prev != nil && prev.NoticeHash == hash && prev.NotifiedAt.Valid && now.Sub(time.UnixMilli(prev.NotifiedAt.Int64)) < NoticeEvery {
		return nil, nil
	}
	manager, err := s.nearestManager(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if manager == "" {
		return nil, ErrNoManager
	}
	if s.d.Notifier == nil {
		return nil, ErrNoticeFailed
	}
	msgID, err := s.d.Notifier.NotifyManager(ctx, agentID, manager, noticeText(sp, verb), []string{ReplyKeep, ReplyPause})
	if err != nil {
		s.d.Logger.Warn("schedules: the manager notice was not sent; the write is refused", "schedule_id", scheduleID, "agent_id", agentID, "err", err)
		return nil, ErrNoticeFailed
	}
	return &notice{hash: hash, manager: manager, messageID: msgID}, nil
}

// nearestManager is the first manager of the AI in its home organization, "" for none.
func (s *Service) nearestManager(ctx context.Context, agentID string) (string, error) {
	home, err := s.d.Store.R().GetAgentOrg(ctx, agentID)
	if err != nil || home == "" {
		return "", err
	}
	st, ok, err := s.d.Authz.StandingOf(ctx, home, agentID, 1)
	if err != nil || !ok || len(st.Managers) == 0 {
		return "", err
	}
	return st.Managers[0], nil
}

// recordNotice stores the notice with the write it was sent for.
func recordNotice(ctx context.Context, q *db.Queries, n *notice, scheduleID string, now time.Time) error {
	if n == nil {
		return nil
	}
	if err := q.SetScheduleNotice(ctx, db.SetScheduleNoticeParams{
		NotifiedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, NoticeHash: n.hash, ID: scheduleID,
	}); err != nil {
		return err
	}
	return q.InsertScheduleNotice(ctx, db.InsertScheduleNoticeParams{
		MessageID: n.messageID, ScheduleID: scheduleID, ManagerIid: n.manager, CreatedAt: now.UnixMilli(),
	})
}

// noticeText is the manager notice (HB-09). The people of this instance read Portuguese, as the quick replies
// of the spec.
func noticeText(sp spec, verb noticeVerb) string {
	every := sql.NullInt64{Int64: int64(sp.every), Valid: sp.cron == ""}
	cron := sql.NullString{String: sp.cron, Valid: sp.cron != ""}
	return fmt.Sprintf("%s \"%s\" (%s). Ela fica ativa.\nPrompt: %s\nManter ou pausar?", verb, sp.name, RuleWords(every, cron, sp.tz), sp.prompt)
}

// RuleWords says a rule in Portuguese: "a cada 2 horas", or the cron expression and its timezone.
func RuleWords(every sql.NullInt64, cron sql.NullString, tz string) string {
	if cron.Valid {
		return fmt.Sprintf("cron %s, %s", strings.TrimSpace(cron.String), tz)
	}
	n := every.Int64
	switch {
	case n == 60:
		return "a cada hora"
	case n == 1440:
		return "todo dia"
	case n%1440 == 0:
		return fmt.Sprintf("a cada %d dias", n/1440)
	case n%60 == 0:
		return fmt.Sprintf("a cada %d horas", n/60)
	}
	return fmt.Sprintf("a cada %d minutos", n)
}

// watchAnswers reads the answers to the manager notices until ctx ends. It first looks for the answers no
// applied answer handled yet (written while the server was down), and again after a lagged bus, which may have
// dropped some (HB-09).
func (s *Service) watchAnswers(ctx context.Context, sub <-chan events.Event) {
	s.rescanAnswers(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			switch e := e.(type) {
			case events.MessageCreated:
				if drop := s.dropAnswer.Load(); drop != nil && (*drop)(e.MessageID) {
					continue // a test's stand-in for an event the bus dropped
				}
				if err := s.answer(ctx, e.MessageID); err != nil && ctx.Err() == nil {
					s.d.Logger.Error("schedules: answer to a manager notice failed", "err", err)
				}
			case events.Lagged:
				s.rescanAnswers(ctx)
			}
		}
	}
}

// rescanLimit is how many unhandled answers one rescan applies.
const rescanLimit = 500

// rescanAnswers applies the answers to notices that no applied answer handled yet, oldest first.
func (s *Service) rescanAnswers(ctx context.Context) {
	ids, err := s.d.Store.R().ListUnhandledNoticeAnswers(ctx, rescanLimit)
	if err != nil {
		if ctx.Err() == nil {
			s.d.Logger.Error("schedules: rescan of the manager notices failed", "err", err)
		}
		return
	}
	for _, id := range ids {
		if err := s.answer(ctx, id); err != nil && ctx.Err() == nil {
			s.d.Logger.Error("schedules: answer to a manager notice failed", "err", err)
		}
	}
}

// answer applies an answer to a manager notice (HB-09), once: Pausar, from someone who manages the AI, disables
// the schedule on their behalf, audited as theirs; Manter changes nothing. Either, from someone who manages the
// AI, handles the notice, so a later rescan never applies it again (a routine its manager enabled again stays
// enabled). Anyone else's answer changes nothing.
func (s *Service) answer(ctx context.Context, messageID string) error {
	q := s.d.Store.R()
	ref, err := q.GetMessageAnswerRef(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || ref.ReplyTo == "" || ref.AnswersJson == "" {
		return err
	}
	ans := dto.ParseMessageAnswers(ref.AnswersJson)
	if ans == nil {
		return nil
	}
	notice, err := q.GetScheduleNotice(ctx, ref.ReplyTo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || notice.HandledAt.Valid {
		return err
	}
	row, err := q.GetSchedule(ctx, notice.ScheduleID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	managed, err := s.d.Authz.ManagesAnywhere(ctx, ref.AuthorIid, row.AgentID)
	if err != nil || !managed {
		return err
	}
	now := s.d.Now()
	paused := false
	if err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.MarkNoticeHandled(ctx, db.MarkNoticeHandledParams{HandledAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, MessageID: notice.MessageID})
		if err != nil || n == 0 {
			return err // handled meanwhile
		}
		cur, err := q.GetSchedule(ctx, row.ID)
		if err != nil || ans.QuickReply != ReplyPause || !cur.Enabled {
			return err
		}
		if err := q.SetScheduleEnabled(ctx, db.SetScheduleEnabledParams{
			Enabled: false, UpdatedBy: ref.AuthorIid, UpdatedAt: now.UnixMilli(), PausedBy: ref.AuthorIid, ID: row.ID,
		}); err != nil {
			return err
		}
		if err := cancelPending(ctx, q, row.ID); err != nil {
			return err
		}
		paused = true
		sp := specOf(cur)
		sp.enabled = false
		return s.recordAs(ctx, q, ref.AuthorIid, ActionDisable, row.AgentID, row.ID, sp, map[string]any{"via": "manager_notice"})
	}); err != nil {
		return err
	}
	if paused {
		s.changed(row.AgentID)
	}
	return nil
}

// recordAs is record with extra detail.
func (s *Service) recordAs(ctx context.Context, q *db.Queries, actor, action, agentID, id string, sp spec, extra map[string]any) error {
	if s.d.Audit == nil {
		return nil
	}
	detail := map[string]any{"schedule_id": id, "name": sp.name, "tz": sp.tz, "enabled": sp.enabled}
	if sp.cron != "" {
		detail["cron"] = sp.cron
	} else {
		detail["every_minutes"] = sp.every
	}
	for k, v := range extra {
		detail[k] = v
	}
	return s.d.Audit.Record(ctx, q, audit.Entry{
		ActorID: actor, Action: action, TargetKind: audit.TargetAgent, TargetID: agentID, Detail: detail,
	})
}
