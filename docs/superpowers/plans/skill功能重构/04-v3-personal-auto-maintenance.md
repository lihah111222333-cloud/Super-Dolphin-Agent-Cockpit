# V3 Personal Auto Maintenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add controlled self-maintenance for personal skills, limited to low-risk automatic patches under `personal/agent`; project skills and user-authored personal skills remain review-first.

**Architecture:** V3 builds on V2 proposal validation. Auto-maintenance is a policy layer that can approve a narrow subset without a user click, then uses the same backup/audit/apply/publish path as V2. Curator operations run dry-run first, snapshot before real-run, and archive instead of deleting.

**Tech Stack:** Go 1.25.7, file-backed usage sidecar under the resolved Super-Dolphin home, existing auditlog store, V2 proposal/apply service, owner-keyed personal settings for enable/disable, V1 canonical archive/restore and mirror publisher ports.

---

## File Structure

- Create: `internal/module/skillmaintenance/policy.go`
- Create: `internal/module/skillmaintenance/policy_test.go`
- Create: `internal/module/skillmaintenance/auto_apply_worker.go`
- Create: `internal/module/skillmaintenance/auto_apply_worker_test.go`
- Create: `internal/module/skillmaintenance/usage_store.go`
- Create: `internal/module/skillmaintenance/usage_store_test.go`
- Create: `internal/module/skillmaintenance/archive.go`
- Create: `internal/module/skillmaintenance/archive_test.go`
- Create: `internal/module/skillmaintenance/curator.go`
- Create: `internal/module/skillmaintenance/curator_test.go`
- Create: `internal/module/skillmaintenance/dryrun_store.go`
- Create: `internal/module/skillmaintenance/dryrun_store_test.go`
- Create: `internal/module/skillmaintenance/rpc.go`
- Create: `internal/module/skillmaintenance/rpc_test.go`
- Create: `internal/module/skillmaintenance/module.go`
- Modify: `internal/module/skillproposal/apply.go`
- Modify: `internal/module/skillproposal/apply_test.go`
- Modify: `internal/module/skill/contract.go`
- Modify: `internal/module/skill/rpc_skill_types.go` only for shared DTOs if unavoidable; prefer `skillmaintenance` DTOs in the new module
- Modify: `internal/app/modules.go`
- Modify: `internal/store/uipreference` or add a personal settings store only if needed for per-user settings
- Modify: frontend files after locating current settings UI source with `rg`

Keep the new module separate from `internal/module/skill` so policy and curator code do not inflate filesystem CRUD files. RPC handlers must be provided by `internal/module/skillmaintenance.NewHandlers` or an equivalent module-local handler provider; do not wire `skill_maintenance/*` through `internal/module/skill/rpc.go` in a way that creates `skill -> skillmaintenance -> skill` import cycles.

## Task 1: Usage Metadata Sidecar

**Files:**
- Create: `internal/module/skillmaintenance/usage_store.go`
- Test: `internal/module/skillmaintenance/usage_store_test.go`

- [ ] **Step 1: Write sidecar tests**

Tests must cover:

- missing `.usage.json` returns empty map
- update writes atomic JSON
- update holds an owner-only file lock and uses expected `Version`/`RecordHash` compare-and-swap semantics
- concurrent pin/use/archive and curator reads cannot lose updates
- pinned skill remains pinned after use-count update
- proposal/apply events update `ProposalCount` and `PatchCount` without changing `UseCount`
- corrupt sidecar is backed up and replaced only after explicit repair action

- [ ] **Step 2: Implement sidecar schema**

Use:

