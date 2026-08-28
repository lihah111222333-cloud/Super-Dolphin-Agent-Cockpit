# LSP 系统提示词与语义守卫规范

共享模型指令以仓库根目录 [`shared-developer-instructions.md`](../../shared-developer-instructions.md) 与 8.23 LSP 评测复盘方案 [`docs/8.23-lsp/LSP评测深度复盘与纯Prompt优化方案.md`](../8.23-lsp/LSP评测深度复盘与纯Prompt优化方案.md) 为准。本文档规定 MCP-LSP 工具契约、语义守卫与五步验证闭环。

---

## 1. 对外工具面与轻重武器分工 (Tool Surface & Weapon Matrix)

MCP-LSP 仅暴露三个纯语义工具：

- `structure`：定位 AST 符号定义、工作区符号、文档符号结构、折叠范围与语义 Token；用于消歧和理解符号结构。
- `xref`：查询符号引用（`references`）、调用层级（`call_hierarchy`）与类型层级（`type_hierarchy`）；用于查下游调用方和跨文件影响面。
- `diagnostics`：单文件或多文件即时语法、类型和语言服务器诊断；用于修改后的 Diff 级静态验证。

> **工具存在性铁律**：LSP 命名空间下只存在 `structure`、`xref`、`diagnostics`；不存在 `file`、`read_file`、`inspect`、`grep`、`edit`、`patch_edit`、`completion` 等工具或兼容别名。文件读取与纯文本搜索使用原生 `cat`/`head`/`grep`/`rg`，修改使用 `apply_patch`，shell/git/测试使用 `exec_command`。

---

## 2. 工具契约精简版 (Condensed Tool Contracts)

为提升 Attention 信噪比，LSP 工具参数严格遵守以下精简结构，拒绝任何未知字段：

- `structure`: 使用 `workspace_symbol` 或 `document_symbol` 定位真实符号；`workspace_language` 与 `file_path` 按 action 契约选择，不凭文本命中猜位置。
- `xref`: `{"action": "references"|"call_hierarchy"|"type_hierarchy", "pos": "path:line:col", "direction": "..."}`；`pos` 必须逐字来自 `structure` 返回的真实位置。
- `diagnostics`: `{"file_path": "..."}` 或 `{"file_paths": ["..."]}`；批量差量过滤使用 `diff_filter: {"mode":"changed_lines","changed_lines":{"path-a":[1],"path-b":[2]}}`，按文件映射 1-based 新文件侧行号。
  `OK` 只表示调用成功，不表示没有诊断；必须读取所有 `ROW`，Error、Warning、Information、Hint 均为待处理项。

---

## 2.1 With-LSP 触发矩阵与边界

**Mandatory With-LSP gate (EN, do not skip):** For multiple production files, parser/transform work, a shared base/registry/dispatcher, or a public/shared API, call `structure` before editing, copy its real `file:line:column` into `xref`, then run per-file `diagnostics` with `diff_filter.changed_lines` for every changed production file before exit. Missing evidence blocks the turn.
**With-LSP 强制门禁（中文）：** 多生产文件、parser/transform、共享基类/注册器/分派器或公共/共享 API，改前 `structure` → 复制真实位置 → `xref`；改后每个变更生产文件用逐文件 `diff_filter.changed_lines` 调 `diagnostics`，证据不全不得结束。

本节是条件式 LSP addendum，不是公共原生工具提示。只有 `structure`、`xref`、`diagnostics` 全部暴露时才注入；No-LSP 或缺少任一工具时零注入 LSP 名称、schema、示例和工作流，不模拟降级调用。

With-LSP 表示语义工具可用，不表示必须调用。零调用是有效的未采用行为，只要 MCP 三工具门禁真实通过；不得为了生成 receipt 而调用。生产代码 diagnostics、目标测试与官方 grader 分别属于静态检查、行为验证和成功真值，互不替代。

触发矩阵如下：

| 任务信号 | 语义动作 | Anti-Overkill 边界 |
| --- | --- | --- |
| 一个生产文件、局部符号、无共享/导出契约且无跨文件失败 | 位置明确时可跳过 `structure`/`xref`，用原生工具和局部测试 | 不为凑调用次数发起 `xref` |
| 多生产文件，或变更符号被其他文件导入/消费 | `structure` → 当前真实位置 → `xref(references)`；按需补调用/类型层级 | 仅查真实影响面，不盲搜无关名称 |
| 共享基类、注册器/注册表、dispatcher/分派器、parser/解析器、transform graph/转换图或公共 helper | 修改前必须 `structure` → 真实 `file:line:column` → `xref` | 根因在源头时禁止调用方胶水补丁 |
| 跨文件测试失败、重名符号、重命名或无法解释的调用方失败 | 重新 `structure` 定位，再用该次真实位置调用 `xref` | 失败后改变假设，不扩散探索 |

