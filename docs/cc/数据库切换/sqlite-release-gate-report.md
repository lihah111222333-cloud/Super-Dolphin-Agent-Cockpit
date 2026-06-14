# SQLite Release Gate Report

| Field | Value |
|---|---|
| Commit SHA | 32ee0ef112412042312b8228e7a7734129358060 |
| OS/arch | windows/amd64 |
| Start time | 2026-06-14T14:34:07Z |
| End time | 2026-06-14T14:41:02Z |

| Gate | Priority | Title | Command | CWD | Start time | End time | Exit code | Raw log artifact | Result | Blocker owner |
|---|---|---|---|---|---|---:|---:|---|---|---|
| G1 | P0 | SQLite runtime startup | go test ./internal/platform/db/... -run TestSQLiteRuntimeStartupSmoke\|TestNewDBCreatesSQLiteWithPragmasAndRestrictiveFiles -count=1 | . | 2026-06-14T14:34:07Z | 2026-06-14T14:34:11Z | 0 | .tmp/final-release-logs/G1.log | PASS |  |
| G2 | P0 | Postgres runtime not used | go test ./internal/platform/db/sqlite -run TestSQLiteRuntimeIgnoresPostgresEnvironment -count=1 | . | 2026-06-14T14:34:11Z | 2026-06-14T14:34:13Z | 0 | .tmp/final-release-logs/G2.log | PASS |  |
| G3 | P0 | SQLite schema baseline and version floor | go test ./internal/platform/db/sqlite -run TestSQLiteBaseline\|TestSQLiteRuntimeStartupSmoke -count=1 | . | 2026-06-14T14:34:13Z | 2026-06-14T14:34:15Z | 0 | .tmp/final-release-logs/G3.log | PASS |  |
| G4 | P0 | Main store SQLite regression | go test ./internal/store/... -run TestSQLite -count=1 | . | 2026-06-14T14:34:15Z | 2026-06-14T14:34:26Z | 0 | .tmp/final-release-logs/G4.log | PASS |  |
| G5 | P0 | mcp-orch SQLite regression | go test ./cmd/mcp-orch ./cmd/mcp-orch/fxadapter ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/commandcard ./cmd/mcp-orch/store/workspace ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/tools -run TestSQLite -count=1 | . | 2026-06-14T14:34:26Z | 2026-06-14T14:34:37Z | 0 | .tmp/final-release-logs/G5.log | PASS |  |
| G6 | P0 | Cron claim concurrency | go test ./internal/store/cron -run TestSQLiteClaimDueJobsConcurrentGoroutinesAndProcesses -count=1 | . | 2026-06-14T14:34:37Z | 2026-06-14T14:34:39Z | 0 | .tmp/final-release-logs/G6.log | PASS |  |
| G7 | P0 | DAG wakeup claim concurrency | go test ./cmd/mcp-orch/store/taskdag -run TestSQLiteWakeupClaimConcurrentGoroutinesAndProcesses -count=1 | . | 2026-06-14T14:34:39Z | 2026-06-14T14:34:42Z | 0 | .tmp/final-release-logs/G7.log | PASS |  |
| G8 | P0 | SQLite runtime lock replacement | go test ./cmd/mcp-orch/store/taskdag -run TestSQLiteRuntimeLock -count=1 | . | 2026-06-14T14:34:42Z | 2026-06-14T14:34:44Z | 0 | .tmp/final-release-logs/G8.log | PASS |  |
| G9 | P0 | Prompt recall lock replacement | go test ./internal/store/prompt -run TestRecallTopicLockSerializesSameCWDTopicAcrossDBHandles\|TestRecallTopicLockRetriesBusyUntilConcurrentWriterCommits -count=1 | . | 2026-06-14T14:34:44Z | 2026-06-14T14:34:47Z | 0 | .tmp/final-release-logs/G9.log | PASS |  |
| G10 | P0 | DAG JSON event golden | go test ./cmd/mcp-orch/store/taskdag -run TestSQLiteRunEventAppendGoldenPayloads\|TestSQLiteRunEventAppendConcurrentWritersDoNotOverwrite -count=1 | . | 2026-06-14T14:34:47Z | 2026-06-14T14:34:49Z | 0 | .tmp/final-release-logs/G10.log | PASS |  |
| G11 | P0 | Mixed main-app and mcp-orch write pressure | go test ./internal/platform/db/sqlite -run TestSQLiteMixedWritePressure -count=1 | . | 2026-06-14T14:34:49Z | 2026-06-14T14:39:52Z | 0 | .tmp/final-release-logs/G11.log | PASS |  |
| G12 | P0 | Packaging smoke without PostgreSQL runtime | go test -v ./scripts -run TestPackageLinux\|TestPackageMacOS\|TestMacOS\|TestPackageWindows\|TestSQLiteReleaseGatePackageSmokeRuntime\|TestSQLiteReleaseGatePackageSmokeCommands -count=1 | . | 2026-06-14T14:39:52Z | 2026-06-14T14:40:02Z | 1 | .tmp/final-release-logs/G12.log | FAIL |  |
| G13 | P1 | Old PostgreSQL data ignored | go test ./internal/platform/config ./internal/app ./internal/provider/... -run TestNew_PostgresEnvAndOldDataDirDoNotOverrideSQLitePath\|TestNewUIDesktopScriptDefaultsSQLiteUnderHomeAndIgnoresOldPostgresDataDir\|Test.*ScrubsDatabaseEnv\|TestBuildManifest_StripsDatabaseEnvironmentFromMCPBinaries\|TestPeerProcessEnvPassesExplicitSQLitePathToTrustedOrchOnly -count=1 | . | 2026-06-14T14:40:02Z | 2026-06-14T14:40:14Z | 0 | .tmp/final-release-logs/G13.log | PASS | sqlite-switch release owner |
| G14 | P1 | Regression, performance, backup restore | go test -v -tags sqlite_stress ./internal/platform/db/sqlite ./internal/module/dashboard ./internal/store/... ./cmd/mcp-orch ./cmd/mcp-orch/fxadapter ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/commandcard ./cmd/mcp-orch/store/workspace ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/tools -run TestSQLiteBackupRestoreSmoke\|TestSQLiteQueryPlanSmoke\|TestSQLiteMediumFixtureDistribution\|TestSQLiteLargeFixtureStressExplicitRun\|TestDashboardDAGSnapshotListQueryCountDoesNotScaleWithPageSize\|TestSQLite -count=1 | . | 2026-06-14T14:40:14Z | 2026-06-14T14:41:02Z | 0 | .tmp/final-release-logs/G14.log | PASS | sqlite-switch release owner |
