# Messenger polish contract notes

Binding for later batches and the Verifier. One section per story. The feature is small, so it has no
`design.md` or `tasks.md`: the Assumptions table of `spec.md` is the design.

## Update the CLI - `partyline update` (UPD-01..UPD-06)

`internal/cli/update.go`; tests `update_test.go` (the ACs) and `update_release_test.go` (a fake HTTPS
release server and an installed binary in a temp directory). 40 tests, 232 with subtests.

- **Channel (assumption "Update source").** The one `install.sh` uses, and the instance is not asked:
  "the server's version" is read as the project's releases. `PARTYLINE_RELEASES_API` (default
  `https://api.github.com/repos/wnexous/partyline/releases/latest`) answers `{"tag_name": …}`; the files are
  `PARTYLINE_RELEASE_BASE` (default `https://github.com/wnexous/partyline/releases/download`) + `/<tag>/` +
  `partyline_<version>_<os>_<arch>.tar.gz` and `checksums.txt` (`<version>` is the tag without `v`;
  `linux`/`darwin`/`windows` × `amd64`/`arm64`, the `.zip` for Windows). A checksums.txt line is
  `<hex>  <name>` or `<hex> *<name>` (what `install.sh` matches); upper-case hex, CRLF and other platforms'
  lines are fine, a path or a longer name is not the asset. `TestUpdateAndInstallShShareOneReleaseChannel`
  fails when `install.sh` and the Go constants drift apart.
- **The tag is data.** It becomes part of download URLs, so it must be a version (`v?N.N.N[-pre][+build]`;
  anything else, a path included, stops the update after the lookup) and gets the `v` `install.sh` adds.
- **UPD-06, HTTPS only.** Both URLs are checked before any request, for `--check` too: scheme `https` (any
  case), a host, no user info. Plain `http` is refused even for loopback (`install.sh` allows loopback; the
  AC says "HTTPS only"). The value is never printed (it could hold a password):
  `partyline update: refusing PARTYLINE_RELEASES_API: want an https:// URL with a host and without user info`.
- **UPD-06, redirects, and the one deviation.** `checkReleaseRedirect` (the `http.Client` policy, also applied
  to an injected client): at most 4 redirects; the target must be https and have the host and port (443 by
  default) of the **first** request of the chain. Exception, **github.com only** (never `api.github.com`, a
  mirror or a look-alike): its target may be `release-assets.githubusercontent.com`,
  `objects.githubusercontent.com` or `github-releases.githubusercontent.com` on 443. Why: a HEAD request
  (2026-10-22, a public release asset of another project) answered `302` with
  `Location: https://release-assets.githubusercontent.com/…`; GitHub hands every release download to its asset
  host, so "the release host only" would make every real update fail. The text of UPD-06 in `spec.md` says it
  now. The error names the target's host only (`refusing redirect to <host>: …`): redirect URLs carry a
  signature. Integrity does not depend on the CDN: the archive is checked against `checksums.txt`.
- **Order of work.** flags → channel URLs → lookup → compare → (`--check`: print and stop) → platform →
  Windows → executable path → a temporary file next to the executable → `checksums.txt` → archive → SHA-256 →
  unpack into the temporary file → `Sync`, `Close`, `chmod 0755` → `rename` over the executable. Only the
  last step touches the executable; every failure removes the temporary file. Creating the file first is the
  writability test (UPD-05) and happens before any download. The temporary name is `.partyline-update-*`.
- **Target.** `stdio.executable` (default `os.Executable`) with symlinks resolved: the file a link points to
  is replaced and the link stays; the directory that must be writable is that file's. Only the directory
  matters: a read-only binary in a writable directory is replaced (mode 0755), a writable one in a read-only
  directory is not.
- **Version order** (`releaseIsNewer`): numeric `major.minor.patch`, then semver pre-release rules (a release
  follows its pre-releases; numeric identifiers compare as numbers and precede the others; more identifiers
  win); build metadata ignored. A build from git past a tag (`v0.4.0-3-g0123abc`, `-dirty`) counts as that
  tag, so it is up to date with `v0.4.0`. A running version that is not a version (`dev`, `dev-<sha>`, a bare
  hash) is older than every release, so `update` replaces it. A running version newer than the latest release
  is up to date; nothing downgrades.
