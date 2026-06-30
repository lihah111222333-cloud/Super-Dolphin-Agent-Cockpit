# Source Plan Snapshot

This file copies the execution-relevant content from `docs/plans/2026-06-30-reasonix-hardening-absorption-review.md` so an isolated implementation worktree does not depend on the original untracked plan file.

## Source State

- Date: 2026-06-30.
- Source status: `NEEDS_APPROVAL`.
- Execution flag: `plan_executable=false`.
- Scope: code-review follow-up absorption plan; this does not claim production code is already implemented.
- Baseline recorded by the source review: `deepseek-reasonix@3e824fc3`, `super-agent-v3@bf3b5a77`.

## Review Conclusion

V3 has already absorbed the main Reasonix architecture boundaries: Session ports, PromptAssemblyBoundary, MCP namespace and per-tool lifecycle, event surface, host-direct history/memory tool, and desktop dependency guard. The remaining useful work is hardening, not a new framework migration.

Absorb:

1. Plan/read-only/tool trust single policy gate.
2. Session/provider artifact path helper authority, limited to path helper ownership.

Spike first:

1. Writer tool preview contract, after a model-callable writer inventory.

Do not immediately absorb:

1. Provider wire-normalization, unless V3 later introduces OpenAI/Anthropic API chat providers or CLI adapters start constructing provider `messages` requests.
2. Tool schema canonical-cache registry, unless there is measured performance, schema drift, provider-wire validity, or stable external MCP schema snapshot evidence.
3. Reasonix global built-in registry, blank import wiring, event bus, workers/site/accounts shells.

## Lane A: Plan / Read-Only / Tool Trust

Goal:

- Create a V3-owned tool safety classification owner.
- Separate read-only, plan-safe, trust source, shell safety, provider sandbox, MCP lifecycle, and approval policy.

Non-goals:

- Do not replace Codex/Claude CLI sandbox or permission mode.
- Do not import Reasonix's tool registry or built-in tool model.
- Do not trust external MCP self-reported read-only information.
- Do not treat Claude native plan-mode disables, Codex `update_plan`, or `AgentTypePlan` as a complete V3 execution-stage policy.

Required sequence:

1. Confirm or add an authoritative stage source. `ToolCallRequest` currently has no stage/mode field.
2. If there is no stage source, only land `toolpolicy` unit tests/package; do not wire runtime blocking, and close runtime gate work as `not_applicable_with_evidence`.
3. Add `internal/platform/toolpolicy` as a stdlib-only or low-dependency leaf package.
4. Model `Stage`, `TrustSource`, `Capability`, and `Decision`.
5. Prove `PlanSafe => ReadOnly`, and prove `ReadOnly` does not imply `PlanSafe`.
6. Add shell safety classification using command/subcommand tables and fail-closed shell syntax behavior.
7. Wire toolbridge only after stage source is proven.
8. Preserve `MCPToolLifecyclePolicyReader` as lifecycle authority.
9. Fail closed for external or unknown read-only hints.
10. Ensure read-only subagent or planning-only delegation receives a restricted tool surface, not only prompt instructions.

Lane A tests:

- `PlanSafe => ReadOnly`.
- `ReadOnly` not automatically `PlanSafe`; post-approval-only tools remain rejectable.
- Unknown/external hint fail-closed.
- Bash shell syntax, background/process-control, and dangerous arguments blocked.
- Read-only delegation excludes writers, workflow/meta tools, job/process tools including `wait` and `bash_output`, planning-state mutators including `todo_write` and `complete_step`, recursive agent/skill, connector tools such as `connect_tool_source`, and external untrusted read-only hints.
- Bash wrappers use the same plan-mode shell policy.
- Toolbridge planning-stage allow/deny.
- Lifecycle disabled/suspended/removed cannot be bypassed by plan policy.
- Schema validation remains before handler invocation.
- Existing provider sandbox tests continue passing.

