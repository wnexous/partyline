-- Routing and context (routing-context feature).
-- Agent membership settings (MEM-01..05): apply to agent members only.
ALTER TABLE room_members ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE room_members ADD COLUMN sign_outbound BOOLEAN NOT NULL DEFAULT 1;
ALTER TABLE room_members ADD COLUMN delivery TEXT NOT NULL DEFAULT 'all'
    CHECK (delivery IN ('all', 'mentions', 'trusted'));
ALTER TABLE room_members ADD COLUMN keywords_json TEXT NOT NULL DEFAULT '[]';

-- Room instructions for agents (INS-01).
ALTER TABLE rooms ADD COLUMN instructions TEXT NOT NULL DEFAULT '';

-- Catch-all routing (ROUTE-01..05): new chats of the connector matching the scope join the room.
ALTER TABLE connectors ADD COLUMN auto_route_scope TEXT NOT NULL DEFAULT 'off'
    CHECK (auto_route_scope IN ('off', 'dms', 'groups', 'all'));
ALTER TABLE connectors ADD COLUMN auto_route_room_id TEXT REFERENCES rooms (id) ON DELETE SET NULL;

-- A chat removed by hand from a connector's catch-all room is not added back before `until`
-- (unix milliseconds).
CREATE TABLE auto_route_skips (
    connector_id TEXT NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    chat_id      TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    until        INTEGER NOT NULL,
    PRIMARY KEY (connector_id, chat_id)
);
