# Super-Dolphin 项目管理与文件治理设计

Date: 2026-06-16
Status: design only
Scope: file management, documentation governance, archive policy, and safe shrink plan for `D:\project\Super-Dolphin-worktrees\ui-refactor-integration-20260613`.

## 1. 背景

本仓库同时承载 Go 后端、Wails 桌面入口、MCP peers、当前 React UI、legacy Vue embed 包、项目级 agent skills、运行脚本、历史计划、审查报告和本地产物。第一轮已把一批根级 agent notes、报告和旧审查材料归档到 `docs/archive/**`，并新增 `docs/README.md` 与 `docs/archive/README.md`。

本轮目标不是继续立即删除文件，而是形成可复用的项目管理方案，避免后续 agent 或维护者在源码、生成物、历史材料和本地运行产物之间反复迷路。

## 2. 扫描结论

### 2.1 当前主要目录规模

按 tracked files 粗略统计，当前主要文件量集中在：

- `internal/`: 1739 files，核心 Go 模块与测试。
- `docs/`: 971 files，当前文档、历史计划、审查材料和代码地图。
- `cmd/`: 959 files，桌面入口、MCP peers 和 legacy frontend。
- `.agent/`: 436 files，项目级 skill/workflow 资产。
- `frontend-app/`: 262 files，当前 React/Vite UI。
- `migrations/`: 111 files，数据库迁移。
- `scripts/`: 89 files，构建、打包、守卫和索引脚本。
- `frontend/`: 45 files，旧 React web frontend。

### 2.2 文档噪音仍然集中

除 `docs/archive/**` 与 `docs/doc/codemap/**` 外，文档剩余大块是：

- `docs/plans/`: 354 files。源码注释、archtest、脚本仍大量引用，不能整体移动。
- `docs/cc/`: 121 files。多为阶段性审查、SQLite 切换和打包证据。
- `docs/ai01-docs/`: 88 files。多为前端审查、测试、资产矩阵和 SOP。
- `docs/superpowers/`: 61 files。包含 specs 与 plans；plans 应默认视为历史计划。

### 2.3 当前不能直接删除的目录

- `frontend/` / `run-new-ui-web.sh`: 已按 Windows/macOS desktop dev flow 收敛方向退役；当前根启动入口只保留 `run-new-ui-desktop.sh` 和 `run-new-ui-desktop.ps1`。
- `cmd/agent-terminal/frontend/**`: Go embed 和打包脚本仍依赖 `cmd/agent-terminal/frontend/dist`；当前它是 legacy/package-embed fallback，不应作为普通旧前端删除。
- `.agent/skills/**`: README 和 AGENTS 明确这是项目 canonical skill 资产。
- `third_party/kelindar-event/**`: `go.mod` 通过 `replace github.com/kelindar/event => ./third_party/kelindar-event` 绑定，且核心代码大量引用。

### 2.4 明确可疑的本地或历史产物

这些不应进入默认阅读路径，也不应进入项目地图索引：

- 根目录 `mcp-lsp.exe`、`mcp-orch.exe`: 已被 `.gitignore` 的 `*.exe` 覆盖，是本地构建产物。
- `.codex-run/**`: 本地启动日志和截图。
- `.codex/vite-ui-refactor*.log`: 本地日志；`.codex/config.toml`、`.codex/hooks.json` 是刻意 tracked 的项目级 Codex 配置，不同于日志。
- `.superpowers/brainstorm/**`: brainstorming visual companion 会话产物。
- `.workspace/mcp-smoke-run-*/**`: smoke 临时工作区。
- `.agnet/shared/reports/**`: sharedfile 历史报告；`.agnet/shared/_internal/**` 和 handoff 已被默认忽略。

### 2.5 项目地图生成器存在治理缺口

`scripts/generate_ai_project_map.js` 当前按文件系统 walk 生成索引，已经排除了 `docs/archive/**`，但仍会把本地 ignored 产物写入 `docs/doc/codemap/project-map/**`，例如 `.codex-run/**`、`.codex/*.log`、`.superpowers/brainstorm/**`、根目录 exe。

