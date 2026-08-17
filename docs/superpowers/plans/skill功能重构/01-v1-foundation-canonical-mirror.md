# V1 Foundation Canonical Mirror Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the canonical skill store, effective skill set, ownership manifest, mirror publisher, and drift/conflict detector without changing Claude/Codex runtime behavior yet.

**Architecture:** `internal/module/skill` becomes the owner of project canonical and personal canonical roots. Provider-native mirrors are generated outputs with manifest ownership and hash checks; mirror files are never treated as canonical unless the user explicitly resolves drift.

**Tech Stack:** Go 1.25.7, stdlib filesystem APIs, existing `internal/module/skill` parsing and validation helpers, Fx ports in `internal/contract`, table-driven unit tests.

---

## File Structure

Create focused files in `internal/module/skill`:

- Create: `internal/module/skill/scope_model.go`
- Create: `internal/module/skill/scope_model_test.go`
- Create: `internal/module/skill/canonical_store.go`
- Create: `internal/module/skill/canonical_store_test.go`
- Create: `internal/module/skill/mirror_manifest.go`
- Create: `internal/module/skill/mirror_manifest_test.go`
- Create: `internal/module/skill/mirror_hash.go`
- Create: `internal/module/skill/mirror_hash_test.go`
- Create: `internal/module/skill/mirror_publisher.go`
- Create: `internal/module/skill/mirror_publisher_test.go`
- Create: `internal/module/skill/mirror_reconciler.go`
- Create: `internal/module/skill/mirror_reconciler_test.go`
- Modify: `internal/module/skill/service.go`
- Modify: `internal/module/skill/module.go`
- Modify: `internal/module/skill/contract.go`
- Modify: `internal/module/skill/skills_meta.go`
- Modify: `internal/module/skill/skills_fs.go`
- Modify: `internal/module/skill/skills_import.go`
- Modify: `internal/module/skill/skills_match.go`
- Modify: `internal/module/skill/events.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/module/skill/rpc_types_test.go`
- Modify: `internal/contract/skill.go`
- Modify: `internal/dto/ui/event.go`
- Modify: `internal/module/turn/skills.go`
- Modify migrations/sql/store files only if the data-scope gate finds persisted non-candidate `scope=system` metadata that must survive V1
- Modify: `.gitignore`

Do not modify provider packages in this plan. Provider startup changes belong to `02-v1-provider-cutover.md`. User-facing import/export/takeover screens belong to `02b-v1-resolution-ui-rpc.md`; this plan still owns the backend primitives and safety tests those screens call.

## Task 1: Scope And Path Model

**Files:**
- Create: `internal/module/skill/scope_model.go`
- Test: `internal/module/skill/scope_model_test.go`
- Modify: `internal/module/skill/service.go`

- [ ] **Step 1: Write failing path tests**

Add tests covering these exact cases:

```go
func TestResolveCanonicalRoots_ProjectAndPersonal(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	got := resolveCanonicalRoots(project, home)
	assertPath(t, got.Project, filepath.Join(project, ".agent", "skills"))
	assertPath(t, got.Personal["user"], filepath.Join(home, ".super-dolphin", "skills", "personal", "user"))
	assertPath(t, got.Personal["agent"], filepath.Join(home, ".super-dolphin", "skills", "personal", "agent"))
	assertPath(t, got.Personal["imported"], filepath.Join(home, ".super-dolphin", "skills", "personal", "imported"))
	assertPath(t, got.Personal["hub"], filepath.Join(home, ".super-dolphin", "skills", "personal", "hub"))
}

func TestNormalizeSkillScopeRejectsNewSystemWrites(t *testing.T) {
	_, _, err := normalizeSkillTarget("system", "")
	if !errors.Is(err, ErrSkillSystemScopeRemoved) {
		t.Fatalf("normalizeSkillTarget(system) error = %v, want ErrSkillSystemScopeRemoved", err)
	}
}
```

- [ ] **Step 2: Add scope types and root resolver**

Implement these exported or package-private constants:

```go
const (
	skillScopeProject  = "project"
	skillScopePersonal = "personal"

	personalSkillTypeUser     = "user"
	personalSkillTypeAgent    = "agent"
	personalSkillTypeImported = "imported"
	personalSkillTypeHub      = "hub"
)
```

