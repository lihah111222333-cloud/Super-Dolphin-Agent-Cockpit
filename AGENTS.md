# Super Agent v3 Agent Context Policy

## Scope

This file defines how agents should load context, use local repo skills, and verify work in this repository. It is adapted for `super-agent-v3`; do not reuse `wjboot-v2` paths or commands unless the user explicitly asks for that other repository.

## Default Context Loading Order

Path discovery mode, when the user asks where files live or which path to change:

1. `README.md` for the current project layout and entry points.
2. `docs/doc/codemap/README.md` for the code-map table of contents and reading boundaries.
3. Open one relevant code-map volume, selected from the table of contents.
4. Use `rg` against `docs/doc/codemap/ai-index.json` or exact source directories to narrow candidate symbols and paths.
5. Read `docs/internal-notes/LSP系统提示词.md`, then use LSP symbol/navigation tools to confirm definitions, references, callers/implementations, and diagnostics before deciding the path to edit or report.
6. Open exact source files and same-package tests after the LSP-confirmed path is resolved.

Behavior reading mode, when the user asks how something works:

1. Source code and tests are the source of truth.
2. Use `docs/decisions/*.md` and `docs/adr/*.md` for accepted architecture decisions.
3. Use `docs/契约/*.md` for conventions such as fx, rungroup, jrpc2, sqlc, stateless, MCP service, and onion architecture.
4. Use `docs/doc/codemap/*.md` to navigate large subsystems.
5. Treat `docs/plans/**`, `docs/迁移/**`, `docs/superpowers/plans/**`, and old reports as historical planning material unless the user explicitly asks for migration history.
6. Read `docs/internal-notes/LSP系统提示词.md`, then use LSP tools for symbol navigation, reference/call-hierarchy checks, hover/signature context, and diagnostics on the relevant files before answering behavior, impact, or implementation questions.

## Mandatory LSP Usage

- Any task involving source-code path discovery, behavior analysis, code review, risk adjudication, refactoring, bug fixing, feature implementation, or impact analysis must use the LSP workflow from `docs/internal-notes/LSP系统提示词.md`.
- `rg`, code-map files, and normal shell reads may be used to find candidates, but they do not replace LSP confirmation. Do not conclude impact, ownership, call paths, or edit targets from text search alone when LSP tools are available.
- Before changing shared symbols or cross-module code, use LSP references or call hierarchy to check the affected surface. After changing code, use LSP diagnostics on the touched files before running the matching tests or guards.
- If LSP tools are unavailable, fail to initialize, or cannot inspect the target language, stop before code changes and report the exact blocker. Do not silently fall back to grep-only or file-read-only analysis.
- Pure documentation edits, Git/status operations, generated-artifact inspection, command execution, and test runs may skip LSP only when they do not require source-code semantics. The final report must state that LSP was skipped and why.


## Context Budget Hygiene

- Prefer targeted `rg` searches and single-file reads over broad directory scans.
- Do not recursively read or index `.build-cache/`, `bin/`, frontend `node_modules/`, frontend `dist/`, `.worktrees/`, `.workspace/`, `.claude/`, `.agent/code_exec/`, `.agent/workspaces/`, `.agnet/report/`, `.agnet/shared/_internal/`, `.agnet/shared/handoff/`, or generated test reports by default.
- Do not recursively read or index `docs/archive/**` by default. Use it only when the user asks for historical reports, old agent notes, migration evidence, or provenance.
- Do not bulk-load `.agent/skills/**`. Repo-local skills are opt-in references, not default context.
- If a generated artifact appears stale, verify the generator or check target before editing it directly.

## Repo-Local Skill Policy

`super-agent-v3` contains repo-local skill documents under `.agent/skills/**/SKILL.md`.

- Skills should be utilized automatically when they are relevant to the current task context, file types, or directory.
- The agent is encouraged to load and apply these skills to ensure repository-specific best practices are followed.
- If a skill is loaded, its instructions are subordinate to this file, the user's latest instruction, and current repository evidence.

