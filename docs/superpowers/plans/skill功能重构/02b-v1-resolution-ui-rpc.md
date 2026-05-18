# V1 Resolution UI RPC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide the user-facing UI and RPC surface for V1 import/export/takeover and drift/conflict resolution so canonical and provider-native mirrors can be operated safely.

**Architecture:** V1 foundation detects conflicts and exposes backend resolution primitives; this plan turns those primitives into explicit user decisions. UI/RPC never treats provider mirror directories as canonical by default, and every mutating resolution requires preview, backup location, hashes, and audit outcome.

**Tech Stack:** Go 1.25.7, existing skill RPC handlers, existing frontend after locating source with `rg`, stdlib diff/JSON helpers, mandatory repo frontend checks.

---

## File Structure

- Modify: `internal/module/skill/rpc.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/module/skill/rpc_types_test.go`
- Create: `internal/module/skill/rpc_resolution_test.go`
- Modify: `internal/module/skill/mirror_reconciler.go` only for missing action parameters needed by the RPC wrapper; do not move foundation primitive ownership from plan 01.
- Modify: `internal/module/skill/mirror_reconciler_test.go` only for RPC-driven edge cases not already covered by plan 01 primitive tests.
- Modify: `cmd/agent-terminal/frontend/vue-app/services/skills-api.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/SkillsPage.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/composables/useSkillEditor.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/composables/useLaunchSkillSelection.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/components/LaunchSkillPicker.js`
- Modify: provider-start frontend builders after locating them with `rg "scope|system|skill" cmd/agent-terminal/frontend/vue-app`
- Modify: frontend tests after locating the existing test pattern with `rg`
- Modify: `README.md` only if the user-facing docs mention manual import/export/takeover

Do not modify provider startup in this plan. Provider startup belongs to `02-v1-provider-cutover.md`. Do not add a command-line entrypoint in V1; older operator-workflow notes are intentionally replaced here by UI/RPC.

## Task 1: Resolution List And Preview RPC

