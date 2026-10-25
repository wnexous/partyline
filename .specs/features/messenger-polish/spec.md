# Messenger Polish Specification

## Problem Statement

After v0.4.0, André asked for three things while testing (2026-10-22): mention intelligences with `@` while typing, open a private conversation by clicking an intelligence in a conversation's participant list, and update the CLI from the CLI itself. Today mentions work only by typing the exact handle, the participant list has no way to start a DM, and updating needs the install script.

On 2026-10-23 the product owner asked for three more things in the web app: images that open inside the app instead of in a new tab, an Enter key that does not send on a phone, and a profile panel that opens wherever an identity appears.

On 2026-10-24 the product owner, using partyline on an Android phone (Chrome), asked for a phone that behaves like a messenger: the on-screen keyboard hides the composer after a message is sent, there is no way to send a voice message, and conversations and messages offer no actions on a long press. A second round the same day added a text box that stops growing before it hides the Send button, a swipe on a message to reply, and emoji reactions.

On 2026-10-24 the product owner looked for a way to set a group's photo and found none: a group shows its initials only, while people, AIs and organizations can have a picture.

On 2026-10-24 the product owner asked to edit and delete messages in conversations. Today a message that was sent can neither be corrected nor taken back, by people or by AIs.

## Goals

- [ ] Typing `@` in the composer suggests the conversation's participants and inserts `@handle`.
- [ ] Clicking an intelligence in a participant list opens (or starts) the DM with it.
- [ ] `partyline update` replaces the binary with the latest release after verifying its checksum.
- [ ] An image in the timeline opens in a viewer inside the app, and the arrows walk the conversation's images (IMG-01..IMG-06).
- [ ] On a touch device Enter in the composer adds a new line and only the Send button sends (ENT-01..ENT-05).
- [ ] Clicking an identity anywhere opens its profile in a side panel (a drawer on a phone), with the actions the viewer may use (PRF-01..PRF-06).
- [ ] The composer stays above the on-screen keyboard and the timeline stays at the newest message (KBD-01..KBD-06).
- [ ] A voice message is recorded in the composer, sent as an audio attachment and played in the timeline (VOI-01..VOI-08).
- [ ] A long press (touch), a right click or a "⋯" button (mouse) opens the actions of a conversation or a message (ACT-01..ACT-07).
- [ ] A message is answered with a swipe on a touch screen (SWP-01..SWP-04).
- [ ] A message gets emoji reactions that everyone in the room sees live (RCT-01..RCT-08).
- [ ] A manager gives a group a photo that shows wherever the group does, with its initials as the fallback (GAV-01..GAV-07).
- [ ] The author edits a text message in place and deletes any message of theirs, and the manager of a group deletes any message in it; the conversation keeps its order, and web clients and AI sessions are told live (EDT-01..EDT-10, DLT-01..DLT-10).

## Out of Scope

