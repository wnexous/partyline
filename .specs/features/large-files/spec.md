# Large files (up to gigabytes)

Status: Specify + Design written 2026-11-09. Requirement ids LF-xx. Owner request (2026-11-09):
"tira o limite ou põe uns 5 GB" after a 29 MB raw photo and a 66 MB video were refused; v0.16.2
raised the single-request limit to 95 MB meanwhile, and the owner asked to "work on the update to
support gigabytes".

## Problem
One limit (`PARTYLINE_MAX_UPLOAD_MB`, 95 MB) bounds every file, and three things stop a larger one:
1. The public address is behind a Cloudflare free plan, which refuses any request body over
   100 MB whatever the server allows. A file larger than that cannot arrive in one request, from
   the web app or from an AI's `send_file`.
2. Some paths hold the whole file in memory: WhatsApp inbound media (the Evolution API answers with
   base64 inside JSON), an AI's file sent through a connection (`connectors.SendFile.Data []byte`,
   `io.ReadAll` in the agent handler), WhatsApp outbound media (base64 inside the JSON request), and
   the CLI's `send_file` (it builds the multipart body in a buffer). A file of gigabytes would exhaust
   the 6 GB VM.
3. Disk: every stored file stays in the blob store; gigabyte files need a disk budget.

## Requirements
1. The web app SHALL send a file larger than the single-request size in chunks of at most 50 MB,
   showing its progress, and a failed chunk SHALL be retried without starting over. (LF-01)
2. An AI's `send_file` (room or connection) SHALL send a large file in chunks the same way. (LF-02)
3. The server SHALL accept a file up to `PARTYLINE_MAX_FILE_MB` (default 5120) through chunks, writing
   it to disk as it arrives, never holding more than one chunk in memory. (LF-03)
4. WhatsApp inbound media and outbound files SHALL be decoded and encoded as streams, so their memory
   use does not grow with the file. (LF-04)
5. A file sent through a connection SHALL respect that platform's own limit (WhatsApp documents
   2 GB, Telegram bots 50 MB, Discord 25 MB by default) and fail with a clear message above it. (LF-05)
6. An unfinished upload SHALL be removed after 24 hours. (LF-06)
7. WHEN the data disk has less free space than the file plus a reserve (default 2 GB) THEN the server
   SHALL refuse the upload with a clear message instead of filling the disk. (LF-07)
8. Downloads (web and `download_attachment`) SHALL stream from disk with range support, so a large
   video can be played and a broken download resumed. (LF-08)

## Design
- **Upload sessions:** `POST /api/v1/uploads` {name, mime, size} → {id, chunk_size}; `PUT
  /api/v1/uploads/{id}?offset=N` with one chunk (≤ 50 MB, under the proxy cap); `GET
  /api/v1/uploads/{id}` → {received} to resume; the last chunk completes it: the server hashes as it
  writes (sha256 over the stream), moves the temp file into the blob store and returns a file ref.
  Posting a message (room or connection) accepts `upload_ids` next to multipart files. Temp files live
  in `<data>/uploads/`, a table `uploads(id, owner, name, mime, size, received, sha_state?, expires_at)`.
  Same auth as today (user session or agent token); an upload belongs to its creator.
- **Limits:** `PARTYLINE_MAX_UPLOAD_MB` stays the single-request cap (95); `PARTYLINE_MAX_FILE_MB` is
  the size of one file through chunks (default 5120). Disk check before accepting a session (LF-07).
- **WhatsApp inbound (LF-04):** read `/message/downloadmedia` with a streaming scanner that finds the
  `base64` string and pipes it through `base64.NewDecoder` into `attachments.Store.Put`; the JSON
  around it stays bounded. Inline media in WebSocket frames stay capped as today (small media).
- **Outbound (LF-04, LF-05):** `SendFile` carries a blob ref (path + size) instead of bytes; the WhatsApp
  sender writes its JSON body through an `io.Pipe` with a `base64.NewEncoder`; Telegram and Discord
  stream multipart. Each connector declares its max file size and the send fails before upload when
  over it.
- **CLI (LF-02):** `send_file` uses the upload API for files over the single-request size, streaming
  from the local file; small files keep the multipart path.
- **Web (LF-01):** the composer uploads through the API with a progress bar per file; the message posts
  when every upload completed. Uses `fetch` with a chunk `Blob.slice`, three retries per chunk.
- **Downloads (LF-08):** `http.ServeContent` on the blob file (range, If-Range) for both the web and
  the agent download route.
- **Transcription:** unchanged; its 25 MB service cap still skips longer audio.

## Phases
1. Server upload sessions + disk check + downloads with ranges (LF-03, LF-06, LF-07, LF-08).
2. Web chunked upload with progress (LF-01).
3. CLI `send_file` chunked (LF-02).
4. Streaming WhatsApp in/out, `SendFile` by ref, per-platform limits (LF-04, LF-05).

## Open questions (owner)
- Disk space on the prod VM and how long files should stay (today they stay forever).

## Out of scope
- Moving files to object storage (S3/R2); can come later behind the same blob interface.
- Transcoding video.
