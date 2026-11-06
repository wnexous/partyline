-- Voice transcription of platform messages (TR-04): one row per voice file of a platform item that
-- had no text when it was queued, written in the same transaction as the item while a transcription
-- service is configured. The worker takes these rows and those of transcriptions (0035) in one queue,
-- oldest first. item_id is a copy of the file's item, so the platform feed finds the rows of the items
-- it reads; the row goes with its file, and so with its item. A row lives until its item is
-- acknowledged: the transcript is platform text, which no ack outlives (CID-09).
CREATE TABLE platform_transcriptions (
    attachment_id TEXT PRIMARY KEY REFERENCES platform_attachments (id) ON DELETE CASCADE,
    item_id       TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('pending', 'done', 'failed', 'skipped')),
    text          TEXT NOT NULL DEFAULT '',
    attempts      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX platform_transcriptions_item ON platform_transcriptions (item_id);
CREATE INDEX platform_transcriptions_pending ON platform_transcriptions (created_at, attachment_id) WHERE state = 'pending';
