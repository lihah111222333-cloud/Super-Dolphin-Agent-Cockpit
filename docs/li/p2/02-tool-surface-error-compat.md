# P2-02：Tool surface / schema / structured error 兼容

## 裁决

**MIGRATE IDEAS**：迁移 v2 的工具名可见性、structured error/hint、部分 schema 兼容思想；不迁移 v2 “handler 返回 JSON 字符串”的整体实现。

v3 的 typed payload 与 `structuredContent` 更适合 MCP；P2 只补调用方可发现性、语言无关 capability 边界与错误可恢复性。

## 当前差距

### P0：`tools/list` 暴露名与系统提示词/v2 不一致

v3 `tools/list` 当前暴露短名：`file/inspect/xref/grep/structure/edit/completion`，见 `cmd/mcp-lsp/tools.go:29-38`；但 legacy alias 只在 call 时映射，见 `cmd/mcp-lsp/tools.go:41-49`。v2 明确暴露 `lsp_file/lsp_inspect/...`，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:33-55`。

影响：模型看到的工具名与系统提示词不一致，容易把 `lsp_file` 当不存在，或把短名当唯一入口。

### P1：错误不可机器解析

v3 `tools/call` handler 失败时直接变 JSON-RPC error，见 `internal/mcpserver/common/server.go:272-279`。v2 返回 `{"success":false,"error":...}`，并能追加 cursor/type hint，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:286-294` 与 `:596-668`。

影响：调用方无法区分 retryable、schema error、cursor coordinate error、LSP unavailable、timeout、panic recovery，也无法用 hint 自动修正。

### P1：`force` schema 缺口与安全边界

