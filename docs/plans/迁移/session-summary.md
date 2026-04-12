# V3 迁移会话摘要

> 生成时间：2026-04-12（P16 UI Diff + 工具调用记录 + P16.1 统一 Diff 完成）
> 会话范围：P0-P9 + P11-P15 + **P16 UI Diff/工具调用记录** + **P16.1 统一 Diff 完成**
> 前序会话 UUID：58fdd978-cc4b-41e6-bd26-d40f3ff66854

---

## 1. 当前结论

### 编译验证：全绿（2026-04-12 实跑）
```
✅ go build ./internal/... ./cmd/... — PASS
✅ go vet ./internal/... ./cmd/... — PASS
✅ go test -run TestCodeSizeGuard ./internal/archtest/... — PASS (internal/archtest 0.701s)
✅ go test ./internal/platform/toolbridge/... — PASS (cached)
✅ go test ./internal/platform/difftracker/... — PASS (0.608s)
✅ go test ./internal/module/uistate/... — PASS (uistate 1.277s, timeline 0.777s)
```

### 迁移状态

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0-P15 | ✅ | 全部完成（见前序 session-summary） |
| **P16 Phase 2 工具调用记录** | ✅ | Timeline Kind 映射 + ActivityStats + UIThreadPatch 增量推送 + 前端修正 |
| **P16 Phase 1 Diff（旧实现）** | 🧹 已清理 | P16.1 Phase 0 删除旧 diff 实现，保留 difftracker 核心 git 能力 |
| **P16 额外修复** | ✅ | 前端 ReferenceError + Claude stdin pipe + interrupt recovery + enrichFromDB CC |
| **P16.1 统一 Diff** | ✅ | **Phase 0 清理旧 diff(-1730行) + Phase 1 proxy+manifest归一+diff tracking+安全加固 + Phase 2 git diff 兜底。11轮计划审查(v1→v11) + 3波实施 + 1:4互审 + 修复** |

---

## 2. P16.1 本会话核心成果

### 2.1 Phase 0 — 旧 diff 清理

- 清理旧 diff 实现：删除 `aggregator/unified/tool_context/hooks_extract` 与 `claude_diff` 旧链路。
- 删除旧测试与死代码，净减约 `-1730` 行。
- `handler.go` 从 aggregator 迁移到 emitter；`module.go` 删除旧 lifecycle。
- 保留 difftracker 核心：`git_diff.go` / `git_ops.go` / `types.go` / `resolver.go`。

### 2.2 Phase 1 — Proxy + Manifest 归一 + Diff Tracking

- 新建 `internal/platform/toolbridge/proxy.go`：MCP HTTP proxy，覆盖 `initialize` / `notifications/initialized` / `tools/list` / `tools/call`，通过 `/mcp/<family>/<agentId>` 注入 agentId。
- Manifest 归一：`ProxyHTTPAddr` 优先；`PeerHTTPAddrs` 删除；sidecar fallback 封死，不再绕回旧 peer 地址。
- Diff tracking：`routeToolCall` 作为统一入口执行 `BeginSnapshot` / `EmitGitDiff`；复用 `DiffEmitter` 发布 `ToolDiffUpdated`。
- 安全加固：`http.MaxBytesReader`、tool call timeout、family 路由校验、params 校验、panic `recover` 保护。

### 2.3 Phase 2 — Git Diff 兜底

- 订阅 `ToolCallEnd`，对 `code_run` / `code_run_test` / `lsp_edit` 做后置 git diff fallback。
- `callId` 去重：Phase 1 已 emit 的 call 不再由 fallback 重复发 diff。
- 新增/复用 `EmitCurrentGitDiff`，无需 before snapshot 即可对当前 working tree 与 HEAD 生成兜底 diff。

### 2.4 互审与修复

- 1:4 互审发现编译失败与 proxy 安全问题，已完成修复。
- 已修复编译失败、proxy method/family/params 校验、请求体大小、timeout、panic recover 等问题。
- 11 轮计划审查（v1→v11）+ 3 波实施 + 1:4 互审 + 修复已完成。

### 2.5 删除死代码

- 删除 `peer_discovery.go`。
- 删除 difftracker 旧链路 9 个文件，仅保留核心 git diff 能力。

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
| **P16.1 统一 Diff** | ✅ 已完成 | Phase 0→1→2 已落地并验证 |
| P16 旧 diff 清理 | ⏳ | P16.1 Phase 0 |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |
| P2 event bus 互审 | 🔄 | 待收报告 |
| P15 后续待办 | ⏳ | DeferLoading / peer 路由 / 并发限流 |
| git commit | ⏳ | P16 所有改动待提交 |

---

## 5. 下一步

1. **git commit** — P16.1 所有改动待提交（本任务）
2. **P15 稳定性优化**
3. **IDA 工具族**

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
| **本轮新增** | |
| P16.1 计划审查（v8→v11，4轮 × 4 Agent） | ~16 |
| P16.1 Phase 0 实施 | 1 |
| P16.1 Phase 1 实施（2 并行） | 2 |
| P16.1 Phase 1 互审（1:4） | 4 |
| P16.1 Phase 1 修复 | 2 |
| P16.1 Phase 2 实施 | 1 |
| P16.1 本轮新增合计 | ~26 |
| **本轮合计** | **~146+** |
| **总计** | **~797+** |

---

## 8. 会话交接

### 8.1 当前仓库状态
- **编译**：go build ✅ / go vet ✅ / 指定 archtest/toolbridge/difftracker/uistate 测试全绿
- **P16.1 统一 Diff**：已完成 Phase 0 清理、Phase 1 proxy + manifest 归一 + diff tracking、Phase 2 git diff 兜底
- **P16 Phase 2**：完整落地，Timeline/Stats/UIThreadPatch 全链路可用
- **前端**：ReferenceError 已修，selector/cards/live-patch/snapshot 已对齐
- **Claude CLI**：stdin pipe 死亡重建 + interrupt recovery 已修

### 8.2 下一会话首任务
1. 若本任务 commit 已完成，下一会话直接进入 **P15 稳定性优化**
2. 继续处理 **IDA 工具族**
3. 视需要做 P16.1 真实 Codex/Claude 工具调用观察

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
