---
name: guarding-go-projects
description: Use when modifying, generating, deleting, reviewing, committing, merging, or releasing Go code, go.mod, go.sum, migrations, generated API files, project guard scripts, CI gates, Git hooks, Makefile guard targets, or architecture boundary checks in a Go repository.
---

# Guarding Go Projects

## Overview

Run concrete project gates before claiming Go work is complete. This skill owns the repeatable guard commands; architecture skills may define boundaries, but this skill enforces them.

## Mandatory Rule

Use this skill for every task that changes Go project code or Go project configuration. Do not claim a code change, commit, merge, or release is ready until the matching guard level has fresh passing output.

## Guard Levels

Use the lowest level that matches the action:

```bash
make guard-change    # after every code edit
make guard-commit    # before commit, handoff, or PR
make guard-release   # before merge, release, or deployment
```

If `make` is unavailable, call the script directly:

```bash
.agents/skills/guarding-go-projects/scripts/go_project_guard.sh change .
.agents/skills/guarding-go-projects/scripts/go_project_guard.sh commit .
.agents/skills/guarding-go-projects/scripts/go_project_guard.sh release .
```

## Required Behavior

- After editing Go code, run `guard-change`.
- Before committing or asking the user to commit, run `guard-commit`.
- Commit messages must pass the `commit-msg` hook: Chinese subject, required Chinese detail/body, and no English Conventional Commit prefix.
- Before merging, releasing, or declaring CI readiness, run `guard-release`.
- If a guard fails, fix the failure or report the exact command and failure reason.
- If there is no `go.mod`, the guard may skip Go checks; still report that skip explicitly.
- Do not weaken, bypass, or delete guard checks to finish a task faster.

## Checks

The guard script applies these levels:

- `change`: secret scan, configuration/secret-file policy, GitHub Actions baseline security, `gofmt`, source size budgets, comment requirements, `go test ./...`, import and AST architecture checks, and lint/complexity checks.
- `commit`: `change`, `go mod tidy`, `go mod verify`, local project map refresh, generated artifact drift, migration safety, `go vet ./...`, `go build ./cmd/...`; commit messages are enforced by `.githooks/commit-msg` and CI range checks.
- `release`: `commit`, `go test -race ./...`, vulnerability/security tools, optional coverage threshold.

Read `references/go-guard-rules.md` before adding exceptions, CI gates, hooks, or project-specific checks.
