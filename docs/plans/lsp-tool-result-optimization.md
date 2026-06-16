# LSP 工具结果处理优化 — 实施计划 v3

## 背景

Claude Code 的 LSP MCP 工具返回格式是混合型：
- **结构化 JSON**：lsp_grep、lsp_structure、lsp_xref、lsp_completion
- **纯文本**：lsp_file(read_file)、lsp_inspect(hover)

当前项目全链路把所有结果当纯文本处理。

## 实际数据（截至 2026-05-08）

| 指标 | 值 |
|---|---|
| 持久化缓存文件数 | 6,739 |
| 超 50K（单结果截断线）| 1 个（git diff，非 LSP） |
| lsp_grep 最大结果 | 47,120 bytes（10 行匹配到内嵌 JSON 大文本） |
| lsp_grep 中位大小 | ~1,278 bytes |
| lsp_structure/lsp_xref | count-cap 50，通常几 KB，从不触发截断 |

**结论**：单结果截断极少发生；更常见的是 turn 级 budget 累计耗尽（120K runes/turn）。

---

## 改动范围

### 高优先（2 项）

#### H1. 前端 knownToolSummary() 补全 ✅ 已完成
- **文件**: cmd/agent-terminal/frontend/vue-app/utils/format-utils.js
- **内容**: 为 lsp_inspect/lsp_xref/lsp_structure/lsp_completion 添加专用摘要
- **风险**: 低

#### H3. JSON-aware 截断（修订策略）
- **问题**: rune 截断会破坏 JSON 结构，导致前端 JSON.parse 失败
- **实际触发场景**: turn 级 budget 耗尽时多个 lsp_grep 结果被截断
- **文件**:
  - internal/module/turn/tool_result_budget.go — 新增 repairTruncatedJSON() 函数（不修改现有 truncateToolResultChars）
  - internal/module/turn/tool_result_storage.go — CaptureToolResult 中截断后调用修复
  - internal/module/turn/tool_result_storage_test.go — 新增 JSON 修复测试用例
- **函数签名**: repairTruncatedJSON(original, truncated string) string
  - original: 截断前的完整字符串，用于 json.Valid() 判断是否值得修复
  - truncated: 截断后的字符串，实际被修复的对象
  - 返回: 合法 JSON 字符串，或原样返回 truncated（修复失败时）
- **调用位置**: CaptureToolResult 中获取 preview 后：
  ```go
  preview, budgetTruncated := takeToolResultPreview(...)
  if toolResultCharCount(preview) < originalSize {
      preview = repairTruncatedJSON(raw, preview)
  }
  ```
- **算法**（正向扫描状态机）:
  1. 检查 original 是否以 { 或 [ 开头且 json.Valid()
  2. 如果不是 JSON → 直接返回 truncated（现有行为）
  3. 如果是 JSON → 正向扫描 truncated 字符串，维护：
     - 字符串内/外状态（跟踪 " 边界，处理 \" 转义）
     - bracket/brace 栈（[ { 入栈，] } 出栈）
     - "最后干净截断位置"：每当扫描到字符串外且某个完整 value 结束后，记录该位置和当前栈快照
  4. 截到最后干净位置
  5. 删除末尾 trailing comma 及空白
  6. 弹出栈中所有未闭合的 ] / } 补到末尾
  7. 修复失败（如找不到干净位置）→ 返回 truncated
- **不注入 _truncated 字段**: 截断状态已由 ToolResultRecord.Truncated 承载，前端 UI 据此展示截断标记。在 JSON 内注入额外 key 会与 M2 的 OutputSchema 校验冲突，且对数组类型（如 lsp_structure 返回值）无合理注入方式
- **为什么新增函数而非修改 truncateToolResultChars**: 职责分离。原函数是纯 rune 操作，被多处调用；JSON 修复是高层语义操作，只在 CaptureToolResult 中使用
- **风险**: 中。修复逻辑是 best-effort + fallback，不影响现有行为

### ~~H2. IsStructured 标记~~ — 砍掉
- **原因**: 全链路无消费者（CaptureToolResult 只收 raw string，provider event_map 不读此字段）
- **行动**: 回退已加的 types.go 字段

---

