# Skill Refactor Plan Index and Decision Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `skill功能重构.md` 拆成可执行的 V1-V3 实施计划，并把必须由产品或工程 owner 决策的点集中列出。

**Architecture:** 计划按可独立实现、但按版本原子验收的 implementation slice 拆分：V1 先建立 canonical/mirror 文件系统闭环和 resolution UI/RPC，再全量切 provider runtime；V2 做 proposal；V3 只放开个人级低风险自动维护。V4 只作为 non-goal，不进入执行任务。

**Tech Stack:** Go 1.25.7、Fx、现有 `internal/module/skill`、`internal/provider/claudecli`、`internal/provider/codexapp`、`internal/platform/toolbridge`、sqlc/store、repo wrapper commands。

---

## Source Of Truth

- Blueprint: `docs/superpowers/plans/skill功能重构/skill功能重构.md`
- Repo policy: `AGENTS.md`
- Current skill module: `internal/module/skill`
- Current Claude provider startup path: `internal/provider/claudecli/driver.go`
- Current Codex provider startup path: `internal/provider/codexapp/driver.go`
- Removed host-direct skill tool compatibility guard: `internal/platform/toolbridge/host_tools.go` and `internal/platform/toolbridge/handler.go`
- Removed cache/library runtime packages: `internal/module/skilllibrary`, `internal/module/skillforge`, `internal/module/fbsd`

## Recommended Plan Set

| Order | Plan | Implementation slice | Why this boundary |
|---:|---|---|---|
| 1 | `01-v1-foundation-canonical-mirror.md` | V1 foundation | Pure skill filesystem and policy core; can be tested without launching providers. |
| 2 | `02b-v1-resolution-ui-rpc.md` | V1 resolution UX | Adds import/export/takeover and drift/conflict decision surfaces so ordinary content conflicts are visible and user-resolvable without blocking provider startup. |
| 3 | `02-v1-provider-cutover.md` | V1 provider cutover | Removes old injection/runtime dependency and wires providers to the new publisher after resolution surfaces exist. |
| 4 | `03-v2-skill-proposal.md` | V2 proposal | Introduces model-assisted proposals with explicit user confirmation. |
| 5 | `04-v3-personal-auto-maintenance.md` | V3 auto-maintenance | Adds personal-only automatic low-risk patching and curator operations. |

The split keeps each implementation slice reviewable, but V1 is one atomic product landing. Provider cutover must not be enabled before foundation and resolution UI/RPC are both merged, because ordinary same-name, drift, unmanaged, and canonical-deleted content conflicts must be visible in the Skills UI for user handling while the chat/provider main path continues to work.

## Cross-Plan Decision Gates

### D1: Provider CLI Ownership And Personal Mirror

**Decision:** Super-Dolphin does not bundle Claude/Codex CLI in V1. Users install and log in to Claude/Codex themselves; Super-Dolphin manages canonical skills and reconciles generated mirrors into provider-native skill directories.

**Recommended default:** Project mirrors live in `<repo>/.claude/skills` and `<repo>/.agents/skills`. Personal mirrors live in the provider-native user skill roots `~/.claude/skills` and `~/.agents/skills`. Codex identity/config still uses the user's `~/.codex`; that identity directory is not a skill mirror. Explicit provider homes remain advanced configuration and may use their own `skills` directory.

**Reason:** This matches the current product boundary: Super-Dolphin统一管理 skill，但不接管用户的 provider 登录身份，也不维护一套 bundled CLI identity.

**Implementation gate:** `02-v1-provider-cutover.md` must include provider-native mirror smoke evidence before the V1 cutover is accepted. File-level mirror smoke is necessary but not sufficient; smoke must launch through the Super-Dolphin Claude/Codex drivers and prove installed providers discover a probe skill through provider-native mechanisms. Missing CLI or missing login must produce a clear setup error instead of silently falling back to old injection.

### D2: Legacy `system` Scope Compatibility

**Decision:** V1 does not keep a live `scope=system` compatibility alias.

