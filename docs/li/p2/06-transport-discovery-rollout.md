# P2-06：Transport / discovery / rollout 验证

## 裁决

**MIGRATE TESTS；MIGRATE IDEAS；DO-NOT-MIGRATE v2 transport。**

v3 peer supervisor、toolbridge、mcpcontrol 的控制面已经比 v2 成熟。P2 不删除 HTTP，不迁移 v2 transport；只补 stale discovery、well-formed invalid JSON-RPC request 继续服务、raw syntax/desync fail-closed、scope cleanup 链路与 rollout 验证。

## 当前差距

### P0：旧 peer discovery 地址可能污染 manifest

v3 `discoverPeers()` 只要 discovery 文件返回地址就收录，见 `internal/module/turn/manifest.go:41-50`。manifest 生成 direct peer HTTP URL 后会 `continue`，跳过 stdio command fallback，见 `internal/contract/manifest.go:37-45`。如果 peer 崩溃/重启失败但 discovery 文件未清理，manifest 可能持续输出死地址。

### P1：discovery 双包口径不明确

manifest reader 与 HTTP runner writer 可能通过不同 discovery 包进入：manifest 侧读 discovery，HTTP runners 写 discovery。P2 必须先统一 discovery 单一事实源，或明确一个包是 façade。否则 cleanup/health probe 修在 writer 包，manifest reader 仍可能读旧地址。

### P1：HTTP direct peer 缺健康校验与 scope 注入保证

HTTP 保留是 P1/P2 共同决策。direct peer URL 不包含 agent/thread path，见 `internal/contract/manifest.go:37-42`；HTTP handler 只从 request params 解 trusted scope。因此 direct peer HTTP 只有在上游继续注入 `_agentId/_threadId/_cwd` 时才具备 multi-agent isolation 语义。

测试预期必须写死：

- direct HTTP 带 `_agentId/_threadId/_cwd`：可进入 scoped path。
- direct HTTP 不带 trusted metadata：不得作为 multi-agent isolation PASS；只能 workspace-only fallback 或 structured scope-missing result，且文档/测试不得称其隔离通过。

### P1：raw JSON stream malformed input 会停止服务

v3 raw stdio decode 出错直接返回 error，见 `internal/mcpserver/common/stdio.go:122-127`；server read/handle error 会 stop，见 `internal/mcpserver/common/server.go:136-145`。v2 newline stdio 对 malformed line 是 log+continue，见 `/Users/mima0000/Desktop/wj/go-agent-v2/internal/mcp/stdio.go:59-70`。

P2 只能对 raw JSON stream 中可恢复的 malformed JSON-RPC message 返回 parse error 后继续；若 decoder 已失同步或 framed `Content-Length` 损坏，则必须关闭连接。

### P1：agent stop/archive scope cleanup 需要 production 链路验证

文档要求清 manager-key clones / diagnostics / bootstrap / cache，但现有 P1 测试更多模拟 resolver 当前态。P2 必须有 agent lifecycle -> mcp-lsp ReleaseScope 的集成测试。

### P1：rollout 缺少通用多语言门禁

P2 不能只跑 Go/gopls。rollout 必须覆盖 generic fake language service matrix，以及已有 multi-language registry/bootstrap/tool tests。真实 TS/Python/Rust/Java/gopls smoke 可作为 `lsp_integration` / `e2e` 补充，不得替代 mandatory fake tests。

### P2：rollout/rollback 文档缺 build 验证

P2 修改涉及 broad Go control-plane / LSP，最终 gate 除 `make test` 外应包含 `make build-plain`。

## 设计

### 1. Discovery fail-closed

规则：

- PeerSupervisor 启动 peer 前清理该 binary 的旧 discovery。
- peer process 退出时立即清理 discovery，再进入 restart backoff。
- restart failure 后保持 degraded，manifest 不得继续使用旧地址。
- `discoverPeers()` 读到地址后做短超时 health probe；失败则删除 discovery 并 fallback stdio command。
- discovery writer/reader 必须共享同一事实源，或 façade 覆盖两边。

### 2. HTTP retained but bounded

