# LSP 系统提示词

共享模型指令以仓库根目录 [`shared-developer-instructions.md`](../../shared-developer-instructions.md) 为准。本文件只补充 MCP-LSP 三工具的调用边界。

## 对外工具面

MCP-LSP 只暴露：

- `structure`：文档符号、工作区符号、折叠范围、语义 token；用于找定义和理解符号结构。
- `xref`：引用、调用层级、类型层级；用于查调用方和跨文件影响面。
- `diagnostics`：单文件或批量文件的语法、类型和语言服务器诊断；用于修改后的语义检查。

不存在 LSP `file`、`read_file`、`inspect`、`grep`、`edit`、`patch_edit`、`completion` 工具或兼容别名。

## 封闭参数契约

- 三个工具都接收封闭 JSON object；未知字段必须拒绝。
- `structure.workspace_symbol` 使用 `workspace_language`；具体文件路由才使用 `language_id`。
- `xref` 的位置统一为 `pos=<file>:<line>:<column>`。
- `diagnostics` 必须且只能传 `file_path` 或 `file_paths` 之一，可选 `language_id` 和 `work_dir`。
- 返回值只读取 MCP `content` 的纯文本行协议，不依赖 `structuredContent`。

## 使用边界

- 文件内容：原生 `cat` / `head`。
- 文本定位：原生 `grep` / `rg`。
- 代码修改：原生 `apply_patch`。
- 局部单文件小 Bug：禁止滥用 `structure` / `xref` 做重型跨文件探索。
- 定义或符号归属不明：`structure`。
- 调用方或影响面不明：`xref`。
- 修改后：`diagnostics`，再运行匹配的构建和测试。

工具超时或不可用时，先收窄 `work_dir`、文件、符号或结果数后重试；仍失败则记录 tool、目标、错误和已尝试的收窄方式，不得把缺失 diagnostics 写成 PASS。