这会造成两个问题：

1. 项目地图在不同机器上不稳定。
2. agent 读取项目地图时会把本地产物误认为仓库结构的一部分。

该问题应作为下一批 P0 治理项处理，但本设计文档不直接修改生成器。

## 3. 治理目标

1. 默认阅读路径只包含当前事实源和必要导航资料。
2. 历史计划、报告和证据保留可追溯性，但退出默认扫描路径。
3. 本地产物和生成产物不进入 tracked 索引，也不污染项目地图。
4. 运行入口、打包路径、测试 fixtures、project skills 和第三方 vendored dependency 在未验证前不删除。
5. 每批瘦身都有可验证的退出条件：引用扫描、构建/guard、项目地图检查和 git diff 可解释。

## 4. 目录分级模型

### Tier 0: 当前运行事实源

这些目录默认保留，变更必须按代码变更处理：

- `cmd/agent-terminal/`
- `cmd/mcp-orch/`
- `cmd/mcp-lsp/`
- `cmd/mcp-ida/`
- `internal/`
- `pkg/`
- `sql/`
- `migrations/`
- `frontend-app/`
- `cmd/agent-terminal/frontend/`
- `third_party/kelindar-event/`
- `.agent/skills/`
- `.agent/workflows/`

规则：

- 不做“顺手清理”。
- 删除前必须有引用扫描和对应测试。
- legacy 目录必须先明确替代路径，再做退役。

### Tier 1: 当前文档与导航

这些是默认阅读入口：

- `README.md`
- `AGENTS.md`
- `docs/README.md`
- `docs/doc/codemap/README.md`
- `docs/adr/**`
- `docs/decisions/**`
- `docs/契约/**`
- `docs/internal-notes/**`

规则：

- 保持短、准、少重复。
- 只记录当前约定和阅读路径。
- 历史说明只保留指针，不展开大段旧过程。

### Tier 2: 历史材料

这些默认不作为当前事实源：

- `docs/plans/**`
- `docs/superpowers/plans/**`
- `docs/cc/**`
- `docs/ai01-docs/**`
- `docs/archive/**`

规则：

- 需要迁移历史或追溯证据时再打开。
- 新增历史文档必须有日期、主题和状态。
- 已完成且不再被源码/测试引用的批次可移动到 `docs/archive/**`。

### Tier 3: 本地运行产物

这些不应进入 tracked 文件，也不应进入项目地图：

- `mcp-*.exe`
- `.codex-run/**`
- `.codex/*.log`
- `.superpowers/brainstorm/**`
- `.workspace/**`
- `.tmp/**`
- `.build-cache/**`
- frontend `node_modules/**`
- frontend `dist/**`
- `.agnet/shared/_internal/**`
- `.agnet/shared/handoff/**`

规则：

- 优先由 `.gitignore` 和项目地图生成器排除。
- 如果已经 tracked，先单独建清理 PR/commit，并说明为什么不是产品资产。
- 不把机器本地路径、日志和截图当作长期文档。

## 5. 分批治理路线

### Batch A: 索引稳定化

目标：让项目地图只索引稳定仓库内容。

建议动作：

- 修改 `scripts/generate_ai_project_map.js`，优先基于 `git ls-files` 生成 tracked 文件索引，或至少排除 `git check-ignore` 命中的路径。
- 明确排除 `.codex-run/**`、`.codex/*.log`、`.superpowers/**`、`.workspace/**`、根级 `*.exe`。
- 重新生成 `docs/doc/codemap/project-map/**`。

验证：

- `node scripts/generate_ai_project_map.js --check --strict-drift`
- `rg -n "\.codex-run|\.superpowers|mcp-lsp\.exe|mcp-orch\.exe" docs/doc/codemap/project-map`
- `git diff --name-status --find-renames`

### Batch B: 本地产物出库

目标：移除已经 tracked 的本地运行产物。

候选：

- `.superpowers/brainstorm/**`
- `.workspace/mcp-smoke-run-*/**`
- `.agnet/shared/reports/code-convention-audit-2026-05-07.md`

规则：

