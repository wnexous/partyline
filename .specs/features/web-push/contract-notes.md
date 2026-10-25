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

## T12: the e2e gate runs the full Chromium, not the headless shell

The desktop ACT-02 hover assertion in `web/e2e/mobile.spec.ts:927` fails under Playwright's default
headless-shell build because the message row keeps the hover state after the context menu closes. The
same assertion passes with the full Chrome for Testing build, which `web/playwright.config.ts` already
supports through `PLAYWRIGHT_CHROMIUM_EXECUTABLE`; the failure also reproduces against a `v0.7.0` binary
built before this feature, so it predates the push work and is not caused by `web/e2e/push.spec.ts`.
`make e2e` (82 passed, 2 pre-existing phone-only skips) and the push spec on its own pass in both
projects with that executable; under the headless shell the push spec passes in both projects as well.