- HTTP MCP 保留；不作为删除对象。
- P2 不用删除 HTTP 作为回滚策略。
- direct HTTP peer 必须保留 trusted metadata 注入测试。
- toolbridge/proxy/manifest path 必须证明会向 direct HTTP peer 注入 `_agentId/_threadId/_cwd`，不能只测 `internal/mcpserver/common` 的裸 HTTP handler。
- HTTP server 增加基本 read/header timeout，避免慢请求拖住 peer。
- 无 trusted metadata 注入的 direct HTTP 不是 multi-agent isolation 验收路径。

### 3. Raw stdio malformed JSON 容错

- well-formed JSON 但 JSON-RPC invalid request：返回 JSON-RPC/tool error 并继续服务。
- raw JSON syntax error / decoder desync：关闭连接，除非实现了明确分隔与 resync 机制并有测试。
- framed `Content-Length` 损坏：关闭连接。

### 4. Rollout waves

```text
Wave A: generic language adapter matrix
Wave B: tools/list + structured error compatibility
Wave C: client health/restart + scope cleanup
Wave D: diagnostics/cache corruption/tombstone/restart
Wave E: Go adapter fault tolerance and non-Go pollution guard
Wave F: discovery/transport fail-closed
Wave G: integration smoke + make test + make build-plain
```

每个 wave 都必须先执行测试存在性校验；任一 `[no tests to run]` 或 required test skip 必须失败。

## 实现步骤

1. `internal/util/discovery` 与 `internal/platform/discovery` 统一事实源或 façade。
2. `internal/provider/codexapp/peer_supervisor.go`：peer lifecycle 清 discovery。
3. `internal/module/turn/manifest.go` / discovery package：短 health probe + stale cleanup + stdio fallback。
4. `internal/mcpserver/common/stdio.go`：区分 JSON-RPC invalid request、raw syntax/desync、framed malformed 三类容错。
5. `internal/mcpserver/common/http_transport.go`：read/header timeout。
6. toolbridge/proxy/manifest direct HTTP path：补 trusted metadata 注入验证。
7. agent lifecycle 到 mcp-lsp admin ReleaseScope 的生产链路。
8. `README.md` / `AGENTS.md` / codemap：把 `mcp-lsp` 入口描述从 gopls-only 改为 generic multi-language LSP peer。
9. `docs/li/p2` 与 P1 rollout 文档同步新增验证命令。

## 必要测试

- `TestManifestFallsBackToStdioWhenPeerDiscoveryStale`
- `TestDiscoverPeersDeletesStalePeerAddrAfterHealthProbeFailure`
- `TestPeerSupervisorClearsDiscoveryOnPeerExitBeforeRestart`
- `TestPeerSupervisorClearsDiscoveryOnRestartFailure`
- `TestHTTPRunnerDiscoveryIsReadableByManifestBuilder`
- `TestHTTPDirectPeerRequiresTrustedScopeMetadata`
- `TestHTTPDirectPeerWithoutTrustedMetadataIsNotMultiAgentIsolationPass`
- `TestToolbridgeHTTPPeerProxyInjectsTrustedScopeMetadata`
- `TestRawJSONRPCInvalidRequestReturnsErrorAndContinues`
- `TestRawJSONStreamSyntaxErrorClosesConnection`
- `TestFramedStdioMalformedFrameStopsConnection`
- `TestAgentLifecycleDispatchesLSPReleaseScopeAdminCall`
- `TestAgentStopTriggersLSPReleaseScope`
- `TestRollbackKeepsHTTPMCPCompatibility`
- `TestMultiLanguageLSPGateCoversRegisteredLanguages`

## Wave gate commands

### Wave A-E：子计划 gates

Wave A-E 分别执行 01-05 子计划自己的验收命令；这些 gate 必须先全部通过，不能由下面的 Wave F transport/discovery focused gate 替代。