```go
type UsageRecord struct {
	Scope         string     `json:"scope"`
	PersonalType  string     `json:"type"`
	CreatedBy     string     `json:"created_by"`
	UseCount      int        `json:"use_count"`
	ProposalCount int        `json:"proposal_count"`
	PatchCount    int        `json:"patch_count"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	LastPatchedAt *time.Time `json:"last_patched_at"`
	State         string     `json:"state"`
	Pinned        bool       `json:"pinned"`
	ArchivedAt    *time.Time `json:"archived_at"`
	OwnerKey      string     `json:"owner_key"`
	Version       int64      `json:"version"`
	RecordHash    string     `json:"record_hash"`
}
```

Store path is `resolvedSuperDolphinHome()/skills/.usage.json`, for example `~/.super-dolphin/skills/.usage.json` in the default profile. The helper must respect `SUPER_DOLPHIN_HOME` and the active App profile, matching the V1 `defaultSuperDolphinHome()` semantics. The sidecar is keyed by derived `owner_key` plus scope/type/name. It must not store raw home path, OS uid, username, or profile path. `RecordHash` is deterministic JSON over the usage fields that affect curator decisions, including `Pinned`, `State`, `LastUsedAt`, `LastPatchedAt`, `ArchivedAt`, counts, and `Version`.

All usage writes must acquire an owner-only lock next to the sidecar and must provide the expected record version/hash for compare-and-swap. A stale writer returns a conflict and must re-read rather than overwriting newer pin/use/archive state. Dry-run and real-run must read usage state under the same lock or through an equivalent snapshot API so a concurrent pin/use/archive update cannot be lost between hash computation and real-run validation.

- [ ] **Step 3: Do not fake provider use counts**

Only increment `UseCount` from observable skill-use events: explicit UI invocation or provider events that explicitly expose skill usage. Super-Dolphin proposal/apply events increment `ProposalCount` or `PatchCount`, not `UseCount`. Native provider inference from file existence is not an observable use event.

- [ ] **Step 4: Run tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillmaintenance -run 'Test.*Usage' -count=1
```

Expected: usage sidecar tests pass.

## Task 2: Auto-Apply Policy

**Files:**
- Create: `internal/module/skillmaintenance/policy.go`
- Test: `internal/module/skillmaintenance/policy_test.go`
- Create: `internal/module/skillmaintenance/auto_apply_worker.go`
- Test: `internal/module/skillmaintenance/auto_apply_worker_test.go`
- Modify: `internal/module/skillproposal/apply.go`

- [ ] **Step 1: Write policy matrix tests**

Policy allows auto-apply only when all are true:

- scope is `personal`
- personal type is `agent`
- resolved target path is under `resolvedSuperDolphinHome()/skills/personal/agent/<skill-name>`
- server-computed safety says `touches_project_scope=false`
- server-computed safety says `touches_provider_mirror=false`
- action is `patch_skill` or `write_support_file`
- `write_support_file` target is limited to non-executable regular files outside `scripts/**`
- no rename/delete/archive
- no `scripts/**` write of any kind
- no executable support-file write
- canonical has no drift
- provider mirrors have no unresolved conflict
- validator exactly matches old/new
- backup succeeds
- user setting enables auto-maintenance

Every other case downgrades to pending proposal.

Explicitly cover project canonical, project mirror, personal provider mirror, system `~/.claude`/`~/.agents`, and unmanaged external paths as reject/downgrade cases.

The policy must compute safety on the server from the resolved target, normalized operation paths, validator diff, canonical type, provider-target classification, and current drift/conflict state. Model/proposal JSON safety fields are advisory only: a missing or stricter model flag may downgrade, but a model flag can never authorize auto-apply. If model JSON claims `touches_project_scope=false` or `touches_provider_mirror=false` while server-computed safety detects the opposite, policy rejects/downgrades with a stable reason and records the mismatch. Add a regression test where proposal JSON lies about safety and the server-computed decision refuses `ApplyModePolicyAuto`.

- [ ] **Step 2: Implement policy decision**

Return:

```go
type AutoApplyDecision struct {
	Allowed bool
	Reason  string
}
```

Reasons must be stable strings for tests and UI, such as `disabled`, `project_scope`, `not_personal_agent`, `provider_mirror_target`, `external_target`, `script_write`, `executable_support_file`, `high_risk_file`, `drift_unresolved`, `validator_failed`.

