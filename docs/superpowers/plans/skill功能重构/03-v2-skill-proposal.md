# V2 Skill Proposal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Super-Dolphin suggest skill changes from observed work, but require user confirmation before any canonical write.

**Architecture:** A recorder captures bounded evidence after a session. A background proposal generator receives packed evidence and relevant canonical skill context, returns structured JSON, and a validator converts only safe proposals into reviewable diffs. Apply writes canonical through the V1 store, creates backup/audit records, then publishes provider mirrors.

**Tech Stack:** Go 1.25.7, sqlc/store for durable proposal records, existing auditlog store, internal model adapter through narrow contract, no MCP tools.

---

## File Structure

- Create: `internal/module/skillproposal/schema.go`
- Create: `internal/module/skillproposal/schema_test.go`
- Create: `internal/module/skillproposal/recorder.go`
- Create: `internal/module/skillproposal/recorder_test.go`
- Create: `internal/module/skillproposal/evidence_packer.go`
- Create: `internal/module/skillproposal/evidence_packer_test.go`
- Create: `internal/module/skillproposal/evidence_policy.go`
- Create: `internal/module/skillproposal/evidence_policy_test.go`
- Create: `internal/module/skillproposal/evidence_retention.go`
- Create: `internal/module/skillproposal/evidence_retention_test.go`
- Create: `internal/module/skillproposal/config.go`
- Create: `internal/module/skillproposal/config_test.go`
- Create: `internal/module/skillproposal/generator.go`
- Create: `internal/module/skillproposal/generator_test.go`
- Create: `internal/module/skillproposal/validator.go`
- Create: `internal/module/skillproposal/validator_test.go`
- Create: `internal/module/skillproposal/apply.go`
- Create: `internal/module/skillproposal/apply_test.go`
- Create: `internal/module/skillproposal/recovery_journal.go`
- Create: `internal/module/skillproposal/recovery_journal_test.go`
- Create: `internal/module/skillproposal/module.go`
- Create: `internal/store/skillproposal/contract.go`
- Create: `internal/store/skillproposal/store.go`
- Create: `internal/store/skillproposal/store_test.go`
- Create: `internal/store/skillproposal/module.go`
- Create: `sql/queries/skill_proposal.sql`
- Create: `migrations/0093_skill_proposals.sql`
- Modify: `sqlc.yaml`
- Modify: `internal/store/module.go`
- Modify: `internal/app/modules.go`
- Modify: `internal/module/skill/contract.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/module/skill/rpc_types_test.go`
- Create: `internal/module/skill/rpc_skill_proposal_test.go`
- Modify: `internal/module/turn/skill_extractor.go`
- Modify: `internal/module/turn/feedback_proposer.go`
- Modify: `internal/module/turn/trajectory_collector.go`
- Modify: `internal/module/memory/kairos.go`
- Modify: `cmd/agent-terminal/frontend/vue-app/services/skills-api.js`
- Modify: frontend pending/candidate review files after locating them with `rg`
- Modify or archive: `internal/store/skillcandidate/**`
- Modify or archive: `internal/module/skill/*candidate*`

V2 must implement reviewable skill-change behavior on `skill_proposals`. V1 provider cutover retires old `skills/candidate/*` production entrypoints so they are not a live old skill pipeline; V2 replaces that removed behavior with `skill_proposals/*`. If `internal/store/skillcandidate` still exists for migrations/tests/history, keep it compile-proven unreachable from production; do not bridge new proposal diffs through the old full-SKILL candidate schema.

## Task 1: Proposal Schema

**Files:**
- Create: `internal/module/skillproposal/schema.go`
- Test: `internal/module/skillproposal/schema_test.go`

- [ ] **Step 1: Write schema tests**

Cover valid and invalid proposals:

```go
func TestProposalSchemaRejectsProviderMirrorTarget(t *testing.T) {
	p := Proposal{Scope: "personal", PersonalType: "agent", Action: "patch_skill", Target: ".agents/skills/x"}
	if err := p.ValidateShape(); err == nil {
		t.Fatal("expected provider mirror target rejection")
	}
}

func TestProposalSchemaProjectRequiresConfirmation(t *testing.T) {
	p := validPatchProposal()
	p.Scope = "project"
	p.Safety.RequiresUserConfirmation = false
	if err := p.ValidateShape(); err == nil {
		t.Fatal("project proposal without confirmation must fail")
	}
}
```

- [ ] **Step 2: Implement schema**

Use action constants:

```go
const (
	ActionCreateSkill      = "create_skill"
	ActionPatchSkill       = "patch_skill"
	ActionWriteSupportFile = "write_support_file"
	ActionArchiveSkill     = "archive_skill"
	ActionRenameSkill      = "rename_skill"
)
```

Disallow direct provider mirror paths, unmanaged external deletes, and any canonical mutating action without explicit user confirmation in V2. This includes personal-scope proposals: `requires_user_confirmation=false` in model output is ignored/rejected by V2 schema validation. V3 `ApplyModePolicyAuto` may auto-apply only after the independent V3 policy authorizes it; it must not trust proposal JSON to bypass review.