**Files:**
- Modify: `internal/module/skill/rpc.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Test: `internal/module/skill/rpc_resolution_test.go`

- [ ] **Step 1: Write preview tests**

Cover:

- `skills/resolution_list` returns unresolved same-name, mirror drift, unmanaged, canonical-deleted-with-drift, and multi-mirror-drift items for a `cwd`
- `skills/resolution_list` and `ui/dashboard/get(page=skills)` return success when project `.agent/skills` has the same name as a personal seeded/hub skill; the conflict is visible as an unresolved item instead of bubbling a hard RPC error such as `skill same-name conflict: ...`
- each unresolved item includes `conflict_id`, `kind`, `scope`, `personal_type`, `name`, provider entries, source/target hashes, and available actions
- list derives repo fingerprint from `cwd` server-side and does not accept client-supplied repo identity
- project mirror drift returns `view_diff`, `save_as_new_skill`, `sync_back_to_canonical`, `canonical_overwrite_mirror`
- personal mirror drift returns `view_diff`, `save_as_new_personal_skill`, `sync_back_to_personal`, `personal_overwrite_mirror`
- `canonical_deleted_with_drift` returns `view_diff`, `save_as_new_skill` or `save_as_new_personal_skill` according to original scope, `sync_back_to_canonical` or `sync_back_to_personal` to restore canonical from the drifted mirror, and `confirm_delete_drifted_mirror` to back up and remove the owned drifted mirror
- project canonical vs personal canonical same-name conflict returns `view_diff`, `rename_personal`, `disable_personal_for_project`
- personal type vs personal type same-name conflict returns `view_diff`, `rename_personal_type`, `merge_manually`, `keep_selected`
- unmanaged provider-native same-name returns `view_unmanaged`, `import_to_personal_imported`, `import_to_project`, `takeover_provider_skill`
- preview includes absolute source path, target path, source hash, target hash, backup path, and provider for every affected provider
- preview request includes the intended action plus action-specific parameters, and the returned `preview_hash` is bound to that action envelope rather than to a generic diff view
- `multi_mirror_drift` rejects multi-provider `sync_back_to_canonical` or `sync_back_to_personal` when provider source hashes differ, unless the user chooses one source provider, saves each as a new skill, or enters an explicit merge preview
- `save_as_new_skill` and `save_as_new_personal_skill` require `new_name` and preview the exact canonical target path; target already exists, target changes after preview, and same-name conflict after preview are all rejected
- `keep_selected` writes an explicit personal canonical selection policy and is not a temporary UI-only preference
- preview does not write canonical or mirror files

- [ ] **Step 2: Implement list/preview RPC and DTOs**

Expose `skills/resolution_list` with request and response shapes:

```json
{
  "cwd": "/repo",
  "include_resolved": false
}
```

```json
{
  "items": [
    {
      "conflict_id": "sha256:...",
      "kind": "multi_mirror_drift",
      "scope": "personal",
      "personal_type": "user",
      "name": "skill-name",
      "available_actions": ["view_diff", "rename_personal"],
      "provider_entries": [
        {
          "provider": "claude",
          "source_path": "/abs/source",
          "target_path": "/abs/target",
          "source_hash": "sha256:...",
          "target_hash": "sha256:..."
        },
        {
          "provider": "codex",
          "source_path": "/abs/source",
          "target_path": "/abs/target",
          "source_hash": "sha256:...",
          "target_hash": "sha256:..."
        }
      ]
    }
  ]
}
```

Expose `skills/resolution_preview` with request and response shapes:

```json
{
  "cwd": "/repo",
  "scope": "project",
  "personal_type": "",
  "provider": "claude",
  "providers": ["claude", "codex"],
  "source_provider": "claude",
  "source_path_id": "provider:claude",
  "name": "skill-name",
  "conflict_id": "sha256:...",
  "action": "sync_back_to_canonical",
  "new_name": "",
  "keep_source_id": "",
  "merge_content_hash": "",
  "disable_policy_target": "",
  "include_diff": true
}
```

```json
{
  "conflict_id": "sha256:...",
  "kind": "mirror_drift",
  "items": [
    {
      "action": "sync_back_to_canonical",
      "provider": "claude",
      "preview_id": "resolution-preview:...",
      "source_provider": "claude",
      "source_path_id": "provider:claude",
      "source_path": "/abs/source",
      "target_path": "/abs/target",
      "source_hash": "sha256:...",
      "target_hash": "sha256:...",
      "preview_hash": "sha256:...",
      "backup_path": "/abs/backup"
    }
  ]
}
```

Use stable action names:

```go
const (
	ResolutionViewDiff                  = "view_diff"
	ResolutionViewUnmanaged             = "view_unmanaged"
	ResolutionImportPersonal            = "import_to_personal_imported"
	ResolutionImportProject             = "import_to_project"
	ResolutionTakeoverProvider          = "takeover_provider_skill"
	ResolutionSaveAsNewSkill            = "save_as_new_skill"
	ResolutionSaveAsNewPersonal         = "save_as_new_personal_skill"
	ResolutionSyncBackCanonical         = "sync_back_to_canonical"
	ResolutionSyncBackPersonal          = "sync_back_to_personal"
	ResolutionCanonicalOverwrite        = "canonical_overwrite_mirror"
	ResolutionPersonalOverwrite         = "personal_overwrite_mirror"
	ResolutionRenamePersonal            = "rename_personal"
	ResolutionDisablePersonalForProject = "disable_personal_for_project"
	ResolutionRenamePersonalType        = "rename_personal_type"
	ResolutionMergeManually             = "merge_manually"
	ResolutionKeepSelected              = "keep_selected"
	ResolutionConfirmDeleteDriftedMirror = "confirm_delete_drifted_mirror"
)
```

The preview response item must echo the requested action. `view_diff` previews are read-only and must not produce a preview id/hash that can be used by mutating apply. For mutating actions, the server creates a short-lived `preview_id` and computes `preview_hash` over a sorted server-side envelope containing conflict id, action, provider or stable path id, source provider, source path id, source path, target path, source hash, target hash, `new_name`, `keep_source_id`, `merge_content_hash`, `disable_policy_target`, `confirm_delete_mirror_hash`, and generated backup path. The envelope never includes `preview_hash` itself. A client cannot preview `view_diff` and apply `sync_back_to_canonical`, cannot switch provider or source provider after preview, and cannot reuse a preview after action parameters change.

- [ ] **Step 3: Run preview tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'Test.*Resolution.*List|Test.*Resolution.*Preview|Test.*ResolutionRPC|Test.*Unmanaged' -count=1
```

Expected: preview reports are complete and read-only.

## Task 2: Import And Takeover Resolutions

**Files:**
- Modify: `internal/module/skill/mirror_reconciler.go`
- Modify: `internal/module/skill/rpc.go`
- Test: `internal/module/skill/mirror_reconciler_test.go`
- Test: `internal/module/skill/rpc_resolution_test.go`

- [ ] **Step 1: Write mutation tests**