If a future separate high-risk automation setting is added, it must be off by default and tested independently. This V3 plan does not allow background auto-apply for `scripts/**` or executable support files.

- [ ] **Step 3: Wire policy into V2 apply path**

The generator still creates proposals. The maintenance module may call V2 apply only through a dedicated policy-authorized mode, for example:

```go
type ApplyMode string

const (
	ApplyModeUserConfirmed ApplyMode = "user_confirmed"
	ApplyModePolicyAuto    ApplyMode = "policy_auto"
)
```

`ApplyModePolicyAuto` is accepted only when V3 policy returns allowed, actor is `system:skillmaintenance`, audit reason includes the stable policy reason, and proposal status transitions through the same durable `applying` / `apply_partial_failure` / `applied` states as user-confirmed apply. Do not weaken `approve_apply` confirmation checks for user-facing RPC.

- [ ] **Step 4: Implement auto-apply worker**

Add a maintenance-owned worker or event hook that claims eligible pending proposals and calls V2 apply in `ApplyModePolicyAuto`. The worker:

1. derives `owner_key` server-side and reads owner settings; missing settings means disabled
2. claims pending `personal/agent` proposals through a durable lease/claim id so concurrent workers cannot apply the same proposal twice
3. loads a fresh diff preview and runs the server-computed policy against current canonical bytes, normalized paths, validator diff, drift state, and provider target classification
4. calls V2 apply only when policy returns allowed, with actor `system:skillmaintenance`, stable policy reason, claim id, and idempotency key
5. releases or marks the claim with stable reasons for downgrade/failure, without changing proposal JSON or canonical bytes when policy rejects

Tests must cover setting off, missing setting, non-`personal/agent`, stale proposal hash, concurrent worker claim, crash after claim before apply, crash after apply partial failure, retry idempotency, and model JSON safety lying while server-computed safety rejects.

- [ ] **Step 5: Run tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillmaintenance ./internal/module/skillproposal -run 'Test.*AutoApply|Test.*Policy' -count=1
```

Expected: only `personal/agent` low-risk proposals auto-apply.

## Task 3: Pin, Archive, Restore

**Files:**
- Create: `internal/module/skillmaintenance/archive.go`
- Test: `internal/module/skillmaintenance/archive_test.go`
- Modify: `internal/module/skill/rpc_skill_types.go`

- [ ] **Step 1: Write archive tests**

Cover:

- archive moves personal canonical to `resolvedSuperDolphinHome()/skills/.archive/<timestamp>/personal/<type>/<skill-name>`
- restore recreates canonical if target name is free
- restore fails on same-name conflict
- pinned skill cannot be auto-archived
- project skill archive API returns invalid scope
- audit intent failure leaves canonical and archive-relative locations unchanged
- audit finalize failure after archive/restore mutation returns a structured partial-failure record with phase, pre-hash, post-hash, archive id or archive-relative path, and recovery action
- archive removes owned, non-drifted provider mirrors through the V1 publisher/reconciler port
- restore republishes provider mirrors through provider-native user-global targets or explicit provider targets
- archive/restore uses temp HOME in tests and never writes real `~/.claude` or `~/.agents`

- [ ] **Step 2: Implement operations**

Archive and restore must execute through the V1 canonical skill operation port, not through ad hoc filesystem moves. Archive moves canonical bytes to the resolved archive root, removes owned non-drifted mirrors through the publisher, and records enough metadata for restore. Restore copies or moves canonical bytes back after conflict checks, then republishes mirrors through explicit provider targets. Permanent delete is not part of V3 auto-maintenance.

- [ ] **Step 3: Emit audit**

Write audit intent before archive/restore mutation and audit finalize after mutation. If audit intent fails, archive/restore returns an error and leaves files unchanged. If mutation succeeds but audit finalize fails, persist a recovery manifest with phase, pre-hash, post-hash, archive id or archive-relative path, actor, and action so the operation can be reconciled or rolled back later; return structured partial failure rather than success. The recovery manifest resolves archive data through the current `resolvedSuperDolphinHome()` at recovery time and must not contain raw resolved home, raw profile, raw uid, username, or absolute provider mirror paths. Write audit events for pin, unpin, archive, restore, and failed restore conflicts, and add tests proving recovery manifests contain only archive ids/relative paths.

- [ ] **Step 4: Run tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillmaintenance ./internal/module/skill -run 'Test.*Archive|Test.*Restore|Test.*Pin' -count=1
```

