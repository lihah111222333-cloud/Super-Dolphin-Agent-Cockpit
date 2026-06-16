# LSP 出参格式统一 Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 统一 MCP LSP 工具链的出参格式，消除"同类结果三种形态"的认知差：统一位置字段为 file+line+col、统一列表承载字段为 data、统一 hint 风格、清理残留契约字段。

**Architecture:** 改动集中在三层：协议层 `protocol/ext.go`（位置结构体字段名）、格式层 `format/compact.go`（CompactList/分组结果字段名）、工具层 `tools/tool_*.go`（各工具的响应结构和 hint 文案）。所有位置输出统一为 `file`(相对路径) + `line`(1-based) + `col`(1-based)；所有列表型结果的承载字段统一为 `data`；所有 hint 统一为 `next: <可复制的工具调用>` 格式。

**Tech Stack:** Go 1.25.7, MCP LSP tools

---

## 当前问题清单

| # | 问题 | 现状 | 目标 |
|---|------|------|------|
| 1 | 位置字段 col vs column | grep/definition 用 `col`，xref/workspace_symbol 用 `column` | 全部统一为 `col` |
| 2 | 列表承载字段 | grep/xref 用 `files`，其他用 `data` | grep/xref 改为 `data`（保持按文件分组结构） |
| 3 | hint 风格 | "step 2: ..."、"Next step: ..."、"results truncated; ..." | 全部统一为 `next: <工具调用示例>` |
| 4 | 残留字段 | edit.version 运行时兼容但不暴露、structure.path 运行时兼容但不暴露 | 已清理（上轮完成），本轮不涉及 |

---

## 文件结构

| 操作 | 文件 | 职责 |
|------|------|------|
| Modify | `internal/sidecar/lsp/protocol/ext.go` | CompactLocation 的 `Column` → `Col`；GroupedLocationResult 的 `Files` → `Data` |
| Modify | `internal/sidecar/lsp/format/compact.go` | CompactWorkspaceSymbol 的 `Column` → `Col`；GroupLocationsByFile 返回值字段对齐；hint 文案统一 |
| Modify | `internal/sidecar/lsp/tools/tool_grep.go` | grepResponse 的 `Files` → `Data`；hint 文案统一 |
| Modify | `internal/sidecar/lsp/tools/tool_edit_replace.go` | hint 文案统一为 `next:` 格式 |
| Modify | `internal/sidecar/lsp/tools/tool_coderun.go` | hint 文案统一为 `next:` 格式 |
| Modify | 相关 `*_test.go` | 同步更新所有断言 |

---

## Task 1: 统一位置字段 — column → col

**Files:**
- Modify: `internal/sidecar/lsp/protocol/ext.go:132-137`
- Modify: `internal/sidecar/lsp/format/compact.go:32-38`
- Test: 相关测试文件

- [ ] **Step 1: 修改 CompactLocation 的 Column → Col**

修改 `internal/sidecar/lsp/protocol/ext.go` L132-137：

```go
type CompactLocation struct {
	Line      int `json:"line"`
	Col       int `json:"col"`
	FuncStart int `json:"func_start,omitempty"`
	FuncEnd   int `json:"func_end,omitempty"`
}
```

JSON tag 从 `"column"` 改为 `"col"`。

- [ ] **Step 2: 修改 CompactWorkspaceSymbol 的 Column → Col**

修改 `internal/sidecar/lsp/format/compact.go` L32-38：

```go
type CompactWorkspaceSymbol struct {
	Name      string `json:"name"`
	Kind      int    `json:"kind,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Container string `json:"container,omitempty"`
}
```

JSON tag 从 `"column"` 改为 `"col"`。

- [ ] **Step 3: 更新所有引用 Column 字段的代码**

在 `internal/sidecar/lsp/format/compact.go` 和 `internal/sidecar/lsp/protocol/ext.go` 中，所有赋值 `.Column = ...` 的地方改为 `.Col = ...`。

用 grep 搜索：`grep -rn '\.Column' internal/sidecar/lsp/format/ internal/sidecar/lsp/protocol/ internal/sidecar/lsp/tools/`

逐一修改。

- [ ] **Step 4: 编译验证**

Run: `go build ./cmd/mcp-lsp/...`
Expected: SUCCESS（如果有编译错误，说明还有引用未更新，继续修复）

- [ ] **Step 5: 更新测试断言**

搜索测试中所有引用 `column` 或 `Column` 的断言，改为 `col` 或 `Col`：
`grep -rn 'column\|Column' cmd/mcp-lsp/ --include='*_test.go'`

- [ ] **Step 6: 运行测试**

Run: `go test ./cmd/mcp-lsp/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 7: Commit**

