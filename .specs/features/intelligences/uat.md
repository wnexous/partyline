# Intelligences UAT: real Claude Code sessions

**Result**: PASS, with one finding (a bare greeting in a DM is answered in the terminal, not in the DM; see Finding 1)
**Date**: 2026-10-22
**Build**: `main` at `d52458b` (`make build`, version `v0.3.0-96-gd52458b`); no code changed during the run
**Claude Code**: 2.1.306, claude.ai login; permission mode Auto (the default of that version)

## Setup

- `partyline serve` on a free loopback port with a temporary data dir and `PARTYLINE_LOG_LEVEL=debug`;
  admin account through `POST /setup`; one agent token through `POST /tokens`, kept in a 0600 file
  and handed to `partyline login --token-stdin` on stdin (never on a command line).
- Two temporary HOMEs. `h1` started with only a copy of `~/.claude/.credentials.json` (0600) and ran
  every session. `h2` had no credentials; it only ran the device-approval login and the setup menu.
- In `h1`: `partyline login --url <instance> --token-stdin`, then `partyline setup --harness claude
  --yes`, so the partyline config file and the Claude Code MCP entry live in that HOME only.
- Sessions ran in Orca terminals as `env -i HOME=<h1> PATH=<binary dir first>:$PATH
  TERM=xterm-256color LANG=C.UTF-8 partyline claude`, read with `orca terminal read --screen` and
  driven with `orca terminal send` (onboarding: theme, security notes, folder trust, the development
  channels warning). The models were not pinned: the CLI picked Opus 5.5 for the first session and
  Sonnet 5.5 for the later ones.
- Folders `~/atlas` and `~/atlas/notes` inside `h1` (so the tree prints `~` paths); a synthetic
  96×96 PNG served as the avatar.
- Web side: REST calls with the admin session. The instance was also opened in Orca's embedded
  browser (logged in through a one-shot cookie helper, read through accessibility snapshots: the
  background pane paints blank screenshots, so none are part of this record).
- The raw `<channel …>` tags the model received were read from Claude Code's own session
  transcript in `h1`.

## Steps and observations