`personalSkillTypeUser` is the stable wire enum for human-authored or explicitly user-created personal skills. Product copy may call this bucket "human", but the V1-V3 filesystem directories, DTOs, manifests, policy files, audit records, and tests use `user` unless a separate all-plan enum migration is approved.

`defaultProjectSkillsRoot(projectRoot)` stays `<repo>/.agent/skills`. Add `defaultSuperDolphinHome()` using `SUPER_DOLPHIN_HOME` when set, otherwise `~/.super-dolphin`.

Add the shared owner identity helper in V1 because foundation already needs owner-scoped `keep_selected`, personal audit, personal archive, and provider-home state. The helper derives:

```text
owner_key = "sd_owner:" + hex(HMAC-SHA256(app_install_salt, normalized_super_dolphin_home + "\n" + os_uid + "\n" + app_profile))
```

`app_install_salt` is generated under the resolved Super-Dolphin home with owner-only file permissions before any owner-scoped policy/write path can persist data. Database rows, RPC payloads, audit extras, local manifests, logs, and policy files store only `owner_key`; they must not store raw home path, OS uid, username, profile path, or platform account name. V2 and V3 reuse this exact helper instead of re-deriving identity.

- [ ] **Step 3: Remove live `system` inputs and update DTOs**

Change scope normalization so new `scope=system` writes are rejected with `ErrSkillSystemScopeRemoved`. If persisted old metadata exists, add a one-time migration/normalizer that rewrites it to `scope=personal` and `personal_type=user` before normal runtime code sees it. Returned DTOs must expose `scope=personal` and `personal_type=user`. Update `contentParams`, `skillNamedContentParams`, `skillSummaryWriteParams`, `importSkillDirParams`, `deleteLocalSkillParams`, `SkillInfo`, and `skillListItem` so storage scope is distinct from `Trust`.

Add a one-time filesystem migration/import path for existing user skills under the old default root `~/.super-dolphin/skills`. The migration copies or moves only valid skill directories into `~/.super-dolphin/skills/personal/user/<skill-name>` after validating `SKILL.md`, computing source and destination hashes, and writing a migration report. It must not overwrite an existing personal canonical directory; same-name conflicts become explicit unresolved conflicts. After migration, normal V1 runtime scanning must not read `~/.super-dolphin/skills` as a live skill root.

Retire the old global-root override as runtime configuration. `SKILLS_ROOT`, `defaultSkillsRoot()`, `systemGlobalSkillsRoot()`, and any remaining `s.root` service field may be used only by the explicit migration/import command path, never by normal list/read/write/delete/match/hydration code after V1. Add regression tests proving `SKILLS_ROOT=$HOME/.super-dolphin/skills` does not make that directory a live scan/write root, while the one-time migration can still import from it by explicit migration flow.

Replace `DeleteLocal(ctx, name string)` with a structured target:

```go
type DeleteSkillParams struct {
	Name         string
	Scope        string
	PersonalType string
}

DeleteLocal(ctx context.Context, params DeleteSkillParams) (any, error)
```

Add a regression test where project and `personal/user` both contain the same skill name: deleting the project target removes only `.agent/skills/<name>`, and deleting the personal target archives only `~/.super-dolphin/skills/personal/user/<name>`.

Add regression coverage for old write surfaces that currently use system/global roots:

- `WriteRemote`
- `WriteSkillContent`
- `WriteSummary`
- `ImportLocalDir`
- `DeleteLocal`
- `skills/local/delete` RPC handler in `internal/module/skill/rpc.go`
- existing `~/.super-dolphin/skills` filesystem migration/import

Expected behavior: new `scope=system` write/import/delete inputs fail; migrated old metadata and old `~/.super-dolphin/skills` content are represented as `personal/user`; new UI/RPC emits explicit `scope=personal` plus `personal_type`; project writes continue to land only in `.agent/skills`; and no runtime scanner treats `~/.super-dolphin/skills`, `SKILLS_ROOT`, or `s.root` as a live root after migration.