```bash
git add internal/sidecar/lsp/protocol/ext.go internal/sidecar/lsp/format/compact.go internal/sidecar/lsp/tools/
git commit -m "refactor(lsp): 统一位置字段 column → col"
```

---

## Task 2: 统一列表承载字段 — files → data

**Files:**
- Modify: `internal/sidecar/lsp/protocol/ext.go:139-145`
- Modify: `internal/sidecar/lsp/tools/tool_grep.go:38`
- Modify: `internal/sidecar/lsp/format/compact.go:204-237`
- Test: 相关测试文件

- [ ] **Step 1: 修改 GroupedLocationResult 的 Files → Data**

修改 `internal/sidecar/lsp/protocol/ext.go` L139-145：

```go
type GroupedLocationResult struct {
	Data      map[string][]CompactLocation `json:"data"`
	Total     int                          `json:"total"`
	Showing   int                          `json:"showing"`
	Truncated bool                         `json:"truncated,omitempty"`
	Hint      string                       `json:"hint,omitempty"`
}
```

字段名从 `Files` 改为 `Data`，JSON tag 从 `"files"` 改为 `"data"`。

- [ ] **Step 2: 修改 grepResponse 的 Files → Data**

修改 `internal/sidecar/lsp/tools/tool_grep.go` L38：

```go
type grepResponse struct {
	Data              map[string]grepFileRows `json:"data"`
	Total             int                     `json:"total"`
	Showing           int                     `json:"showing"`
	Truncated         bool                    `json:"truncated,omitempty"`
	DroppedForPayload int                     `json:"dropped_for_payload,omitempty"`
	RegexFallback     bool                    `json:"regex_fallback,omitempty"`
	Message           string                  `json:"message,omitempty"`
	Hint              string                  `json:"hint,omitempty"`
}
```

字段名从 `Files` 改为 `Data`，JSON tag 从 `"files"` 改为 `"data"`。

- [ ] **Step 3: 更新所有引用 .Files 的代码**

在 `internal/sidecar/lsp/format/compact.go` 的 `GroupLocationsByFile` 函数中，所有 `grouped.Files` 改为 `grouped.Data`。

在 `internal/sidecar/lsp/tools/tool_grep.go` 中，所有 `resp.Files`、`files[...]` 等引用改为 `resp.Data`、`data[...]`。

用 grep 搜索：
- `grep -rn '\.Files' internal/sidecar/lsp/format/ internal/sidecar/lsp/protocol/ internal/sidecar/lsp/tools/`
- `grep -rn 'resp\.Files\|files\[' internal/sidecar/lsp/tools/tool_grep.go`

逐一修改。注意 `tool_grep.go` 中有局部变量 `files` 用于构建 map，需要改名为 `data` 或 `grouped`（避免与 `resp.Data` 混淆）。

- [ ] **Step 4: 编译验证**

Run: `go build ./cmd/mcp-lsp/...`
Expected: SUCCESS

- [ ] **Step 5: 更新测试断言**

搜索测试中所有引用 `files` 或 `Files` 的断言（注意区分 grep/xref 的输出字段和其他用途）：
`grep -rn '\.Files\|"files"' cmd/mcp-lsp/ --include='*_test.go'`

- [ ] **Step 6: 运行测试**

Run: `go test ./cmd/mcp-lsp/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/mcp-lsp/
git commit -m "refactor(lsp): 统一列表承载字段 files → data"
```

---

## Task 3: 统一 hint 风格

**Files:**
- Modify: `internal/sidecar/lsp/tools/tool_grep.go:216`
- Modify: `internal/sidecar/lsp/format/compact.go:231,237`
- Modify: `internal/sidecar/lsp/tools/tool_edit_replace.go:133-137`
- Modify: `internal/sidecar/lsp/tools/tool_coderun.go`（截断 hint）
- Test: 相关测试文件

