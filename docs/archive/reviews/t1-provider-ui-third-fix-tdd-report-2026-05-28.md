# t1 Provider UI third-fix TDD evidence report

- Date: 2026-05-28
- Workdir: `/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract`
- Scope: evidence-only third fix; no intentional production/test code changes beyond temporary mutation restored before GREEN.
- Report target: `docs/reviews/t1-provider-ui-third-fix-tdd-report-2026-05-28.md`

## Initial git status

```text
 M Makefile
 M cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts
 M cmd/agent-terminal/frontend/vue-app/provider-config-options.js
 M cmd/agent-terminal/frontend/vue-app/provider-config-options.test.js
 M cmd/agent-terminal/frontend/vue-app/provider-settings.behavior.test.js
 M cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js
 M cmd/agent-terminal/frontend/vue-app/thread-store-provider-preference.test.js
 M cmd/agent-terminal/frontend/vue-app/thread-store-skill-source.test.js
 M cmd/agent-terminal/frontend/vue-app/thread-store.actions.test.js
 M cmd/agent-terminal/main.go
 M cmd/mcp-ida/main.go
 M cmd/mcp-lsp/main.go
 M cmd/mcp-orch/main.go
 M cmd/mcp-orch/runtime.go
 M cmd/mcp-orch/runtime_test.go
 M docs/doc/codemap/README.md
 M docs/doc/codemap/ai-index.json
 M internal/app/app.go
 M internal/contract/config.go
 M internal/mcpserver/common/tool_error_envelope.go
 M internal/mcpserver/common/tool_error_envelope_test.go
 M internal/module/thread/lifecycle_helpers.go
 M internal/module/uistate/preferences.go
 M internal/module/uistate/preferences_runtime_contract_test.go
 M internal/module/uistate/rpc_test.go
 M internal/platform/config/config.go
 M internal/platform/config/config_test.go
 M internal/platform/db/module.go
 M internal/platform/rpc/server.go
 M internal/platform/rpc/server_minimal_test.go
 M internal/provider/codexapp/dream_executor.go
 M internal/provider/codexapp/dream_executor_test.go
 M internal/provider/codexapp/driver_pool_routing.go
 M internal/provider/codexapp/driver_pool_routing_test.go
 M internal/provider/codexapp/driver_session_test.go
 D internal/provider/codexapp/env_allowlist.go
 M internal/provider/codexapp/env_allowlist_test.go
 M internal/provider/codexapp/peer_supervisor.go
 M internal/provider/codexapp/pool_spawn_cmd.go
 M internal/provider/codexapp/pool_spawn_cmd_test.go
 M internal/provider/codexapp/pool_spawner.go
 M internal/provider/codexapp/support.go
 M internal/provider/codexapp/transport_local_test.go
 M internal/provider/codexapp/transport_process.go
 M internal/ui/wails/http_server.go
 M internal/ui/wails/http_server_test.go
?? cmd/agent-terminal/frontend/vue-app/stores/codex-lsp-defaults.js
?? cmd/agent-terminal/frontend/vue-app/thread-store-codex-default-home.test.js
?? docs/cc/
?? docs/packaging/
?? docs/reviews/
?? docs/superpowers/plans/2026-05-27-packaged-app-mvp-reset-plan.md
?? internal/app/desktop_preflight_test.go
?? internal/platform/db/embedded_postgres_lifecycle_test.go
?? internal/platform/embeddedpg/
?? internal/platform/runtimeenv/
?? internal/provider/codexapp/codex_autoinstall.go
?? internal/provider/codexapp/codex_autoinstall_test.go
?? internal/provider/codexapp/codex_bootstrap_test.go
?? internal/provider/codexapp/driver_pool_appmanaged_default_test.go
?? scripts/build_relocatable_postgres_macos.sh
?? scripts/package_linux.sh
?? scripts/package_macos.sh
?? scripts/package_macos_guard_test.go
?? scripts/verify_packaged_app_macos.sh
```

## Mutation setup

- Mutation file: `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- Backup file outside repo: `/tmp/t1-provider-ui-thread-actions-helpers.js.before-mutation`
- SHA-256 before mutation: `10ebaf6cc545f88f4576185252c4fabdcae4406f420846e415fd3da80885d29e`
- Reason: current code is already fixed, so the historical RED cannot be reproduced from the fixed tree. The mutation reintroduces the removed implicit packaged Codex home fallback so the regression test must fail.
- Mutation detail: replace the conditional Codex home forwarding line with an unconditional fallback to `/Users/ai/Library/Application Support/Super Dolphin/providers/codex` without adding effective lines, so vitest's size guard still reaches the target assertion.

- SHA-256 during mutation: `90fb42bec5e671c929351494f7b9ab4f36f5fdfc50ab4bf14379d79bd75003ff`

## Mutation RED: Codex default home regression

- Workdir: `/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract`
- Complete command: `cd cmd/agent-terminal/frontend && npx vitest run vue-app/thread-store-codex-default-home.test.js`

```text

 RUN  v3.2.4 /Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract/cmd/agent-terminal/frontend

