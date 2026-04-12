# V3 迁移会话摘要

> 生成时间：2026-04-12（P16 UI Diff + 工具调用记录实施 + P16.1 统一 Diff 计划）
> 会话范围：P0-P9 + P11-P15 + **P16 UI Diff/工具调用记录** + **P16.1 统一 Diff 计划**
> 前序会话 UUID：58fdd978-cc4b-41e6-bd26-d40f3ff66854

---

## 1. 当前结论

### 编译验证：全绿
```
✅ go build ./internal/... ./cmd/...
✅ go vet ./internal/... ./cmd/...
✅ TestCodeSizeGuard — PASS
✅ TestDependencyDirection — PASS
✅ TestMCPOrchDependencyDirection — PASS
✅ TestTimeoutLocality — PASS
✅ TestSqlcBoundary — PASS
✅ go test ./internal/platform/difftracker/... — PASS
✅ go test ./internal/platform/toolbridge/... — PASS
✅ go test ./internal/module/uistate/... — PASS
✅ go test ./internal/module/uistate/timeline/... — PASS
✅ go test ./internal/provider/claudecli/... — PASS
```

### 迁移状态

| 阶段 | 状态 | 内容 |
|---|---|---|
| P0-P15 | ✅ | 全部完成（见前序 session-summary） |
| **P16 Phase 2 工具调用记录** | ✅ | Timeline Kind 映射 + ActivityStats + UIThreadPatch 增量推送 + 前端修正 |
| **P16 Phase 1 Diff（旧实现）** | ❌ 不工作 | difftracker 包运行时未被调用 — 待 P16.1 清理重做 |
| **P16 额外修复** | ✅ | 前端 ReferenceError + Claude stdin pipe + interrupt recovery + enrichFromDB CC |
| **P16.1 统一 Diff 计划** | 📝 v8 审查中 | V2 式主进程 proxy 归一方案，8 轮审查迭代 |

---

## 2. P16 本会话核心成果

### 2.1 P16 Phase 2 — 工具调用记录（✅ 完成）

| 改动 | 文件 | 说明 |
|------|------|------|
| Timeline Kind 映射 | timeline/projector.go | item→command/file, tool_call→tool, approval_request→approval |
| plan/error/user parity | timeline/projector_parity.go | PlanDelta/AgentError/TurnInputReceived 订阅 |
| Item 字段补全 | timeline/timeline.go | +Tool/Preview/ElapsedMS/Output/ExitCode/Done |
| mergeItem 拆分 | timeline/merge.go | CC 从 28 降到 2 |
| ActivityStats | uistate/state.go + projector.go | LSPCalls/Commands/FileEdits/ToolCalls 累积 |
| UIThreadPatch 增量 | uistate/patch.go | timeline delta + activityStats + payload_too_large 降级 |
| Patch DTO | dto/ui/patch_types.go | PatchTimelineItem/PatchActivityStats/PatchAlert |
| 前端 selector 修正 | thread-sync-selectors.js | 移除 item/tool_call/approval_request 过滤 |
| 前端 Kind 兼容 | useThreadCards.js | +tool/approval/file kind 处理 |
| 前端 snapshot 对齐 | thread-snapshot.js | overlay/mainAgent/partial 补接 |
| 前端 live-patch 对齐 | thread-live-patch.js | overlay/active/mainAgent/partial 补接 |
| file status 文案 | useThreadCards.js | saved→已保存/running→修改中/failed→修改失败 |

### 2.2 P16 额外 Bug 修复

| # | Bug | 文件 | 说明 |
|---|-----|------|------|
| 1 | 前端 ReferenceError | thread-sync-helpers.js:235 | regressionByContent 变量作用域修复 |
| 2 | Claude stdin pipe 死亡 | claudecli/session.go + transport.go | readyForSend() + 自动 launchCLI 重启 |
| 3 | Claude interrupt 后无法对话 | claudecli/session.go + session_interrupt_cleanup.go | SIGINT→grace→SIGKILL + transport 清理 + restart 复用 resumeID |
| 4 | enrichFromDB CC=11 | uistate/module.go | 拆 applyBindingToThreadRuntime + ensureThreadRuntime |
| 5 | interrupt 窗口 I1-I4 | claudecli/session.go + toolbridge | pidRegistry 注册/注销 + Recoverable 检查 + 并发保护 |

### 2.3 P16 Phase 1 Diff — 旧实现（❌ 运行时不工作）

**发现的问题**：
1. toolbridge.routeToolCall 在运行时从未被调用
2. Claude Agent 的 tool call 完全绕过 toolbridge
3. Codex 和 Claude 维护两套 diff 不合理
4. 前端 syncThreadState 有 ReferenceError（已修复）

**代码已写入仓库但运行时不生效**：
- `internal/platform/difftracker/` — 整个包（待 P16.1 清理）
- toolbridge diff 集成（待 P16.1 回退）

---

## 3. P16.1 统一 Diff 计划（v8 审查中）

