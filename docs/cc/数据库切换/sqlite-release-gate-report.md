# SQLite Release Gate Report

This checked-in file is a report placeholder. The release gate runner overwrites it with command results:

```bash
go run ./scripts/sqlite_release_gates
```

Do not use this placeholder as release evidence. A valid release artifact must include one row per gate G1-G14 with the executed command, cwd, start/end time, exit code, raw log artifact path, result (`PASS` or `FAIL` for executed gates), and blocker owner for non-P0 follow-ups. P0 gates may not be `SKIPPED`.

G12 packaging smoke must launch the unsigned package runtime entrypoint, not a test-process DB shortcut. The raw G12 log must show `TestSQLiteReleaseGatePackageSmokeRuntime`, a clean `SUPER_DOLPHIN_HOME`, PostgreSQL env still set, old PostgreSQL data present, SQLite creation, migration/schema/PRAGMA verification, and no bundled PostgreSQL runtime artifact.

G14 must include explicit large fixture stress evidence. The release command uses `-tags sqlite_stress` with `TestSQLiteLargeFixtureStressExplicitRun`; the raw G14 log records the command output for 100,000 `system_logs`, 50,000 `session_insights`, 20,000 `cron_job_runs`, and 20,000 DAG wakeups/events.

| Field | Value |
|---|---|
| Commit SHA | GENERATED_BY_RUNNER |
| OS/arch | GENERATED_BY_RUNNER |
| Start time | GENERATED_BY_RUNNER |
| End time | GENERATED_BY_RUNNER |

| Gate | Priority | Title | Command | CWD | Start time | End time | Exit code | Raw log artifact | Result | Blocker owner |
|---|---|---|---|---|---|---:|---:|---|---|---|
| G1 | P0 | SQLite runtime startup | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G2 | P0 | Postgres runtime not used | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G3 | P0 | SQLite schema baseline and version floor | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G4 | P0 | Main store SQLite regression | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G5 | P0 | mcp-orch SQLite regression | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G6 | P0 | Cron claim concurrency | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G7 | P0 | DAG wakeup claim concurrency | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G8 | P0 | SQLite runtime lock replacement | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G9 | P0 | Prompt recall lock replacement | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G10 | P0 | DAG JSON event golden | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G11 | P0 | Mixed main-app and mcp-orch write pressure | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G12 | P0 | Packaging smoke without PostgreSQL runtime | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER |  |
| G13 | P1 | Old PostgreSQL data ignored | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | sqlite-switch release owner |
| G14 | P1 | Regression, performance, backup restore | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | GENERATED_BY_RUNNER | sqlite-switch release owner |
