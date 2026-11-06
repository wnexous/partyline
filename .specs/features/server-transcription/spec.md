# Voice transcription on the server

Status: Specify + Design written 2026-11-05; the owner chose the proposals ("ok", 2026-11-05).
Built in two phases: rooms first (TR-01..03, TR-05..08), platform connections next (TR-04). Requirement ids TR-xx. Owner request (voice note, 2026-11-05): "put the transcription
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
4. WHEN a voice message arrives from a platform connection (WhatsApp, Telegram) THEN it SHALL be
   transcribed the same way before it is handed to the AI that operates the connection. (TR-04)
5. A transcript the sender's browser made SHALL be kept and not replaced. (TR-05)
6. WHERE no service is configured, or it fails or times out, THEN the message SHALL behave exactly
   as today: the audio alone, no error shown to anyone; failures are logged without the audio or the
   text. (TR-06)
7. The language SHALL be detected by the model; the sender's app language is the hint. (TR-07)
8. Transcription SHALL run one job at a time per instance, oldest first, with a cap of 10 minutes of
   audio per message; longer audio is sent without a transcript. (TR-08)

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
  safehttp, with a 5-minute request timeout.
- **Web:** the voice bubble shows the browser transcript (message text) when there is one, else the
  attachment transcript; `pending` shows "Transcribing…" (pt-BR "Transcrevendo…").
- **Compose:** `docker-compose.yml` gets an optional `whisper` service (speaches, CPU) with hard
  limits (`cpus: 2`, `memory: 1.5G`), a model-cache volume, and the partyline service's
  `PARTYLINE_TRANSCRIBE_URL: http://whisper:8000`.
- **Phase 2, connections (TR-04):** the platform feed stops at the first item whose voice attachment is
  pending (60 s at most) so its order holds, and `PlatformRecord` content gets the same transcript line.
- **Prod:** the 6 GB VM shares its physical host with the dev box. `small` takes about 1 GB of RAM
  while it runs and 10–20 s of CPU per minute of audio on 2 cores.

## Decisions (owner, 2026-11-05)
1. Model `small`. 2. On the prod VM, with hard limits; watch the load for a week. 3. AIs wait up to
60 s for the transcript.

## Out of scope
- Transcribing audio files that are not voice messages (music, recordings attached as files).
- Translating the transcript.
- Showing word timings in the player.

## Tests to write first
- A voice message with no transcript is transcribed by a fake service and the AI gets it once, with
  the text.
- A slow service: the AI gets the audio alone at the limit; the text still arrives in the web app.
- A failing service: no error to anyone, the message as today.
- A browser transcript is kept; an over-long audio is skipped.
- A restart with a queued job finishes it.
