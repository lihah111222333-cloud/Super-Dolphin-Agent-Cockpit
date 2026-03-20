# P2 执行计划 — 功能补全 + Improvement 收尾

> 生成时间：2026-03-21
> 前置：P1 全部 Blocker 已清零，S1-S8 系统级修复已完成

---

## 1. 目标

将 P5 波次 2 剩余的 **7 个功能级 Blocker** 和 **8 个 Improvement** 全部闭合，使 V3 RPC 层达到 V2 功能等价的最低可用标准。

---

## 2. 三个执行批次

### 批次 A：workspace 功能补全（B6+B7+B8+B12+I7）

| Blocker | 问题 | 目标文件 | 修复策略 |
|---|---|---|---|
| **B6** | workspace/run/merge 退化为纯状态更新，无真实 merge 语义 | service.go | 参照 V2 workspace.go:306-373 实现 merge 主路径：遍历 workspace 文件 → 对比 source 与 workspace 版本 → 调用 s.store.UpsertFile 持久化 → 返回 merge result 对象 |
| **B7** | workspace/run/abort 丢失 updatedBy/reason | rpc_types.go, contract.go, service.go | 扩展 abortRunParams 增加 updatedBy/reason 字段，AbortRun 签名扩展，service 层传递到 TransitionRunStatusInput.UpdatedBy/Metadata |
| **B8** | CreateRun 不创建 workspace 目录/bootstrap Files | service.go | 参照 V2 workspace.go:176-243，在 CreateRun 中创建 workspace 目录 → 遍历 Files → 复制源文件 → UpsertFile 记录 |
| **B12** | workspace 状态变更 event/notify 平面完全缺失 | service.go, module.go | workspace service 注入 event bus → CreateRun/MergeRun/AbortRun/UpdateRunStatus 成功后发布 typed event → module.go 补 bus 注入 |
| **I7** | ListFilesFilter.State 被吞掉 | rpc_types.go, service.go | listRunFilesParams 加 state 字段，service 传递到 ListFilesFilter.State |

**预估代码量**：~150 行
**V2 参考**：go-agent-v2/internal/service/workspace.go（176-373 行）
**风险**：B6 merge 最复杂（V2 67 行逻辑），B8 目录创建依赖 CWD

---

### 批次 B：orchestration 功能补全（B13+B14+B15）

| Blocker | 问题 | 目标文件 | 修复策略 |
|---|---|---|---|
| **B13** | V2 12 个 agent.* 方法面只有 4 个可调用 | rpc.go, rpc_types.go, contract.go, service.go | 补齐 agent.getState（→Snapshot）、agent.getReport（→lastReport）、agent.rememberReportRequest、agent.reportEvent。注意：agent.submit/submitPrompt 已在 P1 接线 |
| **B14** | task/dag/* 全部 ErrNotImplemented | contract.go, service.go, rpc.go | 4 个 DAG 方法补最小实现：create→UpsertDAG、get→GetDAG、list→ListDAGs、node/update→UpdateNodeStatus。注入 taskdag.Store |
| **B15** | orchestration/report 缺失范围远大于 getter | rpc.go, rpc_types.go, service.go | orchestration/report 改为真实读 lastReport、rememberReportRequest 注册请求者、reportEvent 处理报告事件。复杂归并逻辑先最小版+TODO |

**预估代码量**：~170 行
**V2 参考**：go-agent-v2/internal/apiserver/methods_orchestration.go、orchestration_report.go
**风险**：orchestration/service.go 可能超 400 行 → 拆 dag.go + report.go

---

### 批次 C：skill improvements + 文档（I1-I5+I6+I8）

| Improvement | 问题 | 目标文件 | 修复策略 |
|---|---|---|---|
| **I1** | command/exec 缺 30s timeout/cwd fallback/env 白名单 | skill/exec.go | 加 30s timeout、cwd fallback、env 白名单 |
| **I2** | 远端技能读取缺 http.Client timeout | skill/service.go | 构造时设 http.Client{Timeout: 15*time.Second} |
| **I3** | card 工厂只覆盖 3/7 | skill/rpc.go | 扩展 cardByKey helper 覆盖剩余 4 个 card handler |
| **I4** | auto-match 不是运行时接线 | skill/module.go | TODO 标注 P7 接入事件驱动 |
| **I5** | skills/list vs thread/skills/list 语义重叠 | skill/rpc.go, thread/rpc.go | 加注释说明语义差异 |
| **I6** | workspace service 是 store facade | — | 文档标注，B6/B8 补齐后解决 |
| **I8** | runner 注入链未充分验证 | — | 文档标注，archtest 已覆盖 |

**预估代码量**：~80 行
**V2 参考**：go-agent-v2/internal/apiserver/methods_command.go、go-agent-v2/internal/skills/methods.go

---

## 3. 并行策略

Agent A（workspace）/ Agent B（orchestration）/ Agent C（skill+文档）完全并行无冲突。I7 已归入 Agent A。

## 4. 验证标准

go build + go vet + go test archtest + lsp diagnostics

## 5. 代码守卫

每文件 ≤ 400 行，每函数 ≤ 80 行，嵌套 ≤ 4，CC ≤ 10
orchestration/service.go 超限风险 → 拆 dag.go + report.go

## 6. 补充标注

- workspace.Service 当前是 store thin facade。B6/B8 补齐后 service 将承载 merge 语义和目录管理。
- runner 注入链已由 archtest 的 fx_graph_test.go 覆盖验证。
