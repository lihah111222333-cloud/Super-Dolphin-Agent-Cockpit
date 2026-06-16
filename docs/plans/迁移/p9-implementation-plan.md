# P9 LSP 工具族 — 功能对等实施计划

> 基于四方（Codex#1 / Codex#2 / Claude#1 / Claude#2）两轮辩论最终共识
> 生成时间：2026-03-24 | 前置：P8.5 lifecycle hooks 已完成

---

## 0. 策略概述

### 核心原则

1. **不复制 V2 代码** — 按 V3 契约从零实现，仅参考 V2 行为和测试用例
2. **功能和容错与 V2 对等（非 MVP）** — 包含所有截断/大文件保护/重试/健康检查逻辑
3. **一次性交付 9 个工具的全部功能** — 不分 MVP/Phase，10 Agent 并行一轮完成
4. **守卫合规** — ≤400 行/文件，≤80 行/函数，CC≤10

### 为什么不用 MVP 分步

四方辩论确认：V2 的容错逻辑（截断、seek_sequence 4-pass、patch 解析器、bootstrap 等）
不是"增强功能"，而是基础正确性保障。缺少任何一个都会导致生产环境 crash 或数据截断。
分步交付会导致中间态工具可用性极差，不如一次到位。

### V2 参考基线

- V2 功能对等相关精确基线: **11,913 行** (Codex#1 逐文件实测)
- V3 从零功能对等估算四方原始区间（未扣除共享文件重叠与已有 cmd 层）:
  - Codex#1: 10.6k-11.0k (中位 10.8k)
  - Codex#2: 10.0k-10.5k (修正后)
  - Claude#1: ~9,800 (修正后)
  - Claude#2: ~9,200 (修正后)
- **四方原始估算区间: 9,200-10,800；本计划按“最终唯一新增代码”记账: 8,236（cmd/mcp-lsp 根装配 210 + cmd/mcp-lsp 本地子包 8,026）**

---

## 1. 总量与约束

| 维度 | 最终值 | 说明 |
|---|---|---|
| 功能对等生产代码 | **~8,236 行** | §6 Agent 分工表精确求和 (210+1040+776+580+900+1030+1220+1060+1420) |
| 测试代码 | **~6,600 行** | 0.8× 生产比 |
| 总交付量 | **~14,836 行** | 生产 + 测试 |
| 生产文件数 | **~38-44 个** | |
| Agent 数 | **10** | 9 实现 + 1 验证 |
| 关键路径 | **5.5 小时** | S+A+B → C1+C2 → D+E+F+G → V |
| 预估会话数 | **4-5 个** | |

### 代码守卫

| 约束 | 限值 | 来源 |
|---|---|---|
| 单文件行数 | ≤ 400 行 | archtest CodeSizeGuard |
| 单函数行数 | ≤ 80 行 | archtest CodeSizeGuard |
| 圈复杂度 | CC ≤ 10 | archtest CodeSizeGuard |
| 单目录文件数 | ≤ 15 个 | archtest CodeSizeGuard (guardlib.go:23 MaxPackageFiles=15) |

---

## 2. 目录结构

### 总体布局

> 记账口径：本节目录/文件行数均指“最终唯一新增代码”。`main.go` / `fx.go` 的“已有 X 行”仅作基线说明，不计入 `8,236` 行汇总。

```
cmd/mcp-lsp/                              # 薄装配层 (新增 ~210 行)
├── main.go                                # 入口 (新增 ~16 行；已有 14 行基线不计入汇总)
├── fx.go                                  # DI 容器 (新增 ~104 行 LSP 绑定；已有 134 行基线不计入汇总)
├── runtime.go                             # 启动/关闭编排 (~50)
└── tools.go                               # 工具注册表 (~40)

cmd/mcp-lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}/
                                          # 本地子包核心实现 (~8,026 行 = 8,236 - 210 根装配，按新增代码记账)
├── protocol/                              # LSP 协议层 (~520, Agent A)
│   ├── types.go                           #   LSP 基础类型定义 (~130)
│   ├── methods.go                         #   LSP 方法常量 (~40)
│   ├── codec.go                           #   JSON-RPC 编解码 (~130)
│   ├── ext.go                             #   扩展类型 (Location 等) (~130)
│   └── notification.go                    #   通知处理 (~90)
│
├── gopls/                                 # gopls 管理 (~2,510 = C1:580 + C2:900 + E:1030)
│   ├── client.go                          #   LSP 客户端封装 (~360)
│   ├── transport.go                       #   stdio 传输层 (~220)
│   ├── manager.go                         #   Manager 主体 + workspace 路由 (~280)
│   ├── manager_lifecycle.go               #   生命周期管理 + ensureClient (~130)
│   ├── manager_symbols.go                 #   符号查询主路径 (~250)
│   ├── manager_symbols_fallback.go        #   markdown/json/yaml fallback (~250)
│   ├── bootstrap_doc.go                   #   文档 bootstrap + sibling (~280)
│   ├── state.go                           #   bootstrap 状态机 (~210)
│   ├── pool.go                            #   进程池 (~180)
│   ├── recycler.go                        #   RSS 监控 + 回收 (~180)
│   ├── cache.go                           #   cache_store (内存 + env-gated) (~180)
│   └── gomod.go                           #   go.mod root 发现 (~90)
│
├── tools/                                 # 工具 handler (~2,730 = D:690 + F:1060 + G:980)
│   ├── tool_file.go                       #   lsp_file: open/read/diagnostics (~270)
│   ├── tool_grep.go                       #   lsp_grep: text_search/ast_search (~220)
│   ├── tool_inspect.go                    #   lsp_inspect: hover/def/impl/typedef (~260)
│   ├── tool_xref.go                       #   lsp_xref: references/call_hierarchy (~280)
│   ├── tool_structure.go                  #   lsp_structure: doc_symbol/ws_symbol (~300)
│   ├── tool_completion.go                 #   lsp_completion (~220)
│   ├── tool_diagnostics.go                #   diagnostics 子 handler (~200)
│   ├── tool_edit.go                       #   lsp_edit: rename/code_action/format 主 handler (~280)
│   ├── tool_edit_replace.go               #   replace_range 多 hunk 应用与回滚 (~280)
│   ├── tool_coderun.go                    #   code_run handler (~200)
│   └── tool_coderuntest.go                #   code_run_test handler (~220)
│
├── format/                                # 输出格式化 (~520)
│   ├── display.go                         #   坐标转换 (0→1-based) (~200)
│   ├── compact.go                         #   compact 输出模式 (~120)
│   ├── funcrange.go                       #   func_start/func_end 计算 (~80)
│   └── render.go                          #   结果渲染工具函数 (~120)
│
├── edit/                                  # 编辑子系统 (~776)
│   ├── patchparse.go                      #   patch 解析器 (Parse+ParseMulti) (~230)
│   ├── patchmatch.go                      #   patch 多 hunk 匹配+歧义检测 (~190)
│   ├── seeksequence.go                    #   4-pass 上下文定位算法 (~146)
│   └── replaceutil.go                     #   replace_range 辅助 (~210)
│
├── search/                                # 搜索子系统 (~430)
│   ├── fileutil.go                        #   文件读取+安全门 (~210)
│   └── searchutil.go                      #   搜索匹配+排除+裁剪 (~220)
│
├── exec/                                  # 执行子系统 (~220)
│   └── sandbox.go                         #   命令执行+超时+工作目录 (~220)
│
└── middleware/                            # 中间件 (~320)
    ├── logging.go                         #   请求/响应日志 (~90)
    ├── recovery.go                        #   panic 恢复 (~60)
    ├── timeout.go                         #   超时控制 (分级) (~70)
    └── budget.go                          #   输出预算控制 (~100)
```

### 目录结构依据

| 决策 | 依据 |
|---|---|
| cmd/mcp-lsp/ 薄装配层 | v3-migration-plan.md cmd 目录约定 |
| cmd/mcp-lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}/ 核心实现 | archtest rule7 family 隔离 + modularity-convention.md |
| 混合方案 | 四方最终共识 (Codex#1 最终同意); mcp-orch 先例 |
| gopls/ 子目录独立 | V2 manager* 文件群 (~2,400 行) 天然内聚 |
| edit/ 子目录独立 | patch+seek+context ~776 行，高度内聚 |
| format/ 子目录独立 | display+compact+funcrange 被多个工具共用 |

---

## 3. 9 个工具契约定义

### 3.1 lsp_file

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_file.go` (526行), `tool_handlers_dispatch.go`

| Action | 参数 | 输出格式 |
|---|---|---|
| `open_file` | `file_path: string` | `{success, status:"opened", file_path, bytes}` |
| `read_file` | `file_path?: string, file_paths?: []string, offset?: int, limit?: int` | 单文件: 带行号纯文本; 批量: `{success, data:[{file_path,success,content?,error?}], meta:{count,truncated?}}` |
| `diagnostics` | `file_path?: string, file_paths?: []string` | `{success, data:[{file,cols,rows}], meta:{count,source}}` |

**关键行为**:
- `read_file` 不需要 gopls (`requireManager=false`, V2 tool_handlers_file.go:479)
- 批量上限 `lspReadFileBatchMax=10`，载荷上限 `16KB`
- 安全门: Lstat 存在性 → 拒绝 symlink → 拒绝非 regular file → 大小(2MB) → 二进制检测(512B NUL 采样)
- 渐进式裁剪 `encodeBatchReadPayload`: 先截内容后截文件数 (T7)

### 3.2 lsp_inspect

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_core.go` (895行)

| Action | 参数 | 输出格式 |
|---|---|---|
| `hover` | `file_path, line, column` | 原始字符串; 空: `"no hover info available"` |
| `definition` | `file_path, line, column` | `[]LocationResult{file, line, col, end_line?, end_col?, func_start?, func_end?}` |
| `implementation` | `file_path, line, column` | 同 definition |
| `type_definition` | `file_path, line, column` | 同 definition |
| `signature_help` | `file_path, line, column` | `{signatures[], activeSignature?, activeParameter?}` |

**关键行为**:
- definition/implementation/type_definition 返回附带 `func_start/func_end`（共识#3）
- 结果经 display 坐标转换 0-based → 1-based（共识#4）

### 3.3 lsp_xref

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_p1_outputs.go` (258行), `tool_handlers_core.go`

| Action | 参数 | 输出格式 |
|---|---|---|
| `references` | `file_path, line, column, verbosity?, max_results?, include_declaration?` | compact: `{files: map[string][]CompactLoc, total, showing}`; full: `[]Location` |
| `call_hierarchy` | `file_path, line, column, direction?(incoming/outgoing/both)` | `[]CallHierarchyResult{item, incoming?, outgoing?}` |
| `type_hierarchy` | `file_path, line, column, direction?(supertypes/subtypes)` | `[]TypeHierarchyResult{item, supertypes?, subtypes?}` |

**关键行为**:
- compact references 上限 `lspReferencesCompactLimit=30`, `XRefResultLimit=50`
- references compact 附带 `func_start/func_end`

### 3.4 lsp_grep

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_search.go` (669行)

| Action | 参数 | 输出格式 |
|---|---|---|
| `text_search` | `query, path?, glob?, case_sensitive?, max_results?, regex?` | compact: `{files: map, total, showing}` |
| `ast_search` | `query, language, path?, glob?, max_results?` | 同 text_search |

**关键行为**:
- AST 搜索后端优先 `sg`(ast-grep)，sg 不可用时返回错误 `"sg not found in PATH"` (V2 tool_handlers_search.go:171，无 gopls fallback)
- `filterAndCapSearchMatches` 排除 + 裁剪 (T8)
- markdown/json/yaml symbol fallback（共识#7）

### 3.5 lsp_structure

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_core.go`, `tool_handlers_p1_outputs.go`

| Action | 参数 | 输出格式 |
|---|---|---|
| `document_symbol` | `file_path, verbosity?` | 符号树 |
| `workspace_symbol` | `query (required), file_path?, language?, verbosity?, max_results?` | compact 默认值20（显式 max_results 可覆盖） |
| `folding_range` | `file_path` | 折叠范围列表 |
| `semantic_tokens` | `file_path, max_results?` | 上限200 |

**workspace_symbol 参数补充** (V2 tool_handlers_p1_outputs.go:208-237):
- `query` 必填，空字符串返回错误 "query is required"
- `file_path` 和 `language` 二选一，两者同时提供或同时缺失均报错 "exactly one of file_path or language is required"
- `file_path` 不接受目录路径，仅接受源文件路径

**关键行为**:
- document_symbol 含 markdown/json/yaml 非 LSP fallback（共识#7）
- 结果附带 `func_start/func_end`（共识#3）

### 3.6 lsp_edit

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_edit.go` (1,012行), `replace_range_context.go` (292行), `seek_sequence.go` (145行)

| Action | 参数 | 输出格式 |
|---|---|---|
| `replace_range` | `file_path, patch?, new_text?, edits?: [{old_string, new_string}]` | 成功: `{matched_by, resolved_*, edit_context, func_start?, func_end?}` |
| `rename` | `file_path, line, column, new_name, force?` | `{success, applied, applied_count, message}` |
| `code_action` | `file_path, line, column, only?` | `[]CodeActionResult` |
| `format` | `file_path` | `[]TextEdit{range, newText}` |

**关键行为**:
- replace_range 是最重模块：patch 解析 → seek_sequence 4-pass → 上下文匹配
- 安全限制：替换体 256KB，文件内容 4MB，强制绕过 2MB
- `force` 不暴露为外部参数；V2 的 `force` 语义是内部 didChange 自动设置（内容 ≤2MB 时 `force=true`）
- 必须返回 `func_start/func_end/func_body`（enclosing function context）
- ~~dry_run~~ 已删除：V3 工具契约不暴露 dry_run 给 AI，replace_range 始终执行实际替换

### 3.7 lsp_completion

**V2 参考**: `pkg/toolsdk/lsp/tool_handlers_p1_outputs.go`

| 参数 | 输出格式 |
|---|---|
| `file_path, line, column, verbosity?, max_results?` | compact: `{data:[{label,kind?,detail?}], total, showing}`; full: `[]CompletionItem` |

**关键行为**: compact 默认值 `lspCompletionCompactLimit=20`（显式 `max_results` 可覆盖，最终 clamp 到 `XRefResultLimit=50`）

### 3.8 code_run

**V2 参考**: `pkg/toolsdk/tools/code_run.go`

| 参数 | 输出格式 |
|---|---|
| `mode(run/project_cmd), language?, code?, command?, auto_wrap?, work_dir?, timeout?` | `{success, output, exit_code, duration, language, mode, truncated}` |

**关键行为**:
- 审批不在 LSP 层（共识#12），V2 实际使用 `approvals.AwaitApproval` (code_run.go:95)，
  V3 需根据新架构重新设计审批接口
- V3 接法：tool/MCP handler 层构造 `ApprovalRequest{ToolName:"code_run", Kind:"tool", CallbackMethod:"item/commandExecution/requestApproval", Payload:{mode, command, is_dangerous, work_dir}}`，调用 `ApprovalManager.RequestApproval`
- 注意：无 bridge+server 时 V3 自动 decline；`policy=never` 只对 `request_user_input` 生效，不自动放行普通 tool approval
- run 模式支持 Go/JavaScript/TypeScript, auto_wrap 默认 true

### 3.9 code_run_test

**V2 参考**: `pkg/toolsdk/tools/code_run.go`

| 参数 | 输出格式 |
|---|---|
| `test_func, test_pkg?, timeout?` | 同 code_run |

**关键行为**: 底层复用 code_run 执行引擎，自动构造 `go test -run` 命令

硬约束：V3 必须保留结构化 test mode（独立 handler + `CodeRunRequest{Mode:"test", TestFunc, TestPkg}`），不得把 `code_run_test` 降级为字符串拼 shell 再转发到 `project_cmd`。

---

## 4. 12 项不可更改的技术共识

> 四方两轮辩论全部 4/4 确认，任何实现 Agent 不得违反。

### 共识 #1: 懒启动 (ensureClient 双重检查锁)

- **行为**: gopls 客户端按需创建，首次调用 gopls 相关工具时才启动进程
- **V2 证据**: `manager.go:452-506` — `ensureClient()` 双重检查锁；`manager.go:71` — `closed bool` 永久关闭标志
- **V3 实现**: `gopls/manager.go` 的 `ensureClient(cfg)` 方法
- **禁止**: 预启动 (ReadinessGate)、进程预热

### 共识 #2: 截断必须工具级 (不可统一中间件)

- **行为**: 每个工具有独立的截断逻辑，参数不同、阈值不同
- **V2 证据**: 截断分散在 5+ 个文件 — `tool_handlers_file.go` (`formatFileContent`), `tool_handlers_search.go` (`filterAndCapSearchMatches`), `tool_handlers_compact.go` (limit 常量), `protocol_ext_common.go` (`XRefResultLimit`), `tool_handlers_display.go` (坐标转换)
- **V3 实现**: 各 `tools/tool_*.go` 自行调用裁剪，`middleware/budget.go` 仅做最终输出兜底
- **禁止**: 统一 truncate 中间件

### 共识 #3: func_start/func_end 必须包含

- **行为**: definition/implementation/references(compact)/grep 结果附带 func_start/func_end
- **V2 证据**: `tool_handlers_func_range.go` (304行) 计算 enclosing function 范围
- **V3 实现**: `format/funcrange.go` (~80行)
- **禁止**: 省略 func_start/func_end

### 共识 #4: display 坐标转换 (~200 行) 必须包含

- **行为**: LSP 0-based 坐标 → 1-based 用户坐标 + 输出美化；路径规范化不在 `display.go`，在 search 层
- **V2 证据**: `tool_handlers_display.go` (522行) — 全部计划初始都遗漏，Claude#2 首先发现
- **V3 实现**: `format/display.go` (~200行)
- **禁止**: 直接输出 0-based 坐标

### 共识 #5: patch 三件套 ~776 行

- **行为**: unified diff 格式 patch 解析 (Parse + ParseMulti) → 多 hunk 匹配 → 上下文定位
- **V2 证据**: `patch/parser.go` (Parse 119行 + ParseMulti 121行), `replace_range_context.go` (292行 含两级 fallback + 歧义检测), `seek_sequence.go` (145行)
- **V3 实现**: `edit/patchparse.go` + `edit/patchmatch.go` + `edit/seeksequence.go` + `edit/replaceutil.go`
- **禁止**: 简化 patch 解析器

### 共识 #6: seek_sequence 4-pass 全量

- **行为**: exact → trimRight → trimBoth → unicodeNormalized，逐级放宽空白匹配
- **V2 证据**: `seek_sequence.go` 完整 146 行，4 个 pass 函数
- **V3 实现**: `edit/seeksequence.go` (~146行)
- **禁止**: 只实现 exact match

### 共识 #7: markdown/json/yaml symbol fallback 必须

- **行为**: 对非 Go 文件提供基于正则的 symbol 提取 (heading/key/indent symbol)
- **V2 证据**: `manager_markdown_symbols.go` (483行)
- **V3 实现**: `gopls/manager_symbols.go` (~250行) + `gopls/manager_symbols_fallback.go` (~250行)
- **禁止**: 只支持 gopls 能处理的语言

### 共识 #8: cache_store 必须 (内存默认 + env-gated persistent)

- **行为**: workspace/language/uri 三元 key + TTL cleanup + startup write probe
- **V2 证据**: `cache_store.go` (287行), `cache_model.go` (58行)
- **能力面**: disk persistence + TTL cleanup + persistent→memory fallback + 恢复 baseline metadata
- **V3 实现**: `gopls/cache.go` (~180行)
- **禁止**: 跳过 cache_store

### 共识 #9: 池回收 + RSS 跨平台监控必须

- **行为**: 周期性检查 gopls 进程 RSS，超阈值回收
- **V2 证据**: `manager_pool.go` (185行), `manager_pool_recycler.go` (182行)
- **V3 实现**: `gopls/pool.go` (~180行) + `gopls/recycler.go` (~180行)
- **禁止**: 不做 RSS 监控

### 共识 #10: diagnostics waitStable (80ms/40ms/800ms) + generation tracking 必须

- **行为**: 等待 gopls diagnostics 稳定后才返回 — 80ms 初始延迟 → 40ms 轮询间隔 → 800ms 最大等待
- **generation 跟踪**: V2 使用 `atomic.Uint64` 计数器 (`diagGeneration`) 实现诊断代次跟踪。
  generation 仅在 runtime reset 时推进，didChange 路径不推进 generation；发布 diagnostics 时按当前 runtime generation 过滤，丢弃过期 runtime 的结果。
  关键函数: `currentDiagnosticGeneration()`, `advanceDiagnosticGeneration()`,
  `publishDiagnosticsForGeneration(generation)`, `setDiagnosticsForGeneration(uri, diags, generation)`
  (V2 manager_diagnostics.go:11-49)
- **V2 证据**: `manager_diagnostics.go` (221行) `waitDiagnosticsStableImpl` (36行核心) + generation 机制
- **V3 实现**: `tools/tool_diagnostics.go` (~200行) 必须包含 generation tracking
- **禁止**: 立即返回 diagnostics (会返回不完整结果)；不做 generation 过滤 (会导致过期诊断覆盖新结果)

### 共识 #11: bootstrap + sibling bootstrap (cap=20) 必须

- **行为**: 打开文件时自动 bootstrap 同目录相关文件，上限 20
- **V2 证据**: `manager_bootstrap_document.go` (477行), `maxSiblingBootstrap=20`, `maxRefreshFiles=50`, `maxRefreshConcurrency=8`
- **V3 实现**: `gopls/bootstrap_doc.go` (~280行) + `gopls/state.go` (~210行)
- **禁止**: 只 bootstrap 当前文件

### 共识 #12: code_run 审批不在 LSP 层

- **行为**: 危险命令审批不在 LSP 工具层实现
- **V2 证据**: V2 审批在 `pkg/toolsdk/tools/code_run.go:95` 通过 `approvals.AwaitApproval(agentID, callID, mode, command, true)` 实现，
  位于 tools 层而非 LSP 层。V2 LSP 目录搜索 "approval" 零匹配。
- **V3 实现**: `tools/tool_coderun.go` 审批机制需重新设计——V2 使用 `ApprovalProvider` 接口 +
  `AwaitApproval` 方法，V3 需根据新架构确定审批接口（非 `bootstrap.RequestApproval`）
- **禁止**: 在 LSP 工具层实现审批逻辑

---

## 5. 容错层完整清单

### 5.1 截断/预算控制 (T1-T9)

| ID | V3名称 | V2原名 | 行为 | V2 参考文件 |
|---|---|---|---|---|
| T1 | formatFileContent | formatFileContent | 按 limit 截断行数，附加 `[showing lines X-Y of Z total]` | tool_handlers_file.go:432 |
| T2 | batchReadPayloadCap | encodeBatchReadPayload | 批量读取总载荷 ≤16KB，超出截断 | tool_handlers_file.go |
| T3 | searchMatchesCap | filterAndCapSearchMatches | 搜索结果按 max_results 裁剪 | tool_handlers_search.go |
| T4 | symbolResultsCap | limitResults | workspace_symbol compact 默认 20（显式 max_results 可覆盖） | tool_handlers_compact.go |
| T5 | completionCap | limitResults | 补全 compact 默认 20（显式 max_results 可覆盖） | tool_handlers_compact.go |
| T6 | referencesCap | limitResults | 引用上限 30(compact) / XRef 50 | tool_handlers_compact.go + protocol_ext_common.go |
| T7 | encodeBatchReadPayload | encodeBatchReadPayload | 渐进式裁剪: 先截内容长度，再截文件数 (19行核心) | tool_handlers_file.go |
| T8 | filterAndCapSearchMatches | filterAndCapSearchMatches | 排除模式匹配 + 裁剪到上限 (29行核心) | tool_handlers_search.go |
| T9 | semanticTokensCap | SemanticTokenResultLimit | 语义 token 上限 200 | protocol_ext_common.go |

### 5.2 大文件保护 (F1-F3)

| ID | V3名称 | V2原名 | 行为 | V2 参考文件 |
|---|---|---|---|---|
| F1 | readToolFileContent | readToolFileContent | 安全门: Lstat 存在性→拒绝 symlink→拒绝非 regular file→大小(2MB)→二进制(512B NUL 采样) (45行核心) | tool_handlers_file.go |
| F2 | guardReplaceRangeContentSize | guardReplaceRangeContentSize | 替换体 256KB + 文件内容 4MB + 强制绕过 2MB | tool_handlers_edit.go:431 |
| F3 | guardLargeDidChange | guardLargeDidChange | 大文件(>200行) didChange 性能告警 | tool_handlers_edit_flow.go:41 |

### 5.3 错误恢复/重试 (R2-R3)

> 注：R1 已删除；V2 不存在 `ensureClientRetry`，客户端创建失败时直接返回错误，因此不纳入实现或验收。

| ID | V3名称 | V2原名 | 行为 | V2 参考文件 |
|---|---|---|---|---|
| R2 | callWithContextTimeout | callWithContextTimeout | LSP 请求超时 + context cancel 传播 | client.go:418 |
| R3 | fallbackToMemory | fallbackToMemory | persistent cache 写入失败 → 自动降级到纯内存模式 | cache_store.go:270 |

### 5.4 健康检查 (H1-H3)

| ID | V3名称 | V2原名 | 行为 | V2 参考文件 |
|---|---|---|---|---|
| H1 | poolRecycler | poolRecycler (manager_pool_recycler.go) | 周期性 RSS 监控，超阈值进程回收 + 重建 | manager_pool_recycler.go |
| H2 | waitDiagnosticsStableImpl | waitDiagnosticsStableImpl | 80ms init → 40ms poll → 800ms max 等待稳定 | tool_handlers_diagnostics.go:158 |
| H3 | ensurePersistentReady | ensurePersistentReady | cache 启动时执行写入探测，失败降级内存 | cache_store.go:241 |

### 5.5 26 个 V2 安全常量 + 5 个 V3 新增设计常量

#### 文件读取 (6 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 1 | `defaultReadFileLimit` | 300 行 | tool_handlers_file.go:88 |
| 2 | `maxReadFileLimit` | 2,000 行 | tool_handlers_file.go:92 |
| 3 | `maxReadFileBytes` | 2 MB (2<<20) | tool_handlers_file.go:93 |
| 4 | `maxReadFileBinarySample` | 512 B | tool_handlers_file.go:94 |
| 5 | `lspReadFileBatchMax` | 10 个文件 | tool_handlers_file.go:89 |
| 6 | `lspReadFileBatchPayloadMax` | 16 KB (16*1024) | tool_handlers_file.go:90 |

#### 编辑保护 (5 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 7 | `replaceRangeMaxReplacementBytes` | 256 KB | tool_handlers_edit.go:250 |
| 8 | `replaceRangeMaxContentBytes` | 4 MB | tool_handlers_edit.go:251 |
| 9 | `replaceRangeForceBypassMaxBytes` | 2 MB | tool_handlers_edit.go:252 |
| 10 | `replaceRangeFuncBodyMax` | 8 KB | tool_handlers_edit_enclosing.go:160 |
| 11 | `didChangeLargeFileLineThreshold` | 200 行 | tool_handlers_edit.go:248 |

#### 结果裁剪 (6 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 12 | `lspCompletionCompactLimit` | 20 | tool_handlers_compact.go:14 |
| 13 | `lspReferencesCompactLimit` | 30 | tool_handlers_compact.go:15 |
| 14 | `lspWorkspaceSymbolCompactLimit` | 20 | tool_handlers_compact.go:16 |
| 15 | `XRefResultLimit` | 50 | protocol_ext_common.go:399 |
| 16 | `SemanticTokenResultLimit` | 200 | protocol_ext_common.go:542 |
| 17 | `maxReactiveBootstrap` | 30 | tool_handlers_diagnostics.go:117 |

#### 池管理 (3 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 18 | `defaultPoolSize` | 10 | manager_pool.go:23 |
| 19 | `maxPoolSize` | 20 | manager_pool.go:24 |
| 20 | `maxSiblingBootstrap` | 20 | manager_bootstrap_document.go:66 |

#### Bootstrap 并发 (2 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 21 | `maxRefreshFiles` | 50 | manager_bootstrap_document.go:62 |
| 22 | `maxRefreshConcurrency` | 8 | manager_bootstrap_document.go:64 |

#### Diagnostics 时序 (3 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 23 | `diagInitDelay` (V2: `initialWait`) | 80 ms | tool_handlers_diagnostics.go:160 |
| 24 | `diagPollInterval` (V2: `pollInterval`) | 40 ms | tool_handlers_diagnostics.go:161 |
| 25 | `diagMaxWait` (V2: `maxWait`) | 800 ms | tool_handlers_diagnostics.go:162 |

#### 缓存 (1 个)

| # | 常量名 | 值 | V2 来源 |
|---|---|---|---|
| 26 | `defaultLSPCacheTTL` | 7 * 24 * time.Hour (168h) | cache_model.go:14 |

#### V3 新增设计常量 (非 V2 来源，需在实现时确定)

> 以下常量 V2 中不存在，是 V3 新增设计，实现时需根据实际需求确定具体值。

| 常量名 | 建议值 | 说明 |
|---|---|---|
| `TierFast` | 5 s | completion, hover, signature_help |
| `TierNormal` | 30 s | definition, references, structure, rename |
| `TierSlow` | 120 s | workspace_symbol, ast_search, diagnostics |
| `TierExec` | 300 s | code_run, code_run_test |
| `goplsStartTimeout` | 30 s | ensureClient 启动超时 |

---

## 6. 10 Agent 分工详案

> 记账口径：以下行数预算按“最终唯一新增代码归属”统计；共享依赖和调用关系不重复计入多个 Agent。

### 6.1 总览表

| Agent | 范围 | 行数预算 | 文件数 | 依赖 |
|---|---|---|---|---|
| **S** 骨架 | cmd/mcp-lsp/ 扩展fx+新建tools+runtime | ~210 | 2新建+2扩展 | 无 |
| **A** 协议+输出 | protocol/ + format/ | ~1,040 | 9 | 无 |
| **B** Patch引擎 | edit/ (patchparse+patchmatch+seeksequence+replaceutil) | ~776 | 4 | 无 |
| **C1** 客户端 | gopls/client + gopls/transport | ~580 | 2 | A |
| **C2** 管理器核心 | gopls/manager + manager_lifecycle + manager_symbols + manager_symbols_fallback + gomod | ~900 | 5 | A (C1用interface) |
| **E** Bootstrap+健康 | gopls/bootstrap_doc + gopls/state + gopls/pool + gopls/recycler + gopls/cache | ~1,030 | 5 | C2 |
| **D** 文件+搜索+诊断 | tools/tool_file + tools/tool_grep + tools/tool_diagnostics + search/ + middleware/budget | ~1,220 | 6 | C2 |
| **F** 导航+结构+补全 | tools/tool_inspect + tools/tool_xref + tools/tool_structure + tools/tool_completion | ~1,060 | 4 | C2+A |
| **G** 编辑+执行+胶水 | tools/tool_edit + tool_edit_replace + tools/tool_coderun + tools/tool_coderuntest + exec/sandbox + middleware/ | ~1,420 | 8 | C2+B |
| **V** 验证 | build+vet+archtest+schema+冒烟+修复 | ~测试 | - | 全部 |

**均衡度**: σ=227 (CV=23%), 远优于单人分工的 σ=695

### 6.2 并行 DAG 图

```
时间线    Agent                     产出
─────────────────────────────────────────────────────
t=0.0h   ┌─ S 骨架 (~0.5h) ───────── cmd/mcp-lsp/ 4文件
         ├─ A 协议+输出 (~1.0h) ──── protocol/ 5文件 + format/ 4文件
         └─ B Patch引擎 (~1.0h) ──── edit/ 4文件
              │
t=1.0h       ├─ C1 客户端 (~0.75h) ── gopls/client + transport
              └─ C2 管理器 (~0.75h) ── gopls/manager + manager_lifecycle + manager_symbols + manager_symbols_fallback + gomod
                   │
t=1.75h          ├─ D 文件+搜索 (~1.5h) ── tools/tool_file + tool_grep + tool_diagnostics + search/
                  ├─ E Bootstrap (~1.25h) ─ gopls/bootstrap_doc + state + pool + recycler + cache
                  ├─ F 导航+结构 (~1.25h) ─ tools/tool_inspect + tool_xref + tool_structure + tool_completion
                  └─ G 编辑+执行 (~1.75h) ─ tools/tool_edit + tool_edit_replace + tool_coderun + tool_coderuntest + exec/ + middleware/
                       │
t=3.5h               V 验证 (~2.0h) ──── build + vet + archtest + schema + 冒烟测试 + 修复
                       │
t=5.5h           ✅ 完成
```

### 6.3 关键路径分析

```
关键路径: A(1.0h) → C2(0.75h) → G(1.75h) → V(2.0h) = 5.5h
次关键:   A(1.0h) → C2(0.75h) → D(1.5h)  → V(2.0h) = 5.25h
最短路:   S(0.5h) → V(2.0h) = 2.5h (但V等所有Agent)
```

**瓶颈**: Agent G（编辑+执行+胶水）行数最多 (~1,420行)，是关键路径上最长的实现 Agent。

### 6.4 各 Agent 详细文件清单

#### Agent S — 骨架 (新增 ~210行, 2新建 + 2扩展)

| 文件 | 行数 | 职责 |
|---|---|---|
| cmd/mcp-lsp/main.go | 已有14行 + 新增~16 | 入口，添加 LSP 相关调用 |
| cmd/mcp-lsp/fx.go | 已有134行 + 新增~104 | 扩展已有 DI 容器，添加 Manager/ToolHandlers/Server |
| cmd/mcp-lsp/runtime.go | ~50 | 新建，启动关闭编排 (graceful shutdown) |
| internal/sidecar/lsp/tools.go | ~40 | 新建，9 工具注册表 (tool name → handler 映射) |

**注意**: fx.go 已包含 run(), newBootstrapRunner(), bindRuntime() 三个函数。
必须在已有代码基础上扩展，而非从零创建。

**输出**: 可编译的 `cmd/mcp-lsp` 二进制，包含 LSP 工具族支持

#### Agent A — 协议+输出 (~1,040行, 9文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| protocol/types.go | ~130 | Position, Range, Location, TextEdit, Diagnostic 等 LSP 基础类型 |
| protocol/methods.go | ~40 | textDocument/definition 等方法常量 |
| protocol/codec.go | ~130 | JSON-RPC 2.0 编解码 (Request/Response/Notification) |
| protocol/ext.go | ~130 | LocationResult, CompactLocation, GroupedResult 等扩展类型 |
| protocol/notification.go | ~90 | publishDiagnostics/logMessage 通知分发 |
| format/display.go | ~200 | 0-based→1-based 坐标转换 + URI→路径 |
| format/compact.go | ~120 | compact 输出模式序列化 |
| format/funcrange.go | ~80 | func_start/func_end 计算 |
| format/render.go | ~120 | JSON/表格/纯文本 渲染工具 |

**V2 参考**: `protocol.go`, `protocol_ext_common.go`, `tool_handlers_display.go`, `tool_handlers_compact.go`, `tool_handlers_func_range.go`

#### Agent B — Patch引擎 (~776行, 4文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| edit/patchparse.go | ~230 | Parse(单hunk) + ParseMulti(多hunk) unified diff 解析 |
| edit/patchmatch.go | ~190 | 多 hunk 匹配 + 歧义检测 + 两级 fallback |
| edit/seeksequence.go | ~146 | 4-pass 上下文定位: exact→trimRight→trimBoth→unicodeNorm |
| edit/replaceutil.go | ~210 | offset→行号转换 + 替换预览 + edit context 构建 |

**V2 参考**: `patch/parser.go`, `replace_range_context.go`, `seek_sequence.go`, `replace_range_runtime.go`

#### Agent C1 — 客户端 (~580行, 2文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| gopls/client.go | ~360 | LSP Client: initialize/shutdown + request/notify + capability negotiation |
| gopls/transport.go | ~220 | stdio JSON-RPC 传输: reader goroutine + pending map + close |

**V2 参考**: `client.go` (491行), `client_transport.go` (126行), `client_tools.go` (225行)
**依赖**: A (protocol/ 类型)

#### Agent C2 — 管理器核心 (~900行, 5文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| gopls/manager.go | ~280 | Manager 主体 + workspace 路由 |
| gopls/manager_lifecycle.go | ~130 | 生命周期管理 + ensureClient 双重检查锁 |
| gopls/manager_symbols.go | ~250 | 符号查询主路径 |
| gopls/manager_symbols_fallback.go | ~250 | markdown/json/yaml fallback |
| gopls/gomod.go | ~90 | go.mod root 发现 (用于 workspace 分区) |

**V2 参考**: `manager.go` (591行), `manager_markdown_symbols.go` (483行), `gomod_root.go` (83行)
**依赖**: A (protocol/ 类型); C1 通过 interface 解耦，C1/C2 可并行开发

#### Agent E — Bootstrap+健康 (~1,030行, 5文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| gopls/bootstrap_doc.go | ~280 | BootstrapDocument + sibling bootstrap (cap=20) + refresh |
| gopls/state.go | ~210 | bootstrap 状态机 (pending/ready/stale/error) |
| gopls/pool.go | ~180 | 进程池: acquire/release + size 管理 |
| gopls/recycler.go | ~180 | RSS 跨平台监控 + 超阈值回收 + 重建 |
| gopls/cache.go | ~180 | cache_store: 内存默认 + env-gated persistent + TTL + write probe |

**V2 参考**: `manager_bootstrap_document.go` (477行), `manager_bootstrap_document_state.go` (298行), `manager_pool.go` (185行), `manager_pool_recycler.go` (182行), `cache_store.go` (287行), `cache_model.go` (58行)
**依赖**: C2

#### Agent D — 文件+搜索+诊断 (~1,220行, 6文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| tools/tool_file.go | ~270 | lsp_file handler: open/read(单+批量)/diagnostics 分发 |
| tools/tool_grep.go | ~220 | lsp_grep handler: text_search + ast_search（sg 不可用时返回错误） |
| tools/tool_diagnostics.go | ~200 | diagnostics 子handler: waitStable + reactive bootstrap |
| search/fileutil.go | ~210 | 安全门 + 文件读取 + 二进制检测 |
| search/searchutil.go | ~220 | 搜索匹配 + filterAndCapSearchMatches + 排除模式 |
| middleware/budget.go | ~100 | 输出预算兜底 |

**V2 参考**: `tool_handlers_file.go` (526行), `tool_handlers_search.go` (669行), `tool_handlers_diagnostics.go` (365行), `manager_diagnostics.go` (221行)
**依赖**: C2

#### Agent F — 导航+结构+补全 (~1,060行, 4文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| tools/tool_inspect.go | ~260 | lsp_inspect: hover/definition/implementation/type_definition/signature_help |
| tools/tool_xref.go | ~280 | lsp_xref: references(compact/full)/call_hierarchy/type_hierarchy |
| tools/tool_structure.go | ~300 | lsp_structure: document_symbol/workspace_symbol/folding_range/semantic_tokens |
| tools/tool_completion.go | ~220 | lsp_completion: compact/full 模式 |

**V2 参考**: `tool_handlers_core.go` (895行), `tool_handlers_p1_outputs.go` (258行), `tool_handlers_navigation.go` (155行)
**依赖**: C2 + A

#### Agent G — 编辑+执行+胶水 (~1,420行, 8文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| tools/tool_edit.go | ~280 | lsp_edit: rename/code_action/format 主 handler |
| tools/tool_edit_replace.go | ~280 | replace_range: 多 hunk 应用 + 回滚 |
| tools/tool_coderun.go | ~200 | code_run: run/project_cmd 模式 |
| tools/tool_coderuntest.go | ~220 | code_run_test: go test 封装 |
| exec/sandbox.go | ~220 | 命令执行: timeout + work_dir + output capture |
| middleware/logging.go | ~90 | 请求/响应结构化日志 |
| middleware/recovery.go | ~60 | panic 恢复 + 错误格式化 |
| middleware/timeout.go | ~70 | 超时控制 (TierFast/Normal/Slow/Exec) |

**V2 参考**: `tool_handlers_edit.go` (1,012行), `tool_handlers_edit_enclosing.go` (234行), `tool_handlers_edit_flow.go` (109行), `tool_handlers_dispatch.go` (256行), `tool_handlers_ide.go` (121行)
**依赖**: C2 + B

#### Agent V — 验证 (~测试代码)

| 验证项 | 内容 |
|---|---|
| 编译验证 | `go build ./cmd/mcp-lsp/...` + `go build ./...` |
| go vet | `go vet ./cmd/mcp-lsp/...` |
| archtest | 运行现有 + 新增 archtest 规则 |
| schema 快照 | 9 工具 JSON Schema 与 V2 对比 |
| 冒烟测试 | 每工具至少 1 个 happy path |
| 容错测试 | T1-T9, F1-F3, R2-R3, H1-H3 各至少 1 case |
| 守卫检查 | ≤400行/文件, ≤80行/函数, CC≤10 |
| 修复 | 编译/vet/archtest 违规修复 |

**依赖**: 全部 Agent 完成

---

## 7. 每个 Agent 的执行 Prompt 模板

> 以下 prompt 可直接复制给对应 Agent 使用。每个 prompt 包含：范围、V2 参考路径、输出清单、行数约束、12项共识引用。

### 7.1 Agent S — 骨架

```
你是 P9-S Agent，负责扩展 cmd/mcp-lsp/ 薄装配层骨架。

## 任务
扩展 cmd/mcp-lsp/ 已有文件并新增文件，总计新增 ~210 行。

⚠️ **注意**: cmd/mcp-lsp/ 已有 main.go (14行) 和 fx.go (134行)。
fx.go 已包含函数: run(), newBootstrapRunner(), bindRuntime()。
必须在已有代码基础上扩展，而非从零创建。

## 输出文件
1. cmd/mcp-lsp/main.go (已有14行，新增 ~16行) — 添加 LSP 相关入口调用
2. cmd/mcp-lsp/fx.go (已有134行，新增 ~104行) — 在已有 fx.New DI 容器中
   添加 Manager/ToolHandlers/Server 的 Provide 和 Invoke
   已有函数: run(), newBootstrapRunner(), bindRuntime()
3. cmd/mcp-lsp/runtime.go (~50行) — 新建，启动/关闭编排
4. internal/sidecar/lsp/tools.go (~40行) — 新建，9 工具名→handler 注册表 (占位)

## 参考
- **cmd/mcp-lsp/fx.go (134行)** — 必须先读取确认现有内容
- 现有 cmd/mcp-orch/ 目录结构 (同类先例)
- internal/mcpserver/common/server.go (MCP server 基础)
- internal/mcpserver/common/manifest.go (工具清单)
- internal/mcpserver/common/stdio.go (stdio 传输)

## 约束
- 守卫: ≤400行/文件, ≤80行/函数, CC≤10
- 装配模式：仿照 `cmd/mcp-orch/runtime.go`，定义 `registryToolProvider` + `newStdioRunner`，由 `common.NewServer` 装配 stub server
- 无跨 Agent 依赖，S 可独立完成
- tools.go 中 9 个工具名必须精确: lsp_file, lsp_inspect, lsp_xref, lsp_grep,
  lsp_structure, lsp_edit, lsp_completion, code_run, code_run_test
- handler 暂用 stub (返回 "not implemented")
- **fx.go 扩展后总行数 ~238行 (134+104)，须 ≤400行守卫**

## 验证
go build ./cmd/mcp-lsp/...
```

### 7.2 Agent A — 协议+输出

```
你是 P9-A Agent，负责实现 LSP 协议层和输出格式化层。

## 任务
创建 protocol/ (5文件 ~520行) + format/ (4文件 ~520行)，总计 ~1,040 行。

## 输出文件
### protocol/
1. types.go (~130行) — Position, Range, Location, TextEdit, Diagnostic,
   SymbolInformation, CompletionItem, SignatureHelp 等 LSP 基础类型
2. methods.go (~40行) — "textDocument/definition" 等方法字符串常量
3. codec.go (~130行) — JSON-RPC 2.0 Request/Response/Notification 编解码
4. ext.go (~130行) — LocationResult(含func_start/func_end), CompactLocation,
   GroupedLocationResult, CallHierarchyResult 等 V3 扩展类型
5. notification.go (~90行) — publishDiagnostics/logMessage 通知处理

### format/
6. display.go (~200行) — 0-based→1-based 坐标转换 + URI→路径
7. compact.go (~120行) — compact 输出序列化 (lspCompactList/lspGroupedLocationResult)
8. funcrange.go (~80行) — func_start/func_end 基础计算
9. render.go (~120行) — JSON/表格/带行号文本 渲染工具

## V2 参考文件 (只参考行为，从零实现)
- go-agent-v2/pkg/toolsdk/lsp/protocol.go (310行) — 类型定义
- go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go (701行) — 扩展类型+常量
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_display.go (522行) — 坐标转换
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go (206行) — compact 输出
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_func_range.go (304行) — funcrange

## 共识约束
- #3: LocationResult 必须含 func_start/func_end 可选字段
- #4: display.go 必须实现完整坐标转换 (~200行，不可省略)
- 常量: lspCompletionCompactLimit=20, lspReferencesCompactLimit=30,
  lspWorkspaceSymbolCompactLimit=20, XRefResultLimit=50, SemanticTokenResultLimit=200

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.3 Agent B — Patch引擎

```
你是 P9-B Agent，负责实现 replace_range 的 patch 解析和上下文定位引擎。

## 任务
创建 edit/ (4文件)，总计 ~776 行。这是 lsp_edit replace_range 的核心算法层。

## 输出文件
1. edit/patchparse.go (~230行) — Parse(单hunk) + ParseMulti(多hunk) unified diff 解析
2. edit/patchmatch.go (~190行) — 多 hunk 匹配 + 歧义检测 + 两级 fallback
3. edit/seeksequence.go (~146行) — 4-pass 上下文定位算法
4. edit/replaceutil.go (~210行) — offset→行号转换 + 替换预览 + edit context 构建

## V2 参考文件 (关键算法必须行为对等)
- go-agent-v2/pkg/toolsdk/lsp/patch/parser.go — Parse(119行) + ParseMulti(121行)
  * 解析 "@@ context" 行 + "-old" + "+new" 格式
  * ParseMulti 处理多个 @@ 块
- go-agent-v2/pkg/toolsdk/lsp/replace_range_context.go (292行)
  * 两级 fallback: 1️⃣ resolveContextAnchorStarts → collectLineSequenceCandidates
    2️⃣ 失败时 collectRawSubstringCandidates → filterContextCandidates 匹配
  * 歧义检测: 多个 candidate 时报 ambiguous match 错误
  * 6 个核心函数 (V2 真名):
    - resolveContextMatch (292:34) — 入口
    - resolveContextAnchorStarts (292:60) — 锚点定位
    - collectLineSequenceCandidates (292:86) — 行序列候选
    - collectRawSubstringCandidates (292:122) — 子串候选 (fallback)
    - filterContextCandidates (292:149) — before/after 上下文过滤
    - buildEditContext (292:248) — 构建编辑上下文预览
- go-agent-v2/pkg/toolsdk/lsp/seek_sequence.go (146行)
  * 4-pass: exact → trimRight → trimBoth → unicodeNormalized
  * 4 个 seekMatch 常量名 (V2 seek_sequence.go:91-96):
    - `seekMatchExact` = "exact"
    - `seekMatchTrimRight` = "trim_right"
    - `seekMatchTrimBoth` = "trim_both"
    - `seekMatchUnicodeNormalized` = "unicode_normalized"
  * 核心函数: sequenceMatchAt(lines, pattern, start, mode) + lineMatch
  * 每个 pass 是独立函数，逐级放宽空白匹配

## 共识约束
- #5: patch 三件套全量，不可简化
- #6: seek_sequence 4-pass 全量，禁止只实现 exact match
- replaceutil.go 负责 offset→行号转换 + 替换预览 + edit context 构建

## 守卫
≤400行/文件, ≤80行/函数, CC≤10

## 验证
纯算法层，可独立单元测试 (不依赖 gopls)
```

### 7.4 Agent C1 — 客户端

```
你是 P9-C1 Agent，负责实现 gopls LSP 客户端和传输层。

## 任务
创建 gopls/client.go (~360行) + gopls/transport.go (~220行)，总计 ~580 行。

## 输出文件
1. gopls/client.go (~360行) — LSP Client
   - initialize/initialized 握手
   - shutdown/exit 关闭
   - request(method, params) → response (泛型)
   - notify(method, params) 单向通知
   - didOpen/didChange/didClose 文件同步
   - capability negotiation: 必须注册以下 11+1 capabilities (V2 client.go:99-135):
     TextDocument: PublishDiagnostics, Hover, Completion, Rename,
     CallHierarchy, TypeHierarchy, CodeAction, SignatureHelp, Formatting,
     FoldingRange, SemanticTokens
     Workspace: WorkspaceFolders
     必须注册的子字段：PublishDiagnostics.RelatedInformation, Hover.ContentFormat,
     Completion.DynamicRegistration, Rename.PrepareSupport, CallHierarchy.DynamicRegistration,
     TypeHierarchy.DynamicRegistration, CodeAction.DynamicRegistration, SignatureHelp.DynamicRegistration,
     Formatting.DynamicRegistration, FoldingRange.DynamicRegistration,
     SemanticTokens.DynamicRegistration + Requests.Range + Requests.Full.Delta + Formats=["relative"]
2. gopls/transport.go (~220行) — stdio JSON-RPC 传输
   - cmd.Start + stdin/stdout pipe
   - reader goroutine: 循环读 Content-Length header + JSON body
   - 请求 ID 分配 + 写入前注册 pending
   - 返回/超时/cancel 后删除 pending，close 时 clearPending
   - notification callback + stdin 写锁串行化 + process kill

## V2 参考文件
- go-agent-v2/pkg/toolsdk/lsp/client.go (491行) — Client 主体
- go-agent-v2/pkg/toolsdk/lsp/client_transport.go (126行) — 传输层
- go-agent-v2/pkg/toolsdk/lsp/client_tools.go (225行) — 工具方法封装

## 依赖
- protocol/ 类型 (Agent A 产出)

## 接口契约
导出 Client interface 供 Manager 使用 (C2 通过 interface 依赖，不直接依赖实现):
  type Client interface {
      Initialize(ctx, rootURI) error
      Shutdown(ctx) error
      Request(ctx, method, params) (json.RawMessage, error)
      Notify(ctx, method, params) error
      DidOpen(ctx, uri, languageID, version, text) error
      DidChange(ctx, uri, version, changes) error
      DidClose(ctx, uri) error
      Close() error
  }

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.5 Agent C2 — 管理器核心

```
你是 P9-C2 Agent，负责实现 gopls 生命周期管理器。

## 任务
创建 gopls/manager.go (~280行) + gopls/manager_lifecycle.go (~130行) + gopls/manager_symbols.go (~250行) + gopls/manager_symbols_fallback.go (~250行) + gopls/gomod.go (~90行)，
总计 ~900 行。

## 输出文件
1. gopls/manager.go (~280行) — Manager 主体
   - workspace 路由: 按 go.mod root 分配 client
   - ensureClientForFile(path) / ensureClientForLanguage(lang)
2. gopls/manager_lifecycle.go (~130行) — 生命周期
   - ensureClient(cfg) 双重检查锁 (共识#1)
   - Close() 全部 client shutdown
   - closed bool 永久关闭标志
3. gopls/manager_symbols.go (~250行) — 符号查询主路径
   - DocumentSymbol(ctx, uri) — 调 LSP textDocument/documentSymbol
   - WorkspaceSymbol(ctx, query) — 调 LSP workspace/symbol
4. gopls/manager_symbols_fallback.go (~250行) — fallback
   - markdown heading 提取 fallback (共识#7)
   - json key 提取 fallback
   - yaml key/indent symbol fallback
5. gopls/gomod.go (~90行) — go.mod root 发现
   - findGoModRoot(path) → root

## V2 参考文件
- go-agent-v2/pkg/toolsdk/lsp/manager.go (591行) — ensureClient 实现
- go-agent-v2/pkg/toolsdk/lsp/manager_markdown_symbols.go (483行) — fallback 逻辑
- go-agent-v2/pkg/toolsdk/lsp/gomod_root.go (83行) — root 发现

## 共识约束
- #1: ensureClient 必须懒启动+双重检查锁，禁止预启动
- #7: markdown/json/yaml fallback 必须实现
- Manager 通过 Client interface 依赖 C1 (不直接引用实现)

## 依赖
- protocol/ 类型 (Agent A 产出)
- C1 的 Client interface (可先定义 interface，C1 后续实现)

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.6 Agent E — Bootstrap+健康

```
你是 P9-E Agent，负责实现 gopls bootstrap、进程池、资源回收和缓存层。

## 任务
创建 gopls/ 下 5 个文件，总计 ~1,030 行。

## 输出文件
1. gopls/bootstrap_doc.go (~280行) — BootstrapDocument
   - 首次打开文件时 didOpen 通知 gopls
   - sibling bootstrap: 同目录 .go 文件自动 didOpen (cap=20, 共识#11)
   - refresh: 检测文件变更，重新同步
   - maxRefreshFiles=50, maxRefreshConcurrency=8
2. gopls/state.go (~210行) — bootstrap 状态机
   - 状态: pending → bootstrapping → ready → stale → error
   - 每个 workspace/uri 独立状态
   - 用于避免重复 bootstrap
3. gopls/pool.go (~180行) — 进程池
   - acquire(workspace) → client
   - release(client)
   - **pool clone**: resolveClone(index, rootURI) — V2 manager_pool.go:90
     克隆现有 Manager 配置创建新实例，用于多 workspace 场景
   - defaultPoolSize=10, maxPoolSize=20
4. gopls/recycler.go (~180行) — RSS 监控+回收
   - 周期性读取 gopls 进程 RSS (跨平台: linux/darwin)
   - 超阈值 → 优雅关闭旧进程 → 创建新进程
   - 共识#9: 必须实现
5. gopls/cache.go (~180行) — cache_store
   - 三元 key: workspace+language+uri
   - 默认: 内存 map + TTL cleanup goroutine
   - 可选: env AGENT_LSP_CACHE_PERSISTENT=1 → disk backend
   - startup write probe: 写入测试，失败降级内存 (H3)
   - persistent→memory fallback (R3)
   - 恢复 baseline metadata (已 bootstrap 的文件列表)

## V2 参考文件
- go-agent-v2/pkg/toolsdk/lsp/manager_bootstrap_document.go (477行)
- go-agent-v2/pkg/toolsdk/lsp/manager_bootstrap_document_state.go (298行)
- go-agent-v2/pkg/toolsdk/lsp/manager_pool.go (185行)
- go-agent-v2/pkg/toolsdk/lsp/manager_pool_recycler.go (182行)
- go-agent-v2/pkg/toolsdk/lsp/cache_store.go (287行)
- go-agent-v2/pkg/toolsdk/lsp/cache_model.go (58行)

## 共识约束
- #8: cache_store 必须 (内存默认+env-gated persistent)
- #9: 池回收+RSS 监控必须
- #11: bootstrap+sibling(cap=20) 必须
- 常量: maxSiblingBootstrap=20, maxRefreshFiles=50, maxRefreshConcurrency=8,
  defaultPoolSize=10, maxPoolSize=20, defaultLSPCacheTTL=7*24*time.Hour(168h)

## 依赖
- C2 (Manager)

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.7 Agent D — 文件+搜索+诊断

```
你是 P9-D Agent，负责实现 lsp_file、lsp_grep 和 diagnostics handler。

## 任务
创建 tools/ 下 3 个 handler + search/ 2 个工具 + middleware/budget.go，总计 ~1,220 行。

## 输出文件
1. tools/tool_file.go (~270行) — lsp_file handler
   - open_file: 通知 gopls didOpen
   - read_file 单文件: 带行号纯文本，offset/limit 分页
   - read_file 批量: file_paths → 并行读 → encodeBatchReadPayload 渐进裁剪 (T7)
   - diagnostics 分发: 委托 tool_diagnostics.go
   - read_file 不需要 gopls (requireManager=false)
2. tools/tool_grep.go (~220行) — lsp_grep handler
   - text_search: 正则/纯文本搜索 + path/glob 过滤
   - ast_search: 优先 sg(ast-grep) 后端，sg 不可用时返回错误 "sg not found in PATH"
   - filterAndCapSearchMatches: 排除+裁剪 (T8)
   - markdown/json/yaml fallback (共识#7)
3. tools/tool_diagnostics.go (~200行) — diagnostics 子 handler
   - waitDiagnosticsStable: 80ms init→40ms poll→800ms max (共识#10)
   - **generation tracking**: 必须实现 atomic.Uint64 诊断代次计数器，
     generation 仅在 runtime reset 时推进，didChange 路径不推进 generation
     (V2: manager_diagnostics.go currentDiagnosticGeneration/advanceDiagnosticGeneration)
   - reactive bootstrap: 诊断为空时自动 bootstrap (maxReactiveBootstrap=30)
   - 结果格式: {file, cols, rows} 表格
4. search/fileutil.go (~210行) — 文件读取安全门
   - 安全门: Lstat 存在性→拒绝 symlink→拒绝非 regular file→大小(2MB)→二进制(512B NUL 采样) (F1)
   - readToolFileContent 函数
5. search/searchutil.go (~220行) — 搜索工具
   - filterAndCapSearchMatches (T8)
   - glob 路径匹配 (非仅 basename，修复 V2 bug V-13)
6. middleware/budget.go (~100行) — 输出预算兜底

## V2 参考文件
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go (526行)
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go (669行)
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_diagnostics.go (365行)
- go-agent-v2/pkg/toolsdk/lsp/manager_diagnostics.go (221行)

## 共识约束
- #2: 截断必须工具级 (每个 handler 自行裁剪)
- #10: diagnostics waitStable 必须 (80ms/40ms/800ms)
- 常量: defaultReadFileLimit=300, maxReadFileLimit=2000, maxReadFileBytes=2MB,
  lspReadFileBatchMax=10, lspReadFileBatchPayloadMax=16KB, maxReactiveBootstrap=30

## 依赖
- C2 (Manager, 用于 gopls 调用)

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.8 Agent F — 导航+结构+补全

```
你是 P9-F Agent，负责实现 lsp_inspect、lsp_xref、lsp_structure、lsp_completion。

## 任务
创建 tools/ 下 4 个 handler，总计 ~1,060 行。

## 输出文件
1. tools/tool_inspect.go (~260行) — lsp_inspect handler
   - hover: 调 textDocument/hover → 提取 MarkedString/MarkupContent
   - definition: 调 textDocument/definition → LocationResult + func_start/func_end
   - implementation: 调 textDocument/implementation → 同上
   - type_definition: 调 textDocument/typeDefinition → 同上
   - signature_help: 调 textDocument/signatureHelp → SignatureHelpResult
2. tools/tool_xref.go (~280行) — lsp_xref handler
   - references: 调 textDocument/references → compact(分组+func_start/func_end)/full
   - call_hierarchy: prepareCallHierarchy → incoming/outgoing calls
   - type_hierarchy: prepareTypeHierarchy → supertypes/subtypes
   - compact 上限: lspReferencesCompactLimit=30, XRefResultLimit=50
3. tools/tool_structure.go (~300行) — lsp_structure handler
   - document_symbol: 调 textDocument/documentSymbol → 符号树
   - workspace_symbol: 调 workspace/symbol → compact 默认值 20（显式 max_results 可覆盖）
   - folding_range: 调 textDocument/foldingRange
   - semantic_tokens: 调 textDocument/semanticTokens/full → 上限 200
4. tools/tool_completion.go (~220行) — lsp_completion handler
   - 调 textDocument/completion → compact(label+kind+detail 默认 20，显式 max_results 可覆盖，最终 clamp 到 XRefResultLimit=50)/full

## V2 参考文件
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go (895行) — 核心 handler
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go (258行) — 输出格式化
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_navigation.go (155行) — 导航
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go (206行) — compact 常量

## 共识约束
- #3: func_start/func_end 必须包含 (definition/implementation/references compact)
- #4: 坐标转换必须 (调用 format/display.go)
- #7: document_symbol 需 markdown/json/yaml fallback (调 manager_symbols)

## 依赖
- C2 (Manager) + A (protocol/ + format/)

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.9 Agent G — 编辑+执行+胶水

```
你是 P9-G Agent，负责实现 lsp_edit、code_run、code_run_test 和中间件。

## 任务
创建 tools/ 下 4 个 handler + exec/ + middleware/ 3文件，总计 ~1,420 行。
这是行数最多的 Agent，也是关键路径上最长的实现 Agent。

## 输出文件
1. tools/tool_edit.go (~280行) — lsp_edit 主 handler
   - rename: 调 textDocument/rename → 应用 WorkspaceEdit
   - code_action: 调 textDocument/codeAction → 列出可用操作
   - format: 调 textDocument/formatting → 应用 TextEdit[]
   - replace_range: 参数校验 + 分发到 tool_edit_replace.go
2. tools/tool_edit_replace.go (~280行) — replace_range 应用层
   - patch 解析(调 edit/) → seek_sequence 定位 → 按顺序应用多 hunk → didChange → 返回
   - 支持: patch 格式 / 坐标范围 + new_text 格式 / edits 数组格式
   - **multi-edit (edits数组)**: 上限 20 个 edit (V2 tool_handlers_edit_flow.go:82)，
     顺序应用，重叠检测，带 replaceWithDeleteOptimization (V2:793)
   - ~~dry_run~~ 已删除：V3 不暴露 dry_run，始终执行实际替换
   - `force` 不暴露为外部参数；内容 ≤2MB 时内部 didChange 自动 `force=true`
   - 返回: matched_by, resolved_*, edit_context, func_start/func_end
   - 失败返回: error + current_content + func_start/func_end
   - 安全限制: replaceRangeMaxReplacementBytes=256KB, replaceRangeMaxContentBytes=4MB
3. tools/tool_coderun.go (~200行) — code_run handler
   - run 模式: 写临时文件 → 执行 → 捕获输出
   - project_cmd 模式: 在 work_dir 执行 shell 命令
   - auto_wrap: Go 代码自动包装 package main + imports
   - 审批不在此层 (共识#12)，V2 用 ApprovalProvider.AwaitApproval，
     V3 需重新设计审批接口 (非 bootstrap.RequestApproval)
4. tools/tool_coderuntest.go (~220行) — code_run_test handler
   - 保留结构化 test mode：独立 handler + CodeRunRequest{Mode:"test", TestFunc, TestPkg}
   - 构造 go test -run <func> <pkg> 命令
   - 复用 code_run 执行引擎（不得降级为拼 shell 再转发 project_cmd）
5. exec/sandbox.go (~220行) — 命令执行沙箱
   - exec.CommandContext + timeout
   - work_dir 校验 (项目根目录内)
   - stdout+stderr capture + truncation
   - exit code 提取
6. middleware/logging.go (~90行) — 请求/响应结构化日志
7. middleware/recovery.go (~60行) — panic 恢复
8. middleware/timeout.go (~70行) — 超时控制
   - TierFast=5s, TierNormal=30s, TierSlow=120s, TierExec=300s

## V2 参考文件
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go (1,012行)
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit_enclosing.go (234行)
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit_flow.go (109行)
- go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go (256行)
- **go-agent-v2/pkg/toolsdk/lsp/replace_range_runtime.go (119行)** — 含 replaceRangeDiskContentIfStale 等运行时内容解析函数
- go-agent-v2/pkg/toolsdk/tools/code_run.go

## 共识约束
- #5: replace_range 必须调用 edit/ 的 patch 三件套 (Agent B 产出)
- #6: seek_sequence 4-pass 必须 (通过 edit/seeksequence.go)
  seekMatch 常量: seekMatchExact/seekMatchTrimRight/seekMatchTrimBoth/seekMatchUnicodeNormalized
- #12: code_run 审批不在 LSP 层
- 常量: replaceRangeMaxReplacementBytes=256KB, replaceRangeMaxContentBytes=4MB,
  replaceRangeForceBypassMaxBytes=2MB, replaceRangeFuncBodyMax=8KB,
  didChangeLargeFileLineThreshold=200

## 依赖
- C2 (Manager) + B (edit/ patch 引擎)

B↔G 接口契约：
- `edit.Parse(patch string) -> (Hunk, error)`
- `edit.ParseMulti(patch string) -> ([]Hunk, error)`
- `edit.MatchContext(content string, hunks []Hunk) -> ([]Match, error)`
- `edit.SeekSequence(lines []string, pattern []string, start int) -> (int, MatchMode, error)`
- G 负责消费 ParseMulti 产出的多 hunk，按顺序应用，任一失败则整体回滚。

## 守卫
≤400行/文件, ≤80行/函数, CC≤10
```

### 7.10 Agent V — 验证

```
你是 P9-V Agent，负责全量验证和修复。

## 任务
验证所有 Agent 产出的代码，修复编译/lint/archtest 违规。

## 验证清单

### 1. 编译验证
go build ./cmd/mcp-lsp/...
go build ./...

### 2. 静态分析
go vet ./cmd/mcp-lsp/...

### 3. archtest 验证
go test ./internal/archtest/ -run TestMCPLSPDependencyDirection
go test ./internal/archtest/ -run TestMCPLSPExactToolSet
go test ./internal/archtest/ -run TestCodeSizeGuard

### 4. Schema 快照验证
- 9 个工具的 JSON Schema 必须与 V2 MCP manifest 对齐
- 工具名精确匹配: lsp_file, lsp_inspect, lsp_xref, lsp_grep,
  lsp_structure, lsp_edit, lsp_completion, code_run, code_run_test

### 5. 冒烟测试 (每工具至少 1 个 happy path)
- lsp_file: read_file 单文件 + batch
- lsp_inspect: definition
- lsp_xref: references compact
- lsp_grep: text_search
- lsp_structure: document_symbol
- lsp_edit: replace_range (patch 格式)
- lsp_completion: 基础补全
- code_run: Go snippet run
- code_run_test: go test 单函数

### 6. 容错测试
- T1-T9 截断: 超限输入是否正确截断
- F1-F3 大文件: 超大文件是否被安全门拦截
- R2-R3 恢复/降级: 超时传播 + cache fallback 是否工作
- H1-H3 健康: waitStable 是否等待

### 7. 守卫检查
- 所有文件 ≤400 行
- 所有函数 ≤80 行
- 圈复杂度 CC ≤10
- 每目录 ≤15 文件

### 修复策略
- 编译错误: 直接修复
- 行数超限: 拆分文件 (如 tool_edit.go → tool_edit.go + tool_edit_replace.go)
- CC 超限: 提取子函数
- archtest 违规: 调整依赖方向

## 依赖
全部 Agent (S/A/B/C1/C2/D/E/F/G) 完成后启动
```

---

## 8. 验收标准

### 8.1 编译验证 (Gate 1 — 必须全绿)

| 检查 | 命令 | 期望 |
|---|---|---|
| 编译 mcp-lsp 全族 | `go build ./cmd/mcp-lsp/...` | 0 errors |
| 全仓回归编译 | `go build ./...` | 0 errors |
| go vet | `go vet ./cmd/mcp-lsp/...` | 0 warnings |
| LSP diagnostics | `lsp_file(diagnostics, cmd/mcp-lsp/)` | 0 diagnostics |

### 8.2 功能验证 (Gate 2 — 9 工具 × N actions)

| 工具 | Action | 验证点 |
|---|---|---|
| lsp_file | open_file | 返回 success+bytes |
| lsp_file | read_file (单) | 带行号输出 + offset/limit |
| lsp_file | read_file (批量) | 多文件+渐进裁剪 |
| lsp_file | diagnostics | waitStable + 表格输出 |
| lsp_inspect | hover | 返回类型信息或 "no hover" |
| lsp_inspect | definition | LocationResult + func_start/func_end |
| lsp_inspect | implementation | 接口实现列表 |
| lsp_inspect | type_definition | 类型定义位置 |
| lsp_inspect | signature_help | 函数签名 |
| lsp_xref | references (compact) | 分组 + func_start/func_end |
| lsp_xref | references (full) | 完整 Location 列表 |
| lsp_xref | call_hierarchy | incoming/outgoing |
| lsp_xref | type_hierarchy | supertypes/subtypes |
| lsp_grep | text_search | 正则+路径过滤 |
| lsp_grep | ast_search | sg 后端可用 / 不可用时报 `sg not found in PATH` |
| lsp_structure | document_symbol | 符号树 |
| lsp_structure | workspace_symbol | 全局搜索 |
| lsp_structure | folding_range | 折叠范围 |
| lsp_structure | semantic_tokens | token 列表 |
| lsp_edit | replace_range (patch) | 匹配+替换+func context |
| lsp_edit | replace_range (edits) | multi-edit 顺序应用 + overlap 检测 |
| ~~lsp_edit~~ | ~~replace_range (dry_run)~~ | ~~已删除：V3 不暴露 dry_run~~ |
| lsp_edit | rename | 多文件重命名 |
| lsp_edit | code_action | 可用操作列表 |
| lsp_edit | format | 格式化 |
| lsp_completion | (default) | compact 补全列表 |
| code_run | run (Go) | 执行+输出 |
| code_run | project_cmd | shell 命令 |
| code_run_test | (default) | go test 单函数 |

### 8.3 容错验证 (Gate 3)

> 以下为代表性容错样例，用于验收核心行为；不宣称对全部容错分支做穷尽覆盖。

| 类别 | 测试用例 | 期望行为 |
|---|---|---|
| T1 | read_file limit=5 对 100 行文件 | 只返回 5 行 + 截断提示 |
| T2 | batch read 20 个大文件 | 截断到 10 + payload ≤16KB |
| T3 | text_search 设置 max_results=5，真实命中 1000 条 | 仅返回前 5 条，`total/showing` 正确反映裁剪 |
| T4 | workspace_symbol compact 命中 100 个符号 | compact 结果裁剪到 20 条，且保留总数信息 |
| T5 | completion compact 返回 50 个候选 | compact 仅返回 20 条；full 模式不套用 compact cap |
| T6 | references 在 200 处命中（同时验 compact/full） | compact `showing<=30`；full 结果总量仍受 `XRefResultLimit=50` 约束 |
| T7 | encodeBatchReadPayload | 渐进裁剪验证 |
| T8 | text_search 命中被排除路径和超量结果 | 排除模式先过滤，再裁剪到 max_results |
| T9 | semantic_tokens 返回 500 个 token | 裁剪到 200 个 token |
| F1 | read_file 3MB 文件 | 安全门拦截 |
| F1 | read_file 二进制文件 | 二进制检测拦截 |
| F2 | replace_range 300KB 替换体 | 超限拒绝 |
| F3 | 对 500 行文件执行 replace_range → didChange | 编辑继续执行，但返回/日志包含 large file warning |
| R2 | callWithContextTimeout 超时 | 返回 context.DeadlineExceeded 错误，不卡死 |
| R3 | cache persistent 写失败 | 降级内存，日志警告 |
| H1 | gopls 进程 RSS 超阈值 | recycler 回收旧进程 + 创建新进程 |
| H2 | diagnostics 对刚 didOpen 的文件 | waitStable 后返回 |
| H3 | ensurePersistentReady 写入探测失败 | 降级到纯内存模式，后续不再尝试磁盘 |

### 8.4 守卫验证 (Gate 4)

| 规则 | 检查方式 | 标准 |
|---|---|---|
| 文件行数 | archtest CodeSizeGuard | ≤ 400 行 |
| 函数行数 | archtest CodeSizeGuard | ≤ 80 行 |
| 圈复杂度 | archtest CodeSizeGuard | CC ≤ 10 |
| 目录文件数 | archtest CodeSizeGuard | ≤ 15 个 |
| 依赖方向 | archtest DependencyDirection | 无反向依赖 |
| 工具集 | archtest ExactToolSet | 精确 9 个 |

### 8.5 性能基线

| 指标 | 基线 | 方法 |
|---|---|---|
| gopls 首次启动 | < 30s | 计时 ensureClient |
| definition 响应 | < 5s | 计时 lsp_inspect(definition) |
| text_search 响应 | < 30s | 计时 lsp_grep(text_search) |
| replace_range 响应 | < 30s | 计时 lsp_edit(replace_range) |
| 内存占用 | < 512MB RSS | 监控 gopls 进程 |

---

## 9. archtest 新增规则

### 9.1 TestMCPLSPDependencyDirection

> ℹ️ **以下为伪代码示意**，实际实现需使用 internal/archtest 现有框架（非 testify）。

```go
// 伪代码 — 验证 cmd/mcp-lsp 本地子包的依赖方向:
// - protocol/ 不依赖任何 lsp 子包
// - format/ 只依赖 protocol/
// - edit/ 不依赖 gopls/ 或 tools/
// - search/ 不依赖 gopls/ 或 tools/
// - gopls/ 依赖 protocol/, 不依赖 tools/
// - tools/ 依赖 protocol/ + format/ + gopls/ + edit/ + search/
// - middleware/ 不依赖 tools/ (被 tools/ 调用，不反向)
// - cmd/mcp-lsp 根装配只依赖本地子包的公开 API
func TestMCPLSPDependencyDirection(t *testing.T) {
    // 禁止的导入方向
    forbidden := map[string][]string{
        "protocol":   {"gopls", "tools", "format", "edit", "search", "middleware", "exec"},
        "format":     {"gopls", "tools", "edit", "search", "middleware", "exec"},
        "edit":       {"gopls", "tools", "middleware", "exec"},
        "search":     {"gopls", "tools", "middleware", "exec"},
        "gopls":      {"tools"},
        "middleware":  {"tools"},
        "exec":       {"tools", "gopls"},
    }
    // ... 扫描 import 验证
}
```

### 9.2 TestMCPLSPExactToolSet

> ℹ️ **伪代码示意**

```go
// 伪代码 — 验证注册的工具集精确匹配 9 个工具名
func TestMCPLSPExactToolSet(t *testing.T) {
    expected := []string{
        "lsp_file", "lsp_inspect", "lsp_xref", "lsp_grep",
        "lsp_structure", "lsp_edit", "lsp_completion",
        "code_run", "code_run_test",
    }
    // 从 internal/sidecar/lsp/tools.go 或 manifest 中提取实际注册的工具名
    // assert.ElementsMatch(t, expected, actual)
}
```

### 9.3 TestMCPLSPNoDirectV2Import

> ℹ️ **伪代码示意**

```go
// 伪代码 — 验证 cmd/mcp-lsp 本地子包不直接导入 V2 包
func TestMCPLSPNoDirectV2Import(t *testing.T) {
    // 扫描 cmd/mcp-lsp/ 下所有 .go 文件
    // 禁止 import "xxx/pkg/toolsdk/lsp"
}
```

### 9.4 TestMCPLSPBudgetConstants

> ℹ️ **伪代码示意**，实际测试中 `assert.Equal` 需替换为 archtest 框架的断言方式。

```go
// 伪代码 — 验证安全常量值与 V2 对齐
func TestMCPLSPBudgetConstants(t *testing.T) {
    // 验证关键常量值未被意外修改
    assert.Equal(t, 300, defaultReadFileLimit)
    assert.Equal(t, 10, lspReadFileBatchMax)
    assert.Equal(t, 20, lspCompletionCompactLimit)
    // ... 31 个常量全量验证
}
```

---

## 10. 风险与缓解

### 10.1 关键风险矩阵

| # | 风险 | 概率 | 影响 | 缓解措施 |
|---|---|---|---|---|
| R1 | gopls 进程管理复杂度超预期 | 中 | 高 | Agent E 专攻 bootstrap+pool+recycler；V2 参考已验证架构可行 |
| R2 | replace_range patch 解析边界情况 | 高 | 高 | Agent B 独立开发+大量单元测试；V2 seek_sequence 已覆盖 4-pass |
| R3 | 文件行数超 400 行守卫 | 中 | 中 | manager.go 和 tool_edit.go 已拆分确保 ≤400；Agent V 负责收尾超限文件 |
| R4 | Agent 间接口不兼容 | 中 | 高 | protocol/ 和 interface 在 A/C1/C2 中优先定义；Agent S 提供骨架 |
| R5 | sg (ast-grep) 后端不可用 | 低 | 中 | ast_search 返回 "sg not found in PATH" 错误；text_search 不依赖 sg |
| R6 | 跨平台 RSS 监控 | 低 | 低 | linux /proc/pid/status + darwin ps 命令；降级模式跳过监控 |
| R7 | diagnostics waitStable 超时 | 低 | 中 | 800ms max 已有上限；超时返回当前结果(非空) |
| R8 | cache persistent 后端兼容性 | 低 | 低 | 默认纯内存；persistent 是 opt-in；有 fallback (R3) |
| R9 | Agent G 行数过多成为瓶颈 | 中 | 中 | G 是关键路径最长(1.75h)；tool_edit 已拆分，必要时优先裁剪非关键胶水工作 |
| R10 | V2↔V3 行为不一致 | 中 | 高 | Schema 快照对比 + 冒烟测试 + 容错测试覆盖 29 个 action |

### 10.2 紧急缓解预案

| 场景 | 预案 |
|---|---|
| Agent G 超时 | 固定 tool_edit.go / tool_edit_replace.go 边界，优先延后低优先胶水工作，避免再次拆分 |
| Agent E 超时 | cache.go 先用纯内存实现，persistent 后续补充 |
| 编译失败 | Agent V 有 2h 缓冲用于修复；接口问题可追溯到 A/C1/C2 |
| 行数超限 | manager.go 和 tool_edit.go 已拆分；Agent V 负责处理剩余超限文件 |

### 10.3 回滚策略

- LSP 本体位于独立的 `cmd/mcp-lsp/` 二进制目录及其本地子包，不影响现有 `internal/mcpserver/common/`
- `cmd/mcp-lsp/` 是新二进制，不影响 `cmd/mcp-orch/` 或其他二进制
- 可随时回退 `cmd/mcp-lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}` 的搬迁提交

---

## 附录 A: V2 → V3 文件映射表

| V2 文件 | V2 行数 | V3 文件 | V3 预估 | Agent |
|---|---|---|---|---|
| protocol.go | 310 | protocol/types.go + methods.go | ~170 | A |
| protocol_ext_common.go | 701 | protocol/ext.go + codec.go + notification.go | ~350 | A |
| tool_handlers_display.go | 522 | format/display.go | ~200 | A |
| tool_handlers_compact.go | 206 | format/compact.go | ~120 | A |
| tool_handlers_func_range.go | 304 | format/funcrange.go | ~80 | A |
| patch/parser.go | ~240 | edit/patchparse.go | ~250 | B |
| replace_range_context.go | 292 | edit/patchmatch.go + replaceutil.go | ~380 | B |
| seek_sequence.go | 145 | edit/seeksequence.go | ~150 | B |
| client.go | 491 | gopls/client.go | ~360 | C1 |
| client_transport.go | 126 | gopls/transport.go | ~220 | C1 |
| manager.go | 591 | gopls/manager.go + manager_lifecycle.go | ~410 | C2 |
| manager_markdown_symbols.go | 483 | gopls/manager_symbols.go + manager_symbols_fallback.go | ~500 | C2 |
| gomod_root.go | 83 | gopls/gomod.go | ~90 | C2 |
| manager_bootstrap_document.go | 477 | gopls/bootstrap_doc.go | ~280 | E |
| manager_bootstrap_document_state.go | 298 | gopls/state.go | ~210 | E |
| manager_pool.go | 185 | gopls/pool.go | ~180 | E |
| manager_pool_recycler.go | 182 | gopls/recycler.go | ~180 | E |
| cache_store.go + cache_model.go | 345 | gopls/cache.go | ~180 | E |
| tool_handlers_file.go | 526 | tools/tool_file.go + search/fileutil.go | ~480 | D |
| tool_handlers_search.go | 669 | tools/tool_grep.go + search/searchutil.go | ~440 | D |
| tool_handlers_diagnostics.go + manager_diagnostics.go | 586 | tools/tool_diagnostics.go | ~200 | D |
| tool_handlers_core.go | 895 | tools/tool_inspect.go + tool_xref.go + tool_structure.go | ~840 | F |
| tool_handlers_p1_outputs.go | 258 | tools/tool_completion.go (部分) | ~220 | F |
| replace_range_runtime.go | 119 | edit/replaceutil.go (部分) | ~50 | G |
| tool_handlers_edit.go + edit_enclosing + edit_flow | 1,355 | tools/tool_edit.go + tool_edit_replace.go | ~560 | G |
| tool_handlers_dispatch.go | 256 | 分散到各 tool handler | - | G |
| manager_bootstrap.go | 152 | gopls/manager_lifecycle.go (部分) | 合并入 lifecycle | C2 |
| manager_tools.go | 112 | 分散到各 tools/tool_*.go | 分散 | F+G |
| tool_handlers_workspace_manager.go | 81 | 分散到各 tools/tool_*.go（workspace manager 选择/校验） | 分散 | D+F+G |
| tool_handlers_workspace_root.go | 45 | gopls/gomod.go + 各 tools/tool_*.go（workspace_root 解析/归一化） | ~45 | C2 + D/F/G |
| client_tools.go | 226 | gopls/client.go (部分) | 合并入 client | C1 |
| (code_run.go in toolsdk/tools) | ~200 | tools/tool_coderun.go + tool_coderuntest.go + exec/ | ~640 | G |

## 附录 B: 12 项共识速查卡

| # | 共识 | 一句话 | 禁止 |
|---|---|---|---|
| 1 | 懒启动 | ensureClient 双重检查锁 | 预启动 |
| 2 | 工具级截断 | 每个工具自行裁剪 | 统一 truncate 中间件 |
| 3 | func_start/func_end | 定义/引用结果附带 | 省略 |
| 4 | display 坐标转换 | 0→1-based；路径规范化在 search 层 | 直接输出 0-based |
| 5 | patch 三件套 | Parse+ParseMulti+context+seek | 简化 |
| 6 | seek_sequence 4-pass | exact→trimR→trimB→unicodeNorm | 只 exact |
| 7 | md/json/yaml fallback | 非 Go 文件符号提取 | 只支持 gopls 语言 |
| 8 | cache_store | 内存默认+env-gated persistent | 跳过 |
| 9 | 池回收+RSS | 周期监控+超阈值回收 | 不监控 |
| 10 | waitStable | 80ms/40ms/800ms；generation 仅 runtime reset 推进 | 立即返回 |
| 11 | sibling bootstrap | 同目录 .go 文件 cap=20 | 只当前文件 |
| 12 | 审批不在 LSP | V2: approvals.AwaitApproval; V3 重新设计 | LSP 层审批 |