`xref.pos` 必须逐字复制当前 `structure` 返回的真实 `file:line:column`，不得凭文本命中、旧行号或心算列号。修改使用原生 `apply_patch`；共享符号先核对真实调用方，再对每个变更生产文件运行 `diagnostics`。

### Diff 级 diagnostics 精确请求

先执行 `git diff -U0`，从每个 hunk 的新文件侧 `+N,M` 取 1-based 行号（`N` 到 `N+M-1`）。批量请求必须让每个 `file_paths` 自己拥有映射：

```json
{
  "file_paths": [
    "internal/pkg/collector.go",
    "internal/pkg/admin.go"
  ],
  "diff_filter": {
    "mode": "changed_lines",
    "changed_lines": {
      "internal/pkg/collector.go": [42, 43, 44],
      "internal/pkg/admin.go": [18, 19]
    }
  }
}
```

例如 `@@ -41,3 +42,3 @@` 取 `[42,43,44]`，`@@ -0,0 +1,2 @@` 取 `[1,2]`。禁止多个文件共享行号、漏映射、使用旧文件 `-` 侧行号，或降级未过滤全文件掩盖缺口；不完整映射必须 fail-fast。

### 退出门禁与无进展

逐条读取 `diagnostics` 的 `ROW`：changed-line `Error` 必须修复或用具体证据解释；`Warning`、`Information`、`Hint` 都要审阅并修复或说明。显式分类变更行新问题、未变更行存量问题和环境/工具链问题；存量/环境项不能静默丢弃，每个变更生产文件都必须覆盖。

本轮生成的文件也算变更生产文件：若 `diagnostics` 支持其语言，必须在同一逐文件请求中覆盖并列入自检；若不支持，向 runner 输出机器可识别豁免 `{"exemptions":[{"path":"gen/schema.json","kind":"generated","reason":"diagnostics_unsupported","active_round":1}]}`，禁止静默跳过。

结束前输出一行机器可识别轨迹自检：`{"lsp_self_check":{"changed_production_files":[...],"structure_evidence":[{"tool":"structure","pos":"path:line:column"}],"xref_evidence":[{"tool":"xref","action":"references","pos":"path:line:column"}],"diagnostics_coverage":[...],"exemptions":[...]}}`。逐一列出本轮 changed production files、structure/xref 证据与 diagnostics 覆盖路径；缺任何一项必须在同一 turn 补调用，不能只解释。

相同 patch/hash、没有 `file_change` 或收到 contract failure 时，只补缺失证据并改变假设；不重做已通过测试、不重复相同 patch、不重复 grader。`runner` 是最终硬守卫，prompt/diagnostics 通过不能替代 runner 的权威结果。

---

## 3. 认知守卫与刚性架构纪律 (Cognitive Guards & Architectural Disciplines)

### 3.1 多轮假设证伪协议 (Universal Hypothesis Falsification Protocol)
- **禁令**：收到多轮迭代单测 Traceback 时，**严禁**锚定原补丁做局部 `if/else` 胶水缝补。
- **强制动作**：必须先在认知中硬回退（Hard Revert）局部假设，判定单测失败是“边缘分支缺失”还是“基础架构前提违背了全局系统不变量（Global Invariants）”。若是后者，必须在状态定义源头彻底重构。对于复杂字符串切片与边界计算，在修改前必须运行单行 Python 脚本针对真实样例验证断言。

### 3.2 编译器与分派层优先法则 (Compiler & Dispatcher Layer Priority)
- **禁令**：当缺陷源于查询编译、语法生成、AST 转换、表达式重写或请求分派时，**严禁**在无特定方言语法缺陷时去广泛修改各个底层后端方言或驱动实现。
- **强制动作**：优先在编译器、转换器或分派层（如 `compiler` / `dispatcher` / `visitor`）统一解决。仅当语法缺陷被精确定位为某个方言特有时，才修改特定后端。修改语法分析器、模式定义或注册中心时，必须主动检查是否存在伴生的代码生成物、语法解析表（如 `.parsetab`、`.c` 扩展）或模块级注册字典需要同步更新。

