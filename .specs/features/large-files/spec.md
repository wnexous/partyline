# Large files (up to gigabytes)

Status: Specify + Design written 2026-11-09; phases 1-4 built (phase 4 notes below). Requirement ids LF-xx. Owner request (2026-11-09):
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
- **Web as built (phase 2):** there is no config endpoint, so the web app learns the limits from the
  session's answer (`chunk_size`, which is the single-request size when below 50 MB, and `max_size`).
  Files that weigh up to 8 MB together keep the one-request multipart post (pastes, photos, voice);
  above that every file of the message goes through a session, so the attachments keep their order,
  and a multipart post answered 413 is sent again through sessions. Chunks are at most 8 MB (or
  `chunk_size`) so the progress moves every few seconds over fetch; a chunk is retried three times
  (1, 2, 4 s) after asking the server where it is, a 409 `upload_offset` resumes at
  `details.received`, and a send that failed goes on with the sessions it started. A card's button
  cancels (aborts the send, takes that file out). Code: `web/src/shared/api/uploads.ts`,
  `entities/message` `usePostMessage`, `features/send-message` transfer store. A session nobody will
  resume is deleted (`DELETE /uploads/{id}`, best effort: 409 `upload_busy` retried, 404 taken as
  done) when its upload is cancelled, its file is taken out of the draft, its conversation is
  deleted (`room.deleted`) or the page is unloaded (keepalive request on `pagehide`); a failed send
  that stays in the draft keeps its sessions to resume.
- **Phase 4 as built (LF-04, LF-05):**
  - `connectors.SendFile` is `{Name, Mime, Size, Open}`: the file is read by reference, as a stream,
    and `Open` may be called again (a request the transport resends reopens it through `GetBody`).
    The agent connection send writes its multipart file part to a temporary file under
    `<data>/uploads` (`.send-*`, removed when the send is over; one a crash leaves is an orphan the
    next start removes), or takes one finished upload (`upload_ids`, also in the multipart message
    part), read from the blob store. The upload is used up once the send was attempted, sent or
    failed, since a connection's file is sent once; a send refused before it went keeps it. The
    attempt of a file send gets a second per MB on top of its 90 s (`connectors.FileAttemptTimeout`).
  - Platform limits: `Kind.MaxFileBytes` (or `Kind.FileLimit(cfg)` when the limit depends on the
    config): WhatsApp `PARTYLINE_WHATSAPP_MAX_FILE_MB` (512 MB by default, at most 2 GB) for every
    type of media, also received (a larger media is recorded `too_large` without asking evolution-go,
    and a download is cut at it whatever it declared), Telegram 50 MB through the public Bot API and
    2000 MB through a self-hosted one, Discord 25 MB, webhook none (it takes no files). `Sends.plan`
    refuses a larger file with `*connectors.FileTooLargeError` → 413 `file_too_large` with
    `details.max_size` and `details.platform`. `POST /agent/uploads` takes `connection`, checked with
    `Sends.CheckFile` (operator, files, limit) before any chunk.
  - WhatsApp inbound: `/message/downloadmedia` goes through a JSON scanner (`whatsapp/media.go`) that
    finds the string at `data` or `data.base64`, unescapes it as it reads (`\/`, `\uXXXX` of ASCII),
    strips a `data:` URL prefix and decodes it into `attachments.Store.Put`; the JSON before it is
    bounded by `MaxJSONResponse`, what follows it is not read, and the client's cap is the base64 of
    `PARTYLINE_MAX_FILE_MB` plus 4 MiB. Inline media of WebSocket frames stay bounded by the
    single-request limit (`Deps.MaxMediaBytes`), media downloads by `Deps.MaxFileBytes` (Telegram and
    Discord too; the pipeline's `MaxFileBytes`). Frames are handled in order by one goroutine while the
    socket keeps being read (`whatsapp/inbox.go`, at most 32 MB of frames waiting), so pongs are read
    during a long download; the frames read before the socket failed are handled before `Run` returns.
  - WhatsApp outbound: `/send/media` keeps its JSON shape; the body is the fields, then the file's
    base64 encoded by a pull reader (no goroutine, no pipe), with the exact `Content-Length`.
    Telegram streams its multipart body the same way (prefix, file, closing boundary). Discord keeps
    discordgo, which builds the body in memory, bounded by its 25 MB limit.
  - Disk (LF-07): an inbound media whose declared size would leave less than
    `PARTYLINE_DISK_RESERVE_MB` free is not downloaded and is recorded `unavailable`. The declared
    size is the sender's word, so a download also looks at the free space every 64 MB it writes and
    stops (`unavailable`, no blob) when the disk would keep less than the reserve; concurrent
    downloads see each other's bytes there. Every download is capped at the largest file whatever it
    declared.
  - Time: downloads have no client timeout; a stall watch (`connectors.WatchStall`) fails one whose
    first byte takes longer than a minute (WhatsApp: plus a second per MB of declared size, as
    evolution-go fetches the file first) or that then brings nothing for a minute. The CLI waits for
    a connection's send for the server's bound plus a tool call.
  - Memory, measured by the tests (`runtime.MemStats.TotalAlloc`, the fake servers included): a 6 MB
    WhatsApp download allocates about 0.3 MB, an 8 MB WhatsApp send about 0.3 MB, an 8 MB Telegram
    send about 0.25 MB.
- **Known limits (phase 4):**
  - evolution-go itself decodes the base64 of `/send/media` and encodes the one of
    `/message/downloadmedia` in its own memory: a file of gigabytes costs evolution-go several times
    its size, wherever it runs. Partyline's side is bounded; evolution-go's is bounded only by
    `PARTYLINE_WHATSAPP_MAX_FILE_MB` (512 MB by default), which an operator sharing the host keeps low.
  - A send through a connection answers when the platform took the file. A proxy that times out an
    answer (Cloudflare: 100 s) answers the AI with an error while the server goes on sending; the file
    may arrive although the tool said it failed.
  - WhatsApp's limit is applied to every type; a platform that refuses a smaller video or image says so
    with its reason. Discord's 25 MB is the limit of a server without boosts (servers with boosts
    take more, and Discord has lowered the default before): a file Discord refuses fails with its
    reason.
  - A message whose media takes minutes to download holds the messages after it of the same
    connection until it is done (they are handled in order).
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