- Wave A：执行 [01-generic-language-service-architecture.md](01-generic-language-service-architecture.md) 的验收命令。
- Wave B：执行 [02-tool-surface-error-compat.md](02-tool-surface-error-compat.md) 的验收命令。
- Wave C：执行 [03-manager-lifecycle-fault-tolerance.md](03-manager-lifecycle-fault-tolerance.md) 的验收命令。
- Wave D：执行 [04-diagnostics-cache-protection.md](04-diagnostics-cache-protection.md) 的验收命令。
- Wave E：执行 [05-go-workspace-fault-tolerance.md](05-go-workspace-fault-tolerance.md) 的验收命令。

### Wave F：transport/discovery focused gate

```bash
set -euo pipefail
required_tests=(
  './internal/module/turn:TestManifestFallsBackToStdioWhenPeerDiscoveryStale'
  './internal/platform/discovery:TestDiscoverPeersDeletesStalePeerAddrAfterHealthProbeFailure'
  './internal/provider/codexapp:TestPeerSupervisorClearsDiscoveryOnPeerExitBeforeRestart'
  './internal/provider/codexapp:TestPeerSupervisorClearsDiscoveryOnRestartFailure'
  './internal/module/turn:TestHTTPRunnerDiscoveryIsReadableByManifestBuilder'
  './internal/mcpserver/common:TestHTTPDirectPeerRequiresTrustedScopeMetadata'
  './internal/mcpserver/common:TestHTTPDirectPeerWithoutTrustedMetadataIsNotMultiAgentIsolationPass'
  './internal/platform/toolbridge:TestToolbridgeHTTPPeerProxyInjectsTrustedScopeMetadata'
  './internal/mcpserver/common:TestRawJSONRPCInvalidRequestReturnsErrorAndContinues'
  './internal/mcpserver/common:TestRawJSONStreamSyntaxErrorClosesConnection'
  './internal/mcpserver/common:TestFramedStdioMalformedFrameStopsConnection'
  './internal/platform/mcpcontrol:TestAgentLifecycleDispatchesLSPReleaseScopeAdminCall'
  './cmd/mcp-lsp/multilsp:TestAgentStopTriggersLSPReleaseScope'
  './internal/mcpserver/common:TestRollbackKeepsHTTPMCPCompatibility'
  './cmd/mcp-lsp/manager:TestMultiLanguageLSPGateCoversRegisteredLanguages'
)
run_required_test() {
  local pkg="$1"
  local test_name="$2"
  rg -n "func ${test_name}\(" "$pkg" --glob '*_test.go' >/dev/null
  if ! list_output="$(./scripts/test_with_guard.sh "$pkg" -list "^${test_name}$" 2>&1)"; then
    printf '%s\n' "$list_output"
    echo "P2 gate failed: go test -list failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$list_output"
  if ! printf '%s\n' "$list_output" | rg "^${test_name}$" >/dev/null; then
    echo "P2 gate failed: required test not in test binary: ${pkg}:${test_name}" >&2
    exit 1
  fi
  if ! output="$(./scripts/test_with_guard.sh "$pkg" -run "^${test_name}$" -count=1 -v 2>&1)"; then
    printf '%s\n' "$output"
    echo "P2 gate failed: test command failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$output"
  if printf '%s\n' "$output" | rg -i '\[?no tests to run\]?' >/dev/null; then
    echo "P2 gate failed: [no tests to run] for ${pkg}:${test_name}" >&2
    exit 1
  fi
  if printf '%s\n' "$output" | rg "^--- SKIP: ${test_name}(\b|/)" >/dev/null; then
    echo "P2 gate failed: required test skipped: ${pkg}:${test_name}" >&2
    exit 1
  fi
}
for item in "${required_tests[@]}"; do
  pkg="${item%%:*}"
  test_name="${item##*:}"
  run_required_test "$pkg" "$test_name"
done
```


Docs sync gate（实现完成后执行，独立于 Go test）：

```bash
set -euo pipefail
stale_pattern='mcp-lsp.*gopls|gopls.*mcp-lsp|gopls-only|gopls peer'
generic_pattern='mcp-lsp.*generic multi-language LSP peer|generic multi-language LSP peer'
if rg -n "$stale_pattern" README.md AGENTS.md docs/doc/codemap -g '*.md' -g '*.json'; then
  echo 'P2 docs sync failed: mcp-lsp is still described as gopls-only' >&2
  exit 1
fi
require_generic_doc() {
  local label="$1"
  shift
  if ! rg -n "$generic_pattern" "$@" -g '*.md' -g '*.json' >/dev/null; then
    echo "P2 docs sync failed: missing generic multi-language LSP peer wording in ${label}" >&2
    exit 1
  fi
}
require_generic_doc README.md README.md
require_generic_doc AGENTS.md AGENTS.md
require_generic_doc docs/doc/codemap docs/doc/codemap
```

