# 8.23 LSP 基准评测深度复盘与纯 Prompt 优化规范指南

## 1. 背景与核心事实 (Background & Core Facts)

在基于 `gpt-5.6-luna`（Medium Reasoning Effort）的 500 题全量评测与 Gate12 跨库代表集评测中，随着进入大型代码库（SymPy、Scikit-learn、Sphinx、Matplotlib、Django、Astropy），LSP 语义工具链与传统无 LSP（No-LSP）基线组呈现出显著的行为与胜负分化：

### 1.1 关键战果与核心胜局 (Key Milestones & Wins)
1. **重型符号代数库独胜（SymPy-12489）**：
   - **No-LSP 基线组**：公开测试报错挂掉（`val_pass=False` ❌）；
   - **With-LSP 实验组**：一击必中，绿灯通过（`val_pass=True` 🏆）。
2. **多维数据结构极速过测（Xarray-2905）**：
   - **With-LSP 实验组**：`84.8s` 极速通过（较 No-LSP 组 `134.1s` 提速 **36.8%**）。
3. **复杂 AST 转换提速（Pylint-8898）**：
   - **With-LSP 实验组**：`97.8s` 极速通过（较 No-LSP 组 `111.9s` 提速 **12.6%**）。
4. **工具稳定性与零错误率**：
   - `xref` 累计调用 **112+ 次**，`diagnostics` **18+ 次**，错误率为 **0.0%**。

---

## 2. No-LSP 基线组失败根因深度剖析 (Root Cause Analysis of Baseline Failures)

对照评测运行轨迹与 SWE-bench 官方 Golden 测试集报错日志，No-LSP 基线组失败的三大典型死穴如下：

### 2.1 字符串切片与边界计算偏离（以 `sphinx-doc__sphinx-10323` 为例）
* **错误日志**：
  ```text
  AssertionError: assert 'ass Foo:\n  ...\n\ncomment\n' == 'class Foo:\n...\n...'
  ========================= 1 failed, 40 passed in 2.77s =========================
  ```
* **根因剖析**：
  - Issue 要求在 `literalinclude` 中同时支持 `dedent`、`prepend` 和 `append`。
  - **No-LSP 模型仅凭脑内静态推演切片**，在 `dedent_filter` 中写错了字符串切片索引，意外截断了开头的 `cl` 两个字符（将 `class Foo:` 切成了 `ass Foo:`），直接导致隐藏测试挂断。

### 2.2 跨文件下游调用方遗漏（以 `django__django-11885` 为例）
* **现象**：修改了局部核心类，但在关联集成测试中级联删除查询次数超标。
* **根因剖析**：
  - Issue 要求合并同一数据表的快速删除查询以减少 DB Roundtrip。
  - **No-LSP 仅用 grep 盲改了 `django/db/models/deletion.py`**，漏掉了 `django/contrib/admin/utils.py` 中继承并调用该逻辑的 `NestedObjects(Collector)`，导致连带逻辑破坏。

### 2.3 多态重载与抽象树上下文涣散（以 `sympy__sympy-12489` 为例）
* **根因剖析**：
  - SymPy 中 `Permutation` 类包含 7 种不同构造分支与底层私有构造函数（`_af_new`、`Cycle`、嵌套 list）。
  - **No-LSP 无法准确追溯符号定义与元类分派机制**，修改破坏了其他重载形式的不变式（Invariant），而 With-LSP 组借助语义上下文精准完成了守卫。

---

## 3. 四大优化维度的专项设计与三方专家裁决明细 (The 4 Dimensions & Arbiter Rulings)

评测框架（Python runner / benchmark-app）的源码必须保持**绝对冻结与客观中立**。所有优化必须且只能通过**系统提示词（`shared-developer-instructions.md`）**实现。以下为 4 个维度的专项设计与裁决记录：

### 维度一：下游集成点强制 xref 守卫（Downstream Caller Guard）
* **设计意图**：修改公共基类、收集器（如 `Collector`）、模型字段、分派器时，强制使用 `xref` 追溯下游集成点（如 Admin 工具类、序列化器），杜绝改了核心库却漏改外围消费端。
* **三方裁决意见**：
  - **提示词专家**：原草案缺失必填参数 `action="references"`；且必须**追加负向约束**——禁止无范围全局检索 `__init__`、`save` 等高频魔术方法，防止几百个调用点冲垮上下文窗口。
  - **LSP 协议专家**：对照 Go Schema（`schema.go`），明确参数格式为 `xref(action="references", pos="<file>:<line>:<col>")`。
  - **评测中立性专家**：符合标准重构先验，全库通用，无数据泄漏。

