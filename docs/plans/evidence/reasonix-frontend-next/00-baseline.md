# Reasonix Frontend Next — Task 0 Baseline

## Execution topology override

`EXECUTION_OVERRIDE_SINGLE_SERIAL_AGENT`

The user's latest instruction overrides the plan's historical three-lane topology: Tasks 0–8 use this one worktree and this one serially reused implementer. Task 0 stops for controller review before Task 1 begins. No additional worktree, subagent, DAG, or orchestration node was created by the implementer.

## Ownership

- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`
- Branch: `codex/reasonix-frontend-next-serial`
- Worktree HEAD / lane base: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- `origin/main`: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- Reasonix reference HEAD: `1f5740a2129ea54bda7c86755ed58c88b84c16b4`
- Owned files in Task 0: this evidence file and `docs/plans/2026-07-12-reasonix-frontend-next-absorption.md`
- Main checkout was not modified by this implementer. Its pre-existing untracked plan document remains visible in the main checkout.

## Git and tool baseline

| Command | Exit | Result |
|---|---:|---|
| `git -C /Users/mima0000/Desktop/wj/super-agent-v3 status --short --branch` | 0 | `## main...origin/main`; untracked plan document only |
| `git -C /Users/mima0000/Desktop/wj/super-agent-v3 rev-parse HEAD` | 0 | `a7df089e32e4135a90f10a52f6ef10069cab8353` |
| `git -C /Users/mima0000/Desktop/wj/super-agent-v3 rev-parse origin/main` | 0 | `a7df089e32e4135a90f10a52f6ef10069cab8353` |
| `git -C /Users/mima0000/Desktop/wj/deepseek-reasonix rev-parse HEAD` | 0 | `1f5740a2129ea54bda7c86755ed58c88b84c16b4` |
| `go run ./cmd/codex-worktree-setup ready` | 0 | worktree-local `bin/mcp-lsp` and `.codex/config.toml`; tools `file, inspect, xref, grep, structure, patch_edit, completion` |
| `go run ./cmd/codex-worktree-setup verify` | 0 | worktree-local binary/config/language servers verified; seven tools visible |
| `codex mcp get lsp` | 0 | enabled stdio peer; cwd and command both point at this worktree |
| root production Go file count | 0 | `31`; Task 4 may not add another `internal/module/thread/*.go` production file |

## LSP evidence and frozen seams

All calls used `work_dir=/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`.

### Locate / structure

- `grep(text_search, "keydown")` and `structure(document_symbol)` locate the global Escape owner at `frontend-app/src/pages/chat/ChatPage.jsx:202-214` and the composer Enter/IME owner at `frontend-app/src/pages/chat/composer/ComposerDock.jsx:39-49,88,114`.
- `structure(workspace_symbol, "NewUIStateHandlers")` locates the typed preference RPC owner at `internal/module/uistate/rpc.go:47-124`; Settings writes are in `frontend-app/src/pages/settings/settingsPageRuntime.js:476-546`.
- `grep(text_search, "ReadMessages")` and `structure(document_symbol)` locate `service.ReadMessages` at `internal/module/thread/history.go:54-94` and `readMessagesPageSource` at `internal/module/thread/lifecycle_helpers.go:659-673`.
- `grep(text_search, "ReadProviderMessagesPage")` locates JSONL paging at `internal/util/historyjsonl/page.go:31-72`; the current `provider.MessagePageResult` has no source revision field.
- `grep(text_search, "FRONTEND_TRACE_ALLOWED")` locates the canonical trace phase/metadata/status allowlists in `frontend-app/src/shared/api/wails/wailsBridgeConstants.js:17-40` and sanitizer use in `wailsBridgeTraceEvents.js:238-284`.
- `grep(text_search, "materializ")` locates the real 80-message materialization entry at `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js:3-56`.

### Inspect / understanding

- `inspect(definition|hover)` confirms `useComposerSendKeyHandler` is defined at `ComposerDock.jsx:39-49`; it owns Enter send, modifier rejection, and IME/229 protection. The ChatPage hook owns only unmodified global Escape interrupt and preserves local Escape surfaces.
- `inspect(definition)` confirms `ui/preferences/set` delegates to `Service.SetPreference` under `withPreferenceScope`; the frontend façade remains `setPreference`, while Settings currently surfaces write errors.
- `inspect(definition|hover)` confirms `ReadMessages` delegates to `readMessagesPageSource`, which selects live-session paging first and JSONL paging otherwise.
- `inspect(definition)` on `sanitizeFrontendTraceEvent` confirms all emitted trace events pass the phase/status allowlists and metadata sanitizer before remote eligibility.
- `inspect(hover)` confirms `useTimelineMaterialization` returns `{hiddenOlderCount, revealOlder, visibleMessages}` and does not mutate the store.

