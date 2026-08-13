## 默认上下文加载顺序

路径发现模式，适用于用户询问文件位置或应修改哪个路径时：

1. 读取 `README.md`，了解当前项目布局和入口点。
2. 读取 `docs/doc/codemap/README.md`，了解代码地图目录和阅读边界。
3. 根据目录选择并打开一个相关的代码地图分卷。
4. 使用 `rg` 搜索 `docs/doc/codemap/project-map/AI_PROJECT_MAP.md` 和 `docs/doc/codemap/ai-index.json`，缩小候选符号和路径范围。
5. 如果代码地图分卷、项目地图和 `ai-index.json` 仍无法定位到具体源码文件或符号，先运行 `make project-map-refresh` 刷新项目地图，再重复上述地图检索；不得未刷新就直接跳过项目地图。
6. 如果刷新后仍无法定位，先上报项目地图缺口，至少包含定位目标、已执行的刷新命令与结果、已检索范围和缺失项；然后继续等价定位，在精确源码目录使用 `rg` 搜索候选符号和路径。项目地图缺口不得被当作停止定位的理由。
7. 读取 `docs/internal-notes/LSP系统提示词.md`，然后使用 LSP 符号和导航工具确认定义、引用、调用方、实现和诊断，再决定要编辑或报告的路径。
8. 在 LSP 确认路径后，打开精确源码文件和同包测试。

行为阅读模式，适用于用户询问某个机制如何工作时：

1. 源码和测试是事实来源。
2. 使用 `docs/adr/*.md` 查看已接受的架构决策。
3. 使用 `docs/契约/*.md` 查看 fx、rungroup、jrpc2、sqlc、stateless、MCP 服务、洋葱架构等约定。
4. 使用 `docs/doc/codemap/*.md` 导航大型子系统。
5. 除非用户明确询问迁移历史，否则将 `docs/plans/**`、`docs/迁移/**`、`docs/superpowers/plans/**` 和旧报告视为历史规划材料。
6. 读取 `docs/internal-notes/LSP系统提示词.md`，然后在回答行为、影响或实现问题前，使用 LSP 工具对相关文件做符号导航、引用和调用层级检查、悬停和签名上下文检查以及诊断检查。

## 远程 CI 强制契约

- 任何涉及远程 CI、ECI、ImageCache、基准镜像、刷新、分片、耗时账本、缓存命中或校准资源的审查、设计和实现，开始前必须完整读取 `docs/契约/remote-ci-eci-imagecache-contract.md`。
- 该契约冻结唯一执行路径、两小时 SQLite 抢占、后台增量刷新、无并发上限、固定规格校准和精确耗时账本。不得从 `docs/plans/**`、历史提交或旧测试恢复与其冲突的 DataCache、JSON truth source、本地 Docker、ACR 专用、并发上限或第二 executor。
- 若当前源码与该契约不一致，任务目标是让源码收敛到契约并补架构守卫；不得通过放宽契约、添加兼容分支或保留旧入口消除失败。

## LSP 强制使用规则

### LSP 工具链不可降级规则

- `cmd/mcp-lsp` 是 generic multi-language LSP peer；不得把本文件的 LSP 要求降级、改写或替换成 `gopls check`、`go test`、`rg + cat/sed`、单语言检查器或纯 shell 验证。
- Go 文件可以由底层语言服务器使用 gopls，但人工工作流必须通过当前对外暴露的 `file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit`、`completion` 等 LSP 工具完成导航、影响面分析、读取、编辑和诊断；`gopls check` 只能作为额外验证，不能替代 LSP 工具证据。
- 当前 MCP 暴露名使用短名；在 Codex 工具命名空间中对应 `mcp__lsp.file`、`mcp__lsp.inspect`、`mcp__lsp.xref`、`mcp__lsp.grep`、`mcp__lsp.structure`、`mcp__lsp.patch_edit`、`mcp__lsp.completion`。`file(diagnostics)` 是诊断入口，没有独立 `diagnostics` 工具；`edit`、`lsp_edit`、`lsp_file` 等旧名不是当前对外契约。
- LSP 工具超时、不可用或返回异常时，必须先收窄 `work_dir`、路径、查询、语言或结果数后重试；仍失败时记录 blocker，包含 tool/action、`work_dir`、目标文件或符号、错误信息和已尝试的收窄方式。禁止静默降级为 `gopls check` 或 shell 命令，也禁止把“无法取得 diagnostics”写成 PASS。
- 审查、修复、路径判断或行为解释涉及源码时，至少保留一组可复查的 LSP 证据：定位（`grep` 或 `structure`）、理解（`inspect`）、影响面（`xref`）、精读（`file(read_file)`）和诊断（`file(diagnostics)`）。如果某一类证据因工具能力或文件类型无法取得，必须在输出中说明缺口和 blocker。

### LSP diagnostics 处理规则

- LSP diagnostics 返回的 `Error`、`Warning`、`Information`、`Hint` 均视为待修复项；不得因 severity 为 hint 或“仅建议”而忽略。无法修复时必须记录 blocker，包含文件、行号、规则和原因。

### A：AST 搜索 → 精确读取

```text
1. grep(ast_search, query="func ($R) MethodName(", language="go")
2. 用返回的 func_start/func_end → file(read_file, pos=<file>:<func_start>, limit=<func_end-func_start+1>) 精准读取
```

### B：符号定位 → 跳转定义 → 读实现

```text
1. structure(workspace_symbol, query="SymbolName") → 找到符号位置
2. inspect(definition, pos=<file>:<line>:<col>) → 跳到定义
3. file(read_file, pos=<file>:<line>, limit=<n>) → 读实现
```

### C：引用分析 → 调用层级 → 影响面

