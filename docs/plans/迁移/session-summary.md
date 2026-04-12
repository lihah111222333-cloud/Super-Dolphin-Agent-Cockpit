# V3 迁移会话摘要

> 生成时间：2026-04-12（P16 UI Diff + 工具调用记录 + P16.1 统一 Diff + P17 UI功能修复完成）
> 会话范围：P0-P9 + P11-P15 + **P16 UI Diff/工具调用记录** + **P16.1 统一 Diff** + **P17 UI功能修复**
> 前序会话 UUID：58fdd978-cc4b-41e6-bd26-d40f3ff66854

---

## 1. 当前结论

### 编译验证：全绿（2026-04-12 实跑）
```
✅ go build ./internal/... ./cmd/... — PASS
✅ go vet ./internal/... ./cmd/... — PASS
✅ go test -run TestCodeSizeGuard ./internal/archtest/... — PASS (internal/archtest 0.701s)
✅ go test ./internal/platform/toolbridge/... — PASS (1.029s)
✅ go test ./internal/platform/difftracker/... — PASS (cached)
✅ go test ./internal/module/uistate/... — PASS (uistate 0.869s, timeline cached)
✅ go test ./internal/module/thread/... — PASS (cached)
✅ go test ./internal/provider/unified/... — PASS (0.514s)
```

### 迁移状态

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0-P15 | ✅ | 全部完成（见前序 session-summary） |
| **P16 Phase 2 工具调用记录** | ✅ | Timeline Kind 映射 + ActivityStats + UIThreadPatch 增量推送 + 前端修正 |
| **P16 Phase 1 Diff（旧实现）** | 🧹 已清理 | P16.1 Phase 0 删除旧 diff 实现，保留 difftracker 核心 git 能力 |
| **P16 额外修复** | ✅ | 前端 ReferenceError + Claude stdin pipe + interrupt recovery + enrichFromDB CC |
| **P16.1 统一 Diff** | ✅ | **Phase 0 清理旧diff(-1730行) + Phase 1 proxy+manifest归一+diff tracking+安全加固 + Phase 2 git diff兜底 + Codex turn/diff/updated事件映射 + Claude重复消息修复 + 前端optimistic去重。11轮计划审查 + 3波实施 + 1:4互审 + 修复** |
| **P17 UI功能修复** | ✅ | **Phase 1 上下文空间(ui_tokens camelCase兼容) + Phase 2 模型选择(4断点修复+model patch通路) + Phase 3 压缩按钮(capability禁用+错误提示)。v2计划审查(8 Agent) + 3波并行实施 + 1:4互审 + 修复** |

---

## 2. 本会话核心成果

### 2.1 P16.1 统一 Diff

- Phase 0：清理旧 diff 实现，删除 `aggregator/unified/tool_context/hooks_extract` + `claude_diff`（`-1730` 行），保留 `difftracker` 核心。
- Phase 1：新建 `proxy.go`（MCP HTTP proxy），manifest `ProxyHTTPAddr` 归一，diff tracking（`BeginSnapshot` / `EmitGitDiff`），安全加固（`MaxBytesReader` + timeout + family 校验 + recover）。
- Phase 2：git diff 兜底（`ToolCallEnd` 订阅 + `callId` 去重 + `EmitCurrentGitDiff`）。
- Bug fixes：Codex `turn/diff/updated` 事件映射 + `typedEventPublishers` 注册，Claude `system:init` 重复 `AgentLaunched` 修复，前端 optimistic insert 去重。

### 2.2 P17 UI功能修复

- Phase 1：上下文空间 — `ui_tokens.go` 兼容 Codex camelCase payload（`tokenUsage.total` / `last` / `usage` 三级回退），`shouldWarnUnknownRawEvent` 白名单。
- Phase 2：模型选择 — `applyAgentLaunched` 补 `Model`，`SetConfig` persist 后新值返回 + nil/空串语义，`threaddto.Updated.Model` patch 通路，空串回归测试。
- Phase 3：压缩按钮 — 前端 capability 禁用（`context_compact`），`alert` → 内联提示，Codex 链路验证。

---

## 3. P16.1 统一 Diff 落地状态（✅ 已完成）

### 3.1 统一入口

```text
Codex:  session → toolbridge.routeToolCall → peer → mcp-lsp → DiffEmitter
Claude: CLI → proxy /mcp/<family>/<agentId> → toolbridge.routeToolCall → peer → mcp-lsp → DiffEmitter
Fallback: ToolCallEnd → EmitCurrentGitDiff → DiffEmitter（callId 去重）
```

### 3.2 执行计划结果

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 0 | 清理旧 diff 实现，删除 aggregator/unified/tool_context/hooks_extract + claude_diff，保留 difftracker 核心 | ✅ 完成 |
| Phase 1 | proxy server + manifest ProxyHTTPAddr 归一 + routeToolCall diff tracking + 安全加固 | ✅ 完成 |
| Phase 2 | ToolCallEnd 订阅 + callId 去重 + EmitCurrentGitDiff git diff 兜底 | ✅ 完成 |

### 3.3 关键设计决策