Add a single `ProposalOperation` JSON contract used by generator output, validator, diff preview, apply, and UI. Do not let each layer invent action-specific fields independently:

```json
{
  "action": "patch_skill",
  "target": {
    "scope": "personal",
    "personal_type": "agent",
    "name": "skill-name",
    "canonical_id": "personal/agent/skill-name"
  },
  "ops": [
    {
      "rel_path": "SKILL.md",
      "old_text": "exact text",
      "new_text": "replacement",
      "old_file_hash": "sha256:...",
      "expected_canonical_hash": "sha256:...",
      "mode": "0644"
    }
  ],
  "new_name": "",
  "reason": "..."
}
```

Action-specific required fields:

- `create_skill`: `target` with scope, personal type, name, and no existing canonical directory, plus `files[]` containing at least `SKILL.md`; every file has `rel_path`, `content`, `mode`, and optional `file_hash`. Validator rejects an already-existing target, path traversal, symlinks, provider mirror roots, executable non-script support files, and `scripts/**` executable writes unless a separate manually approved high-risk path is added later.
- `patch_skill`: `target`, `ops[]`, `rel_path`, exact `old_text`, `new_text`, `old_file_hash`, and `expected_canonical_hash`. Apply rejects if exact old text or hashes no longer match.
- `write_support_file`: `target`, `rel_path` under `references/**`, `templates/**`, or another non-`SKILL.md` support path, `content`, `old_file_hash` when overwriting, `expected_canonical_hash`, and non-executable `mode`. V3 policy may auto-apply only the low-risk subset; V2 still requires user confirmation.
- `rename_skill`: `target`, `new_name`, and `expected_canonical_hash`; validator rejects an existing destination, case-fold collision, path traversal, and provider mirror target.
- `archive_skill`: `target`, `expected_canonical_hash`, and `archive_reason`. V2 may store and review it, but V2 apply returns `archive_apply_requires_v3`; V3 wires the actual archive operation.

Golden tests must include one valid and at least one invalid fixture per action. Invalid fixtures cover missing old text/hash, missing `new_name`, executable support file, provider mirror path, path traversal, stale canonical hash, duplicate file op order, and an `archive_skill` apply attempt before V3.

- [ ] **Step 3: Run schema tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal -run 'TestProposalSchema' -count=1
```

Expected: schema tests pass.

## Task 2: Evidence Privacy Policy

**Files:**
- Create: `internal/module/skillproposal/evidence_policy.go`
- Test: `internal/module/skillproposal/evidence_policy_test.go`

- [ ] **Step 1: Write redaction tests**

Tests must prove:

- raw transcript text is not persisted
- raw git diff is not persisted
- API-key-like values are redacted
- environment variable secrets are redacted
- private file paths are either removed or reduced to workspace-relative summaries
- evidence exceeding byte/token budget is truncated with `truncated=true`
- provider mirror content is included only as drift evidence, never as a canonical source

- [ ] **Step 2: Implement evidence policy**

Define a policy that stores summaries and digests, not raw conversation or raw diffs. Add these metadata fields to persisted evidence:

```json
{
  "evidence_redaction_version": 1,
  "evidence_digest": "sha256:...",
  "truncated": false
}
```

Set hard budgets with explicit constants:

```go
const (
	MaxTranscriptSummaryBytes     = 12 * 1024
	MaxEventSummaryBytes          = 8 * 1024
	MaxChangedFileSummaryBytes    = 12 * 1024
	MaxCanonicalSkillExcerptBytes = 16 * 1024
	MaxFullCanonicalSkills        = 3
)
```

`evidence_digest` is computed over the redacted, truncated, persisted evidence JSON after deterministic canonical JSON encoding. It must never be computed over raw transcript text, raw git diff, raw absolute private paths, or raw provider mirror content. Full skill text is allowed only for the bounded `MaxFullCanonicalSkills` set of relevant canonical skills.

- [ ] **Step 3: Add user opt-out and retention hooks**

Expose config flags for proposal evidence capture and proposal generation. When evidence capture is disabled, recorder stores no evidence and generator does not run. When proposal generation is explicitly disabled but evidence capture remains enabled, recorder may store bounded redacted evidence with `status=generation_disabled`.

Implement retention for redacted evidence in V2. Add a default retention window for consumed, discarded, generation-disabled, and generation-failed evidence rows, plus an owner/admin-triggered purge job that deletes expired `skill_proposal_evidence` rows and writes a low-sensitive audit/metric count. Pending `captured` or leased evidence is not purged until it reaches a terminal state. `skill_proposals` must not copy the full persisted evidence pack into its own row; it keeps only a logical `evidence_id`, `evidence_digest`, redaction version, proposal idempotency key, and a compact non-sensitive evidence summary suitable for long-term review/audit. Tests must prove expired terminal evidence is purged or compacted, non-expired evidence remains, proposal rows do not retain full evidence JSON after terminal compaction, and raw private data is never added to purge logs.

- [ ] **Step 4: Run policy tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal -run 'Test.*Evidence.*Policy|Test.*Redact|Test.*Budget' -count=1
```

