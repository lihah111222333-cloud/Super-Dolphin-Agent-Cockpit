# super-agent-v3 代码地图

> 由自动索引脚本维护，当前覆盖 18 卷核心模块。

## 阅读边界提示

- **02**：先看 sidecar / registry / bootstrap / tools 暴露；不展开 `internal/module/{memory,prompt,thread}` 的内部组装链。
- **07**：先看 `internal/module` 的职责切面、消费面与入口边界；07 已拆成读侧/写侧两份子卷。
- **10**：先看 store / sql / migrations 的持久化 contract 与实现，回答“是否落库、落到哪里”。
- **11**：再看 `start / resume / fork` 中 memory / prompt / thread / prompt snapshot / provider bridge 的运行态串联，回答“运行时到底怎么接上”。

## 目录

| # | 文件 | 覆盖区域 |
|---|---|---|
| 01 | [01-terminal-ui-go.md](01-terminal-ui-go.md) | super-agent-v3 代码地图：终端入口与 UI 层（Go / Wails） |
| 01 | [01-terminal-ui-vue.md](01-terminal-ui-vue.md) | super-agent-v3 代码地图：终端入口与 UI 层（Vue 前端） |
| 01 | [01-terminal-ui.md](01-terminal-ui.md) | super-agent-v3 代码地图：终端入口与 UI 层 |
| 02 | [02-mcp-orch.md](02-mcp-orch.md) | mcp-orch 代码地图 |
| 03 | [03-mcp-lsp-ida.md](03-mcp-lsp-ida.md) | super-agent-v3 代码地图（03） |
| 04 | [04-app-contract.md](04-app-contract.md) | 04 App 核心与契约层代码地图 |
| 05 | [05-dto.md](05-dto.md) | 05 DTO 数据传输对象层代码地图 |
| 06 | [06-mcpserver.md](06-mcpserver.md) | 06 MCP Server 框架层代码地图 |
| 07 | [07-module-read.md](07-module-read.md) | 07A 业务模块层代码地图（读侧） |
| 07 | [07-module-write.md](07-module-write.md) | 07B 业务模块层代码地图（写侧） |
| 07 | [07-module.md](07-module.md) | 07 业务模块层代码地图（拆卷索引） |
| 08 | [08-platform.md](08-platform.md) | 08 Platform 基础设施层代码地图 |
| 09 | [09-provider.md](09-provider.md) | 09 Provider 集成层代码地图 |
| 10 | [10-store.md](10-store.md) | 10. 数据存储层代码地图 |
| 11 | [11-memory-prompt-thread.md](11-memory-prompt-thread.md) | 11 Memory / Prompt / Thread 拆卷索引 |
| 11 | [11-memory.md](11-memory.md) | 11A Memory 代码地图 |
| 11 | [11-prompt-thread.md](11-prompt-thread.md) | 11B Prompt / Thread 代码地图 |
| 12 | [12-dream-pipeline.md](12-dream-pipeline.md) | 12 Dream Pipeline 代码地图 |

## 生成时间

2026-04-28