### Xref / impact surface

- `xref(references)` for ChatPage `onKeyDown` returns only declaration/add/remove listener. `xref(references|call_hierarchy)` for `useComposerSendKeyHandler` returns `ComposerDock` as its only production caller.
- `xref(references)` for `NewUIStateHandlers` returns module registration plus focused tests. `setPreference` is projected through `backendApiFactoryCore.js` and re-exported from `backendApi.js`.
- `xref(references|call_hierarchy)` for `service.ReadMessages` returns the RPC handler, contract adapter, tests, and outgoing calls to `readMessagesPageSource`, decoration, pagination, and page publication.
- `xref(references)` for `readMessagesPageSource` returns `ReadMessages` as its caller. Current live pager and JSONL fallback both return `MessagePageResult` without source revision.
- `xref(call_hierarchy)` for `emitFrontendTraceEvent` returns RPC start/done/failed, runtime telemetry, slow bridge patch, React slow render, crash reporting, and initial-level diagnostics callers; outgoing edges are sanitizer → remote eligibility → enqueue → flush schedule.
- `xref(references|call_hierarchy)` for `useTimelineMaterialization` returns `ConversationTimeline` and its focused test harness.

### Exact read conclusions

- `ChatPage.jsx:202-214`: retained local Escape handlers must continue to win through `hasOpenLocalEscapeSurface()`; the future global dispatcher cannot swallow them.
- `ComposerDock.jsx:39-49`: history navigation must coexist with Enter send and IME checks rather than replace this handler blindly.
- `settingsPageRuntime.js:476-546` plus `uistate/rpc.go:47-78`: typed preference get/set is the canonical Settings persistence seam. Shortcut overrides must use this route, validate the whole draft, and perform explicit read-after-write.
- `history.go:54-94` plus `lifecycle_helpers.go:659-673`: prompt history must reuse the canonical page source and fail the whole page on source errors. `ListByCWD` at `service.go:223-229` is prefix-based and therefore is not sufficient for the new prompt-history authorization; exact CWD equality must be checked against canonical `ThreadRecord` before message reads.
- `contract.go:198-212` and event/update writers show `Ref.UpdatedAt` is thread lifecycle/status metadata. It must not be used as message revision or nonce input.
- `historyjsonl/page.go:18-24,31-72,86-159` provides stable file offsets/cursors but no `sourceRevision`. Task 4 must add a non-empty deterministic revision for every canonical page source. JSONL revision may use canonical file identity/stat content but must expose no path/content; loaded-session revision must derive from stable canonical page data, never current time, process counters, frontend generation, or second-level thread timestamps.
- `wailsBridgeConstants.js:17-40` and `wailsBridgeTraceEvents.js:248-345`: performance phases and bounded metadata keys must be added to these canonical allowlists and survive sanitizer plus `shouldRemoteFlushFrontendTrace`; a false return remains observable.
- `main.jsx:76-136`: diagnostics bootstrap and StrictMode root are owned here; the headless monitor requires explicit start/cleanup ownership without a second UI surface.
- `useTimelineMaterialization.js:1-58`: the benchmark must invoke the real materialization calculation exported from this owner, preserving the 80/80 behavior without duplicating it.

### Diagnostics

`file(diagnostics)` returned `No diagnostics found` for all Task 0 production seams: ChatPage, ComposerDock, Settings runtime/facade, UIState preferences/RPC, thread history/lifecycle/contract/RPC, history JSONL pager, trace constants/events, `main.jsx`, and timeline materialization.

## Frozen replacement contract

- Every JSONL and loaded-session canonical message page source returns a non-empty deterministic `sourceRevision`.
- Exact CWD membership is checked before any prompt-history message read.
- Prompt-history nonce is SHA-256 over exact-CWD ordered thread identity, lifecycle, and source revision. It changes for same-second/same-text user-message changes and thread create/delete/archive.
- `agent_threads.updatedAt`, current time, in-memory counters, and frontend generation are forbidden nonce/revision sources.
- Pure scanner/state-machine code goes in `internal/module/thread/prompthistory`; root adaptation stays narrow in existing `history.go`. Root production file budget remains 31.
- Trace performance phases and bounded metadata must pass the canonical sanitizer and remote-flush path.

No provider source was found to be intrinsically unable to produce a deterministic revision; the field and derivation are absent today and remain Task 4 implementation work, not a Task 0 baseline blocker.

## Controller disposition and dependency preparation

The controller reviewed the first failure and classified it as a fresh-worktree environment prerequisite rather than a source baseline regression. `README.md` explicitly requires `( cd frontend-app && npm ci )`, and the locked `frontend-app/package-lock.json` is tracked. The controller authorized that exact dependency installation and a full Task 0 baseline rerun while requiring the original failure above to remain preserved.