Cover:

- `import_to_personal_imported` copies external provider skill to `~/.super-dolphin/skills/personal/imported/<name>`
- `import_to_project` copies external provider skill to `<repo>/.agent/skills/<name>`
- `takeover_provider_skill` first backs up unmanaged provider directory, then writes ownership manifest, then treats the mirror as managed
- `view_unmanaged` records no write and keeps conflict unresolved; it is a read-only acknowledgement, not a resolution, and provider startup remains fail-closed
- takeover fails if backup or audit fails
- import and takeover reject path traversal, symlinked provider roots, and symlinked files after canonicalization
- canonical same-name actions require the preview hash for every affected canonical path
- batch apply rejects when any item hash changed after preview
- partial failure reports per-item result and leaves later items untouched
- `disable_personal_for_project` writes a project-scoped canonical resolution policy, keeps the personal canonical directory unchanged, changes the effective set only for the current project, and triggers mirror publish/reconcile for the current `cwd`
- `disable_personal_for_project` is not implemented as provider-mirror opt-out, provider skip, or launch-unblocking unmanaged acknowledgement
- `sync_back_to_canonical` and `sync_back_to_personal` reject multi-provider apply when provider source hashes differ, unless the action carries one selected source provider or a server-generated merge preview hash
- `save_as_new_skill` and `save_as_new_personal_skill` require `new_name`, reject existing targets, and reject if the target appeared or changed after preview
- `confirm_delete_drifted_mirror` is valid only for `canonical_deleted_with_drift` on an owned mirror whose manifest source canonical no longer exists; preview records the drifted mirror hash, generated backup path, provider target id, relative mirror path, and expected missing canonical id; apply backs up the drifted mirror, writes audit intent, deletes only that owned mirror, updates/removes its manifest entry, writes audit finalize, and keeps unmanaged or hash-changed mirrors unresolved
- `keep_selected` writes a personal selection policy under the resolved Super-Dolphin home with source ids and personal types only; it keeps non-selected canonical directories editable through direct APIs and records audit intent/finalize around the policy write

- [ ] **Step 2: Implement apply RPC and safe mutation order**

Expose `skills/resolution_apply` with request shape:

```json
{
  "cwd": "/repo",
  "actor": "user:local",
  "reason": "sync reviewed mirror changes back to project canonical",
  "actions": [
    {
      "conflict_id": "sha256:...",
      "action": "sync_back_to_canonical",
      "scope": "project",
      "personal_type": "",
      "providers": ["claude", "codex"],
      "preview_id": "resolution-preview:...",
      "source_provider": "claude",
      "source_path_id": "provider:claude",
      "name": "skill-name",
      "preview_hashes": {
        "claude": "sha256:...",
        "codex": "sha256:..."
      },
      "backup_paths": {
        "claude": "/abs/backup/claude",
        "codex": "/abs/backup/codex"
      },
      "new_name": "",
      "keep_source_id": "",
	      "merge_content_hash": "",
	      "disable_policy_target": "",
	      "confirm_delete_mirror_hash": ""
	    }
  ],
  "confirmed": true,
  "backup_acknowledged": true
}
```

Each action carries its own `conflict_id` and mutating `preview_id`; there is no batch-level conflict id. `providers` is required when an action affects more than one provider mirror, and the server expands it into per-provider apply results. `provider` remains allowed only for single-provider actions. `source_provider` and `source_path_id` are required whenever a drift action copies bytes from a provider mirror back to canonical or personal canonical. `preview_hashes` and `backup_paths` are keyed by provider for provider-affecting actions and by stable path id for canonical-only actions; apply must include the exact generated backup path returned by preview for each affected provider/path. The server loads the stored envelope by `preview_id`, verifies the caller supplied the matching preview hash and backup path, then re-stats source/target and recomputes hashes before any write. The envelope binds action, provider/path, source provider, source path id, source path, target path, source hash, target hash, action-specific parameters, and backup path, but never includes `preview_hash` as an input to itself. Batch apply can therefore include several unrelated conflicts in one request without losing the mapping between preview, action, source, provider, backup, and partial failure.