Expected: persisted evidence is bounded and redacted.

## Task 3: Durable Proposal Store

**Files:**
- Create: `migrations/0093_skill_proposals.sql`
- Create: `sql/queries/skill_proposal.sql`
- Create: `internal/store/skillproposal/contract.go`
- Create: `internal/store/skillproposal/store.go`
- Test: `internal/store/skillproposal/store_test.go`
- Modify: `sqlc.yaml`
- Modify: `internal/store/module.go`

- [ ] **Step 1: Add migration**

Create table `skill_proposals` with:

- `id BIGSERIAL PRIMARY KEY`
- `scope TEXT NOT NULL`
- `personal_type TEXT NOT NULL DEFAULT ''`
- `repo_fingerprint TEXT NOT NULL DEFAULT ''`
- `owner_key TEXT NOT NULL DEFAULT ''`
- `action TEXT NOT NULL`
- `target TEXT NOT NULL`
- `status TEXT NOT NULL`
- `apply_phase TEXT NOT NULL DEFAULT ''`
- `last_error_kind TEXT NOT NULL DEFAULT ''`
- `last_error TEXT NOT NULL DEFAULT ''`
- `backup_path TEXT NOT NULL DEFAULT ''`
- `pre_apply_hash TEXT NOT NULL DEFAULT ''`
- `post_apply_hash TEXT NOT NULL DEFAULT ''`
- `reason TEXT NOT NULL DEFAULT ''`
- `proposal_json JSONB NOT NULL`
- `proposal_safety_digest TEXT NOT NULL DEFAULT ''`
- `proposal_redaction_version INTEGER NOT NULL DEFAULT 1`
- `evidence_id BIGINT NOT NULL`
- `idempotency_key TEXT NOT NULL`
- `evidence_summary_json JSONB NOT NULL DEFAULT '{}'`
- `evidence_redaction_version INTEGER NOT NULL DEFAULT 1`
- `evidence_digest TEXT NOT NULL`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `decided_at TIMESTAMPTZ`
- `decided_by TEXT NOT NULL DEFAULT ''`

Allowed status values: `pending_review`, `approved`, `rejected`, `applying`, `apply_partial_failure`, `applied`. Allowed `apply_phase` values include `audit_intent_written`, `canonical_mutated`, `mirrors_published`, `audit_finalized`. Add unique constraints on `evidence_id` and `idempotency_key`. `evidence_id` is a logical reference used for idempotency and review lookup; do not add a hard foreign key that prevents retention purge of terminal `skill_proposal_evidence` rows.

Create table `skill_proposal_evidence` as the durable recorder outbox for evidence that has not produced a proposal yet:

- `id BIGSERIAL PRIMARY KEY`
- `scope TEXT NOT NULL`
- `personal_type TEXT NOT NULL DEFAULT ''`
- `repo_fingerprint TEXT NOT NULL DEFAULT ''`
- `owner_key TEXT NOT NULL DEFAULT ''`
- `trigger_kind TEXT NOT NULL`
- `status TEXT NOT NULL`
- `evidence_json JSONB NOT NULL`
- `evidence_redaction_version INTEGER NOT NULL DEFAULT 1`
- `evidence_digest TEXT NOT NULL`
- `proposal_id BIGINT REFERENCES skill_proposals(id)`
- `last_error_kind TEXT NOT NULL DEFAULT ''`
- `lease_owner TEXT NOT NULL DEFAULT ''`
- `lease_expires_at TIMESTAMPTZ`
- `attempt_count INTEGER NOT NULL DEFAULT 0`
- `idempotency_key TEXT NOT NULL`
- `generation_result_json JSONB`
- `generation_result_digest TEXT NOT NULL DEFAULT ''`
- `generation_result_redaction_version INTEGER NOT NULL DEFAULT 0`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `consumed_at TIMESTAMPTZ`

Allowed evidence status values: `captured`, `generating`, `proposal_created`, `generation_disabled`, `generation_failed`, `discarded`. `idempotency_key` is deterministic over owner/repo identity, trigger kind, evidence digest, and proposal target hints, and has a unique constraint so one evidence pack cannot create duplicate proposals. When proposal generation is explicitly disabled or the auxiliary proposal model is unset, recorder inserts `status=generation_disabled` evidence rows and does not insert a `skill_proposals` row. That is a negative-path test only; V2 release acceptance requires a configured model and a generated pending proposal. If evidence capture is disabled, neither table is written.

`generation_result_json` is a bounded, sanitized checkpoint of the validated model output for the claimed evidence row. It is written only after JSON shape validation, redaction, residual-secret scan, path scan, and size cap pass. It is not a raw prompt/response log. If a worker crashes after storing this checkpoint but before `skill_proposals` insert/consume commits, the next worker reuses the checkpoint and does not call the model again. If the process dies before any checkpoint is durable, retry may call the model again; this plan does not claim pre-checkpoint model-call exactly-once semantics unless the chosen model provider exposes a request idempotency key and tests cover it. Unique `evidence_id` and `idempotency_key` still prevent duplicate proposals.