Expected: archive/restore are reversible and project scope is excluded.

## Task 4: Curator Dry-Run

**Files:**
- Create: `internal/module/skillmaintenance/curator.go`
- Create: `internal/module/skillmaintenance/dryrun_store.go`
- Test: `internal/module/skillmaintenance/curator_test.go`
- Test: `internal/module/skillmaintenance/dryrun_store_test.go`

- [ ] **Step 1: Write dry-run tests**

Dry-run should persist a dry-run record but must not write canonical or mirror files:

- merge narrow `personal/agent` skills
- archive stale unpinned `personal/agent` skills
- leave `personal/user`, `personal/imported`, catalog-only `personal/hub`, and project skills untouched
- leave pinned skills untouched
- generate a server-owned dry-run ID bound to `owner_key`
- store action hashes, source canonical hashes, source usage record hashes, usage versions, created_at, expires_at, and actor
- reject real-run if dry-run is expired, owner mismatched, or action hash cannot be recomputed
- reject real-run if pinned/state/last-used/last-patched/archive state changed after dry-run even when canonical bytes did not change

- [ ] **Step 2: Implement curator planning**

Curator reads canonical records and usage sidecar, then emits structured actions:

```go
type CuratorAction struct {
	Action               string   `json:"action"`
	SkillName            string   `json:"skill_name"`
	Scope                string   `json:"scope"`
	PersonalType         string   `json:"personal_type"`
	Reason               string   `json:"reason"`
	TouchedPaths         []string `json:"touched_paths"`
	ProviderTargetIDs   []string `json:"provider_target_ids,omitempty"`
	RiskClass            string   `json:"risk_class"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
}
```

Curator dry-run may propose merge/archive actions, but no background auto-maintenance path may execute curator real-run automatically. `TouchedPaths` may contain only home-relative canonical paths such as `personal/agent/name/SKILL.md`; mirror effects are represented by logical provider target ids, never by physical provider mirror paths. The dry-run record is stored under `resolvedSuperDolphinHome()/skills/.dryruns/<dry_run_id>.json` or an equivalent store with owner-only permissions. `action_hash` is computed by deterministic canonical JSON over action fields plus source canonical hashes and source usage record hashes/versions. Real-run recomputes it server-side after re-reading canonical and usage sidecar state.

- [ ] **Step 3: Run dry-run tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillmaintenance -run 'Test.*Curator.*DryRun' -count=1
```

Expected: dry-run produces no canonical diff and stores a verifiable dry-run record.

## Task 5: Curator Real-Run With Snapshot

**Files:**
- Modify: `internal/module/skillmaintenance/curator.go`
- Test: `internal/module/skillmaintenance/curator_test.go`

- [ ] **Step 1: Write snapshot tests**

Real-run must prepare snapshot staging under an owner-only temporary path before mutation, but rollback-visible final snapshots are promoted only after canonical mutation has produced observed post-hashes:

```text
resolvedSuperDolphinHome()/skills/.snapshots/<timestamp>/
```

The staged snapshot created before mutation contains owner key, dry-run ID, selected action hashes, touched paths, pre-hashes, planned operations, and `phase=pending_mutation`; it is not listed as rollback-ready. The final promoted snapshot contains owner key, dry-run ID, selected action hashes, touched paths, pre-hashes, observed post-hashes, and phase for every action. Split paths into two explicit fields:

```json
{
  "owner_key": "sd_owner:...",
  "dry_run_id": "dryrun_...",
  "rollback_paths": ["personal/agent/name/SKILL.md"],
  "published_mirror_refs": [
    {
      "provider_target_id": "claude:user-global:sd_owner:...",
      "relative_path": "skills/name/SKILL.md"
    }
  ]
}
```

`rollback_paths` may contain only home-relative `personal/agent` canonical paths. `published_mirror_refs` are audit-only logical references and are never restored directly by rollback. Snapshot, dry-run, recovery, and rollback manifests store `owner_key`, home-relative canonical paths, provider target ids, and relative mirror paths only; they must not store raw resolved home, raw profile path, raw uid, username, or absolute provider mirror paths. Runtime resolves these logical paths through the current `resolvedSuperDolphinHome()` and validates owner/profile before any restore.

Tests must distinguish staging from committed snapshots: if audit intent fails, recovery record reservation fails, or mutation never starts, no final `.snapshots/<timestamp>/` directory appears, no snapshot manifest is promoted, and any staging path is removed or left only under a clearly ignored `.staging` recovery-cleanup path that rollback/list RPCs do not treat as a valid snapshot.

- [ ] **Step 2: Implement real-run**

Real-run applies only actions already produced by a persisted dry-run and revalidated against current canonical hashes and usage record hashes/versions. It requires explicit user confirmation of the dry-run ID and selected action hashes. Before archiving or merging, it must re-read usage sidecar and reject if `Pinned`, `State`, `LastUsedAt`, `LastPatchedAt`, or `ArchivedAt` changed after dry-run. It archives rather than permanently deletes. It must apply the same high-risk policy as auto-apply: `scripts/**` writes and executable support files stay proposal/review unless a separate high-risk setting exists, is explicitly enabled, and has its own tests. The default V3 implementation rejects them. Real-run follows D10 order for each mutating action: create backup/snapshot staging -> write audit intent -> reserve and fsync local real-run recovery record with pre-hashes and staged snapshot id -> mutate canonical -> update recovery record with observed post-hash -> persist post-hash and phase -> promote rollback-visible final snapshot manifest with observed post-hashes -> publish mirrors -> persist publish phase -> audit finalize. If audit intent or recovery record reservation fails, real-run returns an error before mutation, deletes any staging snapshot that was created, and leaves canonical, mirror, archive, final `.snapshots` entries, snapshot manifest, and dry-run records unchanged except for a read-only failure event.

The local real-run recovery record is owner-only and independent of SQL/audit storage. It records dry-run ID, selected action hash, owner key, home-relative rollback paths, logical backup/snapshot ids or paths relative to the resolved Super-Dolphin home, pre-hashes, and `phase=pending_mutation` before canonical mutation. It must not persist absolute resolved-home or provider mirror paths. If the recovery record cannot be created and fsynced, real-run fails before mutation. After mutation it records observed post-hashes before any DB/status persistence. If post-hash/phase persistence fails, rollback/list RPCs must read this recovery record and surface the failed real-run. If post-mutation recovery update fails, the pre-mutation record remains valid; recovery recomputes current hashes from the current resolved home/profile and exposes a manual recovery item rather than losing the mutation.

- [ ] **Step 3: Implement rollback**

Rollback restores only canonicalized `rollback_paths` listed in the snapshot action manifest after confirming current owner key matches the manifest owner key and current hashes match the persisted or recovered post-hash for the failed real-run phase. It must reject project canonical, provider mirrors, `personal/user`, `personal/imported`, catalog-only `personal/hub`, and any path outside `personal/agent` even if such a path appears in a malformed manifest. Rollback must resolve the home-relative manifest path through the current `resolvedSuperDolphinHome()`, then use `EvalSymlinks`/`lstat`-based traversal checks and reject symlinked path components, path traversal, hardlink surprises where detectable, and malformed manifests that resolve outside the owner canonical `personal/agent` root. `published_mirror_refs` are audit-only; rollback triggers a fresh publish after canonical restore instead of restoring mirror bytes. If any touched path lacks a persisted or recovered post-hash, belongs to another owner key, or changed after the failed real-run, rollback returns conflict and does not overwrite. Rollback success and rollback conflict both write audit events.

