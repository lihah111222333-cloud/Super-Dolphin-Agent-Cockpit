# Super Agent v3 Agent Context Policy

## Scope

This file defines how agents should load context, use local repo skills, and verify work in this repository. It is adapted for `super-agent-v3`; do not reuse `wjboot-v2` paths or commands unless the user explicitly asks for that other repository.

## Default Context Loading Order

Path discovery mode, when the user asks where files live or which path to change:

1. `README.md` for the current project layout and entry points.
2. `docs/doc/codemap/README.md` for the code-map table of contents and reading boundaries.
3. Open one relevant code-map volume, selected from the table of contents.
4. Use `rg` against `docs/doc/codemap/ai-index.json` or exact source directories for symbols and paths.
5. Open exact source files and same-package tests after the path is resolved.

Behavior reading mode, when the user asks how something works:

1. Source code and tests are the source of truth.
2. Use `docs/decisions/*.md` and `docs/adr/*.md` for accepted architecture decisions.
3. Use `docs/契约/*.md` for conventions such as fx, rungroup, jrpc2, sqlc, stateless, MCP service, and onion architecture.
4. Use `docs/doc/codemap/*.md` to navigate large subsystems.
5. Treat `docs/plans/**`, `docs/迁移/**`, `docs/superpowers/plans/**`, and old reports as historical planning material unless the user explicitly asks for migration history.
6. Read `docs/internal-notes/LSP系统提示词.md` for mandatory LSP tool chain usage guidelines and workflow before using any LSP tools.


## Context Budget Hygiene

- Prefer targeted `rg` searches and single-file reads over broad directory scans.
- Do not recursively read or index `.build-cache/`, `bin/`, frontend `node_modules/`, frontend `dist/`, `.worktrees/`, `.workspace/`, `.claude/`, `.agent/code_exec/`, `.agent/workspaces/`, `.agnet/report/`, `.agnet/shared/_internal/`, `.agnet/shared/handoff/`, or generated test reports by default.
- Do not bulk-load `.agent/skills/**`. Repo-local skills are opt-in references, not default context.
- If a generated artifact appears stale, verify the generator or check target before editing it directly.

## Repo-Local Skill Policy

`super-agent-v3` contains repo-local skill documents under `.agent/skills/**/SKILL.md`.

- Skills should be utilized automatically when they are relevant to the current task context, file types, or directory.
- The agent is encouraged to load and apply these skills to ensure repository-specific best practices are followed.
- If a skill is loaded, its instructions are subordinate to this file, the user's latest instruction, and current repository evidence.

This policy governs agent instruction loading from `.agent/skills/**`. It does not disable or describe the product runtime skill pipeline. Runtime canonical skills are app-managed under project `<cwd>/.agent/skills` and personal `~/.super-dolphin/skills/personal/{user,agent,imported,hub}` roots, then reconciled into generated provider-native mirrors so Claude discovers `<cwd>/.claude/skills` and Codex discovers `<cwd>/.codex/skills` plus provider-home `skills`. Provider mirrors are generated artifacts, not canonical truth. For runtime skill behavior, inspect `internal/module/skill*`, `internal/provider/shared/provider_home.go`, provider mirror tests, and related toolbridge compatibility tests.

## Sub-Agent & Orchestration Policy

- Sub-agents MUST use `mcp-go-agent-orchestration` tools (`task_create_dag`, `task_start_node`, `task_update_node`) to manage task lifecycles, dependencies, and execution status.
- Orchestration via `mcp-orch` ensures observability, retry logic, and structured handoffs between parallel agents.


## Current Repository Shape

- Go module: `github.com/anthropic-ai/super-agent-v3`, Go `1.25.7`.
- Entrypoints:
  - `cmd/agent-terminal`: Wails/Vue desktop UI and HTTP server.
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
  - `cmd/agent-terminal/frontend`: Vue/Vite frontend package.

## Command Policy

- Run commands from the repository root unless a command explicitly changes directory.
- This repository has no `backend/` submodule; do not use `go -C backend`, `GOWORK=off go -C backend`, or `./cmd/code_guard`.
- Prefer repository wrappers over ad hoc commands:
  - `make guard`
  - `./scripts/test_with_guard.sh <packages> -count=1`
  - `make test`
  - `make build-plain`
  - `make install-hooks`
  - `make sqlc-verify`
  - `make codemap-check`
- Frontend commands run in `cmd/agent-terminal/frontend`.

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

Frontend changes:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

`cmd/agent-terminal` embeds frontend assets from `cmd/agent-terminal/frontend/dist`. The bundle is gitignored, so first clone, cleaned dist, and agent-terminal build/run verification require a frontend build before the Go build/run can represent the current UI.

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
严禁使用静默降级、系统时间、默认配置或捕获错误等隐式兜底逻辑。

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