### 3.3 根因最小修复原则与下游调用守卫 (Root-Cause Minimality Priority & Downstream Caller Guard)
- **禁令**：**严禁**在调用方堆砌防御性补丁；**严禁**为局部单文件缺陷展开重型跨文件盲搜。
- **强制动作**：Bug 涉及工具函数、转换器或底层逻辑时，必须在根源函数内部直接修复。修改基类或调度器时，必须通过 `xref` 守卫追溯下游调用方（Downstream Caller Guard），杜绝核心改动破坏外围接口。

### 3.4 公共 API 向后兼容守则 (Public API Backward Compatibility Guard)
- **禁令**：**严禁**在重命名或重构公开方法参数时硬性删除存量参数，导致外部调用崩溃。
- **强制动作**：重命名公开方法参数（如 `old_param -> new_param`）时，必须保留原参数作为废弃关键字参数（Deprecated Keyword Argument），发出弃用警告（Warning）并代理转发回新参数，确保 100% 向后兼容。

### 3.5 富比较与运算符完备性守则 (Rich Comparison & Operator Completeness Guard)
- **禁令**：在各类领域对象、AST 节点或自定义类中重写比较方法时，**严禁**对未处理类型直接抛出未捕获异常或返回破坏对称比较梯度的错误类型。
- **强制动作**：重写比较方法（`__eq__`, `__ne__`, `__lt__`, `__le__`, `__gt__`, `__ge__`）时，确保各派生类对不支持的类型/操作数一致返回 `NotImplemented`，以便运行时优雅回退并尝试反射操作符。

### 3.6 Git 历史审查纪律 (Safe Git Reference Guard)
- **禁令**：使用 `git show` 参考历史提交时，**严禁**在未先查看 `git show --stat` 前手动添加不完整的 pathspec 过滤；**严禁**机械删除正在被老代码依赖调用的核心方法或辅助逻辑。
- **强制动作**：参考历史提交时必须先以 `git show --stat <commit>` 全局了解变更面。合入或移植历史改动时，必须基于当前代码库的实际依赖与上下文进行适配与自洽性检查，严禁盲目删改在用接口。

### 3.7 零构建污染与增量诊断守卫 (Zero Build Pollution & Diff-Scoped Diagnostics Guard)
- **禁令**：**严禁**在补丁中触碰或修改包构建与安装配置文件（如 `pyproject.toml`、`setup.py`、`setup.cfg`、`tox.ini` 或 requirements）。
- **强制动作**：执行即时静态诊断 `diagnostics` 时，严格按 `git diff -U0` 新文件侧行号过滤；changed-line `Error` 必须修复或以证据解释，其他严重度必须审阅并分类。未修改行的存量报警和环境/工具链问题可单独报告，但不能静默丢弃；任何未覆盖的变更生产文件都不能通过门禁。

---

## 4. 测试床环境与强制测试门禁闭环 (Testbed Environment & Hard Test Gate Loop)

- **测试床执行守则 (Testbed Execution Guard)**：所有复现验证与单测执行**必须**严格使用隔离的测试床环境：
  * Python 解释器：`/opt/miniconda3/envs/testbed/bin/python`
  * Pytest 测试器：`/opt/miniconda3/envs/testbed/bin/pytest`
  * 严禁未经测试床环境验证的无验证盲交。

### 五步标准验证闭环：
1. **复现与定位**：优先基于 `/opt/miniconda3/envs/testbed/bin/python` 或 `/opt/miniconda3/envs/testbed/bin/pytest` 运行复现脚本或目标单测以观察红灯报错，精准定位根本原因。
2. **切片与边界行内自验**：涉及字符串切片、正则捕获范围或 AST 行缩进处理时，必须前置执行单行内联脚本（`/opt/miniconda3/envs/testbed/bin/python -c "sample = ...; assert sample[...] == expected"`）针对 Issue 真实样例验证断言，杜绝心算索引偏移。
3. **补丁修改**：使用原生 `apply_patch` 应用最小必要改动。
4. **即时静态诊断**：调用 `diagnostics` 验证修改行无语法/类型报错。
5. **强制测试门禁**：使用测试床环境运行目标测试及受影响的公开测试套件，必须 100% 绿灯 PASS（0 failures, 0 errors, exit=0）后方可完成任务。若测试失败，必须修复逻辑或回退破坏性改动。

---

## 5. 异常处理与降级规范 (Fallback Discipline)

- **超时或负载过高**：若 `structure` 或 `xref` 超时，先收窄范围或调低 `max_results` 后重试。
- **降级记录**：若失败，严禁伪造 diagnostics 结果，必须显式记录错误信息并执行回退。
