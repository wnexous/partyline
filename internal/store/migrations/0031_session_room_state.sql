-- Session notifications (SN-01, SN-02): where each session of an AI has read up to in each of the AI's
-- rooms, and whether that room notifies it. A session is not a row anywhere: it is a live connection
-- whose identity for persistence is the harness session id it sends (X-Partyline-Harness-Session), else
-- its label while the label lives, and, for the principal, its pinned id or "principal" when nothing is
-- pinned (internal/agents.SessionKey). One row per session and room: read_seq is the sequence number of
-- the newest message the session has read, notify the setting 'on' (every message the AI's delivery mode
-- wants) or 'mentions' (only the messages that address the session). The row is created when the session
-- first joins the room, at its join point - the highest sequence the room had assigned - so a brand-new
-- session starts caught up instead of receiving the whole history, and keeps its read position and its
-- setting across reconnects and restarts of the server. The FKs cascade: the rows of an AI or of a room
-- go with it. Additive: no table that another references is rebuilt.
CREATE TABLE session_room_state (
    agent_id    TEXT    NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    session_key TEXT    NOT NULL CHECK (session_key <> ''),
    room_id     TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    read_seq    INTEGER NOT NULL DEFAULT 0,
    notify      TEXT    NOT NULL CHECK (notify IN ('on', 'mentions')),
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (agent_id, session_key, room_id)
);
-- The rows of one session across the AI's rooms: the unread counts of a session and the per-session list
-- of its notification settings.
CREATE INDEX session_room_state_session ON session_room_state (agent_id, session_key);