Do not leave `WriteRemote`, `WriteSkillContent`, `WriteSummary`, or `skills/local/delete` hard-coded to `skillScopeSystem` or `s.root`. Either remove the old RPCs if no frontend/production caller remains, or change their request DTOs and service calls to structured targets with `scope` and `personal_type`. Add an RPC regression test proving `skills/local/delete` must carry `scope` and `personal_type`, rejects missing personal type for personal deletes, and deletes/archives only the addressed canonical target when project and personal skills share a name.

- [ ] **Step 4: Run focused tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'TestResolveCanonicalRoots|TestNormalizeSkillScope' -count=1
```

Expected: scope/path tests pass.

Add a source gate for this task:

```bash
rg -n "SKILLS_ROOT|defaultSkillsRoot|systemGlobalSkillsRoot|s\\.root" internal/module/skill internal/module/turn
```

Expected: remaining matches are migration/import-only code or tests proving old roots are not used for runtime scan/write/delete/match/hydration.

## Task 2: Canonical Store And Effective Set

**Files:**
- Create: `internal/module/skill/canonical_store.go`
- Test: `internal/module/skill/canonical_store_test.go`
- Modify: `internal/module/skill/skills_meta.go`
- Modify: `internal/module/skill/skills_fs.go`
- Modify: `internal/module/skill/skills_import.go`
- Modify: `internal/module/turn/skills.go`

- [ ] **Step 1: Write failing tests for scanning**

Cover project, all four personal types, and same-name collisions:

```go
func TestCanonicalStoreListIncludesProjectAndPersonal(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeSkill(t, filepath.Join(project, ".agent", "skills", "proj"), "proj")
	writeSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "user", "mine"), "mine")

	store := newTestCanonicalStore(project, home)
	got, conflicts, err := store.EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	assertHasSkill(t, got, "proj", "project", "")
	assertHasSkill(t, got, "mine", "personal", "user")
}

