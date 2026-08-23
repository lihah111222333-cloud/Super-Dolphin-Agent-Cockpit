# LSP 系统提示词与语义守卫规范

共享模型指令以仓库根目录 [`shared-developer-instructions.md`](../../shared-developer-instructions.md) 与 8.23 LSP 评测复盘方案 [`docs/8.23-lsp/LSP评测深度复盘与纯Prompt优化方案.md`](../8.23-lsp/LSP评测深度复盘与纯Prompt优化方案.md) 为准。本文档规定 MCP-LSP 工具契约、语义守卫与五步验证闭环。

---

## 1. 对外工具面与轻重武器分工 (Tool Surface & Weapon Matrix)

MCP-LSP 仅暴露三个纯语义工具：

- `structure`：定位 AST 符号定义、工作区符号、文档符号结构、折叠范围与语义 Token；用于消歧和理解符号结构。
- `xref`：查询符号引用（`references`）、调用层级（`call_hierarchy`）与类型层级（`type_hierarchy`）；用于查下游调用方和跨文件影响面。
- `diagnostics`：单文件或多文件即时语法、类型和语言服务器诊断；用于修改后的 Diff 级静态验证。

> **工具存在性铁律**：LSP 命名空间下**不存在** `file`、`read_file`、`inspect`、`grep`、`edit`、`patch_edit`、`completion` 等工具或兼容别名。
> - 文件读取：原生 Shell 命令（`cat` / `head` / `sed`）。
> - 纯文本/正则/字面量/配置/报错搜索：原生 `grep` / `rg`。
> - 代码修改：原生 `apply_patch`。
> - AST 符号定义定位：`structure`。
> - 跨文件引用与下游影响面：`xref`。
> - 静态语法与类型诊断：`diagnostics`。

---

## 2. 封闭参数契约与行协议 (Strict Parameter Contracts)

三个工具均接收严格封闭的 JSON Object，任何未知字段必须立即拒绝。

### 2.1 `structure`
```json
// 工作区符号搜索（多态消歧）
{"action": "workspace_symbol", "query": "SymbolName", "workspace_language": "python"}

// 单文件文档符号大纲
{"action": "document_symbol", "file_path": "relative/path.py"}

// 折叠范围与语义 Token
{"action": "folding_range", "file_path": "relative/path.py"}
{"action": "semantic_tokens", "file_path": "relative/path.py"}
```
- `workspace_symbol` 必须且只能传 `workspace_language` 或 `file_path` 之一；指定 `workspace_language` 时禁止传 `file_path`/`language_id`。
- 可选字段：`match_mode`（`"exact"` / `"fuzzy"`，默认 `"exact"`）、`max_results`（默认 20，上限 50）、`work_dir`。

### 2.2 `xref`
```json
// 查找引用（下游调用方守卫）
{"action": "references", "pos": "relative/path.py:line:column"}

// 调用层级（incoming / outgoing / both）
{"action": "call_hierarchy", "pos": "relative/path.py:line:column", "direction": "incoming"}

// 类型层级（supertypes / subtypes / both）
{"action": "type_hierarchy", "pos": "relative/path.py:line:column", "direction": "supertypes"}
```
- `pos` 格式严格统一为 `<file_path>:<line>:<column>`。
- `references` 接受可选布尔字段 `include_declaration`（默认 `true`）。
- 可选字段：`max_results`（默认 30，上限 50）、`language_id`、`work_dir`。

### 2.3 `diagnostics`
```json
// 单文件诊断
{"file_path": "relative/changed.py"}

// 批量文件诊断
{"file_paths": ["relative/file1.py", "relative/file2.py"]}
```
- 必须且只能传 `file_path` 或 `file_paths` 之一。
- 可选字段：`language_id`、`work_dir`。

### 2.4 返回协议解析
- 返回值仅通过 MCP `content` 纯文本行协议交互，不依赖 `structuredContent`：
  ```text
  OK total=1 showing=1 truncated=0 unit=diagnostic
  ROW file=relative/changed.py line=42 col=8 severity=error message="..."
  ```

