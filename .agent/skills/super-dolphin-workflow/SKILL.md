---
name: super-dolphin-workflow
description: "Use when working in /Users/mima0000/Desktop/wj/super-agent-v3 on the user's recurring operations: inspecting current work status, implementing approved plans, fixing bugs with minimal verified changes, running CodeTrust-style audits plus repo-native checks, validating frontend-app or Go backend changes, updating/committing local main only when explicitly requested, or creating/updating repo-local skills."
---

# Super Dolphin Workflow

## Overview

This skill captures the user's normal working rhythm for the Super-Dolphin checkout. Use it to keep changes small, grounded in source truth, and backed by fresh verification.

Start by stating the current assumption and scope in one short sentence. If the user says `请你根据计划执行` or `PLEASE IMPLEMENT THIS PLAN`, treat the plan as approved and execute within its stated scope. If the user says `请你根据问题去修改解决bug`, ship the smallest verified bug fix instead of a broad rewrite.

## First Pass

Run these checks before editing:

```bash
git status --short
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md$)'
```

Preserve unrelated dirty work. Do not revert, stage, format, or rename files you did not need to touch.

For path discovery, load context in this order:

1. `README.md`
2. `docs/doc/codemap/README.md`
3. One relevant `docs/doc/codemap/*.md` volume
4. Targeted `rg` searches against source files
5. Exact source files and same-package tests

For behavior questions, prefer source code and tests first, then accepted decisions in `docs/decisions/*.md` or `docs/adr/*.md`, then contracts in `docs/契约/*.md`. Treat `docs/plans/**`, `docs/迁移/**`, and old reports as historical unless the user asks for migration history.

## Route Common Requests

For "检查我工作情况" or similar status checks:

- Inspect `git status --short`, `git diff --stat`, recent commits, and relevant untracked reports.
- Report what is current, what appears unfinished, and what should not be touched.
- Use fresh screen/history evidence only when that capability is available and current; otherwise say it is unavailable and rely on repository evidence.

For bug fixes:

- Reproduce or identify the failing path when practical.
- Add or update a focused regression test for non-trivial fixes.
- Make the smallest source change that satisfies the test.
- Run focused verification first, then broader checks if the changed surface requires it.

For approved implementation plans:

- Review the plan for blockers before editing.
- Execute the plan steps in order.
- Stop on unclear destructive actions, data migration choices, or repeated verification failure.

For `使用codetrust`:

- Produce the requested concrete test/audit document first.
- Use CodeTrust as frontend/JS/React evidence when available.
- Do not treat CodeTrust `HIGH_TRUST` with `filesScanned=0` as backend evidence.
- Prove Go/backend coverage with repo-native commands such as `gofmt`, `go vet`, and `./scripts/test_with_guard.sh`.

For "怎么实现的" questions:

- Answer from source/protocol truth, not guesses.
- Give file/function references and a directly reusable implementation skeleton when useful.
- Prefer Chinese when the user asks in Chinese.

## Frequent Paths

Current React UI lives in `frontend-app`. `cmd/agent-terminal/web-dist` is only the generated Go embed asset directory synced from `frontend-app/dist`.

Common frontend paths:

- Chat/thread flow: `frontend-app/src/pages/chat/ChatPage.jsx`
- Client state: `frontend-app/src/entities/client/model/useClientStore.js`
- Backend bridge: `frontend-app/src/shared/api/backendApi.js`
- Wails bridge: `frontend-app/src/shared/api/wailsBridge.js`
- Theme contract: `frontend-app/src/styles.css` and `frontend-app/src/styles.test.js`

Common Go/backend paths:

- App assembly: `internal/app`
- Business modules: `internal/module`
- Provider adapters: `internal/provider`
- Persistence: `internal/store`
- SQL queries: `sql/queries`

Skill runtime truth:

- Canonical repo-local skills live under `.agent/skills`.
- `.agents/skills` and `.claude/skills` are generated provider mirrors, not the normal edit target.
- Runtime skill behavior usually lives under `internal/module/skill*`, `internal/provider/shared/provider_home.go`, provider mirror tests, and toolbridge compatibility tests.

## Verification Matrix

Choose verification by changed surface:

```bash
# Go package changes
./scripts/test_with_guard.sh <affected packages> -count=1

# Guard-only fast check
make guard

# Broad Go changes
make test
make build-plain

# Current React UI changes
cd frontend-app
npm run lint
npm test
npm run build

# SQL/store changes
make sqlc-verify

# Code-map changes
make codemap-check
```

For UI-visible changes, also verify in a browser when the route and dev server are available. Default Vite port `5175` may be busy; `5176` or `5177` are reasonable fallbacks.

Do not claim done, fixed, ready to commit, or ready to merge without fresh command output or explicit manual verification evidence.

## Git Operations

The user often wants operational actions performed, not described.

- If asked to start a script, update `main`, commit, push, or delete a tool, perform the action and verify it.
- If asked to commit local work to `main`, use local `main` as requested; do not invent a feature-branch workflow.
- Commit only when explicitly asked.
- Stage only owned files; never use `git add .`.
- Before and after Git operations, run `git status --short`.

For dirty local `main` updates, use a stash-based fast-forward flow:

```bash
git status --short
git stash push -u -m '<short reason>'
git fetch origin main
git merge --ff-only origin/main
git stash pop
git status --short
```

Resolve conflicts only within the requested scope. Report unrelated dirty files separately.

## Finish

Before the final response:

- Review `git diff --stat` and the exact diff for owned files.
- Check for temporary/generated files, local paths, secrets, unused imports, and accidental broad formatting.
- Summarize what changed, what verification ran, and any skipped checks or remaining risks.