- **Output.** Streams: results on stdout, every error on stderr, prefixed `partyline update: `. Exactly one
  line each: `Updated partyline <old> → <new>` and `partyline <version> is up to date` (the versions are
  `buildVersion()` and the tag). `--check` prints `current: <v>`, `latest:  <v>` and then either the up-to-date
  line or `Run partyline update to install it.`; exit 0 up to date, 1 newer exists. A failed lookup in `--check`
  is exit 1 with an error on stderr and **nothing on stdout**, which is how a script tells it from "newer
  exists". Exit codes: 0 done or up to date, 1 failure or newer (`--check`), 2 usage (an unknown flag or any
  argument; `-h`).
- **UPD-02 messages.** `checksum mismatch for <asset>: nothing was installed`, `<asset> is not listed in
  checksums.txt`, `download <asset>: HTTP 404` (or the transport error, unwrapped from the client's), `could
  not unpack <asset>: …`, `<asset> has no partyline binary` (the file must be a regular `partyline` at the top
  of the archive, `./partyline` accepted), `<asset>: partyline is empty`, `cannot find the latest release: …`.
  Every malformed or missing checksum is a mismatch or "not listed"; none installs.
- **Bounds** (`updateLimits`; tests shrink them): lookup 1 MiB, `checksums.txt` 1 MiB, archive 256 MiB, unpacked
  binary 512 MiB; a body of exactly the limit passes, one byte more fails with `larger than`. Timeouts: 30 s
  for the lookup and `checksums.txt`, 10 min for the archive, both under the command's context (Ctrl+C ends it
  with exit 1 and cleans up).
- **UPD-05.** `<dir> is not writable (<os reason>); partyline was not changed`, then the installer command of
  the instance (`PARTYLINE_URL`, else the saved config, else `https://<your instance>`; quoted for the shell when
  it holds anything but plain characters): `curl -fsSL <instance>/install.sh | sh`, and the same aimed at this
  copy with `| PARTYLINE_INSTALL_DIR=<dir> sh` (the installer writes to `~/.local/bin` otherwise). On stderr,
  exit 1. Being up to date, `--check` and Windows never need write access.
- **Windows.** A running program cannot be replaced there: when an update exists, exit 1 with the zip of the
  release (`<base>/<tag>/partyline_<version>_windows_<arch>.zip`) and `go install github.com/wnexous/partyline/cmd/partyline@<tag>`;
  nothing is downloaded or changed.
