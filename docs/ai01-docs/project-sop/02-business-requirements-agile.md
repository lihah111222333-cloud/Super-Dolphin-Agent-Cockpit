# Step 3-4、14：业务目标、需求与 Agile 流程

## Step 3：业务目标、用户角色、核心流程

### 业务目标

`Super-Dolphin` 是本地优先的多 Agent 编排和桌面控制平台。当前代码体现的核心目标包括：

1. 通过桌面 UI 管理会话、线程、turn、prompt、skill、memory、shared file 和 DAG。
2. 通过 provider 层对接 Codex、Claude CLI、DreamExec 等执行后端。
3. 通过 `mcp-orch` 管理 agent 生命周期、DAG、cron、工具调用和资源访问。
4. 通过 `mcp-lsp` 提供多语言代码导航、结构化读取、xref、grep、edit、completion 等能力。
5. 通过数据库、事件总线、日志、指标和前端观测页形成可追踪的本地运行闭环。

### 用户角色

| 角色 | 目标 | 主要入口 |
| --- | --- | --- |
| 本地操作者 | 启动桌面应用、管理线程、运行 agent | `run-new-ui-desktop.sh`、`run-new-ui-desktop.ps1`、`frontend-app` |
| Agent 使用者 | 发起任务、查看上下文、处理审批 | Chat、Workflow、Files、Observability 页面 |
| Prompt/Skill 维护者 | 管理 prompt 模板、section、skill 文件和候选建议 | Prompt、Skills 页面和对应 RPC |
| 编排维护者 | 创建 DAG、调度节点、处理 cron 和 agent lifecycle | `cmd/mcp-orch`、Workflow 页面 |
| 质量和发布负责人 | 运行 guard、测试、打包、发布、回滚 | `Makefile`、`.github/workflows/ci.yml`、`scripts/package_*` |
| 故障处理负责人 | 查看日志、指标、慢请求、错误和 ELK | `/metrics`、Observability 页面、`deploy/elk` |

### 核心业务流程

| 流程 | 简述 | 关键模块 |
| --- | --- | --- |
| 本地启动 | 启动 Vite、新 UI、后端宿主、peer binaries、SQLite | `run-new-ui-desktop.sh`、`run-new-ui-desktop.ps1`、`cmd/agent-terminal` |
| 线程执行 | 用户选择项目和 provider，创建 thread，发起 turn，provider 执行并回传事件 | `frontend-app`、`internal/module/thread`、`internal/provider` |
| DAG 编排 | 创建 DAG，启动节点，调度 agent，更新节点状态，记录运行 | `cmd/mcp-orch`、`internal/module/cron`、`internal/store` |
| Prompt 管理 | 列表、读写、删除 prompt 和 section，提交 intent draft | `internal/module/prompt`、`frontend-app/src/pages/prompts` |
| Skill 管理 | 本地 skill 文件读写、导入、创建和解析建议 | `internal/module/skill`、`frontend-app/src/pages/skills` |
| Memory 和文件 | 读取 memory、shared file，管理工作文件和运行文件 | `internal/module/memory`、Files 页面 |
| 观测和诊断 | 查看 trace、slow、error、recent、status、日志和指标 | `internal/module/observability`、`/metrics`、ELK |
| 打包发布 | 前端构建、Go 构建、runtime manifest、平台包、Release 上传 | `Makefile`、`scripts/package_*` |

## Step 4：需求、用户故事、验收标准

### 功能需求基线

| 编号 | 需求 | 用户故事 | 验收标准 |
| --- | --- | --- | --- |
| FR-01 | 桌面应用可启动 | 作为本地操作者，我要一条命令启动新 UI 开发环境 | `/metrics` 返回成功，UI 可访问，后端日志无启动 fatal |
| FR-02 | 线程可执行 | 作为 Agent 使用者，我要创建 thread 并发起 turn | UI 展示 turn 状态，数据库有 thread/interaction 记录，日志可追踪 |
| FR-03 | provider 可切换 | 作为操作者，我要选择 Codex 或 Claude 等 provider | provider binding 正确写入，失败时明确报错，不静默降级 |
| FR-04 | DAG 可管理 | 作为编排维护者，我要创建、启动、查看和删除 DAG | DAG、node、run 状态一致，失败节点可定位错误 |
| FR-05 | Cron 可调度 | 作为编排维护者，我要配置定时任务 | `cron_jobs` 和 `cron_job_runs` 可追踪，时区语义明确 |
| FR-06 | Prompt 可维护 | 作为 prompt 维护者，我要管理模板、版本和 section | 读写删除 RPC 成功，版本和 section 关系可追踪 |
| FR-07 | Skill 可维护 | 作为 skill 维护者，我要读写本地 skill、导入目录、生成建议 | 文件变化可见，解析失败明确暴露 |
| FR-08 | 数据可迁移 | 作为维护者，我要自动应用向上迁移并知道如何回滚 | schema version 达到最低要求，回滚使用人工 SQL runbook |
| FR-09 | 质量门禁可靠 | 作为 reviewer，我要变更前后有 guard、测试、lint 或 build 证据 | 对应命令通过，失败项不被忽略 |
| FR-10 | 可观测性可用 | 作为故障处理者，我要看指标、日志、慢请求、错误 | `/metrics`、Observability 页面和本地日志可定位问题 |

### 非功能需求

| 类别 | 要求 | 验收方式 |
| --- | --- | --- |
| Fail-fast | 配置缺失、数据缺失、异常状态应显式失败 | 启动脚本和后端不使用静默兜底 |
| 可审计 | 线程、DAG、日志、审批、工具调用有记录 | 数据库表和 observability RPC 可查询 |
| 本地优先 | 开发环境可在本机启动必要服务 | `run-new-ui-desktop.sh` 自动初始化本地 SQLite 和 peer binaries |
| 最小变更 | 修复或功能开发只改必要文件 | diff review 和 guard 检查 |
| 跨平台发布 | Windows/macOS/Linux 有对应打包脚本 | package 脚本 dry-run 或验证脚本通过 |

## Step 14：Agile Backlog、Sprint、Release 流程

### Backlog 结构

建议每个 Backlog item 至少包含：

- 背景和用户角色。
- 明确范围和非范围。
- 相关路径，例如 `internal/module/thread` 或 `frontend-app/src/pages/workflows`。
- 验收标准。
- 风险和权限需求。
- 需要更新的文档。

### Sprint 执行流程

1. Grooming：按业务价值、风险、受影响模块排序。
2. Ready 检查：确认需求可验证，测试策略明确，无未解决权限阻塞。
3. 实现：遵循仓库 `AGENTS.md` 的最小变更、先读代码、先确认假设原则。
4. 验证：运行受影响面对应命令，不用脚本成功替代真实结果。
5. Review：检查 diff、测试证据、迁移和回滚说明。
6. Merge：确保 hooksPath、pre-commit、CI、工作区状态符合要求。

### Release 流程

1. Freeze：锁定 Release 范围，确认 migration、runtime manifest、前端构建和 peer binaries 状态。
2. Build：按平台运行 `scripts/package_windows.ps1`、`scripts/package_macos.sh` 或 `scripts/package_linux.sh`。
3. Verify：运行对应 `scripts/verify_packaged_app_*`，检查 manifest、bundled tools、LSP 和 Codex runtime。
4. Publish：使用 `scripts/publish_github_release.sh --dry-run` 后再执行真实发布。
5. Observe：发布后检查 issue、日志、崩溃报告、用户反馈和版本升级路径。
6. Rollback：应用级回滚走旧版本包；数据库回滚需停止应用并恢复已验证的同版本 SQLite 文件备份。