v3 `lsp_edit` schema 有 `persist_to_disk/version/only`，但无 v2 的 `force`，见 `cmd/mcp-lsp/schema.go:112-126`。v2 schema 中 `force` 用于 apply safety guard 后强制持久化，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:111-126`。

P2 只允许 `force` 越过明确实现的 apply safety guard；不得暗示已有独立“任务授权文件范围”系统。若未来要接入 owned-files 授权，必须单独定义输入来源、校验层和测试。

### P1：工具 surface 与通用语言能力边界不清

`lsp_file/lsp_inspect/lsp_xref/lsp_grep/lsp_structure/lsp_edit/lsp_completion` 是 generic LSP tools。执行命令、snippet 和测试能力已从 LSP 工具面移除，统一走独立 CLI/命令工具；它们不能被当成通用语言服务能力。

### P2：空结果 envelope 不统一

v3 部分工具空结果仍可能返回纯文本或工具特定结构；P2 应统一 `success/data/meta`，但不必回退成 v2 JSON 字符串。

## 设计

### 1. 可见工具名策略

采用双层兼容：

- `tools/list` 对外暴露 `lsp_file/lsp_inspect/lsp_xref/lsp_grep/lsp_structure/lsp_edit/lsp_completion`。
- 短名 `file/inspect/...` 保留为内部 canonical name 或 hidden alias。
- call path 同时接受 legacy 与短名，最终统一到 canonical handler。

验收：系统提示词、`tools/list`、handler alias 三者一致。

### 2. Structured tool error

定义统一 error envelope：

```go
type ToolErrorEnvelope struct {
    Success   bool           `json:"success"`
    Error     string         `json:"error"`
    Code      string         `json:"code,omitempty"`
    Retryable bool           `json:"retryable,omitempty"`
    Hint      string         `json:"hint,omitempty"`
    Meta      map[string]any `json:"meta,omitempty"`
}
```

建议 code：

- `schema_invalid`
- `file_not_found`
- `position_invalid`
- `language_unsupported`
- `capability_unsupported`
- `lsp_unavailable`
- `lsp_timeout`
- `lsp_client_closed`
- `scope_ambiguous`
- `internal_panic`

### 3. Cursor/type hint

迁移 v2 的 hint 思路，不迁移 v2 的字符串拼 JSON：

- 坐标越界：`line/column are 1-based`。
- replace_range 坐标错误：提示优先使用 patch。
- type hierarchy / type definition / implementation target mismatch：提示 cursor 应在 type/interface/identifier 上。

### 4. Language routing policy

默认从 `file_path` / URI 检测语言；P2 可新增可选 `language_id` override，但必须满足：

- override 进入 trusted resolved scope、workspace key、cache key。
- override 必须通过 adapter capability 校验。
- 同 URI 不同 languageID 不得共享 diagnostics/cache/bootstrap。
- extensionless、多模式文件应返回 structured ambiguity/capability error，而不是默认 Go。

### 5. `force` 参数

在 `lsp_edit` schema 和 handler 中加入 `force`，只允许越过明确的 apply safety guard，不得绕过：

- trusted scope 校验。
- workspace root containment。
- symlink/path safety。
- patch grammar 基本校验。
- language adapter capability 校验。

## 实现步骤

1. 修改 `cmd/mcp-lsp/tools.go`：`tools/list` 暴露 legacy names；call path 保持 canonical alias。
2. 修改 `cmd/mcp-lsp/schema.go` 与 `cmd/mcp-lsp/tools/tool_edit.go`：加入 `force` 并测试。
3. 修改 `internal/mcpserver/common/server.go` 与 middleware：tool handler error 转 structured result，而不是 JSON-RPC transport error；transport/protocol error 仍走 JSON-RPC error。
4. 修改 `cmd/mcp-lsp/tools/factory.go`：统一空列表 envelope。
5. 对 timeout/recovery middleware 增加 structured error 输出。
6. 明确执行能力不属于 LSP 工具面；unsupported language 返回 structured capability error。

## 必要测试

- `TestToolsListExposesLegacyLSPNames`
- `TestToolsCallAcceptsShortAndLegacyLSPNames`
- `TestEditSchemaIncludesForce`
- `TestEditForceDoesNotBypassTrustedScopeOrPathSafety`
- `TestToolsCallReturnsStructuredToolError`
- `TestTimeoutErrorIsStructuredRetryable`
- `TestRecoveryErrorIsStructured`
- `TestCursorErrorIncludesOneBasedHint`
- `TestRenderListResultEmptyEnvelope`
- `TestLanguageOverrideParticipatesInCacheKey`
- `TestStructuredToolErrorEnvelopeIsLanguageAgnosticForGoPythonAndTypeScript`

## 验收命令

```bash
set -euo pipefail
required_tests=(
  './cmd/mcp-lsp:TestToolsListExposesLegacyLSPNames'
  './cmd/mcp-lsp:TestToolsCallAcceptsShortAndLegacyLSPNames'
  './cmd/mcp-lsp:TestEditSchemaIncludesForce'
  './cmd/mcp-lsp/tools:TestEditForceDoesNotBypassTrustedScopeOrPathSafety'
  './internal/mcpserver/common:TestToolsCallReturnsStructuredToolError'
  './internal/mcpserver/common:TestTimeoutErrorIsStructuredRetryable'
  './internal/mcpserver/common:TestRecoveryErrorIsStructured'
  './cmd/mcp-lsp/tools:TestCursorErrorIncludesOneBasedHint'
  './cmd/mcp-lsp/tools:TestRenderListResultEmptyEnvelope'
  './cmd/mcp-lsp/tools:TestLanguageOverrideParticipatesInCacheKey'
  './cmd/mcp-lsp/tools:TestStructuredToolErrorEnvelopeIsLanguageAgnosticForGoPythonAndTypeScript'
)
run_required_test() {
  local pkg="$1"
  local test_name="$2"
  rg -n "func ${test_name}\(" "$pkg" --glob '*_test.go' >/dev/null
  if ! list_output="$(./scripts/test_with_guard.sh "$pkg" -list "^${test_name}$" 2>&1)"; then
    printf '%s\n' "$list_output"
    echo "P2 gate failed: go test -list failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$list_output"
  if ! printf '%s\n' "$list_output" | rg "^${test_name}$" >/dev/null; then
    echo "P2 gate failed: required test not in test binary: ${pkg}:${test_name}" >&2
    exit 1
  fi
  if ! output="$(./scripts/test_with_guard.sh "$pkg" -run "^${test_name}$" -count=1 -v 2>&1)"; then
    printf '%s\n' "$output"
    echo "P2 gate failed: test command failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$output"
  if printf '%s\n' "$output" | rg -i '\[?no tests to run\]?' >/dev/null; then
    echo "P2 gate failed: [no tests to run] for ${pkg}:${test_name}" >&2
    exit 1
  fi
  if printf '%s\n' "$output" | rg "^--- SKIP: ${test_name}(\b|/)" >/dev/null; then
    echo "P2 gate failed: required test skipped: ${pkg}:${test_name}" >&2
    exit 1
  fi
}
for item in "${required_tests[@]}"; do
  pkg="${item%%:*}"
  test_name="${item##*:}"
  run_required_test "$pkg" "$test_name"
done
```
