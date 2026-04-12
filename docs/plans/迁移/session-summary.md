# V3 迁移会话摘要

> 更新时间：2026-04-12
> 会话范围：**P16.1 统一 Diff** + **Codex/Claude 事件修复** + **P17 UI 功能修复** + **临时诊断日志清理收尾**
> 本次验证：`go build ./internal/... ./cmd/...` + `go vet ./internal/... ./cmd/...` + `go test -run TestCodeSizeGuard ./internal/archtest/...` 全绿

---

## 1. 当前结论

- **P16.1 统一 Diff 已完成**：旧 diff 方案已清理，Codex / Claude 统一收敛到 toolbridge + difftracker 主链路。
- **Codex / Claude 事件修复已完成**：`turn/diff/updated` 映射补齐，Claude `system:init` 重复 `AgentLaunched` 已修，前端 optimistic insert 已去重。
- **P17 UI 功能修复已完成**：上下文空间、模型选择、压缩按钮三条链路已补齐。
- **临时诊断日志已清理**：排查期临时 WARN 已删除或降为 Debug，保留 `toolbridge: proxy started` 启动确认日志。
- **当前仓库验证通过**：build / vet / CodeSizeGuard 全部通过。

---

## 2. 本会话核心成果（2026-04-12）

### 2.1 P16.1 统一 Diff

- **Phase 0**：清理旧 diff 实现（`-1730` 行死代码），保留 `difftracker` 核心。
- **Phase 1**：落地 `proxy.go` MCP HTTP proxy + manifest `ProxyHTTPAddr` 归一 + diff tracking + 安全加固。
- **Phase 2**：补上 git diff 兜底（`ToolCallEnd` + `callId` 去重）。
- **互审**：完成 `1:4` 互审 + 修复（编译错误 + proxy 安全 + family 路由）。

### 2.2 Codex / Claude 事件修复

- `turn/diff/updated` 事件映射补齐，并完成 `typedEventPublishers` 注册。
- Claude `system:init` 不再重复发 `AgentLaunched`。
- 前端 optimistic insert 去重，兼容 proxy 引入后的时序变化。

### 2.3 P17 UI 功能修复

#### Phase 1：上下文空间显示
- `ui_tokens` 兼容 camelCase。
- `tokenUsage.last` 优先，保持 V2 parity。
- `contextWindow` 改为从 `tokenUsage` 层级继续搜索。

#### Phase 2：模型选择
- `applyAgentLaunched` 补 `Model`。
- `SetConfig` 改为本地存储，不转发 Codex。
- `model/list` 解析补齐 `data` 格式。
- `runtimeConfig` 回填到 `turn/start`。
- `AllowedModels` 支持 `data` key。

#### Phase 3：压缩按钮
- `capabilities` 从 provider 推断并投影到 `agentRuntimeById`。
- 按钮禁用态补齐。
- 错误提示补齐。
- `compact` 可用性与 provider 能力保持一致。

### 2.4 额外修复

- 删除 `developerInstructions` 工具目录注入（上下文约 `77k → 16k`）。
- `TokenUsage` JSON tag 改 camelCase，并新增 `usedTokens` / `usedPercent`。
- `thread/config/set` 不再转发 codex，改为本地存储并在 `turn/start` 生效。
- `threadResumeParams` 扩展字段。
- `applyConfigSet` 改为本地 `runtimeConfig` 存储。
- `effort` / `model` 从 `runtimeConfig` 补填到 `turn/start` params。

---

## 3. 本次收尾：临时诊断日志清理

### 3.1 已清理/降级的日志

- `internal/provider/unified/ui_tokens.go`
  - 删除 `ui_tokens: tokensUpdatedEvent returned false`
  - 删除 `ui_tokens: publishing UITokensUpdated`
  - 删除 `ui_tokens: contextWindow parse`
  - 删除 `mapKeys` helper
  - 删除无用 `pkglogger` import
- `internal/provider/codexapp/session.go`
  - `codexapp: turn/start params`：`WARN -> DEBUG`
- `internal/provider/codexapp/session_approval.go`
  - 删除 `codexapp: onNotification received`
- `internal/provider/codexapp/support.go`
  - 删除 `codexapp: model/list raw response`
- `internal/platform/toolbridge/proxy.go`
  - `proxy: incoming request`：`WARN -> DEBUG`
- `internal/platform/toolbridge/module.go`
  - 保留 `toolbridge: proxy started`（启动确认日志仍有价值）

### 3.2 本次验证结果

```text
✅ go build ./internal/... ./cmd/...
✅ go vet ./internal/... ./cmd/...
✅ go test -run TestCodeSizeGuard ./internal/archtest/...
```

---

## 4. 已知限制

1. `modelContextWindow` 在切模型后不会更新（Codex 平台行为）。
2. Claude `compact` 暂不支持，仍需摘要 + 重启方案，待排期。
3. 前端 E2E 桥接测试仍不稳定（历史已知问题）。

---

## 5. 下一会话建议关注

1. 继续观察真实长会话下的统一 diff 链路稳定性。
2. 若要支持 Claude compact，需要设计“摘要后重启”方案。
3. 前端 E2E 桥接测试需要单独稳定化治理。

---

## 6. 交接结论

- 今天这轮核心工作已经完成，可直接基于当前状态继续后续迭代。
- 当前重点已从“功能补齐”转到“稳定性观察 + 后续能力排期”。