### 3.1 核心设计

借鉴 V2 做法 + Claude Code 官方实现，统一 Codex/Claude 到一条路径：

```
Codex: session → toolbridge.routeToolCall → peer → mcp-lsp
Claude: CLI → proxy /mcp/<family>/<agentId> → toolbridge.routeToolCall → peer → mcp-lsp

toolbridge.routeToolCall（统一入口）：
  replace_range/rename → before 读文件 → 执行 → after 读文件 → diff → bus.Publish
```

### 3.2 关键设计决策

| 决策 | 原因 |
|------|------|
| 主进程 proxy（不做 MCP session 基建） | MCP server 缺连接级 session，基建工程量太大 |
| URL path `/mcp/<family>/<agentId>` 注入 agentId | 零基建，从 URL 直接提取 |
| diff 在主进程 toolbridge 内做（不在 MCP server 内） | 主进程天然有 agentId |
| ManifestContext 新增 ProxyHTTPAddr | driver.go:62-70 + :85-91 传入 |
| PeerHTTPAddrs 保留作 fallback | 归一但保留降级路径 |

### 3.3 执行计划

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 0 | 清理旧 difftracker + 回退 toolbridge diff 代码 | 待执行 |
| Phase 1 | proxy server + manifest 指向 proxy + agentId 透传 + diff tracking | 待执行 |
| Phase 2 | git diff 兜底（可选增强） | 待执行 |

### 3.4 审查历程（8 轮）

| 版本 | 方案 | 结果 | 核心问题 |
|------|------|:----:|----------|
| v1 | hooks 优先 + git 兜底 | 部分通过 | 架构 OK，落点缺 |
| v2 | v1 + 审查修正 | 部分通过 | metadata 没打通 |
| v3 | MCP session 基建 | 2 不通过 | MCP server 缺 session |
| v4 | MCP session 归一 | 2 不通过 | 同上 + 落点悬空 |
| v5 | 补基建 | 2 不通过 | OnCustomEvent 不存在 |
| v6 | V2 式主进程 proxy | 3 不通过 | 落点偏差 |
| v7 | 精确落点 | 3 不通过 | driver.go 落点 + tools/list |
| v8 | 全部落点钉死 | 审查中 | — |

---

## 4. 未完成

| 项 | 状态 | 说明 |
|---|---|---|
| **P16.1 统一 Diff** | 📝 v8 审查中 | 通过后执行 Phase 0→1→2 |
| P16 旧 diff 清理 | ⏳ | P16.1 Phase 0 |
| IDA 工具族 | ⏳ | 82 个工具，暂缓 |
| P2 event bus 互审 | 🔄 | 待收报告 |
| P15 后续待办 | ⏳ | DeferLoading / peer 路由 / 并发限流 |
| git commit | ⏳ | P16 所有改动待提交 |

---

## 5. 下一步

1. **P16.1 v8 审查通过后** → Phase 0 清理旧 diff → Phase 1 实施 proxy + diff → Phase 2 git 兜底
2. **git commit** — P16 Phase 2 成果 + bug fixes 先提交
3. **P15 稳定性** — peer sweeper 回收重启观察

---

## 6. 关键文档清单

| 文档 | 路径 |
|------|------|
| P16 执行计划 | docs/plans/迁移/p16-ui-diff-toolcall-plan.md |
| **P16.1 统一 Diff 计划（v8）** | **docs/plans/迁移/p16.1-unified-diff-plan.md** |
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
| **本轮合计** | **~120+** |
| **总计** | **~771+** |

---

## 8. 会话交接

### 8.1 当前仓库状态
- **编译**：go build ✅ / go vet ✅ / archtest 全绿
- **P16 Phase 2**：完整落地，Timeline/Stats/UIThreadPatch 全链路可用
- **P16 Diff**：旧实现在仓库但运行时不工作，待 P16.1 清理重做
- **前端**：ReferenceError 已修，selector/cards/live-patch/snapshot 已对齐
- **Claude CLI**：stdin pipe 死亡重建 + interrupt recovery 已修

### 8.2 下一会话首任务
1. 收 P16.1 v8 审查报告
2. 通过后执行 P16.1 Phase 0（清理旧 diff）
3. 执行 P16.1 Phase 1（proxy + 统一 diff）
4. git commit

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
| 1 | P16 旧 difftracker 在仓库但不工作 |
| 2 | Claude Agent diff 面板始终空白（待 P16.1） |
| 3 | Codex Agent diff 面板始终空白（待 P16.1） |
| 4 | 工具调用记录在前端 ReferenceError 修复后应该可用（需重编译验证） |

### 8.5 审查规则（本会话确立）
1. **审查员只审不改** — 禁止 lsp_edit
2. **≥2 人确认才修** — 单人发现的先记录
3. **修复由原发现者执行** — 谁发现谁修
4. **Agent 提示词必须包含**：LSP 强制 + 不需要审批 + 守卫 + 死代码清理
