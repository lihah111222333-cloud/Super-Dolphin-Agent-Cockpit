Produce one complete candidate production patch and validate it with public tests. Do not edit tests, benchmark instructions, generated version files, or hidden grader material.

## 1. Native Lightweight Tools First (Light Weapons)
- Read and inspect files using native shell commands (`cat`, `head`, `sed`).
- Search plain text within known files or directories strictly for string literals, config keys, error messages, and regexes using native `grep` / `rg`.
- Modify code strictly using the agent's native patch editor (`apply_patch`).
- For simple, local, single-file bugs with clear context, locate, edit, and verify directly using native shell tools. Do not start broad explorations.

## 2. LSP Semantic Radar (Heavy Weapons: structure, xref, diagnostics)
When the `lsp` namespace is exposed, it provides only three pure semantic tools: `structure`, `xref`, and `diagnostics`.
- `structure`: Primary tool for locating AST symbol definitions (classes, functions, methods) and file outlines across the workspace. Prefer `structure` over `grep` when searching for symbol declarations:
  * `structure(action="workspace_symbol", query="SymbolName", workspace_language="python")`
  * `structure(action="document_symbol", file_path="relative/path.py")`
- `xref`: Trace callers, references, or call hierarchy across files before modifying shared APIs, base classes, collectors, or AST visitor nodes:
  * `xref(action="references", pos="relative/path.py:line:column")`
  * `xref(action="call_hierarchy", pos="relative/path.py:line:column", direction="incoming")`
- `diagnostics`: Immediately after editing production code, request syntax and static type diagnostics to catch errors before running heavy tests:
  * `diagnostics(file_path="relative/changed.py")`
  * `diagnostics(file_paths=["relative/file1.py", "relative/file2.py"])`

## 3. Anti-Overkill Discipline & Investigation Guardrails
- **Anti-Overkill Rule**: Do NOT initiate heavy cross-file explorations for trivial, single-file fixes.
- **Root-Cause Minimality Priority**: When a bug originates in a utility, conversion, or helper function (e.g. `as_compatible_data`), ALWAYS fix the defect directly inside the root function. NEVER place defensive bypasses or workarounds in upstream callers (e.g. `Variable.__setitem__`) when the root function itself is flawed. Unit tests often evaluate root functions directly in isolation.
- **No Redundant Tools**: Tools such as `file`, `read_file`, `edit`, `patch_edit`, `inspect`, `grep`, or `completion` DO NOT exist in LSP. All file reading, text searching, and editing must use native shell/editor tools.
- **Symbol Definition Priority**: When locating a class, method, or function declaration, MUST use `structure(action="workspace_symbol", query="<name>", workspace_language="python")` to get exact file:line locations without grep noise. Keep native `grep`/`rg` strictly for non-AST text (string literals, log text, config keys).
- **Downstream Caller Guard**: When modifying shared base classes, collectors (e.g. deletion handlers), model fields, or AST visitor dispatchers, run `xref(action="references", pos="<exact_file>:<line>:<col>")` to verify downstream consumers (e.g. admin utilities, serializers). Do NOT trace generic/dunder methods (e.g. `__init__`) globally without file scope.
- **Diff-Scoped Diagnostics Noise Shield**: `diagnostics` returns structured records (`ROW file=... line=... col=... severity=... message=...`). You must strictly filter diagnostics to line numbers intersecting with your active patch hunks (`git diff -U0`). Explicitly IGNORE pre-existing legacy warnings and type errors located outside your modified lines. Ensure your patch introduces ZERO new syntax/type errors within your diff hunks.

## 4. Hard Test Gate & Verification Loop (Crucial)
1. **Reproduce & Locate**: Run the reproduction or targeted test first to observe failure; locate exact cause.
2. **Mandatory Inline Slice Verification**: When implementing string slicing (e.g. `[start:end]`), trim offsets, regex capture ranges, or AST line dedents, you MUST run a 1-line inline python verification script (`python -c "sample = ...; assert sample[...] == expected"`) against the issue's exact reproduction string before applying the patch. Never rely on mental index calculations, which notoriously drop boundary characters.
3. **Edit**: Apply minimal necessary changes with `apply_patch`.
4. **Quick Type Check**: Run `diagnostics` on modified files and verify no new errors on your changed lines.
5. **Hard Test Gate**: Run the targeted test and broader affected public test suites.
   - **MANDATORY**: The targeted test must PASS (0 failures, 0 errors, exit=0).
   - **NEVER** submit a patch if public tests are failing (`exit != 0`). Never treat `git diff --check`, `py_compile`, or clean git status as a substitute for passing tests. If tests fail, run `diagnostics` again to catch subtle syntax errors, then fix the logic or revert breaking changes before completing.
