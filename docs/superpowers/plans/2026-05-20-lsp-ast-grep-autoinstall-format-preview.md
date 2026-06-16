# LSP ast-grep Autoinstall and Format Preview Split Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Source of truth:** This plan supersedes ast-grep/`sg`, `grep.ast_search`, installer post-install binary resolution, and formatting preview/apply instructions in `docs/superpowers/plans/2026-05-20-lsp-error-transparency.md`. That older plan now has a superseded-scope notice; do not implement its hard-coded `sg run` / `sg scan` snippets.

**Goal:** 让 `grep.ast_search` 缺少 ast-grep 时复用现有 installer 自动安装，并把 formatting API 演进为“预览工具只读、编辑工具可落盘”的清晰契约。

**Architecture:** `ast_search` 通过注入式 ast-grep resolver 查找、验证和必要时安装 `ast-grep`，不 fallback 到文本搜索。第一阶段保持 `edit.format` 省略/false 的 legacy preview 兼容，同时新增顶级 `format_preview` 工具并支持 `edit.format persist_to_disk=true` 显式落盘；后续版本再把 `edit.format` 默认切为落盘。

**Tech Stack:** Go, MCP tool handlers, existing `installer.Provider`, LSP `TextEdit`, npm global install, existing `applyWorkspaceEdit` edit pipeline.

---

## Non-negotiable decisions

- `grep.ast_search` 必须开箱即用：缺少可用 ast-grep CLI 时，按现有 `EnsureInstalled` 模式执行静态安装命令并验证。
- `ast_search` 不允许静默降级到 `text_search`。
- `sg` 不能被盲信：优先 `ast-grep`，只有验证为 ast-grep CLI 的 `sg` 才可使用。
- 第一阶段不得直接把 `edit.format` 省略参数改成落盘，避免破坏现有 result-only 调用方。
- 长期目标是 `edit.format` 默认落盘；第一阶段先新增 `format_preview` 并支持 `edit.format persist_to_disk=true`。
- LSP-originated `WorkspaceEdit` 必须按 UTF-16 code-unit 坐标应用；`replace_range` 的用户坐标语义不能被改坏。

## Files and responsibilities

- Modify: `internal/sidecar/lsp/installer/installer.go`
  - 增强 npm post-install binary 解析、输出截断、必要的 per-key 安装互斥。
- Modify/Test: `cmd/mcp-lsp/runtime.go`
  - 注册 ast-grep installer，并把 installer-backed resolver 注入工具层。
- Modify: `internal/sidecar/lsp/tools/tool_file.go`
  - 扩展 `tools.Config`，承载 ast-grep resolver / installer adapter。
- Modify: `internal/sidecar/lsp/tools.go`
  - 给 `newToolHandlers` 传 resolver；新增 `format_preview` manifest/handler/可选 alias。
- Modify/Test: `internal/sidecar/lsp/search/searchutil.go`
  - `ASTSearchOptions` 接受 resolver/binary；`run`/`scan` 使用 resolved binary，不再硬编码 `sg`。
- Modify/Test: `internal/sidecar/lsp/tools/tool_grep.go`
  - ast_search 将 resolver 传入 `SearchAST`；确保生产 handler 真调用 installer。
- Modify/Test: `internal/sidecar/lsp/tools/tool_edit.go`
  - `format` 增加 `persist_to_disk=true` apply 路径；省略/false 保持 legacy preview。
- Modify/Test: `internal/sidecar/lsp/tools/tool_edit_support.go`
  - LSP WorkspaceEdit 应用路径切换到 UTF-16 helper；保留 replace_range 用户坐标。
- Create/Modify: `internal/sidecar/lsp/tools/tool_format_preview.go`
  - 新增顶级 `format_preview` 工具，只返回 edits，不落盘。
- Modify/Test: `cmd/mcp-lsp/schema.go`
  - 新增 `format_preview` schema；更新 `edit.format` / `persist_to_disk` 文案。
- Modify: `docs/交接笔记/内部笔记/LSP系统提示词.md`
  - 更新工具语义。
- Modify if present: `docs/交接笔记/内部笔记/lsp提示词英文版.md`
  - 同步英文提示词。

---

## Task 1: ast-grep installer support and npm post-install resolution

**Files:**
- Modify: `internal/sidecar/lsp/installer/installer.go:18-144`
- Test: `internal/sidecar/lsp/installer/installer_test.go`

- [ ] **Step 1: Write npm post-install failing test**

  使用 fake `npm`：

  ```go
  // fake npm writes executable ast-grep into fake npm global bin.
  // PATH intentionally does not include that global bin after install.
  got, err := p.EnsureInstalled(ctx, "ast-grep")
  ```

  Expected before fix: install succeeds but `EnsureInstalled` cannot find `ast-grep`.

  Expected after fix: returns absolute path to fake npm global bin `ast-grep`.

