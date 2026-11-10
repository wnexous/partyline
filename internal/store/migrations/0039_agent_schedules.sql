-- Scheduled heartbeats (ai-supervisor feature, HB-01..HB-12). Additive: nothing is rebuilt.
--
-- A schedule is a routine of one AI: a prompt delivered to the AI's principal session on a rule, either
-- every N minutes (anchored at anchor_at, so runs never drift) or a 5-field cron evaluated in tz. origin
-- and set_by name who wrote the current prompt: the AI itself (self) or someone who manages it (manager);
-- the AI changes only its own (HB-02). The intelligence columns are plain ids without a foreign key: a
-- setter that was deleted still names the schedule's history, and only makes its prompt untrusted (HB-08).
-- next_run_at is the next due time while the schedule is enabled, NULL while it is not. notified_at and
-- notice_hash are when the AI's nearest manager was last told about the routine the AI set, and the content
-- it was told (HB-09): a routine of the AI is active only once its manager was told what it says, and only an
-- identical repeat within the hour goes untold. paused_by is whoever manages the AI and disabled the routine:
-- only someone who manages the AI enables it again.
CREATE TABLE agent_schedules (
    id            TEXT PRIMARY KEY,
    agent_id      TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    prompt        TEXT NOT NULL,
    rule_kind     TEXT NOT NULL CHECK (rule_kind IN ('every', 'cron')),
    every_minutes INTEGER,
    cron          TEXT,
    tz            TEXT NOT NULL DEFAULT 'America/Sao_Paulo',
    catch_up      TEXT NOT NULL DEFAULT 'once' CHECK (catch_up IN ('once', 'skip')),
    enabled       BOOLEAN NOT NULL DEFAULT 1,
    origin        TEXT NOT NULL CHECK (origin IN ('self', 'manager')),
    set_by        TEXT NOT NULL,
    created_by    TEXT NOT NULL,
    updated_by    TEXT NOT NULL,
    anchor_at     INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    next_run_at   INTEGER,
    last_run_at   INTEGER,
    last_status   TEXT,
    notified_at   INTEGER,
    notice_hash   TEXT NOT NULL DEFAULT '',
    paused_by     TEXT NOT NULL DEFAULT '',
    CHECK ((rule_kind = 'every' AND every_minutes IS NOT NULL AND cron IS NULL)
        OR (rule_kind = 'cron' AND cron IS NOT NULL AND every_minutes IS NULL))
);
CREATE INDEX agent_schedules_agent ON agent_schedules (agent_id, created_at);
CREATE INDEX agent_schedules_due ON agent_schedules (next_run_at) WHERE enabled = 1;

-- One run of a schedule (HB-04..HB-07, HB-11). pending is a run that is due and not delivered yet:
-- waiting for the principal, or written to it and not acknowledged (fired_at set). Every other status is
-- final; cancelled is a run that was pending when its routine was disabled. expires_at is the end of the run's window (the shorter of the rule's period and 12 hours,
-- HB-06): a pending run past it is expired. Runs are kept 30 days (HB-11).
CREATE TABLE agent_schedule_runs (
    id          TEXT PRIMARY KEY,
    schedule_id TEXT NOT NULL REFERENCES agent_schedules (id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    due_at      INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    fired_at    INTEGER,
    acked_at    INTEGER,
    status      TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'superseded', 'missed', 'expired', 'client_outdated', 'cancelled')),
    session_key TEXT,
    manual      BOOLEAN NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);
CREATE INDEX agent_schedule_runs_pending ON agent_schedule_runs (agent_id, due_at) WHERE status = 'pending';
CREATE INDEX agent_schedule_runs_schedule ON agent_schedule_runs (schedule_id, due_at);
CREATE INDEX agent_schedule_runs_created ON agent_schedule_runs (created_at);

-- The DMs the server wrote, as the AI, to tell its nearest manager about a routine the AI set (HB-09):
-- an answer "Pausar" to one of them disables the schedule. Kept as long as the runs.
CREATE TABLE agent_schedule_notices (
    message_id  TEXT PRIMARY KEY,
    schedule_id TEXT NOT NULL REFERENCES agent_schedules (id) ON DELETE CASCADE,
    manager_iid TEXT NOT NULL,
    created_at  INTEGER NOT NULL
);
CREATE INDEX agent_schedule_notices_created ON agent_schedule_notices (created_at);
