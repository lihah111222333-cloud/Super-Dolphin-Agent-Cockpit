# P5 RPC 迁移方案审查 A：V2 方法覆盖完整性

## 结论

- 当前 V2 注册表共 151 个方法。
- 其中：覆盖 127，noop 23，遗漏 1。
- `129 个 V2 RPC 方法` 的表述不准确。
- 若按“全部注册方法”统计，真实总数是 151。
- 若排除当前就已经是 noop/stub 的 23 个占位方法，剩余可执行/有语义方法为 128。
- 这 128 个里仍有 1 个方法 `fuzzyFileSearch` 在现方案中没有明确归宿。

## 口径

- 统计对象：仅统计当前 V2 在 `s.methods[...]` 中注册出的 RPC 方法；`dashboard/*` 通过 `dashboard_bindings.go -> dashrpc.Register(...)` 间接写入，同样计入。
- `覆盖`：方案对该方法给出了明确归宿、合并去向、下沉去向或删除处置。
- `noop`：V2 当前实现本身就是 `noopHandler` / `stubHandler` 占位。
- `遗漏`：方案未给出明确归宿；本次仅发现 `fuzzyFileSearch`。

## 明细表

| # | V2 方法名 | V2 注册文件 | 方案归宿模块 | 状态（覆盖/遗漏/noop） |
|---|---|---|---|---|
| 1 | initialize | methods.go | platform/rpc | 覆盖 |
| 2 | fuzzyFileSearch | methods.go | 无 | 遗漏 |
| 3 | app/list | methods.go | module/skill | 覆盖 |
| 4 | command/exec | methods.go | platform/rpc | 覆盖 |
| 5 | approval/respond | methods.go | module/turn + platform/rpc | 覆盖 |
| 6 | log/list | methods.go | platform/rpc | 覆盖 |
| 7 | log/filters | methods.go | platform/rpc | 覆盖 |
| 8 | log/relay | methods.go | platform/rpc | 覆盖 |
| 9 | initialized | methods.go | platform/rpc/noop | noop |
| 10 | fuzzyFileSearch/sessionStart | methods.go | platform/rpc/noop | noop |
| 11 | fuzzyFileSearch/sessionUpdate | methods.go | platform/rpc/noop | noop |
| 12 | fuzzyFileSearch/sessionStop | methods.go | platform/rpc/noop | noop |
| 13 | feedback/upload | methods.go | platform/rpc/noop | noop |
| 14 | dashboard/agentStatus | dashboard_bindings.go | module/dashboard | 覆盖 |
| 15 | dashboard/dags | dashboard_bindings.go | module/dashboard | 覆盖 |
| 16 | dashboard/taskAcks | dashboard_bindings.go | module/dashboard | 覆盖 |
| 17 | dashboard/taskTraces | dashboard_bindings.go | module/dashboard | 覆盖 |
| 18 | dashboard/commandCards | dashboard_bindings.go | module/dashboard | 覆盖 |
| 19 | dashboard/prompts | dashboard_bindings.go | module/dashboard | 覆盖 |
| 20 | dashboard/sharedFiles | dashboard_bindings.go | module/dashboard | 覆盖 |
| 21 | dashboard/auditLogs | dashboard_bindings.go | module/dashboard | 覆盖 |
| 22 | dashboard/aiLogs | dashboard_bindings.go | module/dashboard | 覆盖 |
| 23 | dashboard/busLogs | dashboard_bindings.go | module/dashboard | 覆盖 |
| 24 | dashboard/skills | dashboard_bindings.go | module/dashboard | 覆盖 |
| 25 | dashboard/dagDetail | dashboard_bindings.go | module/dashboard | 覆盖 |
| 26 | agent.launch | methods_orchestration.go | module/orchestration | 覆盖 |
| 27 | agent.submit | methods_orchestration.go | module/orchestration | 覆盖 |
| 28 | agent.submitPrompt | methods_orchestration.go | module/orchestration | 覆盖 |
| 29 | agent.stop | methods_orchestration.go | module/orchestration | 覆盖 |
| 30 | agent.list | methods_orchestration.go | module/orchestration | 覆盖 |
| 31 | agent.getReport | methods_orchestration.go | module/orchestration | 覆盖 |
| 32 | agent.rememberReportRequest | methods_orchestration.go | module/orchestration | 覆盖 |
| 33 | agent.reportEvent | methods_orchestration.go | module/orchestration | 覆盖 |
| 34 | agent.getState | methods_orchestration.go | module/orchestration | 覆盖 |
| 35 | agent.saveSubAgent | methods_orchestration.go | module/orchestration | 覆盖 |
| 36 | agent.deleteSubAgent | methods_orchestration.go | module/orchestration | 覆盖 |
| 37 | agent.persistSubAgentBinding | methods_orchestration.go | module/orchestration | 覆盖 |
| 38 | thread/start | methods_thread_turn.go | module/thread | 覆盖 |
| 39 | thread/resume | methods_thread_turn.go | module/thread | 覆盖 |
| 40 | thread/recover | methods_thread_turn.go | module/thread | 覆盖 |
| 41 | thread/fork | methods_thread_turn.go | module/thread | 覆盖 |
| 42 | thread/archive | methods_thread_turn.go | module/thread | 覆盖 |
| 43 | thread/unarchive | methods_thread_turn.go | module/thread | 覆盖 |
| 44 | thread/delete | methods_thread_turn.go | module/thread | 覆盖 |
| 45 | thread/name/set | methods_thread_turn.go | module/thread | 覆盖 |
| 46 | thread/compact/start | methods_thread_turn.go | provider/unified | 覆盖 |
| 47 | thread/rollback | methods_thread_turn.go | module/thread | 覆盖 |
| 48 | thread/list | methods_thread_turn.go | module/thread | 覆盖 |
| 49 | thread/loaded/list | methods_thread_turn.go | module/thread（并入 thread/list） | 覆盖 |
| 50 | thread/read | methods_thread_turn.go | module/thread | 覆盖 |
| 51 | thread/resolve | methods_thread_turn.go | module/thread（并入 thread/read,list） | 覆盖 |
| 52 | thread/config/get | methods_thread_turn.go | module/thread | 覆盖 |
| 53 | thread/config/set | methods_thread_turn.go | module/thread | 覆盖 |
| 54 | thread/messages | methods_thread_turn.go | module/thread | 覆盖 |
| 55 | thread/backgroundTerminals/clean | methods_thread_turn.go | provider/unified | 覆盖 |
| 56 | turn/start | methods_thread_turn.go | module/turn | 覆盖 |
| 57 | turn/steer | methods_thread_turn.go | module/turn | 覆盖 |
| 58 | turn/interrupt | methods_thread_turn.go | module/turn | 覆盖 |
| 59 | turn/forceComplete | methods_thread_turn.go | module/turn | 覆盖 |
| 60 | thread/realtime/start | methods_thread_turn.go | provider/unified | 覆盖 |
| 61 | thread/realtime/appendAudio | methods_thread_turn.go | provider/unified | 覆盖 |
| 62 | thread/realtime/appendText | methods_thread_turn.go | provider/unified | 覆盖 |
| 63 | thread/realtime/stop | methods_thread_turn.go | provider/unified | 覆盖 |
| 64 | review/start | methods_thread_turn.go | module/turn | 覆盖 |
| 65 | thread/undo | methods_thread_turn.go | provider/unified | 覆盖 |
| 66 | thread/model/set | methods_thread_turn.go | module/thread（并入 thread/config/set） | 覆盖 |
| 67 | thread/personality/set | methods_thread_turn.go | module/thread（并入 thread/config/set） | 覆盖 |
| 68 | thread/approvals/set | methods_thread_turn.go | module/thread（并入 thread/config/set） | 覆盖 |
| 69 | thread/mcp/list | methods_thread_turn.go | tool/registry / provider/unified | 覆盖 |
| 70 | thread/skills/list | methods_thread_turn.go | module/skill | 覆盖 |
| 71 | thread/debugMemory | methods_thread_turn.go | 删除（无公共去向） | 覆盖 |
| 72 | mock/experimentalMethod | methods_thread_turn.go | platform/rpc/noop | noop |
| 73 | skills/list | methods.go | module/skill | 覆盖 |
| 74 | skills/local/read | methods.go | module/skill | 覆盖 |
| 75 | skills/local/listFiles | methods.go | module/skill | 覆盖 |
| 76 | skills/local/write | methods.go | module/skill | 覆盖 |
| 77 | skills/local/importDir | methods.go | module/skill | 覆盖 |
| 78 | skills/local/delete | methods.go | module/skill | 覆盖 |
| 79 | skills/remote/list | methods.go | module/skill | 覆盖 |
| 80 | skills/remote/export | methods.go | module/skill | 覆盖 |
| 81 | skills/remote/read | methods.go | module/skill | 覆盖 |
| 82 | skills/remote/write | methods.go | module/skill | 覆盖 |
| 83 | skills/config/read | methods.go | module/skill | 覆盖 |
| 84 | skills/config/write | methods.go | module/skill | 覆盖 |
| 85 | skills/summary/write | methods.go | module/skill | 覆盖 |
| 86 | skills/match/preview | methods.go | module/skill | 覆盖 |
| 87 | model/list | methods.go | platform/rpc | 覆盖 |
| 88 | collaborationMode/list | methods.go | platform/rpc | 覆盖 |
| 89 | experimentalFeature/list | methods.go | platform/rpc | 覆盖 |
| 90 | config/read | methods.go | platform/rpc | 覆盖 |
| 91 | externalAgentConfig/detect | methods.go | platform/rpc | 覆盖 |
| 92 | externalAgentConfig/import | methods.go | platform/rpc | 覆盖 |
| 93 | config/value/write | methods.go | platform/rpc | 覆盖 |
| 94 | config/batchWrite | methods.go | platform/rpc | 覆盖 |
| 95 | config/lspPromptHint/read | methods.go | platform/rpc | 覆盖 |
| 96 | config/lspPromptHint/write | methods.go | platform/rpc | 覆盖 |
| 97 | configRequirements/read | methods.go | platform/rpc | 覆盖 |
| 98 | account/login/start | methods.go | platform/rpc | 覆盖 |
| 99 | account/login/cancel | methods.go | platform/rpc | 覆盖 |
| 100 | account/logout | methods.go | platform/rpc | 覆盖 |
| 101 | account/read | methods.go | platform/rpc | 覆盖 |
| 102 | account/rateLimits/read | methods.go | platform/rpc | 覆盖 |
| 103 | mcpServer/oauth/login | methods.go | platform/rpc | 覆盖 |
| 104 | config/mcpServer/reload | methods.go | platform/rpc | 覆盖 |
| 105 | mcpServerStatus/list | methods.go | platform/rpc | 覆盖 |
| 106 | windowsSandbox/setupStart | methods.go | platform/rpc | 覆盖 |
| 107 | lsp_diagnostics_query | methods.go | platform/rpc | 覆盖 |
| 108 | workspace/run/create | methods.go | module/workspace | 覆盖 |
| 109 | workspace/run/get | methods.go | module/workspace | 覆盖 |
| 110 | workspace/run/list | methods.go | module/workspace | 覆盖 |
| 111 | workspace/run/merge | methods.go | module/workspace | 覆盖 |
| 112 | workspace/run/abort | methods.go | module/workspace | 覆盖 |
| 113 | ui/preferences/get | methods.go | module/uistate | 覆盖 |
| 114 | ui/preferences/set | methods.go | module/uistate | 覆盖 |
| 115 | ui/preferences/getAll | methods.go | module/uistate | 覆盖 |
| 116 | ui/projects/get | methods.go | module/uistate | 覆盖 |
| 117 | ui/projects/add | methods.go | module/uistate | 覆盖 |
| 118 | ui/projects/remove | methods.go | module/uistate | 覆盖 |
| 119 | ui/projects/setActive | methods.go | module/uistate | 覆盖 |
| 120 | ui/code/open | methods.go | module/uistate + ui/dashboard | 覆盖 |
| 121 | ui/code/locate | methods.go | module/uistate + ui/dashboard | 覆盖 |
| 122 | ui/code/save | methods.go | module/uistate + ui/dashboard | 覆盖 |
| 123 | ui/dashboard/get | methods.go | module/uistate + module/dashboard | 覆盖 |
| 124 | ui/state/get | methods.go | module/uistate | 覆盖 |
| 125 | ui/sidebar/get | methods.go | module/uistate | 覆盖 |
| 126 | lsp/gui_file | methods.go | module/uistate | 覆盖 |
| 127 | lsp/gui_grep | methods.go | module/uistate | 覆盖 |
| 128 | lsp/gui_structure | methods.go | module/uistate | 覆盖 |
| 129 | lsp/gui_inspect | methods.go | module/uistate | 覆盖 |
| 130 | lsp/gui_xref | methods.go | module/uistate | 覆盖 |
| 131 | ui/log | methods.go | platform/rpc | 覆盖 |
| 132 | debug/runtime | methods.go | platform/rpc/debug | 覆盖 |
| 133 | debug/gc | methods.go | platform/rpc/debug | 覆盖 |
| 134 | ml-interceptor/status | methods.go | platform/rpc/debug | 覆盖 |
| 135 | workspace-root-options | methods.go | platform/rpc/noop | noop |
| 136 | agent-home | methods.go | platform/rpc/noop | noop |
| 137 | git-origins | methods.go | platform/rpc/noop | noop |
| 138 | mcp-servers | methods.go | platform/rpc/noop | noop |
| 139 | platform-info | methods.go | platform/rpc/noop | noop |
| 140 | open-in-targets | methods.go | platform/rpc/noop | noop |
| 141 | agent-agents-md | methods.go | platform/rpc/noop | noop |
| 142 | local-environments/list | methods.go | platform/rpc/noop | noop |
| 143 | worktrees/list | methods.go | platform/rpc/noop | noop |
| 144 | tasks/list | methods.go | platform/rpc/noop | noop |
| 145 | tasks/get | methods.go | platform/rpc/noop | noop |
| 146 | inbox-items | methods.go | platform/rpc/noop | noop |
| 147 | inbox-items/get | methods.go | platform/rpc/noop | noop |
| 148 | pending-automation-runs | methods.go | platform/rpc/noop | noop |
| 149 | mcp/status | methods.go | platform/rpc/noop | noop |
| 150 | config/read-all | methods.go | platform/rpc/noop | noop |
| 151 | diff/get | methods.go | platform/rpc/noop | noop |

