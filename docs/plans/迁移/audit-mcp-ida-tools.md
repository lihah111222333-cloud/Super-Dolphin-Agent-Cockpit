# MCP IDA 工具族完整审计

## 1. 审计边界与取证路径

- 本次仅使用 LSP 取证：`text_search`、`workspace_symbol`、`references`、`call_hierarchy`、`read_file`、`document_symbol`。
- 当前工作树里并不存在 `go-agent-v2/internal/mcp/ida/` 目录；V2 的 IDA 工具公开面实际由 `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:74-97`、`go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:28-135`、`go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:24-116`、`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:195-400` 定义。
- 你提到的 “mcp 在 go-agent-v2 根目录” 对应的是 `go-agent-v2/internal/mcp` 这层 runtime/transport，不是单独的 IDA tool 目录；它只负责把外部 runtime tools 注入注册器，见 `go-agent-v2/internal/mcp/runtime.go:374-376`。
- V2 真正的 runtime 注册链路是：
  - `go-agent-v2/internal/apiserver/server_dynamic_tools.go:38-48`
  - `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-171`
  - 其中 `appendCommonTools(...)` 会统一挂载 `LSP + CodeRun + Resource + Orchestration + IDA`。
- V2 forwarded IDA tool 的公开命名规则是 `ida_` + workerName，见 `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:338-345` 与 `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:505-553`。
- V2 forwarded IDA tool 的转发链路是：
  - schema/handler 组装：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:505-553`
  - 实际调用 worker control plane：`go-agent-v2/pkg/idamcp/provider.go:26-68`
- Frida 8 个管理工具是条件暴露，不是始终可见：
  - capability gate：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:239-247`
  - tool surface gate：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:391-400`
  - provider runtime gate：`go-agent-v2/pkg/idamcp/gateway.go:102-108`
  - 注册行为测试：`go-agent-v2/internal/apiserver/server_dynamic_tools_ida_test.go:125-138`、`go-agent-v2/internal/apiserver/server_dynamic_tools_ida_test.go:176-185`

## 2. 核心结论

- V2 的 IDA public surface 不是 P7 计划里写的 3 个占位能力，而是 4 大类共 82 个工具：
  - 管理/编排 22 个：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:74-97`
  - 静态分析 24 个（含 1 个兼容 helper）：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:101-187`
  - 静态修改 13 个：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:190-238`
  - 动态调试 23 个：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:241-297`
- V3 当前注册的 IDA tool 数量是 0。`cmd/mcp-ida` 只有一个空 `fx.New(fx.NopLogger)`，没有 `Provide`、没有 `Invoke`、没有任何 tool 注册，见 `cmd/mcp-ida/fx.go:5-12`、`cmd/mcp-ida/main.go:8-13`。
- V3 目前只会在 manifest 里宣告 `go-agent-mcp-ida` 这个 binary，而不是宣告任何 tool surface，见 `internal/dto/provider/manifest.go:30-45`。
- P7 计划对 `mcp-ida` 的定义严重失真。计划只写了 `ida/analyze`、`ida/decompile`、`ida/symbols` 三个占位名，见 `docs/plans/迁移/p7-execution-plan.md:80-89`；但 V2 真实公开名是 `ida_decompile`、`ida_disasm`、`ida_list_funcs`、`ida_imports`、`ida_export_funcs`、`ida_dbg_*`、`ida_frida_*` 等一整套离散工具，见 `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:74-297`。
- V2 公开名里没有独立的 `reverse_*`、`binary_*`、`frida_*` MCP name；Frida 也是挂在 `ida_frida_*` 下，forwarded tool 也统一 canonicalize 为 `ida_*`，见 `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:338-345` 与 `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:313-389`。

## 3. 重点能力面检查

| 能力面 | V2 实际覆盖 | 证据 | 结论 |
|---|---|---|---|
| 逆向分析 | `ida_decompile`、`ida_disasm`、`ida_basic_blocks`、`ida_callees`、`ida_callgraph`、`ida_xrefs_to`、`ida_xrefs_to_field`、`ida_find*`、`ida_stack_frame` | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:102-177` | 已覆盖，且远超 `ida/analyze` 单一占位名 |
| 符号/导入导出/字符串 | `ida_list_funcs`、`ida_lookup_funcs`、`ida_list_globals`、`ida_imports`、`ida_export_funcs`、`ida_find(scope=strings)`、`ida_get_string` | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:125-157` | 已覆盖，但不是单一 `ida_symbols` 工具，而是拆成多工具 |
| 内存操作 | 静态读：`ida_get_bytes`、`ida_get_int`；静态写：`ida_put_int`；动态读写：`ida_dbg_read`、`ida_dbg_write`；搜索：`ida_find_bytes` | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:129-161`、`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:223-227`、`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:278-285` | 已覆盖读/写/搜索，但没有单独命名为 `memory_*` |
| Frida 脚本 | `ida_frida_attach`、`ida_frida_spawn`、`ida_frida_resume`、`ida_frida_load_script`、`ida_frida_post`、`ida_frida_poll`、`ida_frida_unload`、`ida_frida_detach` | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:313-389` | 已覆盖 session + script control loop；没有独立 `frida_inject` / `frida_hook` / `frida_trace` 命名 |
| 二进制操作 | `ida_patch`、`ida_patch_asm`、`ida_define_func`、`ida_define_code`、`ida_undefine`、`ida_declare_type`、`ida_set_type` | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:190-238` | 已覆盖 patch/define/type，但没有单独的 public `dump/load` tool |
| 调试相关 | 网关侧 attach/server 管理：`ida_android_debug_*`；forwarded runtime debug：`ida_dbg_*` | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:24-116`、`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:241-297` | 已覆盖 breakpoint/step/registers/threads/segments/hw breakpoint |

## 4. 逐一对照表

V3 对应/状态的统一依据：

- `cmd/mcp-ida/fx.go:5-12` 只创建空 `fx.App`
- `cmd/mcp-ida/main.go:8-13` 只调用 `run()`
- `internal/dto/provider/manifest.go:30-45` 只宣告 binary，不宣告 tool

Forwarded 行的统一路由依据：

- tool 生成：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:505-553`
- provider 转发：`go-agent-v2/pkg/idamcp/provider.go:26-68`

