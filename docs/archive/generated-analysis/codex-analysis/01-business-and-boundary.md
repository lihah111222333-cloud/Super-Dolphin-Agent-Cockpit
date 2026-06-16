# 业务目标与系统边界

## 1. 本阶段目标

从仓库证据识别项目服务对象、核心业务场景、输入/处理/输出，以及系统边界。

## 2. 已读取文件

- `README.md`
- `frontend-app/src/App.jsx`
- `frontend-app/src/entities/client/model/useClientStore.js`
- `frontend-app/src/shared/api/backendApi.js`
- `internal/app/modules.go`
- `cmd/mcp-orch/tools/registry.go`
- `cmd/mcp-orch/tools/orchestration_tools.go`
- `cmd/mcp-orch/tools/task_tools.go`

## 3. 关键发现

- 一句话总结：Super-Dolphin 是面向本地/桌面开发场景的 AI 多代理编排平台，用于创建线程、驱动 provider 会话、编排 DAG/自动化、管理技能、记忆、共享文件和可观测性。
- 目标用户：AI 辅助开发操作者、需要多代理任务编排的工程用户、需要本地 Codex/Claude provider 会话管理的开发者。
- 核心业务场景：聊天线程启动与回合执行、自动化 DAG/cron、技能管理、prompt 管理、记忆中心、共享文件、链路追踪、mcp-orch 子代理工具调用。
- 主要输入：用户 prompt、cwd、provider/model/effort 配置、技能/提示词/记忆、DAG 定义、共享文件路径、RPC 调用参数。
- 主要输出：provider 流式事件、线程/回合状态、工具调用结果、DAG 运行状态、日志/trace、共享文件和 UI 投影。

## 4. 证据说明

| 业务判断 | 文件 |
|---|---|
| 多代理编排、session、tool execution、cron、memory 是项目定位 | `README.md` |
| UI 页面包含 Chat、技能、提示词、自动化、记忆、共享文件、链路追踪、设置 | `frontend-app/src/App.jsx` |
| 前端通过 `thread/start`、`turn/start`、DAG、cron、skill、memory 等 RPC 方法驱动业务 | `frontend-app/src/shared/api/backendApi.js` |
| 后端组装 dashboard、memory、prompt、skill、thread、turn、cron、notify、insight、uistate | `internal/app/modules.go` |
| mcp-orch 暴露 agent、task、workspace、prompt、recall、command、shared file、video/TTS 等工具 | `cmd/mcp-orch/tools/registry.go` |

## 5. 风险与问题

- P1：系统能力边界很宽，UI、provider、MCP、DB、LSP、自动化混合在一个仓库内，变更影响面容易被低估。
- P1：mcp-orch 工具可启动 agent、创建 DAG、读写 shared file，权限与 cwd 约束必须持续保持 fail-fast。
- P2：项目“非目标范围”主要靠文档和 guard 约束，仓库内没有生产部署边界说明。

## 6. 无法判断的信息

- 无法判断真实生产用户规模、SLA、成本指标和外部服务使用额度。
- 无法判断是否存在远端 SaaS 控制面；当前仓库证据更偏本地桌面/本地服务。

## 7. 下一阶段建议

继续梳理技术栈与目录结构，明确哪些目录是业务、基础设施、配置、测试和历史遗留。
