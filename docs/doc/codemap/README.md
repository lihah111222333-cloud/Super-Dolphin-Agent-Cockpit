# super-agent-v3 代码地图

> 由自动索引脚本维护，当前覆盖 19 卷核心模块。

## 阅读边界提示

- **01 / frontend**：当前且唯一的前端源码在 `frontend-app/`；`cmd/agent-terminal/web-dist/` 仅是 Go embed 构建产物同步目录。前端页面修改优先看 `frontend-app` 与 `01-terminal-ui-react.md`。
- **02**：先看 sidecar / registry / bootstrap / tools 暴露；不展开 `internal/module/{memory,prompt,thread}` 的内部组装链。
- **03**：`cmd/mcp-lsp` 是 generic multi-language LSP peer；阅读时按通用多语言 LSP peer 入口处理，不把它收窄成单一语言服务。
- **07**：先看 `internal/module` 的职责切面、消费面与入口边界；07 已拆成读侧/写侧两份子卷。
- **10**：先看 store / sql / migrations 的持久化 contract 与实现，回答“是否落库、落到哪里”。
- **11**：再看 `start / resume / fork` 中 memory / prompt / thread / prompt snapshot / provider bridge 的运行态串联，回答“运行时到底怎么接上”。

## 目录

- [AI_PROJECT_MAP.md](project-map/AI_PROJECT_MAP.md)：全仓文件级 AI 项目地图，按领域输出 TSV 索引和漂移报告。
- [capability_manifest.json](capability-contract/capability_manifest.json)：核心 Go 领域的符号级能力契约清单。

| # | 文件 | 覆盖区域 |
|---|---|---|
| 01 | [01-terminal-ui-go.md](01-terminal-ui-go.md) | super-agent-v3 代码地图：终端入口与 UI 层（Go / Wails） |
| 01 | [01-terminal-ui-react.md](01-terminal-ui-react.md) | super-agent-v3 代码地图：终端入口与 UI 层（当前 React/Vite 新 UI） |
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
| 13 | [13-archtest-boundaries.md](13-archtest-boundaries.md) | 13 Archtest 后端边界规则地图 |

## 生成时间

2026-07-19
