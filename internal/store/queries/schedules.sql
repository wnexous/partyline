-- name: CreateSchedule :exec
INSERT INTO agent_schedules (id, agent_id, name, prompt, rule_kind, every_minutes, cron, tz, catch_up, enabled,
                             origin, set_by, created_by, updated_by, anchor_at, created_at, updated_at, next_run_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSchedule :one
SELECT * FROM agent_schedules WHERE id = ?;

-- name: GetScheduleForAgent :one
SELECT * FROM agent_schedules WHERE id = ? AND agent_id = ?;

-- name: ListSchedulesByAgent :many
SELECT * FROM agent_schedules WHERE agent_id = ? ORDER BY created_at, id;

-- name: CountSchedulesByAgent :one
SELECT COUNT(*) FROM agent_schedules WHERE agent_id = ?;

-- name: UpdateSchedule :exec
-- HB-01: every field a write may change; the run history and the notice time stay.
UPDATE agent_schedules
SET name = ?, prompt = ?, rule_kind = ?, every_minutes = ?, cron = ?, tz = ?, catch_up = ?, enabled = ?,
    origin = ?, set_by = ?, updated_by = ?, anchor_at = ?, updated_at = ?, next_run_at = ?, paused_by = ?
WHERE id = ?;

-- name: DeleteSchedule :execrows
DELETE FROM agent_schedules WHERE id = ? AND agent_id = ?;

-- name: ListDueSchedules :many
-- The enabled schedules whose next run is due, oldest due first.
SELECT * FROM agent_schedules
WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= sqlc.arg(now)
ORDER BY next_run_at
LIMIT sqlc.arg(limit);

-- name: NextScheduleDue :one
-- The earliest next run of an enabled schedule; 0 when there is none.
SELECT CAST(COALESCE(MIN(next_run_at), 0) AS INTEGER) FROM agent_schedules
WHERE enabled = 1 AND next_run_at IS NOT NULL;

-- name: SetScheduleNextRun :exec
UPDATE agent_schedules SET next_run_at = ? WHERE id = ?;

-- name: SetScheduleNotice :exec
-- HB-09: the manager was told this content of the routine.
UPDATE agent_schedules SET notified_at = ?, notice_hash = ? WHERE id = ?;

-- name: SetScheduleEnabled :exec
UPDATE agent_schedules SET enabled = ?, next_run_at = ?, updated_by = ?, updated_at = ?, paused_by = ? WHERE id = ?;

-- name: RefreshScheduleLastRun :exec
-- The schedule's last run and status are those of its newest run.
UPDATE agent_schedules
SET last_run_at = (SELECT r.due_at FROM agent_schedule_runs r WHERE r.schedule_id = sqlc.arg(id)
                   ORDER BY r.created_at DESC, r.due_at DESC LIMIT 1),
    last_status = (SELECT r.status FROM agent_schedule_runs r WHERE r.schedule_id = sqlc.arg(id)
                   ORDER BY r.created_at DESC, r.due_at DESC LIMIT 1)
WHERE id = sqlc.arg(id);

-- name: InsertScheduleRun :exec
INSERT INTO agent_schedule_runs (id, schedule_id, agent_id, due_at, expires_at, status, manual, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: SupersedeUnfiredRuns :execrows
-- HB-05: a new run replaces the runs of its schedule that are still waiting and were never written to a
-- session.
UPDATE agent_schedule_runs SET status = 'superseded'
WHERE schedule_id = ? AND status = 'pending' AND fired_at IS NULL;

-- name: ListPendingRunsForAgent :many
-- The pending runs of an AI with their schedule, newest first within each schedule. A disabled schedule
-- delivers only a run started by hand ("run now").
SELECT r.id, r.schedule_id, r.due_at, r.expires_at, r.fired_at, r.manual,
       s.name, s.prompt, s.origin, s.set_by
FROM agent_schedule_runs r
JOIN agent_schedules s ON s.id = r.schedule_id
WHERE r.agent_id = ? AND r.status = 'pending' AND (s.enabled = 1 OR r.manual = 1)
ORDER BY r.schedule_id, r.due_at DESC, r.created_at DESC;

-- name: ListExpiredPendingRuns :many
-- Pending runs whose window ended while they waited for the principal (HB-06). A run written to a session
-- waits for its acknowledgement instead (HB-05).
SELECT id, schedule_id, agent_id FROM agent_schedule_runs
WHERE status = 'pending' AND fired_at IS NULL AND expires_at <= sqlc.arg(now)
LIMIT sqlc.arg(limit);

-- name: CountPendingRunsForAgent :one
SELECT COUNT(*) FROM agent_schedule_runs WHERE agent_id = ? AND status = 'pending';

-- name: SetRunStatus :execrows
-- A pending run reaches a final status; a run that already has one keeps it.
UPDATE agent_schedule_runs SET status = sqlc.arg(status) WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: FireRun :exec
UPDATE agent_schedule_runs SET fired_at = ?, session_key = ? WHERE id = ? AND status = 'pending';

-- name: AckRun :one
-- HB-04: the principal acknowledged a run written to it.
UPDATE agent_schedule_runs SET status = 'delivered', acked_at = ?
WHERE id = ? AND agent_id = ? AND status = 'pending' AND fired_at IS NOT NULL
RETURNING schedule_id;

-- name: ListRunsBySchedule :many
SELECT * FROM agent_schedule_runs WHERE schedule_id = ? ORDER BY created_at DESC, due_at DESC LIMIT ?;

-- name: PurgeScheduleRuns :execrows
-- HB-11: runs are kept 30 days. A pending run is kept, whatever its age, until it is final.
DELETE FROM agent_schedule_runs WHERE created_at < ? AND status <> 'pending';

-- name: PurgeScheduleNotices :execrows
DELETE FROM agent_schedule_notices WHERE created_at < ?;

-- name: InsertScheduleNotice :exec
INSERT INTO agent_schedule_notices (message_id, schedule_id, manager_iid, created_at) VALUES (?, ?, ?, ?);

-- name: GetScheduleNotice :one
SELECT * FROM agent_schedule_notices WHERE message_id = ?;

-- name: GetMessageAnswerRef :one
-- HB-09: what an answer to a manager notice needs: the question it answers, its author and the answer.
SELECT reply_to, author_iid, answers_json FROM messages WHERE id = ?;

-- name: AdvanceDueSchedule :execrows
-- The scheduler takes a due schedule: its next run moves on only if nobody changed it since it was read.
UPDATE agent_schedules SET next_run_at = sqlc.arg(next_run_at)
WHERE id = sqlc.arg(id) AND enabled = 1 AND next_run_at = sqlc.arg(prev);

-- name: CancelPendingRuns :execrows
-- A routine that is disabled cancels its pending runs: none of them is delivered afterwards.
UPDATE agent_schedule_runs SET status = 'cancelled' WHERE schedule_id = ? AND status = 'pending';

-- name: MarkNoticeHandled :execrows
-- HB-09: an answer to the notice was applied; it is applied once.
UPDATE agent_schedule_notices SET handled_at = ? WHERE message_id = ? AND handled_at IS NULL;

-- name: ListUnhandledNoticeAnswers :many
-- The structured answers to notices that no applied answer handled yet, oldest first: what a lagged bus may have
-- dropped.
SELECT m.id FROM agent_schedule_notices n
JOIN messages m ON m.reply_to = n.message_id AND m.answers_json <> ''
WHERE n.handled_at IS NULL
ORDER BY m.created_at
LIMIT ?;
