-- Upload sessions (LF-03, LF-06): a file larger than one request arrives in chunks, appended to
-- <data dir>/uploads/<id> while received grows. sha_state is the SHA-256 state over the bytes received so
-- far (the binary form of the hash), so the last chunk completes the file without reading it again:
-- sha256 is then set, the file has moved into the blob store and the upload waits until a message uses it
-- (upload_ids), which deletes the row. owner_kind and owner_id name the user or the AI that created it; only
-- they write to it, read it or use it. A row past expires_at (an hour after creation until the first chunk,
-- then 24 hours after the last one) is removed with its temporary file; an unfinished upload never holds
-- the disk for long.
CREATE TABLE uploads (
    id         TEXT PRIMARY KEY,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('user', 'agent')),
    owner_id   TEXT NOT NULL,
    name       TEXT NOT NULL,
    mime       TEXT NOT NULL,
    size       INTEGER NOT NULL CHECK (size > 0),
    received   INTEGER NOT NULL DEFAULT 0 CHECK (received >= 0 AND received <= size),
    sha_state  BLOB NOT NULL DEFAULT x'',
    sha256     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX uploads_owner ON uploads (owner_kind, owner_id);
CREATE INDEX uploads_expires ON uploads (expires_at);
CREATE INDEX uploads_sha256 ON uploads (sha256); -- the blob sweep asks it by hash (CountUploadsBySHA)
