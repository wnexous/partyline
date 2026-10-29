# Container agents: AIs that live in a container on a server you choose

Status: Specify + Design + Tasks written 2026-10-29. Requirement ids CA-xx.

## Problem
An AI exists only while someone keeps a terminal open with `partyline claude|pi|opencode|omp`.
The owner wants AIs that are always alive, isolated, and can run on a server of their choice,
started and managed from the web app (owner request 2026-10-26, defaults agreed 2026-10-29).

## Decisions taken with the owner (2026-10-29)
- Servers are registered by the owner (SSH host, port, user); the first is the dev box.
- Partyline generates one SSH key pair per server; the owner installs the public key in
  `authorized_keys`; the private key is stored sealed with the instance secret key.
- The AI inside the container logs in with credentials the owner stores in Partyline as named
  secrets (e.g. an OpenCode Go key for pi/OpenCode, a `claude setup-token` token for Claude Code),
  sealed at rest, never shown again after saving.
- Supported harnesses are the four with a live channel: Claude Code, pi, OpenCode, omp.

## Requirements
1. An owner SHALL register, list, rename and delete servers (name, host, port, user). On create,
   Partyline SHALL generate an ed25519 key pair, store the private key sealed and show the public
   key (with a copy button and the one-line command to install it). (CA-01)
2. "Check connection" SHALL connect over SSH, pin the server's host key on first success (trust on
   first use) and refuse a later different host key with a clear error; it SHALL report the Docker
   version or why Docker is not usable. (CA-02)
3. An owner SHALL store named secrets (name, value); values are write-only (never returned by the
   API, never logged), can be replaced or deleted, and are sealed at rest. (CA-03)
4. An owner SHALL create a container AI by choosing a server, a harness, a display name/handle, an
   optional git repository (URL + branch) for the workspace, the secrets to expose as environment
   variables (secret name → variable name) and limits (CPU, memory). Partyline SHALL create the
   AI's identity and an agent token, and start a container on that server that runs the harness
   with Partyline connected; the AI SHALL come online as a normal AI with the container session as
   its principal. (CA-04)
5. The container SHALL restart by itself after a crash or a server reboot (restart policy) and keep
   its workspace in a named volume. (CA-05)
6. From the AI's profile the owner SHALL see the container state (running, stopped, error, last
   error) and start, stop, restart, view the last log lines, and delete it (delete removes the
   container, and asks whether to also remove the workspace volume). (CA-06)
7. Only the owner of a server can use it; only whoever manages a container AI can operate its
   container; every create/start/stop/restart/delete/check is recorded in the audit log. (CA-07)
8. No secret value, token or private key SHALL appear in a log line, an error message, an audit
   entry, a process list on the server (no secret on a command line) or an API response. (CA-08)
9. Commands sent over SSH SHALL be built from validated values with every argument shell-quoted;
   names, images, URLs and refs SHALL be validated against strict patterns. (CA-09)
10. The web pages SHALL work at 360 px with large font (mobile first) and follow `web/AGENTS.md`.
    (CA-10)

## Out of scope (later)
Sleep/wake on message; several containers per AI; non-Docker runtimes; scheduling across servers.
