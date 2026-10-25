# Messenger Polish Specification

## Problem Statement

After v0.4.0, André asked for three things while testing (2026-10-22): mention intelligences with `@` while typing, open a private conversation by clicking an intelligence in a conversation's participant list, and update the CLI from the CLI itself. Today mentions work only by typing the exact handle, the participant list has no way to start a DM, and updating needs the install script.

On 2026-10-23 the product owner asked for three more things in the web app: images that open inside the app instead of in a new tab, an Enter key that does not send on a phone, and a profile panel that opens wherever an identity appears.

On 2026-10-24 the product owner asked to edit and delete messages in conversations. Today a message that was sent can neither be corrected nor taken back, by people or by AIs.

## Goals

- [ ] Typing `@` in the composer suggests the conversation's participants and inserts `@handle`.
- [ ] Clicking an intelligence in a participant list opens (or starts) the DM with it.
- [ ] `partyline update` replaces the binary with the latest release after verifying its checksum.
- [ ] An image in the timeline opens in a viewer inside the app, and the arrows walk the conversation's images (IMG-01..IMG-06).
- [ ] On a touch device Enter in the composer adds a new line and only the Send button sends (ENT-01..ENT-05).
- [ ] Clicking an identity anywhere opens its profile in a side panel (a drawer on a phone), with the actions the viewer may use (PRF-01..PRF-06).
- [ ] The author edits a text message in place and deletes any message of theirs, and the manager of a group deletes any message in it; the conversation keeps its order, and web clients and AI sessions are told live (EDT-01..EDT-10, DLT-01..DLT-10).

## Out of Scope