- [ ] **Step 2: Extend post-install resolution**

  Keep current Go post-install behavior. Add npm-specific resolution:

  - Run `npm bin -g` when supported.
  - If unavailable, run `npm prefix -g`.
  - POSIX candidates include `<prefix>/bin/<binary>`.
  - Windows candidates include both `<prefix>/<binary>.cmd` and `<prefix>/<binary>.exe`; also check `<prefix>/bin/<binary>.cmd` and `<prefix>/bin/<binary>.exe` for npm layouts that still use `bin`.
  - Do not rely only on current process `PATH`.
  - Return absolute executable path.
  - Add tests for npm shims, including `<binary>.cmd` on Windows-style fake layouts.

- [ ] **Step 3: Add output truncation**

  Cap combined install output in errors, e.g. 8 KiB. Test that very large fake npm output is truncated.

- [ ] **Step 4: Add per-dependency install mutex/singleflight**

  Ensure concurrent `EnsureInstalled(ctx,"ast-grep")` calls trigger one install. Use a normalized dependency key based on `BinaryName + InstallCmd + InstallArgs`, not just the language key, so the same binary/install tuple shares one install. Do not hold the provider's config map lock while executing the install command. Test with fake installer counting invocations.

---

## Task 2: ast-grep resolver and production grep handler injection

**Files:**
- Modify: `cmd/mcp-lsp/runtime.go`
- Modify: `internal/sidecar/lsp/tools/tool_file.go`
- Modify: `internal/sidecar/lsp/tools.go`
- Modify: `internal/sidecar/lsp/tools/tool_grep.go`
- Modify: `internal/sidecar/lsp/search/searchutil.go`
- Test: `internal/sidecar/lsp/search/searchutil_test.go`
- Test: `internal/sidecar/lsp/tools/*grep*test.go`

- [ ] **Step 1: Define a small resolver interface**

  Do not make `search` depend on concrete `installer.Provider`.

  ```go
  type ASTGrepResolver interface {
      ResolveASTGrep(ctx context.Context) (string, error)
  }
  ```

  `ASTSearchOptions` accepts a function or interface for resolving the binary.

- [ ] **Step 2: Register ast-grep installer**

  In `setupInstaller()` register:

  ```go
  InstallerConfig{
      BinaryName: "ast-grep",
      InstallCmd: "npm",
      InstallArgs: []string{"install", "-g", "@ast-grep/cli"},
      Language: "ast-grep",
  }
  ```

- [ ] **Step 3: Inject resolver into production grep handler**

  Extend `tools.Config` and `newToolHandlers` so production `grep.ast_search` receives resolver from the same installer created in runtime.

  Acceptance:

  - A production-style handler test proves `grep.ast_search` calls fake installer when neither `ast-grep` nor valid `sg` exists.
  - Invalid query/glob/path fails before resolver/install is called.
  - `text_search` never calls resolver/install.

- [ ] **Step 4: Resolve and validate binary**

  Resolver order:

  1. `LookPath("ast-grep")`; validate.
  2. `LookPath("sg")`; validate it is ast-grep.
     - If `sg` exists but validation proves it is not ast-grep, record it as a rejected candidate and continue to install.
     - Do **not** fail immediately on invalid system `sg`; Linux `/usr/bin/sg` must not block autoinstall.
  3. `EnsureInstalled(ctx, "ast-grep")`; validate returned path.

  Validation:

  - Short timeout.
  - `run --help` must succeed and indicate ast-grep run support.
  - `scan --help` must succeed and indicate ast-grep scan support.

- [ ] **Step 5: Use resolved binary for all AST commands**

  Update `runSGPatternSearch` and `runSGKindSearch` to accept `binary string`.

  Never call hard-coded `"sg"` after resolution.

- [ ] **Step 6: Add tests**

  Cover:

  - fake `ast-grep` wins over fake `sg`.
  - fake non-ast-grep `sg` is skipped/reported as an invalid candidate, then installer is called.
  - installer success returns absolute path and run/scan use it.
  - installer failure fail-fast with truncated output.
  - context cancel stops install/search.
  - no fallback to text search.

---

## Task 3: LSP UTF-16 TextEdit application path

**Files:**
- Modify: `internal/sidecar/lsp/tools/tool_edit_support.go`
- Test: `internal/sidecar/lsp/tools/tool_edit_support_test.go`

