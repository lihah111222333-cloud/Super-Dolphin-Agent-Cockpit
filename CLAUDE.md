## 默认上下文加载顺序

路径发现模式，适用于用户询问文件位置或应修改哪个路径时：

1. 读取 `README.md`，了解当前项目布局和入口点。
2. 读取 `docs/doc/codemap/README.md`，了解代码地图目录和阅读边界。
3. 根据目录选择并打开一个相关的代码地图分卷。
4. 使用 `rg` 搜索 `docs/doc/codemap/ai-index.json` 或精确源码目录，缩小候选符号和路径范围。
5. 读取 `docs/internal-notes/LSP系统提示词.md`，然后使用 LSP 符号和导航工具确认定义、引用、调用方、实现和诊断，再决定要编辑或报告的路径。
6. 在 LSP 确认路径后，打开精确源码文件和同包测试。

行为阅读模式，适用于用户询问某个机制如何工作时：

1. 源码和测试是事实来源。
2. 使用 `docs/decisions/*.md` 和 `docs/adr/*.md` 查看已接受的架构决策。
3. 使用 `docs/契约/*.md` 查看 fx、rungroup、jrpc2、sqlc、stateless、MCP 服务、洋葱架构等约定。
4. 使用 `docs/doc/codemap/*.md` 导航大型子系统。
5. 除非用户明确询问迁移历史，否则将 `docs/plans/**`、`docs/迁移/**`、`docs/superpowers/plans/**` 和旧报告视为历史规划材料。
6. 读取 `docs/internal-notes/LSP系统提示词.md`，然后在回答行为、影响或实现问题前，使用 LSP 工具对相关文件做符号导航、引用和调用层级检查、悬停和签名上下文检查以及诊断检查。

## LSP 强制使用规则

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

审查类：grep(text_search|ast_search) 定位 → inspect(definition|hover|type_definition) 理解 → xref(references|call_hierarchy) 影响面 → file(read_file) 精读 → 输出判定

修复类：grep(text_search|ast_search) 定位 → xref(references|call_hierarchy) 影响面 → file(read_file) 读取 → edit(replace_range) 修改 → file(diagnostics) 检查 → build/test 验证


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

子代理不强制绑定 `mcp-go-agent-orchestration` 或 `mcp-orch` 生命周期；优先使用当前平台可用的原生子代理/多代理能力。只有任务确实需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`。

## 完成验证

在声称已完成、已修复、可提交或可合并之前，运行与变更面匹配的验证。

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