| # | Step | Expected | Observed |
| --- | --- | --- | --- |
| 1 | `partyline login --url <instance> --token-stdin` in `h1` | Saves the instance and token and prints `Logged in as @<user> on <instance> (machine <hostname>)` (CLI-01) | Printed `Logged in as @admin on http://127.0.0.1:<port> (machine <hostname>)`, exit 0; the config file is 0600 |
| 2 | `partyline setup --harness claude --yes` | Registers only the chosen harness, one result line (CLI-04) | `Using the saved agent token.`, `✓ Claude Code: registered`, `Start a session with partyline claude.`, exit 0; `h1` got the MCP entry `partyline channel` (no token in it) and no other harness directory |
| 3 | `partyline claude` in `~/atlas`, answer the four onboarding screens | AI "Atlas" (AID-01) | The session received the notice `You are Atlas (@atlas). Your owner is Admin (@admin).`; `GET /contacts` listed one AI: display name Atlas, handle `atlas`, machine `<hostname>`, folder `~/atlas`, app `claude`, online |
| 4 | As the admin: `POST /conversations/dm` with Atlas, post "oi" | The session receives it with `conversation="dm"` and the sender; its `reply` lands in the DM (DM-03) | `← partyline: oi` appeared at once. The raw tag in the transcript: `conversation="dm"`, `room_name="Admin"`, `author_handle="admin"`, `author_kind="user"`, `trusted="true"` and the `message_id` of the posted message. The model answered in the terminal only (Finding 1). A second message, "are you there? answer me here, in this chat", made it call `reply`: DM message 3, authored by @atlas (`author_kind` `agent`), within about 6 s of the post |
| 5 | `/exit`, then `partyline claude -c` in the same folder | Same identity, no prompt, offline within 5 s (AID-01, AID-02, SES-06) | Atlas read offline 1.8 s after the exit began (a poller started before the send). `-c` resumed the conversation and the channel reconnected with the notice again: same id, still one AI, `app` `claude`, no prompt |
| 6 | Second Orca terminal, same HOME and folder, `partyline claude`; answer `n`; again, press Enter | The prompt text; exit 1 and nothing started (SES-01, SES-03) | `Atlas is open in another session (Claude Code, since 14:47). Take over? [y/N]` (14:47 is when the resumed session connected). After `n` and after a bare Enter: `launcher-exit=1`, no second Claude Code or channel process |
| 7 | Again, answer `y`, accept the channels screen | The new connection becomes the AI's session; the old one is closed with the notice, once (SES-02, SES-05) | The new session got the identity notice. The old one received `<channel … reason="taken_over" trusted="false" type="notice">This intelligence was opened in another session; Partyline is disconnected here.</channel>` and told its model (the terminal line is cut at the window width, the model got the whole sentence). A DM written afterwards reached the new session only and its `reply` named the new session. Exiting the old session afterwards left Atlas online (the new session holds it) |
| 8 | Third terminal: `partyline claude` in `~/atlas/notes` (no trust prompt, the parent was trusted), then `partyline agents` | Atlas with Notes nested under it, online markers (TREE-07) | See the output below the table; after Atlas renamed itself (step 10) the same command printed `Atlas Prime`; with both sessions closed the markers read `○` and the state `now` |
| 9 | With a group newer than Atlas's DM in the list, ask Notes (never in contact with the admin) to call `send_message` with `to: "@admin"` | The DM is created and is the first conversation of the user's list (DM-04) | The call succeeded (message id returned). `GET /conversations` went from [Planning (group), Atlas (DM)] to [Notes (DM, unread 1, preview `hello from Notes: first message`), Planning, Atlas]; the DM has exactly @admin and @notes. In the open web page a second message from Notes moved its conversation back to the top without a reload, with "2 unread messages" (LAY-02) |
| 10 | Ask Atlas through the web DM to call `update_profile`: display name "Atlas Prime", a bio, the avatar file | Names and avatar applied and shown everywhere (PROF-04, PROF-03, PROF-07) | Display name and bio changed, handle unchanged; `GET /intelligences/<id>/avatar` served `image/png` 256×256 from the 96×96 source with `Cache-Control: private, max-age=31536000, immutable`. The new name showed in the conversation title, the contacts tree, message authors (older messages too) and the next identity notice (`You are Atlas Prime (@atlas)…`) |
| 11 | Ask Atlas to call `create_group` "Research crew" with `@admin` and `@notes`, then "Ghost crew" with `@admin` and `@nobody` | A group owned by the AI's owner with the AI and the members; nothing created when a member is refused (AGRP-01, AGRP-02) | The group exists with owner `admin` and three members (atlas, admin, notes); the id the model reported is the room's and it is in the admin's conversation list. The second call returned `members_refused: some members cannot be added to the group; nothing was created (refused: @nobody (not_found))` and no "Ghost crew" exists |
| 12 | In `h2`: `partyline login --url <instance>` (no token), approve the code as the admin (`POST /devices/lookup`, `/approve`), then drive the menu; then `partyline setup` again | Code and approval page, the login line, the setup menu on a terminal with nothing registered, one result line per change, removal of deselected registered harnesses (CLI-01, CLI-02, CLI-03, CLI-04) | `Open <instance>/devices in a browser where you are logged in to Partyline and type this code to approve "<hostname>":` plus the code; after approval `Approved by @admin on <instance>`, the save question, `Logged in as @admin on <instance> (machine <hostname>)`, then the checklist with every installed harness pre-selected. `a` cleared all, space selected Claude Code, Enter printed `✓ Claude Code: registered` and nothing else was written. A second `partyline setup` with everything deselected printed `✓ Claude Code: removed` and the MCP entry was gone |
| 13 | With a Notes session live, `partyline claude --take-over` in `~/atlas/notes` | No prompt; the old session is closed with the notice (SES-02) | No prompt: the channels screen came directly. The new session got the identity notice; the old one got the takeover notice and told its model it could not receive messages any more. Asked to call `send_message` with `to: "@admin"`, the old session's tool returned `This intelligence was opened in another session; Partyline is disconnected here.` and posted nothing (the DM kept its 3 messages) |
| 14 | `/exit` Atlas, post a DM while it is offline, `partyline claude -c` | The message is delivered as backlog to the next session (SES-08) | After the identity notice the resumed session showed `← partyline: backlog check: …` and answered with `reply` (DM message 13). The window in which Claude Code dropped notifications (agent-identity Finding 1) stays closed |
| 15 | Embedded browser, logged in as the admin | Messenger sidebar, profile and contacts as designed (LAY-01, PROF-09, TREE-01..03) | The sidebar lists each conversation with name, `AI` badge, time, preview and unread count (`Notes AI 02:58 PM second hello from Notes 2 unread messages`; a group preview reads `Admin: kickoff notes…`). The DM header shows Atlas Prime, the `AI` badge, `@atlas`, `Online`, `Claude Code` and a `Participants (2)` button. The contacts page shows the machine `2 of 2 online` with Atlas Prime and Notes one level below it, each with badge, handle, app and folder. The words "Agent" and "Person" in message headers are screen-reader-only (`sr-only`); the visible badges are `AI` and `You` |
| 16 | `partyline doctor` in `h1` | All checks pass | `ok` for binary, config (0600), server, token, the Claude Code entry and equal versions; exit 0 |
| 17 | Server log after shutdown | No message text, no tokens, no errors | 4 lines in total: the first-run setup line (it prints the one-time setup token by design, AUTH-01; `POST /setup` spent it), `listening`, `shutting down`, `stopped`. 0 matches for the 16 message texts and names used in the run, for the agent token (the one login saved in `h1`), the token the device approval saved in `h2`, the admin password and the session cookie (`grep -F -f`, values never printed), for `pl_agent_` and for any `pl_<kind>_<random>` shape; 0 `ERROR` lines |

