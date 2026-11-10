-- Instance settings (RET-01): one row per setting an instance admin set; a setting without a row takes
-- its default from the server's configuration. value is the setting's text form (a number of days for
-- attachment_retention_days).
CREATE TABLE instance_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Attachment retention (RET-03) looks for stored room attachments older than a cutoff; an expired one has
-- status 'expired' and no sha256 any more, so no reference to its blob remains.
CREATE INDEX attachments_stored_created ON attachments (created_at) WHERE status = 'stored';