Command: `cd frontend-app && npm ci`

Exit: `0`

Output summary:

```text
npm warn deprecated whatwg-encoding@3.1.1: Use @exodus/bytes instead for a more spec-conformant and faster implementation

added 448 packages, and audited 449 packages in 4s

140 packages are looking for funding
  run `npm fund` for details

found 0 vulnerabilities
```

Post-install checks:

- `git diff --name-only -- frontend-app/package.json frontend-app/package-lock.json`: empty.
- `git status --short`: only the owned untracked plan document and evidence directory.
- `git diff --check`: exit 0.

## Unmodified baseline commands

The first required command failed, so the stop-on-first-failure rule prevented every later baseline command from running.

### First deterministic failure

Command: `cd frontend-app && npm run guard:critical-skip`

Exit: `1`

Raw output:

```text
> super-dolphin-frontend-app@0.1.0 guard:critical-skip
> node scripts/no-critical-skip.mjs && node scripts/no-silent-async-failure.mjs && node scripts/frontend-contract-store-guard.mjs && node scripts/frontend-code-size-guard.mjs && node scripts/frontend-z-index-token-guard.mjs

node:internal/modules/package_json_reader:301
  throw new ERR_MODULE_NOT_FOUND(packageName, fileURLToPath(base), null);
        ^

Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'typescript' imported from /Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial/frontend-app/scripts/no-critical-skip.mjs
    at Object.getPackageJSONURL (node:internal/modules/package_json_reader:301:9)
    at packageResolve (node:internal/modules/esm/resolve:768:81)
    at moduleResolve (node:internal/modules/esm/resolve:991:11)
    at #cachedDefaultResolve (node:internal/modules/esm/loader:713:20)
    at #resolve (node:internal/modules/esm/loader:695:17)
    at ModuleLoader.getOrCreateModuleJob (node:internal/modules/esm/loader:615:33)
    at ModuleJob.syncLink (node:internal/modules/esm/module_job:160:33) {
  code: 'ERR_MODULE_NOT_FOUND'
}

Node.js v25.6.1
```

Environment confirmation after failure:

```text
frontend-app/node_modules: missing
frontend-app/node_modules/typescript/package.json: missing
```

Not run because of this baseline blocker:

- `npm run typecheck:contracts`
- `npm run audit:rpc-contracts`
- `npm test`
- `npm run build`
- `go test ./internal/module/thread ./internal/module/uistate -count=1`

At that checkpoint Task 0 was not complete and no commit was created. Installing dependencies or wiring a worktree-local dependency runtime would have changed the execution state after a failed baseline, so the implementer stopped for controller direction instead of silently repairing or bypassing it.

### Controller-authorized full rerun

After the prerequisite installation above, the complete Task 0 baseline was rerun from the first command.

| Command | Exit | Output summary |
|---|---:|---|
| `cd frontend-app && npm run guard:critical-skip` | 0 | critical skips `0`; silent catch handlers `0`; all contract/store counters `0/0`; code-size `files=361, frozen=0`; z-index guard passed |
| `cd frontend-app && npm run typecheck:contracts` | 0 | `tsc -p tsconfig.contracts.json --noEmit` completed without diagnostics |
| `cd frontend-app && npm run audit:rpc-contracts` | 0 | RPC methods `138`, registry `138`, Go handlers `294`; all missing/mismatch/drift/guard/validator counters `0` |
| `cd frontend-app && npm test` | 0 | `128` test files and `1617` tests passed; duration `75.58s` |
| `cd frontend-app && npm run build` | 0 | Vite `8.0.16`; `5551` modules transformed; build completed and canonical sync script ran |
| `go test ./internal/module/thread ./internal/module/uistate -count=1` | 0 | both packages `ok`; thread `0.309s`, uistate `0.329s` |

The build was followed immediately by `git status --short` and `git diff --name-only -- cmd/agent-terminal/web-dist docs/doc/codemap`; neither showed generated drift.

## Generated artifacts and blockers

- Generated artifacts changed: none. `git diff --name-only -- docs/doc/codemap cmd/agent-terminal/web-dist` remained empty after the build.
- `git diff --check`: exit 0 before staging.
- Worktree status before staging: untracked plan document and untracked `docs/plans/evidence/` only.
- Resolved blocker: `BLOCKED_BASELINE_DEPENDENCY` was classified by the controller as a documented fresh-worktree prerequisite and resolved only with the locked `npm ci` command. The original failure remains above.
- Remaining blockers before the commit hook: none for Task 0.
- Reviewer/controller disposition: environment prerequisite accepted; full baseline rerun authorized and completed. Final commit remains subject to exact-file staging and controller review before Task 1.

## Commit-hook generated drift blocker

The exact controller-required commit command was attempted once and failed. It must not be retried at this checkpoint.