- [ ] **Step 1: 定义统一 hint 格式规范**

所有 hint 统一为格式：`next: <tool_name> <key=value params>`

示例：
- `next: file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>`
- `next: edit action=replace_range file_path=<file> patch="..."` 
- `next: file action=read_file pos=<file>:1 limit=200`

规则：
- 前缀固定为 `next: `（小写，带冒号和空格）
- 后面跟可直接复制的工具调用参数
- 截断类 hint 格式：`next: increase max_results or narrow query/path/glob`（无具体工具调用时用建议）

- [ ] **Step 2: 修改 grep hint**

修改 `internal/sidecar/lsp/tools/tool_grep.go` L216：

将 `"step 2: use the returned func_start/func_end to read that function range, e.g. file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"`

改为 `"next: file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"`

- [ ] **Step 3: 修改 xref hint**

修改 `internal/sidecar/lsp/format/compact.go` L231：

将 `"step 2: use the returned func_start/func_end to read that function range, e.g. file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"`

改为 `"next: file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>"`

- [ ] **Step 4: 修改 CompactList 截断 hint**

修改 `internal/sidecar/lsp/format/compact.go` L82 附近的截断 hint：

将 `"results truncated; increase max_results or narrow the request"`

改为 `"next: increase max_results or narrow the request"`

同样修改 L237 的 xref 截断 hint：

将 `"results truncated; increase max_results or narrow the target position"`

改为 `"next: increase max_results or narrow the target position"`

- [ ] **Step 5: 修改 edit failure hint**

修改 `internal/sidecar/lsp/tools/tool_edit_replace.go` L133-137 的 `appendFailureNextStep`：

将 `"Next step: file action=read_file pos=%s:1 limit=%d ..."` 格式

改为 `"next: file action=read_file pos=%s:1 limit=%d ..."` 格式（小写 next，去掉 "step"）

同样修改 L137 的无行数版本。

- [ ] **Step 6: 修改 code_run 截断 hint**

修改 `internal/sidecar/lsp/tools/tool_coderun.go` 中截断时的 hint：

将 `"output truncated; rerun with narrower scope or check logs"`

改为 `"next: rerun with narrower scope or check logs"`

- [ ] **Step 7: 编译和测试**

Run: `go build ./cmd/mcp-lsp/... && go test ./cmd/mcp-lsp/... -count=1 -timeout 120s`
Expected: BUILD SUCCESS, ALL PASS（可能有测试断言旧 hint 文案需要更新）

- [ ] **Step 8: Commit**

```bash
git add cmd/mcp-lsp/
git commit -m "refactor(lsp): 统一 hint 风格为 next: 格式"
```

---

## Task 4: 集成验证

- [ ] **Step 1: 全量编译**
Run: `go build ./cmd/mcp-lsp/...`
Expected: SUCCESS

- [ ] **Step 2: 全量测试**
Run: `go test ./cmd/mcp-lsp/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 3: 代码守卫**
Run: `make guard`
Expected: PASS

---

## 复杂度红线

| 维度 | 上限 |
|------|------|
| 位置字段 | `file` + `line` + `col`（全工具统一，1-based） |
| 列表承载字段 | 永远是 `data`（不管是 map 还是 array） |
| hint 格式 | `next: <tool> <params>`（可直接复制调用） |
| 元字段 | `total`/`showing`/`truncated`/`hint`（顶层） |

---

## 不改的项（决策记录）

| 项 | 理由 |
|----|------|
| diagnostics 表格列名 `L`/`C` | 表格格式（cols/rows）是独立契约层，列名是缩写，与结构化位置字段（file/line/col）无关；改了反而增加 token 消耗 |
| grep.path | 搜索根目录，不是 legacy alias，保留 |
| grep.case_sensitive | smart-case 默认但保留显式覆盖能力，保留 |
| edit.version | 上轮已从 schema 移除，运行时兼容，不再改动 |
| structure.path | 上轮已从 schema 移除，运行时兼容，不再改动 |
| diagnostics/batch 的 success/meta | envelope 特有语义，核心路径（data + total/showing/truncated/hint）已统一 |
| read_file/hover 的 value 格式 | LLM 最优格式（纯文本/Markdown），不属于列表型结果，后续再议 |