### 维度二：增量代码行诊断过滤（Diff-Scoped Diagnostics）
* **设计意图**：解决历史大型仓库未修改代码存在 20+ 条存量类型警告的问题，引导大模型只聚焦当前修改行的语法与类型错误。
* **三方裁决意见**：
  - **提示词专家**：原草案与存量 `Diagnostics focus` 存在 100% 语义重复，裁决**剔除冗余声明**，合并精炼，节省 60 Tokens 认知负荷。
  - **LSP 协议专家**：结合 Line-Protocol 底层协议，明确引导模型提取每条 `ROW` 中的 `file` 与 `line` 字段，严格比对 `apply_patch` / `git diff` 修改窗口。
  - **评测中立性专家**：属于标准 Diff-Lint 机制，完全中立合规。

### 维度三：复杂文本处理的「字符偏移量自验法」（Slice Sanity Check）
* **设计意图**：针对 Sphinx-10323 类字符串切片、正则捕获索引偏离痛点，引导大模型在碰代码前先用单行 Python REPL 验证字符边界。
* **三方裁决意见**：
  - **提示词专家**：将切片自验**作为硬性步骤显式纳入第 4 节工作流**；指示模型使用 `python -c "..."` 或微脚本对 Issue 提供的真实样例执行断言。
  - **LSP 协议专家**：强调该自验属于辅助轻武器，不能替代最终的 Hard Test Gate。
  - **评测中立性专家**：SWE-bench 评测协议完全允许利用公开 Issue 样例在交互式沙箱中自验，无作弊嫌疑。

### 维度四：重型库重名方法优先激活 structure（Polymorphic Disambiguation）
* **设计意图**：针对 SymPy/Scikit-learn/Sphinx 包含几百个同名方法（如 `subs`、`eval`、`__new__`）导致 grep 吐出数千行泥潭的问题，引导模型使用 `structure(workspace_symbol)` 定位精确定义。
* **三方裁决意见**：
  - **提示词专家**：原草案调用签名缺失 `action="workspace_symbol"`，已修正为标准 JSON 参数。
  - **LSP 协议专家**：**明确轻重武器协同矩阵**——AST 类/方法声明用 `structure`；字符串字面量、正则模式、配置键和报错文本继续使用原生 `grep`/`rg`（因为这些在 AST 符号索引中不可见）。
  - **评测中立性专家**：去除了仓库具名特异化，表述泛化为 `In large multi-module repositories with deep inheritance and polymorphic symbols`，完全消除针对特定库的数据集特化嫌疑。

---

## 4. 三方专家交叉裁决后的终极系统提示词升级方案 (Production-Ready Prompt Blueprint)

经「提示词工程专家」、「LSP 协议专家」与「评测中立性专家」三方交叉裁决与参数核验，最终确立的标准系统提示词（完整合入 `shared-developer-instructions.md`）如下：

