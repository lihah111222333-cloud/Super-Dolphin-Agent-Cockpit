# Super Agent v3 项目指令

## 本地技能索引

> 使用方法：Agent 应根据上下文语义、任务类型或涉及的文件自动加载并应用对应的技能。
> 也可以通过 `@触发词` 或 `请参考 <path>` 显式指定，但系统默认支持基于上下文的自动感知触发。

| 触发词 | 技能路径 |
|--------|----------|
| @后端 | `.agent/skills/后端/SKILL.md` |
| @Agent工程学 | `.agent/skills/Agent工程学/SKILL.md` |
| @MCP协议 | `.agent/skills/MCP协议/SKILL.md` |
| @Vue3 | `.agent/skills/vue3/SKILL.md` |
| @UI设计 | `.agent/skills/ui-ux-design/SKILL.md` |
| @测试驱动开发 | `.agent/skills/测试驱动开发/SKILL.md` |
| @编写计划 | `.agent/skills/编写计划/SKILL.md` |
| @执行计划 | `.agent/skills/执行计划/SKILL.md` |
| @调度并行代理 | `.agent/skills/调度并行代理/SKILL.md` |
| @子代理驱动开发 | `.agent/skills/子代理驱动开发/SKILL.md` |
| @系统化调试 | `.agent/skills/系统化调试/SKILL.md` |
| @头脑风暴 | `.agent/skills/头脑风暴/SKILL.md` |
| @思维与决策辅助 | `.agent/skills/思维与决策辅助/SKILL.md` |
| @请求代码审查 | `.agent/skills/请求代码审查/SKILL.md` |
| @接收代码审查 | `.agent/skills/接收代码审查/SKILL.md` |
| @完成前验证 | `.agent/skills/完成前验证/SKILL.md` |
| @使用git工作区 | `.agent/skills/使用git工作区/SKILL.md` |
| @使用超能力 | `.agent/skills/使用超能力/SKILL.md` |
| @结束开发分支 | `.agent/skills/结束开发分支/SKILL.md` |
| @编写技能 | `.agent/skills/编写技能/SKILL.md` |
| @安全工程师 | `.agent/skills/安全工程师规范/SKILL.md` |
| @核心信息提取与总结 | `.agent/skills/核心信息提取与总结/SKILL.md` |

### 技能系统说明

