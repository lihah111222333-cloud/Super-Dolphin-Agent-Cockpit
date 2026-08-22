# LSP Prompt Notes & Semantic Guardrails (English)

The canonical shared agent guidance is [`shared-developer-instructions.md`](../../shared-developer-instructions.md) and the 8.23 LSP Review & Optimization Plan [`docs/8.23-lsp/LSP评测深度复盘与纯Prompt优化方案.md`](../8.23-lsp/LSP评测深度复盘与纯Prompt优化方案.md).

---

## 1. Tool Surface & Weapon Matrix

The public MCP-LSP surface exposes only three pure semantic tools:

- `structure`: Locate AST symbol definitions, workspace symbols, document symbol outlines, folding ranges, and semantic tokens.
- `xref`: Trace references (`references`), call hierarchies (`call_hierarchy`), and type hierarchies (`type_hierarchy`) across files.
- `diagnostics`: Request immediate syntax, type, and language server diagnostics for modified files.

> **Tool Non-existence Rule**: Tools such as `file`, `read_file`, `inspect`, `grep`, `edit`, `patch_edit`, or `completion` DO NOT exist in LSP.
> - File inspection: Native shell commands (`cat`, `head`, `sed`).
> - Plain text/regex/config/error search: Native `grep` / `rg`.
> - Code editing: Native `apply_patch`.
> - AST definition location: `structure`.
> - Cross-file caller tracing: `xref`.
> - Diff-scoped syntax/type checks: `diagnostics`.

---

## 2. Parameter Contracts & Line Protocol

All three tools accept closed JSON objects; unknown fields are rejected.

- `structure`:
  * `{"action": "workspace_symbol", "query": "SymbolName", "workspace_language": "python"}`
  * `{"action": "document_symbol", "file_path": "relative/path.py"}`
- `xref`:
  * `{"action": "references", "pos": "relative/path.py:line:column"}`
  * `{"action": "call_hierarchy", "pos": "relative/path.py:line:column", "direction": "incoming"}`
  * `{"action": "type_hierarchy", "pos": "relative/path.py:line:column", "direction": "supertypes"}`
- `diagnostics`:
  * `{"file_path": "relative/changed.py"}` or `{"file_paths": ["file1.py", "file2.py"]}`
- Return Protocol: Pure text Line-Protocol (`OK total=... / ROW file=... line=... col=... severity=... message=...`).

---

## 3. Four Core Semantic Guardrails

1. **Downstream Caller Guard**: When modifying shared base classes, collectors (e.g. deletion handlers), model fields, or AST visitor dispatchers, run `xref(action="references", pos="...")` to verify downstream consumers. Do NOT trace generic/dunder methods (e.g. `__init__`, `save`) globally without file scope.
2. **Polymorphic Disambiguation**: In large multi-module repositories with deep inheritance and polymorphic symbols, avoid flooding context with massive text grep. Use `structure(action="workspace_symbol", query="<symbol>", workspace_language="python")` to pinpoint exact class definitions and AST hierarchies.
3. **Diagnostics Line-Protocol Focus**: Extract `file` and `line` from each `ROW` record and strictly focus on errors intersecting with your modified patch hunks. Untouched legacy warnings must not block progress, but the patch must introduce zero syntax or type regressions.
4. **Slice & Boundary Inline Verification**: When implementing string manipulation, regex slicing (`[a:b]`), or AST filters, execute a minimal 1-line python inline snippet (`python -c "..."`) against the exact sample string from the issue description to verify boundary indices before editing.

---

## 4. Hard Test Gate & 5-Step Verification Loop

1. **Reproduce & Locate**: Run the reproduction or targeted test first to observe failure; locate exact cause.
2. **Slice & Boundary Inline Verification**: Run minimal inline snippet to verify boundary indices.
3. **Edit**: Apply minimal necessary changes with `apply_patch`.
4. **Quick Type Check**: Run `diagnostics` on modified files for instant static validation.
5. **Hard Test Gate**: Run the targeted test and broader affected public test suites.
   - **MANDATORY**: The targeted test must PASS (`0 failures, 0 errors, exit=0`).
   - **NEVER** submit a patch if public tests are failing (`exit != 0`). Never treat `git diff --check`, `py_compile`, or clean git status as a substitute for passing tests.