Command:

```text
git commit -m "docs(plan): lock Reasonix frontend serial baseline"
```

Exit: `1`

Raw hook output:

```text
[pre-commit] codemap refresh...
[generated] refresh codemap artifacts
/Library/Developer/CommandLineTools/usr/bin/make archtest-map-refresh
go run ./scripts/archtestmap
✅ archtest map and README stats refreshed
go run scripts/codemap_index.go
ai-index.json: 393 files, 1549 total refs, 352 sections, 19 codemaps
✅ codemap ai-index.json refreshed
[generated] refresh AI project map
node scripts/generate_ai_project_map.mjs --filesystem-scan
project-map: 4454 files, 7 domains, drift=OK
✅ project map refreshed
[pre-commit] AI maintenance gates...
ai-maintenance evidence file not supplied; command gates will run, but LSP evidence remains controller-blocking

==> make codemap-check
/Library/Developer/CommandLineTools/usr/bin/make archtest-map-check
go run ./scripts/archtestmap --check
✅ archtest map and README stats are up to date
go run scripts/codemap_index.go --check
ai-index.json: 393 files, 1549 total refs, 352 sections, 19 codemaps (up to date)
✅ codemap generated files are up to date

==> make project-map-check
node scripts/generate_ai_project_map.mjs --check --strict-drift
project-map: 10 files up to date, drift=OK
✅ project map generated files are up to date

==> git diff --check
[pre-commit] full codebase guard...
✅ 入口守卫: 未发现裸跑 go test 入口。
📏  文件≤800 函数≤80 嵌套≤4 CC≤10 下划线≤3 包文件≤30
📊 生产 冻结棘轮通过 — 0 个文件冻结中
📊 测试 冻结棘轮通过 — 0 个文件冻结中
📊 priority SSA 冻结检查通过 — 0 条违规冻结中
✅ 代码守卫: 全部通过
ok  github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest  22.050s
✅ pre-commit OK
[commit-msg] Chinese commit message guard...
FAIL: commit title must contain Chinese text.
  title: docs(plan): lock Reasonix frontend serial baseline
```

The pre-commit refresh staged generated drift before the commit-msg rejection. Exact status immediately after the failed attempt:

```text
M  docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md
M  docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json
M  docs/doc/codemap/project-map/AI_PROJECT_MAP.md
M  docs/doc/codemap/project-map/index/docs-agent.tsv
A  docs/plans/2026-07-12-reasonix-frontend-next-absorption.md
A  docs/plans/evidence/reasonix-frontend-next/00-baseline.md
```

Exact generated name/status and diff summary:

```text
M  docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md
M  docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json
M  docs/doc/codemap/project-map/AI_PROJECT_MAP.md
M  docs/doc/codemap/project-map/index/docs-agent.tsv

docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md       |  2 +-
docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json  | 10 +++++-----
docs/doc/codemap/project-map/AI_PROJECT_MAP.md         |  6 +++---
docs/doc/codemap/project-map/index/docs-agent.tsv      |  2 ++
4 files changed, 11 insertions(+), 9 deletions(-)
```

Exact semantic diff:

```text
AI_PROJECT_DRIFT.md: indexed files 4452 -> 4454.
AI_PROJECT_MANIFEST.json: files 4452 -> 4454; docs-agent 909 -> 911; docs-agent size 162 -> 162.4; docs module 901 -> 903.
AI_PROJECT_MAP.md: indexed files 4452 -> 4454; docs-agent 909/162.0 KB -> 911/162.4 KB; docs module 901 -> 903.
index/docs-agent.tsv: added the plan document and this evidence document.
```

The initial checkpoint provisionally labeled this `BLOCKED_BASELINE_DRIFT` and restored the four files while awaiting controller adjudication. The controller then inspected `.githooks/README.md` and corrected that classification: pre-commit is required to refresh and stage project-map artifacts from the staged snapshot. Because the exact semantic diff only indexes the two newly staged docs and changes no unrelated path, these four files are expected hook-owned generated output, not anomalous drift.

The earlier `npm run build` invoked only Vite plus `sync-frontend-dist.mjs`, so it correctly produced no `web-dist`/codemap/project-map drift. The later Git hook uses a different generator boundary: it sees the newly staged plan/evidence and updates the docs-agent project map. Additional paths, content unrelated to those staged docs, or a failed generated-state check would still be `BLOCKED_BASELINE_DRIFT`.

The actual first commit blocker was the English-only title. The plan contained nine English-only `git commit -m` examples, all of which were corrected to retain their Conventional Commit prefixes while adding Chinese titles. Task 0 now uses `docs(plan): 锁定 Reasonix 前端串行基线`. The retry must use normal hooks; `--no-verify` is explicitly forbidden. No commit existed at the time of this disposition.
