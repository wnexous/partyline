-- Reactions to the messages of a room (messenger polish, RCT-03, RCT-05, RCT-07): one row for each
-- intelligence, message and emoji. The id is the order they reacted in, so a message lists its
-- emoji in the order each was first used and, for each emoji, who used it in order. A reaction
-- goes with its message (the room's deletion and retention take messages away) and with the
-- intelligence that made it (a user, an AI or a person; all share the intelligences table).
CREATE TABLE message_reactions (
    id              INTEGER PRIMARY KEY,
    message_id      TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    intelligence_id TEXT NOT NULL REFERENCES intelligences (id) ON DELETE CASCADE,
    emoji           TEXT NOT NULL,
    created_at      INTEGER NOT NULL,
    UNIQUE (message_id, intelligence_id, emoji)
);
CREATE INDEX message_reactions_message ON message_reactions (message_id, id);
CREATE INDEX message_reactions_intelligence ON message_reactions (intelligence_id);
