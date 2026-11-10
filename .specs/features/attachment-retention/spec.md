# Attachment retention

Status: Specify + Design written 2026-11-10, built the same day. Requirement ids RET-xx. Owner request
(2026-11-10): every file stays forever today; an instance needs a way to delete old files, to protect the
data disk and the privacy of what was shared.

## Problem
Room messages may be deleted by their room's retention (OPS-02, 90 days by default, 0 keeps them
forever), and their files go with them. A room that keeps its messages keeps its files too: photos,
voice notes and videos of years ago stay on the disk, and with files of gigabytes (LF-xx) the disk fills.
There is no way to keep a conversation and let its files go.

## Requirements
1. The instance SHALL have one setting "delete attachments older than N days": 0 keeps them forever,
   which is the default and today's behaviour. `PARTYLINE_ATTACHMENT_RETENTION_DAYS` gives its default
   value; WHEN an instance admin sets it in the web app THEN that value SHALL apply instead of the
   variable's until the admin goes back to the default. (RET-01)
2. Only an instance admin SHALL read or change the setting (`GET`/`PATCH /api/v1/admin/settings`); any
   other caller gets 403 (401 without a session). A value below 0 or above 36500 SHALL be refused with
   422. A change SHALL be recorded in the audit log with the old and new values. (RET-02)
3. WHILE the setting is above 0 the retention job SHALL mark every stored attachment of a room message
   older than N days `expired`, at start, every 24 hours and right after an admin changed the setting.
   (RET-03)
4. WHEN an attachment expires THEN its blob SHALL be deleted unless another attachment that is not
   expired, a platform queue file, an avatar, an organization or group photo, or a finished upload still
   references the same content; a blob written in the last hour is left to the next pass (it may belong to
   a message being posted). (RET-04)
5. WHEN a voice attachment expires THEN its server transcript SHALL be deleted with it. A transcript the
   sender's browser made is the message's text and SHALL stay. (RET-05)
6. A download of an expired attachment (`GET /api/v1/attachments/{id}`, `GET
   /api/v1/agent/attachments/{id}`) SHALL answer 410 with the code `attachment_expired`; a caller who
   cannot view the room still gets 404. (RET-06)
7. The web app SHALL show an expired attachment as a muted card "File expired" / "Arquivo expirado",
   with its name and no download, image or player, in the timeline and in the room's files list. (RET-07)
8. An AI SHALL get an expired attachment with status `expired` in its frames; its channel notification
   SHALL list it as `[attachment expired: <name>, <mime>]`, without an id to download, and
   `download_attachment` of its id SHALL fail with the `attachment_expired` error. (RET-08)
9. The job SHALL log counts only (attachments expired, blobs removed), never names or hashes. (RET-09)
10. Files of platform queue items (they go with their items, CID-09), avatars and organization or group
    photos SHALL NOT be touched by this setting. (RET-10)

## Design
- **Settings model:** there was no instance settings table, so migration `0038_instance_settings.sql`
  adds `instance_settings(key TEXT PRIMARY KEY, value TEXT, updated_at)`: a row exists only for a value
  an admin set. Package `internal/settings` (kernel) reads it: the effective retention is the row's
  value, else `config.AttachmentRetentionDays` (`PARTYLINE_ATTACHMENT_RETENTION_DAYS`, a non-negative
  integer, default 0). `PATCH /admin/settings` takes `attachment_retention_days` (a number) or
  `reset_attachment_retention: true` (delete the row: the variable applies again); both at once is 422.
  The answer, like `GET`, is `InstanceSettings{attachment_retention_days, attachment_retention_default_days,
  attachment_retention_overridden}`. The change and its audit entry (`instance.settings_update`, target
  kind `instance`, detail `{setting, from, to}`) commit in one transaction.
- **Expiry:** `rooms.Service.Cleanup` (the daily retention job) first deletes messages past their room's
  retention, then expires attachments: batches of 1000 rows `status = 'stored' AND created_at < now - N
  days` (partial index `attachments_stored_created`) are set `status = 'expired', sha256 = ''` and their
  `transcriptions` rows deleted, in one transaction per batch. Clearing the hash makes every existing
  reference count (`CountAttachmentsBySHA`, used by the blob sweep and `ReleaseBlobs`) ignore an expired
  row without a new query, and leaves no fingerprint of the content behind. Then the existing blob sweep
  runs and removes every blob no row references any more and older than an hour (RET-04). The name, type
  and size stay, so the card can say what was there.
- **When:** the job runs at start and every 24 hours (`RetentionInterval`); `Service.KickRetention` wakes
  it at once, and the settings service calls it after a change. Views already open are not pushed the
  change: they show the expired card on their next load, and a download in the meantime answers 410.
- **Download:** `downloadAttachment` and `agentAttachment` check the status after the access check and
  answer `410 attachment_expired` ("the file expired: this instance deletes attachments after N days").
  Signed file links of connectors resolve only stored attachments already.
- **Agents:** `dto.AttachmentStatusExpired = "expired"` travels in the message frames as every status does;
  `channel.contentOf` prints the line without the id; the channel client turns the 410 into the tool error
  `attachment_expired: ...`.
- **Web:** `AttachmentView` and the files list's `FileRow` already render a muted dashed card for a file
  that is not stored; they gain the `expired` label. The admin area gets a page `/admin/settings`
  (account menu, admin group) with a number field (days, 0 = keep forever), the server default shown,
  Save and "Use the server default".

## Tests
- Go `internal/rooms/attachment_retention_test.go`: `TestAttachmentRetentionExpiresOldFiles` (old
  attachments expire, a younger one stays stored, unshared blob deleted, transcript deleted, counts),
  `TestAttachmentRetentionKeepsSharedBlob` (a blob shared with a younger attachment stays),
  `TestAttachmentRetentionZeroKeepsEverything`, `TestAttachmentRetentionLeavesOtherBlobs` (avatar and
  platform file blobs stay), `TestKickRetentionRunsAtOnce`.
- Go `internal/settings/settings_test.go`: `TestAttachmentRetentionDefault`, `TestSetAttachmentRetention`
  (admin value wins, 0 included, reset, audit entry, no entry for an unchanged value),
  `TestSetAttachmentRetentionBounds`.
- Go `internal/api/attachment_retention_test.go`: `TestAdminSettingsRequireAdmin`,
  `TestAdminSettingsUpdate` (set, read, reset, 422), `TestExpiredAttachmentDownloadIsGone` (user and agent
  410, outsider 404, frame/message status `expired`).
- Go `internal/channel`: `TestContentOfExpiredAttachment`, `TestDownloadExpiredAttachment`.
- Go `internal/config`: `TestLoadAttachmentRetention` and the defaults/overrides/invalid tables.
- Web (vitest): `timeline-live.test.tsx` "expired files (RET-07)" (muted card, no link, image or player;
  Portuguese label), `room-files-live.test.tsx` "expired files (RET-07)", `admin.test.tsx` "admin instance
  settings (RET-01, RET-02)" (load, save, reset, client and server 422) and the 403 page and menu entry.
- Decision recorded as AD-040.