Create table `skill_proposal_apply_recovery` for the narrow case where filesystem mutation succeeds but proposal status persistence fails:

- `id BIGSERIAL PRIMARY KEY`
- `proposal_id BIGINT NOT NULL`
- `scope TEXT NOT NULL`
- `repo_fingerprint TEXT NOT NULL DEFAULT ''`
- `owner_key TEXT NOT NULL DEFAULT ''`
- `target TEXT NOT NULL`
- `backup_path TEXT NOT NULL`
- `pre_apply_hash TEXT NOT NULL`
- `observed_post_apply_hash TEXT NOT NULL`
- `failed_phase TEXT NOT NULL`
- `last_error_kind TEXT NOT NULL`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `resolved_at TIMESTAMPTZ`

Recovery rows are append-only until resolved. Startup and proposal RPC reads must surface unresolved recovery rows before allowing retry/apply.

Add an owner-only local append journal for the same recovery events, independent of the SQL store connection and transaction, for example:

```text
~/.super-dolphin/skills/.proposal-recovery/<proposal_id>-<timestamp>.jsonl
```

The local journal is the first durable recovery sink around canonical mutation when proposal-row persistence fails. Before mutation, apply must create and fsync a recovery-intent record with proposal id, scope, repo fingerprint or owner key, logical canonical id such as `project/<name>` or `personal/agent/<name>`, home-relative backup id/path, pre-apply hash, and `phase=pending_mutation`. It must not persist raw resolved home, OS uid, profile path, absolute target path, or absolute backup path. If this journal sink cannot be created, apply fails before canonical mutation. After mutation, apply updates or appends the observed post-apply hash and failed phase. If the post-mutation journal update fails, the pre-mutation intent remains durable; startup recovery resolves the logical canonical id through the current project root or `resolvedSuperDolphinHome()`, recomputes the current target hash, compares it with backup/pre-hash, and surfaces a manual recovery state instead of losing the mutation. SQL `skill_proposal_apply_recovery` remains useful for normal UI queries, but it is not the only recovery record. If the database connection, transaction, proposal update, and SQL recovery insert are all unavailable after filesystem mutation, apply still has the local journal and returns `apply_partial_failure`.

Project-scope proposals require a valid `repo_fingerprint`. Personal-scope proposals require the `owner_key` derived by the V1 owner identity helper. Database rows, RPC payloads, logs, journals, and audit extras store only the derived `owner_key`; they must not store the raw home path, OS uid, username, profile path, absolute canonical path, or absolute backup path. Add CHECK constraints and indexes for `(scope, repo_fingerprint, status)` and `(scope, owner_key, status)`.

- [ ] **Step 2: Add sqlc queries**

Queries and recovery readers needed:

- `InsertSkillProposal`
- `InsertSkillProposalFromClaimedEvidence`
- `InsertSkillProposalEvidence`
- `ClaimSkillProposalEvidenceForGeneration`
- `ReleaseSkillProposalEvidenceLease`
- `MarkSkillProposalEvidenceConsumed`
- `MarkSkillProposalEvidenceFailed`
- `StoreSkillProposalGenerationResult`
- `GetSkillProposalGenerationResultForEvidence`
- `PurgeExpiredSkillProposalEvidence`
- `ListSkillProposalEvidence`
- `InsertSkillProposalApplyRecovery`
- `ListLocalSkillProposalApplyRecovery` or equivalent module-level journal reader outside sqlc
- `ListUnresolvedSkillProposalApplyRecovery`
- `ResolveSkillProposalApplyRecovery`
- `GetSkillProposalByID`
- `ListPendingSkillProposals`
- `ApproveSkillProposal`
- `RejectSkillProposal`
- `MarkSkillProposalApplying`
- `MarkSkillProposalPartialFailure`
- `MarkSkillProposalApplied`
- `CompactTerminalSkillProposalEvidence`

All get/list/approve/reject/apply queries must include caller identity: project calls pass repo fingerprint, personal calls pass owner key. Mismatch returns not found or state mismatch and does not reveal another project's proposal.

Evidence generation workers must claim rows with a short lease before calling the model. Use transactional row claiming such as `FOR UPDATE SKIP LOCKED` where available, or an equivalent compare-and-swap on `status`, `lease_owner`, `lease_expires_at`, and `attempt_count`. A crashed worker leaves the row claimable after lease expiry. A second worker must not call the model for the same unexpired lease. Before calling the model, the worker checks for a durable `generation_result_json` checkpoint on the evidence row and reuses it if present. After a successful model response, the worker validates, sanitizes, caps, and stores the checkpoint before attempting proposal insert. Tests must cover crashes before model call, after model call before checkpoint, after checkpoint before proposal insert, expired lease retry, and concurrent workers; the strict guarantee is no duplicate proposals and no duplicate model call after a generation-result checkpoint exists.