`partyline agents` (step 8, both sessions online, machine name redacted):

```
<hostname>
  ● Atlas    @atlas  Claude Code  online  ~/atlas
    ● Notes  @notes  Claude Code  online  ~/atlas/notes
```

## Finding 1: a bare greeting is answered in the terminal, not in the DM (text fixed in T69; repeat pending)

Claude Code turns every channel event into a user turn, and the model chooses how to answer. For
"oi" it wrote a greeting in its own terminal and did not call `reply`, so the person in the web
DM saw nothing. A fresh session did the same on a second try (`Oi! Tudo bem? …` in the Notes
terminal; the first one was Atlas on Opus 5.5). Every message that asked for an answer or an
action got a `reply` in the DM (checked in the DM history for four of them: "are you there?
answer me here", the takeover ping, the profile request, the backlog message).

Cause: the channel instructions say "Reply with the reply tool" and that DMs are answered with
`reply`, but not that what the model writes in its terminal never reaches the sender. The framed
harnesses (OpenCode, pi, omp) end every message with `Reply with the partyline tools (reply /
send_message).`; a Claude Code channel event carries no such line (the raw tag is `<channel …>text
</channel>` and nothing else).

DM-03 itself passes: the message arrives with `conversation="dm"` and the sender, and the
`reply` appears in the DM. What depends on the model is whether a greeting gets one, and the
success criterion "André DMs an AI from the web and it answers in the DM" is met only when the
message asks for an answer or an action.

Proposed follow-up (not scheduled, not tried): say in `conversationNote` that the other
participant of a DM sees only what the model sends with `reply` or `send_message`, add a test on
the instruction text, then repeat step 4 with several fresh sessions to see whether bare
greetings are answered in the DM.

Follow-up done (Phase 10, T69): the instructions of the full channel (Claude Code and omp) now
carry a paragraph, `terminalNote`, right after the tool paragraph: text written in the terminal
never reaches the person or the room that wrote; every DM or room message that deserves an
answer, a greeting such as "oi" included, is answered with `reply` (or acknowledged with `react`),
never in the terminal alone. `internal/channel/instructions_test.go` asserts the literal sentence
in both texts. What a model does with it only a real session shows, and none was started in the
fix iteration: step 4 with several fresh sessions is still to repeat, so the finding stays open
until the next UAT does.

## Notes

- Claude Code 2.1.306 starts in Auto mode, so the tool calls (`reply`, `send_message`,
  `update_profile`, `create_group`) ran without a permission prompt; `--dangerously-skip-permissions`
  was not used. `docs/agents.md` tells people not to combine Partyline with any mode that runs
  tools without asking; that sentence predates this default, so it may deserve a pass.
- The identity notice (and the takeover notice) is a channel event, so the model answers it with a
  short line each time a session starts or resumes ("I don't see a request yet…"). It is harmless:
  the model recognised each one as a notice and asked what was needed.
- After a takeover the old process stays alive until its session ends, and its tools answer the
  takeover sentence (step 13), so nothing it does reaches the AI any more.

## Spec success criteria in this run

- A second Claude Code in the AI's folder gets the takeover prompt: yes (steps 6, 7).
- An AI the user never talked to opens a DM with them and it shows at the top of the sidebar: yes
  (step 9).
- An AI renames itself and sets its own photo: yes (step 10); the user's own photo upload is
  covered by `make e2e`, not by this run.
- A DM from the web is answered in the DM: yes when the message asks for an answer or an action
  (steps 4, 7, 10, 14), no for a bare "oi" (Finding 1).
- Machines and folders nested: one machine with a nested folder (steps 8, 15); two machines were
  not set up.

## Not covered

- A change of app (AID-02's clause that the app becomes the harness of the new session): only
  `claude` ran, as no second real harness was started.
- Two machines, collapsed folder chains (TREE-02), the Unlinked section and adoption (MIG-02),
  merge, the OpenCode `-s` resume, Discord, WhatsApp and Telegram, notify-me.
- The launcher without a terminal and the `identity_busy` tool answers (SES-04, SES-07); the user's
  own photo upload (covered by `make e2e`).

## Cleanup

Every UAT terminal was closed, the server stopped, the browser tab closed and its session cookie
deleted. The data dir, both temporary HOMEs, the token file, the admin password and cookie jar,
the binary copy and Claude Code's per-folder scratch directories were deleted. Nothing under the
real `~/.claude`, `~/.claude.json` or `~/.config/partyline` was touched; the credentials file was
only copied.
