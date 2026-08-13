# AGENTS.md 样板（LSP 强制工作流）

> 使用方式：目标仓库没有 `AGENTS.md` 时可复制本文件；已有文件时必须人工合并，禁止直接覆盖。随后按项目实际情况补充构建、测试和目录规则。

<!-- mcp-lsp-project-rules:start -->

## 项目事实来源

1. 源码和同包测试是当前行为的事实来源。
2. 修改前先阅读仓库 `README.md`、架构决策、契约和代码地图。
3. 历史计划和旧报告只用于追溯，不得覆盖当前源码、测试和已接受契约。

## LSP 工具链

- 涉及源码的路径判断、行为解释、审查或修复，必须使用 LSP 完成导航、理解、影响面分析、精读和诊断。
- 对外工具短名固定为 `file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit`、`completion`。
- `file(action=diagnostics)` 是诊断入口；不存在独立的 `diagnostics` 工具。
- `patch_edit` 是编辑入口；不要使用 `edit`、`lsp_edit`、`lsp_file` 等旧别名。
- Shell 只用于构建、测试和必要脚本，不得用 `rg + cat/sed`、`gopls check` 或纯 shell 检查替代 LSP 证据。

## mcp-lsp 启动与平台契约

- 独立 stdio `mcp-lsp` 的客户端 server env 必须显式包含 `SUPER_DOLPHIN_RUNTIME_MODE=dev`、指向运行时资源根的 `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR`、`SUPER_DOLPHIN_DEPENDENCY_PROFILE=production`，以及 `GO_AGENT_LSP_ROOT`、合法 JSON 数组形式的 `GO_AGENT_LSP_ROOTS`。
- 缺少必需字段时必须 fail-fast；不得在二进制、项目规范或配置脚本中增加隐式默认值。
- Windows 原生 PowerShell/Desktop 客户端使用 `mcp-lsp-windows-*.exe` 和 Windows drive/UNC 路径；WSL 内客户端使用 `mcp-lsp-linux-*` 和 `/mnt/...` Linux 路径。按实际启动 server 的客户端进程自动选择，不增加人工切换变量，也不混用两种路径体系。
- file URI 只在 URI 到本机路径的边界百分号解码一次，再做 workspace containment。中文、空格或字面 `%` 路径必须用真实 `file(action=diagnostics)` 验证；`path_outside_workspace` 不得写成 PASS。
- Go 项目必须保留用户的 `GOTOOLCHAIN=auto` 或 `<name>+auto` 策略。工具链探测在解析出的 module/go.work 目录执行；不得因 PATH 默认 Go 较旧而把固定补丁版本写成通用配置。

## 强制证据链

审查或解释：

1. `grep(text_search|ast_search)` 定位候选符号。
2. `structure(document_symbol|workspace_symbol)` 确认文件或工作区结构。
3. `inspect(definition|hover|type_definition|implementation)` 理解定义和类型。
4. `xref(references|call_hierarchy)` 确认调用方、被调用方和影响面。
5. `file(read_file)` 精确读取实现及相关测试。
6. `file(diagnostics)` 获取诊断后再输出结论。

修复：

1. 按上述证据链完成定位和影响面分析。
2. 使用 `patch_edit(replace_range|rename|code_action|format)` 修改。
3. 对所有修改过的源码运行 `file(diagnostics)`。
4. 运行与变更面匹配的构建和测试。
5. 涉及 `cmd/mcp-lsp` 时，至少编译目标客户端平台；同时发布 PowerShell 与 WSL 包时分别验证 Windows 与 Linux 二进制的 GOOS/GOARCH。

## LSP 不可用时

- 先收窄 `work_dir`、路径、符号、查询、语言或结果数后重试。
- 重试仍失败时必须报告 blocker：工具和 action、`work_dir`、目标文件或符号、原始错误、已经尝试的收窄方式。
- 不得静默降级，不得把缺失 diagnostics 或工具失败写成 PASS。
- diagnostics 中的 Error、Warning、Information、Hint 都必须处理；无法修复时明确记录原因。

## 修改与验证

- 保持最小变更，不覆盖用户已有改动，不修改任务范围外的文件。
- 遇到异常、配置为空或数据缺失时 fail-fast；禁止静默降级、吞错或隐式默认值。
- 写代码前先查定义和引用；改共享符号前必须完成 `xref` 影响面分析。
- 声称完成、可提交或可合并前，必须运行与变更面匹配的 diagnostics、构建和测试，并报告实际结果与未覆盖项。
- 禁止使用 `--no-verify` 绕过 Git hooks，除非用户针对当前操作明确授权。

## 子任务协作

- 每个子任务只授予一个边界清晰、可独立验证的范围。
- 任务说明必须给出目标仓库的绝对 `cwd`、允许修改的路径、验收标准和禁止触碰的范围。
- 子任务结果只能作为线索；主任务在合并结论前必须复核源码、diff、diagnostics 和测试证据。

<!-- mcp-lsp-project-rules:end -->
