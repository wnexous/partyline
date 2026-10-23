# Messenger Polish Specification

## Problem Statement

After v0.4.0, André asked for three things while testing (2026-10-22): mention intelligences with `@` while typing, open a private conversation by clicking an intelligence in a conversation's participant list, and update the CLI from the CLI itself. Today mentions work only by typing the exact handle, the participant list has no way to start a DM, and updating needs the install script.

On 2026-10-23 the product owner asked for three more things in the web app: images that open inside the app instead of in a new tab, an Enter key that does not send on a phone, and a profile panel that opens wherever an identity appears.

## Goals

- [ ] Typing `@` in the composer suggests the conversation's participants and inserts `@handle`.
- [ ] Clicking an intelligence in a participant list opens (or starts) the DM with it.
- [ ] `partyline update` replaces the binary with the latest release after verifying its checksum.
- [ ] An image in the timeline opens in a viewer inside the app, and the arrows walk the conversation's images (IMG-01..IMG-06).
- [ ] On a touch device Enter in the composer adds a new line and only the Send button sends (ENT-01..ENT-05).

## Out of Scope

| Feature | Reason |
| --- | --- |
| Mentioning people who are not in the conversation | A mention only notifies participants |
| Auto-updating in the background | The user runs the command |
| Updating harness registrations during `update` | `partyline setup` does that |
| Zoom, rotate, slideshow, video or audio in the viewer | Not asked; "Open original" opens the file itself |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Mention suggestions source | The conversation's participants that have a handle (AIs, users, people), filtered by the text typed after `@` (display name or handle, accents and case ignored), at most 8, the viewer excluded | Mentions resolve by handle (T12) | n |
| Mention keys | Up/Down move, Enter or Tab inserts `@handle ` (with a trailing space), Escape closes; the list closes when the caret leaves the `@word` | Common chat convention | n |
| Mobile | The same list above the composer, tappable | No hover on phones | n |
| Where a DM can be opened from | The participant list of a conversation (members panel) and the author name/avatar in the timeline; not for the viewer, not for people without a route (DM-05) | André's request; mirrors the contacts page action | n |
| DM action for people | Shown only when the viewer can DM them (the API answers `no_route_to_person` otherwise; the button is hidden for people) | Avoid a dead button | n |
| Update source | The same release channel as `install.sh`: the instance's `/install.sh` logic — latest GitHub release of the server's version, `checksums.txt` SHA-256 verified before replacing | One source of truth, integrity check | n |
| Update target | The running executable's path (`os.Executable`, symlinks resolved); written to a temp file in the same directory, chmod 0755, renamed over the old one; Windows prints the manual command | Atomic replace on Unix | n |
| `--check` | `partyline update --check` prints current and latest version and exits 0 when up to date, 1 when an update exists | Scripts can check | n |
| Images the viewer walks | The stored PNG, JPEG, GIF and WebP attachments of the messages loaded in the timeline, oldest first, one entry per attachment; older pages join the list as the timeline loads them | The same set the timeline shows inline (SVG and files the server did not store never open in the viewer) | n |
| Where the viewer opens from | The timeline. The files panel keeps its links to the files | Scope of the request: "an image attachment in the timeline" | n |
| What a touch device is | The main pointer is coarse (`(pointer: coarse)`) or it cannot hover (`(hover: none)`), read live from the media queries; a mouse or trackpad with hover is not, even on a touch laptop | Decided in one helper (`hooks/use-enter-sends.ts`); phones and tablets answer both, Chrome's device emulation too | n |
| Enter on a touch device | A new line; the Send button sends. Shift+Enter is a new line on both. With the mention list open Enter still completes the mention (MEN-03): that is a pick, not a send | The on-screen keyboard's Return key is for writing; sending is a deliberate tap | n |
| Viewer chrome | A paper strip on top (name, position, Download, Open original, Close) over a dark stage; previous and next sit on the stage's edges | Operate surface: standard controls, the image is the only dark thing on the screen | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Mention with @ ⭐ MVP

**User Story**: As a user, I want suggestions when I type `@` so that I mention an intelligence without knowing its exact handle.

**Acceptance Criteria**:

1. WHEN the user types `@` at the start of a word in the composer THEN the app SHALL show up to 8 participants of the conversation (viewer excluded) with avatar, display name, kind badge and @handle. (MEN-01)
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
3. WHEN the user clicks an author's name or avatar in the timeline THEN the app SHALL open that intelligence's profile, where "Message" is available. (PDM-03)
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

**Coverage:** 27 total

---

## Success Criteria

- [ ] André mentions an AI by typing a few letters after `@`.
- [ ] André opens a DM with an agent from a group's participant list.
- [ ] André updates the notebook with `partyline update`.
- [ ] The product owner clicks a picture in a conversation, flips through the others and closes it without leaving the app.
- [ ] The product owner writes three lines on his phone, sends them with one tap, and on his computer Enter still sends.
