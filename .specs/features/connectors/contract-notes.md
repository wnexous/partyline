# Contract Notes (from connectors Phase 1: framework)

Binding conventions for Phase 2 (platform adapters T7-T11) and the web app. Types live in
`internal/connectors` (framework) and `internal/api/dto` (HTTP, AD-003).

## Adding a platform
- A platform is a package `internal/connectors/<kind>` that exports a `connectors.Kind` (e.g. `func Kind() connectors.Kind`)
  and a type implementing `connectors.Connector` (`Run`, `Send`) and, when the platform can list chats, `connectors.ChatLister`.
- Register it with one line in `app.BuiltinKinds()` (`internal/app/app.go`). There is no global registry:
  `connectors.NewRegistry(kinds...)` is built per app; `app.Options.ConnectorKinds` / `testkit.WithConnectorKinds` override it.
- `Kind{Name, Label, Fields, CanListChats, InboundHook, New func(Config, Deps) (Connector, error)}`. `CanListChats` must match
  whether the connector implements `ChatLister` (refresh answers 409 otherwise). `InboundHook: true` (webhook) makes the API
  report `inbound_url = <public URL>/api/v1/hooks/<id>`.
- `Deps{ID, Logger, UserAgent}`: `UserAgent` is `partyline/<version>` (send it on every platform HTTP/WS call). The logger is
  already scoped with `connector_id` and `kind`; never log message text, chat content, URLs with tokens or secrets.

## Config schema (AD-007, CON-01..03)
- `Field{Name, Label, Type, Required, Help, Default}`; types `string|url|secret|bool|string_list` (`dto.FieldType*`).
  `Field.Secret()` is `Type == secret`; secrets never get a `Default`.
- `Kind.Validate(cfg)` returns the normalized config (strings trimmed, empty values dropped, lists as `[]string`, defaults
  applied) or `map[field]reason`. Reasons: `required`, `invalid url` (absolute http/https only), `must be a string`,
  `must be true or false`, `must be a list of strings`, `unknown field`. The service prefixes keys: 422 fields are
  `config.<field>` (plus `kind`, `name`).
- The connector receives the full decrypted `Config`; read it with `cfg.String(name)`, `cfg.Bool(name)`, `cfg.Strings(name)`
  (lists decode from JSON as `[]any`; `Strings` handles both).
- Secrets: only in `connectors.secrets_enc` (sealed JSON map via `secrets.Box`); `config_json` holds the rest. Reads return
  `"********"` for every set secret (unset secrets are absent); PATCH with the mask keeps the stored value, `null` clears a field,
  absent keys stay unchanged (PATCH config is a merge).