**Recommended default:** No gray compatibility alias after V1 lands. In the V1 implementation, update all internal callers, RPC handlers, frontend payloads, launch skill selection UI, tests, seed data, and docs to emit explicit `scope=personal` plus `personal_type=user`. `user` is the stable wire enum for human-authored personal skills; UI copy may call it "human" or "user-created", but V1-V3 schemas, directories, manifests, and audit records must not introduce a separate `human` wire value unless every plan/schema/test is changed together. Add a one-time migration/import for persisted old metadata and existing `~/.super-dolphin/skills` directories into `~/.super-dolphin/skills/personal/user`; reject new `scope=system` writes with `invalid_scope_system_removed`.

**Reason:** The old name is semantically wrong. Keeping a compatibility alias creates exactly the half-old, half-new runtime path this refactor is meant to remove.

**Implementation gate:** `rg "scope=system|skillScopeSystem|\\\"system\\\"" internal cmd docs` must show only migration rejection tests, historical docs, or removed code comments before V1 is considered complete. Skill editor, import, delete, summary generation, resolution UI/RPC, and provider-start payload builders must not emit or display a live `system` scope; removed chat launch-selection code must not be reintroduced.

### D3: Same-Name Conflict Button Order

**Decision:** Same-name conflicts default to no write and use the action order below.

**Recommended default:** Default action is no write. Present actions in this order:

- Project canonical vs personal canonical: `View diff`, `Rename personal`, `Disable personal for this project` (the UI may label this as `Use project version`, but the RPC action remains `disable_personal_for_project`).
- Personal type vs personal type: `View diff`, `Rename one`, `Merge manually`, `Keep selected`.
- Canonical vs unmanaged provider-native directory: `View unmanaged blocker`, `Import to personal/imported`, `Import to project`, `Take over after backup`.

**Reason:** Same-name conflicts are where silent shadowing hurts most. The safest default is no write. Viewing or acknowledging unmanaged content is not a resolution; the conflict must remain visible in the Skills UI until the user imports, renames/removes, disables, or takes over after explicit backup and hash display. Ordinary content conflicts do not block provider startup.

### D4: Personal Auto-Maintenance Default

**Decision:** V3 auto-maintenance is off by default.

**Recommended default:** Off by default. Enable per user through settings; only `personal/agent` can auto-apply after the setting is enabled.

**Reason:** The feature changes user-owned canonical files. Proposal-only behavior is the safe default.

### D5: Archive Retention

**Decision:** No purge in V1-V3.

**Recommended default:** No purge in V1-V3. V3 provides manual restore from archive and snapshots; permanent purge is a follow-up/future feature after retention policy is proven.

**Reason:** Skill directories are small, and archive is the rollback mechanism for self-maintenance. Adding purge before restore/rollback is proven weakens the safety model.

### D6: Proposal Model

**Decision:** V2 uses a dedicated auxiliary model configuration owned by Super-Dolphin.

**Recommended default:** Use a dedicated auxiliary model configuration owned by Super-Dolphin. V2 release acceptance requires a configured proposal model and a generation smoke that creates a structured proposal from a real trigger. If the model is unset because the user explicitly disabled proposal generation or the environment is broken, generation is disabled and the recorder may store redacted evidence in a durable evidence/outbox table when evidence capture is enabled, but that state does not satisfy V2 completion.

**Reason:** Proposal generation is a maintenance task, not the user-facing turn. Keeping it separately configured avoids surprising latency/cost and avoids feeding provider runtime with write authority.

### D7: Old `skilllibrary` / `skillforge` / `fbsd` Full Removal

**Decision:** Old runtime consumers are removed or migrated in this plan set.

**Recommended default:** V1 cutover owns full runtime removal. Remove old provider-runtime dependencies on `skilllibrary`, `skillforge`, and `fbsd` in the same implementation slice. Old `skillcandidate` production UI/RPC/writer paths must also be removed, disabled, or migrated before V1 acceptance; V2 may introduce the replacement `skill_proposals` model, but V1 cannot leave `skills/candidate/*` as a live old skill pipeline.

**Reason:** Full landing means no old skill injection/cache/library path remains callable at runtime. The safety check is compile/test proof, not a staged rollout.

### D8: Mirror Drift Button Order

**Decision:** Mirror drift defaults to no write and uses the action order below.

