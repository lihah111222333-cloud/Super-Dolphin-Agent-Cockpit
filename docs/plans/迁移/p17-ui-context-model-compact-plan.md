# P17 UI 功能修复计划 v2（上下文空间 + 模型选择 + 压缩按钮）

> 生成时间：2026-04-12（v2 — 8 Agent 审查修订：字段精确化 + model patch 通路 + capability 落点 + Claude 延后）
> 前置依赖：P16.1（统一 Diff）已完成
> 调研依据：5 Agent 调研 + 3 Agent 断点排查 + 8 Agent 计划审查

---

## 0. 背景

V3 前端组件从 V2 完整搬入，但三个核心 UI 功能不工作：
- 上下文空间显示（token usage）— 数据链断裂
- 模型选择（model selector）— 4 处断点
- 压缩按钮（compact）— Claude 无原生 compact，Codex 链路已通但未验证

---

## 1. 上下文空间显示（P0 优先）

### 1.1 断点

`internal/provider/unified/ui_tokens.go:29-35`

Codex WS 发 `thread/tokenUsage/updated`，payload：
```json
{
  "tokenUsage": { "total": { "inputTokens": N, "outputTokens": N }, "last": {...} },
  "modelContextWindow": 200000
}
```

但 `tokensUpdatedEvent()` 只认 `usage.inputTokens` 或顶层 `inputTokens`，**不认 `tokenUsage.total.*`** → `UITokensUpdated` 从未 publish。

### 1.2 修复

**修改** `internal/provider/unified/ui_tokens.go`：

`tokensUpdatedEvent()` 兼容 Codex payload（字段均为 **camelCase**）：
1. 优先解析 `tokenUsage.total.inputTokens` / `tokenUsage.total.outputTokens` / `tokenUsage.total.totalTokens`
2. 回退 `tokenUsage.last.inputTokens` / `tokenUsage.last.outputTokens`
3. 再回退 `usage.inputTokens` / `usage.outputTokens`（现有逻辑）
4. contextWindow：`modelContextWindow` > `contextWindow` > `context_window`
5. 处理 >100% 占用率：`totalTokens > contextWindow` 时 clamp 到 100%

**unknown 日志消除**：修改 `codexapp/event_map.go` 的 `shouldWarnUnknownRawEvent()` 白名单，加入 `thread/tokenUsage/updated`（不加 publish 分支，因为 `PublishUITokensUpdated` 已在 `translateCodexEvent` 中被调用）

**测试**：`thread/tokenUsage/updated` → `UITokensUpdated` → `UIThreadPatch.TokenUsage` 回归测试

**已确认**：`UITokensUpdated` 已在 `typedEventPublishers` 注册（P16.1 已加），修好解析即可路由

### 1.3 验证

- 启动 Codex Agent，确认前端显示 token 使用量
- 日志中不再出现 `thread/tokenUsage/updated` 的 unknown raw event

---

## 2. 模型选择（P1）

### 2.1 断点（4 处）

| # | 断点 | 文件 | 说明 |
|---|------|------|------|
| 1 | codexapp session 缺 `ReadConfig` | codexapp/session | `thread/config/get` 回退旧快照 |
| 2 | `SetConfig()` persist 前读旧 config | thread/command.go | 返回值 stale + 旧 model 写回 DB |
| 3 | `applyAgentLaunched()` 丢 `ev.Model` | uistate/projector_handlers.go | patch 不更新 model |
| 4 | `updateRuntimeConfigFromPatch()` 不同步 model/effort | codexapp/support.go | config/read 缺 live model |

### 2.2 修复

**断点 3（首次推送，先做）**：
- `uistate/projector_handlers.go` 的 `applyAgentLaunched()`：补 `Model: strings.TrimSpace(ev.Model)`
- 注意：这只覆盖 agent 启动时的首次 model 推送，**后续切模型不会触发 launched 事件**

**断点 2（核心 + model 增量通路）**：
- `thread/command.go` 的 `SetConfig()`：
  - persist 后重新构造 `ThreadConfig` 返回值，不依赖 stale `cfg`
  - **nil vs 空串语义**：`patch.Model == nil` 表示不改，`patch.Model == ""` 表示清除 override 回默认
  - `persistThreadConfig()` 更新 `thread.Model` 时优先用 `patch.Model`（区分 nil/空串）