### 中优先（2 项）

#### M1. MCP server structuredContent 双轨返回
- **规范依据**: MCP 2025-06-18 spec
- **价值**: 前瞻性合规；未来 MCP 客户端可直接消费结构化数据
- **文件和改法**（4 个出口）:
  - internal/mcpserver/common/server.go:
    - 删除 callTool() 方法（内联到 handleToolsCall）
    - handleToolsCall() 直接调 s.tools.CallTool() 拿到 any，json.Marshal 得 raw bytes
    - 用 json.RawMessage(raw) 作为 structuredContent（避免对原始 any 二次 marshal）
    - 日志 result_len 改为 len(raw)
  - internal/mcpserver/common/http_transport.go: 同步改动
  - cmd/mcp-lsp/fx.go: cfg.OnToolsCall 中加 structuredContent
  - cmd/mcp-orch/fx.go: cfg.OnToolsCall 中同步加 structuredContent
- **structuredContent 值类型**: json.RawMessage(raw) — 复用已 marshal 的 bytes，不重复序列化
- **toolbridge 消费侧暂不改**: 当前 provider 只传 text 给 LLM，structured content 无消费场景
- **向后兼容**: content 不变，structuredContent 是新增可选字段
- **风险**: 低

#### M2. MCPTool DTO 加 OutputSchema
- **文件**:
  - internal/dto/mcp/tool.go — MCPTool 增加 OutputSchema json.RawMessage
  - cmd/mcp-lsp/fx.go — registryToolProvider.ListTools() 中传递 OutputSchema
  - internal/sidecar/lsp/tools/ 中的工具 manifest — 为 lsp_grep 声明 outputSchema
- **只做 lsp_grep 一个**: 最常用的结构化返回工具
- **风险**: 低

### ~~M3. Content annotations~~ — 砍掉
- **原因**: 统一设 audience:["assistant"] 等于默认行为，无实际效果

---

## 砍掉和不做的

| 项目 | 原因 |
|---|---|
| H2 IsStructured 标记 | 无消费者，死代码（需回退已加字段） |
| M3 Content audience 注解 | 统一标注无实际效果 |
| LSP 结果缓存 | mcp-lsp 内部 gopls 已有 session 级缓存 |
| Resource Link 延迟加载 | Claude CLI 不支持 resource 协议 |
| Token 紧凑序列化 | 需 per-tool formatter，维护成本高 |

---

## 实施顺序

```
Phase 1 — 高优先:
  H1 ✅  前端 knownToolSummary 补全（已完成）
  回退   types.go 中已加的 IsStructured 字段
  H3     repairTruncatedJSON() + CaptureToolResult 调用 + 测试

Phase 2 — 中优先（M1 和 M2 无依赖，可并行）:
  M2     MCPTool DTO 加 OutputSchema
  M1     structuredContent 双轨返回（4 个出口）

Phase 3 — 验证:
  go test ./internal/module/turn/...
  go test ./internal/mcpserver/common/...
  go test ./internal/platform/toolbridge/...
  go vet ./...
```

## 受影响的测试文件

| 文件 | 影响 | 需要的动作 |
|---|---|---|
| internal/mcpserver/common/server_test.go | M1 改变响应格式 | 更新 TestServerHandlesToolsCall 的断言，验证 structuredContent 存在 |
| internal/module/turn/tool_result_storage_test.go | H3 新增修复逻辑 | 新增 JSON 修复的测试用例（合法 JSON 被截断 → 修复后可解析；非 JSON → 不变） |
| internal/platform/toolbridge/handler_test.go | 回退 IsStructured | TestToolBridge_PeerError_AdaptToResult 不受影响（该字段是可选的） |

不受影响的测试:
- internal/mcpserver/common/bootstrap/*_test.go — 不涉及响应格式
- internal/platform/toolbridge/diff_fallback_test.go — 不涉及响应格式
- cmd/mcp-orch/fx_test.go — 只是 fx 启动冒烟测试

## 回滚策略

- H3: repairTruncatedJSON 失败 → 代码内 fallback 到 rune 截断
- M1: structuredContent 是新增可选字段，删掉即回退
- M2: OutputSchema 是 metadata，删掉不影响运行时