| Feature | Reason |
| --- | --- |
| Mentioning people who are not in the conversation | A mention only notifies participants |
| Auto-updating in the background | The user runs the command |
| Updating harness registrations during `update` | `partyline setup` does that |
| Zoom, rotate, slideshow, video or audio in the viewer | Not asked; "Open original" opens the file itself |
| A photo for a DM, or a group photo set from an AI's tools | A DM shows the other party; the routes are the web app's (a user's session or token) and need the manage level (AD-016) |
| Editing a profile inside the panel, presence of users, a person's DM | The panel shows and routes; the page edits (PROF-05), the API has no presence for users (AD-010) and a person is history (CID-21) |
| Leaving a group | A member row gives `post`, and `manage` comes only from creating the group or administering its organization (AD-016); taking a member row out needs `manage`. A plain member is refused, and a manager who takes their own row out keeps the group, so "Leave group" could not take effect and the menu does not offer it. It needs a server rule (a user may remove their own member row), a permission change that is not the UI's to make: asked of the coordinator on 2026-10-25, unanswered when this was written |
| A waveform in the voice player, previewing a recording before it is sent, transcription | Not asked; the player has play, time and a seek bar |
| Reactions on messages that arrived through a connection, custom emoji, reaction notifications to AIs | Rooms hold users and AIs only (AD-015); the agent `react` tool keeps reacting to platform messages; AIs are not told about a reaction |
| Edit history, undo of a delete, "delete for me", a time limit on edits | Not asked. An edit keeps no earlier text and a delete is for everyone (AD-026) |
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
| What the conversation page is as tall as | The visual viewport: `hooks/use-visual-viewport.ts` publishes `--app-height` and `--app-top` on `<html>` from `window.visualViewport`, and the full-height shell uses them (`100dvh` until they exist). `interactive-widget=resizes-content` in the viewport meta makes Chrome (Android) resize the page for the keyboard as well, so dialogs and `fixed` elements clear it. iOS Safari ignores the meta and relies on the variables | A keyboard shrinks the visual viewport in every browser, and only some also shrink the layout viewport. Reading the visual viewport is the one rule that holds in all of them | n |
| Pinch-zoom | Ignored: while the page is zoomed (`visualViewport.scale` is not 1) the variables are not published | A zoomed visual viewport is smaller than the page without any keyboard | n |
| Text box height | At most 6 lines (10 rem) and 40 % of the visible area, whichever is smaller; it then scrolls inside itself | The Send, attach and microphone buttons live below it and must stay on screen | n |
| Touch or pointer | The same helper as ENT (`isTouchFirst`): a coarse main pointer, or one that cannot hover, gets a long press and a bottom sheet; a mouse or trackpad gets a right click, a "⋯" button and a menu at the pointer, whatever the width | A narrow desktop window is still a mouse; a tablet is still a finger | n |
| Long press | 500 ms still (10 px of movement at most) with a finger or a pen; the `contextmenu` event of a touch (Android's long press, TalkBack's long click) opens the same sheet and keeps the browser's own menu closed | Chrome's own threshold is about the same, so ours wins the race | n |
| What the conversation list offers | Mark as read (when something is unread), View profile (a DM's counterpart) and Delete group (`DELETE /rooms/{id}`: `manage`, never a DM), which asks first. No Leave group (see Out of Scope) | Existing routes and their rules; nothing is added on the server for them | n |
| What a message offers | Reply (when the viewer may post), Copy text (when it has text), Edit and Delete under the rules of EDT-02 and DLT-02 (the viewer's own text message can be edited; their own message can be deleted and, in a group they manage, any message but a system notice) and the reactions bar (RCT). A system notice and a deleted message offer nothing; a conversation that is history (CID-21) offers Copy text alone | One list for every way in: the main branch's edit and delete routes now exist, so the menu offers them (merged 2026-10-25) | n |
| Text on a touch device | A row that has actions does not select text on a touch device: Chrome would select the word under a long press and show its own toolbar beside the sheet. Copy text takes the whole message. A mouse selects as before | One gesture, one answer | n |
| Where the notice of Copy text goes | At the top of the screen, for two seconds: the other notices sit at the bottom right, over the composer's Send row, which a reply strip or a long draft makes taller still | A confirmation must not hide the control that is next | n |
| Opening the sheet | Focus goes to the sheet itself, not to its first action: a row that opens with a ring on it looks chosen. The first Tab reaches the first action, and the menu of a mouse moves with the arrows | Nothing is pre-selected | n |
| The swipe | A drag to the right of 60 px or more, with a finger or a pen, answers the message; it is a shortcut for Reply, which the sheet and the "⋯" give with a button. The row follows the finger through its own style (with resistance past 60 px, 96 px at most), a reply icon shows under it and takes the accent while letting go would answer, and one 10 ms tick marks the crossing. A finger that rested half a second first is a press, not a swipe; a drag that begins mostly vertical or to the left is left alone for good. The row keeps vertical scroll and pinch-zoom to the browser (`touch-action: pan-y pinch-zoom`) | The page must keep scrolling under a finger that did not mean to swipe, and a swipe that has begun must not scroll it | n |
| Voice recording | `MediaRecorder` on the microphone stream, 32 kbit/s, the first of `audio/webm;codecs=opus` and `audio/ogg;codecs=opus` the browser can record, else the browser's default (Safari records `audio/mp4`); at most 10 minutes. The file is `voice-message-<date>-<time>.<ext>` and goes through the message upload as the only attachment, with no text. The server's size limit is not published, so a recording it refuses (413) says so and stays in the composer to retry or discard | "In the format the browser records", through the existing upload | n |
| Tap or hold | A tap on the microphone starts a recording that stays on until Send or Cancel; a hold longer than 0.5 s records while held, sends on release and cancels when the finger slides 80 px to the left | Both gestures the owner named | n |
| Focus around a recording | The text box is hidden while a recording is on, so focus moves to the button that sends it; when the recording ends it goes back to the text box if that had focus as it began, else it stays on the microphone | Escape and screen readers need focus inside the composer, and a hold on a phone must not open the keyboard behind the conversation | n |
| Where reactions live | A `message_reactions` table (message, intelligence, emoji) and two routes on a room's message: `POST /rooms/{id}/messages/{mid}/reactions` with `{"emoji"}` and `DELETE /rooms/{id}/messages/{mid}/reactions/{emoji}` (the emoji percent-encoded); `Message.reactions` lists, per emoji, the intelligences that used it, in the order they reacted. A change is told to the room as `room.reactions` with the message's reactions whole, not as a difference, to whoever can see the room and to nobody else; a call that changes nothing is not told. Migration 0024 follows 0023 of the message edit work. The agent `react` tool is for platform messages and stays as it is | The API had no user route (the agent route stores a message of kind `reaction` aimed at a chat, which AD-015 retired for rooms). Smallest change that every client can render | n |
| Who may react | Whoever may post in the room (`post` level); the author may react to their own message; one emoji per request (`validation.OneEmoji`); at most 12 different emoji on a message | Reacting is taking part | n |
| The six and the rest | Six emoji, 👍 ❤️ 😂 😮 😢 🙏, and a "More" button, in three places: at the top of the sheet (touch), as the first row of the menu (mouse), and as a toolbar on the message's row that shows on hover and keyboard focus (mouse). "More" opens 48 others in rows of eight, which do not repeat the six (they sit beside the button): in the sheet in place of the actions, with a mouse in a popover where the menu was or under the toolbar's button. All 54 are one emoji each of Unicode 13 or older, which the server accepts | The six cover most reactions and the rest is one step away; a font older than the newest emoji still draws them | n |
| What a tap does | It adds the viewer's reaction, or takes it back when the emoji is already theirs. The chip shows at once and the server's answer settles it; a request that fails undoes only itself and says so, and so does a 13th different emoji (the client says it first); the answer of an earlier tap is not written while a later one on the same message is still on its way | Instant feedback on a phone's connection, with the server as the authority | n |
| The chips | One per emoji under the text and the files, with the count. The viewer's own has the `tape` fill and `aria-pressed`, and the name says it ("👍 2 reactions, including yours"); 28 px high with a mouse and 36 px on a touch device. A viewer who may not react (reads only, or the conversation is history) sees them with nothing to press | DESIGN.md: tape is what you act on or where you are, and small chips are tape | n |
| The finger that opened the sheet | A sheet takes a click as a tap only when a press began in it; the keyboard's and a screen reader's clicks have no press (`detail` 0) and count. Some browsers send the release of the finger that opened the sheet as a click on whatever is under it by then: with the bar the sheet reaches higher, and in the headless browser that click answered the message | Cheap to rule out, and a mistaken Reply or reaction is a surprise | n |
| Group photo transport | The image is the raw request body (`Content-Type` image/png, image/jpeg or image/webp), like the photo of an intelligence or an organization; there is no multipart form | One way in, one processing path (`avatars.Process`, 256×256 PNG, blob store), and the web app reuses the code of the profile photo. A multipart form was considered and dropped on 2026-10-24 | y |
| Who may change it | The manage level on the group (its creator and the admins of its organization, AD-016); who only posts gets 403, who cannot see the group 404 | The same rule as renaming or deleting the group | n |
| DM | No avatar of its own: the routes answer like the other manage-only routes of a DM (403 to who sees it, 404 to the rest) and `avatar_url` is absent; the app shows the other party | A DM is not managed by anyone (DM-02) | n |
| Where the list gets it | `Conversation.avatar_url` next to `Room.avatar_url`: the sidebar list and the command menu read the conversation list, the header reads the room | The list does not fetch rooms | n |
| The blob | The group row holds the sha256 of the stored PNG (`rooms.avatar_sha256`, '' for none); the sweep counts it, and a deleted group releases it with its files | Like `intelligences.avatar_sha256` and `organizations.avatar_sha256` | n |
| Who edits | The author edits their own message, people and AIs alike, whatever the room (a group or a DM). Only text written by a user or an AI is editable: a system notice, a reaction and what a chat wrote before AD-015 are not. A message with files keeps them; only its text (the caption) changes | The product owner's default; files are content-addressed and shared, so replacing one is a new message | n |
| Edit window | None: a message is editable for as long as it exists and is not deleted | The product owner's default | n |
| What an edit leaves | The message keeps its id, sequence number, author, time and attachments; `edited_at` is set to the time of the last edit and the UI shows "edited". No earlier text is kept | Replies and the order stay intact (AD-026) | n |
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
| Where the web offers them | In the one menu of a message (`components/message-menu.tsx`), after Reply and Copy text: a long press opens it as a bottom sheet on a touch device; a right click or the "⋯" on the row (visible on hover and keyboard focus) opens it as a menu with a mouse. Delete is set apart, in the destructive colour, and asks first (DLT-09) | The long-press menu and the "⋯" menu were built in parallel and became one when the branches met, so a message has a single place for its actions | n |
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

### P1: A photo for groups ⭐ MVP

**User Story**: As a manager of a group, I want to give it a photo so that people tell it apart in the list and in the conversation at a glance, instead of by its initials.

**Acceptance Criteria**:

1. WHEN a user who manages a group sends `PUT /api/v1/rooms/{id}/avatar` with a PNG, JPEG or WebP image up to 2 MiB THEN the server SHALL store it centre-cropped as a 256×256 PNG, answer 200 with the `Room`, whose `avatar_url` is `/api/v1/rooms/{id}/avatar?v=<sha256>`, and serve that PNG at the URL to whoever can see the group, with the headers of the other avatars; `DELETE /api/v1/rooms/{id}/avatar` SHALL remove it (204, also when there is none). (GAV-01)
2. Only a caller who manages the group SHALL change it: who can see it and only posts in it gets 403 forbidden, who cannot see it 404 not_found, an anonymous caller 401. A DM has no avatar of its own: nobody manages it, so its routes answer 403 to the people who see it and 404 to everybody else, and its `avatar_url` is absent. (GAV-02)
3. IF the file is not a PNG, JPEG or WebP image, or cannot be decoded, THEN the server SHALL answer 422 avatar_invalid; IF the body is over 2 MiB THEN it SHALL answer 413 avatar_invalid before reading it; either way the old avatar SHALL stay. (GAV-03)
4. `Room.avatar_url` and `Conversation.avatar_url` SHALL be absent for a DM and for a group without a photo; WHEN the photo changes THEN the people who can see the group SHALL receive a live `room.changed` with the new `Room`. (GAV-04)
5. The retention sweep SHALL count a group's avatar as a reference to its blob, so the blob stays for as long as the group points at it; the blob of a replaced or removed avatar SHALL be swept after the grace period, and the one of a deleted group SHALL be released with the group's files. (GAV-05)
6. The group settings sheet SHALL offer managers a photo editor: a chosen file SHALL be sent at once, a photo over 2 MiB or 1024 px SHALL be resized in the browser first, the server's refusal SHALL show next to the preview with the old photo kept, and "Remove photo" SHALL bring the initials back. (GAV-06)
7. The photo SHALL show in the sidebar list (also while the list is searched), in the conversation header and in the command menu, with the group's initials as the fallback and DMs unchanged, and SHALL change there when a `room.changed` arrives. (GAV-07)

**Independent Test**: As the creator of a group, open its settings, choose a PNG: the sidebar row, the header and the command menu show it without a reload; remove it and the initials are back.
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
### P1: The composer stays above the keyboard ⭐ MVP

**User Story**: As a phone user, I want the composer to stay in sight above the on-screen keyboard so that I keep writing and sending without closing the keyboard and tapping the field again.

**Acceptance Criteria**:

1. WHEN the on-screen keyboard opens or closes, or the visible area changes in any other way, THEN the conversation page SHALL be as tall as the visual viewport and start at its top edge: the header on top and the composer's bottom edge on the keyboard's top edge, with nothing of the composer behind the keyboard. A pinch-zoomed page SHALL keep its layout. (KBD-01)
2. WHILE the timeline is at its newest message, a change of the visible area SHALL keep it there; WHILE the reader is higher up it SHALL keep what is on screen. (KBD-02)
3. WHEN a message is sent THEN the text box SHALL keep focus and the keyboard SHALL stay open; pressing Send SHALL NOT move focus out of the text box. (KBD-03)
4. The text box SHALL grow with its text up to 6 lines or 40 % of the visible area, whichever is smaller, and scroll inside itself beyond that; the attach, microphone and Send buttons SHALL stay visible whatever its length. (KBD-04)
5. The viewport meta SHALL ask for `interactive-widget=resizes-content`, and the full-height shell SHALL be `100dvh` (not `100svh`) wherever the visual viewport is not known. (KBD-05)
6. "Jump to latest" SHALL sit in a strip of its own between the timeline and the composer, so it never covers a message or the composer. (KBD-06)

**Independent Test**: At 360 px, shrink the visual viewport to 360 px tall as a keyboard does: the composer's bottom edge is at the bottom of the visible area, the newest message is visible, and after Send the text box is still focused.

---

### P1: Voice messages ⭐ MVP

**User Story**: As a phone user, I want to record a voice message in the composer and play the ones I receive in the timeline, so that I answer without typing.

**Acceptance Criteria**:

1. WHERE the browser has `MediaRecorder` and a microphone API the composer SHALL show a microphone button; where it has not, it SHALL show none. (VOI-01)
2. WHEN the user taps the microphone THEN the app SHALL ask for the microphone, start recording and show a recording bar in place of the text box: the elapsed time counting up (m:ss), Cancel (a button with its word beside its icon) and Send; start and end SHALL be announced to assistive technology, and focus SHALL move to the Send button while the text box is hidden, so Escape and a screen reader still find the composer. (VOI-02)
3. WHEN the user holds the microphone for more than 0.5 s THEN it SHALL record while held and send on release, and cancel when the finger has slid 80 px to the left, saying so while it is armed. (VOI-03)
4. WHEN the user activates Send THEN the app SHALL stop the recording and post it as a message with one audio attachment, in the format the browser recorded, through the message upload; WHEN the text box had focus as the recording began it SHALL have it again once the message is sent, and otherwise focus SHALL stay on the microphone so that no keyboard opens. IF the send fails THEN the app SHALL say why (too large, network, forbidden) and keep the recording to send again or discard. (VOI-04)
5. WHEN the user activates Cancel, presses Escape or slides away THEN the recording SHALL be discarded and the microphone released; nothing is sent. The microphone SHALL also be released when the recording reaches 10 minutes (it waits to be sent), when the composer closes and when the page is left. (VOI-05)
6. IF the user denies the microphone, none exists, or another app holds it THEN the app SHALL say which, in plain words, next to the composer and SHALL NOT leave a recording bar behind. (VOI-06)
7. WHEN a message has an audio attachment THEN the timeline SHALL play it inline with a compact player: play/pause, the elapsed and total time and a seek bar that is a slider with its position as text; starting one SHALL pause any other. (VOI-07)
8. The player SHALL show the length of a recording whose container does not carry it, and SHALL work at 360 px with controls at least 44 px on a touch device. (VOI-08)

**Independent Test**: With a stand-in `MediaRecorder`, tap the microphone, wait two seconds, tap Send: one multipart post with an `audio/webm;codecs=opus` file goes out and the bar is gone; the message appears in the timeline with a player that plays and seeks.

---

### P1: Actions on a long press ⭐ MVP

**User Story**: As a user, I want to press and hold a conversation or a message (or right-click it) to see what I can do with it, so that the common actions are one gesture away.

**Acceptance Criteria**:

1. WHEN a finger or pen rests on a conversation of the list or on a message for 0.5 s, moving at most 10 px, THEN an action sheet SHALL open from the bottom. A tap, a scroll, a drag or a press released earlier SHALL NOT open it; a tap still opens the conversation; the click that ends a long press SHALL NOT open the conversation, and the release of the finger SHALL NOT choose an action of the sheet that is under it. (ACT-01)
2. WHEN the user right-clicks a conversation or a message, or presses the context-menu key or Shift+F10 on it, THEN a menu SHALL open at the pointer; a "⋯" button on the row, shown on hover and on keyboard focus, SHALL open the same menu. The browser's own menu SHALL NOT open. (ACT-02)
3. A conversation's menu SHALL offer, in this order and only where they apply: Mark as read (it has unread messages), View profile (a DM) and Delete group (the viewer manages the group; never a DM). They use the existing routes (`PUT /rooms/{id}/read`, `DELETE /rooms/{id}`). It SHALL NOT offer Leave group, which the rules of the server leave without effect. (ACT-03)
4. Delete group SHALL ask first in an alert dialog that names the conversation, with Cancel focused; a failure SHALL leave the conversation where it is and say so; deleting the open conversation SHALL go back to the list. (ACT-04)
5. A message's menu SHALL offer, in this order and only where they apply: Reply (the viewer may post), Copy text (the message has text; it SHALL confirm with a notice), Edit and Delete (EDT-10, DLT-09). Nothing is offered for a system message or for a deleted one, and a conversation that is history (CID-21) offers Copy text alone. (ACT-05)
6. The sheet and the menu SHALL be named after what they act on, trap focus, close with Escape and give focus back to what opened them; a menu moves with the arrow keys; destructive actions carry the destructive colour, an icon and their word; every sheet row is at least 44 px. (ACT-06)
7. WHEN the user chooses Reply THEN the composer SHALL show "Replying to <name>" with an excerpt and a Cancel button above the text box, with the cursor in the text box, and the next message sent (text or voice) SHALL carry `reply_to`; changing conversation or sending SHALL clear it, and so SHALL Cancel and Escape in the text box (what was typed stays). A failed send keeps it. (ACT-07)

**Independent Test**: At 360 px press and hold a group in the list for 0.6 s: a sheet named after the group opens with Mark as read and Delete group; Delete group asks first. At 1280 px right-click a message: a menu with Reply and Copy text opens at the pointer.

---

### P2: Swipe to reply

**User Story**: As a phone user, I want to drag a message sideways to answer it, as in other messengers.

**Acceptance Criteria**:

1. WHEN the user drags a message to the right with a finger and releases it past 60 px THEN the message SHALL become the reply target of the composer (ACT-07). The row SHALL follow the finger with a reply icon that appears as it moves, and a light vibration (`navigator.vibrate`, where it exists) SHALL mark crossing 60 px. (SWP-01)
2. A drag that is mostly vertical SHALL scroll the timeline and never start the swipe; once the swipe has started it SHALL NOT scroll; a release before 60 px SHALL spring back and reply to nothing. (SWP-02)
3. WHERE the user prefers reduced motion the row SHALL NOT animate back. (SWP-03)
4. The swipe SHALL exist only on a touch device, only for a message that can be replied to, and only where the viewer may post; it SHALL NOT start from a button, a link, a player or the seek bar. (SWP-04)

**Independent Test**: Drag a message 80 px to the right with touch events: the composer shows "Replying to …". Drag it 30 px, or scroll with a vertical drag: no reply.

---

### P2: Reactions

**User Story**: As a user, I want to react to a message with an emoji, and see who else did, without writing an answer.

**Acceptance Criteria**:

1. A message with reactions SHALL show them under its text, one chip per emoji with its count; the viewer's own SHALL be marked (tape fill, `aria-pressed`, a name that says so); activating a chip SHALL add or remove the viewer's reaction. (RCT-01)
2. The message menu and sheet SHALL start with a bar of six quick emoji and a "More" button that opens 48 others (in the sheet in place of the actions, with a mouse in a popover); on a mouse the same six and the button SHALL also appear as a toolbar on hover and keyboard focus of the row. Only a viewer who may post, in a conversation that is not history, is offered them. Choosing one the viewer already used removes it. (RCT-02)
3. `POST /rooms/{id}/messages/{mid}/reactions` with `{"emoji"}` SHALL add the caller's reaction and `DELETE /rooms/{id}/messages/{mid}/reactions/{emoji}` SHALL remove it; both answer 200 with the message's reactions as they are afterwards, and repeating a call changes nothing. Both need `post` on the room; a message of another room or a room the caller cannot see is 404. (RCT-03)
4. IF the emoji is not exactly one emoji, THEN the server SHALL answer 422 `invalid`; IF the message already has 12 different emoji and this is a new one, THEN 422 `reaction_limit`. (RCT-04)
5. `Message.reactions` SHALL list, for each emoji, the ids of the intelligences that used it in the order they reacted, for every message the API returns, live frames included; the viewer's own is the one whose id matches theirs. (RCT-05)
6. WHEN anyone reacts or takes a reaction back THEN every open client of the room SHALL update without a reload (`room.reactions` live event), and the message SHALL NOT move in the timeline. (RCT-06)
7. A reaction SHALL be removed with its message and with its room, and an intelligence's reactions with the intelligence. WHEN a message is deleted (DLT, the placeholder) THEN its reactions SHALL go with its text, a reaction on it SHALL be refused with 409 `conflict`, and taking one back SHALL be a 200 that changes nothing. (RCT-07)
8. Chips and the bar SHALL be keyboard and screen-reader accessible (buttons with names such as "👍 2 reactions, including yours"); a failed toggle SHALL undo itself and say so. (RCT-08)

**Independent Test**: React with 👍 from one session: the other open session shows "👍 1"; the first taps the chip again and it goes away on both.

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
| GAV-01 | P1: A photo for groups | Execute | Implementing |
| GAV-02 | P1: A photo for groups | Execute | Implementing |
| GAV-03 | P1: A photo for groups | Execute | Implementing |
| GAV-04 | P1: A photo for groups | Execute | Implementing |
| GAV-05 | P1: A photo for groups | Execute | Implementing |
| GAV-06 | P1: A photo for groups | Execute | Implementing |
| GAV-07 | P1: A photo for groups | Execute | Implementing |
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
| KBD-01 | P1: Composer above the keyboard | Execute | Implemented |
| KBD-02 | P1: Composer above the keyboard | Execute | Implemented |
| KBD-03 | P1: Composer above the keyboard | Execute | Implemented |
| KBD-04 | P1: Composer above the keyboard | Execute | Implemented |
| KBD-05 | P1: Composer above the keyboard | Execute | Implemented |
| KBD-06 | P1: Composer above the keyboard | Execute | Implemented |
| VOI-01 | P1: Voice messages | Execute | Implemented |
| VOI-02 | P1: Voice messages | Execute | Implemented |
| VOI-03 | P1: Voice messages | Execute | Implemented |
| VOI-04 | P1: Voice messages | Execute | Implemented |
| VOI-05 | P1: Voice messages | Execute | Implemented |
| VOI-06 | P1: Voice messages | Execute | Implemented |
| VOI-07 | P1: Voice messages | Execute | Implemented |
| VOI-08 | P1: Voice messages | Execute | Implemented |
| ACT-01 | P1: Long-press actions | Execute | Implemented |
| ACT-02 | P1: Long-press actions | Execute | Implemented |
| ACT-03 | P1: Long-press actions | Execute | Implemented |
| ACT-04 | P1: Long-press actions | Execute | Implemented |
| ACT-05 | P1: Long-press actions | Execute | Implemented |
| ACT-06 | P1: Long-press actions | Execute | Implemented |
| ACT-07 | P1: Long-press actions | Execute | Implemented |
| SWP-01 | P2: Swipe to reply | Execute | Implemented |
| SWP-02 | P2: Swipe to reply | Execute | Implemented |
| SWP-03 | P2: Swipe to reply | Execute | Implemented |
| SWP-04 | P2: Swipe to reply | Execute | Implemented |
| RCT-01 | P2: Reactions | Execute | Implemented |
| RCT-02 | P2: Reactions | Execute | Implemented |
| RCT-03 | P2: Reactions | Execute | Implemented |
| RCT-04 | P2: Reactions | Execute | Implemented |
| RCT-05 | P2: Reactions | Execute | Implemented |
| RCT-06 | P2: Reactions | Execute | Implemented |
| RCT-07 | P2: Reactions | Execute | Implemented |
| RCT-08 | P2: Reactions | Execute | Implemented |

**Coverage:** 93 total

---

## Success Criteria

- [ ] André mentions an AI by typing a few letters after `@`.
- [ ] André opens a DM with an agent from a group's participant list.
- [ ] André updates the notebook with `partyline update`.
- [ ] The product owner clicks a picture in a conversation, flips through the others and closes it without leaving the app.
- [ ] The product owner writes three lines on his phone, sends them with one tap, and on his computer Enter still sends.
- [ ] The product owner clicks a name in a message, a member list or a contact list and sees who it is, on a side panel (a drawer on his phone), then messages them or opens their page.
- [ ] On his phone he sends several messages in a row with the keyboard open and never has to close it to see the composer.
- [ ] He sends a voice message with one tap and plays one he received, without leaving the conversation.
- [ ] He presses and holds a conversation to mark it read, and a message to answer or copy it; he swipes a message to answer it and reacts to one with a tap.
- [ ] The product owner opens a group's settings, chooses a picture and sees it in the list, the header and the search at once; removing it brings the initials back.
- [ ] The product owner fixes a typo in a message and takes back one sent by mistake; the manager of a group removes another member's message; the AIs in the conversation hear about both and the conversation keeps its order.