Lane A source verification command:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider' -count=1
make guard
```

## Lane B: Session / Provider Artifact Path Helper Authority

Goal:

- Move deterministic session/provider artifact path derivation to a leaf helper owner.
- Preserve all existing path layouts and business owners.

Non-goals:

- Do not migrate DB fields.
- Do not migrate session JSONL, thread/message, job/artifact, cleanup, provider home, skill mirror, runtime install root, session filename minting, config root discovery, subagent artifact layout, branch/session metadata owner, or Codex identity canonicalization.
- Do not change Codex rollout filename glob.
- Do not change scratchpad cleanup semantics.

Suggested package:

- `internal/platform/sessionpaths`, stdlib-only.

Suggested functions:

- `CodexRolloutGlob(codexHome, threadID string) (string, error)`.
- `ManagedScratchpadDir(tempRoot, projectRoot, threadID string) string`.
- `IsManagedScratchpadDir(tempRoot, dir string) bool`.
- `SanitizeProjectPath(raw string) string`.

Caller migration:

- `internal/provider/codexapp/history_rollout.go` rollout glob construction.
- `internal/util/historyjsonl/history.go` Codex history discovery and `codexRoot` path derivation, preserving empty Codex home fallback to `~/.codex`.
- `internal/module/thread/scratchpad.go` scratchpad path, sanitize, and managed check.

Owner boundaries:

- provider/codexapp still reads Codex rollout.
- thread module still owns scratchpad lifecycle.
- sessionpaths only derives deterministic paths.

Lane B tests:

- `internal/platform/sessionpaths/*_test.go` path golden coverage.
- Preserve `internal/provider/codexapp/history_rollout_path_test.go`.
- Preserve `internal/module/thread/phasef_scratchpad_test.go`.
- Preserve scratchpad cleanup coverage in `internal/module/thread/stop_test.go`.
- Arch guard: sessionpaths is stdlib-only.
- Literal-placement guard: rollout glob fragments and managed scratchpad suffix only appear in allowed production locations or tests.

Lane B source verification command:

```bash
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Path|Provider|Thread' -count=1
make guard
```

## Lane C: Writer Preview Contract Spike

Goal:

- Decide whether V3 needs pre-call preview/diff for model-callable first-party writers.
- Do not add a production preview interface during the spike unless separately approved.

Reason for spike:

- V3 writer surfaces are not the same as Reasonix in-process built-in file writers.
- V3 write surfaces include Codex/Claude native tools, mcp-lsp edit, mcp-orch workflow/shared-file writers, workspace run tools, host-direct memory writer, and artifact/process side-effect tools.
- Reasonix `Previewer` is UI/checkpoint preview, not permission or transaction authority.

Inventory requirements:

1. host-direct default writer: `memory_write`.
2. host-direct non-default writers: `workflow_template_save`, `workflow_template_rollback`.
3. mcp-lsp `edit` actions: `replace_range`, `rename`, `code_action`, `format`.
4. mcp-orch `shared_file_write` and all tools exposed through `defineTaskWriteTool`.
5. workspace tools: `workspace_create_run`, `workspace_merge_run`, `workspace_abort_run`; classify `workspace_merge_run.dry_run` separately.
6. Codex/Claude native writer tools as provider-native boundary only.
7. Artifact/media generation tools such as `tts_generate`, `av_merge`, `video_with_audio`, if present.

Output requirements:

- Output an ADR or plan amendment.
- Mark writer owner as host-direct, mcp-lsp, mcp-orch, provider-native, or artifact/process side effect.
- Mark whether each writer is default model-callable.
- Mark whether deterministic preview can be produced without side effects.
- Do not treat post-call difftracker output as pre-call approval preview.
- If a future interface is recommended, keep it optional and record preview/execute consistency requirements including newline, encoding/large file, path validation, and permission order.

Lane C source verification command:

```bash
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker|Preview' internal cmd docs
```