- 若内容仍有长期价值，先移动到 `docs/archive/evidence/**`。
- 若只是会话缓存、pid、server log 或 smoke 临时 go.mod，直接 `git rm`。
- 同批更新 `.gitignore` 和项目地图排除规则，避免再次出现。

验证：

- `git status --short --branch --untracked-files=all`
- `git diff --name-status --find-renames`
- `node scripts/generate_ai_project_map.js --check --strict-drift`

### Batch C: 文档历史分层

目标：降低 `docs/**` 默认扫描噪音。

候选：

- `docs/ai01-docs/前端测试文档/**`
- `docs/ai01-docs/审查文档/**`
- `docs/ai01-docs/assets/**`
- `docs/cc/**` 中已经完成的审查/证据包
- `docs/调研/**` 中原始材料

规则：

- `docs/plans/**` 不整体移动，因为源码和测试仍引用。
- 对每个移动目录先跑引用扫描。
- 源码或测试引用的文档，先保留或只加历史状态说明。
- 移动使用 `git mv`，保留历史。

验证：

- `rg -n "old/path" -g '!docs/archive/**'`
- `git diff --name-status --find-renames`
- `node scripts/generate_ai_project_map.js --check --strict-drift`

### Batch D: `frontend/` 退役决策

目标：决定根级旧 React web frontend 是否继续存在。

当前决策：

- `run-new-ui-web.sh` 已删除。
- 根启动入口只保留 macOS 的 `run-new-ui-desktop.sh` 和 Windows 的 `run-new-ui-desktop.ps1`。
- `frontend-app/` 是当前 React UI，普通本地启动不再提供单独 web-only 根脚本。

验证：

- `rg -n "frontend/" README.md AGENTS.md Makefile scripts cmd internal docs`
- 确认 CI、Makefile、打包脚本和当前文档不再引用已退役的 web-only 根脚本。

### Batch E: legacy Vue embed 退役决策

目标：决定 `cmd/agent-terminal/frontend/**` 是否继续作为 embed fallback。

当前事实：

- Go embed 入口仍是 `cmd/agent-terminal/frontend/dist`。
- packaging scripts 会把 `frontend-app/dist` sync 到该 dist 目录。
- legacy Vue source 和 tests 仍有大量资产与守卫。

规则：

- 不和文档瘦身混在一批。
- 需要独立 RFC：保留 embed dist 目录但删除 Vue source，或保留完整 legacy 包。
- 删除前必须跑 packaging guard 和 desktop smoke。

## 6. 操作纪律

1. 每批只处理一种资产类型：索引、本地产物、历史文档、frontend 退役、legacy embed 退役不要混在一起。
2. 每批先列候选清单，再做引用扫描。
3. 删除动作优先用 `git rm`；移动历史资料用 `git mv`。
4. 不 stage 其他任务的 UI、prompt、personalization 改动。
5. 不删除 `.agent/skills/**`、`.agent/workflows/**`、`third_party/**`，除非有明确替代方案和测试。
6. 不把 `docs/archive/**`、`.tmp/**`、`.workspace/**`、`.codex-run/**`、`.superpowers/**` 纳入默认项目地图。

## 7. 成功标准

项目治理完成后应满足：

- 新人或 agent 默认入口是 `README.md` -> `docs/README.md` -> `docs/doc/codemap/README.md`。
- 项目地图只包含稳定仓库内容，不包含本地 exe、日志、pid、临时工作区。
- 历史资料集中在 `docs/archive/**` 或明确标记为 historical。
- `frontend-app/` 是当前 UI 事实源；`frontend/` 和 legacy Vue 的状态明确。
- 每次瘦身提交都能用 `git diff --name-status --find-renames` 解释清楚。

## 8. 推荐下一步

推荐下一批只做 Batch A。

原因：

- 它不删除产品代码。
- 它能立刻降低项目地图噪音。
- 它为后续 Batch B/C 提供稳定扫描基线。
- 风险低，验证清晰。

Batch A 完成后，再决定是否删除 `.superpowers/brainstorm/**`、`.workspace/mcp-smoke-run-*` 这类本地会话产物。