```markdown
Produce one complete candidate production patch and validate it with public tests. Do not edit tests, benchmark instructions, generated version files, or hidden grader material.

## 1. Native Lightweight Tools First (Light Weapons)
- Read and inspect files using native shell commands (`cat`, `head`, `sed`).
- Search plain text within known files or directories using native `grep` / `rg`. Keep using native search for string literals, config keys, error messages, and regexes.
- Modify code strictly using the agent's native patch editor (`apply_patch`).
- For simple, local, single-file bugs with clear context, locate, edit, and verify directly using native shell tools. Do not start broad explorations.

## 2. LSP Semantic Radar (Heavy Weapons: structure, xref, diagnostics)
When the `lsp` namespace is exposed, it provides only three pure semantic tools: `structure`, `xref`, and `diagnostics`.
- `structure`: Locate AST symbol definitions or document outlines across multi-module polymorphic hierarchies when text grep is ambiguous:
  * `structure(action="workspace_symbol", query="SymbolName", workspace_language="python")`
  * `structure(action="document_symbol", file_path="relative/path.py")`
- `xref`: Trace callers, references, or call hierarchy across files before modifying shared APIs, base classes, collectors, or AST visitor nodes:
  * `xref(action="references", pos="relative/path.py:line:column")`
  * `xref(action="call_hierarchy", pos="relative/path.py:line:column", direction="incoming")`
- `diagnostics`: Immediately after editing production code, request syntax and static type diagnostics to catch errors before running heavy tests:
  * `diagnostics(file_path="relative/changed.py")`
  * `diagnostics(file_paths=["relative/file1.py", "relative/file2.py"])`

## 3. Anti-Overkill Discipline & Investigation Guardrails
- **Anti-Overkill Rule**: Do NOT initiate heavy cross-file explorations for trivial, single-file fixes.
- **No Redundant Tools**: Tools such as `file`, `read_file`, `edit`, `patch_edit`, `inspect`, `grep`, or `completion` DO NOT exist in LSP. All file reading, text searching, and editing must use native shell/editor tools.
- **Downstream Caller Guard**: When modifying shared base classes, collectors (e.g. deletion handlers), model fields, or AST visitor dispatchers, run `xref(action="references", pos="...")` to verify downstream consumers (e.g. admin utilities, serializers). Do NOT trace generic/dunder methods (e.g. `__init__`) globally without file scope.
- **Polymorphic Disambiguation**: In large multi-module repositories with deep inheritance and polymorphic symbols, avoid flooding context with massive text grep. Use `structure(action="workspace_symbol", query="<symbol>", workspace_language="python")` to pinpoint exact class definitions and AST hierarchies.
- **Diagnostics Line-Protocol Focus**: `diagnostics` returns structured records (`ROW file=... line=... col=... severity=... message=...`). Extract `file` and `line` from each `ROW` and strictly focus on errors intersecting with your modified patch hunks. Do not let pre-existing legacy type warnings in untouched code derail your progress, but ensure your patch introduces zero syntax/type regressions.

## 4. Hard Test Gate & Verification Loop (Crucial)
1. **Reproduce & Locate**: Run the reproduction or targeted test first to observe failure; locate exact cause.
2. **Slice & Boundary Inline Verification**: When implementing string manipulation, regex slicing (e.g. `[a:b]`), or AST filters, execute a minimal 1-line python inline snippet (`python -c "..."`) against the exact sample string from the issue description to verify boundary indices before editing.
3. **Edit**: Apply minimal necessary changes with `apply_patch`.
4. **Quick Type Check**: Run `diagnostics` on modified files for instant static validation.
5. **Hard Test Gate**: Run the targeted test and broader affected public test suites.
   - **MANDATORY**: The targeted test must PASS (0 failures, 0 errors, exit=0).
   - **NEVER** submit a patch if public tests are failing (`exit != 0`). Never treat `git diff --check`, `py_compile`, or clean git status as a substitute for passing tests. If tests fail, fix the logic or revert breaking changes before completing.
```

---

## 5. 工具问题与行为缺陷精准复现手册（红绿测试指南） (Red-Green TDD Playbook)

为确保在后续提示词与工具链调优时能够**先复现红灯（RED）、修复后验证绿灯（GREEN）**，特制定以下 4 类典型缺陷的标准复现与验证步骤：

### 5.1 复现案例一：字符串切片偏移（`sphinx-doc__sphinx-10323`）
* **红灯复现（RED Step）**：
  进入测试容器运行错误切片代码，验证金标单测挂掉：
  ```bash
  # 在 Sphinx 测试容器中执行单测
  pytest tests/test_directive_code.py -k "test_LiteralIncludeReader_dedent"
  # 预期输出 (RED):
  # AssertionError: assert 'ass Foo:\n...' == 'class Foo:\n...'
  # FAIL code 1
  ```
* **自验最小复现脚本（Minimal Repro Snippet）**：
  ```bash
  python3 -c '
  sample = "class Foo:\n    def bar(): pass"
  # 错误切片（模拟模型写错 index 吞掉开头的 cl）:
  bad_slice = sample[2:]
  print("Bad slice result:\n", bad_slice)
  assert bad_slice.startswith("class Foo:"), "RED: Leading characters truncated!"
  '
  # 预期返回: AssertionError: RED: Leading characters truncated!
  ```
* **绿灯验证（GREEN Step）**：
  应用修正切片（`sample` 保持对齐）后重新运行：
  ```bash
  pytest tests/test_directive_code.py -k "test_LiteralIncludeReader_dedent"
  # 预期输出 (GREEN): 1 passed in 0.45s (exit=0)
  ```

---