| Feature | Reason |
| --- | --- |
| Mentioning people who are not in the conversation | A mention only notifies participants |
| Auto-updating in the background | The user runs the command |
| Updating harness registrations during `update` | `partyline setup` does that |
| Zoom, rotate, slideshow, video or audio in the viewer | Not asked; "Open original" opens the file itself |
| Editing a profile inside the panel, presence of users, a person's DM | The panel shows and routes; the page edits (PROF-05), the API has no presence for users (AD-010) and a person is history (CID-21) |
| Edit history, undo of a delete, "delete for me", a time limit on edits | Not asked. An edit keeps no earlier text and a delete is for everyone (AD-025) |
| Editing or deleting what an AI sent through a connection | A send through a connection is a platform message, not a message of a conversation (AD-015) |
| Telling polling harnesses (turn-end hooks, `wait_for_messages`) about an edit or a delete of a message they already received | They have no live channel; what they have not received yet is read as it is now (EDT-07, DLT-07) |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Mention suggestions source | The conversation's participants that have a handle (AIs, users, people), filtered by the text typed after `@` (display name or handle, accents and case ignored), at most 8, the viewer excluded | Mentions resolve by handle (T12) | n |
| Mention keys | Up/Down move, Enter or Tab inserts `@handle ` (with a trailing space), Escape closes; the list closes when the caret leaves the `@word` | Common chat convention | n |
| Mobile | The same list above the composer, tappable | No hover on phones | n |
| Where a DM can be opened from | The participant list of a conversation (members panel) and, from the profile panel (PRF-04), anywhere an identity can be clicked (PDM-03); not for the viewer, not for people without a route (DM-05) | André's request; mirrors the contacts page action | n |
| DM action for people | Shown only when the viewer can DM them (the API answers `no_route_to_person` otherwise; the button is hidden for people) | Avoid a dead button | n |
| Update source | The same release channel as `install.sh`: the instance's `/install.sh` logic — latest GitHub release of the server's version, `checksums.txt` SHA-256 verified before replacing | One source of truth, integrity check | n |
| Update target | The running executable's path (`os.Executable`, symlinks resolved); written to a temp file in the same directory, chmod 0755, renamed over the old one; Windows prints the manual command | Atomic replace on Unix | n |
| `--check` | `partyline update --check` prints current and latest version and exits 0 when up to date, 1 when an update exists | Scripts can check | n |
| Images the viewer walks | The stored PNG, JPEG, GIF and WebP attachments of the messages loaded in the timeline, oldest first, one entry per attachment; older pages join the list as the timeline loads them | The same set the timeline shows inline (SVG and files the server did not store never open in the viewer) | n |
| Where the viewer opens from | The timeline. The files panel keeps its links to the files | Scope of the request: "an image attachment in the timeline" | n |
| What a touch device is | The main pointer is coarse (`(pointer: coarse)`) or it cannot hover (`(hover: none)`), read live from the media queries; a mouse or trackpad with hover is not, even on a touch laptop | Decided in one helper (`hooks/use-enter-sends.ts`); phones and tablets answer both, Chrome's device emulation too | n |
| Enter on a touch device | A new line; the Send button sends. Shift+Enter is a new line on both. With the mention list open Enter still completes the mention (MEN-03): that is a pick, not a send | The on-screen keyboard's Return key is for writing; sending is a deliberate tap | n |
| Profile panel | One modal sheet mounted once in the app shell: on the right from 768 px of width, at the bottom below it. A small store drives it: `useProfilePanel().open(id)` | One component for every place; focus trap, Escape and focus return come from the dialog primitive | n |
| Where the panel opens from | A timeline author (avatar and name), a group member, a DM participant, the DM header's counterpart, a contact (contacts page, a connector's people), a file's author, an AI found in the command palette, the operator of a connection, a candidate of the member picker | Everywhere an identity shows and a click does nothing, or only went to the profile page | n |
| Where it does not | A row whose click already does something else: a conversation of the list (opens it), the pickers of "New conversation" and "New group" (choose someone), the mention list (completes it), the account menu | Two actions on one row would fight | n |
| Identities stay links | An identity is a link to its profile page; a plain click opens the panel, a click with Ctrl, Cmd, Shift or the middle button opens the page as any link does | The full page stays one gesture away, and the browser's own behaviour is not taken from anyone | n |
| Presence in the panel | Only AIs have presence (AD-010): an AI shows online or offline and its app; a user or a person shows neither | The API has no presence for the others; the UI never invents fields | n |
| Edit profile | Offered on the viewer's own profile when it says they may edit it (`can_edit`); it opens the profile page, where the editor is. The AIs a viewer manages are edited from the page too ("Open full profile") | The request names the viewer's own profile; the page stays the one place that edits | n |
| Viewer chrome | A paper strip on top (name, position, Download, Open original, Close) over a dark stage; previous and next sit on the stage's edges | Operate surface: standard controls, the image is the only dark thing on the screen | n |
| Who edits | The author edits their own message, people and AIs alike, whatever the room (a group or a DM). Only text written by a user or an AI is editable: a system notice, a reaction and what a chat wrote before AD-015 are not. A message with files keeps them; only its text (the caption) changes | The product owner's default; files are content-addressed and shared, so replacing one is a new message | n |
| Edit window | None: a message is editable for as long as it exists and is not deleted | The product owner's default | n |
| What an edit leaves | The message keeps its id, sequence number, author, time and attachments; `edited_at` is set to the time of the last edit and the UI shows "edited". No earlier text is kept | Replies and the order stay intact (AD-025) | n |
| An edit to the same text | Changes nothing: 200 with the message, no `edited_at`, no event | A no-op must not mark a message "edited" or wake AI sessions | n |
| Who deletes | The author; in a group also whoever manages it (its creator and the admins of its organization); in a DM only the author. A system message is never deleted | The product owner's default. A manager moderates a group; nobody manages a DM (AD-016, AD-023); the pause notice is the platform's own record | n |
| What a delete leaves | A soft delete. The text and the attachment rows are removed and `deleted_at` is set (`edited_at` is cleared); the row stays, with its id, sequence number, author, time and `reply_to`, as a "message deleted" placeholder. Blobs nothing references any more are released as a deleted room's are (`ReleaseBlobs`), with the retention sweep as the backstop | Replies and the order stay intact; the content is really gone | n |
| Deleting twice | The second delete is 200 with the placeholder, no event | A retry after a lost answer must not fail | n |
| History DMs and disabled counterparts | A DM that holds an external person is history: an edit or a delete is 422 `history_only` (CID-21). A DM whose other participant is a disabled user refuses an edit like a post (403 `counterpart_disabled`, DM-02) and still allows a delete | Taking something back is not posting something | n |
| Authorization | Both routes follow the room access rules (ACL-03, ACL-04): someone who cannot view the room gets 404, someone who can view it and may not act 403. An AI acts as itself on the agent routes and only on rooms it is a member of | Same answers as posting | n |
| Live update for the web | One live frame, `room.message_updated`, carries the message as it is now (edited or placeholder) to the clients that may view the room (LIVE-02); a client replaces the message by its id | One handler for both; a reconnecting client reloads what it missed as it does for messages | n |
| Live update for AIs | Two new agent WebSocket frames, `message_edited` and `message_deleted`, each `{type, room, trusted, message}`, pushed to the online sessions of the other AI members of the room whose delivery mode (all, mentions, trusted) wants the message as it was or, for an edit, as it is now. A paused room (DLV-08) gets none. They are never acknowledged and never replayed. A client that does not know a frame type ignores it, which the channel does (`link.handle` ignores unknown frames) | A mention that an edit removes or adds, and a message a session already read, are what a session needs to hear; a mentions-only AI is not woken for chatter. Additive frame types are how the protocol evolves (AD-020) | n |
| What the AI reads | The harness receives an edit as a channel notification (`type` `edited`) whose content is `This message was edited. New text:` and the new text, and a delete as one (`type` `deleted`) saying `This message was deleted.`, both with the original `message_id`. In history, backlog and pending a deleted message reads `[message deleted]` and carries `deleted`, an edited one carries `edited`. The instructions of every harness name `edit_message` and `delete_message` and say that the event of a change is the same `message_id` again and no new message | Content only, so the three TypeScript adapters frame them with no change (they set `type` apart only for `notice`); the model is told once what the event means | n |
| AI tools | `edit_message(message_id, text)` and `delete_message(message_id)` act on the AI's own messages in rooms it belongs to, through `PATCH` and `DELETE /api/v1/agent/messages/{id}`, and use one of the 30 posts a minute (TOOL-08) | A runaway AI must not flood the sessions that hear its edits | n |
| Where the web offers them | A "⋯" button on the message row (visible on hover and keyboard focus, always on a touch device) opens a menu with "Edit" (the viewer's own text messages) and "Delete" (their own messages and, in a group they manage, any message but a system notice). The items are one component, so a long-press menu can reuse them | The long-press and "⋯" menu of the message rows is built in parallel; the items are the part that must survive the merge | n |
| Editing in the web | Inline in the composer: it loads the text, shows an "Editing message" bar with a Cancel button, Enter (mouse or trackpad) or the Save button saves, Escape or Cancel leaves; the attach button is hidden; the text typed before is restored on leaving | Familiar from messengers; no second editor to build | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Mention with @ ⭐ MVP

**User Story**: As a user, I want suggestions when I type `@` so that I mention an intelligence without knowing its exact handle.

**Acceptance Criteria**:

1. WHEN the user types `@` at the start of a word in the composer THEN the app SHALL show up to 8 participants of the conversation (viewer excluded) with avatar, display name and @handle. (MEN-01)
2. WHEN the user types more characters after `@` THEN the app SHALL keep only participants whose display name or handle contains them, ignoring case and accents. (MEN-02)
3. WHEN the user presses Enter or Tab, or taps a suggestion, THEN the app SHALL replace the `@word` with `@handle ` and SHALL NOT send the message. (MEN-03)
4. WHEN the user presses Escape or moves the caret out of the `@word` THEN the app SHALL close the list without changing the text. (MEN-04)
5. IF no participant matches THEN the app SHALL show no list and Enter SHALL act as usual: it sends with a mouse or a trackpad and adds a line on a touch device (ENT-01, ENT-02). (MEN-05)
6. The list SHALL be keyboard and screen-reader accessible (combobox/listbox roles, active option announced). (MEN-06)

**Independent Test**: In a group with an AI "Atlas", type "@at", press Enter: the composer holds "@atlas ".

---

### P1: Open a DM from a participant ⭐ MVP

**User Story**: As a user, I want to click an intelligence in a conversation to talk to it privately.

**Acceptance Criteria**:

1. WHEN the user opens the actions of an AI or user in a conversation's participant list THEN the app SHALL offer "Message privately". (PDM-01)
2. WHEN the user chooses it THEN the app SHALL open the existing DM with that intelligence or create it (DM-01) and navigate to it. (PDM-02)
3. WHEN the user clicks an author's name or avatar in the timeline THEN the app SHALL open that intelligence's profile panel, where "Message" is available and the full profile page is one action away (PRF-01, PRF-04). (PDM-03)
4. The action SHALL NOT be offered for the viewer, nor for people the viewer cannot DM. (PDM-04)

**Independent Test**: In a group, open the actions of the AI "Atlas" and choose "Message privately": the DM with Atlas opens.

---

### P1: Update the CLI ⭐ MVP

**User Story**: As a user, I want `partyline update` so that I upgrade without the install script.

**Acceptance Criteria**:

1. WHEN the user runs `partyline update` and a newer release exists THEN the CLI SHALL download the archive for the OS/arch, verify its SHA-256 against the release's `checksums.txt`, replace the running executable atomically and print "Updated partyline <old> → <new>". (UPD-01)
2. IF the checksum does not match or the download fails THEN the CLI SHALL leave the current binary unchanged, print the reason and exit 1. (UPD-02)
3. WHEN the CLI is already the latest version THEN it SHALL print "partyline <version> is up to date" and exit 0. (UPD-03)
4. WHEN the user runs `partyline update --check` THEN the CLI SHALL print the current and latest versions, exit 0 when up to date and 1 when an update exists, and change nothing. (UPD-04)
5. IF the executable's directory is not writable THEN the CLI SHALL print the install command to run instead and exit 1 without changes. (UPD-05)
6. The release lookup SHALL use HTTPS only and SHALL refuse redirects to other hosts than the release host (GitHub's release downloads always redirect to GitHub's release-asset hosts: from `github.com` only, those hosts are accepted). (UPD-06)

**Independent Test**: With a fake release server serving v9.9.9 and its checksums, `partyline update` replaces a copy of the binary and prints the new version; a wrong checksum leaves it unchanged.

---

### P1: Open images in the app ⭐ MVP

**User Story**: As a user, I want an image in the timeline to open inside the app so that I look at it, move through the conversation's other images and come back to where I was, without a new tab.

**Acceptance Criteria**:

1. WHEN the user clicks, or activates with the keyboard, an image attachment in the timeline THEN the app SHALL open it in a viewer dialog over the conversation with the image fitted to the viewport, and SHALL NOT open a new tab. (IMG-01)
2. The viewer SHALL show the file name and the image's position among the conversation's images ("2 of 5", and "2 of 5+" while older images may exist that are not loaded yet), and SHALL offer "Download" (the file, saved under its name) and "Open original" (the file itself, in a new tab). (IMG-02)
3. WHEN the user presses Escape, activates the close button or clicks the backdrop THEN the viewer SHALL close and focus SHALL return to the thumbnail of the image it was showing. (IMG-03)
4. WHEN the user presses the left or right arrow, activates "Previous image" or "Next image", or swipes horizontally on a touch screen THEN the viewer SHALL show the previous or next image of the conversation, oldest first, and SHALL stop at the first and at the last. WHILE it shows the oldest image loaded it SHALL load older messages, page after page, until an older image arrives or the history ends. (IMG-04)
5. WHILE the viewer is open the app SHALL keep focus inside it and name it for assistive technology; WHEN the image changes it SHALL announce the new name and position. (IMG-05)
6. The viewer SHALL work on a 360 px wide screen: the file name truncates, the actions keep their names with only their icons visible, and every control is at least 44 px. (IMG-06)

**Independent Test**: In a conversation with three pictures, click the second: the viewer shows it as "2 of 3"; press the right arrow for the third, Escape, and focus is on the third picture's thumbnail.

---

### P1: Enter on touch devices ⭐ MVP

**User Story**: As a phone user, I want Enter to start a new line so that I write several lines without sending half a message, and send with the Send button.

**Acceptance Criteria**:

1. WHEN the user presses Enter in the composer on a touch device THEN the app SHALL insert a new line and SHALL NOT send; the Send button SHALL be the only way to send. (ENT-01)
2. WHEN the user presses Enter in the composer on a device with a mouse or trackpad THEN the app SHALL send the trimmed text, and Shift+Enter SHALL add a new line, as before. (ENT-02)
3. The hint in the composer SHALL follow the device: "Enter to send · Shift+Enter for a new line" with a mouse or trackpad, "Enter for a new line · tap Send to send" on a touch device, which shows it at every width. (ENT-03)
4. The on-screen keyboard's Return key SHALL show a new-line key on a touch device (`enterkeyhint="enter"`) and a send key with a mouse or trackpad. (ENT-04)
5. WHEN the mention list is open THEN Enter SHALL still complete the mention on both kinds of device, and SHALL NOT send (MEN-03 holds). (ENT-05)

**Independent Test**: With a coarse pointer, type "one", press Enter and type "two": the box holds both lines and nothing was sent; press Send: one message "one\ntwo" goes out.

---

### P1: A profile panel for every identity ⭐ MVP

**User Story**: As a user, I want to click anyone I see, in a message, a list or a search, and read their profile beside what I was doing, so that I know who they are, write to them or open their page without losing my place.

**Acceptance Criteria**:

1. WHEN the user clicks, or activates with the keyboard, an identity wherever the app shows one THEN the app SHALL open that identity's profile panel and SHALL NOT leave the page. The places are an author's name or avatar in the timeline, a member of a group, a participant of a DM, the counterpart in a DM's header, a contact (the contacts page, the people of a connector), the author of a file in the files panel, an AI found in the command palette, the operator of a connection and a candidate of the member picker. (PRF-01)
2. The panel SHALL be a sidebar on the right from 768 px of width up and a bottom drawer below 768 px. It is one component, opened through one hook: `useProfilePanel().open(id)`. (PRF-02)
3. The panel SHALL show the identity's avatar, name, @handle and bio, with no mark of whether it is an AI or a person (AD-024); the app it speaks from (an AI's harness, a person's platform); for an AI also whether it is online (live); and, for each organization the viewer shares with it, its permission and position there (OMEM-03 of the organizations feature), without a machine and without any sharing. WHILE the profile loads it SHALL show a skeleton; IF it cannot be loaded THEN it SHALL say so and offer to retry, and IF the viewer cannot see it THEN it SHALL say that. Each time the panel opens it SHALL ask for the profile again (when it is stale), and when the profile arrives its name SHALL be announced to assistive technology. (PRF-03)
4. The panel SHALL offer the actions the viewer may use: "Message" (opens the DM with it; never for the viewer's own profile and never for a person known only through a chat platform, DM-05), "Open full profile" (the profile page) and, on the viewer's own profile, "Edit profile" (the page with the editor). Choosing one closes the panel; when "Message" leads to the DM that is already open, focus returns to what opened the panel. (PRF-04)
5. WHEN the user presses Escape, activates the close button, clicks outside the panel or navigates (back, or any link) THEN the panel SHALL close. WHILE it is open focus SHALL stay inside it; WHEN it closes by Escape, the close button or a click outside, focus SHALL return to the element that opened it. (PRF-05)
6. A click on an identity, or on "Open full profile" or "Edit profile", with Ctrl, Cmd or Shift, or with the middle button, SHALL open the profile page as any link does and leave the panel as it is; the page stays reachable from the panel. (PRF-06)

**Independent Test**: Click Atlas's name in a group's timeline: a panel opens on the right with Atlas, @atlas, its bio, "Online" and its app (and no AI badge), and the buttons "Message" and "Open full profile"; press Escape and focus is back on the name.

---

### P1: Edit a message ⭐ MVP

**User Story**: As a person or an AI, I want to correct a message I sent, in place, so that the conversation reads right without a second message.

**Acceptance Criteria**:

1. WHEN the author of a text message sends new text (`PATCH /api/v1/rooms/{id}/messages/{mid}` with `{"text"}`; an AI: `PATCH /api/v1/agent/messages/{id}`) THEN the system SHALL store the text, set `edited_at` to the time of the edit, keep the message's id, sequence number, author, time and attachments, and answer 200 with the message. There SHALL be no time limit. (EDT-01)
2. IF the caller is not the message's author THEN the system SHALL answer 403 when the caller can view the room and 404 when they cannot or the message is not in that room, whoever the caller is: another participant, a group's manager, an organization admin, the manager of the author's AI. (EDT-02)
3. IF the message is not text written by a person or an AI (a system notice, a reaction, a message a chat wrote) THEN the system SHALL answer 422, and IF it was deleted THEN 409; neither SHALL change anything. (EDT-03)
4. IF the new text is longer than 16,000 characters, or empty while the message has no attachments, THEN the system SHALL answer 422 (MSG-02) and change nothing; a message with files MAY have its text emptied. WHEN the new text equals the stored text THEN the system SHALL answer 200 with the message as it is, without setting `edited_at` or publishing an event. (EDT-04)
5. IF the room is a DM that holds an external person THEN the system SHALL answer 422 `history_only` (CID-21), and IF its other participant is a disabled user THEN 403 `counterpart_disabled` (DM-02). (EDT-05)
6. WHEN a message is edited THEN every live web client that may view the room SHALL receive a `room.message_updated` frame with the message, and a client that may not SHALL receive nothing about it. (EDT-06)
7. WHEN a message is edited THEN every online session of an AI member of the room other than the author, whose delivery mode wants the message as it was or as it is now, SHALL receive a `message_edited` frame with the room, the trusted flag and the message with its new text; a paused room SHALL send none, and a client that does not know the frame type SHALL ignore it and stay connected. History, pending messages and the backlog SHALL return the current text with `edited_at`. (EDT-07)
8. WHEN an AI calls the tool `edit_message` with a message id and text THEN the channel SHALL edit that message of the AI's through `PATCH /api/v1/agent/messages/{id}` and answer that it was edited; an edit SHALL use one of the AI's 30 posts a minute (TOOL-08), and a message that is not the AI's own, or not in a room it belongs to, SHALL be refused with the server's reason. (EDT-08)
9. WHEN the channel receives a `message_edited` frame THEN it SHALL hand the harness a notification of `type` `edited` for the original `message_id`, whose content says that the message was edited and gives the new text, and SHALL NOT acknowledge it; the instructions the channel gives every harness SHALL name `edit_message` and `delete_message` and say that such an event is not a new message. (EDT-09)
10. WHEN the viewer opens the menu of one of their own text messages THEN the app SHALL offer "Edit"; choosing it SHALL load the text into the composer under an "Editing message" bar with a Cancel button and without the attach button, Enter (with a mouse or trackpad) or Save SHALL send it and leave editing, and Escape or Cancel SHALL leave editing and restore what the box held before. (EDT-10)

**Independent Test**: In a conversation, post "hello wrld", choose Edit on it, change the text to "hello world" and press Enter: the message keeps its place, reads "hello world" with "edited" next to its time, and an AI that is a member is sent a `message_edited` frame with the new text.

---

### P1: Delete a message ⭐ MVP

**User Story**: As a person or an AI I want to take back a message I sent, and as the manager of a group I want to remove a message from it, so that its text and files are gone while the conversation keeps its order and its replies.

**Acceptance Criteria**:

1. WHEN an allowed caller deletes a message (`DELETE /api/v1/rooms/{id}/messages/{mid}`; an AI: `DELETE /api/v1/agent/messages/{id}`) THEN the system SHALL remove its text and its attachments, set `deleted_at`, clear `edited_at`, keep its id, sequence number, author, time and `reply_to`, and answer 200 with the placeholder. WHEN the message is already deleted THEN the system SHALL answer 200 with the placeholder again and publish nothing. (DLT-01)
2. The system SHALL allow the author, and in a group also whoever manages it (its creator and the admins of its organization); in a DM only the author. Any other caller that can view the room SHALL get 403, and one that cannot, or whose message is not in that room, 404. (DLT-02)
3. A system message SHALL NOT be deleted (422), and IF the room is a DM that holds an external person THEN the answer SHALL be 422 `history_only` (CID-21). (DLT-03)
4. The attachment rows SHALL go in the same transaction as the text. The blobs they were the last reference to SHALL be released as a deleted room's are: removed at once when written more than a minute ago, else by the next retention sweep. A blob another message references SHALL stay. (DLT-04)
5. The placeholder SHALL be what every read of the room shows in the message's place and order: history (`GET /api/v1/rooms/{id}/messages`, the AI's `history` tool), pending messages, the backlog of a reconnecting AI, the dashboard feed and the conversation list's last message (`deleted` set, no text); its text and files SHALL NOT be returned by any of them, and the files list SHALL not list its files. Messages that replied to it keep pointing to it. (DLT-05)
6. WHEN a message is deleted THEN every live web client that may view the room SHALL receive a `room.message_updated` frame with the placeholder, and a client that may not SHALL receive nothing about it. (DLT-06)
7. WHEN a message is deleted THEN every online session of an AI member of the room other than the author, whose delivery mode wanted the message, SHALL receive a `message_deleted` frame with the room, the trusted flag and the placeholder; a paused room SHALL send none, and a client that does not know the frame type SHALL ignore it and stay connected. A reconnecting AI SHALL receive a deleted message it has not received yet as the placeholder `[message deleted]`. (DLT-07)
8. WHEN an AI calls the tool `delete_message` with a message id THEN the channel SHALL delete that message of the AI's own through `DELETE /api/v1/agent/messages/{id}` and answer that it was deleted; a deletion SHALL use one of the AI's 30 posts a minute (TOOL-08). WHEN the channel receives a `message_deleted` frame THEN it SHALL hand the harness a notification of `type` `deleted` for the original `message_id` saying that the message was deleted, and SHALL NOT acknowledge it. (DLT-08)
9. WHEN the viewer opens the menu of a message they may delete (their own and, in a group they manage, any but a system notice) THEN the app SHALL offer "Delete"; choosing it SHALL ask for confirmation, saying that it is deleted for everyone and cannot be undone, and only confirming SHALL delete it; a failure SHALL be said and leave the message as it was. (DLT-09)
10. A deleted message SHALL show in the timeline as "Message deleted" in the place of its text and files, with its author and time, no menu and no Markdown; the conversation list SHALL show "Message deleted" as the preview of a deleted last message, and the quote of a deleted message SHALL say it too. (DLT-10)

**Independent Test**: Post a file with a caption in a group, choose Delete on it as its author and confirm: the row reads "Message deleted" in the same place, the file is gone from the files panel and, once nothing else references it, from disk; an AI member is sent a `message_deleted` frame; the group's creator, who did not write it, may delete another member's message the same way, and a plain member may not.

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| MEN-01 | P1: Mentions | Execute | Implementing |
| MEN-02 | P1: Mentions | Execute | Implementing |
| MEN-03 | P1: Mentions | Execute | Implementing |
| MEN-04 | P1: Mentions | Execute | Implementing |
| MEN-05 | P1: Mentions | Execute | Implementing |
| MEN-06 | P1: Mentions | Execute | Implementing |
| PDM-01 | P1: DM from participant | Execute | Implementing |
| PDM-02 | P1: DM from participant | Execute | Implementing |
| PDM-03 | P1: DM from participant | Execute | Implementing |
| PDM-04 | P1: DM from participant | Execute | Implementing |
| UPD-01 | P1: Update | Execute | Implementing |
| UPD-02 | P1: Update | Execute | Implementing |
| UPD-03 | P1: Update | Execute | Implementing |
| UPD-04 | P1: Update | Execute | Implementing |
| UPD-05 | P1: Update | Execute | Implementing |
| UPD-06 | P1: Update | Execute | Implementing |
| IMG-01 | P1: Images in the app | Execute | Implementing |
| IMG-02 | P1: Images in the app | Execute | Implementing |
| IMG-03 | P1: Images in the app | Execute | Implementing |
| IMG-04 | P1: Images in the app | Execute | Implementing |
| IMG-05 | P1: Images in the app | Execute | Implementing |
| IMG-06 | P1: Images in the app | Execute | Implementing |
| ENT-01 | P1: Enter on touch devices | Execute | Implementing |
| ENT-02 | P1: Enter on touch devices | Execute | Implementing |
| ENT-03 | P1: Enter on touch devices | Execute | Implementing |
| ENT-04 | P1: Enter on touch devices | Execute | Implementing |
| ENT-05 | P1: Enter on touch devices | Execute | Implementing |
| PRF-01 | P1: Profile panel | Execute | Implementing |
| PRF-02 | P1: Profile panel | Execute | Implementing |
| PRF-03 | P1: Profile panel | Execute | Implementing |
| PRF-04 | P1: Profile panel | Execute | Implementing |
| PRF-05 | P1: Profile panel | Execute | Implementing |
| PRF-06 | P1: Profile panel | Execute | Implementing |
| EDT-01 | P1: Edit a message | Execute | Implementing |
| EDT-02 | P1: Edit a message | Execute | Implementing |
| EDT-03 | P1: Edit a message | Execute | Implementing |
| EDT-04 | P1: Edit a message | Execute | Implementing |
| EDT-05 | P1: Edit a message | Execute | Implementing |
| EDT-06 | P1: Edit a message | Execute | Implementing |
| EDT-07 | P1: Edit a message | Execute | Implementing |
| EDT-08 | P1: Edit a message | Execute | Implementing |
| EDT-09 | P1: Edit a message | Execute | Implementing |
| EDT-10 | P1: Edit a message | Execute | Implementing |
| DLT-01 | P1: Delete a message | Execute | Implementing |
| DLT-02 | P1: Delete a message | Execute | Implementing |
| DLT-03 | P1: Delete a message | Execute | Implementing |
| DLT-04 | P1: Delete a message | Execute | Implementing |
| DLT-05 | P1: Delete a message | Execute | Implementing |
| DLT-06 | P1: Delete a message | Execute | Implementing |
| DLT-07 | P1: Delete a message | Execute | Implementing |
| DLT-08 | P1: Delete a message | Execute | Implementing |
| DLT-09 | P1: Delete a message | Execute | Implementing |
| DLT-10 | P1: Delete a message | Execute | Implementing |

**Coverage:** 53 total

---

## Success Criteria

- [ ] André mentions an AI by typing a few letters after `@`.
- [ ] André opens a DM with an agent from a group's participant list.
- [ ] André updates the notebook with `partyline update`.
- [ ] The product owner clicks a picture in a conversation, flips through the others and closes it without leaving the app.
- [ ] The product owner writes three lines on his phone, sends them with one tap, and on his computer Enter still sends.
- [ ] The product owner clicks a name in a message, a member list or a contact list and sees who it is, on a side panel (a drawer on his phone), then messages them or opens their page.
- [ ] The product owner fixes a typo in a message and takes back one sent by mistake; the manager of a group removes another member's message; the AIs in the conversation hear about both and the conversation keeps its order.
