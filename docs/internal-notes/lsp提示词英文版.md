# LSP Toolchain Guide

This document is required reading before using LSP tools. Use LSP tools for code understanding, symbol navigation, reference analysis, diagnostics, and structured edits. Use ordinary commands for shell work, git, filesystem inspection, package scripts, and tests.

## Core Tools

- `grep(text_search, glob=...)`: text or regex search with path filters.
- `grep(ast_search, language=...)`: syntax-aware search for functions, types, and expressions.
- `structure(document_symbol | workspace_symbol)`: file outline or project-wide symbol lookup.
- `inspect(definition | implementation | hover | signature_help | type_definition)`: navigate or inspect symbols from `file:line:col`.
- `xref(references, verbosity="compact")`: reference scan; results may include `func_start` and `func_end`.
- `xref(call_hierarchy, direction="incoming|outgoing|both")`: call-chain analysis.
- `file(read_file, pos=<file>:<line>, limit=<n>)`: paged file reads using 1-based line numbers.
- `file(diagnostics, file_paths=[...])`: batched compile or type diagnostics.
- `edit(file_path=..., patch="*** Begin Patch...")`: apply-patch style edits with LSP synchronization.
- `completion(pos=<file>:<line>:<col>)`: code completion.

## Recommended Workflow

Review tasks:

```text
grep locate -> inspect understand -> xref impact -> file(read_file) study -> conclude
```

Fix tasks:

```text
grep locate -> xref impact -> file(read_file) context -> edit apply -> file(diagnostics) check -> run relevant tests
```

## Required Contracts

- When you know a line number, read context with `file(read_file, pos=<file>:<line>, limit=<n>)`.
- When `ast_search`, `definition`, `implementation`, or `references` returns `func_start` / `func_end`, read the function with `file(read_file, pos=<file>:<func_start>, limit=<func_end-func_start+1>)`.
- Before changing a shared symbol, inspect impact with `xref(references)` or call hierarchy.
- After multi-file edits, prefer one batched `file(diagnostics, file_paths=[...])` before running tests.
- Diagnostics do not replace tests; behavior changes require relevant test execution.

## Prohibitions

- Do not rely only on the `grep + file` pair for complex code review.
- Do not replace symbol or impact analysis with plain text search when LSP tools are appropriate.
- Do not edit before reading the surrounding context.
- Do not claim a fix is complete before diagnostics or tests have been run.