- [ ] **Step 1: Add LSP UTF-16 tests**

  Fixtures must include:

  - ASCII.
  - BMP Chinese characters.
  - non-BMP emoji / surrogate pairs.
  - position in the middle of surrogate pair must fail-fast.
  - multiple edits sorted by original snapshot.
  - overlapping edits fail-fast.
  - out-of-range line/character fail-fast.
  - CRLF preservation.
  - no partial write on invalid edit.

- [ ] **Step 2: Split LSP-originated edit application from user-coordinate replace_range**

  Keep existing `replace_range` coordinate behavior. Add helper such as:

  ```go
  func applyLSPTextEdits(content string, edits []protocol.TextEdit) (string, error)
  ```

- [ ] **Step 3: Wire LSP-originated WorkspaceEdit to UTF-16 helper**

  `applyWorkspaceEdit` -> `loadWorkspaceEditUpdatesFromFiles` must use UTF-16 helper for `WorkspaceEdit` text edits.

  If `replace_range` relies on existing `applyTextEdits`, keep or rename that function for user-coordinate use.

- [ ] **Step 4: Keep existing edit actions green**

  Add/keep regression tests for:

  - `rename` workspace edits.
  - `code_action` workspace edits if persisted in future.
  - `replace_range` patch/old_string behavior unchanged.

---

## Task 4: `edit.format` explicit apply and compatibility bridge

**Files:**
- Modify: `internal/sidecar/lsp/tools/tool_edit.go`
- Test: `internal/sidecar/lsp/tools/tool_edit_logging_test.go`
- Test: `internal/sidecar/lsp/tools/tool_edit_format_test.go` or adjacent edit tests

- [ ] **Step 1: Preserve legacy default/false**

  For first release:

  - `edit.format` with omitted `persist_to_disk` returns top-level `[]format.TextEdits`, no envelope.
  - `edit.format persist_to_disk=false` returns same preview shape plus a warning where compatible; if warning would break shape, log deprecation and document it.
  - No disk write.
  - No LSP DidChange/sync.

- [ ] **Step 2: Add explicit apply path**

  `edit.format persist_to_disk=true`:

  - Calls `manager.Format`.
  - Wraps raw edits into single-file `WorkspaceEdit`.
  - Calls `applyWorkspaceEdit`.
  - Returns envelope with:

    ```json
    {
      "success": true,
      "action": "format",
      "status": "applied",
      "applied": true,
      "persisted": true,
      "applied_count": 1,
      "text_edit_count": 1,
      "lsp_sync": true,
      "diagnostic_generation": 123
    }
    ```

- [ ] **Step 3: Empty edits behavior**

  - Preview path returns empty array.
  - Apply path returns no_change envelope, `applied=false`, `persisted=false`, `text_edit_count=0`.

- [ ] **Step 4: Rollback behavior**

  On sync failure:

  - Roll back disk file.
  - Attempt to restore LSP buffer/DidChange state.
  - Return error and do not claim success.
  - Rollback failure returns combined error.

- [ ] **Step 5: Rename compatibility**

  Add regression:

  - `rename` omitted `persist_to_disk` still defaults to apply.
  - `rename persist_to_disk=false` still returns prepared/workspace_edit.

---

## Task 5: New `format_preview` top-level tool

**Files:**
- Create: `internal/sidecar/lsp/tools/tool_format_preview.go`
- Modify: `internal/sidecar/lsp/tools.go`
- Modify: `cmd/mcp-lsp/schema.go`
- Tests: `internal/sidecar/lsp/tools/*format_preview*test.go`

- [ ] **Step 1: Add schema and manifest**

  Schema fields:

  - `file_path` required.
  - `language_id` optional.
  - formatting options optional if currently supported.

  Manifest must say: preview only, no disk write, no DidChange.

- [ ] **Step 2: Add handler registration**

  Update:

  - `lspToolManifests`.
  - `newToolHandlers`.
  - `legacyToolAliases` if adding `lsp_format_preview`.

- [ ] **Step 3: Implement shared format request helper**

  `edit.format` and `format_preview` should share:

  - path resolution.
  - manager resolution.
  - `manager.Format` call.
  - raw/display coordinate documentation.

- [ ] **Step 4: Output contract**

  Return:

  ```json
  {
    "success": true,
    "action": "format_preview",
    "status": "prepared",
    "applied": false,
    "persisted": false,
    "requires_apply": true,
    "text_edit_count": 3,
    "raw_text_edits": [
      {
        "range": {
          "start": {"line": 0, "character": 0},
          "end": {"line": 0, "character": 0}
        },
        "newText": "\t"
      }
    ],
    "display_text_edits": [
      {
        "range": {
          "start": {"line": 1, "character": 1},
          "end": {"line": 1, "character": 1}
        },
        "newText": "\t"
      }
    ],
    "coordinate_contract": {
      "raw_text_edits": "LSP native: 0-based line, 0-based UTF-16 character offsets",
      "display_text_edits": "Display only; do not feed into apply"
    },
    "diagnostic_generation": 123
  }
  ```

  If output budget is exceeded, fail-fast or omit raw edits with a clear warning. Do not return truncated raw edits as if complete.