Proposal insert and evidence consumption must be one transactional operation: insert `skill_proposals` with the claimed evidence row's `id` and deterministic `idempotency_key`, then mark the evidence row `proposal_created` with `proposal_id` before commit. If the process crashes after insert but before commit, neither change is visible. If retry races with an already committed insert, the unique `evidence_id`/`idempotency_key` constraint returns the existing proposal rather than inserting a duplicate. Tests must cover insert-success/mark-consumed failure, duplicate retry, expired lease retry, and concurrent workers.

- [ ] **Step 3: Implement store facade**

Follow the narrow-querier test pattern used by `internal/store/skillcandidate/store.go`. Tests must cover fingerprint mismatch and owner-key mismatch. Add owner-key tests proving the same normalized home/uid/profile produces the same key, different home or uid produces a different key, and no raw home path appears in stored proposal rows or audit fields.

Add journal tests proving the local recovery writer uses owner-only directory/file permissions, appends deterministic JSON lines, can be read on startup without a live DB connection, and rejects path traversal or symlinked recovery directories.

- [ ] **Step 4: Verify sqlc**

Run:

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/store/skillproposal -count=1
```

Expected: generated sqlc output is synced, `internal/store/sqlc/*skill_proposal*.go` exists or equivalent sqlc generated methods are updated, and store tests pass.

## Task 4: Evidence Recorder And Packer

**Files:**
- Create: `internal/module/skillproposal/recorder.go`
- Create: `internal/module/skillproposal/evidence_packer.go`
- Test: `internal/module/skillproposal/recorder_test.go`
- Test: `internal/module/skillproposal/evidence_packer_test.go`

- [ ] **Step 1: Write trigger tests**

Recorder should trigger review for:

- user says `记住`
- user says `以后这样`
- user says `写成 skill`
- repeated failure then success
- user correction event
- skill canonical/mirror operation
- provider mirror drift resolution

Recorder should not trigger for a simple single-turn chat with no durable signal.

- [ ] **Step 2: Wire real evidence sources and implement bounded, redacted evidence**

Before packing, wire the recorder to real sources rather than synthetic test-only structs:

- user text and assistant final decisions from the turn/thread store or event stream
- user correction events where available
- validation command summary from tool/timeline events
- changed file summary from tracked file-diff events or a deterministic workspace diff summarizer
- existing trajectory tool args/results only after Task 2 redaction policy is applied

If a source is not available in the current codebase, add the narrow event/store field needed in the same task and a test proving the recorder receives it. Do not infer user intent only from raw tool args/results.

Evidence pack includes:

- compressed transcript summary
- user corrections
- final decisions
- error/retry events
- validation command summary
- changed file summary
- relevant canonical skill summaries
- at most a small number of full relevant `SKILL.md` files

Never include provider mirror as source text except as drift evidence. Never persist raw transcript or raw diff; store summaries, selected event facts, hashes, and redacted snippets that pass Task 2 policy.

The recorder persists every triggered, redacted evidence pack to `skill_proposal_evidence` before any model call. Tests must cover these cases: evidence capture disabled writes nothing; evidence capture enabled with proposal generation explicitly disabled or auxiliary model unset writes `status=generation_disabled` evidence and no proposal; auxiliary model configured claims evidence through a lease, checks or stores the generation-result checkpoint, and consumes the evidence row by linking it to the created proposal in the same transaction that inserts the proposal; duplicate workers, crashed leases, and proposal-insert retry do not duplicate proposals or duplicate model calls after a checkpoint exists.

- [ ] **Step 3: Run recorder tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal -run 'Test.*Recorder|Test.*Evidence' -count=1
```

Expected: deterministic trigger and packer tests pass.

## Task 5: Background Model Generator

**Files:**
- Create: `internal/module/skillproposal/config.go`
- Create: `internal/module/skillproposal/generator.go`
- Test: `internal/module/skillproposal/config_test.go`
- Test: `internal/module/skillproposal/generator_test.go`
- Modify: `internal/contract/skill.go` or create a narrower proposal contract file if preferred by local patterns
- Modify: `internal/contract/dream.go` or add a dedicated proposal-model contract if local patterns support it
- Modify: provider/unified proposal model wiring files after locating current DreamExecutor providers with `rg "DreamExecutor|ExecuteDream|DREAM_"`

- [ ] **Step 1: Add auxiliary model config**

Add a dedicated proposal model config owned by Super-Dolphin, separate from the active conversation provider. It may wrap the existing `DreamExecutor` infrastructure only if the wrapper takes an explicit proposal-model config and tests prove it does not reuse the active user-facing provider/model implicitly. If unset, proposal generation is disabled: recorder stores redacted evidence in `skill_proposal_evidence` when evidence capture is enabled, but no model call happens and no pending proposal is inserted. This unset-model path is a supported disabled/degraded mode, not V2 release acceptance.

Tests must cover:

- unset auxiliary config does not call model
- disabled proposal generation still inserts durable `skill_proposal_evidence` when evidence capture is enabled
- active user-facing provider/model is not reused implicitly
- existing `DREAM_*_MODEL` / unified failover defaults do not silently become the proposal model unless explicitly mapped by the new Super-Dolphin proposal config
- configured proposal model generation smoke converts a real trigger evidence row into a validated pending proposal; this smoke is required before V2 can be marked complete
- claim lease expiry allows retry, while an unexpired lease prevents duplicate model calls

- [ ] **Step 2: Add narrow model port**

Define an interface that accepts evidence and returns raw JSON. Keep file writes outside the model port:

```go
type SkillProposalModel interface {
	GenerateSkillProposal(ctx context.Context, input SkillProposalPromptInput) ([]byte, error)
}
```

- [ ] **Step 3: Validate model output before storage**

Generator must parse JSON into `Proposal` and run shape validation before insert. It must also run the same residual redaction checks used for evidence on the long-lived proposal payload: API-key-like values, raw private paths, raw transcript excerpts, raw diffs, and provider mirror content are rejected or redacted before `proposal_json` or `generation_result_json` is stored. Apply a proposal payload size cap and store only bounded canonical excerpts. Invalid JSON or unsafe JSON must not insert a `skill_proposals` row and must not store a reusable generation checkpoint. It can write a low-sensitive audit/metric record with redacted reason. It must not write canonical files.

- [ ] **Step 4: Run generator tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal -run 'Test.*Generator' -count=1
```

Expected: valid proposals are pending review; invalid JSON and unsafe JSON do not create proposal rows and never write canonical or mirror files.

## Task 6: Diff Review And Apply

**Files:**
- Create: `internal/module/skillproposal/validator.go`
- Create: `internal/module/skillproposal/apply.go`
- Test: `internal/module/skillproposal/validator_test.go`
- Test: `internal/module/skillproposal/apply_test.go`
- Modify: `internal/module/skill/contract.go`
- Modify: `internal/module/skill/rpc_skill_types.go`

- [ ] **Step 1: Write validator tests**

Validator must reject:

- path traversal
- provider mirror targets
- project/repo fingerprint mismatch
- personal owner-key mismatch
- missing exact `old` text for replace operations
- any V2 canonical mutation without explicit user confirmation, including personal-scope mutations
- proposal JSON that marks canonical mutation as not requiring user confirmation
- `ApplyModePolicyAuto` unless the V3 policy module independently authorizes it
- executable script write unless action is manually approved with high-risk flag
- `archive_skill` apply in V2 with reason `archive_apply_requires_v3`

- [ ] **Step 2: Implement diff preview**

Diff preview returns a server-owned `proposal_preview_id`, `preview_hash`, expires-at timestamp, target logical canonical id, old hash, new hash, per-operation old/new file hashes, and unified diff. It does not write files. The preview hash is computed over proposal id, action, target identity, operation payload, old/new hashes, canonical hash, actor identity, and generated backup id/path; it never includes `preview_hash` itself.

- [ ] **Step 3: Implement apply**

Apply must:

1. re-read canonical
2. verify hashes still match
3. write backup
4. write audit intent event
5. mark proposal `applying` with `apply_phase=audit_intent_written`
6. mutate canonical through V1 canonical store
7. update local recovery journal with observed post hash
8. persist `post_apply_hash` and `apply_phase=canonical_mutated`
9. publish mirrors through the V1 mirror publisher/reconciler port using explicit provider targets
10. persist `apply_phase=mirrors_published`
11. write audit finalize event
12. mark proposal applied

Audit is fail-closed for V2 proposal apply. If backup or audit intent insert fails, apply returns an error, leaves canonical unchanged, does not publish mirrors, and does not mark the proposal applied. If `MarkSkillProposalApplying` fails after audit intent but before canonical mutation, apply returns an error before mutation; it does not proceed on the assumption that audit alone is enough.

Before canonical mutation, apply must reserve the local recovery journal and fail before mutation if that sink is unavailable. If canonical mutation succeeds but persisting `post_apply_hash` / `apply_phase=canonical_mutated` fails, apply must update the local recovery journal outside the proposal row and outside the failed SQL transaction/connection, then attempt the SQL recovery row if the database is available. Return `apply_partial_failure` and include proposal id, logical canonical id, home-relative backup id/path, pre-apply hash, observed post-mutation hash, journal id/path relative to the resolved Super-Dolphin home, and error kind. On restart, recovery scans local journal records first, then SQL recovery rows, resolves logical paths through the current project root or `resolvedSuperDolphinHome()`, and reconciles the proposal row before any retry or UI action. If post-mutation journal update itself fails, startup recovery must still find the pre-mutation intent and surface current target hash plus relative backup id/path as a manual recovery item. If publish or audit finalize fails after mutation, persist `status=apply_partial_failure`, `apply_phase`, `pre_apply_hash`, `post_apply_hash`, `backup_path`, and `last_error_kind` so UI and recovery can resume after process restart. The persisted `backup_path` value is a logical/home-relative backup id, not an absolute filesystem path. Do not hide it as a clean apply.

Add tests for all durable-state failure edges: audit intent failure before mutation leaves bytes and status unchanged; `MarkSkillProposalApplying` failure after audit intent leaves bytes unchanged; local recovery journal reservation failure leaves bytes unchanged; proposal phase persist failure after canonical mutation leaves a local recovery journal record with post hash; simultaneous DB proposal update failure and DB recovery insert failure still leave the local journal record; post-mutation journal update failure leaves a durable pre-mutation intent that startup recovery surfaces; restart recovery rehydrates `apply_partial_failure` from the local journal before using SQL recovery rows; mirror publish failure through the V1 publisher port remains visible to UI; publish/audit finalize failures remain visible to UI.

Do not reuse `candidate_audit.go` best-effort audit behavior. Add a fail-closed audit port or transactional wrapper that returns audit errors to the caller before mutation. If the audit store remains a separate insert, tests must inject an audit failure and prove canonical bytes and proposal status stay unchanged before the mutation phase.

`archive_skill` may exist as a pending review proposal in V2, but `approve_apply` must reject/defer execution with `archive_apply_requires_v3`. Archive execution belongs to `04-v3-personal-auto-maintenance.md`.

`approve_apply` is preview-bound. The request must include `proposal_preview_id`, `preview_hash`, reviewed target logical id, reviewed old/new hashes, confirmation copy, backup acknowledgement, actor, and reason. Apply loads the stored preview envelope, rejects expired previews, rejects replay of a consumed preview, re-renders/re-hashes the proposal from current canonical bytes, and rejects if any reviewed hash or operation differs. A bare `{confirmed:true}` is invalid. User confirmation for project scope and personal scope uses the same preview-bound contract; V3 `ApplyModePolicyAuto` bypasses only the user-facing confirmation copy after policy authorization, not validator/hash/recovery checks.

- [ ] **Step 4: Run apply tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal ./internal/module/skill -run 'Test.*Proposal|Test.*Apply|Test.*Validator' -count=1
```

Expected: proposal apply writes only canonical and publisher handles mirrors.

## Task 7: UI/RPC Review Surface

**Files:**
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/module/skill/rpc.go`
- Create: `internal/module/skill/rpc_skill_proposal_test.go`
- Modify: `internal/module/turn/skill_extractor.go`
- Modify: `internal/module/turn/feedback_proposer.go`
- Modify: `internal/module/memory/kairos.go`
- Modify: `cmd/agent-terminal/frontend/vue-app/services/skills-api.js`
- Modify: frontend files after locating current UI source path with `rg`

- [ ] **Step 1: Expose RPCs**

Expose these RPCs. Each handler derives identity server-side from trusted project context (`cwd` -> repo fingerprint) or the local Super-Dolphin profile (`owner_key`), never from client-supplied identity fields:

- `skill_proposals/list_pending`
- `skill_proposals/get`
- `skill_proposals/preview_diff`
- `skill_proposals/approve_apply`
- `skill_proposals/reject`

- [ ] **Step 2: Add backend RPC tests**

Cover:

- `preview_diff` is read-only
- `approve_apply` requires explicit confirmation
- `approve_apply` rejects missing `proposal_preview_id`, stale `preview_hash`, expired preview, replayed preview, mismatched reviewed hashes, missing backup acknowledgement, and missing confirmation copy
- `list_pending` derives caller identity server-side from trusted `cwd` or local profile and does not accept client-supplied `repo_fingerprint` / `owner_key`
- `get` returns not found for caller repo fingerprint mismatch or owner-key mismatch
- `preview_diff` returns not found for caller repo fingerprint mismatch or owner-key mismatch
- `approve_apply` rejects caller repo fingerprint mismatch or owner-key mismatch without leaking whether the proposal exists
- `reject` rejects caller repo fingerprint mismatch or owner-key mismatch without leaking whether the proposal exists
- reject transitions pending to rejected
- project apply without confirmation copy is rejected

- [ ] **Step 3: Add UI only after locating source**

Before editing frontend, run:

```bash
rg -n "skill|proposal|candidate|pending" cmd/agent-terminal frontend internal -g'*.vue' -g'*.ts' -g'*.js'
```

Use the discovered UI source files. Do not assume `cmd/agent-terminal/frontend/src` exists.

- [ ] **Step 4: Add UI behavior tests**

Mock pending proposal list, diff preview, approve, and reject. Assert project-scope and personal-scope apply buttons stay disabled until the user has loaded a fresh preview, reviewed the displayed hashes/diff, acknowledged backup, and supplied the required confirmation copy. The approve call must send the server `proposal_preview_id` and `preview_hash`; the UI must reload preview after a stale/rejected apply rather than retrying a consumed preview.

- [ ] **Step 5: Replace any remaining old skillcandidate consumers**

Replace any remaining production writers/readers of old `skillcandidate` with `skillproposal`. If V1 already removed or disabled the old entrypoint, this step wires the replacement UI/RPC to `skill_proposals` rather than reviving `skills/candidate/*`:

- turn `DefaultExtractor` inserts `skill_proposals` with redacted evidence instead of old candidates
- `FeedbackProposer` inserts `skill_proposals`
- memory feedback insertion routes through the proposal recorder/store
- skill RPC removes or rewires `skills/candidate/*` to `skill_proposals/*`
- frontend pending/candidate review UI calls `skill_proposals/*`
- migrations/sqlc/store tests prove no new runtime code writes `skill_candidates`
- V1 removed/unsupported old candidate handlers remain removed; compatibility wrappers must not write `skill_candidates`

Add a final grep check:

```bash
rg -n "skillcandidate|skills/candidate|SkillCandidate|InsertSkillCandidate" internal cmd
rg -n "skilllibrary|skillforge|fbsd|skill_read_section|SkillManifestRenderer|RenderManifest" internal cmd
```

Expected: matches are deleted-code tests, migration history, or explicit compatibility rejection tests only. V2 cannot be accepted if the V1 D7 old-runtime removal gate is still live in production wiring.

- [ ] **Step 6: Run frontend checks**

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: UI review surface builds.

## Task 8: Plan-Level Verification

- [ ] **Step 1: Run affected Go packages**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal ./internal/module/skill ./internal/module/turn ./internal/module/memory ./internal/store/skillproposal ./internal/store/auditlog -count=1
```

- [ ] **Step 2: Run sqlc and guard**

Run:

```bash
make sqlc-verify
make guard
```

- [ ] **Step 3: Run configured proposal smoke**

Run the V2 smoke with an explicit proposal model test config:

```bash
./scripts/test_with_guard.sh ./internal/module/skillproposal -run 'Test.*Proposal.*Configured.*Smoke|Test.*Generator.*Smoke' -count=1
```

Expected: a real trigger evidence row is claimed, model output is validated, and a pending proposal is inserted. Evidence-only unset-model behavior is tested separately and does not satisfy V2 completion.

- [ ] **Step 4: Run frontend checks**

Run from `cmd/agent-terminal/frontend`:

```bash
node scripts/size-guard.cjs
npx vitest run
npm run build
```

- [ ] **Step 5: Check status**

Run:

```bash
git diff --check
rg -n "skilllibrary|skillforge|fbsd|skill_read_section|SkillManifestRenderer|RenderManifest" internal cmd
git status --short
```

Expected: no provider mirror writes are committed, and old V1 runtime-skill consumers are absent from production wiring before V2 is accepted.

## Accepted Defaults And Gates For This Plan

- D6 is fixed: use a dedicated auxiliary proposal model config.
- V2 completion requires a configured proposal model smoke that creates a validated pending proposal. If the auxiliary proposal model is unset, durable redacted evidence is stored in `skill_proposal_evidence` when evidence capture is enabled, and no pending proposal row is inserted; that is a disabled/degraded negative path, not release acceptance.
- `skillcandidate` production usage must be migrated into `skill_proposals` or removed from runtime.
- V2 starts only after the V1 D7 cutover gate has passed: old `skilllibrary`, `skillforge`, `fbsd`, `skill_read_section`, Codex manifest injection, and old `skills/candidate/*` production entrypoints are removed or compile-proven unreachable.
- Evidence outbox generation is lease/idempotency/checkpoint protected; multi-worker and crash retry paths cannot duplicate proposals or permanently strand captured evidence, and cannot repeat a model call once a sanitized generation-result checkpoint exists. Pre-checkpoint worker crashes may repeat a model call unless provider-level request idempotency is implemented and tested.
- Proposal rows do not retain full evidence packs. They keep `evidence_id`, digest, idempotency key, and compact summary only; terminal evidence retention can purge or compact outbox rows without being defeated by a copied `skill_proposals.evidence_json`.
- Proposal payloads are safety-scanned separately from evidence. `proposal_json` and generation-result checkpoints must not persist model-echoed secrets, raw private paths, raw transcript, raw diff, or provider mirror content.
- Apply recovery is durable outside the failed SQL path: before canonical mutation, apply reserves a local recovery journal; if canonical mutation succeeds but proposal status persistence fails, the journal records or can recover the observed post hash before retry/UI recovery; `skill_proposal_apply_recovery` is a DB-backed mirror of that recovery state, not the only sink.
- Redacted evidence retention includes an actual purge path for terminal evidence rows; retention is not only a configuration label.
- Invalid model output persistence semantics are fixed: no proposal row; redacted audit/metric only.
- Audit is fail-closed for proposal apply.
- Executable support-file policy is fixed: default reject scripts unless user explicitly approves high-risk writes.
- All V2 canonical mutations require explicit user confirmation by default. Project proposal confirmation copy must be explicit because project scope writes affect team-reviewed canonical files; personal proposal confirmation can use lighter copy but cannot be bypassed by proposal JSON.
