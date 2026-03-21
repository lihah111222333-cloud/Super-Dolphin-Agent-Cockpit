# P7 执行计划 v2 — V2 兼容收尾 + Dashboard/UI State

> 修订：2026-03-21
> 变更：MCP 工具族拆出为 P8(编排) / P9(LSP) / IDA(暂缓)
> 当前状态：P7w1（V2 兼容收尾）✅ 已完成；剩余 P7w2 Dashboard/UI State + P7w3 集成测试

---

## 范围调整

| 原 P7 | 新归属 |
|---|---|
| 波次 1 V2 兼容收尾 D1-D12 | ✅ 已完成 |
| 波次 2 MCP 编排工具 (20个) | → **P8** |
| 波次 2 MCP LSP 工具 (9个) | → **P9** |
| 波次 2 MCP IDA 工具 (82个) | → **暂缓** |
| 波次 3 Dashboard + UI State | **保留 P7** |
| 波次 4 集成测试 | **保留 P7** |

## P7 剩余内容

### 波次 2（原波次 3）：Dashboard + UI State
- uistate：前端状态同步（thread list/agent list/turn status）
- dashboard：监控面板（agent 健康/性能/日志）
- 预估 ~1500 行

### 波次 3（原波次 4）：集成测试
- 全量 handler 注册完整性测试
- V2 契约测试迁移
- smoke test

## 整体路线图

```
P0-P6  ✅ 已完成（代码已落地并可构建）
P7     Wave 1 ✅ + Wave 2 Dashboard/UI State + Wave 3 集成测试
P8     MCP 编排工具族（20 个，待开工）
P9     MCP LSP 工具族（9 个，待开工）
IDA    暂缓（82 个，独立 server 不影响核心）
```
