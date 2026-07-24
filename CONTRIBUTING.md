# Contributing to Super Dolphin Agent

Thank you for helping improve Super Dolphin Agent. This repository is maintained
with AI agents, compiler-backed guards, generated maps, and human review. A
contribution is ready for review only when its scope, evidence, and remaining
risk are explicit.

## Before You Start

- Read the [Code of Conduct](CODE_OF_CONDUCT.md).
- Use [Security Policy](SECURITY.md) for vulnerabilities. Never disclose a
  vulnerability, credential, user data, or private log in a public issue.
- Search existing issues before opening a new one.
- Keep one logical task in one branch and one pull request.

## Development Setup

The current development baseline is Go 1.25.7 and Node.js matching `^20.19.0 || ^22.13.0 || >=24`. The
desktop provider flow also requires the provider and language-server tools
listed in the [README](README.md#quick-start).

Fork the repository on GitHub, clone the fork using the URL GitHub provides,
then register the canonical repository as `upstream`:

These GitHub steps apply after the canonical repository is published. Before
publication, existing maintainers must use only their current authorized
checkout and remotes.

```bash
git remote add upstream https://github.com/lihah111222333-cloud/super-dolphin-agent.git
git fetch upstream
git switch main
git merge --ff-only upstream/main
make install-hooks
( cd frontend-app && npm ci )
```

Create a focused branch before editing. This is an example, not a required
branch-naming scheme:

```bash
git switch -c docs/community-guides
```

When working on multiple tasks concurrently, isolate them in separate Git
worktrees. For example:

```bash
git worktree add ../super-dolphin-agent-docs -b docs/community-guides main
```

### Worktree and LSP Readiness

An AI agent working in a linked worktree must use the LSP peer built from that
same checkout. From the linked worktree, run:

```bash
make codex-worktree-ready
go run ./cmd/codex-worktree-setup ready
go run ./cmd/codex-worktree-setup verify
```

`ready` writes only worktree-local ignored configuration and binaries. After it
passes, start a new Codex task so the task loads that worktree's MCP/LSP peer.
Do not reuse a binary, configuration, definition, diagnostic, or edit result
from another checkout.

Do not mix unrelated tasks, formatting sweeps, dependency upgrades, or generated
artifact churn into the same branch.

## Change Discipline

1. Reproduce a defect before changing production behavior.
2. Add a regression test or equivalent executable evidence for a non-trivial
   fix.
3. Make the narrowest change that addresses the demonstrated cause.
4. Fail fast on invalid configuration, missing dependencies, and malformed
   data. Do not hide errors with defaults, empty values, or silent catches.
5. Preserve contract, module, provider, runtime, store, and frontend boundaries.
6. Refresh generated files through their owning generator; do not hand-edit a
   generated result to make a drift check green.
7. Keep credentials, local databases, logs, provider homes, user memory, and
   machine-specific paths out of commits and issue attachments.

The [code map](docs/doc/codemap/README.md) is the fastest orientation entry point.
Current engineering contracts are indexed in
[docs/契约/README.md](docs/%E5%A5%91%E7%BA%A6/README.md).

## Verification

Run the checks that match the changed surface. Record the exact commands and
results in the pull request.

| Change surface | Required verification |
| --- | --- |
| Documentation only | `git diff --check` |
| Focused Go package | `./scripts/test_with_guard.sh ./internal/archtest -count=1` is a concrete example; pass the packages actually changed |
| Architecture rules or guard baselines | `make guard` |
| Broad Go or cross-layer change | `make test` and `make build-plain` |
| React frontend | `cd frontend-app && npm run lint && npm test && npm run build` |
| SQL or store generation | `make sqlc-verify` plus guarded tests for the affected Go packages |
| Code map | `make codemap-check` |
| Project map | `make project-map-check` |
| Capability contract | `make capcontract-check` |

Do not change a baseline, freeze file, or generated output merely to suppress a
failure. Explain any intentional baseline change in the commit and pull request.

## Commit Requirements

The repository commit-message and pre-push hooks enforce these rules:

- Every commit title must contain Chinese text.
- A non-empty commit body must also contain Chinese text. An empty body is
  allowed.
- A commit classified as `fix`, `hotfix`, `bugfix`, or `修复` must include a
  bug-locking test, fixture, golden file, or snapshot in the same commit.
- Each commit must have one logical intent and leave the repository in a
  verifiable state.
- Do not use `--no-verify` to turn a failed hook into a successful claim.

An accepted title shape is:

```text
docs(community): 补齐开源协作文档
```

Install the hooks with `make install-hooks`. The pre-push hook repeats the title
and fix-test checks, so bypassing the commit-message hook does not make an
invalid commit acceptable.

## Pull Requests

A reviewable pull request:

- explains the problem and the intended outcome;
- lists the files and behavior intentionally changed;
- includes reproduction or acceptance evidence;
- gives exact verification commands and results;
- describes architecture, generated-artifact, security, compatibility, and
  rollback risk;
- separates existing failures from failures introduced by the change; and
- contains no secrets, private traces, user data, or machine-specific paths.

Use the repository pull request template and keep every checkbox factual. A
passing rerun is not proof that an earlier failure did not happen; retain and
explain relevant failure evidence.

## License

The project is licensed under the [Apache License 2.0](LICENSE). Do not remove or
misattribute third-party license notices from vendored source directories.