**Recommended default:** Default action is no write. Present actions in this order: `View diff`, `Save mirror as new skill`, `Sync mirror back to canonical`, `Canonical overwrites mirror`.

**Reason:** Project scope is team-reviewed, and personal scope still needs rollback. The safest default is inspection, then non-destructive save-as, then mutating paths.

### D9: V1 Archive Boundary

**Decision:** V1 owns low-level delete safety; V3 owns user-facing archive/restore workflows.

**Recommended default:** V1 foundation provides a low-level non-destructive archive primitive for personal delete so no personal canonical is permanently removed. V2 may generate a pending `archive_skill` proposal, but V2 apply must reject or defer archive execution with `archive_apply_requires_v3`. V3 owns pin/archive/restore UI, curator archive, snapshot, and rollback user workflows.

**Reason:** V1 needs delete safety. V3 needs the full maintenance UX.

### D10: Mutating Resolution Audit Order

**Decision:** Mutating paths must create backup and audit intent before changing files.

**Recommended default:** All mutating resolution/apply paths use the same order: preview hash -> backup -> audit intent -> mutate -> publish mirrors when needed -> audit finalize. If backup or audit intent fails, no files are changed. If publish or audit finalize fails after mutation, return a structured partial-failure report with the final hash and unresolved follow-up action.

**Reason:** Audit is part of the write contract, not a best-effort side effect. This keeps V1 resolution, V2 proposal apply, and V3 maintenance behavior consistent.

**Implementation gate:** Each plan that mutates skill files must include a regression test where audit intent fails and canonical, mirror, archive, and snapshot paths remain unchanged.

### D11: V1 Atomic Landing

**Decision:** V1 is not accepted until foundation, resolution UI/RPC, and provider cutover are all merged and verified together.

**Recommended default:** Implement in the listed order, but keep provider runtime cutover blocked behind code review until resolution list/preview/apply/export UI and RPC are available. A partial V1 branch may be tested internally, but there is no product state where old injection is removed and users cannot see and resolve ordinary skill content conflicts.

**Reason:** Full landing is only usable when users can discover and fix conflicts in the Skills UI while unrelated provider/chat functionality remains available.

## Execution Rules

- Use an isolated worktree for implementation.
- Do not push without a fresh user approval in the current workflow.
- Do not use `git add .`; stage only owned files.
- For every code plan, write failing tests before implementation.
- After each plan lands, update that plan with the commit hash and validation result.
- Provider smoke tests must state whether they used real installed provider CLIs or fake test doubles.

## Verification Matrix

| Surface | Required checks |
|---|---|
| Skill module filesystem/policy | `./scripts/test_with_guard.sh ./internal/module/skill -count=1` |
| Provider cutover | `./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/codexapp ./internal/platform/toolbridge ./internal/app -count=1`, mandatory frontend checks, `make sqlc-verify`, and `bash scripts/skill_native_smoke.sh` for release acceptance with installed Claude/Codex CLIs plus clear missing-CLI/login negative paths |
| V1 resolution UI/RPC | affected skill RPC tests plus mandatory frontend checks |
| Store/sqlc proposal work | `make sqlc-verify` plus affected store tests |
| V2 proposal generation | configured auxiliary proposal model smoke that creates a validated pending proposal from a real trigger; unset-model evidence-only mode is a negative-path test, not release acceptance |
| V3 maintenance UI/RPC | affected maintenance packages plus mandatory frontend checks for settings/maintenance UI |
| Broad cross-module cutover | `make guard` and `make build-plain` |
| Docs-only plan edits | No runtime tests required; run placeholder scan and `git diff --check` |

## Non-Goals Through V3

- MCP/API control plane for skill management.
- Marketplace install/upgrade/signature verification.
- Trust policy, third-party signature verification, and third-party skill security scanning.
- Project-level automatic writes or automatic PR generation.
- Multi-machine sync.
- Team-level central skill server.
- Complex provider adapter matrix beyond Claude/Codex.
- Cross-provider standardization of provider-native skill-call events.
- Writes to system provider skill roots `~/.claude/skills` or `~/.agents/skills` without explicit import/export/takeover.
- Provider mirror as source of truth.
- Permanent archive purge.