### Wave G：通用 LSP / 多语言 smoke

Mandatory fake/generic tests。该 gate 必须与 P2-01 的 generic language service gate 等价执行；不能只复制一条宽正则 `go test` 命令。

```bash
set -euo pipefail
required_tests=(
  './cmd/mcp-lsp/multilsp:TestGenericLanguageServicesBootstrapCacheDiagnosticsMatrix'
  './cmd/mcp-lsp/multilsp:TestDeadClientRestartRebootstrapForRegisteredLanguageIDs'
  './cmd/mcp-lsp/multilsp:TestDiagnosticsAllNoCrossLanguageCacheLeak'
  './cmd/mcp-lsp/multilsp:TestCacheKeySeparatesLanguageIDAcrossSameURI'
  './cmd/mcp-lsp/tools:TestToolErrorEnvelopeLanguageAgnostic'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectNonGoLanguageAdapters'
  './cmd/mcp-lsp/multilsp:TestAdapterLanguageSpecificHashFeedsWorkspaceKey'
  './cmd/mcp-lsp/multilsp:TestLanguageAdapterRegistryOwnsRootEnvBootstrapPolicy'
  './cmd/mcp-lsp/multilsp:TestGenericManagerHasNoLanguageSpecificRootBranches'
  './cmd/mcp-lsp/multilsp:TestGenericLanguageServicesMatrixCoversGoJSTSPythonRustJavaCSS'
  './cmd/mcp-lsp/multilsp:TestNonLSPDocumentLanguagesUseCapabilityFallback'
)
run_required_test() {
  local pkg="$1"
  local test_name="$2"
  rg -n "func ${test_name}\(" "$pkg" --glob '*_test.go' >/dev/null
  if ! list_output="$(./scripts/test_with_guard.sh "$pkg" -list "^${test_name}$" 2>&1)"; then
    printf '%s\n' "$list_output"
    echo "P2 gate failed: go test -list failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$list_output"
  if ! printf '%s\n' "$list_output" | rg "^${test_name}$" >/dev/null; then
    echo "P2 gate failed: required test not in test binary: ${pkg}:${test_name}" >&2
    exit 1
  fi
  if ! output="$(./scripts/test_with_guard.sh "$pkg" -run "^${test_name}$" -count=1 -v 2>&1)"; then
    printf '%s\n' "$output"
    echo "P2 gate failed: test command failed for ${pkg}:${test_name}" >&2
    exit 1
  fi
  printf '%s\n' "$output"
  if printf '%s\n' "$output" | rg -i '\[?no tests to run\]?' >/dev/null; then
    echo "P2 gate failed: [no tests to run] for ${pkg}:${test_name}" >&2
    exit 1
  fi
  if printf '%s\n' "$output" | rg "^--- SKIP: ${test_name}(\b|/)" >/dev/null; then
    echo "P2 gate failed: required test skipped: ${pkg}:${test_name}" >&2
    exit 1
  fi
}
for item in "${required_tests[@]}"; do
  pkg="${item%%:*}"
  test_name="${item##*:}"
  run_required_test "$pkg" "$test_name"
done
```

Optional real language server smoke（可 skip，不能替代 mandatory gate）。每条 optional lane 也必须输出可解析的 `SKIPPED/PASSED/FAILED`；设置 `P2_OPTIONAL_SMOKE_HARD_FAIL=1` 时，`SKIPPED` 也会使命令失败。

`lsp_integration` lane（真实 gopls / 外部 language server preflight 后运行）与 `e2e` lane（当前 `TestMultiLanguageLSP_E2E` 位于 `//go:build e2e` 文件；不得用 `-tags lsp_integration` 声称覆盖它）：