- [ ] **Step 4: Run real-run tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillmaintenance -run 'Test.*Curator.*RealRun|Test.*Snapshot|Test.*Rollback' -count=1
```

Expected: real-run is snapshot-protected and rollback refuses unsafe overwrites.

## Task 6: Settings And RPC

**Files:**
- Create: `internal/module/skillmaintenance/rpc.go`
- Create: `internal/module/skillmaintenance/rpc_test.go`
- Modify: `internal/app/modules.go`
- Modify: personal settings store files after selecting the storage path
- Modify: frontend settings files after discovery

- [ ] **Step 1: Expose RPCs**

Expose:

- `skill_maintenance/settings_read`
- `skill_maintenance/settings_write`
- `skill_maintenance/usage_read`
- `skill_maintenance/usage_repair`
- `skill_maintenance/pin`
- `skill_maintenance/unpin`
- `skill_maintenance/archive`
- `skill_maintenance/restore`
- `skill_maintenance/curator_dry_run`
- `skill_maintenance/curator_run`
- `skill_maintenance/rollback_snapshot`

The handlers are registered by the `skillmaintenance` module and depend on narrow ports for canonical skill operations, proposal apply, audit, usage, and settings. They must not be added to `internal/module/skill/rpc.go` unless that file imports only an interface from `internal/contract` and does not create a package cycle.

V3 also completes the `archive_skill` proposal apply path introduced in V2. `skill_proposals/approve_apply` may execute `archive_skill` only in `ApplyModeUserConfirmed` after V3 archive services are registered; it must call the same canonical archive operation used by `skill_maintenance/archive`, with the same audit, recovery, and mirror publisher behavior. Before V3 wiring exists, V2 continues to reject archive apply with `archive_apply_requires_v3`.

- [ ] **Step 2: Add settings UI**

Before editing frontend, locate source:

```bash
rg -n "settings|preference|skill|maintenance" cmd/agent-terminal frontend internal -g'*.vue' -g'*.ts' -g'*.js'
```

UI must show auto-maintenance default off and scope text that says auto-apply only affects `personal/agent`. The setting is per-user, keyed by `owner_key`, not by project `cwd`; do not store it in the existing project-scoped `ui_preference(cwd,key)` path unless that store is extended with a personal scope. If V3 exposes pin/archive/restore/curator/rollback in UI, add screens or dialogs for pin/unpin, archive/restore, dry-run preview, real-run confirmation, usage repair, and rollback conflict display. If any operation remains RPC-only in V3, document it in the plan completion report and keep the RPC tests as the acceptance surface.

Usage, dry-run, snapshot, rollback, and settings must all derive `owner_key` through the same helper used by V2. Tests must prove different app profiles do not share usage/settings/dry-run records and stored data contains only the derived owner key, never raw home/profile/uid values.

- [ ] **Step 3: Add RPC behavior tests**

Cover:

- `usage_repair` backs up corrupt sidecar and writes audit
- `settings_read` returns disabled/default-off when no setting exists
- `settings_write` stores settings under derived `owner_key` and never under project `cwd`
- missing or disabled setting makes auto-apply policy return reason `disabled`
- different app profiles or Super-Dolphin homes do not share settings
- usage writes reject stale expected version/hash and do not lose a concurrent pin/use/archive update
- `curator_run` rejects missing dry-run ID or action hash
- `curator_run` rejects expired dry-run, owner-key mismatch, and action hash mismatch
- `curator_run` rejects changed usage record hash/version, including user pin or observed use after dry-run
- `curator_run` rejects concurrent pin/use/archive changes between dry-run and real-run even under parallel writers
- `curator_run` rejects audit intent failure before mutation and proves canonical, mirror, archive, final `.snapshots` entries, snapshot manifest, staging cleanup, and dry-run records are unchanged
- `curator_run` fails before mutation if local real-run recovery record reservation fails
- `curator_run` surfaces local recovery if canonical mutation succeeds but post-hash/phase persistence fails
- `rollback_snapshot` refuses paths outside manifest
- `rollback_snapshot` refuses symlinked rollback path components and path traversal after canonicalization
- `rollback_snapshot` refuses owner-key mismatch between caller and snapshot manifest
- `rollback_snapshot` refuses rollback when persisted post-hash is missing or current hash differs from post-hash
- pin/archive/restore reject project scope
- archive/restore audit intent failure returns error before filesystem mutation
- archive/restore audit finalize failure after mutation returns structured partial failure and persists recovery manifest

- [ ] **Step 4: Add UI behavior tests**

Mock dry-run preview, real-run confirmation, rollback conflict, usage repair, stale usage conflict, and local recovery surfacing. Assert mutating operations require explicit confirmation.

- [ ] **Step 5: Run frontend checks**

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: settings UI builds.

## Task 7: Plan-Level Verification

- [ ] **Step 1: Run affected Go packages**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillmaintenance ./internal/module/skillproposal ./internal/module/skill ./internal/store/uipreference -count=1
```