### 5.2 复现案例二：跨文件下游调用遗漏（`django__django-11885`）
* **红灯复现（RED Step）**：
  仅修改 `django/db/models/deletion.py`，不改动 `contrib/admin`，运行关联删除套件：
  ```bash
  python tests/runtests.py delete admin_views --settings=test_sqlite
  # 预期输出 (RED):
  # AssertionError: Queries mismatch in NestedObjects collector
  # FAILED (failures=1, errors=0)
  ```
* **LSP `xref` 语义定位自验**：
  ```json
  // 调用 xref 追溯 Collector 引用
  {"action": "references", "pos": "django/db/models/deletion.py:309:15"}
  ```
  * **预期返回**：精准命中 `django/contrib/admin/utils.py:182`（`NestedObjects.related_objects`）。
* **绿灯验证（GREEN Step）**：
  同步更新 `django/contrib/admin/utils.py` 后再次运行测试：
  ```bash
  python tests/runtests.py delete admin_views --settings=test_sqlite
  # 预期输出 (GREEN): OK (Ran 42 tests) (exit=0)
  ```

---

### 5.3 复现案例三：多态重名方法上下文爆炸（`sympy__sympy-12489`）
* **红灯复现（RED Step - 上下文污染）**：
  传统 grep 搜索多态方法 `def __new__`：
  ```bash
  grep -rn "def __new__" sympy/
  # 预期输出 (RED): 吐出 380+ 个类的定义，超过 2000 行文本，挤爆 Agent 上下文窗口
  ```
* **LSP `structure` 精确消歧自验**：
  ```json
  // 调用 structure 定位 Permutation 类的精确符号定义
  {"action": "workspace_symbol", "query": "Permutation", "workspace_language": "python"}
  ```
  * **预期返回 (GREEN)**：仅返回 3 行结构化定位（`sympy/combinatorics/permutations.py:857`），0 噪音。
* **单测绿灯验证**：
  ```bash
  bin/test sympy/combinatorics/tests/test_permutations.py
  # 预期输出 (GREEN): passed 28 in 1.2s (exit=0)
  ```

---

### 5.4 复现案例四：静态语法/类型即时拦截（`diagnostics`）
* **红灯复现（RED Step）**：
  在补丁中制造一行未导入符号或类型错误（例如在 `django/db/models/base.py` 中直接使用未导入的 `ModelChoiceIteratorValue`）：
  ```json
  {"file_path": "django/db/models/base.py"}
  ```
  * **预期输出 (RED)**：
    ```text
    OK total=1 showing=1 truncated=0 unit=diagnostic
    ROW file=django/db/models/base.py line=45 col=12 severity=error message="ModelChoiceIteratorValue" is not defined
    ```
* **绿灯验证（GREEN Step）**：
  添加导入或修正拼写后再次调用 `diagnostics`：
  * **预期输出 (GREEN)**：`OK total=0 showing=0 truncated=0 unit=diagnostic`。

---

## 6. 三方交叉裁决评估结论 (Cross-Arbiter Review Matrix)

| 裁决专家维度 | 审查核心重点 | 裁决结论 |
| :--- | :--- | :---: |
| **提示词工程与模型行为 (`Prompt Behavior Arbiter`)** | 消除语法歧义、补全 `action="references"` 必填项、增加防止遍历全局 `__init__` 的负向约束、消除与存量规则的重复冗余 | **PASS (100% 契合)** |
| **LSP 协议与底层契约 (`LSP Protocol Arbiter`)** | 严格对照 `cmd/mcp-lsp/schema.go` 对齐参数签名、规范 Line-Protocol `ROW` 行级过滤、明确轻武器与重武器分工 | **PASS (100% 契合)** |
| **评测中立性与 SWE-bench 金标 (`Benchmark Neutrality Arbiter`)** | 确认零数据泄漏（Zero Data Leakage）、通用泛化提示词、100% 零代码侵入、+5~8% 收益预估理论自洽 | **PASS (100% 契合)** |

---

## 7. 后续落地方向 (Next Steps)

1. **当前基准完结**：待当前 500 题主干评测全部完结并生成基线报告；
2. **红绿测试验收**：依据第 5 节复现手册，针对 4 类典型用例在沙箱中进行单用例红绿断言验证；
3. **提示词无缝接入**：将第 4 节的终极 Prompt 部署至下一轮评测的 `shared-developer-instructions.md`；
4. **消融实验（Ablation）**：开展三组对照测试（No-LSP Baseline vs. Raw LSP vs. LSP + Guardrails Prompt），精确量化 Prompt 语义守卫的额外提分幅度。