- Kind-specific create-time defaults that are not static (the webhook's generated `secret`) need a small framework hook in
  T11 (e.g. `Kind.Prepare func(Config) Config` called by `Service.Create` before validation); none exists yet.

## Running (CON-04..06)
- `connectors.Manager` (`App.ConnectorManager`) starts enabled connectors at `App.Start` and on create/update/enable
  (synchronously creates the instance via `Kind.New`, then runs it in a goroutine); disable/delete stop it.
- `Run(ctx, h)` must block until `ctx` is done or the transport fails, and must return promptly on cancel. Call
  `h.Status(connectors.StatusConnected, "")` once the platform accepted the connection (this also resets the backoff);
  the manager reports `connecting` before each Run and `error` with the returned error after it. Backoff 2 s doubling to
  120 s (`connectors.Backoff`). A nil return while ctx is alive counts as an error ("connection closed").
- Errors (Run, Send, ListChats, Kind.New) are passed through `connectors.Redact(text, secretValues)` (raw, query- and
  path-escaped forms → `[redacted]`). Adapters still avoid putting tokens in error text where they can (e.g. wrap
  `*url.Error` without the URL).
- `Manager.Send(ctx, id, Outbound)` → `ErrNotRunning` (409 on HTTP, retry in the outbox) when the connector is not running.

## Inbound (CON-07..12, IDN-02/03)
- Call `h.Inbound(ctx, connectors.Inbound{...})` for every received message, synchronously; it never returns an error.
  Fill `ChatID` (platform chat id; WhatsApp `<instance>|<jid>`), `ChatTitle`, `ChatKind` (`group|dm|channel`), `MessageID`
  (platform id; dedupe key per room), `SenderID`, `SenderAltID`, `SenderName`, `FromSelf`, `Kind` (a `dto.MessageKind`; empty =
  text), `Text`, `ReplyToID` (platform id of the quoted message), `Media`, `At`.
- The pipeline drops `FromSelf`, handles `!link CODE` / `/link CODE` (identity of `(connector id, SenderID, SenderAltID)`;
  superseded by the fix iteration notes below),
  upserts the chat (empty title/kind keep the stored ones; `chat.discovered` on new/changed), then stores only for chats in
  rooms. Messages with empty text and no media are not stored: give non-text kinds a short text (e.g. location, contact).
- `Media{Name, Mime, Size, Open}`: set `Size` when the platform reports it (larger than the upload limit → `too_large`
  without download); `Open` is called at most once, only for routed messages, so download lazily there. Open/read failures
  become `unavailable` attachments.
- A reply is linked when `ReplyToID` matches a stored message of that chat or Partyline's sent delivery to it.

## Outbound (DLV-07)
- `connectors.Sender` (default outbox sender of the app) builds `Outbound{ChatID (platform id), Text, ReplyToID,
  ReplyToSenderID, Files, MessageID, RoomID, AuthorName, CreatedAt}`. `ReplyToID`/`ReplyToSenderID` are set only when the
  quoted message came from that chat (sender = its `SenderID`) or was delivered to it (sender empty).
- `Files` are stored attachments only: `OutFile{ID, Name, Mime, Size, Open}`; `Open` may be called more than once
  (each call opens the blob anew). Text formatting (author prefix, splitting at platform limits) is the adapter's job.
- Return the platform message id; any error means a failed attempt (retry per DLV-07), stored redacted in `outbox.last_error`.

## HTTP
| Method | Path | Auth | Answer |
| --- | --- | --- | --- |
| GET | `/connector-kinds` | user | `List[ConnectorKind]` (by name) |
| GET | `/connectors` | user | `List[Connector]` owned + shared (`my_level` manage/use) |
| POST | `/connectors` | user | 201 `Connector`; 422 `kind`, `name`, `config.<field>` |
| GET | `/connectors/{id}` | use | `Connector` (`inbound_url` for hook kinds) |
| PATCH | `/connectors/{id}` | manage | `Connector`; config change or enable restarts, disable stops |
| DELETE | `/connectors/{id}` | manage | 204; removes chats (and their room memberships, outbox) and grants |
| GET/PUT | `/connectors/{id}/sharing` | use / manage | `Sharing` (use grants; audited as `grants.set`) |
| GET | `/connectors/{id}/chats` | use | `List[Chat]` by title |
| POST | `/connectors/{id}/chats/refresh` | manage | 204; 409 when not running or the kind cannot list chats |
| GET | `/chats` | user | `List[Chat]` of owned and shared connectors |
| POST | `/chats/{id}/route` | use on connector (else 404), use on agent (else 403) | 201 `Room` (private, named after the chat, chat + agent members); 422 `agent_id` |
| GET | `/identities` | user | `List[Identity]` (own) |
| POST | `/identities/pairing` | user | 201 `PairingCode` (`^[A-HJ-NP-Z2-9]{6}$`, 10 min) |
| DELETE | `/identities/{id}` | owner | 204; 404 for others' |

- 404 for no access, 403 for use without manage (same matrix as agents; viewer = use grantee in `authmatrix_test.go`).
- Audit (AUD-01): `connector.create`, `connector.update` (detail `changed: ["name","config"]`, never values),
  `connector.enable`, `connector.disable`, `connector.delete`; target kind `connector`.
- The webhook endpoint (T11) adds its routes in its own handler file and calls the pipeline through the running connector's
  handler (or `Pipeline.Handle` with `Source{ConnectorID, Kind}`).

## Testing
- `internal/connectors/connectortest`: `fake := connectortest.New()`, `testkit.WithConnectorKinds(fake.Kind())`; kind `fake`
  (secret `token` required, `label`, can list chats). `fake.Lookup(id)` / `fake.Instance(t, id)`, `inst.WaitRun/WaitStop`,
  `inst.Connected()`, `inst.Emit(ctx, Inbound)` (synchronous through the pipeline), `inst.Fail(err)`, `inst.SetChats`,
  `inst.SetSendError`, `inst.WaitSend(t)`, `inst.Sent()`. Waits use `connectortest.Timeout` (30 s, load-tolerant).
- testkit keeps outbox entries pending; pass `testkit.WithOutbox()` to run the worker with the connectors sender.
- `connectors.ManagerDeps.Wait` replaces the backoff sleep (fake clock in unit tests).
- Assert "starts within 2 s" structurally (instance exists when the request returns, no backoff before the first Run), not
  against wall time: the shared host is loaded.

# Contract Notes (from connectors Phase 2: platforms)

Binding for the web app, the Verifier and later platforms. Kinds registered in `app.BuiltinKinds()`:
`whatsapp`, `telegram`, `discord`, `webhook`.

## Framework additions
- `Handler.MigrateChat(ctx, oldID, newID)`: moves a chat to a new platform id keeping its record and rooms (Telegram
  group → supergroup). No-op when the old id is unknown or the new id is already a chat of the connector
  (`MigrateChatExternalID` query, `ChatDiscovered` on success). Every `Handler` implementation must have it.
- `connectors.SplitText(text, limit)` (rune-based; breaks after the last newline, else last space, else at the limit;
  parts join back to the text) and `connectors.AuthorLine(author, prefix, suffix, text)` for the author prefix.
- `connectortest.NewRecorder()`: a `Handler` for adapter tests (`NextInbound`, `NextStatus`, `NextMigration`, `NoInbound`).
- `Kind.Prepare func(Config) (Config, []string)`: completes a new connector's config before validation and names the
  generated fields; `Service.Create` returns their values once in `View.Generated` → `dto.Connector.generated_secrets`
  (create response only; every read masks them).
- `connectors.HookReceiver` (`ReceiveHook(ctx, HookRequest{Timestamp, Signature, Body})`) for `InboundHook` kinds;
  `Manager.Hook(id)` finds the running one. Errors: `ErrBadSignature` (401), `ErrRateLimited` (429), `ErrNotRunning`
  (404), `*validation.Error` (422). `connectors.MaxHookBody` = 64 KiB (413 above).
- `Deps.FileURL(attachmentID)`: signed, expiring (10 min) download link `<public URL>/api/v1/hooks/files/<token>`;
  the token is the attachment id + expiry sealed with the instance key (`connectors.FileLinks`); forged, expired or
  non-stored attachments answer 404. The link needs no login.

## Adapters
- Author prefix on outbound text: WhatsApp `*Name*\n`, Telegram `Name: ` (plain text, no parse mode), Discord
  `**Name**: `; webhook sends `author_name` as a field.
- **WhatsApp** (`base_url`, `api_key` secret, `instances` list). Chat id `<instance>|<bare jid>`; group kind for
  `@g.us`, DM kind otherwise (DM title = push name); status broadcasts and protocol messages other than edit/revoke are
  dropped. Reaction/edit/revoke set `ReplyToID` to the target message and carry a short text. `ChatTitle` is empty for
  groups (names come from `ListChats`). Text goes to `/send/text` (quoted on replies), files to `/send/media` one by one
  without caption; the returned id is the first sent message's. Run fails with `unauthorized` on 401/403.
- **Telegram** (`bot_token` secret, `api_base` default `https://api.telegram.org`). Chat id = Telegram chat id; kinds
  private → dm, channel → channel, else group. Offset is kept in memory across Run restarts. No `ListChats`.
- **Discord** (`bot_token` secret). Chat id = channel id; guild channels are `channel` (title `Guild #name`, the same
  in inbound messages and ListChats), DMs `dm`.
  Mentions are disabled on sends (`AllowedMentions{}`); files ride on the last text part. discordgo reconnects by
  itself; Run reports connected after `Open` and fails when `Open` fails.
- **Webhook** (`secret` secret, generated when empty; `outbound_url` optional). Inbound chats are `channel` kind.
  Outbound payload per design; `message.reply_to_external_id` omitted when empty; `attachments` always an array.
  The returned id is the receiver's `{"message_id"}` when it answers one, else the room message id. Without
  `outbound_url` every send fails (and is retried by the outbox).

## Not done here (web app)
- Show `generated_secrets.secret` once after creating a webhook connector, with `inbound_url` and the signing recipe
  (`X-Partyline-Timestamp`, `X-Partyline-Signature: sha256=hex(HMAC_SHA256(secret, timestamp + "." + body))`).

# Contract Notes (from connectors fix iteration 1: T12-T20)

Binding for the re-verify, the web app and later platforms.

## Identities belong to a connector (IDN-02, IDN-04, IDN-05; S1)
- `identities.connector_id` (FK to connectors, cascade) with unique `(connector_id, external_id)`; `connector_kind` stays
  for display. Migration `0003_identities_per_connector.sql` drops older rows: users link again.
- `identities.Link(ctx, code, Account{ConnectorID, ConnectorKind, SenderID, AltID, Display}, onlyUser)`;
  `ResolveSender(ctx, connectorID, sender, alt)` matches only that connector. The pipeline passes `Source.ConnectorID`.
- `Kind.LinkOwnerOnly` (webhook): `!link` links only codes of the connector's owner (`Source.OwnerID`); another
  user's code links nothing and is not consumed. Set it on any kind whose caller chooses sender ids.
- Trust note: linking through a connector you don't own means trusting that connector's owner.
- `dto.Identity.connector_id` is new (web types regenerated).

## Outbound network policy (CON-13; S2)
- Every platform call goes through `internal/connectors/safehttp`: `NewClient(Options{AllowPrivate, Timeout,
  MaxResponseBytes})` for requests, `NewStreamClient(allowPrivate)` for WebSocket handshakes. Pass
  `Deps.AllowPrivateNetworks` (from `PARTYLINE_ALLOW_PRIVATE_NETWORKS`, default false). Never use `http.DefaultClient`
  or a bare `http.Client` in an adapter. Adapter tests against httptest servers set `AllowPrivateNetworks: true`; api
  tests set the env var through `testkit.WithEnv`.
- Blocked: loopback, RFC 1918/ULA, link-local (incl. 169.254.169.254), CGNAT, multicast, unspecified, reserved,
  IPv4-mapped/NAT64 forms of those. Errors wrap `safehttp.ErrBlockedAddress`; the webhook reports
  `outbound: destination address not allowed`.
- Redirects are not followed (a 3xx is a failed call); bodies over 256 MiB fail with `ErrResponseTooLarge`.
- Platform error bodies stay out of errors: Telegram reports `<method>: error <code>`.

## Webhook (HOOK-04, HOOK-05; S3, S4)
- Order: timestamp + signature → replay check → 60/min budget → payload. `HookRequest.RemoteIP` is the client IP
  (`httpx.ClientIP`); invalid requests count 30/min per IP and then get `ErrRateLimited` (429, not audited).
- Accepted signatures are kept 10 minutes (`ReplayTTL`); a repeat returns `connectors.ErrReplayed` → 409 `conflict`.
  The cache is per running connector, so a restart or config change clears it (superseded by iteration 2 below).

## Other
- Inbound text over `dto.MaxTextLength` (16,000 code points) is stored cut, ending with `connectors.TruncatedMarker`
  (`…[truncated]`) (CON-14).
- `Manager.ListChats` wraps platform failures in `connectors.ErrPlatform`; `POST /connectors/{id}/chats/refresh` then
  answers 502 `unavailable` "the platform did not answer the request" (CON-09). The redacted detail is logged.
- Testing: `connectortest.Instance.SetListError(err)`.
- Split limits (TG-02, DC-02) count Unicode code points (`connectors.SplitText` is rune-based).

# Contract Notes (from connectors fix iteration 2: T22-T30)

Binding for the final re-verify, the web app and later platforms.

## Pairing codes belong to a connector (IDN-01, IDN-02; S5)
- `POST /identities/pairing` takes `dto.PairingRequest{connector_id}` (422 `connector_id: required` when empty; 404 when
  the user neither owns the connector nor holds `use` on it) and answers `dto.PairingCode{code, connector_id,
  expires_at}`. `identities.CreatePairing(ctx, userID, connectorID)`.
- `pairing_codes.connector_id` (migration `0005`, older codes dropped). `Link` consumes a code only on its connector;
  through any other connector the code links nothing, stays unused, and the `!link` message is not routed.
  `Kind.LinkOwnerOnly` (webhook) still applies on top.
- `connectors.Field.Endpoint` marks fields naming the platform server (`base_url`, `api_base`, `outbound_url`).
  `Service.Update` deletes the connector's identities when one changes (`Kind.EndpointChanged`) and audits
  `connector.identities_reset` (`detail: {kind, name, reason: "endpoint_changed", removed}`), never values. New kinds
  must mark their endpoint fields.
- Web: the `/identities` pairing dialog asks for the connector (preselected when there is one; empty state with a link
  to `/connectors` when none) and shows `/link CODE` for Telegram, `!link CODE` elsewhere.

## Webhook replays survive restarts (HOOK-05; S7, N35)
- The signature header is trimmed and lower-cased before the constant-time compare; the replay key is the computed
  signature, so padded or upper-cased copies are replays.
- `connectors.Replays` (`Seen`, `Remember`) in `Deps.Replays`: `StoreReplays` keeps SHA-256 hashes in `webhook_replays`
  (migration `0004`, unique `(connector_id, sig_hash)`, cascade on connector delete) until `expires_at` (inclusive);
  expired rows are deleted on every insert. `MemoryReplays` is the fallback when `Deps.Replays` is nil (unit tests).
- `testkit.Env.Restart()` restarts the app on the same data dir behind the same server.

## Network policy and response caps (CON-13; S6, S8)
- Adapters build clients only through `Deps.JSONClient(timeout)` (4 MiB, `connectors.MaxJSONResponse`),
  `Deps.MediaClient(timeout)` (`Deps.MediaLimit()`: `PARTYLINE_MAX_UPLOAD_MB`, 25 MiB when unset),
  `Deps.HTTPClient(timeout, cap)` and `Deps.StreamClient()`; all apply `AllowPrivateNetworks` and `PrivateAllowlist`.
  `safehttp.DefaultMaxResponse` is now 4 MiB.
- WhatsApp: REST answers 4 MiB; `/message/downloadmedia` answers and WebSocket frames `base64(limit) + 4 MiB`.
  Telegram: Bot API 4 MiB, file downloads the upload limit. Discord: attachments the upload limit. Webhook outbound:
  4 MiB (it reads at most 64 KiB of the answer).
- A media download cut off by a cap (`safehttp.ErrResponseTooLarge`) is stored as `too_large`.
- `PARTYLINE_PRIVATE_NETWORK_ALLOWLIST` (`safehttp.ParseAllowlist`): comma-separated CIDRs (any port), IPs or host
  names with an optional port. Address entries match the dialed address; host entries match the host written in the
  URL (whatever it resolves to). Invalid entries fail startup naming the variable.
- `connectortest.Instance.Deps` exposes what the manager passed.

## Migrations
- `0004_webhook_replays.sql`, `0005_pairing_codes_per_connector.sql`. Another branch adding a migration must
  renumber after these.