🛡️  Running codebase size guard...
📏 size-guard: 302 文件 (生产 166, 测试 136)
✅ 体积守卫通过 — 无新增超限
 ❯ vue-app/thread-store-codex-default-home.test.js (1 test | 1 failed) 12ms
   × thread store Codex default home > does not forward a Codex home when no Codex home preference is set 11ms
     → expected { codexInstanceKey: 'default', …(4) } to not have property "codexHome"

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  vue-app/thread-store-codex-default-home.test.js > thread store Codex default home > does not forward a Codex home when no Codex home preference is set
AssertionError: expected { codexInstanceKey: 'default', …(4) } to not have property "codexHome"

[32m- Expected:[39m
undefined

[31m+ Received:[39m
"/Users/ai/Library/Application Support/Super Dolphin/providers/codex"

 ❯ vue-app/thread-store-codex-default-home.test.js:76:38
     74|       codexModelProvider: 'super-dolphin-relay',
     75|     }));
     76|     expect(startPayload?.config).not.toHaveProperty('codexHome');
       |                                      ^
     77|     expect(JSON.stringify(startPayload)).not.toContain('Library/Applic…
     78|   });

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/1]⎯


 Test Files  1 failed (1)
      Tests  1 failed (1)
   Start at  17:42:59
   Duration  728ms (transform 143ms, setup 0ms, collect 205ms, tests 12ms, environment 0ms, prepare 55ms)

```

- Exit code: `1`

## Mutation restore

- Restore command: `cp /tmp/t1-provider-ui-thread-actions-helpers.js.before-mutation cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- SHA-256 after restore: `10ebaf6cc545f88f4576185252c4fabdcae4406f420846e415fd3da80885d29e`
- Restored equals before mutation: `yes`

## GREEN target after restore

- Workdir: `/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract`
- Complete command: `cd cmd/agent-terminal/frontend && npx vitest run vue-app/thread-store-codex-default-home.test.js`

```text

 RUN  v3.2.4 /Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract/cmd/agent-terminal/frontend

🛡️  Running codebase size guard...
📏 size-guard: 302 文件 (生产 166, 测试 136)
✅ 体积守卫通过 — 无新增超限
 ✓ vue-app/thread-store-codex-default-home.test.js (1 test) 10ms

 Test Files  1 passed (1)
      Tests  1 passed (1)
   Start at  17:43:01
   Duration  650ms (transform 132ms, setup 0ms, collect 197ms, tests 10ms, environment 0ms, prepare 46ms)

```

- Exit code: `0`

## Required frontend size guard and vitest

- Workdir: `/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract`
- Complete command: `cd cmd/agent-terminal/frontend && node scripts/size-guard.cjs && npx vitest run vue-app/provider-settings.behavior.test.js vue-app/thread-store-provider-preference.test.js vue-app/thread-store.actions.test.js vue-app/thread-store-codex-default-home.test.js`

```text
📏 size-guard: 302 文件 (生产 166, 测试 136)
✅ 体积守卫通过 — 无新增超限

 RUN  v3.2.4 /Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract/cmd/agent-terminal/frontend

🛡️  Running codebase size guard...
📏 size-guard: 302 文件 (生产 166, 测试 136)
✅ 体积守卫通过 — 无新增超限
 ✓ vue-app/provider-settings.behavior.test.js (11 tests) 10ms
 ✓ vue-app/thread-store-codex-default-home.test.js (1 test) 10ms
 ✓ vue-app/thread-store-provider-preference.test.js (7 tests) 16ms
 ✓ vue-app/thread-store.actions.test.js (29 tests) 160ms

 Test Files  4 passed (4)
      Tests  48 passed (48)
   Start at  17:43:02
   Duration  891ms (transform 298ms, setup 0ms, collect 1.05s, tests 196ms, environment 1ms, prepare 205ms)

```

- Exit code: `0`

## Required Go test_with_guard

- Workdir: `/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract`
- Complete command: `./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/mcpserver/common -count=1`

```text
✅ 入口守卫: 未发现裸跑 go test 入口。
📏  文件≤600 函数≤80 嵌套≤4 CC≤10 下划线≤3 包文件≤30 包行≤10000
📊  生产 baseline 棘轮通过 — 81 个文件冻结中
📊  测试 baseline 棘轮通过 — 32 个文件冻结中
✅  代码守卫: 全部通过
# github.com/anthropic-ai/super-agent-v3/internal/archtest.test
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000020.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000021.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000022.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000023.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000024.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000025.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000026.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000027.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000028.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000029.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000030.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000031.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000032.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000033.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-2567061777/000034.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ok  	github.com/anthropic-ai/super-agent-v3/internal/archtest	1.774s
ok  	github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp	10.519s
ok  	github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common	0.673s
```

- Exit code: `0`

## Required make guard

