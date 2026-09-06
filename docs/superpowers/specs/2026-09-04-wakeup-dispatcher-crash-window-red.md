# Wakeup Dispatcher Crash Window — RED / NO-GO

Status: **planning evidence only; NO-GO for exact-once delivery**.

`TestWakeupDispatcherSQLiteCrashWindow_RED_NO_GO` uses a file-backed SQLite
database with two independent `sql.DB` connections. A child test process claims
a valid legacy `start` wakeup and a fake launcher records provider acceptance,
then exits with `os.Exit(0)` before `MarkWakeupSent` can run. The parent opens a
second connection, expires the lease, runs `ReclaimStaleDispatchingWakeups`,
and dispatches the wakeup again through the normal dispatcher path.

Observed result: the fake provider acceptance occurs twice (`attempt_count=2`)
and the row eventually reaches `sent`. No external provider is started; the
fake launcher is only an observation point for the accepted side effect.

The current claim/lease fence prevents the crashed worker's stale
`MarkWakeupSent` from overwriting a later claim. It does **not** prove that the
provider did not already accept the command. There is no durable command/attempt
owner spanning provider acceptance and the sent mark, so lease reclaim makes the
same side effect dispatchable again. This is a real duplicate-delivery RED
candidate, not a GREEN safety test.

Run the focused evidence test with:

```sh
GOCACHE=/tmp/super-agent-v3-gocache \
GOTMPDIR=/tmp/super-agent-v3-tmp \
go test ./cmd/mcp-orch/orchestration \
  -run '^TestWakeupDispatcherSQLiteCrashWindow_RED_NO_GO$' -count=1 -v
```

Closure requires integrating wakeup dispatch with the canonical durable
command/attempt owner (including an explicit accepted/unknown outcome) and then
replacing this characterization with an exact-once or operator-recovery test.
