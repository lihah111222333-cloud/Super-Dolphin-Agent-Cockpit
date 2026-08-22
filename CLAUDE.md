## 默认上下文加载顺序

路径发现模式，适用于用户询问文件位置或应修改哪个路径时：

1. 读取 `README.md`，了解当前项目布局和入口点。
2. 读取 `docs/doc/codemap/README.md`，了解代码地图目录和阅读边界。
3. 根据目录选择并打开一个相关的代码地图分卷。
4. 使用 `rg` 搜索 `docs/doc/codemap/ai-index.json` 或精确源码目录，缩小候选符号和路径范围。
5. 读取 `docs/internal-notes/LSP系统提示词.md`；仅当地图和原生搜索仍无法确认符号归属或跨文件影响面时，使用 `structure` / `xref`。
6. 打开精确源码文件和同包测试；修改后使用 `diagnostics` 检查改动文件。

行为阅读模式，适用于用户询问某个机制如何工作时：

1. 源码和测试是事实来源。
2. 使用 `docs/decisions/*.md` 和 `docs/adr/*.md` 查看已接受的架构决策。
3. 使用 `docs/契约/*.md` 查看 fx、rungroup、jrpc2、sqlc、stateless、MCP 服务、洋葱架构等约定。
4. 使用 `docs/doc/codemap/*.md` 导航大型子系统。
5. 除非用户明确询问迁移历史，否则将 `docs/plans/**`、`docs/迁移/**`、`docs/superpowers/plans/**` 和旧报告视为历史规划材料。
6. 读取 `docs/internal-notes/LSP系统提示词.md`；需要符号定义或跨文件影响证据时分别使用 `structure` / `xref`，局部源码事实直接使用原生文件工具读取。

## LSP 三工具与原生工具分工

- 共享作战法则以 `shared-developer-instructions.md` 为准：文件读取用原生 `cat` / `head`，文本定位用原生 `grep` / `rg`，代码修改用原生 `apply_patch`。
- `cmd/mcp-lsp` 对外只暴露 `structure`、`xref`、`diagnostics`：找定义用 `structure`，查调用方和影响面用 `xref`，修改后查语法和类型错误用 `diagnostics`。
- 不存在 `file`、`read_file`、`inspect`、`grep`、`edit`、`patch_edit`、`completion` LSP 工具或兼容别名；不得用纯 shell 命令伪造 LSP 语义结果。
- Anti-Overkill：局部单文件小 Bug 优先用原生工具直接解决，禁止无必要的重型跨文件探索。只有定义不明、调用关系不明或影响面跨文件时才使用 `structure` / `xref`。
- 三个 LSP 工具的入参都是封闭 JSON object。返回值只读取 MCP `content` 中的纯文本行协议（`OK` / `ERROR`、`ATTR`、`ROW`、`HINT`），不得依赖 `structuredContent`。
- `structure(workspace_symbol)` 的无文件语言选择使用 `workspace_language`；具体文件的语言覆盖使用 `language_id`；`xref` 使用 `pos=<file>:<line>:<column>`；`diagnostics` 必须且只能传 `file_path` 或 `file_paths` 之一。
- LSP 工具超时、不可用或返回异常时，先收窄 `work_dir`、文件、符号或结果数后重试；仍失败时记录 tool、目标、错误和已尝试的收窄方式。禁止把无法取得 diagnostics 写成 PASS。
- diagnostics 返回的 Error、Warning、Information、Hint 均视为待处理项；无法修复时记录文件、行号、规则和原因。

### 最小工作流

- 局部修复：原生读取/定位 → `apply_patch` → `diagnostics` → 格式化、构建和测试。
- 定义不明：`structure` → 原生读取精确文件 → `apply_patch` → `diagnostics` → 构建和测试。
- 影响面不明：先 `structure` 定位符号，再 `xref` 查引用或调用层级；只扩展到真实相关文件。
- 审查/解释：原生读取是事实来源；需要符号归属或跨文件影响证据时分别追加 `structure` / `xref`，需要诊断证据时使用 `diagnostics`。

## 上下文预算卫生

- 优先使用定向 `rg` 搜索和单文件读取，避免大范围目录扫描。
- 默认不要递归读取或索引 `.build-cache/`、`bin/`、前端 `node_modules/`、前端 `dist/`、`.worktrees/`、`.workspace/`、`.claude/`、`.agent/code_exec/`、`.agent/workspaces/`、`.agnet/report/`、`.agnet/shared/_internal/`、`.agnet/shared/handoff/` 或生成的测试报告。
- 默认不要递归读取或索引 `docs/archive/**`。只有当用户要求历史报告、旧代理笔记、迁移证据或来源追溯时才使用它。
- 不要批量加载 `.agents/skills/**`。仓库本地技能是按需选择的参考，不是默认上下文。
- 如果生成物看起来可能过期，在直接编辑它之前先验证生成器或检查目标。

## 仓库本地技能策略

`super-agent-v3` 的仓库本地技能唯一规范入口是 `.agents/skills/*/SKILL.md`。

- 用户点名技能、任务明显匹配某个技能 `description`，或技能与当前任务上下文、文件类型、目录、变更面明显相关时，本回合必须自动使用覆盖范围最小的匹配技能。
- 使用技能前必须先完整读取对应 `SKILL.md`；仅在该文件要求时再读取 references、scripts 或 assets。
- 未匹配到可用技能时，直接按本文件的项目地图、LSP 与验证闭环执行，禁止全量扫读技能目录。
- 如果加载了某个技能，它的指令从属于本文件、用户最新指令和当前仓库证据。

本策略管理从 `.agents/skills/*/SKILL.md` 加载代理指令的行为。它不会禁用或描述产品运行时技能管线。历史 `.agent/skills` 仅作为旧路径保留，不是规范入口，也不是人工编辑目标。运行时规范技能由本项目管理，位置包括项目内 `<cwd>/.agents/skills` 以及活跃个人根目录 `~/.super-dolphin/skills/personal/{user,agent,imported}`；`personal/hub` 仅作为目录索引，不得被扫描、镜像或当作普通个人技能处理。显式配置的提供方主目录仍可使用自己的 `skills` 目录。要检查运行时技能行为，请查看 `internal/module/skill*`、`internal/provider/shared/provider_home.go`、提供方镜像测试以及相关 toolbridge 兼容性测试。

子代理不强制绑定 `mcp-orch` 生命周期；优先使用当前平台可用的原生子代理/多代理能力。只有任务确实需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`。

## 完成验证

在声称已完成、已修复、可提交或可合并之前，运行与变更面匹配的验证。

## 提交信息门禁

提交标题（以及填写的正文）必须含中文；默认不得用 `--no-verify` 绕过 Git hooks。仅当用户针对当前提交明确授权绕过 hooks 时，才允许使用 `--no-verify`。

每改完一个 Go 文件，必须运行以下命令进行单文件守卫验证（0 表示无违规，1 表示有违规）：
```bash
./scripts/test_with_guard.sh <file.go>
```

当前新 UI 前端变更：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

## 禁止兜底代码
遇到异常、配置为空或数据缺失时，必须立即报错并阻断（Fail-Fast）。
严禁使用包括但不限于静默降级、默认配置、吞错捕获等隐式兜底逻辑。