```text
1. xref(references, pos=<file>:<line>:<col>) → 找所有引用点
2. xref(call_hierarchy, pos=<file>:<line>:<col>, direction="incoming") → 谁调用了它
3. xref(call_hierarchy, pos=<file>:<line>:<col>, direction="outgoing") → 它调用了谁
```

### D：接口→实现→引用 三级跳

```text
1. inspect(definition, pos=<file>:<line>:<col>) → 接口定义
2. inspect(implementation, pos=<file>:<line>:<col>) → 所有实现类
3. xref(references, pos=<file>:<line>:<col>) → 所有调用点
```

### E：文件大纲对比

```text
1. structure(document_symbol, file_path="v3/file.go")
2. structure(document_symbol, file_path="v2/file.go")
3. 逐一对比找缺失
```

## 三、强制工作流

- 创建 Git 工作树时，目标路径必须是当前项目根目录下的 `<repo-root>/.worktrees/<worktree-name>`；禁止在项目根目录之外、项目同级目录、系统临时目录或其他位置创建工作树。执行 `git worktree add` 前必须解析并确认目标路径位于 `<repo-root>/.worktrees/` 内。

审查类：grep(text_search|ast_search) 定位 → inspect(definition|hover|type_definition) 理解 → xref(references|call_hierarchy) 影响面 → file(read_file) 精读 → 输出判定

修复类：grep(text_search|ast_search) 定位 → xref(references|call_hierarchy) 影响面 → file(read_file) 读取 → patch_edit(replace_range|rename|code_action|format) 修改 → file(diagnostics) 检查 → build/test 验证


## 上下文预算卫生

- 优先使用定向 `rg` 搜索和单文件读取，避免大范围目录扫描。
- 创建子 agent 时禁止继承父级对话上下文；所有子 agent 必须以空上下文启动（例如调用 `spawn_agent` 时显式设置 `fork_turns="none"`），并仅通过任务说明传递完成该子任务所必需的最小信息。
- 每次创建或继续子 agent，都必须在任务说明中显式下发 `cwd=<absolute-path>`；根工作区任务使用当前项目根目录的绝对路径，工作树任务使用 `<repo-root>/.worktrees/<worktree-name>` 的绝对路径。禁止依赖父 agent 的当前目录、默认工作目录或隐式上下文推断 `cwd`，子 agent 执行命令前必须核对其实际工作目录与下发值一致。
- 默认不要递归读取或索引 `.build-cache/`、`bin/`、前端 `node_modules/`、前端 `dist/`、`.worktrees/`、`.workspace/`、`.claude/`、`.agent/code_exec/`、`.agent/workspaces/`、`.agnet/report/`、`.agnet/shared/_internal/`、`.agnet/shared/handoff/` 或生成的测试报告。
- 默认不要递归读取或索引 `docs/archive/**`。只有当用户要求历史报告、旧代理笔记、迁移证据或来源追溯时才使用它。
- 不要批量加载 `.agents/skills/**`。仓库本地技能是按需选择的参考，不是默认上下文。
- 如果生成物看起来可能过期，在直接编辑它之前先验证生成器或检查目标。

## 仓库本地技能策略

`super-agent-v3` 的仓库本地技能唯一规范入口是 `.agents/skills/*/SKILL.md`。

- 用户点名技能时，本回合必须使用该技能。
- 自动加载仅限四个技能：Go 后端工作使用 `后端`，代码审查使用 `代码审查维度`，前端规范工作使用 `前端`；新增、删除、重命名或跨层传递结构化字段，以及修改 DTO、mapper、schema、API/RPC、事件、配置、store、序列化或前后端契约时使用 `字段守卫`。只有任务与对应变更面直接相关时，才自动加载其中覆盖范围最小的技能；字段变更与后端、前端或审查任务重叠时，`字段守卫` 可与对应技能同时加载。
- 除上述四个技能外，其他所有技能都必须由用户明确点名后才能加载；不得因任务语义相似、技能包含项目事实、显式依赖条件或需要仓库专属模板/资产而自动加载。
- 自动加载后的技能只提供仓库事实与约束；其中与模型通用推理能力重复的流程、角色说明、方法论、示例话术和常识检查表不构成必须遵循的附加步骤。
- 不得为了“先检查技能”而自动加载元技能；技能选择直接依据本节规则完成。
- 使用技能前必须先完整读取对应 `SKILL.md`；仅在该文件要求时再读取 references、scripts 或 assets。
- 未匹配到可用技能时，直接按本文件的项目地图、LSP 与验证闭环执行，禁止全量扫读技能目录。
- 如果加载了某个技能，它的指令从属于本文件、用户最新指令和当前仓库证据。

本策略管理从 `.agents/skills/*/SKILL.md` 加载代理指令的行为。它不会禁用或描述产品运行时技能管线。历史 `.agent/skills` 仅作为旧路径保留，不是规范入口，也不是人工编辑目标。运行时规范技能由本项目管理，位置包括项目内 `<cwd>/.agents/skills` 以及活跃个人根目录 `~/.super-dolphin/skills/personal/{user,agent,imported}`；`personal/hub` 仅作为目录索引，不得被扫描、镜像或当作普通个人技能处理。显式配置的提供方主目录仍可使用自己的 `skills` 目录。要检查运行时技能行为，请查看 `internal/module/skill*`、`internal/provider/shared/provider_home.go`、提供方镜像测试以及相关 toolbridge 兼容性测试。

## 完成验证

在声称已完成、已修复、可提交或可合并之前，运行与变更面匹配的验证。

## 提交信息门禁

提交标题（以及填写的正文）必须含中文；默认不得用 `--no-verify` 绕过 Git hooks。仅当用户针对当前提交明确授权绕过 hooks 时，才允许使用 `--no-verify`。

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
