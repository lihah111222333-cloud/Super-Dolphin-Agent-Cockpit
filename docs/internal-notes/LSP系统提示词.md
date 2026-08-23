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

### 3.5 根因修复与参数管道透传豁免 (Root-Cause vs Parameter Plumbing)
- **根源状态与缺陷修复**：Bug 涉及全局配置、缓存变量（如 `DNS_NAME`）、环境字典或跨层共享输入时，严禁仅在末端消费者调用点（如 `message.py`）打局部防御补丁。必须使用 `structure` 向上追溯至变量定义或源头初始化位置（如 `utils.py`），在数据源头完成清洗与规范化。
- **参数管道透传豁免 (Mandatory Plumbing)**：当 API 签名扩展或新增参数（如 `samesite`、`encoding`、`secure` 等）时，在各层调用方（如 `middleware`、`storage`、`session`、`views`）中逐层透传有效参数属于必须的管道流转（Mandatory Plumbing），严禁误判为下游打补丁而刻意漏改或回退。

### 3.6 抽象基类与同族派生类继承全覆盖 (Polymorphic Hierarchy Completeness)
- **触发场景**：修改多后端驱动、Schema 编辑器、数据库客户端或 AST 访问器派生树（如 `BaseDatabase*`、`SchemaEditor`）时。
- **动作要求**：严禁在未核对基类的情况下假设 Bug 仅存在于单一具体派生类。必须先查验基类契约（若行为通用则在基类层修复），并遍历核验所有同族派生类（PostgreSQL、SQLite、MySQL 等）的多态一致性。

### 3.7 框架公共工具复用守卫 (Framework Utility Coherence)
- **触发场景**：需要进行 CSV 切割、正则分词、参数校验或数学表达式分发等通用处理时。
- **动作要求**：严禁在叶子业务模块内自行临时 `import csv` 或就地手写正则解析器。必须使用 `structure(action="workspace_symbol")` 查找并复用框架现有成熟工具（如 `django.utils.*`、`pylint.utils.*`、`sympy.core.*`），维持架构一致性。

### 3.8 零构建文件污染红线守卫 (Zero Build File Pollution Guard)
- **触发场景**：本地单测执行遇到环境报错、第三方库依赖冲突或包缺失时。
- **动作要求**：严禁在补丁中触碰或修改包构建与安装配置文件（如 `pyproject.toml`、`setup.py`、`setup.cfg`、`tox.ini` 或 requirements）。此类文件被改动会导致官方沙箱在应用 Patch 时重新构建环境触发冲突判负。除非 Issue 题目显式要求修改构建配置，否则补丁必须 100% 局限于业务与应用源码文件。

### 3.9 局部纯净辅助函数提取 (Clean Helper Extraction)
- **触发场景**：跨文件调用或复杂 AST 访问需要通用可复用逻辑，且框架内无现有公共工具时。
- **动作要求**：允许且鼓励在同包 `utils.py`（或局部辅助模块）中提取纯净、可复用的独立辅助函数，严禁在 Visitor/Inspector 等业务分派节点内部硬编码臃肿的内联逻辑。

### 3.10 功能关闭调度层阻断原则 (Feature Disable & Dispatch Bypass)
- **触发场景**：配置或参数显式要求关闭某项功能或设定阈值为 0 / False（如 `min_lines=0`、`enabled=False`）时。
- **动作要求**：必须在最高层调度入口（如 `run()`、`process()`）直接短路拦截返回（Short-circuit Bypass），避免调用下游格式化、渲染或空展示逻辑导致多余空头标记或意外副作用。

### 3.11 测试驱动的逆向溯源 (Test-Driven Backward Induction)
- **触发场景**：诊断模糊问题时。
- **动作要求**：优先定位并阅读 `tests/` 中的失败测试用例与断言，从断言差距逆向溯源执行流到源码缺陷点。

### 3.12 运算符与多态完整性 (Holistic Polymorphic & Operator Completeness)
- **触发场景**：修改类构造函数、工厂方法或返回签名时。
- **动作要求**：强制检查并同步更新所有相关的运算符重载（如 `__add__`, `__mul__`, `__eq__` 等魔术方法）及继承链，确保多态完整性。

### 3.13 当前工作树绝对聚焦 (Active Working-Tree Focus)
- **触发场景**：推演问题与排查逻辑时。
- **动作要求**：必须完全基于当前工作树（Active Working Tree）的实际代码逻辑和 AST 结构进行推演，严禁通过 `git log` / `git blame` 翻阅历史 commit 盲抄旧答案。

---

## 4. 强制测试门禁与五步标准闭环 (Hard Test Gate & 5-Step Loop)

1. **复现与定位 (Reproduce & Locate)**：优先运行复现脚本或目标单测以观察红灯报错（RED），精准定位根本原因。
2. **切片与边界行内自验 (Slice & Boundary Inline Verification)**：针对复杂字符串/正则逻辑，运行单行内联脚本（`python -c "..."`）针对 Issue 样例自验边界。
3. **补丁修改 (Edit)**：使用原生 `apply_patch` 应用最小必要改动。
4. **即时静态诊断与破除假绿幻觉 (Quick Type Check & Diagnostics Reality)**：立即对修改文件调用 `diagnostics`，验证修改行语法与静态类型无报错。
   - **破除假绿幻觉**：明确静态分析 `diagnostics` 仅用于捕获语法与类型错误，**绝不能替代真实单测执行**。Diagnostics 0 错误（假绿）绝不代表业务逻辑正确。
5. **强制测试门禁与环境自省 (Hard Test Gate & Environment Discovery)**：运行目标测试及受影响的公开测试套件。
   - **环境自省与主动探测 (Environment Discovery)**：当容器默认 `python` 缺少测试依赖（如缺少 `pytest` 或测试框架依赖）时，必须主动探测环境路径（如 `/opt/miniconda3/envs/*/bin/python`、`/opt/conda/envs/*/bin/python` 或 `.venv/bin/python`）直接运行 `pytest` / `python tests/runtests.py`。
   - **通过标准**：目标测试必须 100% 绿灯 PASS（`0 failures, 0 errors, exit=0`）。
   - **反主观合理化防线 (Anti-Rationalization Discipline)**：严禁将单测失败主观合理化为“旧测试已过时 (stale expectations)”或“测试环境噪音”。测试失败即代表破坏了既有契约，必须修复。
   - **红线禁令**：测试失败（`exit != 0`）时**绝不允许**提交补丁。绝不能将 `git diff --check`、`py_compile` 或 clean git status 视为测试通过的替代品。若测试失败，重新调用 `diagnostics` 排查语法细节并修复业务逻辑。

---

## 5. 异常处理与降级规范 (Fallback Discipline)

- **超时或负载过高**：若 `structure` 或 `xref` 超时，先收窄 `work_dir`、限制文件范围或调低 `max_results` 后重试。
- **降级记录**：若重试后仍失败，应显式记录 tool 名称、目标位置、错误信息与已尝试的收窄手段，严禁将未取得 diagnostics 伪造成 PASS。