### 4.1 管理 / 编排工具

| V2 Tool Name | V2 文件 | V3 对应 | 状态 |
|---|---|---|---|
| ida_ping | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:198`; `go-agent-v2/pkg/idamcp/provider.go:365` | 无 | ❌ |
| ida_fork | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:204`; `go-agent-v2/pkg/idamcp/provider.go:421` | 无 | ❌ |
| ida_shutdown | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:215`; `go-agent-v2/pkg/idamcp/provider.go:462` | 无 | ❌ |
| ida_changelog | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:221`; `go-agent-v2/pkg/idamcp/provider.go:363` | 无 | ❌ |
| ida_merge_export | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:227`; `go-agent-v2/pkg/idamcp/provider.go:470` | 无 | ❌ |
| ida_device_lease | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:30`; `go-agent-v2/pkg/idamcp/gateway_p3.go:20` | 无 | ❌ |
| ida_device_release | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:55`; `go-agent-v2/pkg/idamcp/gateway_p3.go:36` | 无 | ❌ |
| ida_adb_forward_open | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:73`; `go-agent-v2/pkg/idamcp/gateway_p3.go:51` | 无 | ❌ |
| ida_adb_forward_close | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:96`; `go-agent-v2/pkg/idamcp/gateway_p3.go:68` | 无 | ❌ |
| ida_adb_forward_list | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:116`; `go-agent-v2/pkg/idamcp/gateway_p3.go:85` | 无 | ❌ |
| ida_android_debug_server_start | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:26`; `go-agent-v2/pkg/idamcp/gateway_p4.go:19` | 无 | ❌ |
| ida_android_debug_server_stop | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:51`; `go-agent-v2/pkg/idamcp/gateway_p4.go:34` | 无 | ❌ |
| ida_android_debug_attach | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:69`; `go-agent-v2/pkg/idamcp/gateway_p4.go:49` | 无 | ❌ |
| ida_android_debug_detach | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:97`; `go-agent-v2/pkg/idamcp/gateway_p4.go:75` | 无 | ❌ |
| ida_frida_attach | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:315`; `go-agent-v2/pkg/idamcp/gateway_p5.go:26` | 无 | ❌ |
| ida_frida_spawn | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:327`; `go-agent-v2/pkg/idamcp/gateway_p5.go:41` | 无 | ❌ |
| ida_frida_resume | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:341`; `go-agent-v2/pkg/idamcp/gateway_p5.go:56` | 无 | ❌ |
| ida_frida_load_script | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:347`; `go-agent-v2/pkg/idamcp/gateway_p5.go:71` | 无 | ❌ |
| ida_frida_post | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:356`; `go-agent-v2/pkg/idamcp/gateway_p5.go:86` | 无 | ❌ |
| ida_frida_poll | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:366`; `go-agent-v2/pkg/idamcp/gateway_p5.go:101` | 无 | ❌ |
| ida_frida_unload | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:375`; `go-agent-v2/pkg/idamcp/gateway_p5.go:118` | 无 | ❌ |
| ida_frida_detach | `go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:382`; `go-agent-v2/pkg/idamcp/gateway_p5.go:133` | 无 | ❌ |

### 4.2 静态分析工具

| V2 Tool Name | V2 文件 | V3 对应 | 状态 |
|---|---|---|---|
| ida_decompile | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:102` | 无 | ❌ |
| ida_disasm | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:105` | 无 | ❌ |
| ida_basic_blocks | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:109` | 无 | ❌ |
| ida_callees | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:112` | 无 | ❌ |
| ida_callgraph | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:115` | 无 | ❌ |
| ida_xrefs_to | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:119` | 无 | ❌ |
| ida_xrefs_to_field | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:122` | 无 | ❌ |
| ida_find | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:125` | 无 | ❌ |
| ida_find_bytes | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:129` | 无 | ❌ |
| ida_find_regex | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:132` | 无 | ❌ |
| ida_list_funcs | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:135` | 无 | ❌ |
| ida_lookup_funcs | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:140` | 无 | ❌ |
| ida_list_globals | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:143` | 无 | ❌ |
| ida_imports | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:147` | 无 | ❌ |
| ida_export_funcs | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:151` | 无 | ❌ |
| ida_get_bytes | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:152` | 无 | ❌ |
| ida_get_string | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:155` | 无 | ❌ |
| ida_get_int | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:158` | 无 | ❌ |
| ida_get_global_value | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:162` | 无 | ❌ |
| ida_search_structs | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:165` | 无 | ❌ |
| ida_read_struct | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:168` | 无 | ❌ |
| ida_stack_frame | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:172` | 无 | ❌ |
| ida_py_eval | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:175` | 无 | ❌ |
| ida_int_convert | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:184` | 无 | ❌ |

### 4.3 静态修改工具

| V2 Tool Name | V2 文件 | V3 对应 | 状态 |
|---|---|---|---|
| ida_rename | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:191` | 无 | ❌ |
| ida_set_comments | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:194` | 无 | ❌ |
| ida_set_type | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:197` | 无 | ❌ |
| ida_declare_type | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:201` | 无 | ❌ |
| ida_infer_types | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:204` | 无 | ❌ |
| ida_patch | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:207` | 无 | ❌ |
| ida_patch_asm | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:210` | 无 | ❌ |
| ida_define_func | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:213` | 无 | ❌ |
| ida_define_code | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:216` | 无 | ❌ |
| ida_undefine | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:219` | 无 | ❌ |
| ida_put_int | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:223` | 无 | ❌ |
| ida_declare_stack | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:228` | 无 | ❌ |
| ida_delete_stack | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:234` | 无 | ❌ |