- [ ] **Step 5: Tests**

  - Does not write disk.
  - Does not DidChange/sync.
  - Does not require `line`/`column`.
  - Unsafe path rejected.
  - Output budget behavior is safe.
  - Root-level tool contract tests are updated: manifest/list includes `format_preview`, output shape is stable, and alias behavior is covered if a legacy alias is added.

---

## Task 6: Schema and docs

**Files:**
- Modify: `cmd/mcp-lsp/schema.go`
- Modify: `internal/sidecar/lsp/tools.go`
- Modify: `docs/交接笔记/内部笔记/LSP系统提示词.md`
- Modify if exists: `docs/交接笔记/内部笔记/lsp提示词英文版.md`
- Optional generated docs/codemap: only update through generator/check flow.

- [ ] **Step 1: Update edit schema**

  Document action-specific `persist_to_disk` defaults:

  - `rename`: omitted/true => apply; false => prepared.
  - `format`: first release omitted/false => legacy preview; true => apply.
  - Future migration note: `edit.format` will become default apply after preview migration.
  - The default flip must be handled by a separate follow-up plan/version migration after `format_preview` is shipped, documented, and known callers have migrated.

- [ ] **Step 2: Update format_preview schema/manifest**

  Clearly identify `format_preview` as the preview path.
  Add `lspFormatPreviewOutputSchema` and register `format_preview` with `toolManifestWithOutputSchema` so the stable envelope fields are visible in `tools/list`.
  Add stable output contract documentation. If raw edits are omitted for budget reasons, do not return `raw_text_edits: []` as if complete; return explicit fields such as `raw_text_edits_omitted=true`, `raw_text_edits_complete=false`, and keep `text_edit_count`.

- [ ] **Step 3: Update LSP prompts/docs**

  Must mention:

  - `ast_search` may auto-install ast-grep via existing installer.
  - `ast_search` is not purely in-process text search: first use may run the existing installer and execute the static `npm install -g @ast-grep/cli` command if no valid ast-grep binary exists.
  - `ast_search` never falls back to text search.
  - Use `format_preview` to inspect formatting edits.
  - Use `edit.format persist_to_disk=true` to apply formatting now.
  - Long-term direction: `edit.format` will become default apply.
  - Raw LSP edits use UTF-16 coordinates; display edits are not apply input.

---

## Task 7: Verification

**Files:** no code changes expected.

- [ ] **Step 1: Run focused affected packages**

  ```bash
  ./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1
  ```

  Expected: exit 0.

- [ ] **Step 2: Run guard**

  ```bash
  make guard
  ```

  Expected: exit 0.

- [ ] **Step 3: Run build**

  ```bash
  make build-plain
  ```

  Expected: exit 0.

- [ ] **Step 4: Docs/codemap if touched**

  If codemap/generated docs changed:

  ```bash
  make codemap-check
  ```

- [ ] **Step 5: Broad test if installer/runtime blast radius expands**

  ```bash
  make test
  ```

  Run when installer/runtime/shared tool manifest changes are broader than `cmd/mcp-lsp`.

---

## Review iteration notes

- R1 P1 fixed: npm install path must not depend on PATH; add npm global bin/prefix resolution.
- R1 P1 fixed: production grep handler must receive resolver via `tools.Config` and pass it to `ASTSearchOptions`.
- R2 P1 fixed: do not immediately make `edit.format` omitted default write files; first release keeps legacy preview, explicit true applies, future migration documented.
- R2 P1 fixed: UTF-16 non-BMP/CRLF/multiple edit tests are mandatory.
- R3 P1 fixed: this file is now the single source of truth; older overlapping plan scope is retained only with a superseded-scope notice.
- R3 P1 fixed: InstallerConfig alone is insufficient; plan adds ast-grep resolver with alias validation and npm post-install absolute path resolution.
- R3 P1 fixed: LSP-originated WorkspaceEdit path must use UTF-16 helper; replace_range keeps its existing user-coordinate path.
- S2 P1 fixed: invalid system `sg` is a skipped candidate and must not block autoinstall.
- S2 P1 fixed: npm post-install resolution includes Windows npm shim candidates such as `<binary>.cmd`, and tests must cover them.
- T2 P1 fixed: older `2026-05-20-lsp-error-transparency.md` has a superseded-scope notice, and this plan is the source of truth for ast-grep/format-preview work.