`new_name` is required for rename and save-as-new actions. `keep_source_id` is required for `keep_selected`. `merge_content_hash` is required for `merge_manually` and must match the server-generated diff/preview. `disable_policy_target` is required for `disable_personal_for_project` and must use a server-validated target such as `personal/user/<name>` or `personal/agent/<name>`, never a provider mirror path. `confirm_delete_mirror_hash` is required for `confirm_delete_drifted_mirror` and must match the drifted owned mirror hash from preview. Multi-provider sync-back actions are valid only when all selected provider source hashes match; otherwise the action must name exactly one `source_provider`/`source_path_id` or use `merge_content_hash` from an explicit merge preview. If the source is ambiguous, missing, or not part of the original preview envelope, apply must reject before backup/audit. The response must return per-item `conflict_id`, `action`, `provider`, `source_provider`, `source_path_id`, `source_path`, `target_path`, `status`, `old_hash`, `new_hash`, `backup_path`, `audit_id`, `error_kind`, and `unresolved_followup_action` for partial failures so the UI can keep failed items mapped and visible.

`disable_personal_for_project` writes a project-local canonical policy file, for example `<repo>/.agent/skills/.super-dolphin-skill-policy.json`, with relative skill names and personal type only; it must not store absolute cwd or raw user home. The server derives repo fingerprint from `cwd`, validates that the current unresolved same-name conflict still exists, writes audit intent, writes the policy, recomputes the effective set for this `cwd`, then publishes mirrors for the resulting effective set. The personal canonical skill remains in `~/.super-dolphin/skills/personal/<type>/<name>` and remains available to other projects that do not carry this project policy. Tests must prove list/read/match/launch for the current project excludes the disabled personal target while direct personal edit/delete outside that project still targets the original personal canonical skill.

Do not implement `use_project_version` as a separate action. Project-wins semantics are exactly `disable_personal_for_project`; any implementation that skips personal only at launch time, hides it only in the UI, or changes provider mirror selection without writing the project canonical policy is invalid.

`keep_selected` writes an owner-scoped personal canonical selection policy under the resolved Super-Dolphin home, with selected source id, excluded source ids, skill name, personal types, hashes, actor, and audit id. It affects effective-set/match/launch/publish resolution for that owner only and must be consumed by read, match, launch, and publish paths consistently. It does not delete, archive, or mutate non-selected personal canonical directories, and direct explicit personal-target APIs can still edit them.

Mutating actions must run in this order:

1. re-stat source and target
2. recompute hashes
3. verify hashes match preview
4. create backup
5. write audit intent with action, actor, reason, preview id, preview hashes, and backup path
6. mutate canonical or manifest
7. publish provider mirrors when canonical changed
8. write audit finalize with success or failure result

If backup creation or audit intent write fails, the method returns an error and leaves files unchanged. If mutation succeeds but publish or audit finalize fails, return a structured partial-failure report with the mutation hash and unresolved follow-up action; do not hide the failure behind a success response.