func TestEffectiveSetSameNameIsStrictConflict(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeSkill(t, filepath.Join(home, ".super-dolphin", "skills", "personal", "user", "build"), "build")

	_, conflicts, err := newTestCanonicalStore(project, home).EffectiveSet(context.Background(), project)
	if err != nil {
		t.Fatalf("EffectiveSet: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].Kind != "same_name" {
		t.Fatalf("conflicts = %+v, want same_name", conflicts)
	}
}
```

Add explicit same-name tests for every personal type collision that can shadow another personal skill:

- `personal/user` vs `personal/agent`
- `personal/user` vs `personal/imported`
- `personal/agent` vs `personal/imported`

For each active pair, `EffectiveSet` must return `same_name` conflict entries that include both `scope=personal` and the exact `personal_type`; it must not pick a winner by type priority, trust, lowercased name, or filesystem scan order. `personal/hub` is catalog-only and must not be scanned, mirrored, or treated as an active personal root.

- [ ] **Step 2: Implement canonical records**

Introduce a record shape with enough provenance for publisher decisions:

```go
type canonicalSkillRecord struct {
	Name         string
	Scope        string
	PersonalType string
	Dir          string
	SkillFile    string
	ContentHash  string
	DirHash      string
}
```

Use existing `parseSkillRecord`, `validateSkillName`, and `skillDirContentHash` where possible. Do not scan provider mirror roots.

Add a project-local resolution policy reader used only by the effective-set builder. The policy file lives under the project canonical skill area, for example `<repo>/.agent/skills/.super-dolphin-skill-policy.json`, and records project-scoped decisions such as `disable_personal_for_project` with relative skill name and personal type. It must not store absolute cwd, raw home path, raw owner uid, or provider mirror paths. The effective set applies this policy only for the current `cwd`: a disabled personal target is excluded from match/launch/publish for that project while the personal canonical directory remains readable/editable through explicit personal-target APIs and remains available to other projects.

Add tests proving a project policy that disables `personal/user/build` makes project `build` unambiguous for that `cwd`, does not delete or archive `~/.super-dolphin/skills/personal/user/build`, and does not affect another temp project without the policy file.

Add an owner-scoped personal selection policy reader for `keep_selected` decisions created by plan `02b`. The policy lives under the resolved Super-Dolphin home, stores only the V1-derived `owner_key`, skill name, selected source id, excluded source ids, exact personal types, content hashes, and relative canonical ids such as `personal/user/build`; it must not store raw home paths, raw OS uid, raw profile, or provider mirror paths. Policy files are owner-only and rejected if permissions are broader than the owner on platforms where this is observable. The effective-set builder applies this policy consistently for read, match, launch, and publish so a resolved personal-type same-name conflict does not reappear at runtime. Add tests where `personal/user/build` and `personal/agent/build` conflict, `keep_selected` picks one source, direct explicit APIs can still edit the non-selected canonical directory, and another owner/profile without the policy still sees the conflict.

- [ ] **Step 3: Update write/import/delete surfaces**

Project writes continue to land in `.agent/skills`. Personal writes require `personal_type`; new `scope=system` is rejected. Deletes must use `DeleteSkillParams{Name, Scope, PersonalType}` or an equivalent typed target so same-name project/personal skills cannot be accidentally deleted from the wrong root. Deletes in personal scope move to a home-relative archive location resolved under the current `resolvedSuperDolphinHome()`:

```text
~/.super-dolphin/skills/.archive/<timestamp>/<scope>/<type>/<skill-name>/
```

Project deletes remain physical deletes from `.agent/skills/<name>`.

Personal delete must write an audit intent event before moving the canonical directory and an audit finalize event after the move. If backup/archive location creation or audit intent write fails, delete returns an error and leaves canonical untouched. The archive record must include enough metadata to restore later: archive id or home-relative archive path, relative canonical id, scope, personal type, skill name, canonical hash, actor, and timestamp. It must not store raw home path, raw profile, raw uid, username, or absolute provider mirror path. V3 owns the user-facing restore flow, but V1 must not permanently delete personal canonical data.

Personal create/edit/import is still "free to edit" only in the sense that it does not require team review. It must remain recoverable: before mutating an existing personal canonical directory, write a backup or rollback manifest with old hash, target identity, actor, and timestamp, then write audit intent, mutate, and write audit finalize. If backup or audit intent fails, no personal canonical bytes are changed. New personal creates with no previous bytes write a creation recovery manifest and audit intent before the directory is made visible. Import must follow the same backup/audit/finalize order and must reject path traversal, symlinked directories, symlinked `SKILL.md`, symlinked support files, and detectable hardlink surprises before backup or mutation. Implement the validator with `lstat` on every path component plus `EvalSymlinks` / clean-path containment checks where supported; if the platform cannot prove a path is safe, reject before backup.

- [ ] **Step 4: Rewire existing read, expand, delete, and hydration paths**

Every existing business path that resolves skills by name must use the conflict-aware effective set:

- `ListSkills` reports conflicts and does not silently collapse project/personal duplicates
- `ReadLocal` / `Expand` returns an explicit same-name conflict error when the target name is ambiguous
- `DeleteLocal` uses `DeleteSkillParams` and never calls a first-match `resolveSkill` for ambiguous names
- legacy match preview and any remaining compatibility matcher do not lowercase-dedupe project/personal same-name skills into one candidate; chat launch auto-match is removed and must not be reintroduced
- turn hydration in `internal/module/turn/skills.go` does not build a lowercase map that overwrites same-name skills; it must either receive the conflict-free effective set or surface a conflict that prevents launch
- effective-set policy can disable a personal skill for one project without mutating personal canonical content or acting as a provider mirror opt-out

Add regression tests for project+personal and personal+personal same-name read, delete, list, compatibility match preview, and turn hydration. The expected result is conflict or explicit user resolution, not arbitrary first match, type-priority winner, trust winner, or lowercase dedupe shadowing. Chat launch auto-match is removed and must not be reintroduced.

- [ ] **Step 5: Run focused tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/module/turn -run 'TestCanonicalStore|TestEffectiveSet|Test.*Personal|Test.*SameName|Test.*Hydrat' -count=1
```

Expected: canonical store tests pass and existing scope tests still pass.

## Task 3: Mirror Manifest And Hashing

**Files:**
- Create: `internal/module/skill/mirror_manifest.go`
- Create: `internal/module/skill/mirror_hash.go`
- Test: `internal/module/skill/mirror_manifest_test.go`
- Test: `internal/module/skill/mirror_hash_test.go`

- [ ] **Step 1: Write manifest round-trip tests**

Test JSON uses the exact file name `.super-dolphin-skill-mirror.json` and records provider, scope, `personal_type`, canonical hash, mirror hash, and source type. Round-trip tests must prove active personal entries `personal/user`, `personal/agent`, and `personal/imported` retain their exact type without inferring it from `canonical_id`; `personal/hub` must be rejected for runtime mirror manifests.

- [ ] **Step 2: Implement manifest schema**

Use this struct shape:

```go
type SkillMirrorManifest struct {
	Version         int                         `json:"version"`
	Manager         string                      `json:"manager"`
	Scope           string                      `json:"scope"`
	Provider        string                      `json:"provider"`
	CanonicalRootID string                    `json:"canonical_root_id"`
	GeneratedAt     time.Time                   `json:"generated_at"`
	Skills          map[string]SkillMirrorEntry `json:"skills"`
}

type SkillMirrorEntry struct {
	CanonicalID   string `json:"canonical_id"`
	CanonicalHash string `json:"canonical_hash"`
	MirrorHash    string `json:"mirror_hash"`
	SourceType    string `json:"source_type"`
	PersonalType  string `json:"personal_type,omitempty"`
	Owned         bool   `json:"owned"`
}
```

For project mirrors, `canonical_root_id` may be a repo fingerprint or another non-secret project identifier; if implementation keeps an absolute repo path for diagnostics, that field is allowed only in project mirror manifests and must never be emitted for personal mirrors. For personal mirrors, `canonical_root_id` is the derived `owner_key`, and `canonical_id` is home-relative such as `personal/user/build` or `personal/agent/build`; manifest tests must assert personal mirror manifests contain no raw home path, OS uid, username, profile path, or absolute provider mirror path.

- [ ] **Step 3: Implement stable directory hash**

Hash every regular file under a skill directory except the mirror manifest. Include relative path, mode bits, and file bytes in sorted order. Reject paths that escape the source root.

- [ ] **Step 4: Run focused tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'TestSkillMirrorManifest|TestMirrorHash' -count=1
```

Expected: manifest and hash tests pass.

## Task 4: Mirror Publisher

**Files:**
- Create: `internal/module/skill/mirror_publisher.go`
- Test: `internal/module/skill/mirror_publisher_test.go`

- [ ] **Step 1: Write publisher tests**

Cover these cases:

- publish project canonical to `<repo>/.claude/skills/<name>` and `<repo>/.agents/skills/<name>`
- publish personal canonical to `~/.claude/skills/<name>` and `~/.agents/skills/<name>` by default
- mirror includes `SKILL.md`, `references/`, `templates/`, `scripts/`
- unmanaged same-name mirror is not overwritten
- canonical deletion deletes owned, non-drifted mirror
- mirror deletion is regenerated when canonical still exists
- `.agents/skills/<name>/SKILL.md` is ignored by git
- existing legacy `.claude/skills` symlink to `~/.super-dolphin/skills-cache` is detected before writing and fails closed unless explicitly replaced by the V1 cutover path
- non-owned provider mirror roots, unexpected symlinks, and symlinked final skill directories are not followed or overwritten
- symlink entries inside canonical skill directories are rejected or copied as inert metadata, never followed
- a malicious relative path cannot make mirror copy write outside the mirror root
- executable mode is preserved only for regular files under `scripts/**`; non-script support files are normalized to non-executable mode

- [ ] **Step 2: Implement publisher target model**

Use explicit provider targets:

```go
type SkillProvider string

const (
	SkillProviderClaude SkillProvider = "claude"
	SkillProviderCodex  SkillProvider = "codex"
)

type SkillMirrorTarget struct {
	TargetID        string
	Provider        SkillProvider
	Scope           string
	Root            string
	CanonicalRootID string
}
```

`TargetID` is the stable logical provider target id used by publisher reports, audit records, V3 dry-run/snapshot records, and rollback manifests, for example `claude:project:<repo_fingerprint>` or `claude:user-global:<owner_key>`. Runtime resolves `TargetID` to a physical root from trusted project/provider configuration; persisted reports must not rely on absolute provider mirror paths as identity.

- [ ] **Step 3: Implement safe copy**

Publisher validates the mirror root and final target path with `lstat` before any temp write. It must fail closed for non-owned roots, unexpected symlinks, and legacy `.claude/skills -> ~/.super-dolphin/skills-cache` style links; it must not follow the link and must not write generated mirrors into the old runtime cache. Publisher writes to a temp directory next to the final skill directory, then renames into place. It only overwrites directories that are present in the manifest and not drifted. Safe copy must walk with `lstat`, reject path escape, reject symlink traversal, and apply a documented mode policy: regular files under `scripts/**` may preserve executable bits, while `SKILL.md`, `references/**`, `templates/**`, and other support files are written non-executable.

- [ ] **Step 4: Return structured report**

Return a report with published, skipped, deleted, and conflicts. Every report item includes `target_id`, provider, scope, relative mirror path, canonical id, old/new hashes, and conflict kind. Provider startup in plan 02 will use this report to surface UI warnings without guessing, and V3 will persist only `target_id` plus relative mirror references for audit/snapshot records.

- [ ] **Step 5: Add `.agents/` gitignore coverage**

Add `/.agents/` to `.gitignore` in this plan because foundation can already generate `<repo>/.agents/skills`. Add verification:

```bash
git check-ignore -q .agents/skills/probe/SKILL.md
```

Expected: generated Codex mirror content is ignored before provider cutover begins.

- [ ] **Step 6: Run focused tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'TestSkillMirrorPublisher' -count=1
```

Expected: publisher tests pass.

## Task 5: Drift, Conflict, And Import/Takeover Primitives

**Files:**
- Create: `internal/module/skill/mirror_reconciler.go`
- Test: `internal/module/skill/mirror_reconciler_test.go`
- Modify: `internal/module/skill/events.go`
- Modify: `internal/dto/ui/event.go`

- [ ] **Step 1: Write drift tests**

Cover project drift actions:

- `sync_back_to_canonical`
- `canonical_overwrite_mirror`
- `save_as_new_skill`

Cover personal drift actions:

- `sync_back_to_personal`
- `personal_overwrite_mirror`
- `save_as_new_personal_skill`

Cover unmanaged provider-native same-name actions as backend primitives:

- `view_unmanaged`
- `import_to_personal_imported`
- `import_to_project`
- `takeover_provider_skill`

- [ ] **Step 2: Implement conflict result DTOs**

Use machine-readable kinds: `same_name`, `mirror_drift`, `unmanaged_same_name`, `canonical_deleted_with_drift`, `multi_mirror_drift`.

- [ ] **Step 3: Implement resolution methods**

Resolution methods must use the cross-plan D10 order: preview hash -> backup -> audit intent -> mutate -> publish mirrors when needed -> audit finalize. If backup creation or audit intent fails, return an error and leave canonical/mirror files unchanged. If publish or audit finalize fails after mutation, return a structured partial-failure report with the resulting hash and required follow-up action. Read-only detection can still work when the audit store is unavailable, but every mutating resolution/delete/archive path requires a fail-closed audit writer; nil audit writer or audit insert failure is a test-covered error before mutation.

Takeover must show and validate preview hash before writing ownership metadata. The write order is backup unmanaged provider directory, verify backup hash, write audit intent, write manifest ownership, publish, then write audit finalize. Import to project or personal/imported copies into canonical and never overwrites an existing canonical skill without an explicit same-name resolution. Import and takeover must reject path traversal and symlinked external/provider directories after canonicalization.

- [ ] **Step 4: Publish events after successful writes**

Add `PersonalType string json:"personal_type,omitempty"` to `internal/dto/ui/event.go` `SkillsChanged`. Emit skills-changed with `scope`, `personal_type`, repo fingerprint, and relative path so UI can refresh the correct bucket without leaking absolute cwd. Do not emit events when detection found conflicts but made no changes.

Update `skillsChangedMergeable` so events only coalesce when `(scope, personal_type, repo_fingerprint, relative_path)` all match. Add tests proving `personal/user` and `personal/agent` changes with the same name flush as separate events.

- [ ] **Step 5: Run focused tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'TestSkillMirrorReconciler|Test.*Drift|Test.*Conflict' -count=1
```

Expected: drift and conflict tests pass.

## Task 6: Write-Time Publish Hooks

**Files:**
- Modify: `internal/module/skill/skills_fs.go`
- Modify: `internal/module/skill/skills_import.go`
- Test: `internal/module/skill/skills_fs_test.go`
- Test: `internal/module/skill/skills_import_test.go`

- [ ] **Step 1: Write service-level publish tests**

Cover:

- `WriteLocal` publishes owned mirrors after project canonical write when provider targets are configured
- `ImportLocalDir` publishes owned mirrors after successful import
- `DeleteLocal` removes owned, non-drifted mirrors after canonical delete/archive
- write-time publish conflict leaves canonical write result visible and returns a report with unresolved conflicts
- personal canonical writes publish to provider-native user-global roots using temp HOME in tests, and never touch the real user's home during test runs

- [ ] **Step 2: Implement write-time publish integration**

Wire write/import/delete success paths to `SkillMirrorPublisher`. Provider startup reconcile remains in `02-v1-provider-cutover.md`; this step only ensures Super-Dolphin writes produce fresh mirrors when safe.

Project mirrors are derivable from `cwd` and can be published by foundation. Personal mirrors are derivable from user-global provider-native roots by default: `~/.claude/skills` for Claude and `~/.agents/skills` for Codex. Explicit provider homes remain allowed only when injected/configured. Tests must set temp `HOME` before write-time personal publish, so unit tests never write the real user's provider directories.

- [ ] **Step 3: Run write-time tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'Test.*Write.*Publish|Test.*Import.*Publish|Test.*Delete.*Mirror' -count=1
```

Expected: write-time publish runs for owned mirrors and never overwrites unmanaged provider-native directories.

## Task 7: Contract And Backend Surface

**Files:**
- Modify: `internal/module/skill/contract.go`
- Modify: `internal/module/skill/module.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/module/skill/rpc_types_test.go`
- Modify: `internal/contract/skill.go`
- Modify: `internal/dto/ui/event.go`

- [ ] **Step 1: Add narrow ports**

Add contract ports for provider cutover without importing the skill module into providers:

```go
type SkillProviderMirrorTarget struct {
	Provider   string
	HomeRoot   string
	SkillsRoot string
}

type SkillMirrorReconciler interface {
	ReconcileProviderMirrors(ctx context.Context, cwd string, targets []SkillProviderMirrorTarget) (SkillMirrorReport, error)
}
```

`HomeRoot` is the provider-native parent root used for a mirror target. By default this is `~/.claude` for Claude personal skills and `~/.agents` for Codex personal skills; project targets use the project root; explicit provider homes may use their own `skills` subdirectory. `SkillsRoot` is the exact provider-native skills directory under that root or project.

The provider-facing target intentionally does not accept a caller-supplied `TargetID`. The skill module derives the stable `SkillMirrorTarget.TargetID` server-side from trusted context: project targets use provider plus repo fingerprint, default personal targets use `user-global` plus derived `owner_key`, and explicit provider homes use `explicit-home`, for example `claude:project:<repo_fingerprint>` or `claude:user-global:<owner_key>`. Tests must prove malicious or stale provider-start payloads cannot spoof target identity and that every publisher report item includes the derived `target_id`.

Add a fail-closed mutation audit port for backend skill writes and resolution applies. This must not reuse best-effort candidate audit behavior. Tests must inject nil audit and insert failure and prove canonical, mirror, archive, and manifest paths remain unchanged.

The same fail-closed audit and recovery contract applies to normal personal `WriteLocal`, `WriteSkillContent`, `WriteSummary`, and import paths, not only conflict-resolution RPCs. Tests must prove personal edit/import failures before audit intent leave canonical bytes unchanged and leave enough rollback metadata for failures after mutation.

Keep DTOs in `internal/contract` if providers need them; otherwise keep detailed conflict DTOs inside `internal/module/skill`. User-facing RPC wrapping and UI workflow belong to `02b-v1-resolution-ui-rpc.md`; this plan exposes backend primitives, typed report DTOs, and provider-facing ports only.

Wire the implementation through Fx in `internal/module/skill/module.go`, for example with `fx.As(new(contract.SkillMirrorReconciler))` or an equivalent provider, so `internal/provider/claudecli` and `internal/provider/codexapp` can inject the port without importing `internal/module/skill`. The audit writer must be a required dependency for mutating paths, not an optional best-effort field. Add Fx graph tests that fail before the mirror reconciler and fail-closed audit writer are exposed.

- [ ] **Step 2: Add backend DTOs and service methods**

Add service-level detection and resolution methods that return machine-readable kinds, hashes, target paths, backup paths, and proposed actions. Do not add UI workflow handlers here beyond existing compatibility wrappers needed to keep current RPC tests compiling.

- [ ] **Step 3: Run package tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/contract -count=1
```

Expected: skill and contract tests pass.

Add a V1 data-scope gate:

```bash
rg -n 'scope.*system|system.*scope|skill_candidates|skills/candidate' migrations sql internal/store internal/module/skill internal/module/turn
```

Expected: if any persisted DB metadata outside the old candidate pipeline can still store live `scope=system`, plan 01 adds the migration/schema/sqlc change. Old `skill_candidates` rows and handlers are not migrated here if plan 02 removes or disables that production pipeline before V1 acceptance; that dependency must stay explicit in the V1 atomic gate.

## Task 8: Plan-Level Verification

- [ ] **Step 1: Run affected package tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/contract -count=1
```

- [ ] **Step 2: Run guard**

Run:

```bash
make guard
```

- [ ] **Step 3: Check formatting and unintended generated drift**

Run:

```bash
owned_files=$(printf '%s\n' \
	  internal/module/skill/scope_model.go \
	  internal/module/skill/canonical_store.go \
  internal/module/skill/mirror_manifest.go \
  internal/module/skill/mirror_hash.go \
  internal/module/skill/mirror_publisher.go \
  internal/module/skill/mirror_reconciler.go \
	  internal/module/skill/service.go \
	  internal/module/skill/module.go \
	  internal/module/skill/contract.go \
  internal/module/skill/skills_meta.go \
  internal/module/skill/skills_fs.go \
  internal/module/skill/skills_import.go \
	  internal/module/skill/skills_match.go \
	  internal/module/skill/events.go \
	  internal/module/skill/rpc.go \
		  internal/module/skill/rpc_skill_types.go \
		  internal/module/turn/skills.go \
		  internal/contract/skill.go \
  internal/dto/ui/event.go)
existing_owned_files=$(printf '%s\n' "$owned_files" | while read -r f; do test -f "$f" && printf '%s\n' "$f"; done)
test -z "$existing_owned_files" || gofmt -w $existing_owned_files
git diff --check
git status --short
```

Expected: only owned files changed; no unrelated generated drift.

If the Task 1 data-scope gate introduces or changes migrations, SQL queries, store wiring, or generated sqlc files, this plan-level verification must also run `make sqlc-verify` and report any generated diff instead of relying on module tests alone.

## Accepted Defaults And Gates For This Plan

- D2 is fixed: no live `system` alias; V1 updates callers/tests, migrates persisted old metadata and old `~/.super-dolphin/skills` content to `personal/user`, and stops runtime scanning the old root.
- `personal/user` is the wire enum for human-authored personal skills; do not add a second `human` enum or directory in V1-V3.
- `personal/hub` is catalog-only. It is not an active canonical root, not a valid write/import/delete target, and must not be published into provider-native mirrors.
- Old global root configuration is fixed: `SKILLS_ROOT`, `defaultSkillsRoot()`, `systemGlobalSkillsRoot()`, and service `s.root` are not accepted as runtime skill roots after V1. They may remain only as explicit migration/import inputs with test coverage.
- Same-name conflicts are fixed across both project/personal and personal/personal type pairs; foundation must never shadow by scan order, trust, type priority, or lowercase map overwrite.
- Manifest location is fixed: mirror root contains `.super-dolphin-skill-mirror.json`, and each entry records exact `personal_type` where applicable instead of deriving it from path strings.
- D9 is fixed: V1 provides low-level personal archive primitive; V3 provides pin/archive/restore user workflows.
- Audit behavior is fixed: mutating conflict resolution fails closed if audit cannot be written.
- Write-time publish is enabled in foundation only for targets that are explicitly derivable or injected. Personal provider mirrors use provider-native user-global roots by default and must be isolated with temp `HOME` in tests.
- Publisher safety is fixed: legacy `.claude/skills` symlinks and other unexpected provider-root symlinks are detected before write and fail closed instead of redirecting mirrors into old runtime caches.