```bash
set -euo pipefail
optional_smoke() {
  local pkg="$1"
  local test_name="$2"
  local tags="$3"
  local label="$4"
  local hard_fail="${P2_OPTIONAL_SMOKE_HARD_FAIL:-0}"

  if ! rg -n "func ${test_name}\(" "$pkg" --glob '*_test.go' >/dev/null; then
    echo "SKIPPED ${label}: ${test_name} source not found"
    [ "$hard_fail" = "1" ] && return 1 || return 0
  fi
  if ! list_output="$(./scripts/test_with_guard.sh "$pkg" -tags "$tags" -list "^${test_name}$" 2>&1)"; then
    printf '%s\n' "$list_output"
    echo "FAILED ${label}: go test -list failed for ${test_name}" >&2
    return 1
  fi
  printf '%s\n' "$list_output"
  if ! printf '%s\n' "$list_output" | rg "^${test_name}$" >/dev/null; then
    echo "SKIPPED ${label}: ${test_name} not in ${tags} test binary"
    [ "$hard_fail" = "1" ] && return 1 || return 0
  fi
  if ! output="$(./scripts/test_with_guard.sh "$pkg" -tags "$tags" -run "^${test_name}$" -count=1 -v 2>&1)"; then
    printf '%s\n' "$output"
    echo "FAILED ${label}: ${test_name}" >&2
    return 1
  fi
  printf '%s\n' "$output"
  if printf '%s\n' "$output" | rg -i '\[?no tests to run\]?' >/dev/null; then
    echo "FAILED ${label}: [no tests to run] for ${test_name}" >&2
    return 1
  fi
  if printf '%s\n' "$output" | rg "^--- SKIP: ${test_name}(\\b|/)" >/dev/null; then
    echo "SKIPPED ${label}: ${test_name} skipped by test"
    [ "$hard_fail" = "1" ] && return 1 || return 0
  fi
  echo "PASSED ${label}: ${test_name}"
}
# Preflight 示例：按 lane/CI 策略选择 SKIPPED 还是 hard-fail；不得让缺依赖静默 PASS。
missing_optional=0
for bin in gopls tsserver pyright rust-analyzer jdtls; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "SKIPPED preflight: missing optional language server: $bin"
    missing_optional=1
  fi
done
if [ "${P2_OPTIONAL_SMOKE_HARD_FAIL:-0}" = "1" ] && [ "$missing_optional" -ne 0 ]; then
  echo 'FAILED preflight: optional language server dependency missing' >&2
  exit 1
fi

optional_smoke ./cmd/mcp-lsp/manager TestGoWorkRealGoplsSmoke lsp_integration gopls
optional_smoke ./cmd/mcp-lsp/manager TestMultiLanguageLSP_E2E e2e multilang-e2e
```

Dependency lanes / CI policy：

- 默认本地/CI：只跑上面的 fake/generic mandatory gate，不依赖外部 language server。
- `lsp_integration` job：先 preflight `gopls`、`tsserver`、`pyright/pylsp`、`rust-analyzer`、`jdtls`；缺依赖时必须按 job/env 明确 `SKIPPED` 或 `FAILED`，不能静默 PASS。
- `e2e` job：只运行 `//go:build e2e` 文件中的端到端 smoke；不能用 `-tags lsp_integration` 声称覆盖 e2e-only 测试。
- optional smoke 结果必须在 rollout 报告中逐项标注 `SKIPPED/PASSED/FAILED`，且不得替代 Wave G mandatory fake/generic gate。

Final repo gates：

```bash
make test
make build-plain
```

## 回滚策略

- 每个 P2 子项独立提交，可单项 revert。
- 回滚不得删除 HTTP MCP。
- 若 client health/restart 误伤，先回退 dead-client auto-detach，保留 structured error 和 tests。
- 若本次 persistent-cache hardening 或启用入口出问题，回退到 env-gated、默认关闭；不得把 `AGENT_LSP_CACHE_PERSISTENT` 默认改为 `1`，memory scoped cache 保留。
- 若 discovery health probe 误判，回退 probe，但保留 peer exit 清 discovery。
