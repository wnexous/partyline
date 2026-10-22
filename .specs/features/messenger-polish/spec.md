# Messenger Polish Specification

## Problem Statement

After v0.4.0, André asked for three things while testing (2026-10-22): mention intelligences with `@` while typing, open a private conversation by clicking an intelligence in a conversation's participant list, and update the CLI from the CLI itself. Today mentions work only by typing the exact handle, the participant list has no way to start a DM, and updating needs the install script.

## Goals

- [ ] Typing `@` in the composer suggests the conversation's participants and inserts `@handle`.
- [ ] Clicking an intelligence in a participant list opens (or starts) the DM with it.
- [ ] `partyline update` replaces the binary with the latest release after verifying its checksum.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Mentioning people who are not in the conversation | A mention only notifies participants |
| Auto-updating in the background | The user runs the command |
| Updating harness registrations during `update` | `partyline setup` does that |

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
5. IF no participant matches THEN the app SHALL show no list and Enter SHALL send as usual. (MEN-05)
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
6. The release lookup SHALL use HTTPS only and SHALL refuse redirects to other hosts than the release host. (UPD-06)

**Independent Test**: With a fake release server serving v9.9.9 and its checksums, `partyline update` replaces a copy of the binary and prints the new version; a wrong checksum leaves it unchanged.

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
| PDM-01 | P1: DM from participant | Execute | Pending |
| PDM-02 | P1: DM from participant | Execute | Pending |
| PDM-03 | P1: DM from participant | Execute | Pending |
| PDM-04 | P1: DM from participant | Execute | Pending |
| UPD-01 | P1: Update | Execute | Pending |
| UPD-02 | P1: Update | Execute | Pending |
| UPD-03 | P1: Update | Execute | Pending |
| UPD-04 | P1: Update | Execute | Pending |
| UPD-05 | P1: Update | Execute | Pending |
| UPD-06 | P1: Update | Execute | Pending |

**Coverage:** 16 total

---

## Success Criteria

- [ ] André mentions an AI by typing a few letters after `@`.
- [ ] André opens a DM with an agent from a group's participant list.
- [ ] André updates the notebook with `partyline update`.
