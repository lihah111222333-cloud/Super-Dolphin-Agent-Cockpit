# Super-Dolphin Docs

This directory separates current project references from historical planning
material and archived agent output. Use this file as the default entry point
before scanning large documentation folders.

## Current Sources

- `../README.md`: repository layout, entry points, and local development flow.
- `doc/codemap/README.md`: generated code-map table of contents and reading
  boundaries.
- `decisions/` and `adr/`: accepted architecture decisions.
- `契约/`: long-lived engineering conventions for framework, runtime, store,
  RPC, MCP, and module boundaries.
- `internal-notes/`: mandatory local workflow notes, including LSP tool-chain
  guidance.

## Historical Material

- `plans/`: historical execution plans. These files can explain why earlier
  work happened, but they are not current implementation truth.
- `superpowers/plans/`: historical Superpowers implementation plans.
- `archive/`: old reports, agent notes, generated analysis, review logs, and
  evidence moved out of the default reading path.

When current behavior and historical documents disagree, trust source code,
tests, accepted ADRs, and active contract docs first.

## Reading Order

1. Read `../README.md` for the active project shape.
2. Read `doc/codemap/README.md`, then one relevant code-map volume.
3. Read source code and same-package tests for the behavior in question.
4. Use `decisions/`, `adr/`, and `契约/` for accepted constraints.
5. Open `plans/`, `superpowers/plans/`, or `archive/` only for history,
   migration context, or provenance.
