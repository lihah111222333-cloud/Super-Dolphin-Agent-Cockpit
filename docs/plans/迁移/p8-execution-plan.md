# P8 执行计划 v2 — MCP 编排工具族（20 个）

> 修订：2026-03-21（V3 现状行数已回刷；V2 LOC 沿用 2026-03-21 审查口径）

---

## 1. V2 体量（沿用 2026-03-21 审查口径）

| 目录 | 生产代码 |
|---|---:|
| pkg/toolsdk/tools/ 编排相关 | 1,997 |
| pkg/toolsdk/tooladapter/ | 353 |
| internal/mcp/ | 1,543 |
| internal/apiserver/ 编排 backend | 719 |
| **总计** | **4,612** |

## 2. V3 可复用资产

> 注：上表 V2 LOC 沿用 `audit-mcp-orch-tools.md` 的 2026-03-21 审查口径；当前仓库不包含独立第二统计源。当前 V3 侧已按 HEAD 回刷行数与能力描述。

| V3 现有模块 | 行数 / 能力 | 覆盖 |
|---|---:|---|
| orchestration/rpc.go | 217 | agent + task 15 个 RPC |
| workspace/rpc.go | 137 | workspace 7 个 RPC |
| skill/rpc.go (card 部分) | 102 | command card 7 个 RPC |
| skill/cards.go | 199 | card CRUD + run/render helper |
| store/prompt | list/get/upsert/version | 缺 delete/setEnabled |
| store/commandcard | get/list/upsert/delete/version | 缺 MCP tool facade |
| store/sharedfile | get/list/upsert/delete | 缺 module/tool facade |

## 3. 当前状态与 V3 预估（修正后）

| 口径 | 乐观 | 中位 | 悲观 |
|---|---:|---:|---:|
| V3 MCP 壳层（复用现有模块） | 800 | **1,200** | 1,400 |
| V3 含 prompt/shared_file 补全 | 1,100 | **1,500** | 2,200 |

**之前估 ~700 偏低但不离谱（乐观边界），中位应为 ~1,200。当前 `cmd/mcp-orch` 与 `internal/mcpserver/common` 仍是骨架，family wiring 尚未开始。**

## 4. 工具组真实拆分（纠偏）

| 工具组 | V2 实际数 | V2 行数 | V3 复用度 |
|---|---:|---:|---|
| agent 管理 | 5 | 1,180 | 高（orchestration 已有） |
| DAG/task | 4 | 1,052 | 高（dag.go 已实现） |
| prompt 模板 | 2 | 117 | 中（store 有，缺 delete/enable） |
| command 命令卡 | 2 | 116 | 高（skill 模块已有） |
| shared_file | 2 | 123 | 中（store 有，缺 module 层） |
| workspace | 5 | 325 | 高（workspace 已有） |

## 5. Agent 拆分（4 实现）

| Agent | 范围 | 预估行数 |
|---|---|---:|
| 1. wiring | cmd/mcp-orch + family manifest + registry | 200-300 |
| 2. agent+DAG | orchestration_* + task_* | 250-400 |
| 3. workspace | workspace_* 复用现有 rpc | 100-200 |
| 4. prompt+cmd+shared | prompt_* + command_* + shared_file_* | 350-500 |

## 6. 关键风险

- prompt store 缺 Delete/SetEnabled
- shared_file 需要补 module 薄层还是直连 store？
- workspace tool 不能与 module/workspace 分叉

## 7. 代码守卫
每文件 ≤ 400 行，每函数 ≤ 80 行，CC ≤ 10

## 附：P8 前置必修项（延后自 P7 终极验收）

| # | 问题 | 来源 | 预估 |
|---|---|---|---|
| D-1 | sqlc 生成层漂移：threadbinding 缺席、dbquery placeholder、ailog 挂 system_log | verify-align-store-sm | 需新增 migration SQL + regenerate sqlc |
| D-2 | AgentSnapshot port/provider 仅推断非实测 | verify-align-agent | 需 runtime 上报机制 |
| D-3 | 状态机外直写路径封堵 + awaiting_user_input 闭环 | verify-align-store-sm | orchestration+approval+provider 三方联动 |
