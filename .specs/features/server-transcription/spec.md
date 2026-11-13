# Voice transcription on the server

Status: Specify + Design written 2026-11-05; the owner chose the proposals ("ok", 2026-11-05).
Built in two phases: rooms first (TR-01..03, TR-05..08, v0.15.0), platform connections next (TR-04). Requirement ids TR-xx. Owner request (voice note, 2026-11-05): "put the transcription
engine inside Partyline itself, a transcription server in the Docker Compose, instead of the phone or
the browser transcribing".

## Problem
A voice message carries a transcript only when the sender's browser made one with the Web Speech API
(VOI-09). Firefox has no such API, and Android's beeped and cut the text, so it was turned off there
in v0.14.10. Without a transcript every AI in the conversation transcribes the same audio itself,
each with its own tool, or cannot read it at all. Voice messages from WhatsApp and Telegram
connections arrive with no transcript either.

## Goal
The server transcribes every voice message once, with a Whisper model that runs next to it, so that
every browser, phone and connection gets the same text, and the audio never leaves the instance.

## Requirements
1. WHERE a transcription service is configured (`PARTYLINE_TRANSCRIBE_URL`) WHEN a voice message is
   posted in a room with no transcript THEN the server SHALL transcribe its audio and store the text as
   the message's transcript. (TR-01)
2. The web app SHALL show the message at once with "Transcribing…" under the player, and the text
   when it arrives, live, for every viewer. (TR-02)
3. WHEN the message is for AIs THEN the server SHALL hold its delivery to them until the transcript is
   ready or 60 seconds have passed, and deliver it once, with the transcript; past the
   limit it SHALL deliver the audio alone, as today. (TR-03)
4. WHEN a voice message arrives from a platform connection (WhatsApp, Telegram, Discord, webhook)
   THEN it SHALL be transcribed the same way before it is handed to the AI that operates the
   connection: that AI gets it once, with the transcript, or with the audio alone past 60 seconds,
   and its platform messages keep their order. (TR-04)
5. A transcript the sender's browser made SHALL be kept and not replaced. (TR-05)
6. WHERE no service is configured, or it fails or times out, THEN the message SHALL behave exactly
   as today: the audio alone, no error shown to anyone; failures are logged without the audio or the
   text. (TR-06)
7. The language SHALL be detected by the model; the sender's app language is the hint. (TR-07)
8. Transcription SHALL run one job at a time per instance, oldest first, with a cap of 10 minutes of
   audio per message; longer audio is sent without a transcript. (TR-08)
9. The message text SHALL be read as a browser transcript only for a recording made in the composer
   (named `voice-message-<date>-<time>.<ext>`). WHEN any other audio file is posted in a room with
   text THEN the text SHALL show as its caption, as ordinary message text, and the server SHALL
   transcribe the file as if it came without text. (TR-09; owner report 2026-11-11: an audio file
   sent from the computer with a caption showed the caption as its transcript.)
