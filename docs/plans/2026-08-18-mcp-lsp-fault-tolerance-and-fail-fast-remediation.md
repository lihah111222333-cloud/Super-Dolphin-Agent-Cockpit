# LSP 工具链容错增强、传输分层与 Fail-Fast 边界治理实施方案

> **文档路径**：`docs/plans/2026-08-18-mcp-lsp-fault-tolerance-and-fail-fast-remediation.md`  
> **状态**：**Final Sign-off & Ready for Execution**（已通过 1003 会话日志检测、8 大多语言专家裁决、4 轮共 24 方合规复核与红队对抗审查，并已合入执行过程安全红绿测契约）  
> **责任范围**：`cmd/mcp-lsp/`、`internal/mcpserver/common/`、多语言 AST 与 Schema 契约体系  
> **核心哲学**：**传输与表示层良性归一化（吸收格式抖动），业务与编译层坚决 Fail-Fast（死守确定性红线）**  
> **执行过程安全准则**：**真实二进制 E2E 红测先行 $\to$ 单元/契约红测 $\to$ 原子编码修绿 $\to$ 重新编译真实二进制 E2E 终验全绿**

---

## 目录
1. [背景与问题诊断](#一背景与问题诊断)
2. [多语言交叉裁决共识与核心架构设计](#二多语言交叉裁决共识与核心架构设计)
3. [执行过程安全与双重红绿测试契约（Process Safety Protocol）](#三执行过程安全与双重红绿测试契约process-safety-protocol)
4. [详细改造方案与技术规格](#四详细改造方案与技术规格)
   - [Phase 1: 传输层元数据过滤、乐观锁前置与 Schema 修正（P0）](#phase-1-传输层元数据过滤乐观锁前置与-schema-修正p0)
   - [Phase 2: Diff 文本、坐标与编码表示层良性归一化（P1）](#phase-2-diff-文本坐标与编码表示层良性归一化p1)
   - [Phase 3: 多语言生态闭环、函数抽取与跨文件锁守卫（P1）](#phase-3-多语言生态闭环函数抽取与跨文件锁守卫p1)
   - [Phase 4: Actionable 诊断错误反馈与测试守卫（P2）](#phase-4-actionable-诊断错误反馈与测试守卫p2)
5. [四轮红队对抗审查防御性收紧矩阵](#五四轮红队对抗审查防御性收紧矩阵)
6. [修改文件清单与前后代码设计](#六修改文件清单与前后代码设计)
7. [坚决禁止的隐式兜底红线（Fail-Fast 边界）](#七坚决禁止的隐式兜底红线fail-fast-边界)
8. [全量验证计划与测试守卫同步矩阵](#八全量验证计划与测试守卫同步矩阵)

---

## 一、背景与问题诊断

在对历史会话记录（扫描 1003 个会话 Transcript，提取 1149 条真实 LSP 工具调用样本）的深度分析中发现，当前 `cmd/mcp-lsp` 虽具备完善的 Fail-Fast 机制，但在模型交互层与传输层存在以下高频非业务性调用摩擦：

1. **宿主平台元数据注入误杀（P0 痛点）**：
   Google Gemini CLI、Claude Code、Antigravity 等 IDE 在调用 `tools/call` 时自动向 `arguments` 注入通用元数据（`toolAction`, `toolSummary`, `_meta`）。由于业务 DTO 启用了 `additionalProperties: false` 与 `DisallowUnknownFields`，导致合法工具调用因 `json: unknown field "toolAction"` 直接被拒。
2. **Schema 属性缺失与假阳性拒绝（P0 痛点）**：
   - `patch_edit` Go 结构体支持 `Version int`（乐观锁版本），但 `newPatchEditSchema()` 遗漏声明 `version` 属性；
   - `file.scope` 的 Schema 枚举仅定义了 `["lines"]`，显式传递默认语义 `scope="function"` 时被 Schema 校验器拦截；
   - `file(diagnostics)` Go Handler 支持无参查询当前已打开文档，但 Schema `oneOf` 强制必须包含 `file_path` 或 `file_paths`。
3. **Patch 纯空行格式脆弱与拓扑时序漏洞（跨语言最高频失败点）**：
   Unified Diff 规范中上下文空行应为 `' '`（1个前导空格）。但 LLM 代码生成与 Black / Ruff / gofmt 格式化时空行通常为 `""`（0 字符）。`parsePatchBodyLine` 遇到 `""` 报 `must start with ' ', '-', or '+'` 强制阻断；且历史代码中存在单边削减 `oldLines` 尾部空行导致的非对称行错位漏洞。
4. **单/多路径参数形式摩擦**：
   模型直觉常传入单个字符串 `"paths": "internal/foo"`，由于 Schema 仅声明 `array` 导致反序列化失败。
5. **坐标冒号段内空格偶发解析失败**：
   LLM 分词偶发产生 `"file.go: 42 : 9"`，因段内未做 `strings.TrimSpace` 导致 `strconv.Atoi(" 9")` 失败。
6. **多语言生态盲区与生命周期自愈漏洞**：
   - React TSX 箭头函数组件（`const App = () => {}`）在 DocumentSymbol 中为 `Variable`/`Constant`，当前 `funcrange.go` 抽取函数时常年返回 `outside_function`；
   - 语言短别名（`ts`, `tsx`, `js`, `jsx`, `sql`, `sh`, `cs`）在 `search/fileutil.go` 与 `searchutil.go` 中缺失映射；
   - 无扩展名可执行脚本（`bin/*`, `scripts/*`）未接入安全 Shebang（`#!/usr/bin/env python` 等）嗅探；
   - `multilsp/manager_retry.go` 的重试白名单遗漏 `workspace/symbol`，导致崩溃后无法透明自愈。

---

## 二、多语言交叉裁决共识与核心架构设计

经 8 大多语言专家（Go, TS/JS, Python, Rust, C/C++/C#, Shell/DevOps, SQL, Meta-Arbiter）交叉裁决与四轮红队极限对抗，确立以下架构准则：

### 1. 洋葱模型分层解耦（Transport-DTO Separation）
```
  [外部 MCP 客户端 Payload (携带 toolAction, toolSummary, _meta)]
                             │
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 传输与宿主适配层 (Transport / Wrapper Sanitization Layer)  │
  │ - 位于: internal/mcpserver/common/scope.go, server.go    │
  │ - 职责: 提取可信 Scope (_cwd, _agentId)，剥离宿主元数据    │
  │ - 安全: 限制元数据单字段 <= 4096B，防 DoS 内存膨胀        │
  └──────────────────────────────────────────────────────────┘
                             │ (纯净的工具业务参数 JSON)
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 业务 DTO 解码校验层 (Strict Business DTO Unmarshaling)    │
  │ - 位于: cmd/mcp-lsp/tools/factory_bindings.go             │
  │ - 职责: 严格校验业务字段 (DisallowUnknownFields)          │
  │ - 安全: isReservedFieldStem 规范化拦截 _Cwd 等变体穿透   │
  │ - 动作: 发现非法业务参数立即 Fail-Fast 报错并给出提示       │
  └──────────────────────────────────────────────────────────┘
```

### 2. 表示层良性归一化 vs 业务层严格 Fail-Fast 的判定界限
- **准予良性归一化（表示层 Syntax Sugar）**：
  - 语义无歧义（Isomorphic）、零信息丢失、零状态伪造（如非空字符串升切片、空行视作上下文空行、坐标段内 Trim、严格字面量 Coercion、Unicode BOM / Format 控制符清洗）。
- **坚决禁止隐式兜底（业务/状态层）**：
  - 严禁路径默认回退根目录、严禁光标默认第1列、严禁模糊匹配 Patch、严禁 AST 失败降级为文本正则、严禁语言服务未就绪伪造空列表、严禁废除 `additionalProperties: false` 变为宽松 map。
- **关于 `workspace_language` 的特殊裁决（Rust/Go/C++ 强力共识）**：
  - **严禁静默将 `language` 字段映射为 `workspace_language`**（防止掩盖调用方参数漂移）。
  - **正确做法**：Schema 严格校验 Fail-Fast 拦截，并在解码失败信息中返回 `rustc` 风格的精准 Actionable 迁移指引。

---

## 三、执行过程安全与双重红绿测试契约（Process Safety Protocol）

为杜绝任何“盲改代码”、“假绿冒进”或“测试与真实运行脱节”的工程风险，本次实施必须严格遵循 **双重红绿测试契约（Dual Red-Green Testing Protocol）**：

```
┌───────────────────────────────────────────────────────────────────────────────────────────┐
│ 步骤 1: 真实二进制 E2E 红测先行 (Real Binary E2E Red Test)                                │
│ - 编写 cmd/mcp-lsp/fault_tolerance_e2e_test.go                                            │
│ - 调用 buildMcpLSPBinaryForTest 构建当前源码真实二进制，通过真实 stdio 注入 6 大失败 Payload    │
│ - 执行并确认红测: 断言精确捕获当前版本的预见性失败 (E2E RED 确认)                          │
└─────────────────────────────────────────────┬─────────────────────────────────────────────┘
                                              │
                                              ▼
┌───────────────────────────────────────────────────────────────────────────────────────────┐
│ 步骤 2: 单元与契约层红测 (Unit & Contract Red Tests)                                      │
│ - 在 cmd/mcp-lsp/ 各子模块编写针对具体函数/类型的单元测试用例                               │
│ - 执行 go test 确认测试处于 RED 失败状态 (Unit RED 确认)                                    │
└─────────────────────────────────────────────┬─────────────────────────────────────────────┘
                                              │
                                              ▼
┌───────────────────────────────────────────────────────────────────────────────────────────┐
│ 步骤 3: 原子编码修绿 (Green Implementation Phase)                                         │
│ - 按照 Phase 1 -> Phase 2 -> Phase 3 -> Phase 4 实施代码改造                              │
│ - 逐个模块运行 go test 验证单元测试 100% 变绿 (Unit GREEN 达成)                             │
│ - 同步更新 cmd/mcp-lsp/schema_test.go 守卫断言                                            │
└─────────────────────────────────────────────┬─────────────────────────────────────────────┘
                                              │
                                              ▼
┌───────────────────────────────────────────────────────────────────────────────────────────┐
│ 步骤 4: 重新编译真实二进制并 E2E 终验全绿 (Final E2E Green Pass on Real Binary)            │
│ - 重新编译最新 cmd/mcp-lsp 真实二进制                                                     │
│ - 重新运行 fault_tolerance_e2e_test.go，断言 6 大场景全部转为成功 (E2E GREEN 达成)         │
│ - 运行全仓守护测试 ./scripts/test_with_guard.sh 确保 0 契约漂移与 0 架构违规                │
│ - 只有真实二进制 E2E 也全绿，才算最终任务通过与可交付！                                    │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 四、详细改造方案与技术规格

### Phase 1: 传输层元数据过滤、乐观锁前置与 Schema 修正（P0）

#### 1.1 传输层剥离宿主元数据与保留字规范化 Stem 拦截
- **涉及文件**：`internal/mcpserver/common/scope.go`、`cmd/mcp-lsp/tools/atomic_write.go`、`cmd/mcp-lsp/tools/factory_bindings.go`
- **规格**：
  1. 在 `ToolCallParams` 中显式声明 `ToolAction`, `ToolSummary`, `ToolActionSnake`, `ToolSummarySnake`，吸收顶层注入；
  2. 在 `stripToolWrapperFields` 中将 `toolAction`, `toolSummary`, `tool_action`, `tool_summary`, `_meta` 从 `arguments` 中安全剔除；
  3. **红队安全加固 1（单字段限长）**：限制 `toolAction`/`toolSummary` 单个元数据字段长度 $\le 4096$ 字节（严禁误杀 256KB 的 patch 文本）；
  4. **红队安全加固 2（规范化 Stem 拦截）**：采用 `isReservedFieldStem` 规范化比对，全量拦截 `_cwd`, `_agentId`, `_Cwd`, `_AGENT_ID`, `_workspace_roots`, `_threadId`, `_callId` 等所有大小写与下划线变体，杜绝参数命名空间穿透。

#### 1.2 修复并发乐观锁（Pre-write Lock Validation）与补齐 `version` 声明
- **涉及文件**：`cmd/mcp-lsp/schema.go`、`cmd/mcp-lsp/tools/tool_edit_replace.go`、`cmd/mcp-lsp/multilsp/manager_explicit_documents.go`、`cmd/mcp-lsp/tools/tool_edit_replace_update.go`
- **规格**：
  1. 在 `newPatchEditSchema()` 中补充属性声明：`"version": integerProp("Optional document edit version for optimistic concurrency control.")`；
  2. **红队安全加固 1（前置校验）**：在落盘（`atomicReplaceFile`）**之前**先严格校验 LSP 乐观锁版本；若 `req.Version > 0` 且与服务端当前文档版本不匹配，**立即拒绝写盘并 Fail-Fast 报错 `concurrency_version_conflict`**；
  3. **红队安全加固 2（废除静默自增）**：废除 `manager_explicit_documents.go` 中对 full change 的 `version = state.lspVersion + 1` 静默自增覆盖，确保版本冲突绝不被吞掉；
  4. **红队安全加固 3（禁止取消 LSP 同步导致状态机撕裂）**：在 `tool_edit_replace_update.go` 中，`git diff` 确认后**禁止强行 cancel 正在进行的 LSP 同步 context**，保证 LSP 内存版本号 `state.lspVersion` 正常推进并与磁盘严格一致，杜绝连续编辑产生假阳性冲突。

#### 1.3 扩展 `file.scope` 枚举声明与渲染层同步
- **涉及文件**：`cmd/mcp-lsp/schema.go`、`cmd/mcp-lsp/tools/tool_file.go`、`cmd/mcp-lsp/tools/tool_file_render.go`
- **规格**：
  - `schema.go` 中 `scope` 声明为：`enumProp("Read mode override...", "lines", "function", "line")`；
  - `tool_file.go` 中将 `"line"` 归一化为 `"lines"`；
  - `tool_file_render.go` 的 `renderReadRows` 响应头 `ATTR` 中补充输出 `version=<current_lsp_version>`，打通乐观锁冲突自愈闭环。

#### 1.4 `file(diagnostics)` 支持已打开文档无参调用
- **涉及文件**：`cmd/mcp-lsp/schema.go`
- **规格**：
  调整 `diagnostics` action 的 Schema 条件，允许不传 `file_path`/`file_paths`，直接查询所有已打开文档的诊断。

---

### Phase 2: Diff 文本、坐标与编码表示层良性归一化（P1）

#### 2.1 Patch 内部纯空行容错、多 Hunk 拓扑单调推进与对称性保护
- **涉及文件**：`cmd/mcp-lsp/edit/patchparse.go`、`cmd/mcp-lsp/edit/patchmatch.go`、`cmd/mcp-lsp/tools/tool_edit_support.go`
- **规格**：
  1. 在 `parsePatchBodyLine` 中：
     ```go
     if line == "" || line == "\r" {
         return patchBodyLine{kind: ' ', text: ""}, nil
     }
     ```
  2. **红队安全加固 1（禁止全空行伪锚点）**：若 Hunk 全部由 `""` 组成且无任何增删，严禁升格为 Section Anchor，直接抛出 `ErrInvalidPatch`；
  3. **红队安全加固 2（彻底废除非对称截断）**：彻底删除 `patchmatch.go:270-273` 中单边削减 `oldLines` 尾部空行的逻辑，比对与替换必须严格保持拓扑对称；
  4. **红队安全加固 3（多 Hunk 拓扑单调递增推进）**：在 `matchContextHunk`（`patchmatch.go:132`）中推进 `nextMinimum = candidate.startOffset + len(hunk.NewText)`，防止非锚点多 Hunk 逆序或重叠匹配；
  5. **红队安全加固 4（EOL 保持与 \r 规范化）**：嗅探源文件换行符类型，写回前自动转换 `NewText` 为源文件 EOL；`tool_edit_support.go` 使用统一 Replacer 处理 `\r\n` 与 `\r`。

#### 2.2 `pos` 坐标冒号段内空白 Trim、提示截断与 UTF-16 单调性保护
- **涉及文件**：`cmd/mcp-lsp/tools/position.go`
- **规格**：
  1. 在 `parsePositivePosSegment` 中执行 `strconv.Atoi(strings.TrimSpace(value))`；
  2. **红队安全加固 1（单调性保护）**：在 `utf16OffsetsForRunes` 中，当 `utf16.RuneLen(value) < 0`（非法/损坏 UTF-8 字符）时安全设为 1，确保 `offsets` 严格单调递增，防止代理对检索撕裂；
  3. **红队安全加固 2（建议数量硬上限）**：`suggestedIdentifierColumns` 增加硬上限（`maxSuggestions = 8`），超出部分立即截断，防止超长单行造成堆内存震荡与 JSON DoS。

#### 2.3 字符串自动提升为切片与 BOM/Unicode 格式控制符清洗
- **涉及文件**：`cmd/mcp-lsp/tools/factory_bindings.go`、`cmd/mcp-lsp/schema.go`、`cmd/mcp-lsp/tools/tool_file.go`
- **规格**：
  1. 引入 `CleanPathParameter`，安全剥离 `\uFEFF` (BOM) 与 `unicode.Is(unicode.Cf, r)` 格式控制字符（包含 BiDi 隔离符等）；
  2. **红队安全加固（严禁提升空字符串）**：清洗后若为空，**坚决报错阻断**，严禁将空路径提升为 `[""]`；
  3. `schema.go` 声明 `oneOf: [{"type": "string", "minLength": 1}, {"type": "array", "items": {"type": "string", "minLength": 1}, "minItems": 1}]`；
  4. `tool_file.go` 中的 `readBatch` 增加前置守卫：有效路径数为 0 时必须抛出错误，严禁返回 `Success: true`。

#### 2.4 严格白名单标量 Coercion
- **涉及文件**：`cmd/mcp-lsp/tools/factory_bindings.go`
- **规格**：
  - 布尔值仅严格接受字面量 `"true"` 和 `"false"`；
  - 整数仅严格接受正则 `^[0-9]{1,9}$` 的无符号正整数；
  - **维持 `CaseSensitive *bool` 的 `nil` 状态**，空值或未传递时保持 `nil`（Smart-Case 语义）。

---

### Phase 3: 多语言生态闭环、函数抽取与跨文件锁守卫（P1）

#### 3.1 React TSX 箭头函数组件与 Hook 识别（共享判定谓词）
- **涉及文件**：`cmd/mcp-lsp/format/funcrange.go`、`cmd/mcp-lsp/tools/tool_file_render.go`
- **规格**：
  1. 抽象共享谓词 `IsFunctionOrComponentSymbol(symbol protocol.DocumentSymbol) bool`：
     - 支持 `SymbolKindFunction` (12), `SymbolKindMethod` (6)；
     - 支持跨多行的 `SymbolKindVariable` (13) / `SymbolKindConstant` (14)（校验 `PascalCase` 组件名、`^use[A-Z]` Hook 名或 `Detail` 包含 `=>`/`React.FC`）；
  2. `funcrange.go:findInSymbol` 优先深入 `symbol.Children` 子树，无匹配时调用谓词识别外层组件；
  3. `tool_file_render.go:findFunctionName` 同步调用谓词，确保响应头 `ATTR symbol=` 正确返回组件名；
  4. 限制递归深度最大 32，防堆栈溢出。

#### 3.2 补齐多语言短别名与 AST Grep 映射链条
- **涉及文件**：`cmd/mcp-lsp/search/fileutil.go`、`cmd/mcp-lsp/search/searchutil.go`、`cmd/mcp-lsp/manager/registry.go`
- **规格**：
  1. `registry.go` 补齐扩展名：`.mts -> typescript`, `.cts -> typescript`, `.sql -> sql`；
  2. `fileutil.go:inferLanguage` 补齐：`.mts`, `.cts`, `.sql`, `.sh`, `.bash`, `.zsh`, `.cs`；
  3. `fileutil.go:normalizeLanguageAlias` 补齐：`ts`, `tsx`, `js`, `jsx`, `py`, `rs`, `sh`, `bash`, `zsh`, `sql`, `cs`, `c#`；
  4. `searchutil.go:astGrepLanguageID` 增加 `case "shellscript": return "bash"`，修复 Shell AST Grep 崩溃；
  5. `searchutil.go:normalizeASTLanguage` 对 `ast_language="sql"` 明确返回 Fail-Fast 提示：`ast_search does not support SQL; use text_search instead`。

#### 3.3 无扩展名文件安全 Shebang 语言嗅探
- **涉及文件**：`cmd/mcp-lsp/search/fileutil.go`、`cmd/mcp-lsp/manager/registry.go`
- **规格**：
  1. 使用 `os.Lstat` 校验，仅对常规普通文件（`fileMode.IsRegular()`）生效，跳过 FIFO、Socket 与设备文件；
  2. 非阻塞读取首部最多 512 字节；
  3. **空字节熔断**：若内容包含 `0x00`（二进制文件），立即终止并返回空字符串，防 ELF 伪 Shebang 导致语言服务崩溃；
  4. 解析首行：支持 `python*`, `bash/sh/zsh`, `node/bun/deno` 以及 `env -S` 复合参数。

#### 3.4 跨文件重命名并发锁守卫、自排序去重与 Format 锁补齐
- **涉及文件**：`cmd/mcp-lsp/tools/tool_edit_rename.go`、`cmd/mcp-lsp/tools/tool_edit_lock.go`、`cmd/mcp-lsp/tools/tool_edit_lsp_actions.go`
- **规格**：
  1. **`lockEditFiles` 内部自排序与自去重**：内部对传入 paths 自动执行 `filepath.Clean`、`sort.Strings` 与 `slices.Compact`，杜绝调用端乱序/非连续重复导致自死锁；
  2. 在 `applyWorkspaceEdit` 开头调用 `unlock := lockEditFiles(h.lockRegistry, paths); defer unlock()`；
  3. **TOCTOU 防御**：获取全量文件锁后快速比对受影响文件的当前版本/指纹，防陈旧 AST 坐标脏写；
  4. 补齐 `tool_edit_lsp_actions.go:handleFormat` 的 `lockEditFile` 保护。

#### 3.5 补齐 `workspace/symbol` 自动重试与废除坏客户端/失步吞错回滚
- **涉及文件**：`cmd/mcp-lsp/multilsp/manager_retry.go`、`cmd/mcp-lsp/multilsp/manager_lifecycle.go`
- **规格**：
  1. `canAutoRetryDeadClientRequest` 增加 `case protocol.MethodWorkspaceSymbol: return true`；
  2. `rebuildClientAfterFailure` 中若 `closeErr != nil`，彻底销毁客户端，禁止调用 `restoreDetachedWorkspaceClient`；
  3. **废除 `recoverFullDocumentDidChange` 吞错**：彻底废除 `reopenErr == nil -> return nil` 隐式兜底，如实暴露原始错误并规范化重置版本号为 1。

---

### Phase 4: Actionable 诊断错误反馈与测试守卫（P2）

#### 4.1 精准字段迁移与坐标指引
- **涉及文件**：`cmd/mcp-lsp/tools/factory_bindings.go`、`cmd/mcp-lsp/tools/position.go`
- **规格**：
  - 遇到未知字段 `language` 时返回：  
    `"unknown field 'language'; HINT: use 'workspace_language' for workspace_symbol, or 'language_id' for file-level override"`；
  - 遇到 `pos` 缺少列号时，读取当前行并返回识别到的标识符列号建议（上限 8 条，`suggested_columns: [6, 15, 28]`）；
  - 遇到行号或列号为 0 时提示：  
    `"LSP coordinates are 1-based (line >= 1, column >= 1), but got 0; HINT: add 1 to convert from 0-based"`。

---

## 五、四轮红队对抗审查防御性收紧矩阵

| 攻击向量 / 威胁场景 | 触发位置 | 强制收紧规则 | Fail-Fast 行为 |
| :--- | :--- | :--- | :--- |
| **保留字变体穿透** | `arguments` 传入 `_Cwd`, `_AGENT_ID` | `isReservedFieldStem` 规范化全量匹配拦截；元数据单字段限长 4096B | 报错 `reserved_metadata_in_arguments` |
| **并发伪乐观锁** | 写入后再校验导致数据静默覆写 | 落盘前先断言版本号；废除 `version = state.lspVersion + 1` 静默自增 | 拒绝写盘，返回 `concurrency_version_conflict` |
| **`cancelSync` 状态机撕裂** | `git diff` 确认后 cancel LSP 导致版本回滚 | 禁止在 diff 确认后 cancel LSP 同步 context，确保版本单调推进 | 内存状态机与磁盘严格同步 |
| **`recoverFullDocumentDidChange` 吞错** | 版本失步后静默重开并返回 nil | 废除 `reopenErr == nil -> return nil`，原样暴露底层错误并重置版本 | 坚决 Fail-Fast 报错 |
| **BOM / 零宽字符穿透** | 路径传入 `\uFEFF`, `unicode.Cf` 绕过 | `CleanPathParameter` 彻底过滤不可见格式字符；断言有效路径 > 0 | 拒绝提升，返回 `empty_path_element` |
| **超长单行 DoS 注入** | 1MB+ 单行报错触发 10 万个标识符扫描 | `suggestedIdentifierColumns` 强制设置硬上限 8 条截断 | 快速返回并限制响应 JSON 体积 |
| **多 Hunk 逆序匹配** | 非锚点多 Hunk 导致重叠/逆序匹配 | `matchContextHunk` 强制推进 `nextMinimum` 保证拓扑单调递增 | 破坏单调性立即返回 `ErrSequenceNotFound` |
| **Diff 非对称截断** | 尾部空行单边削减导致行号位移 | 彻底删除 `patchmatch.go:270` 单边剥离；写回自动统一源文件 EOL | 保持拓扑 1:1 对称，未命中即阻断 |
| **跨文件重命名死锁/脏写** | AB-BA 交叉重命名或 TOCTOU 坐标脏写 | `lockEditFiles` 内部自排序去重；获取锁后校验受影响文件版本 | 互斥串行化，冲突即阻断 |
| **Format 并发竞态** | `format` 与 `rename` 并发修改同一文件 | 补齐 `handleFormat` 的 `lockEditFile` 文件锁保护 | 互斥执行，防止数据覆写 |
| **Shebang I/O 挂死** | FIFO 命名管道导致 Goroutine 挂死 | `Lstat` 普通文件校验 + 非阻塞打开 + `0x00` 二进制空字节极速熔断 | 安全退出并返回空语言 ID |
| **React 伪函数误识别** | 500 行配置大对象误判为函数 | `IsFunctionOrComponentSymbol` 校验名称/签名，递归深度上限 32 | 非函数安全退化为行窗口 |
| **崩溃后工作区符号失效** | 下游服务崩溃后 `workspace_symbol` 失败 | `canAutoRetryDeadClientRequest` 补充 `workspace/symbol` 重试白名单 | 透明重建客户端并自愈恢复 |

---

## 六、修改文件清单与前后代码设计

| 文件路径 | 变更类型 | 改造说明 |
| :--- | :---: | :--- |
| `cmd/mcp-lsp/fault_tolerance_e2e_test.go` | [NEW] | **[执行过程安全]** 真实二进制端到端红绿测试用例（覆盖 6 大场景真实 stdio 验证） |
| `internal/mcpserver/common/scope.go` | [MODIFY] | `ToolCallParams` 声明平台元数据字段并完成传输层剥离 |
| `cmd/mcp-lsp/tools/atomic_write.go` | [MODIFY] | `validateReservedToolWrapperFields` 剥离平台元数据并采用 `isReservedFieldStem` 严格拦截保留字变体 |
| `cmd/mcp-lsp/schema.go` | [MODIFY] | 补充 `patch_edit.version`、扩展 `file.scope`、放宽 `paths`/`file_paths` 为带 `minLength:1` 的 `oneOf`、放宽 `diagnostics` 无参调用 |
| `cmd/mcp-lsp/tools/tool_edit_replace.go` | [MODIFY] | 落盘前增加前置乐观锁版本校验，废除静默自增 |
| `cmd/mcp-lsp/tools/tool_edit_replace_update.go` | [MODIFY] | 移除 `git diff` 抢先确认后对 `cancelSync()` 的主动调用，保证 LSP 状态机正常提交 |
| `cmd/mcp-lsp/multilsp/manager_explicit_documents.go` | [MODIFY] | 废除 full change 下的版本强制覆盖，版本冲突坚决报错 |
| `cmd/mcp-lsp/multilsp/manager_lifecycle.go` | [MODIFY] | 废除 `recoverFullDocumentDidChange` 静默重开吞错兜底，版本失步如实报错 |
| `cmd/mcp-lsp/tools/tool_edit_lock.go` | [MODIFY] | `lockEditFiles` 内部增加 `filepath.Clean`、`sort.Strings` 与 `slices.Compact` 自排序去重 |
| `cmd/mcp-lsp/tools/tool_edit_rename.go` | [MODIFY] | `applyWorkspaceEdit` 接入 `lockEditFiles` 字典序锁与 TOCTOU 版本/指纹断言 |
| `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go` | [MODIFY] | `handleFormat` 补齐 `lockEditFile` 保护；`applyTextEdits` 增加重叠区间校验 |
| `cmd/mcp-lsp/edit/patchparse.go` | [MODIFY] | `parsePatchBodyLine` 支持纯空行容错，全空行 Hunk 强制阻断 |
| `cmd/mcp-lsp/edit/patchmatch.go` | [MODIFY] | 移除 line 270 单边削减；`matchContextHunk` 推进 `nextMinimum` 拓扑单调递增 |
| `cmd/mcp-lsp/tools/tool_edit_support.go` | [MODIFY] | `normalizeLineEndings` 统一处理 `\r\n` 与 `\r`，增加源文件 EOL 保持 |
| `cmd/mcp-lsp/tools/position.go` | [MODIFY] | `parsePositivePosSegment` 增加 `TrimSpace`；`utf16OffsetsForRunes` 防负数；`suggestedIdentifierColumns` 硬上限 8 条截断 |
| `cmd/mcp-lsp/tools/factory_bindings.go` | [MODIFY] | 增加 `StringOrSlice`、`CleanPathParameter`、严格字面量 Coercion 与 Actionable 迁移指引 |
| `cmd/mcp-lsp/tools/tool_file.go` | [MODIFY] | `readBatch` 增加非空路径有效性前置守卫 |
| `cmd/mcp-lsp/format/funcrange.go` | [MODIFY] | 增加共享谓词 `IsFunctionOrComponentSymbol`，支持 React TSX 箭头函数/Hook 识别与递归剪枝 |
| `cmd/mcp-lsp/tools/tool_file_render.go` | [MODIFY] | 导入 `format` 包，`findFunctionName` 同步调用共享谓词回显组件名；`renderReadRows` 下发 `version` |
| `cmd/mcp-lsp/search/fileutil.go` | [MODIFY] | 补全别名字典与扩展名推断，增加安全 Shebang 嗅探 |
| `cmd/mcp-lsp/search/searchutil.go` | [MODIFY] | `astGrepLanguageID` 补齐 `shellscript -> bash`；SQL AST 显式 Fail-Fast 报错 |
| `cmd/mcp-lsp/manager/registry.go` | [MODIFY] | 补齐 `.mts`, `.cts`, `.sql`, `.sh` 等扩展名映射 |
| `cmd/mcp-lsp/multilsp/manager_retry.go` | [MODIFY] | `canAutoRetryDeadClientRequest` 补充 `workspace/symbol` 白名单，废除坏客户端回滚 |
| `cmd/mcp-lsp/schema_test.go` | [MODIFY] | 同步更新 4 处测试断言（移除 version 禁止与两处豁免、更新 diagnostics 与 paths 断言） |

---

## 七、坚决禁止的隐式兜底红线（Fail-Fast 边界）

全量通过 8 大语言裁决与四轮红队审查锁定的 **6 大绝对禁止红线**：

1. **严禁缺失路径时默认回退到工作区根目录**：必须显式报错要求路径，防止全仓误扫或推导错误编译单元。
2. **严禁光标定位未命中时静默默认第 1 列**：必须阻断并返回 `suggested_columns`，防止误跳关键字或头文件包含。
3. **严禁 Patch 上下文不匹配时就近模糊应用**：必须严格校验上下文序列，防止 Python 缩进作用域错位或 sqlc 重复投影列误打补丁。
4. **严禁 AST 语法解析失败时静默降级为文本搜索**：必须显式报错 `ast_syntax_parse_error`，防止注释与伪代码假阳性污染。
5. **严禁语言服务未就绪时伪造空列表响应**：必须返回 `indexing` 或 `service_unavailable`，防止 Agent 误判“无引用”而误删代码。
6. **严禁废除 `additionalProperties: false`**：业务 DTO 必须维持类型安全与封闭校验，仅在传输层做元数据剥离。

---

## 八、全量验证计划与测试守卫同步矩阵

### 1. 自动化测试套件与执行顺序（严格按过程安全执行）

```bash
# 步骤 1: 运行真实二进制 E2E 红测 (验证捕获预见性失败，确保红测有效)
go test -v -run TestE2E_RealBinary_ ./cmd/mcp-lsp/ -tags=e2e

# 步骤 2: 运行各包单元红测 (验证单测 RED 状态)
go test -v ./cmd/mcp-lsp/tools/... ./cmd/mcp-lsp/edit/... ./cmd/mcp-lsp/format/... ./internal/mcpserver/common/...

# 步骤 3: 实施代码修绿并验证单测 100% GREEN
go test -v ./cmd/mcp-lsp/... ./internal/mcpserver/common/...

# 步骤 4: 重新编译并执行真实二进制 E2E 终验全绿测试 (必须 100% PASS)
go test -v -run TestE2E_RealBinary_ ./cmd/mcp-lsp/ -tags=e2e

# 步骤 5: 运行全仓守护测试 (含架构守卫与大小检查)
./scripts/test_with_guard.sh --canonical-backend ./cmd/mcp-lsp/... ./internal/mcpserver/...
```

### 2. 测试守卫同步修改矩阵（必须同步更新 `schema_test.go`）
1. **`TestEditSchemaExposesPatchDiskFieldsOnly`**（Line 24, Line 29）：将 `"version"` 移出禁止列表，加入 `expected` 属性白名单；
2. **`TestPatchEditSchemaCoversHandlerParameterFields` & `lspSchemaContracts`**（Line 106, Line 443-445）：同时从两处移除 `version` 的人工豁免项；
3. **`TestLSPToolSchemasRejectActionSpecificInvalidArguments`**（Line 273）：将 `file/diagnostics missing locator` 迁移至合法无参用例；
4. **`TestGrepSchemaUsesCanonicalPathsOnly`**（Line 221-224）：更新 `paths` 断言以匹配带 `minLength: 1` 的 `oneOf` 结构。

### 3. 核心验收用例矩阵
- **用例 1（真实二进制 E2E 传输解耦与保留字防御）**：真实二进制向各工具发送携带 `toolAction: "testing"` 成功执行；传入 `_Cwd` 或 `_AGENT_ID` 变体被严格拦截。
- **用例 2（真实二进制 E2E Patch 空行、CRLF 保持与多 Hunk 单调推进）**：真实二进制向 Go/Python/SQL 文件应用多 Hunk 包含 `""` 的补丁，验证比对成功且严格正向单调递增。
- **用例 3（真实二进制 E2E 切片提升与 Unicode 安全）**：真实二进制传入单路径 `"paths": "cmd/mcp-lsp"` 成功提升；传入 `\uFEFF` 或 `unicode.Cf` 格式控制字符被拦截。
- **用例 4（真实二进制 E2E TSX 组件提取与渲染回显）**：真实二进制在 React TSX 箭头函数组件与 Hook 内部调用 `read_file`，验证行范围与响应头 `symbol=` 均正确回显。
- **用例 5（真实二进制 E2E Shebang 探针安全与熔断）**：真实二进制对普通 Python 脚本成功嗅探；对 FIFO 管道与包含 `0x00` 的二进制文件安全熔断。
- **用例 6（真实二进制 E2E 并发乐观锁与状态机单调推进）**：真实二进制模拟版本冲突写盘，在落盘前被成功拦截并抛出 `concurrency_version_conflict`；快速连续编辑时状态机单调递增且不被 `cancelSync` 破坏；`read_file` 成功返回最新 `version` 支撑客户端原子重试。
- **用例 7（真实二进制 E2E 跨文件重命名加锁、自排序与 Format 互斥）**：真实二进制跨文件 `rename` 与 `format` 成功持有字典序多文件锁，自排序去重防自死锁，按确定顺序执行并支持原子回滚。
- **用例 8（真实二进制 E2E 崩溃自愈与失步 Fail-Fast）**：真实二进制模拟下游语言服务崩溃，`workspace/symbol` 自动触发重试；版本失步时原样暴露错误并重置版本为 1；超长单行报错被 8 条建议截断防护。
- **用例 9（真实二进制 E2E Fail-Fast 坚守）**：真实二进制传入未知参数 `"foo": 123` 或历史字段 `"language": "go"` 时立即阻断，并包含 Actionable 修复建议。