| 决策 | 结果 |
|------|------|
| 主进程 proxy，不做 MCP session 基建 | ✅ 保持低基建改造面 |
| URL path `/mcp/<family>/<agentId>` 注入 agentId | ✅ Claude/Codex 归一到 toolbridge |
| `ProxyHTTPAddr` 优先 | ✅ manifest 不再依赖旧 PeerHTTPAddrs fallback |
| diff 在 `routeToolCall` 统一入口内做 | ✅ Codex/Claude 共享一条 diff tracking 路径 |
| ToolCallEnd git diff 兜底 | ✅ 覆盖无 before snapshot 的后置 diff 场景 |

### 3.4 审查历程（11 轮）

| 版本 | 方案 | 结果 | 核心问题/结论 |
|------|------|:----:|---------------|
| v1-v5 | hooks/git fallback/MCP session 多版方案 | 未通过 | metadata、MCP session、落点悬空 |
| v6-v8 | V2 式主进程 proxy + 精确落点 | 继续修订 | driver.go 落点、tools/list、fallback 细节 |
| v8-v11 | proxy 归一 + 安全加固 + git diff 兜底 | ✅ 通过并落地 | 进入 3 波实施与 1:4 互审修复 |

---

## 4. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| P16.1 统一 Diff | ✅ | 已完成 |
| P17 UI功能修复 | ✅ | 已完成 |
| P16 旧 diff 清理 | ⏳ | P16.1 Phase 0 |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |
| P2 event bus 互审 | 🔄 | 待收报告 |
| P15 后续待办 | ⏳ | DeferLoading / peer 路由 / 并发限流 |
| git commit | ⏳ | P16 所有改动待提交 |

---

## 5. 下一步

1. git commit — P16.1 + P17 所有改动
2. Claude compact 摘要重启方案（待排期）
3. P15 稳定性优化
4. IDA 工具族

---

## 6. 关键文档清单

| 文档 | 路径 |
|------|------|
| P16 执行计划 | docs/plans/迁移/p16-ui-diff-toolcall-plan.md |
| **P16.1 统一 Diff 计划（v11/已完成）** | **docs/plans/迁移/p16.1-unified-diff-plan.md** |
| 会话习惯画像 | docs/会话习惯.md |

---

## 7. Agent 使用统计

| 类型 | 数量 |
|---|---|
| 前序会话累计 | ~651+ |
| **本会话** | |
| P16 调研（V2/V3 差异） | 3+3 |
| P16 计划审查（3 轮 × 8 Agent） | ~24 |
| P16 Phase 1 实施 | 5 |
| P16 Phase 1 互审（2 轮 × 5） | 10 |
| P16 Phase 2 实施 | 4 |
| P16 Phase 2 互审 | 4 |
| P16 Phase 3 验证 + 终审 | 2+5+3 |
| P16 修复 Agent | ~15 |
| P16 验证 Agent | ~6 |
| Bug fix（stdin/interrupt/CC） | 4 |
| Claude Code 调研 | 3 |
| 精确落点调研 | 5 |
| P16.1 计划审查（8 轮 × 4） | ~32 |
| **本轮新增（概算）** | |
| P16.1 计划审查（v8→v11，4轮 × 4 Agent） | ~16 |
| P16.1 Phase 0 实施 | 1 |
| P16.1 Phase 1 实施（2 并行） | 2 |
| P16.1 Phase 1 互审（1:4）+ 修复 | 6 |
| P16.1 Phase 2 实施 | 1 |
| P16.1 bug fix（事件映射+重复消息+optimistic） | 主 Agent 直修 |
| P17 调研（5+3） | 8 |
| P17 计划审查（8 Agent） | 8 |
| P17 Phase 1-3 实施（3 并行） | 3 |
| P17 互审（1:4）+ 修复 | 7 |
| **本轮新增合计** | **~52** |
| **本会话合计（更新后）** | **~172+** |
| **总计（更新后）** | **~823+** |

---

## 8. 会话交接

### 8.1 当前仓库状态
- **编译验证**：go build / go vet / archtest / toolbridge / difftracker / uistate / thread / provider/unified 全绿。
- **P16.1 + P17**：均已完成。

### 8.2 下一会话首任务
1. **git commit** — P16.1 + P17 所有改动

### 8.3 关键守卫参数
| 参数 | 值 |
|------|------|
| 单文件行数 | ≤400 |
| 单函数行数 | ≤80 |
| 圈复杂度 CC | ≤10 |
| 包非测试文件数 | ≤15（claudecli 例外 ≤20） |
| 包总行数 | ≤4500 |

### 8.4 已知限制
| # | 限制 |
|---|------|
| 1 | P16.1 diff 链路已完成，仍建议在真实 Codex/Claude 长会话中继续观察 |
| 2 | IDA 工具族仍待迁移 |
| 3 | P15 稳定性优化仍待处理 |
| 4 | P2 event bus 互审仍待收报告 |

### 8.5 审查规则（本会话确立）
1. **审查员只审不改** — 禁止 lsp_edit
2. **≥2 人确认才修** — 单人发现的先记录
3. **修复由原发现者执行** — 谁发现谁修
4. **Agent 提示词必须包含**：LSP 强制 + 不需要审批 + 守卫 + 死代码清理
