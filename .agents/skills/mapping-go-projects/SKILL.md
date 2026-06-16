---
name: mapping-go-projects
description: Use when locating, indexing, refreshing, or querying Go project maps; searching Go functions, methods, structs, interfaces, packages, imports, symbols, ownership, or entry points; or when commit, merge, release, CI, or guard workflows need an AI-readable source index.
---

# Mapping Go Projects

## Overview

Use a generated, local-only project map as the first index for Go code navigation. The map is derived from Go AST syntax, optimized for AI lookup, and never treated as the source of truth.

The source of truth is still the Go source file. The map only narrows the search to the likely package, symbol, file, and line.

## Industry Baseline

This repository uses a lightweight local map because it fits AI sessions and commit hooks. It borrows from established practices:

- Go standard AST parsing for reliable declarations.
- Language Server Protocol style symbol lookup for package/function/type navigation.
- ctags-style flat symbol indexes for fast grep.
- Sourcegraph SCIP/LSIF-style machine-readable code intelligence, but without committing heavyweight indexes.
- Architecture package maps and ADRs for ownership, with executable guards enforcing drift-sensitive rules.

Read `references/project-map-standards.md` before replacing this with a heavier indexing system.

## Mandatory Lookup Rule

When asked to find, explain, edit, review, or trace a Go package, function, method, struct, interface, const, var, or import:

1. If `.project-map/project-map.json` is missing or older than Go source files, run:

   ```bash
   make project-map
   ```

2. Query the map before broad `rg` searches:

   ```bash
   python3 .agents/skills/mapping-go-projects/scripts/query_project_map.py --root . UserService
   ```

3. Open the referenced source file and line before making claims or edits.
4. If the map has no hit, fall back to `rg`, then refresh the map after relevant code is added.

## Generated Files

The map is generated under `.project-map/` at the repository root:

- `.project-map/PROJECT_MAP.md`: human-readable package and symbol overview.
- `.project-map/project-map.json`: machine-readable full index.
- `.project-map/symbols.tsv`: fast grep index for functions, methods, structs, interfaces, vars, and consts.
- `.project-map/packages.tsv`: package ownership and file index.
- `.project-map/imports.tsv`: import edges by package and file.

`.project-map/` must stay in `.gitignore`. Do not stage or commit it.

## Quick Commands

| Task | Command |
| --- | --- |
| Generate map | `make project-map` |
| Query any symbol | `python3 .agents/skills/mapping-go-projects/scripts/query_project_map.py --root . Name` |
| Query only structs | `python3 .agents/skills/mapping-go-projects/scripts/query_project_map.py --root . --kind struct Name` |
| Exact symbol lookup | `python3 .agents/skills/mapping-go-projects/scripts/query_project_map.py --root . --exact Name` |
| Raw grep index | `rg -n 'Name' .project-map/symbols.tsv` |

## Lifecycle Rules

- `guard-commit` generates the map before commit completion.
- `guard-release` also generates the map because release includes commit guard.
- `.githooks/pre-commit` blocks staged `.project-map/` files and runs `guard-commit` for Go project changes.
- `.githooks/pre-merge-commit` runs `guard-release` for merge commits.
- `.githooks/post-merge` refreshes `.project-map/` after merge or pull operations.
- CI runs `guard-release` on pull requests and `main` pushes, so merge validation refreshes the map in the CI workspace too.

Git cannot block every fast-forward merge with a local pre-merge hook, so the durable merge gate is CI plus branch commit guards.

## Common Mistakes

- Do not use the map as proof that code compiles or behavior is correct. Run the Go guards.
- Do not edit generated map files by hand. Regenerate them.
- Do not commit `.project-map/`; it contains local paths and can be regenerated.
- Do not skip source inspection after a map hit. The map indexes declarations, not all call paths or runtime behavior.
- Do not expect full type resolution. This is a syntactic AST index; use `gopls`, `go list`, or tests for semantic questions.

## Companion Skills

- Use `designing-go-architecture` when package ownership or dependency direction is part of the question.
- Use `writing-go-code` when editing Go code found through the map.
- Use `guarding-go-projects` after Go code edits and before commit, merge, or release claims.