This policy governs agent instruction loading from `.agent/skills/**`. It does not disable or describe the product runtime skill pipeline. Runtime canonical skills are managed by this project under project `<cwd>/.agent/skills` and active personal roots `~/.super-dolphin/skills/personal/{user,agent,imported}`; `personal/hub` is catalog-only and must not be scanned, mirrored, or treated as a normal personal skill. Active canonical skills are reconciled into generated provider-native mirrors so Claude discovers `<cwd>/.claude/skills` plus `~/.claude/skills`, and Codex discovers `<cwd>/.agents/skills` plus `~/.agents/skills`. Explicit provider homes may still use their own `skills` directory when configured. Provider mirrors are generated artifacts, not canonical truth. For runtime skill behavior, inspect `internal/module/skill*`, `internal/provider/shared/provider_home.go`, provider mirror tests, and related toolbridge compatibility tests.

## Sub-Agent & Orchestration Policy

- Sub-agents are not required to bind their lifecycle to `mcp-orch` or `mcp-go-agent-orchestration`.
- Use the platform's native sub-agent or multi-agent capability directly when that is the available or requested execution path.
- Use `mcp-orch` only when the task specifically needs persistent DAG state, retry/lease semantics, cron/wakeup behavior, or structured cross-agent handoff records.
- If `mcp-orch` tools are unavailable, continue with native sub-agents or single-session execution as appropriate, and report any reduced observability instead of blocking solely because orchestration tools are missing.


## Current Repository Shape

- Go module: `github.com/anthropic-ai/super-agent-v3`, Go `1.25.7`.
- Entrypoints:
  - `cmd/agent-terminal`: Wails desktop host and HTTP server. In dev, `VITE_DEV_URL` proxies the host to the current `frontend-app` Vite server; without that dev proxy it serves the legacy embedded bundle.
  - `cmd/mcp-orch`: orchestration peer for agent lifecycle, DAG, cron, and toolbridge.
  - `cmd/mcp-lsp`: generic multi-language LSP peer.
  - `cmd/mcp-ida`: IDA MCP peer.
- Core packages:
  - `internal/app`: app assembly, runner, adapters.
  - `internal/contract`: interfaces and DTOs crossing module boundaries.
  - `internal/module`: business modules such as memory, prompt, thread, cron, skill.
  - `internal/platform`: db, rpc, config, runtime safety, toolbridge infrastructure.
  - `internal/provider`: provider adapters for Claude CLI, Codex, and related runtimes.
  - `internal/store`: sqlc-backed persistence layer.
  - `internal/archtest`: architecture guards, code-size guard, and ratchet baseline.
  - `pkg`: reusable public libraries.
  - `frontend-app`: current React/Vite new UI package used by `run-new-ui-desktop.sh`.
  - `cmd/agent-terminal/frontend`: legacy Vue/Vite embedded frontend package; edit only when a task explicitly targets the legacy/package-embed path.

## Command Policy

- Run commands from the repository root unless a command explicitly changes directory.
- Local Windows toolchain paths:
  - Go binary directory: `C:\Program Files\Go\bin`.
  - Node.js binary directory: `C:\Program Files\nodejs`.
  - PostgreSQL binary directory: `D:\Program Files\PostgreSQL\16\bin`.
  - If `go`, `node`, or `npm` are not available on `PATH`, invoke them from these directories.
- This repository has no `backend/` submodule; do not use `go -C backend`, `GOWORK=off go -C backend`, or `./cmd/code_guard`.
- Prefer repository wrappers over ad hoc commands:
  - `make guard`
  - `./scripts/test_with_guard.sh <packages> -count=1`
  - `make test`
  - `make build-plain`
  - `make install-hooks`
  - `make sqlc-verify`
  - `make codemap-check`
- 每改完一个 Go 文件，先运行单文件守卫再继续。根据当前设备和 shell 选择守卫入口：
  - macOS / Linux / Git Bash / WSL:
    `./scripts/test_with_guard.sh <file.go>`
  - Windows 原生 PowerShell:
    `pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 <file.go>`
  只传入 Go 文件路径时，该守卫保持安静：exit 0 表示无违规且不输出内容；exit 1 表示有违规，stderr 只输出具体违规项。不要在 Windows PowerShell 中直接运行 `.sh`；必须使用 `.ps1` 入口。
- Current new UI frontend commands run in `frontend-app`.
- Legacy Vue frontend commands run in `cmd/agent-terminal/frontend` only when the task explicitly targets the legacy/package-embed path.

