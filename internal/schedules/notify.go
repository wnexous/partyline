package schedules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
)

// noticeManager tells the AI's nearest manager, in a DM written as the AI, about a schedule the AI set or
// changed (HB-09): its name, rule and prompt, with the quick replies Manter and Pausar. At most one notice per
// schedule an hour; an AI nobody manages tells nobody. Best effort: a failure is logged, the write stands.
func (s *Service) noticeManager(ctx context.Context, row db.AgentSchedule) {
	if s.d.Notifier == nil {
		return
	}
	q := s.d.Store.R()
	home, err := q.GetAgentOrg(ctx, row.AgentID)
	if err != nil || home == "" {
		return
	}
	st, ok, err := s.d.Authz.StandingOf(ctx, home, row.AgentID, 1)
	if err != nil || !ok || len(st.Managers) == 0 {
		return
	}
	manager := st.Managers[0]
	now := s.d.Now()
	var claimed int64
	if err := s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		claimed, err = q.ClaimScheduleNotice(ctx, db.ClaimScheduleNoticeParams{
			Now: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: row.ID,
			Before: sql.NullInt64{Int64: now.Add(-NoticeEvery).UnixMilli(), Valid: true},
		})
		return err
	}); err != nil || claimed == 0 {
		return
	}
	text := noticeText(row, row.CreatedAt == row.UpdatedAt)
	msgID, err := s.d.Notifier.NotifyManager(ctx, row.AgentID, manager, text, []string{ReplyKeep, ReplyPause})
	if err != nil {
		s.d.Logger.Warn("schedules: the manager notice was not sent", "schedule_id", row.ID, "agent_id", row.AgentID, "err", err)
		return
	}
	if err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.InsertScheduleNotice(ctx, db.InsertScheduleNoticeParams{
			MessageID: msgID, ScheduleID: row.ID, ManagerIid: manager, CreatedAt: now.UnixMilli(),
		})
	}); err != nil {
		s.d.Logger.Error("schedules: the manager notice was not recorded", "schedule_id", row.ID, "err", err)
	}
}

// noticeText is the manager notice (HB-09). The people of this instance read Portuguese, as the quick replies
// of the spec.
func noticeText(row db.AgentSchedule, created bool) string {
	verb := "Mudei a rotina"
	if created {
		verb = "Criei a rotina"
	}
	return fmt.Sprintf("%s \"%s\" (%s). Ela já está ativa.\nPrompt: %s\nManter ou pausar?", verb, row.Name, RuleWords(row.EveryMinutes, row.Cron, row.Tz), row.Prompt)
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

// watchAnswers reads the answers to the manager notices until ctx ends.
func (s *Service) watchAnswers(ctx context.Context, sub <-chan events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			if m, isMessage := e.(events.MessageCreated); isMessage {
				if err := s.answer(ctx, m.MessageID); err != nil && ctx.Err() == nil {
					s.d.Logger.Error("schedules: answer to a manager notice failed", "err", err)
				}
			}
		}
	}
}

// answer applies an answer to a manager notice (HB-09): Pausar, from someone who manages the AI, disables the
// schedule on their behalf, audited as theirs; Manter, any other answer and anyone else's change nothing.
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
	if ans == nil || ans.QuickReply != ReplyPause {
		return nil
	}
	notice, err := q.GetScheduleNotice(ctx, ref.ReplyTo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	row, err := q.GetSchedule(ctx, notice.ScheduleID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || !row.Enabled {
		return err
	}
	managed, err := s.d.Authz.ManagesAnywhere(ctx, ref.AuthorIid, row.AgentID)
	if err != nil || !managed {
		return err
	}
	now := s.d.Now()
	if err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.SetScheduleEnabled(ctx, db.SetScheduleEnabledParams{
			Enabled: false, UpdatedBy: ref.AuthorIid, UpdatedAt: now.UnixMilli(), ID: row.ID,
		}); err != nil {
			return err
		}
		sp := specOf(row)
		sp.enabled = false
		return s.recordAs(ctx, q, ref.AuthorIid, ActionDisable, row.AgentID, row.ID, sp, map[string]any{"via": "manager_notice"})
	}); err != nil {
		return err
	}
	s.changed(row.AgentID)
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