### 4.4 动态调试工具

| V2 Tool Name | V2 文件 | V3 对应 | 状态 |
|---|---|---|---|
| ida_dbg_start | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:242` | 无 | ❌ |
| ida_dbg_exit | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:245` | 无 | ❌ |
| ida_dbg_continue | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:246` | 无 | ❌ |
| ida_dbg_run_to | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:247` | 无 | ❌ |
| ida_dbg_step_into | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:250` | 无 | ❌ |
| ida_dbg_step_over | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:253` | 无 | ❌ |
| ida_dbg_bps | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:256` | 无 | ❌ |
| ida_dbg_add_bp | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:257` | 无 | ❌ |
| ida_dbg_delete_bp | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:260` | 无 | ❌ |
| ida_dbg_toggle_bp | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:263` | 无 | ❌ |
| ida_dbg_regs_all | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:266` | 无 | ❌ |
| ida_dbg_regs_remote | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:267` | 无 | ❌ |
| ida_dbg_regs | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:268` | 无 | ❌ |
| ida_dbg_gpregs_remote | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:269` | 无 | ❌ |
| ida_dbg_gpregs | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:270` | 无 | ❌ |
| ida_dbg_regs_named_remote | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:271` | 无 | ❌ |
| ida_dbg_regs_named | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:274` | 无 | ❌ |
| ida_dbg_stacktrace | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:277` | 无 | ❌ |
| ida_dbg_read | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:278` | 无 | ❌ |
| ida_dbg_write | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:282` | 无 | ❌ |
| ida_dbg_threads | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:286` | 无 | ❌ |
| ida_dbg_add_hw_bp | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:289` | 无 | ❌ |
| ida_dbg_segments | `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:294` | 无 | ❌ |

