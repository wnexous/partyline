# Web Push Contract Notes

## T2: the corrupt-key test and checkbox predate PUSH-26

`tasks.md` T2 still carried two lines from before commit `b908708` ("a bad VAPID key never stops the
server"): a `TestVAPIDRefusesACorruptFile` that expects the loader to fail, and a Done-when box saying a
corrupt key stops `app.New`. Both contradict the updated What paragraph, the design's
[VAPID keys](design.md#vapid-keys) and PUSH-26. The implementation follows the design and PUSH-26, and the
T2 test and checkbox now state the recover-and-continue behaviour.

The loader receives only the data directory and the secrets box (the design's signature), not the
store, yet it must drop every `push_subscriptions` row when it replaces a bad key. It therefore opens
`<data dir>/partyline.db` itself for that one `DELETE`, after checking that the table exists: `app.New`
runs before `Start`'s migrations, so on a fresh or pre-`0025` database there is nothing to drop. A
failure to drop is a warning, never a startup error.

## T7: the app wiring and the web fixture follow from the routes

T7's **What** asks for the service "stored on `App.PushService`" and for `api.Deps.Push`, but its
**Also** line omits `internal/app/app.go`. That file is the only place that builds `api.Deps`, so it
gains the `PushService` field, `webpush.NewService` next to the notifier and the `Push` argument; no
other behaviour changes there.

`dto.Me` now carries the required `push_groups`, so the regenerated TypeScript type makes
`web/src/test/fixtures.ts`'s `makeMe` fail `tsc` without the field. The fixture gains the default
`"mentions"`; the Settings UI itself stays for T8.
