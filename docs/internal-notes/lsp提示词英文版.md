# LSP Toolchain Guide — required reading for all agents

You have 11 repository-aware tools covering four action classes (search, understand, edit, verify). Do not default to the `lsp_grep + lsp_file` two-tool combo, and do not wrap `code_run` around shell commands that a dedicated tool already does.

### Tools and primary actions

- `lsp_grep(text_search, glob=...)` — text / regex search, path-filterable
- `lsp_grep(ast_search, language=...)` — match functions, types, and expressions by syntax shape
- `lsp_structure(document_symbol | workspace_symbol)` — file outline / project-wide symbol lookup
- `lsp_inspect(definition | implementation)` — from `file:line:col`, jump to definition / all implementations
- `lsp_xref(references, verbosity="compact")` — reference scan; each hit carries `func_start / func_end`
- `lsp_xref(call_hierarchy, direction="incoming")` — who calls this function
- `lsp_file(read_file, offset=, limit=)` — paged read with 1-based line numbers
- `lsp_file(diagnostics, file_paths=[...])` — batch compile / type diagnostics
- `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new" | edits=[{old_string,new_string}, ...])` — precise edits
- `lsp_completion` — code completion
- `code_run / code_run_test` — real shell, build, scripts, Go test functions

### Mandatory workflow

```
Review: grep locate  → inspect read   → xref impact → read_file study   → conclude
Fix:    grep locate  → xref impact    → read_file context → edit apply  → diagnostics check → code_run_test / code_run verify
```

### Five combos

- **A**: `ast_search` → use the returned `func_start / func_end` with `read_file(offset, limit)` for precise reads
- **B**: `workspace_symbol` → `definition` → `read_file` — three-step locate
- **C**: `references` + `call_hierarchy(incoming + outgoing)` — full impact view
- **D**: `definition` → `implementation` → `references` — interface three-step
- **E**: two `document_symbol` calls to diff method coverage between two files

### func_start / func_end shortcut

`lsp_grep(ast_search)`, `lsp_inspect(definition|implementation)`, and `lsp_xref(references, verbosity="compact")` all return `func_start / func_end`. Call `read_file(offset=func_start, limit=func_end-func_start+1)` directly to read the full function — no need to run `document_symbol` first.

### Parallel and batch

- Fire independent read-only calls (multiple `definition / references / read_file`) in parallel; serialize only when one depends on another
- After multi-file edits, run `diagnostics(file_paths=[...])` once instead of per file

### Prohibitions

1. Do not default to the `lsp_grep + lsp_file` two-tool combo
2. Do not invoke `code_run` to run `grep / rg / cat / head / tail / sed / awk / find / ls` or similar shell commands that a dedicated tool already covers
3. Do not modify code without first running `xref` for impact
4. Do not claim verification passed without running `diagnostics` — `diagnostics` only catches compile / type errors; runtime behavior must be exercised with actual tests
5. Each task must combine at least four LSP tools

## Advanced debugging

### Call-chain tracing

- `lsp_xref(call_hierarchy, direction="incoming")` — who calls this function (walk back to triggers)
- `lsp_xref(call_hierarchy, direction="outgoing")` — what this function calls (trace internal flow)
- `lsp_xref(call_hierarchy, direction="both")` — both directions in one shot, useful for hub functions
- Pattern: `inspect(definition)` to reach the target → `call_hierarchy(incoming)` for entry points → `outgoing` for internal dependencies — a full data-flow picture

### Type hierarchy (interfaces / inheritance)

- `lsp_xref(type_hierarchy, direction="supertypes")` — parent classes / implemented interfaces
- `lsp_xref(type_hierarchy, direction="subtypes")` — all implementations / subclasses
- Pattern: `inspect(type_definition)` to jump to the type → `type_hierarchy(subtypes)` to enumerate implementations → `document_symbol` on each to decide which implementation to change

### Lightweight context (without reading full code)

- `lsp_inspect(hover)` — type, docs, and signature summary; quick confirmation of parameter or return types
- `lsp_inspect(signature_help)` — parameter hints at a call site; useful for diagnosing argument order / type mismatches
- `lsp_inspect(type_definition)` — jump to the "type" of a symbol (`definition` jumps to the symbol itself)

### Diagnostics strategy

- Scope diagnostics with `file_paths=[...]` to the files you care about, avoiding cross-file noise on large repos
- Handle by severity: error first, then warning, then hint / info as appropriate
- Diagnostics do not replace tests — if runtime behavior changed, run the relevant tests

### Edit strategy (picking the right action saves a lot of churn)

- Rename a symbol → `lsp_edit(rename)`: LSP renames safely across files; do not search-and-replace with `replace_range`
- Multiple edits in one file → `lsp_edit(replace_range, edits=[{old_string,new_string}, ...])` batched in one call
- Single in-place edit → `lsp_edit(replace_range, patch="@@ ctx\n-old\n+new")`; the patch form carries context and is safer
- Semantic fix (missing import, simple quickfix) → `lsp_edit(code_action, only=["quickfix"])`
- Format after edits → `lsp_edit(format)`

### Structure helpers

- `lsp_structure(folding_range)` — folding regions for understanding nested structure
- `lsp_structure(semantic_tokens)` — semantic highlight tokens; rarely needed

### Execution and testing

- `code_run(project_cmd)` — real shell / build / scripts
- `code_run_test(test_func, test_pkg)` — run a single Go test function; faster than whole-package `go test`

### Advanced combinations (beyond basic A–E)

- Full data-flow: `call_hierarchy(both)` collects both directions in one call, saving a round compared to separate incoming / outgoing runs
- Root-cause hunt: `ast_search` (shape) + `diagnostics` (compile error) + `call_hierarchy(incoming)` (trigger path)

### Picking a tool

- Know `file:line:col` → inspect / xref family (pinpoint operations)
- Only have a keyword or pattern → grep family (scan)
- Need cross-file structure → structure family (outline)
- About to modify → edit family (structural edits)
- Need to verify → `file.diagnostics` + `code_run_test`