---

## 3. 四大核心语义守卫与反过载约束 (The 4 Guardrails & Anti-Overkill)

### 3.1 下游集成点强制 xref 守卫 (Downstream Caller Guard)
- **触发场景**：修改公共基类、收集器/删除分派器（如 `Collector`）、模型字段、公共 API 或 AST 访问器节点时。
- **动作要求**：必须在修改前或修改后执行 `xref(action="references", pos="...")` 追溯外围下游集成点（如 Admin 实用工具、序列化器、关联消费模块），杜绝核心改动破坏外围调用。
- **负向约束**：严禁在无文件范围限制下对通用魔术方法（如 `__init__`、`save`、`get`）发起全局无差别引用检索，防止数百条无关调用挤爆模型上下文。

### 3.2 重型库多态重名方法精确消歧 (Polymorphic Disambiguation)
- **触发场景**：在大型多模块仓库（如 SymPy、Scikit-learn、Sphinx、Django）中存在上百个同名方法（如 `subs`、`eval`、`__new__`、`clean`）时。
- **动作要求**：严禁使用宽泛的文本 `grep` 导致数千行输出污染上下文。优先使用 `structure(action="workspace_symbol", query="<symbol>", workspace_language="python")` 精确定位符号 AST 定义与归属类。
- **协同原则**：AST 符号定位用 `structure`；字符串字面量、正则模式、配置键与错误消息继续使用原生 `grep`/`rg`。

### 3.3 增量代码行诊断聚焦 (Diff-Scoped Diagnostics Line-Protocol Focus)
- **动作要求**：从 `diagnostics` 返回的每条 `ROW` 记录中提取 `file` 与 `line`，严格只聚焦与当前补丁（patch hunks）修改行发生交集的错误。
- **存量容忍与零回归**：未修改历史存量代码中的旧 Warning/Hint 不得阻塞当前修复流程；但补丁本身必须引入 0 条语法错误和 0 条类型回归。

### 3.4 字符切片与边界计算行内自验 (Slice & Boundary Inline Verification)
- **触发场景**：涉及字符串切片、多重索引计算（如 `[a:b]`、`[start:end]`）、正则替换捕获组或 AST 文本过滤器时。
- **动作要求**：在修改生产代码前，必须执行单行 Python 行内脚本（`python -c "..."`）针对 Issue 描述中的真实样例字符串验证边界索引与断言，杜绝脑内静态推演偏差（如误吞首尾字符）。

---

## 4. 强制测试门禁与五步标准闭环 (Hard Test Gate & 5-Step Loop)

1. **复现与定位 (Reproduce & Locate)**：优先运行复现脚本或目标单测以观察红灯报错（RED），精准定位根本原因。
2. **切片与边界行内自验 (Slice & Boundary Inline Verification)**：针对复杂字符串/正则逻辑，运行单行内联脚本自验边界。
3. **补丁修改 (Edit)**：使用原生 `apply_patch` 应用最小必要改动。
4. **即时静态诊断 (Quick Type Check)**：立即对修改文件调用 `diagnostics`，验证语法与静态类型无报错。
5. **强制测试门禁 (Hard Test Gate)**：运行目标测试及受影响的公开测试套件。
   - **通过标准**：目标测试必须 100% 绿灯 PASS（`0 failures, 0 errors, exit=0`）。
   - **红线禁令**：测试失败（`exit != 0`）时**绝不允许**提交补丁。绝不能将 `git diff --check`、`py_compile` 或 clean git status 视为测试通过的替代品。

---

## 5. 异常处理与降级规范 (Fallback Discipline)

- **超时或负载过高**：若 `structure` 或 `xref` 超时，先收窄 `work_dir`、限制文件范围或调低 `max_results` 后重试。
- **降级记录**：若重试后仍失败，应显式记录 tool 名称、目标位置、错误信息与已尝试的收窄手段，严禁将未取得 diagnostics 伪造成 PASS。