10. WHERE a transcription service is configured the web app SHALL NOT run the browser's live
   transcript: a recording goes alone and the server transcribes it, so every voice message is heard
   by the same model. `GET /me` says so in `server_transcription`. (TR-10; owner, 2026-11-13: "we
   agreed to always transcribe with the server".)

## Design
- **Engine:** an OpenAI-compatible transcription server running as a second container:
  `speaches` (faster-whisper, CPU, `POST /v1/audio/transcriptions`). Partyline only speaks that one
  HTTP endpoint, so the engine can be swapped (whisper.cpp server, a GPU box later) without code
  changes. Config: `PARTYLINE_TRANSCRIBE_URL`, `PARTYLINE_TRANSCRIBE_MODEL`.
- **Compose:** `docker-compose.yml` gets an optional `whisper` service with hard limits
  (`cpus: 2`, `memory: 1.5G`) and a volume for the model cache; the partyline service points at it.
- **Server (phase 1, rooms):**
  - Migration 0035: `transcriptions(attachment_id PK → attachments ON DELETE CASCADE, message_id,
    room_id, state 'pending'|'done'|'failed'|'skipped', text, attempts, created_at, updated_at)`.
  - The row is written in the same transaction as the message (`rooms.post`/`insert`) when the
    service is configured, the message's trimmed text is empty and an attachment is a voice file:
    mime `audio/*`, status `stored`. A size over `MaxTranscribeBytes` (25 MB) is `skipped`.
  - Package `internal/transcribe`: one worker goroutine, oldest pending first. It reads the blob
    (`attachments.Store.Open`), posts it as multipart to `<URL>/v1/audio/transcriptions` (`file`,
    `model`, `response_format=json`), stores `done` + text, or retries a transient failure (network,
    5xx) up to 3 attempts with backoff, then `failed`. A restart resumes `pending` rows. Logs never
    hold audio or text. Then it publishes `events.MessageTranscribed{RoomID, MessageID}`.
  - Not an edit: `MessageUpdated` would send AIs an "edited" frame. The transcript is a field of the
    attachment (`dto.Attachment.Transcript`, `TranscriptState`), filled by the same loaders that read
    attachments, so the web, history, backlog and AI frames all carry it.
  - **Dispatcher hold (TR-03):** the dispatcher is one goroutine and the gateway drops a principal
    frame whose seq is not above the last one sent in its room, so a held message must keep its
    room's order: while a room has a held message, every later event of that room (created, updated)
    waits behind it in a per-room queue; other rooms flow. A hold ends on `MessageTranscribed` for that
    message or at 60 s (a timer in the same goroutine), then the held message and its queue are
    dispatched in order. A message whose transcription row is already done/failed/skipped is not held.
  - **Live (TR-02):** the live hub turns `MessageTranscribed` into `room.message_updated` with the
    message re-read, so every viewer gets the text.
  - **Channel:** `contentOf` adds the transcript under the attachment line:
    `[attachment <id>: <name>, <mime>]` then `Transcript: <text>`.
  - **Language (TR-07):** the model detects it; no hint in phase 1.
- **Config:** `PARTYLINE_TRANSCRIBE_URL` (base URL; empty = off), `PARTYLINE_TRANSCRIBE_MODEL`
  (default `Systran/faster-whisper-small`). The URL is the operator's: plain `http.Client`, not
  safehttp, with a 90-second request timeout, so a hung service holds the queue for about 5 minutes
  at most (3 attempts).
- **Web:** the voice bubble shows the browser transcript (message text) when there is one, else the
  attachment transcript; `pending` shows "Transcribing…" (pt-BR "Transcrevendo…").
- **Compose:** `docker-compose.yml` gets an optional `whisper` service (speaches, CPU) with hard
  limits (`cpus: 2`, `memory: 1.5G`), a model-cache volume, and the partyline service's
  `PARTYLINE_TRANSCRIBE_URL: http://whisper:8000`.
- **Phase 2, connections (TR-04):** the platform feed stops at the first item whose voice attachment is
  pending (60 s at most) so its order holds, and `PlatformRecord` content gets the same transcript line.
  Built as:
  - Migration 0036: `platform_transcriptions(attachment_id PK → platform_attachments ON DELETE
    CASCADE, item_id, state, text, attempts, created_at, updated_at)`, the columns of 0035 for the
    files of the platform queue. A separate table, because `transcriptions.attachment_id` references
    room attachments; the row goes with its file, and so with its item (expiry, cap, connection
    deleted).
  - `platform.Queue.Enqueue` writes the row in the item's transaction when the service is configured,
    the item's trimmed text is empty (a caption means no row) and a file is a stored voice file
    (`transcribe.IsVoice`, with a blob); over `MaxBytes` it is `skipped`. `Unacked`, `Get` and `Take`
    read the rows with the files (`platform.Attachment.TranscriptState`, `Transcript`, and
    `TranscriptSince`, the row's `created_at`).
  - The worker keeps one queue: it reads the oldest pending row of each table and takes the older
    (created_at, then id), with the same attempts, backoff, store retry and logs (`item_id` in place
    of `message_id`). A platform job ends with `events.PlatformTranscribed{AgentID, ItemID}`, the AI
    that holds the item when it ends. The worker also wakes on `PlatformQueued`.
  - **Feed hold:** `platformFeed.fill` skips the items in flight as before and stops at the first other
    item with a pending row whose `created_at` + hold is still ahead (the app clock, so a restart or a
    reconnection does not restart the wait); items before it go, nothing after it does. It arms a
    timer for the rest of the wait, and the gateway's bus watcher pokes the feed on
    `PlatformTranscribed` for its AI. The hold is `app.Options.TranscriptHold`, the same option as the
    dispatcher's (`testkit.WithTranscriptHold`).
  - The frame: `convert.PlatformEvent` copies `TranscriptState` and, when done, `Transcript`, so
    `FormatPlatform` / `PlatformRecord` print `Transcript: <text>` under the attachment line for the
    WebSocket, `partyline listen` and the pi/OpenCode adapters alike.
  - **Ack (CID-09):** the transcript is platform text, so the ack deletes the item's rows with its
    text; a job still pending then is dropped (the worker's finish matches no row and publishes
    nothing). An item that is acknowledged, or in flight on the connection, is never sent again
    because its transcript came later.
  - Without a service the startup sweep fails the pending rows of both tables.
- **Known limits (phase 1):**
  - An AI session that connects while one of its rooms holds a voice message gets that message from
    its backlog as it is then, without the transcript. Nothing is lost or sent twice: the gateway
    drops the held copy when the hold ends, because its seq was already sent.
  - A silent clip, or one the model hears nothing in, ends `done` with an empty text: the web shows
    no transcript and the AI gets the attachment line alone.
  - The 10-minute cap of TR-08 is the 25 MB size cap; the server does not read the audio's length.
  - The part sent to the service carries the stored type only when it parses as an `audio/*` type
    (else `application/octet-stream`), and a file name with the type's extension when it has none.
- **Known limits (phase 2):**
  - An item sent alone past the limit and not acknowledged before its connection ends is sent again
    to the next connection, as every unacknowledged item is (CID-08); that copy carries the
    transcript if it is ready by then.
  - A voice note that waits holds every later platform item of its AI, from every chat and
    connection, since the AI's platform items have one order. Room messages are not held by it.
  - `Queue.Take` (the polling path) does not wait for a pending transcript: it has no caller in the
    server today.
  - A platform voice note is a stored `audio/*` file. The kind the platform gave the message is not
    looked at, so an audio file sent without a caption is transcribed too.
- **Prod:** the 6 GB VM shares its physical host with the dev box. `small` takes about 1 GB of RAM
  while it runs and 10–20 s of CPU per minute of audio on 2 cores.

## Decisions (owner, 2026-11-05)
1. Model `small`. 2. On the prod VM, with hard limits; watch the load for a week. 3. AIs wait up to
60 s for the transcript.

## Out of scope
- Telling speech from music: every audio file posted in a room is transcribed; music yields little or
  no text.
- Translating the transcript.
- Showing word timings in the player.

## Tests to write first
- A voice message with no transcript is transcribed by a fake service and the AI gets it once, with
  the text.
- A slow service: the AI gets the audio alone at the limit; the text still arrives in the web app.
- A failing service: no error to anyone, the message as today.
- A browser transcript is kept; an over-long audio is skipped.
- An audio file that is not a recording keeps its text as a caption and is transcribed (TR-09).
- With a service configured the composer starts no browser recognition and `/me` says
  `server_transcription: true` (TR-10).
- A restart with a queued job finishes it.
- Phase 2: a platform voice note reaches its AI once with the transcript; a later platform item does
  not overtake it; a slow service sends it alone at the limit; a caption, oversize audio, a failing
  service and no service leave it as it was; a restart resumes the job; leftover platform rows fail
  at a start without a service; room and platform jobs share one queue.