- **SetConfig 成功后发布 model patch**：通过 `bus.Publish(UIThreadPatch{Model: newModel})` 推送前端，解决“切模型后前端不更新”问题

**断点 1（暂缓）**：
- `codexapp session` 实现 `ReadConfig()` — 从 codex app-server 读取 live config
- 暂不做，依赖断点 2 的修复让 offline fallback 数据正确

**断点 4（暂缓）**：
- `codexapp/support.go` 的 `updateRuntimeConfigFromPatch()` 同步 model/effort
- 依赖断点 2 的 patch 通路后可补

### 2.3 验证

- 前端选择模型 → `thread/config/set` → 返回新 model
- 下次 turn 使用新 model
- sidebar 显示正确的 model

---

## 3. 压缩按钮（P2）

### 3.1 现状

| Provider | 状态 | 说明 |
|----------|:---:|------|
| Codex | ✅ 链路已通 | capability + session 实现 + 事件反馈全有 |
| Claude | ❌ 不支持 | 无原生 compact API |

### 3.2 Codex 验证

链路代码已通，需端到端验证：
- `thread/compact/start` RPC → codex session CompactThread → `/compact` → `thread.Compacted` → 前端
- 验收项：无超时 / timeline 变化 / token 计数更新

### 3.3 Claude

**P17 不做 Claude compact**。Claude 无原生 compact API，只能通过摘要会话拉新 agent 替代，工作量较大，单独排期。

### 3.4 前端优化

**capability 数据来源**（审查修正）：
- 前端通过 `agentRuntimeById[threadId]` 获取 agent runtime
- runtime 中的 `capabilities` 来自 `ui/state/get` RPC 返回值
- 后端 `uistate/sidebar_compat.go` 把 `thread.Capabilities` 投影到 `agentRuntimeById`
- 前端检查 `capabilities.includes('context_compact')`：有 → 显示压缩按钮；无 → 禁用/隐藏

**错误处理**：
- `useThreadActions.js:183-190` 的 compact catch 块：不再吞错误，添加 toast 提示


---

## 4. 执行计划

### Phase 1: 上下文空间修复（1 Agent）

| Task | 内容 | 落点 |
|------|------|------|
| 1-1 | `ui_tokens.go` 兼容 `tokenUsage.total.*` + `modelContextWindow` | unified/ui_tokens.go:29-35 |
| 1-2 | `event_map.go` 加 `thread/tokenUsage/updated` 显式 case | codexapp/event_map.go |
| 1-3 | 回归测试 | unified/ui_tokens_test.go |

### Phase 2: 模型选择修复（1-2 Agent）

| Task | 内容 | 落点 |
|------|------|------|
| 2-1 | `applyAgentLaunched` 补 Model 字段 | uistate/projector_handlers.go |
| 2-2 | `SetConfig` persist 后用新值构造返回（区分 nil/空串） | thread/command.go |
| 2-3 | `persistThreadConfig` 优先用 patch.Model（区分 nil/空串） | thread/factory.go |
| 2-4 | SetConfig 成功后 publish UIThreadPatch{Model} | thread/command.go |
| 2-5 | 验证 thread/config/set → 返回新 model + 前端实时更新 | go test |

### Phase 3: 压缩验证 + 前端优化（1 Agent）

| Task | 内容 | 落点 |
|------|------|------|
| 3-1 | Codex compact 端到端验证 | 手动测试 |
| 3-2 | 前端按 capability 控制按钮显示 | useThreadActions.js |
| 3-3 | compact 失败 toast 提示 | useThreadActions.js |

### Phase 4: Claude 摘要重启（待排期）

暂不实施。

---

## 5. 守卫

```
守卫：≤400 行 / ≤80 行 / CC≤10 / 包≤15 / ≤4500
互审：Phase 1 1:2 / Phase 2 1:3 / Phase 3 1:2
```

---

## 6. 预估

| Phase | 工作量 | 说明 |
|-------|:---:|------|
| Phase 1 | 0.5d | ui_tokens 兼容 camelCase + 白名单 + 测试 |
| Phase 2 | 1.5-2d | 3 断点修复 + model patch 通路 + nil/空串语义 + 验证 |
| Phase 3 | 0.5d | Codex 验证 + capability 控制 + toast |
| **合计** | **2.5-3d** | Claude compact 单独排期 |
