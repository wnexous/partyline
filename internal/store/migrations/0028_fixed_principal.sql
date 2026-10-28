-- A fixed principal session (ai-sessions, SESS-51..SESS-62).
-- principal_session is the harness session id (Claude Code's CLAUDE_CODE_SESSION_ID, pi's session id)
-- of the one session that holds the principal role ("") of the AI. It is stored, not derived from who
-- connects first: a parallel session never takes the role by itself, only an explicit action changes it
-- (the --principal launch flag, or "make principal" in the web). '' means no session is pinned (an AI
-- created before this migration until its first session with an id connects, or one whose harness has no
-- session id): the folder's first live session acts as the principal, as before.
ALTER TABLE agents ADD COLUMN principal_session TEXT NOT NULL DEFAULT '';

-- The label a parallel session had, by the harness session id, so that it survives a restart of the
-- server (SESS-54): a session that connects again with the same id gets the same label back while it is
-- free. Presence itself stays in memory. updated_at lets old rows be dropped.
CREATE TABLE agent_session_labels (
    agent_id        TEXT    NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    harness_session TEXT    NOT NULL,
    label           TEXT    NOT NULL CHECK (label <> ''),
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (agent_id, harness_session)
);