- **Seams for tests and black-box checks.** `stdio.updateHTTP` (the client; its transport is kept, so a test
  client that trusts the fake server's certificate works, and the redirect policy is still applied),
  `stdio.goos`/`stdio.goarch` (empty: `runtime`), `stdio.executable`, `stdio.getenv` for the two variables. A
  built binary trusts a self-signed fake server through `SSL_CERT_FILE`. No test reaches the network: the
  GitHub-shaped flow (including its redirect to the asset host) runs through a stub `RoundTripper`.
- **Tests** (each cites its AC): `TestUpdateReplacesTheRunningBinaryWithTheLatestRelease`, the four platforms,
  the host platform, unsupported platforms, symlinks (and which directory must be writable), modes,
  atomicity (the old binary whole while downloading; the new one is a different file), every checksums.txt
  form, a tag without `v`, development builds, 21 failure cases that each break only one thing (UPD-02), non-version
  tags, the four bounds at and over the limit, Ctrl+C mid-download, up to date (also in a read-only
  directory), 32 version-order rows, `--check` (newer, current, read-only, failed lookup, no extra output
  without it), usage errors, UPD-05 (instance from the environment, from the saved config, placeholder,
  quoting, the install directory, file vs directory), Windows, HTTPS-only (12 refused URLs × with and without
  `--check`), redirects (inside the host followed for all three requests; to another server, another name
  of the same server and plain http refused for all three), the redirect policy as a table, the GitHub flow
  and what leaves GitHub, and `--help`.
- **Discrimination.** A scratch copy was mutated about 65 ways (each rule of the redirect policy, the checksum
  comparison and its line forms, chmod, the rename, the temp-file cleanup, every limit, each URL rule, the
  version order and the output). The first round left the scheme rule (hidden by the port rule: a row for
  `http://host:443` now isolates it), the shell quoting (a test now needs it) and name-suffix matching in
  checksums.txt alive; after the new rows nothing survives except removing `replaced = true` (equivalent: the
  deferred `Remove` then finds no file).
- **Not covered.** The real GitHub (no network in tests); the only real-network step was the one HEAD request above.
  A built binary was run end to end against a local TLS server (update, `--check`, a tampered checksum, a
  read-only directory, an untrusted certificate, a hand-over redirect): outputs as above.
- **Docs.** `README.md`, `README.pt-BR.md` (command table; the upgrade note) and `docs/agents.md` (section 1).

## A photo for groups (GAV-01..GAV-07)

Server: `internal/rooms/avatar.go` (`SetAvatar`, `ClearAvatar`, `Avatar`), the handlers in
`internal/api/avatar_handlers.go`, the body limit in `internal/api/middleware.go`. Web: `GroupAvatar`,
`ConversationAvatar`, the photo editor in `room-settings-sheet.tsx`. Tests: `internal/api/room_avatar_test.go`,
`internal/rooms/retention_test.go`, `internal/store/room_avatar_migration_test.go`, `middleware_test.go`
(`TestAvatarUploadBodyLimit`) and, in `web/src`, `group-avatar`, `room-settings-sheet`, `conversation-list`,
`command-menu`, `rooms/conversation` and `live/handlers`.

- **Schema.** Migration `0022_room_avatar.sql` adds `rooms.avatar_sha256 TEXT NOT NULL DEFAULT ''` and a partial
  index on the non-empty values, like `intelligences`. `CountRoomAvatar` is the fourth reference the sweep counts
  (attachments, platform files, intelligences, organizations, groups); `SetRoomAvatar` bumps `updated_at` and
  nothing else: `updated_by` is the author of the name and description (INS-06), not of the photo.
- **Who and in which order.** `rooms.Service.authorize(manage)` runs before the image is read: 404 for who cannot
  see the group, 403 for who only posts, and only then `avatars.Process`, so a refusal never depends on the image.
  A DM is never managed, so it answers like `PATCH` and `DELETE` of a DM do; `GET` on a DM is 404 for everybody
  (no photo to give), and `avatar_url` is never set for a DM.
- **Transport.** The image is the raw request body, as for intelligences and organizations and as the web app
  sends it (`putAvatarImage`, one helper for the three uploads, with the 30 s abandon). There is no multipart
  variant: it was considered and dropped on 2026-10-24, for one way in that is exactly that of the avatars of
  intelligences and organizations.
- **413.** The middleware refuses a body whose `Content-Length` is over 2 MiB before it reads it
  (`isAvatarUpload` now includes `PUT /rooms/{id}/avatar`), exactly as for an intelligence; a body that hides its
  length and is longer is cut by the limit and refused as an image that is not one (422), also as there.
- **Same image, no change.** Storing the sha the group already has changes and announces nothing; a change is a
  `room.changed` to the people who see the group (the existing `RoomChanged` event; the frame is the `Room`, so it
  carries `avatar_url`).
- **Blobs.** A replaced or removed photo is swept after the one-hour grace like any avatar. Deleting a group adds
  its photo to the blobs released with its files (`Delete`), and `ReleaseBlobs` keeps a blob another group (or
  anything else) still uses: one image is one blob.
- **List and live.** `Conversation.avatar_url` (groups only) feeds the sidebar and the command menu; the
  `room.changed` handler patches it on the row of a group and ignores it for a DM. The mutations patch the room,
  the rooms list and the row at once and refetch the lists.
- **Web.** `PhotoEditor` is shared with the profile and the organization page and now says `type="button"` on
  "Remove photo": the editor sits inside the settings form here, where a default button would submit it (and
  Enter in the name field would have removed the photo). `GroupAvatar` is the one place that draws a group (a
  rounded square); `ConversationAvatar` picks the counterpart of a DM or the group, for the sidebar and the
  palette, whose rows had an icon for every conversation before.
