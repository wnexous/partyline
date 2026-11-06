-- Voice transcription on the server (TR-01, TR-08): one row per voice attachment of a room message that
-- had no transcript when it was posted, written in the same transaction as the message while a
-- transcription service is configured. The worker takes the pending rows oldest first and leaves each
-- done (with the text), failed (after its attempts) or skipped (over the size cap, written so at once).
-- message_id and room_id are copies of the attachment's message, so the dispatcher and the live hub find
-- the rows of a message without a join; the row goes with its attachment.
CREATE TABLE transcriptions (
    attachment_id TEXT PRIMARY KEY REFERENCES attachments (id) ON DELETE CASCADE,
    message_id    TEXT NOT NULL,
    room_id       TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('pending', 'done', 'failed', 'skipped')),
    text          TEXT NOT NULL DEFAULT '',
    attempts      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX transcriptions_message ON transcriptions (message_id);
CREATE INDEX transcriptions_pending ON transcriptions (created_at, attachment_id) WHERE state = 'pending';
