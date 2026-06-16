# Step 1-2：交接目标与项目资产收集

## Step 1：明确交接目标

### 交接目标

接手方应能在不依赖口头说明的情况下完成以下动作：

1. 理解 `Super-Dolphin` 的业务定位、运行形态和核心模块边界。
2. 在本地按仓库脚本启动当前新 UI 桌面开发环境。
3. 跑通最小核心流程：进入 UI、创建或打开线程、发起 turn、观察事件和日志、查看 DAG 或任务状态。
4. 理解数据库表、迁移、回滚限制和 sqlc 生成边界。
5. 知道 CI、质量门禁、打包、发布、日志、指标和故障处理入口。
6. 能维护本目录下的交接文档包，并把评审反馈转化为长期知识库内容。

### 当前交接边界

- 仓库根目录：`D:\project\Super-Dolphin`
- 当前 Go module：`github.com/anthropic-ai/super-agent-v3`
- 当前新 UI：`frontend-app`
- 旧嵌入式前端：`cmd/agent-terminal/frontend`
- 桌面宿主入口：`cmd/agent-terminal`
- 编排 peer：`cmd/mcp-orch`
- LSP peer：`cmd/mcp-lsp`
- IDA peer：`cmd/mcp-ida`

### 成功验收标准

| 项 | 验收方式 |
| --- | --- |
| 文档完整 | 本目录包含 Step 1 到 Step 18 对应文档 |
| 路径准确 | 所有入口、脚本、模块、配置路径均能在当前仓库定位 |
| 风险透明 | 未执行、未验证、权限未知和环境漂移项均被列出 |
| 可执行 | 启动、测试、发布、回滚、故障处理均有命令或检查清单 |
| 可维护 | 文档说明何时更新、由谁评审、如何沉淀到知识库 |

## Step 2：收集项目文档、代码、环境、权限

### 代码和文档资产

| 类别 | 位置 | 说明 |
| --- | --- | --- |
| 项目总览 | `README.md` | 产品定位、目录、快速启动、构建和测试说明 |
| 仓库规则 | `AGENTS.md` | Codex 修改仓库时的行为规则、命令策略和验证要求 |
| 代码地图 | `docs/doc/codemap/README.md` | 大型子系统阅读入口和分卷索引 |
| 当前 UI | `frontend-app` | React/Vite 新 UI，`run-new-ui-desktop.sh` 会代理到该 Vite 服务 |
| 旧 UI | `cmd/agent-terminal/frontend` | 旧 Vue/Vite 嵌入式前端，仅在明确目标为旧前端时修改 |
| 数据库迁移 | `migrations`、`sql`、`sqlc.yaml` | PostgreSQL schema、sqlc query 和生成配置 |
| 发布脚本 | `scripts/package_*`、`scripts/publish_github_release.sh` | Windows/macOS/Linux 打包和 GitHub Release 发布 |
| 本地日志栈 | `deploy/elk`、`scripts/elk-local.ps1` | 本地 Elasticsearch/Kibana/Logstash 日志查看 |

### 本地环境快照

本次读取到的当前机器状态：

| 项 | 当前值 | 风险 |
| --- | --- | --- |
| 仓库根目录 | `D:/project/Super-Dolphin` | 无 |
| Git 分支 | `main` | 直接在 main 上工作需谨慎 |
| HEAD | `47ee7608` | 后续文档应记录更新后的 commit |
| Go | `go1.26.3 windows/amd64` | `go.mod` 声明 `go 1.25.7`，存在版本漂移 |
| Node | `v26.1.0` | README 提到 Node 20+，CI 使用 Node 20，需确认本地兼容性 |
| npm | `11.13.0` | 与 CI 不完全一致 |
| `.env` | 未发现 | 启动时可能依赖脚本默认值或本机外部环境 |
| `frontend-app/package-lock.json` | 存在 | 前端安装应使用 `npm ci` 或仓库既有脚本 |
| Git hooksPath | 指向 `D:/project/Super-Dolphin-worktrees/chat-windows-dialogue/.githooks` | 当前 checkout 的 hooksPath 指向其他 worktree，需运行 `make install-hooks` 修正 |

### 必需权限清单

| 权限 | 用途 | 验证方式 |
| --- | --- | --- |
| Git 仓库读写 | 拉取、提交、推送、PR | `git remote -v`、`git fetch`、推送前确认分支策略 |
| GitHub Actions/Release | CI、发布资产、Release 管理 | `gh auth status`、Release dry-run |
| PostgreSQL 本地或嵌入式运行权限 | 本地启动、迁移、测试 | `DATABASE_URL` 或脚本自动嵌入式 Postgres |
| Claude CLI 登录态 | Claude provider 运行 | `claude` 相关 provider smoke，不在本次验证范围 |
| Codex CLI 登录态 | Codex provider 运行 | `codex` provider smoke，不在本次验证范围 |
| 代码签名或 notarization 权限 | macOS/Windows 发布 | 打包脚本和密钥配置检查 |
| 端口占用权限 | 本地 HTTP/RPC/Vite/ELK | `4511/4512/8092/5175/9200/5601` 等端口检查 |

### 当前仓库状态注意事项

本次读取前仓库已有未提交变更和新增文件，且不属于本 SOP 任务范围。交接文档维护时只应修改：

- `docs/审查报告/前端项目资料/project-sop/**`

不要清理、格式化或回滚其他目录的变更，除非对应变更的所有者明确要求。