## Completion Verification

Before claiming done, fixed, ready to commit, or ready to merge, run verification that matches the changed surface.

Go code:

```bash
./scripts/test_with_guard.sh <affected packages> -count=1
```

Guard-only fast check:

```bash
make guard
```

Broad Go changes:

```bash
make test
make build-plain
```

Guard, architecture, or baseline changes:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

Current new UI frontend changes:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Legacy embedded Vue frontend changes:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

`run-new-ui-desktop.sh` starts `frontend-app` on Vite and launches `cmd/agent-terminal` with `VITE_DEV_URL`, so the desktop host proxies the current React UI. `cmd/agent-terminal` still embeds assets from `cmd/agent-terminal/frontend/dist` when no dev proxy is configured; that legacy bundle is gitignored and only represents the legacy/package-embed frontend path.

SQL/store changes:

```bash
make sqlc-verify
```

Code-map changes:

```bash
make codemap-check
```

If a command is intentionally skipped because the task is docs-only or the surface is not affected, say so explicitly in the final report.

## 禁止兜底代码
遇到异常、配置为空或数据缺失时，必须立即报错并阻断（Fail-Fast）。
严禁使用包括但不限于静默降级、默认配置、吞错捕获等隐式兜底逻辑。

## 函数级中文注释策略

函数级注释写给维护系统的人看，先说明这个函数或关键代码块做什么，再补充代码本身看不出来的原因、约束和风险；不要逐行复述实现。

必须补函数级中文注释的场景：

- 导出函数、导出方法、导出类型的关键方法。
- 跨模块入口、provider / store / scheduler / thread / prompt / memory / skill / DAG 等关键路径函数。
- 涉及状态变化、幂等、重试、锁、并发、恢复、fail-fast、权限或持久化边界的函数。
- 私有函数如果有效代码行较长、分支复杂、嵌套较深，必须说明它负责什么、不能误改什么。
- React hooks、store slice、service、复杂页面 controller 需要说明数据来源和本地状态边界。

不要求给简单 getter/setter、小型纯映射、小 JSX 渲染片段、测试内直观 helper 机械补注释。

注释风格要求：

- 使用自然、简洁的中文，优先 1-3 行。
- 少用“语义、契约、生命周期、治理、收敛”等重工程词，除非代码里的领域名必须保留。
- 先写明函数或关键代码块做什么；必要时再写为什么这样做、哪里不能乱改、失败时会怎样。

函数级注释守卫应由 `internal/archtest/guardlib.go` 实现，并通过 `./scripts/test_with_guard.sh <file.go>`、`make guard`、`./scripts/test_with_guard.sh ./internal/archtest -count=1` 验证。

## Guard and Baseline Rules

1. Any failing guard means the task is not complete.
2. `internal/archtest/baseline.json` is the per-file ratchet baseline. Default checks may shrink it, but agents must inspect and report any baseline diff.
3. Do not use `go run scripts/code_size_guard.go --freeze` unless the user explicitly approves it or the task is specifically about updating guard rules.
4. Do not weaken guard thresholds to pass a task.
5. Fix-like commits must include a same-commit regression test, fixture, golden, or snapshot that locks the bug.

## Git Hooks

- On first clone, after moving this repository, or before working in a newly linked worktree, run:

```bash
make install-hooks
```

- Confirm `git config --get core.hooksPath` points at this repository's `.githooks` absolute path.
- `pre-commit` checks staged Go impact, rejects staged/worktree mismatch, runs gofmt/go vet/short tests, and runs the guard for Go changes.
- `commit-msg` rejects fix/hotfix/bugfix/修复 commits without a same-commit bug-locking test.
- `pre-push` requires a clean worktree/index/untracked state, only allows pushing current `HEAD`, and rechecks fix-test and affected package tests over the pushed range.

## Git Discipline

- Check `git status --short` before editing and before final reporting.
- Do not revert, stage, or format unrelated user changes.
- Do not use `git add .`; stage only owned files.
- Avoid `--no-verify`. It is only for emergency bypass, and missed checks must be run afterward.
- For atomic push/merge requests, keep unrelated local modifications out of the commit and push only the coherent branch.