## 关键证据

- `docs/plans/迁移/v3-migration-plan.md:1176-1220`：P5 明确要求统一注册表与 handler 分组。
- `docs/plans/迁移/v3-migration-plan.md:1459-1503`：给出 `dashboard_bindings.go`、`methods*.go`、`workspace_methods.go` 等文件级归宿。
- `docs/plans/迁移/v3-module-migration-details.md:45-79`：给出 thread/turn 相关方法的保留、合并、下沉、删除处置。
- `docs/plans/迁移/v3-module-migration-details.md:138-139`、`204-205`、`340`、`400`、`576-577`：分别约束 turn、skill、workspace、uistate、dashboard 的 RPC 责任边界。
- `docs/plans/迁移/p4-wave3-deep-audit.md:21`、`208`：明确指出 `(*Adapter).FuzzyFileSearch` 当前未发现 V3 归宿。

## 备注

- 直接包含注册语句或注册调用的文件只有 `methods.go`、`methods_thread_turn.go`、`methods_orchestration.go`、`dashboard_bindings.go`；其余指定文件主要用于确认 handler 语义与方案归宿。
- 方案中的 `129` 更像是“151 总注册项 - 22 个明显 noop/stub 占位项”的历史口径；该口径没有扣除 `mock/experimentalMethod` 这个额外 stub，也没有反映 `fuzzyFileSearch` 的缺口。