- Workdir: `/Users/ai/Desktop/Super-Dolphin/.worktrees/t1-provider-ui-contract`
- Complete command: `make guard`

```text
./scripts/test_with_guard.sh --guard-only
✅ 入口守卫: 未发现裸跑 go test 入口。
📏  文件≤600 函数≤80 嵌套≤4 CC≤10 下划线≤3 包文件≤30 包行≤10000
📊  生产 baseline 棘轮通过 — 81 个文件冻结中
📊  测试 baseline 棘轮通过 — 32 个文件冻结中
✅  代码守卫: 全部通过
# github.com/anthropic-ai/super-agent-v3/internal/archtest.test
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000020.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000021.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000022.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000023.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000024.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000025.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000026.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000027.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000028.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000029.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000030.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000031.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000032.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000033.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ld: warning: object file (/private/var/folders/xl/33p_9r195yd4glcqfpbc0cv40000gn/T/go-link-1148980033/000034.o) was built for newer 'macOS' version (26.0) than being linked (11.0)
ok  	github.com/anthropic-ai/super-agent-v3/internal/archtest	1.592s
```

- Exit code: `0`

## Final git status

```text
 M Makefile
 M cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts
 M cmd/agent-terminal/frontend/vue-app/provider-config-options.js
 M cmd/agent-terminal/frontend/vue-app/provider-config-options.test.js
 M cmd/agent-terminal/frontend/vue-app/provider-settings.behavior.test.js
 M cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js
 M cmd/agent-terminal/frontend/vue-app/thread-store-provider-preference.test.js
 M cmd/agent-terminal/frontend/vue-app/thread-store-skill-source.test.js
 M cmd/agent-terminal/frontend/vue-app/thread-store.actions.test.js
 M cmd/agent-terminal/main.go
 M cmd/mcp-ida/main.go
 M cmd/mcp-lsp/main.go
 M cmd/mcp-orch/main.go
 M cmd/mcp-orch/runtime.go
 M cmd/mcp-orch/runtime_test.go
 M docs/doc/codemap/README.md
 M docs/doc/codemap/ai-index.json
 M internal/app/app.go
 M internal/contract/config.go
 M internal/mcpserver/common/tool_error_envelope.go
 M internal/mcpserver/common/tool_error_envelope_test.go
 M internal/module/thread/lifecycle_helpers.go
 M internal/module/uistate/preferences.go
 M internal/module/uistate/preferences_runtime_contract_test.go
 M internal/module/uistate/rpc_test.go
 M internal/platform/config/config.go
 M internal/platform/config/config_test.go
 M internal/platform/db/module.go
 M internal/platform/rpc/server.go
 M internal/platform/rpc/server_minimal_test.go
 M internal/provider/codexapp/dream_executor.go
 M internal/provider/codexapp/dream_executor_test.go
 M internal/provider/codexapp/driver_pool_routing.go
 M internal/provider/codexapp/driver_pool_routing_test.go
 M internal/provider/codexapp/driver_session_test.go
 D internal/provider/codexapp/env_allowlist.go
 M internal/provider/codexapp/env_allowlist_test.go
 M internal/provider/codexapp/peer_supervisor.go
 M internal/provider/codexapp/pool_spawn_cmd.go
 M internal/provider/codexapp/pool_spawn_cmd_test.go
 M internal/provider/codexapp/pool_spawner.go
 M internal/provider/codexapp/support.go
 M internal/provider/codexapp/transport_local_test.go
 M internal/provider/codexapp/transport_process.go
 M internal/ui/wails/http_server.go
 M internal/ui/wails/http_server_test.go
?? cmd/agent-terminal/frontend/vue-app/stores/codex-lsp-defaults.js
?? cmd/agent-terminal/frontend/vue-app/thread-store-codex-default-home.test.js
?? docs/cc/
?? docs/packaging/
?? docs/reviews/
?? docs/superpowers/plans/2026-05-27-packaged-app-mvp-reset-plan.md
?? internal/app/desktop_preflight_test.go
?? internal/platform/db/embedded_postgres_lifecycle_test.go
?? internal/platform/embeddedpg/
?? internal/platform/runtimeenv/
?? internal/provider/codexapp/codex_autoinstall.go
?? internal/provider/codexapp/codex_autoinstall_test.go
?? internal/provider/codexapp/codex_bootstrap_test.go
?? internal/provider/codexapp/driver_pool_appmanaged_default_test.go
?? scripts/build_relocatable_postgres_macos.sh
?? scripts/package_linux.sh
?? scripts/package_macos.sh
?? scripts/package_macos_guard_test.go
?? scripts/verify_packaged_app_macos.sh
```

## Exit-code summary

- Mutation RED expected non-zero; actual: `1`
- GREEN target expected zero; actual: `0`
- Required frontend command expected zero; actual: `0`
- Required Go command expected zero; actual: `0`
- Required make guard expected zero; actual: `0`
- Mutation file restored: `yes`