## 5. 缺失工具完整参数签名

结论先行：由于 V3 当前 `mcp-ida` 没有注册任何 IDA tool，所以下列 V2 全部签名都是 V3 缺失项；V3 缺失依据统一见 `cmd/mcp-ida/fx.go:5-12`。

### 5.1 管理 / 编排

- `ida_ping(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:198-201`
- `ida_fork(i64_path, count?, gui?)`，required=`i64_path`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:204-212`
- `ida_shutdown()`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:215-218`
- `ida_changelog()`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:221-224`
- `ida_merge_export(changelog_paths?, output_dir?, include_current?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:227-234`
- `ida_device_lease(serial, owner_agent, owner?, ttl_seconds?, mode?, abi?, root?, frida_ready?, ida_debug_ready?)`，required=`serial, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:30-42`
- `ida_device_release(serial, owner_agent)`，required=`serial, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:55-60`
- `ida_adb_forward_open(serial, purpose, host_port?, device_port, owner_agent, owner?, ttl_seconds?)`，required=`serial, purpose, device_port, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:73-83`
- `ida_adb_forward_close(serial, owner_agent, purpose?, host_port?)`，required=`serial, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:96-103`
- `ida_adb_forward_list(serial, owner_agent?)`，required=`serial`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p3.go:116-121`
- `ida_android_debug_server_start(serial, owner_agent, owner?, ttl_seconds?, abi?, root?, server_binary?, host_port?, device_port?)`，required=`serial, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:26-38`
- `ida_android_debug_server_stop(serial, owner_agent)`，required=`serial, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:51-56`
- `ida_android_debug_attach(serial, owner_agent, instance, pid, package_name?, owner?, ttl_seconds?, abi?, root?, server_binary?, host_port?, device_port?)`，required=`serial, owner_agent, instance, pid`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:69-84`
- `ida_android_debug_detach(serial, owner_agent)`，required=`serial, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p4.go:97-102`
- `ida_frida_attach(serial?, owner_agent, pid, owner?, ttl_seconds?, host_port?, device_port?, root?)`，required=`owner_agent, pid`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:315-326`
- `ida_frida_spawn(serial?, owner_agent, target, argv?, env?, owner?, ttl_seconds?, host_port?, device_port?, root?)`，required=`owner_agent, target`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:327-340`
- `ida_frida_resume(session_id, owner_agent)`，required=`session_id, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:341-346`
- `ida_frida_load_script(session_id, owner_agent, name?, source, trace_id?)`，required=`session_id, owner_agent, source`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:347-355`
- `ida_frida_post(session_id, script_id, owner_agent, trace_id?, payload, data?)`，required=`session_id, script_id, owner_agent, payload`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:356-365`
- `ida_frida_poll(session_id, owner_agent, trace_id?, cursor?, limit?)`，required=`session_id, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:366-374`
- `ida_frida_unload(session_id, script_id, owner_agent)`，required=`session_id, script_id, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:375-381`
- `ida_frida_detach(session_id, owner_agent)`，required=`session_id, owner_agent`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools_p5.go:382-387`

### 5.2 静态分析

以下所有 forwarded tool 都共同带有 `instance?`，来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:506-519`。

- `ida_decompile(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:102-104`
- `ida_disasm(instance?, queries, count?)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:105-108`
- `ida_basic_blocks(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:109-111`
- `ida_callees(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:112-114`
- `ida_callgraph(instance?, queries, depth?)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:115-118`
- `ida_xrefs_to(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:119-121`
- `ida_xrefs_to_field(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:122-124`
- `ida_find(instance?, pattern, scope?)`，required=`pattern`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:125-128`
- `ida_find_bytes(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:129-131`
- `ida_find_regex(instance?, pattern)`，required=`pattern`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:132-134`
- `ida_list_funcs(instance?, count?, filter?, offset?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:135-139`
- `ida_lookup_funcs(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:140-142`
- `ida_list_globals(instance?, count?, filter?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:143-146`
- `ida_imports(instance?, offset?, count?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:147-150`
- `ida_export_funcs(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:151`
- `ida_get_bytes(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:152-154`
- `ida_get_string(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:155-157`
- `ida_get_int(instance?, addr, size?)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:158-161`
- `ida_get_global_value(instance?, name)`，required=`name`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:162-164`
- `ida_search_structs(instance?, pattern)`，required=`pattern`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:165-167`
- `ida_read_struct(instance?, addr, struct_name)`，required=`addr, struct_name`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:168-171`
- `ida_stack_frame(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:172-174`
- `ida_py_eval(instance?, code)`，required=`code`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:175-177`
- `ida_int_convert(instance?, value)`，required=`value`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:183-186`

### 5.3 静态修改

以下所有 forwarded tool 都共同带有 `instance?`，来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:506-519`。

- `ida_rename(instance?, batch)`，required=`batch`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:191-193`
- `ida_set_comments(instance?, items)`，required=`items`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:194-196`
- `ida_set_type(instance?, addr, type_str)`，required=`addr, type_str`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:197-200`
- `ida_declare_type(instance?, declaration)`，required=`declaration`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:201-203`
- `ida_infer_types(instance?, queries)`，required=`queries`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:204-206`
- `ida_patch(instance?, patches)`，required=`patches`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:207-209`
- `ida_patch_asm(instance?, items)`，required=`items`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:210-212`
- `ida_define_func(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:213-215`
- `ida_define_code(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:216-218`
- `ida_undefine(instance?, addr, size?)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:219-222`
- `ida_put_int(instance?, addr, value, size?)`，required=`addr, value`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:223-227`
- `ida_declare_stack(instance?, func_addr, offset, name, type_str?)`，required=`func_addr, offset, name`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:228-233`
- `ida_delete_stack(instance?, func_addr, offset)`，required=`func_addr, offset`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:234-237`

### 5.4 动态调试

以下所有 forwarded tool 都共同带有 `instance?`，来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:506-519`。

- `ida_dbg_start(instance?, args?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:242-244`
- `ida_dbg_exit(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:245`
- `ida_dbg_continue(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:246`
- `ida_dbg_run_to(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:247-249`
- `ida_dbg_step_into(instance?, count?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:250-252`
- `ida_dbg_step_over(instance?, count?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:253-255`
- `ida_dbg_bps(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:256`
- `ida_dbg_add_bp(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:257-259`
- `ida_dbg_delete_bp(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:260-262`
- `ida_dbg_toggle_bp(instance?, addr)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:263-265`
- `ida_dbg_regs_all(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:266`
- `ida_dbg_regs_remote(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:267`
- `ida_dbg_regs(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:268`
- `ida_dbg_gpregs_remote(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:269`
- `ida_dbg_gpregs(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:270`
- `ida_dbg_regs_named_remote(instance?, names)`，required=`names`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:271-273`
- `ida_dbg_regs_named(instance?, names)`，required=`names`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:274-276`
- `ida_dbg_stacktrace(instance?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:277`
- `ida_dbg_read(instance?, addr, size)`，required=`addr, size`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:278-281`
- `ida_dbg_write(instance?, addr, bytes)`，required=`addr, bytes`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:282-285`
- `ida_dbg_threads(instance?, tid?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:286-288`
- `ida_dbg_add_hw_bp(instance?, addr, size?, type?)`，required=`addr`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:289-293`
- `ida_dbg_segments(instance?, filter?)`。来源：`go-agent-v2/pkg/toolsdk/tools/ida_tools.go:294-296`

## 6. 额外发现

### 6.1 P7 计划遗漏的不止是 IDA 全量 surface

- V2 runtime 注册的 MCP 工具族不止 `lsp / orch / ida` 三族；它还会一起注册 `code_run`、`code_run_test`、`task_*`、`command_*`、`prompt_*`、`shared_file_*`、`workspace_*`，见 `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-171`。
- `go-agent-v2/internal/mcp/runtime_test.go:14-39` 直接验证了 `task_create_dag`、`command_list`、`prompt_list`、`shared_file_read`、`workspace_list_runs` 会被 runtime 注册。
- `go-agent-v2/pkg/toolsdk/tools/code_run.go:33-76` 定义了 `code_run`、`code_run_test`。
- `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:45-292` 定义了 `task_create_dag`、`task_get_dag`、`task_update_node`、`task_start_node`、`command_list`、`command_get`、`prompt_list`、`prompt_get`、`shared_file_read`、`shared_file_write`、`workspace_create_run`、`workspace_get_run`、`workspace_list_runs`、`workspace_merge_run`、`workspace_abort_run`。
- P7 计划虽然在总目标里提了 “Skills runtime + workspace 工具层”，见 `docs/plans/迁移/p7-execution-plan.md:10-15`，但在 MCP server 章节只枚举了 `mcp-lsp / mcp-orch / mcp-ida`，并未把 `code_run` 和 resource 工具族显式纳入迁移清单，见 `docs/plans/迁移/p7-execution-plan.md:45-89`。

### 6.2 没有发现独立的 `skill_*` MCP runtime tool 族

- V2 runtime 注册列表只追加 `LSP + CodeRun + Resource + Orchestration + IDA`，没有单独的 `skill_*` tool family，见 `go-agent-v2/pkg/toolsdk/tooladapter/registry.go:164-171`。
- V2 的 skill 能力存在于 appserver/RPC 层，而不是 MCP runtime tool 层；例如 `skill/list`、`skill/write`、`skill/delete` 出现在 `go-agent-v2/internal/guards/rpc_golden_test.go:156-184`，但不在 `pkg/toolsdk/tooladapter` 的 runtime append 列表中。

## 7. 建议的迁移结论

- 如果 P7 继续按 `ida/analyze`、`ida/decompile`、`ida/symbols` 三个占位名推进，最终只会得到一个与 V2 完全不兼容的 mcp-ida binary，见 `docs/plans/迁移/p7-execution-plan.md:80-89` 对比 `go-agent-v2/pkg/toolsdk/tools/ida_tools.go:74-297`。
- 正确的迁移拆分应至少按 V2 真实 surface 的四层做：
- 正确的迁移拆分之一：gateway-local management，对应 `ida_ping`、`ida_fork`、`ida_*lease*`、`ida_android_debug_*`、`ida_frida_*`。
- 正确的迁移拆分之二：forwarded static analysis，对应 `ida_decompile` 到 `ida_int_convert`。
- 正确的迁移拆分之三：forwarded static modify，对应 `ida_rename` 到 `ida_delete_stack`。
- 正确的迁移拆分之四：forwarded dynamic debug，对应 `ida_dbg_start` 到 `ida_dbg_segments`。
- 若只做最小可用集，也必须明确它不是 “V2 完整兼容”，而只是 “V3 新设计的最小 IDA 子集”；当前 V3 甚至还没有做到这一步，因为 `cmd/mcp-ida` 仍是空壳，见 `cmd/mcp-ida/fx.go:5-12`。