- [ ] **Step 3: Run mutation tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'Test.*Import.*Resolution|Test.*Takeover|Test.*ViewUnmanaged|Test.*Resolution.*Apply' -count=1
```

Expected: unmanaged provider content is never overwritten without backup and explicit takeover.

## Task 3: Drift And Conflict Resolution UI

**Files:**
- Modify: frontend files after discovery
- Test: frontend tests after discovery

- [ ] **Step 1: Locate existing UI source**

Run:

```bash
rg -n "skill|candidate|conflict|drift|settings" cmd/agent-terminal frontend internal -g'*.vue' -g'*.ts' -g'*.js'
```

Use the discovered files. Do not assume `cmd/agent-terminal/frontend/src` exists.

- [ ] **Step 2: Add drift/conflict decision surface**

UI must show one row per conflict or drift item:

- conflict kind
- provider
- scope and `personal_type`
- source path
- canonical path
- source hash
- canonical hash
- backup path for mutating actions
- diff preview before any write
- action buttons in D3 and D8 order from `00-implementation-index-and-decisions.md`
- independent selection state per item
- batch apply button that sends per-action `conflict_id`, `providers`, and preview hashes for every selected item

The UI must first call `skills/resolution_list`; it must not require the user to know a `conflict_id` in advance.

The Skills dashboard must render unresolved same-name conflicts as data, not as a page-level failure. A conflict between repo-local project canonical `.agent/skills/<name>` and a personal seeded/hub skill is a normal V1 state; the page must show the blocking item, available resolution actions, and provider startup fail-closed status without crashing or hiding the rest of the skill list. Conflicted skills must not be silently published to Claude/Codex mirrors before the user resolves them.

- [ ] **Step 3: Add UI tests**

Mock these payloads:

- project mirror drift
- personal mirror drift
- unmanaged same-name provider skill
- project canonical vs personal canonical same-name conflict
- project `.agent/skills` vs personal seeded/hub same-name conflict that previously produced `skill same-name conflict: ...`
- personal type vs personal type same-name conflict
- `multi_mirror_drift`
- `canonical_deleted_with_drift`
- multi-provider conflict list with at least Claude and Codex entries

Assert the default selected action is no write, mutating actions are disabled until preview is loaded, per-item selection does not affect other items, batch apply includes per-action conflict ids, provider lists, preview hash maps, and hash validation for all selected items, and partial failure leaves unresolved items visible. For same-name conflicts, assert the Skills page returns a successful dashboard response and renders conflict rows/actions rather than showing a fatal load error. For `canonical_deleted_with_drift`, assert the UI offers restore canonical from mirror, save as new skill, and confirm-delete owned drifted mirror; confirm-delete must show backup path and the exact mirror hash being deleted.

- [ ] **Step 4: Migrate existing create/edit/import personal scope UI**

Current skill create/edit/import/delete, launch skill selection, match preview, and provider-start payload builders must stop emitting or displaying `scope=system`. Update the existing skill editor/API payloads so personal writes send:

```json
{
  "scope": "personal",
  "personal_type": "user"
}
```

Add frontend tests for `writeSkill`, `importSkills`, editor save, delete, `useLaunchSkillSelection`, `LaunchSkillPicker`, and provider-start config builders proving `system` is not sent or displayed. Delete must send `name`, `scope`, and `personal_type`; it must not call the old `{name}`-only path. Add backend RPC tests rejecting new `scope=system` writes with `invalid_scope_system_removed`.

- [ ] **Step 5: Retire old candidate review UI/RPC entrypoints for V1**

V1 must not keep `skills/candidate/*` as a live old skill pipeline. Before V2 proposal UI lands, remove or hide existing candidate review screens and make old candidate RPC handlers compile-proven unreachable or return a removed/unsupported error that does not write `skill_candidates`. V2 owns the replacement `skill_proposals/*` workflow.

- [ ] **Step 6: Run frontend checks**

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: drift/conflict UI builds and tested actions match backend action names.

## Task 4: Explicit Export To External Provider Directories

**Files:**
- Modify: `internal/module/skill/rpc.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Test: `internal/module/skill/rpc_resolution_test.go`
- Modify: `cmd/agent-terminal/frontend/vue-app/services/skills-api.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/SkillsPage.js`
- Modify: frontend export dialog/menu components after locating existing patterns with `rg`
- Test: frontend export tests after locating the existing frontend test pattern

- [ ] **Step 1: Write export tests**

Cover:

- export to `~/.claude/skills` requires explicit destination path
- export to `~/.codex/skills` requires explicit destination path
- export rejects destinations outside canonicalized provider roots `~/.claude/skills` and `~/.codex/skills`, unless a future explicit external-root allowlist is implemented
- export rejects symlinked destination roots and path traversal after `EvalSymlinks` / canonicalization
- export refuses to overwrite existing external skill unless user passes preview hash and `confirmed=true`
- export preview returns canonicalized destination path, existing destination hash, source hash, backup path, preview hash, diff summary, and whether overwrite would occur
- export writes backup before overwrite
- export apply follows the same safe order as resolution apply: re-stat source/destination, verify preview envelope and hashes, create backup, write audit intent, mutate destination, write audit finalize
- export backup creation failure leaves destination unchanged
- export apply rejects if source or destination hash changed after preview
- export apply writes audit intent before backup-protected mutation; audit intent failure leaves destination unchanged, and audit finalize or post-write failure returns structured partial failure with final hash and follow-up action
- export does not convert external directory into a Super-Dolphin managed mirror unless the user separately chooses takeover
- export UI is present in V1 for project and personal canonical skill rows; V1 must not ship backend-only export

- [ ] **Step 2: Implement export preview RPC**

Expose `skills/export_external_preview` with parameters:

```json
{
  "cwd": "/repo",
  "scope": "project",
  "personal_type": "",
  "name": "skill-name",
  "destination": "/Users/me/.claude/skills",
  "include_diff": true
}
```

Response:

```json
{
  "preview_id": "export-preview:...",
  "canonicalized_destination": "/Users/me/.claude/skills/skill-name",
  "canonicalized_destination_root": "/Users/me/.claude/skills",
  "canonicalized_destination_path": "/Users/me/.claude/skills/skill-name",
  "source_path": "/repo/.agent/skills/skill-name",
  "source_hash": "sha256:...",
  "destination_hash": "sha256:...",
  "preview_hash": "sha256:...",
  "backup_path": "/abs/backup",
  "would_overwrite": true,
  "diff_summary": "..."
}
```

The frontend must obtain `preview_id`, `preview_hash`, `canonicalized_destination_root`, `canonicalized_destination_path`, and `backup_path` from this server RPC. It must not compute hashes or canonicalize destinations itself.

The export preview hash is computed over a sorted server-side envelope containing action `export_external`, `cwd` repo fingerprint, scope, personal type, skill name, canonical source path, canonical source hash, canonicalized destination root, canonicalized destination skill path, destination hash, backup path, and overwrite flag. The envelope never includes `preview_hash` itself. Export apply must reject if the caller changes destination, scope, personal type, name, source hash, destination hash, canonicalized destination, or backup path after preview, even when the new target happens to have the same file hash. `view` or dry-run style previews must not be reusable for `export_external` apply.

- [ ] **Step 2b: Implement export UI**

Add an explicit export action in the Skills page for project and personal canonical skills. The UI must:

- require the user to choose or type the external provider destination
- call `skills/export_external_preview` before enabling apply
- show canonicalized destination, overwrite state, backup path, and diff summary
- require confirmation and backup acknowledgement before overwrite
- call `skills/export_external` with the server `preview_id`, preview hash, canonicalized destination fields, source/destination hashes, and backup path from the preview response
- keep export separate from takeover; export does not mark the destination as a managed mirror

- [ ] **Step 3: Implement export apply RPC**

Expose `skills/export_external` with parameters:

```json
{
  "cwd": "/repo",
  "scope": "project",
  "personal_type": "",
  "name": "skill-name",
  "preview_id": "export-preview:...",
  "destination": "/Users/me/.claude/skills",
  "canonicalized_destination_root": "/Users/me/.claude/skills",
  "canonicalized_destination_path": "/Users/me/.claude/skills/skill-name",
  "source_hash": "sha256:...",
  "destination_hash": "sha256:...",
  "backup_path": "/abs/backup",
  "preview_hash": "sha256:...",
  "confirmed": true,
  "actor": "user:local",
  "reason": "explicit external export",
  "backup_acknowledged": true
}
```

Apply must either load a server-persisted preview by `preview_id` or reject if the preview has expired. It must compare the caller-supplied `canonicalized_destination_root`, `canonicalized_destination_path`, `source_hash`, `destination_hash`, `backup_path`, and `preview_hash` with the stored preview envelope before backup/audit/mutation. Re-canonicalizing a changed destination to the same final string is not enough; the submitted fields must match the server preview and the current source/destination hashes must still match the preview.

- [ ] **Step 4: Run export tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'Test.*ExportExternal' -count=1
```

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: external export is explicit, backed up on overwrite, visible in UI, and not treated as mirror ownership.

## Task 5: Plan-Level Verification

- [ ] **Step 1: Run backend tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/contract -run 'Test.*Resolution|Test.*Import|Test.*Takeover|Test.*Export|Test.*Drift' -count=1
```

- [ ] **Step 2: Run frontend checks**

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

- [ ] **Step 3: Check docs and status**

Run:

```bash
git diff --check
git status --short
```

Expected: provider-native mirrors under `.claude/` and `.codex/` are not staged or committed.

## Accepted Defaults And Gates For This Plan

- D3 same-name conflict button order is fixed by `00-implementation-index-and-decisions.md`.
- D8 mirror drift button order is fixed by `00-implementation-index-and-decisions.md`.
- V1 must implement export preview and export apply RPCs; the frontend must use server-produced preview hashes and canonicalized destinations.
- V1 must implement export UI; backend-only export is not accepted.
- V1 must migrate create/edit/import/delete/launch selection/match preview/provider-start UI away from live `system` scope.
- No provider-mirror opt-out action or launch-unblocking unmanaged skip action is accepted in V1.
- Dashboard/Skills page load must not hard-fail on unresolved conflicts. Same-name conflicts are blocking data with resolution actions; provider mirrors publish only non-conflicted records until the conflict is resolved.
- Takeover copy semantics are fixed: backup then mark managed, without silently changing canonical content.
- `disable_personal_for_project` is a project canonical conflict policy, not a provider mirror opt-out. It preserves personal canonical content and changes only the current project's effective set and mirrors derived from that effective set.
- `use_project_version` is not a standalone action; use `disable_personal_for_project` for project-wins semantics.
- `skills/resolution_apply` must support true multi-conflict batch apply by carrying `conflict_id`, affected provider list, and provider/path-keyed preview hashes on each action item.
