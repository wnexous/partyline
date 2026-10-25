# Connection-Down Push Alerts — Contract Notes

Notes on places where the implementation had to differ from the plan, kept with the feature so a
verifier can judge the deviation against the design.

## T3 — `TestAlerterDropsOnAFullQueue`

The task lists the test as "`QueueSize: 1`, no `Run`; … publishing the `error` … and the log contains
`webpush: connection alert queue full`; no delivery happens because no worker runs".

That combination cannot happen with the design's lifecycle: `NewAlerter` only *subscribes* to the bus
and `Run` is what consumes the subscription and starts the worker's two send goroutines. With no
`Run`, no event reaches `handle`, so nothing can fill the queue and no queue-full line can be logged.
Starting the worker anywhere earlier (for example consuming in the constructor) would contradict the
design ("`Run(ctx)` consumes the subscription"; "`Close()` cancels the bus subscription, also when
`Run` never started") and would make `WithoutPush` deliver alerts, which T4's
`TestWithoutPushStopsTheConnectionAlerter` forbids.

The test therefore keeps every other expectation and starts `Run` with a delivery that holds each job
until the test opens the gate, five devices (so at most the two running workers plus the one queued
slot can be accepted), and `QueueSize: 1`:

- publishing the status does not block (the assertion the AC names);
- the log carries exactly `webpush: connection alert queue full` with `connector_id` and `status`, and
  neither the connection name nor the platform detail;
- the dropped device never reaches `Delivery`: at most three of the five devices are delivered.

No production code depends on the difference; only the test's starting conditions changed.
