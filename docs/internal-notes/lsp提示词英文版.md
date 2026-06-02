# LSP Toolchain Guide — required reading for all agents

You have 7 repository-aware LSP tools covering search, understand, edit, and diagnostics. Use the current short tool names: `file`, `grep`, `inspect`, `xref`, `structure`, `edit`, and `completion`. Use `exec_command` by default for ordinary shell, git, filesystem inspection, package scripts, and tests; prefer LSP for code understanding, symbol navigation, references / call chains, diagnostics, and edits. Do not default to the `grep + file` two-tool combo, and do not use ordinary shell work in place of dedicated tools.

### Tools and primary actions

- `grep(text_search, glob=...)` — text / regex search, path-filterable
- `grep(ast_search, language=...)` — match functions, types, and expressions by syntax shape
- `structure(document_symbol | workspace_symbol)` — file outline / project-wide symbol lookup
- `inspect(definition | implementation)` — from `file:line:col`, jump to definition / all implementations
- `xref(references, verbosity="compact")` — reference scan; each hit carries `func_start / func_end`
- `xref(call_hierarchy, direction="incoming")` — who calls this function
- `file(read_file, pos=<file>:<line>, limit=)` — paged read with 1-based line numbers
- `file(diagnostics, file_paths=[...])` — batch compile / type diagnostics
- `edit(file_path=..., patch="*** Begin Patch...")` — apply-patch style disk edits with LSP sync
- `completion(pos=...)` — code completion
- `exec_command` — ordinary shell, git, package scripts, tests

### Mandatory workflow

```
Review: grep locate  → inspect read   → xref impact → file(read_file) study   → conclude
Fix:    grep locate  → xref impact    → file(read_file) context → edit apply  → file(diagnostics) check → exec_command verify
```

### Five combos

- **A**: `ast_search` → use the returned `func_start / func_end` with `file(read_file, pos=<file>:<func_start>, limit)` for precise reads
- **B**: `workspace_symbol` → `definition` → `file(read_file)` — three-step locate
- **C**: `references` + `call_hierarchy(incoming + outgoing)` — full impact view
- **D**: `definition` → `implementation` → `references` — interface three-step
- **E**: two `document_symbol` calls to diff method coverage between two files

### func_start / func_end shortcut

`grep(ast_search)`, `inspect(definition|implementation)`, and `xref(references, verbosity="compact")` all return `func_start / func_end`. Call `file(read_file, pos=<file>:<func_start>, limit=func_end-func_start+1)` directly to read the full function; no need to run `document_symbol` first.

### Parallel and batch

- Fire independent read-only calls (multiple `definition / references / read_file`) in parallel; serialize only when one depends on another
- After multi-file edits, run `file(diagnostics, file_paths=[...])` once instead of per file

### Prohibitions

1. Do not default to the `grep + file` two-tool combo
2. Do not use ordinary shell to run `grep / rg / cat / head / tail / sed / awk / find / ls` or similar commands that a dedicated tool already covers
3. Do not modify code without first running `xref` for impact
4. Do not claim verification passed without running `file(diagnostics)` — diagnostics only catch compile / type errors; runtime behavior must be exercised with actual tests
5. Complex cross-file edits should combine multiple LSP tools; simple tasks do not need tool-count padding

## Advanced debugging

### Call-chain tracing

- `xref(call_hierarchy, direction="incoming")` — who calls this function (walk back to triggers)
- `xref(call_hierarchy, direction="outgoing")` — what this function calls (trace internal flow)
- `xref(call_hierarchy, direction="both")` — both directions in one shot, useful for hub functions
- Pattern: `inspect(definition)` to reach the target → `call_hierarchy(incoming)` for entry points → `outgoing` for internal dependencies — a full data-flow picture

### Type hierarchy (interfaces / inheritance)

- `xref(type_hierarchy, direction="supertypes")` — parent classes / implemented interfaces
- `xref(type_hierarchy, direction="subtypes")` — all implementations / subclasses
- Pattern: `inspect(type_definition)` to jump to the type → `type_hierarchy(subtypes)` to enumerate implementations → `document_symbol` on each to decide which implementation to change

### Lightweight context (without reading full code)

- `inspect(hover)` — type, docs, and signature summary; quick confirmation of parameter or return types
- `inspect(signature_help)` — parameter hints at a call site; useful for diagnosing argument order / type mismatches
- `inspect(type_definition)` — jump to the "type" of a symbol (`definition` jumps to the symbol itself)

### Diagnostics strategy

- Scope diagnostics with `file_paths=[...]` to the files you care about, avoiding cross-file noise on large repos
- Handle by severity: error first, then warning, then hint / info as appropriate
- Diagnostics do not replace tests — if runtime behavior changed, run the relevant tests

### Edit strategy (picking the right action saves a lot of churn)

- Rename a symbol → use `xref(references)` first, then apply small per-file `edit(file_path, patch)` changes
- Multiple edits in one file → submit one contextual multi-hunk `edit(file_path, patch)`
- Single in-place edit → use `edit(file_path, patch="*** Begin Patch...")`; the patch form carries context and is safer
- Semantic fixes and formatting → semantic-action and formatting operations are not exposed in the current tool surface; use `file(diagnostics)` plus tests after edits

### Structure helpers

- `structure(folding_range)` — folding regions for understanding nested structure
- `structure(semantic_tokens)` — semantic highlight tokens; rarely needed

### Execution and testing

- `exec_command` — ordinary shell, git, package scripts, tests

### Advanced combinations (beyond basic A–E)

- Full data-flow: `call_hierarchy(both)` collects both directions in one call, saving a round compared to separate incoming / outgoing runs
- Root-cause hunt: `ast_search` (shape) + `diagnostics` (compile error) + `call_hierarchy(incoming)` (trigger path)

### Picking a tool

- Know `file:line:col` → inspect / xref family (pinpoint operations)
- Only have a keyword or pattern → grep family (scan)
- Need cross-file structure → structure family (outline)
- About to modify → edit family (structural edits)
- Need to verify → `file(diagnostics)` + `exec_command`
