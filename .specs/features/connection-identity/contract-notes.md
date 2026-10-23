# Connection Identity Contract Notes

Binding for later tasks, the web app and the Verifier. Each task adds its section to this file.
Where a section refines the design, it says so.

## T27: WhatsApp connection uses exactly one instance (CID-04)

- **Config**: `instances` (string list) becomes `Required`. It is not renamed, so there is no
  config migration. Its help text is `exactly one instance: the number of this connection`.
  A create or update that names zero instances is refused by the schema with `required`.
- **Runtime shape**: `Connector` keeps one instance name (`instance`), not a set. The name is the
  single item of `instances`; it is empty when a stored config names zero or several instances.
  `New` always succeeds, so `Run` can report why the connection is idle.
- **Idle connection**: `Run` with zero or several instances reports
  `StatusError` with the exact detail `choose the instance (number) of this connection`. It makes
  no REST call, dials no WebSocket, receives nothing, and blocks until ctx ends (no error return,
  so the manager does not restart it with a backoff). With exactly one instance it connects as
  before.
- **Receive filter**: `handle` delivers a frame only when its `instanceName` is the connection's
  instance. Messages of other instances on the same evolution-go server are ignored.
- **Send**: `Send` returns `ErrOtherInstance` (`chat of another instance`) before opening files or
  calling the platform when the chat id's instance prefix is not the connection's instance. An
  ambiguous config (zero or several instances) refuses every chat. `React` and the internal
  token lookup use the same rule, so reactions never leave through another number.
- **Chat listing**: `ListChats` lists `GET /group/list` only for the connection's instance. With an
  ambiguous config it returns no chats.
- **Account label**: the label is the masked phone number of the connection's instance only. The
  device suffix is dropped; a LID or a missing jid yields an empty label.
- **Retired**: the empty allowlist meaning "use every instance with WebSocket events enabled",
  the aggregation of several numbers into one account label, and the `idleWarning` branch for a
  server with no WebSocket-enabled instance. Their tests are deleted with them.
  `idleWarning` survives only for `evolution-go has no instance named X`, which is not that
  retired branch: the rule is now about the one configured instance and is still shown with the
  connected status.
