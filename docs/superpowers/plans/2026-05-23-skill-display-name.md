# Skill Display Name Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split skill identity from human-readable labels so `name` remains a safe runtime identifier while `display_name` can contain spaces and natural language.

**Architecture:** Add `DisplayName` to skill metadata, parse `display_name` / `title` frontmatter, and keep all runtime paths, mirror manifests, conflict keys, deletes, and selected refs keyed by `Name`. Support legacy safe display-only `name` values by deriving the canonical identifier with `skillSlug()` and preserving the original as `DisplayName`; unsafe path/control names still fail fast.

**Tech Stack:** Go skill module, provider-native mirror publisher, Vue buildless frontend skill page/editor, Vitest, repository guard wrapper.

---

### Task 1: Backend Metadata Contract

**Files:**
- Modify: `internal/contract/skill.go`
- Modify: `internal/module/skill/skills_meta.go`
- Modify: `internal/module/skill/canonical_store.go`
- Test: `internal/module/skill/skills_meta_test.go`
- Test: `internal/module/skill/canonical_store_test.go`

- [x] Write failing tests for explicit `display_name` and safe legacy display-only `name`.
- [x] Run targeted tests and confirm RED.
- [x] Add `DisplayName` to `SkillInfo`, parse `display_name` / `title`, and resolve legacy safe display names into slug `Name` plus `DisplayName`.
- [x] Run targeted tests and confirm GREEN.

### Task 2: RPC And Mirror Projection

**Files:**
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/module/skill/rpc.go`
- Modify: `internal/module/skill/mirror_publisher.go`
- Modify: `internal/module/skill/mirror_manifest.go`
- Modify: `internal/module/skill/mirror_reconciler_actions.go`
- Modify: `internal/module/skill/mirror_reconciler_resolution.go`
- Test: `internal/module/skill/rpc_types_test.go`
- Test: `internal/module/skill/mirror_publisher_test.go`

- [x] Write failing tests proving RPC exposes `display_name` and generated mirrors rewrite legacy `name` to canonical `name` plus `display_name`.
- [x] Run targeted tests and confirm RED.
- [x] Thread `DisplayName` through list payloads and mirror copy/rewrite paths.
- [x] Run targeted tests and confirm GREEN.

### Task 3: Frontend Skill Management

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/utils/skill-parser.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/composables/useSkillEditor.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/SkillsPage.js`
- Test: `cmd/agent-terminal/frontend/vue-app/skill-parser.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/skills-page.test.js`

- [x] Write failing tests for parsing/building `display_name` and rendering display names while preserving identity name for actions.
- [x] Run targeted Vitest tests and confirm RED.
- [x] Add editor state/input and list card rendering for display labels.
- [x] Run targeted Vitest tests and confirm GREEN.

### Task 4: Legacy Alias Compatibility And Mirror Root Symlinks

**Files:**
- Modify: `internal/module/skill/skills_fs.go`
- Modify: `internal/module/skill/skills_match.go`
- Modify: `internal/module/skill/canonical_store.go`
- Modify: `internal/module/skill/mirror_publisher.go`
- Modify: `internal/module/skill/mirror_reconciler.go`
- Add: `internal/module/skill/mirrorpath/path.go`
- Test: `internal/module/skill/skills_fs_test.go`
- Test: `internal/module/skill/skills_match_test.go`
- Test: `internal/module/skill/canonical_store_test.go`
- Test: `internal/module/skill/mirror_publisher_test.go`

- [x] Write failing tests for legacy display-name read/delete, explicit `@display_name`, configured display-name aliases, project policy aliases, and valid mirror root symlinks.
- [x] Confirm RED against the existing strict lookup paths.
- [x] Resolve existing-skill references by canonical `name`, `display_name`, and safe legacy display-only `name` while keeping new runtime names strict.
- [x] Ensure local directory import can convert safe legacy display-only skill names into canonical `name` plus `display_name`.
- [x] Port the valid mirror root symlink resolution from remote `00725eb9e3c258d267cd4225db74f4017f32e92f` without adopting its spaces-in-`name` behavior.
- [x] Keep unsafe path values, unsafe symlink entries, and non-`skills` root symlinks fail-closed.

### Task 5: Verification And Review

**Files:**
- Verify changed surfaces only.

- [x] Run `./scripts/test_with_guard.sh ./internal/module/skill ./internal/module/skill/identity ./internal/module/skill/mirrorpath -count=1`.
- [x] Run frontend `node scripts/size-guard.cjs`, targeted Vitest tests, full `npx vitest run`, and `npm run build` from `cmd/agent-terminal/frontend`.
- [x] Run `git diff --check`.
- [x] Review diff for unrelated churn, generated mirror edits, and baseline drift.
