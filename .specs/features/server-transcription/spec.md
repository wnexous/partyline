# Voice transcription on the server

Status: Specify + Design written 2026-11-05, waiting for the owner's OK on the open decisions before
it is built. Requirement ids TR-xx. Owner request (voice note, 2026-11-05): "put the transcription
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
   ready or 60 seconds have passed, and deliver it once, with the transcript as its text; past the
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
- **Server:** a `transcribe` package with a single worker goroutine and a queue in SQLite (a
  `transcriptions` table: message id, state, attempts), so a restart resumes the queue. The
  dispatcher waits on the job (TR-03) for messages whose audio has no transcript.
- **Storage:** the transcript goes into the message text, the field the browser transcript already
  uses (VOI-09), through the normal edit path, so the web app, history and AIs need no new field. An
  edit from the server is marked as the system's, not the author's.
- **Prod:** the 6 GB VM shares its physical host with the dev box. `small` takes about 1 GB of RAM
  while it runs and 10–20 s of CPU per minute of audio on 2 cores.

## Open decisions (owner)
1. Model: `small` (lighter, good Portuguese, the one already used on this machine) or `large-v3-turbo` (better
   text, about 3 GB of RAM, slower on CPU). Proposal: `small`.
2. Where it runs: on the prod VM next to Partyline (simplest; uses its CPU) or on another machine.
   Proposal: the prod VM with the hard limits above, and watch the load for a week.
3. Wait for AIs: hold up to 60 s for the transcript (proposal), or deliver at once and send an edit
   when the text is ready (AIs get the message twice).

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
