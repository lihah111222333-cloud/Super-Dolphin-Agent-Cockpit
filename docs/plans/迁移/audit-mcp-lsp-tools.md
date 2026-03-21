# MCP LSP 工具族完整审查

## 审查范围与方法

- 本次仅用 LSP 工具链做静态审查；未改业务代码。
- V2 实际 LSP MCP surface 不在 `go-agent-v2/internal/mcp/lsp/` 独立目录下，而是由 `go-agent-v2/internal/mcp/runtime.go:16` 直接引入 `go-agent-v2/pkg/toolsdk/lsp`，并在 `go-agent-v2/internal/mcp/runtime.go:86-101`、`go-agent-v2/internal/mcp/runtime.go:154-176` 用 `lsp.ToolHandlers` 包装成 MCP runtime provider。
- V2 的 schema surface 由 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:33-143` 定义，注册链为 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:145-168` -> `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:174-213` -> `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:94-116` -> `go-agent-v2/internal/mcp/runtime.go:104-114` / `go-agent-v2/internal/apiserver/server_dynamic_tools.go:38-48`。
- V3 当前仅审查 `cmd/mcp-lsp/*` 与 `internal/mcpserver/common/*`，并补查当前仓库里的已注册 LSP/RUN tool 痕迹。

## 1. V2 LSP 工具完整清单

### 1.1 实际 tool surface

V2 当前可确认的 MCP LSP tool 一共 7 个，全部定义在 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:33-143`，且 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:167-168` 明确 `LSPAddonTools()` 当前返回 `nil`。

| Tool | V2 schema 定义 | Action / 说明 | 实现入口 |
|---|---|---|---|
| `lsp_file` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:35-48` | `open_file` / `read_file` / `diagnostics` | `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:15-20`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:232-234` |
| `lsp_inspect` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:49-60` | `hover` / `definition` / `implementation` / `type_definition` / `signature_help` | `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:22-28`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:235-237` |
| `lsp_xref` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:61-77` | `call_hierarchy` / `type_hierarchy` / `references` | `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:30-34`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:238-240` |
| `lsp_grep` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:78-94` | `text_search` / `ast_search` | `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:36-39`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:241-243` |
| `lsp_structure` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:95-109` | `document_symbol` / `workspace_symbol` / `folding_range` / `semantic_tokens` | `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:41-46`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:244-246` |
| `lsp_edit` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:110-130` | `rename` / `code_action` / `format` / `replace_range` | `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:48-53`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:247-249` |
| `lsp_completion` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:131-141` | 独立 tool，无 `action` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:132-140`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:981-986`, `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:90-158` |

### 1.2 V2 注册点

- `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:145-155` 用 `RegisterLSPHandlers` 把 schema name 绑定到 provider handler。
- `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:174-213` 用 `buildLSPTools` 将 `tools.LSPTools()` 的 schema 与 handler 组合为 runtime tool。
- `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:94-116` 用 `Register` 真正把 runtime handler 注册进 registry。
- `go-agent-v2/internal/mcp/runtime.go:104-114` 与 `go-agent-v2/internal/mcp/runtime.go:374-376` 在 MCP runtime 内调用 `tooladapter.Register(...)`。
- `go-agent-v2/internal/apiserver/server_dynamic_tools.go:38-48` 在 apiserver 侧也走同一条 `tooladapter.Register(...)` 注册链。
- `go-agent-v2/internal/mcp/stdio.go:135-175` 的 `tools/list` 会把 runtime schemas 合并进 MCP 输出，注释明确点名包含 `ida`、`lsp_*`、`code_run`。

### 1.3 `lsp_` / `code_run` 前缀方法与 legacy 名称

V2 代码里除了 7 个正式 MCP tool 名，还存在一组内部/legacy `lsp_` 前缀执行名；它们不是额外 MCP tool，而是 merged tool 的 action 分派名或 logger 名。

- 文件类：`lsp_open_file`、`lsp_read_file`、`lsp_diagnostics` 分别出现在 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:62-88`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:221-236`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:497-545`。
- 搜索类：`lsp_text_search`、`lsp_ast_search` 出现在 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:59-113`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:133-185`。
- inspect/xref/structure 类：`lsp_hover`、`lsp_definition`、`lsp_implementation`、`lsp_type_definition`、`lsp_signature_help`、`lsp_references`、`lsp_call_hierarchy`、`lsp_type_hierarchy`、`lsp_document_symbol`、`lsp_workspace_symbol`、`lsp_semantic_tokens`、`lsp_folding_range` 出现在 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:713-1009`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:12-187`。
- edit 类：`lsp_rename`、`lsp_code_action`、`lsp_format`、`lsp_replace_range`、`lsp_replace_range_multi`、`lsp_did_change` 出现在 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:56-163`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:202-246`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:345-746`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:945-1074`。
- `code_run` / `code_run_test` 正式 tool 名定义在 `go-agent-v2/pkg/toolsdk/tools/code_run.go:33-76`，dispatcher 对这两个名字做专门参数上限和 cancel-tracker 处理见 `go-agent-v2/pkg/toolsdk/tooladapter/dispatch.go:28-39`、`go-agent-v2/pkg/toolsdk/tooladapter/dispatch.go:118-154`。