- 上表规定了 agent 在本仓库内读取 `.agent/skills/**/SKILL.md` 的触发映射关系。
- 产品运行时的技能系统：canonical 真值由本项目管理，项目级在 `<cwd>/.agent/skills`，生效的个人级在 `~/.super-dolphin/skills/personal/{user,agent,imported}`；`personal/hub` 仅作为目录/市场来源，不参与扫描、镜像或 provider 调用。生效 canonical 再 reconcile 到生成型 provider-native mirror。Claude 通过 `<cwd>/.claude/skills` 和 `~/.claude/skills` 发现，Codex 通过 `<cwd>/.agents/skills` 和 `~/.agents/skills` 发现；显式配置 provider home 时才使用该 home 下的 `skills`；mirror 不是 canonical 真值。
- 涉及运行时技能行为时，以 `internal/module/skill*`、`internal/provider/shared/provider_home.go`、provider mirror 测试和 toolbridge 兼容性测试为准；`skill_read_section` 不是生产 skill 发现入口。
- **子代理编排**：子代理不强制绑定 `mcp-go-agent-orchestration` 或 `mcp-orch` 生命周期；优先使用当前平台可用的原生子代理/多代理能力。只有任务确实需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`。

## 代码地图与上下文加载

定位文件路径、模块入口、或改动影响面时，按低 token 顺序读取：

1. `README.md`：项目结构、启动方式、核心模块概览。
2. `docs/doc/codemap/README.md`：代码地图目录和阅读边界。
3. 根据问题选择单个代码地图卷，例如：
   - `docs/doc/codemap/01-terminal-ui.md`
   - `docs/doc/codemap/02-mcp-orch.md`
   - `docs/doc/codemap/04-app-contract.md`
   - `docs/doc/codemap/07-module.md`
   - `docs/doc/codemap/08-platform.md`
   - `docs/doc/codemap/09-provider.md`
   - `docs/doc/codemap/10-store.md`
   - `docs/doc/codemap/11-memory-prompt-thread.md`
4. 用 `rg` 在 `docs/doc/codemap/ai-index.json` 或具体源码目录里精确检索。
5. 打开目标源码和同包测试；行为问题以代码和测试为准。

架构/契约问题优先读 `docs/decisions/*.md`、`docs/adr/*.md`、`docs/契约/*.md`；LSP 工具链规范必读 `docs/internal-notes/LSP系统提示词.md`；`docs/plans/**`、`docs/迁移/**`、`docs/superpowers/plans/**`、历史报告默认视为历史材料。

避免默认扫描 `.build-cache/`、`bin/`、`frontend-app/node_modules/`、`frontend-app/dist/`、`cmd/agent-terminal/web-dist/`、`.worktrees/`、`.workspace/`、`.claude/`、`.agent/code_exec/`、`.agent/workspaces/`、`.agnet/report/`、`.agnet/shared/_internal/`、`.agnet/shared/handoff/`、历史迁移文档和报告目录，除非用户明确要求。

## 项目现状

- Go module：`github.com/anthropic-ai/super-agent-v3`，Go `1.25.7`。
- 主入口：
  - `cmd/agent-terminal`：Wails 桌面宿主和 HTTP/RPC bridge；开发模式通过 `VITE_DEV_URL` 代理当前 `frontend-app`。
  - `cmd/mcp-orch`：agent lifecycle、DAG、cron、toolbridge orchestration peer。
  - `cmd/mcp-lsp`：gopls/LSP 代码智能 peer。
  - `cmd/mcp-ida`：IDA MCP peer。
- 核心目录：
  - `internal/app`：应用装配、runner、toolbridge adapters。
  - `internal/contract`：跨模块接口和 DTO。
  - `internal/module`：turn、prompt、cron、memory、skill 等业务模块。
  - `internal/platform`：db、rpc、config、runtime safety、toolbridge。
  - `internal/provider`：Claude CLI、Codex 等 provider 适配。
  - `internal/store`：sqlc 生成的数据访问层。
  - `internal/archtest`：架构守卫和 baseline 棘轮。
  - `pkg`：可复用公共库。
  - `frontend-app`：当前且唯一的 React/Vite 前端源码包，由 `run-new-ui-desktop.sh` / `.ps1` 启动。
  - `cmd/agent-terminal/web-dist`：由 `frontend-app` 构建同步出的 Go embed 静态资源目录，不是前端源码入口。

## 任务完成验证

每次声称 done/fixed/ready-to-commit/ready-to-merge 前，根据改动范围跑对应验证。不要套用 `wjboot-v2` 的 `backend/`、`GOWORK=off go -C backend`、`docs/guide` 或 `cmd/code_guard` 命令。

### Go 代码

常规包级验证：

```bash
./scripts/test_with_guard.sh <affected packages> -count=1
```

只需要快速跑仓库守卫时：

```bash
make guard
```

每改完一个 Go 文件，先跑单文件守卫再继续：

```bash
./scripts/test_with_guard.sh <file.go>
```

只传入 Go 文件路径时，该守卫保持安静：exit 0 表示无违规且不输出内容；exit 1 表示有违规，stderr 只输出具体违规项。

大范围改动或发布前：

```bash
make test
make build-plain
```

修改 `internal/archtest`、守卫、baseline 或架构边界时，至少补跑：

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

### 前端代码

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

`cmd/agent-terminal` 无 dev proxy 时会读取 `cmd/agent-terminal/web-dist`。该目录内容除 `.gitkeep` 外被 gitignore；`make frontend-app-build` 会先构建 `frontend-app/dist`，再用跨平台 Node 同步脚本更新 embedded dist。

### SQL / store

修改 `sql/queries/**`、migrations、或 `internal/store/sqlc/**` 时：

```bash
make sqlc-verify
```

需要重生成时先运行：

```bash
make sqlc-generate
```

### 代码地图

修改影响代码地图覆盖范围时：

```bash
make codemap-check
```

需要刷新 `docs/doc/codemap/ai-index.json` 时：

```bash
make codemap-refresh
```

## 禁止兜底代码
遇到异常、配置为空或数据缺失时，必须立即报错并阻断（Fail-Fast）。
严禁使用包括但不限于静默降级、默认配置、吞错捕获等隐式兜底逻辑。

## 函数级中文注释策略

函数级注释写给维护系统的人看，先说明这个函数或关键代码块做什么，再补充代码本身看不出来的原因、约束和风险；不要逐行复述实现。

必须补函数级中文注释的场景：

- 导出函数、导出方法、导出类型的关键方法。
- 跨模块入口、provider / store / scheduler / thread / prompt / memory / skill / DAG 等关键路径函数。
- 涉及状态变化、幂等、重试、锁、并发、恢复、fail-fast、权限或持久化边界的函数。
- 私有函数如果有效代码行较长、分支复杂、嵌套较深，必须说明它负责什么、不能误改什么。
- React hooks、store slice、service、复杂页面 controller 需要说明数据来源和本地状态边界。

不要求给简单 getter/setter、小型纯映射、小 JSX 渲染片段、测试内直观 helper 机械补注释。

注释风格要求：

- 使用自然、简洁的中文，优先 1-3 行。
- 少用“语义、契约、生命周期、治理、收敛”等重工程词，除非代码里的领域名必须保留。
- 先写明函数或关键代码块做什么；必要时再写为什么这样做、哪里不能乱改、失败时会怎样。

函数级注释守卫应由 `internal/archtest/guardlib.go` 实现，并通过 `./scripts/test_with_guard.sh <file.go>`、`make guard`、`./scripts/test_with_guard.sh ./internal/archtest -count=1` 验证。

## 守卫和 baseline 红线

1. 守卫任一失败 = 任务未完成；不得声称完成、提交或合并。
2. `internal/archtest/baseline.json` 是 per-file ratchet baseline。默认 guard 可自动收缩，不应手工放宽。
3. `go run scripts/code_size_guard.go --freeze` 只能在守卫规则变化或用户明确同意时使用；不能用来掩盖代码恶化。
4. fix/hotfix/bugfix/修复/修正 类提交必须在同一提交包含锁定 bug 的测试、fixture、golden 或 snapshot。
5. `git commit --no-verify` / `git push --no-verify` 仅限紧急事故；使用后必须补跑遗漏验证。

## Git Hook

- 首次 clone、仓库移动、或新 worktree 首次开发前运行：
  ```bash
  make install-hooks
  ```
- 用 `git config --get core.hooksPath` 确认 hooks 指向当前仓库的 `.githooks` 绝对路径。
- `pre-commit` 会检查 staged Go 影响面、拒绝 staged/worktree 不一致、运行 gofmt/go vet/短测，并在 Go 改动时跑守卫。
- `commit-msg` 会拦截缺少同提交 bug-locking 测试的 fix/hotfix/bugfix/修复 类提交。
- `pre-push` 要求 worktree/index/untracked 干净，只允许推送当前 `HEAD`，并按 push range 复查 fix-test 规则和受影响包测试。

## Git 与工作区纪律

- 开始前看 `git status --short`；不要覆盖或整理无关本地改动。
- 不使用 `git add .`；只 stage 本任务拥有的文件。
- 需要原子提交/推送时，保持一个主题一个提交，修复与锁定测试同提交。
- 多 worktree / 多代理并行时，先确认各自 owns 的路径，避免跨工作区修改。