If Task 6 adds a new personal settings store package instead of extending `internal/store/uipreference`, include that new store package in this command before accepting V3.

If Task 6 touches migrations, `sql/queries/**`, `sqlc.yaml`, or any SQL-backed store package, also run `make sqlc-verify` before accepting V3.

- [ ] **Step 2: Run guard and build**

Run:

```bash
make guard
make build-plain
```

- [ ] **Step 3: Run frontend checks**

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

- [ ] **Step 4: Check status**

Run:

```bash
git diff --check
git status --short
```

Expected: V3 does not add project auto-write behavior.

## Accepted Defaults And Gates For This Plan

- D4 is fixed: auto-maintenance defaults off.
- D5 is fixed: no archive purge in V1-V3.
- Scripts and executable support files are high-risk and excluded from default auto-apply; any separate high-risk automation setting is future/extra work unless explicitly approved.
- All V3 state paths use the resolved Super-Dolphin home and active profile; examples that show `~/.super-dolphin` are defaults, not hard-coded paths.
- Archive/restore uses V1 canonical operations and publisher ports so owned provider mirrors are removed or republished, and missing explicit provider targets fail structured without writing system homes.
- V3 completes user-confirmed `archive_skill` proposal apply; V2-only implementations must keep returning `archive_apply_requires_v3`.
- Curator merge strategy defaults to class-level umbrella skill over many session-specific skills.
- Curator real-run is user-confirmed only; background auto-maintenance cannot call it.
- Usage, dry-run, snapshot, and recovery writes use owner-only locking plus expected version/hash CAS where mutable sidecars are involved.
- Dry-run, snapshot, recovery, and rollback records use home-relative canonical paths and provider target ids; raw resolved home/profile/uid values and absolute provider mirror paths are not persisted.
- Curator dry-run and real-run hashes include usage sidecar state; pin/use/archive changes after dry-run reject real-run, including concurrent updates.
- Curator real-run audit intent failure is fail-closed and leaves canonical, mirror, archive, final `.snapshots` entries, snapshot manifest, and dry-run records unchanged; snapshot bytes before audit intent may exist only in staging and must be removed or ignored by rollback/list RPCs on failure.
- Curator real-run has a local recovery record reserved before mutation and updated with observed post-hash after mutation; rollback/list RPCs can recover from post-hash/phase persistence failure.
- Usage, settings, dry-run, snapshot, and rollback state are keyed by derived `owner_key` and do not store raw user/profile paths.
- Snapshot rollback rejects owner-key mismatch between caller and manifest and rejects symlink/path traversal after canonicalization.
- `UseCount` excludes proposal/apply maintenance events.