## 2. V3 当前 LSP 工具清单

### 2.1 `cmd/mcp-lsp`

- `cmd/mcp-lsp/main.go:8-13` 只调用 `run()`。
- `cmd/mcp-lsp/fx.go:5-12` 只构造 `fx.New(fx.NopLogger)`；没有引入任何 LSP family module、tool registry、manifest 或 server wiring。

### 2.2 `internal/mcpserver/common`

- `internal/mcpserver/common/manifest.go:3-12` 只定义 `ToolManifest` / `FamilyManifest` 类型，没有任何 tool 实例。
- `internal/mcpserver/common/server.go:8-21` 只有 `Server` 结构、`NewServer`、`Run(ctx)`；没有 `tools/list`、`tools/call` 或注册逻辑。
- `internal/mcpserver/common/stdio.go:1-3` 只有 “later phase” 注释，没有 transport 实现。

### 2.3 结论

在当前 V3 源码里，`cmd/mcp-lsp/*` 与 `internal/mcpserver/common/*` 仍是骨架，没有任何已注册的 LSP tool，也没有 `code_run` / `code_run_test` 对应的 family wiring。证据就是这几个路径下当前只有空 `fx` 启动、空 server 壳和 manifest 类型定义：`cmd/mcp-lsp/fx.go:5-12`、`internal/mcpserver/common/server.go:14-21`、`internal/mcpserver/common/manifest.go:3-12`、`internal/mcpserver/common/stdio.go:1-3`。

## 3. 重点检查：`code_run` / `code_run_test`

### 3.1 V2 定义位置

- `code_run` / `code_run_test` 两个 tool 的 schema 和 handler 都在 `go-agent-v2/pkg/toolsdk/tools/code_run.go:33-76`。
- 这两个 tool 会被 `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-171` 的 `appendCommonTools(...)` 并入 MCP tool 集合。
- dispatcher 对这两个名字有独立的参数大小与取消跟踪逻辑：`go-agent-v2/pkg/toolsdk/tooladapter/dispatch.go:28-39`、`go-agent-v2/pkg/toolsdk/tooladapter/dispatch.go:118-154`。

### 3.2 V3 对应情况

当前 V3 inspected paths 下没有任何 `code_run` / `code_run_test` family wiring。`cmd/mcp-lsp/fx.go:5-12` 未挂载模块；`internal/mcpserver/common/server.go:14-21` 也没有 tool registry 或 dispatch，因此结论是 V3 当前没有对应。

## 4. 逐一对照表

| V2 Tool Name | V2 文件 | V3 对应 | 状态 |
|---|---|---|---|
| `lsp_file` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:35-48` | 未发现注册点；`cmd/mcp-lsp/fx.go:5-12` 仅空 `fx.New(...)`，`internal/mcpserver/common/server.go:14-21` 仅空 server | ❌ |
| `lsp_inspect` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:49-60` | 同上；未发现 family wiring | ❌ |
| `lsp_xref` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:61-77` | 同上；未发现 family wiring | ❌ |
| `lsp_grep` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:78-94` | 同上；未发现 family wiring | ❌ |
| `lsp_structure` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:95-109` | 同上；未发现 family wiring | ❌ |
| `lsp_edit` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:110-130` | 同上；未发现 family wiring | ❌ |
| `lsp_completion` | `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:131-141` | 同上；未发现 family wiring | ❌ |
| `code_run` | `go-agent-v2/pkg/toolsdk/tools/code_run.go:38-57` | 同上；未发现 family wiring | ❌ |
| `code_run_test` | `go-agent-v2/pkg/toolsdk/tools/code_run.go:58-74` | 同上；未发现 family wiring | ❌ |

## 5. 缺失工具完整参数签名

### 5.0 通用返回约定

- 大多数 LSP handler 的失败都走统一 error envelope：`{"success":false,"error":...}`，定义在 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:301-309`。
- 大多数 LSP handler 的空结果都走统一 empty envelope：`{"success":true,"data":[],"meta":{"count":0,"message":...}}`，定义在 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:491-540`。
- 非空成功结果通常直接返回“裸 JSON typed payload”，不是统一 `success:true` envelope；这是 `runAndMarshalLogged(...)` 的直接 `json.Marshal(normalized)` 行为，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:529-539`。
- 例外：`lsp_file(action=read_file)` 单文件返回编号后的纯文本片段，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:250-260`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:434-473`；`lsp_inspect(action=hover)` 返回原始 hover 字符串或 `"no hover info available"`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:889-919`；`lsp_edit` 的 `rename` / `replace_range` / `did_change` 成功路径返回专用 success envelope，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:226-246`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:56-137`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:345-512`。

### 5.1 `lsp_file`

- 参数签名：`action:string(required, enum=open_file|read_file|diagnostics)`、`file_path:string(optional)`、`file_paths:string[](optional)`、`offset:number(optional)`、`limit:number(optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:35-48`。
- 返回结构：
  - `open_file` -> `{"success":true,"status":"opened","message":"opened","file_path":string,"bytes":number}`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:62-88`。
  - `read_file` 单文件 -> 纯文本 `"N: line\n..."`，带 offset/limit 截断提示，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:250-260`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:434-473`。
  - `read_file` 批量 -> `{"success":true,"data":[{"file_path":string,"success":bool,"content"?:string,"error"?:string}],"meta":{"count":number,"success_count":number,"error_count":number,"truncated"?:bool,"requested_count"?:number,"max_batch"?:number,"dropped"?:number,"message"?:string}}`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:263-312`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:375-416`。
  - `diagnostics` -> `{"success":true,"data":[{"file":string,"cols":[...],"rows":[...]}],"meta":{"count":number,"source":string}}`，空结果时 `meta.message="no diagnostics"`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:497-545`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:701-748`。
- V2 实现：分派见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:15-20`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:232-234`；具体 handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:62-88`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:221-236`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_file.go:497-545`。

### 5.2 `lsp_inspect`

- 参数签名：`action:string(required, enum=hover|definition|implementation|type_definition|signature_help)`、`file_path:string(required)`、`line:number(required)`、`column:number(required)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:49-60`。
- 返回结构：
  - `hover` -> 原始字符串；空时返回 `"no hover info available"`，失败时返回 error envelope，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:889-919`。
  - `definition` / `implementation` / `type_definition` -> `[]LocationResult`；序列化后每项是扁平对象 `{file,line,col,end_line?,end_col?,func_start?,func_end?}`，结构定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:82-88`、`go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:106-135`，具体 handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:922-955`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:988-1009`。
  - `signature_help` -> `SignatureHelpResult{signatures,activeSignature?,activeParameter?}`，其中 `signatures[]` 为 `SignatureInformationResult{label,documentation?,documentationKind?,parameters[]}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:404-420`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:1002-1040`。
- V2 实现：分派见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:22-28`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:235-237`；handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:889-1009`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:1002-1040`。

### 5.3 `lsp_xref`

- 参数签名：`action:string(required, enum=call_hierarchy|type_hierarchy|references)`、`file_path:string(required)`、`line:number(required)`、`column:number(required)`、`direction:string(optional, enum=incoming|outgoing|both|supertypes|subtypes)`、`include_declaration:boolean(optional)`、`verbosity:string(optional, enum=compact|full)`、`max_results:number(optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:61-77`。
- 返回结构：
  - `references` 默认 compact -> `lspGroupedLocationResult{files:map[string][]lspCompactLocation,total,showing,hint?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go:39-51`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go:183-206`；full -> `[]Location`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol.go:35-38`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:12-88`。
  - `call_hierarchy` -> `[]CallHierarchyResult{item,incoming?,outgoing?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:387-391`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:713-727`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:745-785`。
  - `type_hierarchy` -> `[]TypeHierarchyResult{item,supertypes?,subtypes?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:392-396`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:729-743`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:745-785`。
- V2 实现：分派见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:30-34`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:238-240`；handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:12-88`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:713-785`。

### 5.4 `lsp_grep`

- 参数签名：`action:string(required, enum=text_search|ast_search)`、`query:string(optional for schema but text_search 实际 required)`、`path:string(optional)`、`glob:string(optional)`、`case_sensitive:boolean(optional)`、`max_results:number(optional)`、`regex:boolean(optional)`、`language:string(optional; ast_search 实际 required)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:78-94`；运行时参数结构见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:35-50`。
- 返回结构：
  - 非空成功 -> `{"files":{path:{"cols":["line","col","text","func_start","func_end"],"rows":[...]}},"total":number,"truncated"?:bool,"hint"?:string}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:278-333`。
  - 空结果 -> `{"success":true,"data":[],"meta":{"count":0,"message":"no matches found"}}`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:256-276`。
- V2 实现：`text_search` 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:59-113`，`ast_search` 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_search.go:133-185`，分派见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:36-39`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:241-243`。

### 5.5 `lsp_structure`

- 参数签名：`action:string(required, enum=document_symbol|workspace_symbol|folding_range|semantic_tokens)`、`file_path:string(optional)`、`query:string(optional)`、`language:string(optional)`、`verbosity:string(optional, enum=compact|full)`、`max_results:number(optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:95-109`。
- 返回结构：
  - `document_symbol` -> `[]DocumentSymbol{name,detail?,kind,range,selectionRange,children?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol.go:212-219`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:961-979`。
  - `workspace_symbol` default compact -> `lspCompactList[lspCompactWorkspaceSymbol]{data,total,showing}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go:18-37`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go:75-127`；full -> `[]WorkspaceSymbolResult`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:89-92`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:160-187`。
  - `semantic_tokens` -> `SemanticTokensResult{resultId?,data?,decoded?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:546-563`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:839-867`。
  - `folding_range` -> `[]FoldingRange{startLine,startCharacter?,endLine,endCharacter?,kind?,collapsedText?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:567-574`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:869-887`。
- V2 实现：分派见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:41-46`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:244-246`；handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_core.go:839-979`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:160-187`。

### 5.6 `lsp_edit`

- 参数签名：`action:string(required, enum=rename|code_action|format|replace_range)`、`file_path:string(required)`、`line:number(optional)`、`column:number(optional)`、`patch:string(optional)`、`edits:object[](optional)`、`new_name:string(optional)`、`new_text:string(optional)`、`persist_to_disk:boolean(optional)`、`force:boolean(optional)`、`version:number(optional)`、`only:string[](optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:110-130`。
- 返回结构：
  - `rename` -> 成功 envelope `{success,status,message,applied,persisted,requires_apply,lsp_sync?,warning?}`；无变更时 `{success,action:"rename",status:"no_change",message:"no edits produced",applied:false,persisted:false,requires_apply:false}`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:56-137`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:226-246`。
  - `code_action` -> `[]CodeActionResult{codeAction?,command?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:41-48`、`go-agent-v2/pkg/toolsdk/lsp/protocol_ext_common.go:93-96`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:945-1000`。
  - `format` -> `[]TextEdit{range,newText}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol.go:298-305`，handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:1043-1074`。
  - `replace_range` 普通成功 -> 以 did_change success envelope 为基底，再注入 `matched_by`、`resolved_start_offset`、`resolved_end_offset`、`resolved_lsp_line`、`preview_start_line`、`preview_end_line`、`edit_context`，并可能追加 `func_start`、`func_end`、`func_body`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:345-512`、`go-agent-v2/pkg/toolsdk/lsp/replace_range_runtime.go:101-118`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:1233-1244`。
  - `replace_range` dry-run -> `{success:true,dry_run:true,replaced,replacement,replaced_len,replacement_len,func_start?,func_end?,func_body?}`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:447-472`。
  - `replace_range` 失败 -> `{"success":false,"error":...,"current_content":...,"func_start"?:number,"func_end"?:number}`，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:414-430`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:1248-1263`。
  - `replace_range` 多编辑 (`edits`) -> 走 `ReplaceRangeMulti`，最终仍返回 did_change success envelope，见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:703-746`。
- V2 实现：分派见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:48-53`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_dispatch.go:247-249`；handler 见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:56-163`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:202-246`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:345-746`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_edit.go:945-1074`。

### 5.7 `lsp_completion`

- 参数签名：`file_path:string(required)`、`line:number(required)`、`column:number(required)`、`verbosity:string(optional, enum=compact|full)`、`max_results:number(optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:131-141`。
- 返回结构：
  - compact default -> `lspCompactList[lspCompactCompletionItem]{data,total,showing}`，其中 item 为 `{label,kind?,detail?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go:18-28`、`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_compact.go:75-94`。
  - full -> `[]CompletionItem{label,kind?,detail?,documentation?,insertText?}`，定义见 `go-agent-v2/pkg/toolsdk/lsp/protocol.go:268-278`。
- V2 实现：`go-agent-v2/pkg/toolsdk/lsp/tool_handlers_p1_outputs.go:90-158`，注册见 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:131-141`。

### 5.8 `code_run`

- 参数签名：`mode:string(required, enum=run|project_cmd)`、`language:string(optional; run 模式实际需要)`、`code:string(optional)`、`command:string(optional)`、`auto_wrap:boolean(optional)`、`work_dir:string(optional)`、`timeout:number(optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/code_run.go:38-57`。
- 返回结构：
  - 成功 -> `CodeRunResult{success,output,exit_code,duration,language,mode,truncated}`，定义见 `go-agent-v2/pkg/toolsdk/tools/types_sdk.go:6-25`、序列化见 `go-agent-v2/pkg/toolsdk/tools/code_run.go:282-293`。
  - 失败 / 审批拒绝 -> `{"error":string,"exit_code":-1}`，见 `go-agent-v2/pkg/toolsdk/tools/code_run.go:93-99`、`go-agent-v2/pkg/toolsdk/tools/code_run.go:127-129`。
- V2 实现：`go-agent-v2/pkg/toolsdk/tools/code_run.go:33-57`，注册接入见 `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-171`。

### 5.9 `code_run_test`

- 参数签名：`test_func:string(required)`、`test_pkg:string(optional)`、`timeout:number(optional)`。schema 见 `go-agent-v2/pkg/toolsdk/tools/code_run.go:58-74`。
- 返回结构：
  - 成功 -> 同 `CodeRunResult{success,output,exit_code,duration,language,mode,truncated}`，定义见 `go-agent-v2/pkg/toolsdk/tools/types_sdk.go:17-25`，序列化见 `go-agent-v2/pkg/toolsdk/tools/code_run.go:282-293`。
  - 失败 -> `{"error":string,"exit_code":-1}`，见 `go-agent-v2/pkg/toolsdk/tools/code_run.go:151-157`。
- V2 实现：`go-agent-v2/pkg/toolsdk/tools/code_run.go:58-74`、`go-agent-v2/pkg/toolsdk/tools/code_run.go:135-158`，注册接入见 `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-171`。

## 6. 最终结论

- V2 当前实际 MCP LSP/RUN tool surface 是 9 个：`lsp_file`、`lsp_inspect`、`lsp_xref`、`lsp_grep`、`lsp_structure`、`lsp_edit`、`lsp_completion`、`code_run`、`code_run_test`，定义与注册链分别在 `go-agent-v2/pkg/toolsdk/tools/lsp_tools.go:33-168`、`go-agent-v2/pkg/toolsdk/tools/code_run.go:33-76`、`go-agent-v2/pkg/toolsdk/tooladapter/registry.go:94-213`。
- V3 当前 inspected paths 仍未进入 family 实现阶段；`cmd/mcp-lsp/*` 和 `internal/mcpserver/common/*` 还没有任何 LSP/RUN tool registry 或 manifest 实例，因此上述 9 个 tool 在 V3 当前代码面全部缺失，证据见 `cmd/mcp-lsp/fx.go:5-12`、`internal/mcpserver/common/server.go:14-21`、`internal/mcpserver/common/manifest.go:3-12`、`internal/mcpserver/common/stdio.go:1-3`。
