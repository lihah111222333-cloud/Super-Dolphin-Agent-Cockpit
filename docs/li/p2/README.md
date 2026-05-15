# P2：通用语言服务容错与 v2 差距选择性修补

## 目标

在 P1 已完成的多 agent LSP trusted scope / manager pool / diagnostics cache / Go workspace root 基础上，把 v3 的 `mcp-lsp` 收敛为 **通用语言服务（generic language service）**：同一套 tool surface、manager lifecycle、diagnostics/cache/bootstrap、transport/discovery 容错规则必须适用于 Go、JS/TS、Python、Rust、Java、CSS 等已注册语言。

P2 会参考 `/Users/mima0000/Desktop/wj/go-agent-v2` 的成熟经验，但不是迁回 v2，也不是把 v3 做成 Go/gopls 专用服务。Go workspace / GOWORK / go.work 只属于 Go language adapter；所有通用修补必须保持 language-agnostic。

## 总裁决

| 主题 | 裁决 | 原因 |
| --- | --- | --- |
| 成套迁移 v2 LSP 实现 | **DO-NOT-MIGRATE** | v3 的 trusted scope、scoped cache key、diagnostics tombstone、Go topology hash 已强于 v2。 |
| 建立 generic language adapter 合同 | **P0 / MUST** | 否则 P2 容易继续在 `multilsp` 中叠加 Go/JSTS/Java 特例，无法成为通用语言服务。 |
| 迁移 v2 工具名/错误体验 | **MIGRATE IDEAS** | v3 `tools/list` 暴露短名，v2/系统提示词使用 `lsp_*`；v2 的 structured error/hint 更可操作。 |
| 迁移 v2 client health/restart 思路 | **MIGRATE IDEAS** | v3 `Client` 无 health，已有 client transport 死亡后可能被复用；v2 有 `Running()` 与重建路径。 |
| 迁移 v2 diagnostics/cache 测试思路 | **MIGRATE TESTS；MIGRATE IDEAS；DO-NOT-MIGRATE v2 cache store** | v3 scoped cache 结构更好；只迁移 stale refresh、DidChange/DidClose recovery、persistent cache 损坏/原子写/tombstone 的测试与保护思想。 |
| 迁移 v2 Go root 简化 resolver | **MIGRATE NARROW FIXES；DO-NOT-MIGRATE v2 resolver** | v3 go.work/use-list/linked worktree/topology hash 更完整；只补 GOWORK=auto、破损 go.work、外部 GOWORK 冲突等 v3 原生窄修。 |
| 迁移 v2 transport 实现 | **MIGRATE TESTS；MIGRATE IDEAS；DO-NOT-MIGRATE v2 transport** | v3 peer supervisor/control-plane 更成熟；只迁移 stale discovery cleanup、well-formed invalid JSON-RPC 不中断服务、fail-closed 测试思想。 |

## 裁决词表

- **DO-NOT-MIGRATE**：不迁移 v2 的结构、存储格式、transport 实现或 manager/cache key 模型。
- **MIGRATE IDEAS**：只迁移故障处理思想、错误语义或用户体验原则，并重新实现为 v3 原生结构。
- **MIGRATE TESTS**：迁移/重建 v2 暴露过的测试场景与回归门禁，不迁移对应实现。
- **MIGRATE NARROW FIXES**：在 v3 现有架构内做窄修补，禁止借机回退到 v2 简化实现。

## 非目标

- 不删除、不弃用 HTTP MCP；P2 继续遵守 P1 的 HTTP 保留决策。
- 不恢复 v2 hidden `__tool_call_meta` 或从 tool arguments 派生 agent/thread/cwd。
- 不把 v2 cache key 结构搬到 v3；v3 必须保留 `ScopeKey/WorkspaceKey/LanguageSpecificHash`。
- 不引入 agentID-only、rootURI-only 的 manager/cache 隔离。
- 不把 fixed sleep diagnostics 等待策略照搬到 v3。
- 不把 GOWORK/go.work 规则放进通用 root/cache/manager 层。
- 不把真实外部 language server smoke 当作唯一门禁；必须有 fake/generic language-service 单测。

## 审查来源

本 P2 来自 5 个 v2 gap 审查 agent、2 个通用语言服务专项 agent 的只读复审。主 agent 裁决：采纳所有 “NO-GO as-is” 文档问题，修文后作为 P2 可执行规范。

| Agent 主题 | 采纳裁决 |
| --- | --- |
| tool surface / schema / UX | 增加 `lsp_*` 可见性、structured error、test presence gate、收窄 `force` 边界。 |
| scope / pool / manager | 补 ReleaseScope 跨进程接口、active lease/drain、shared peer 定义、dead-client retry 边界。 |
| diagnostics / cache / bootstrap | 补 full-document/disk-backed DidChange 边界、v3 单文件 scoped cache 语义、跨语言 cache 测试。 |
| Go workspace / gopls | 明确 Go adapter 限界，GOWORK 不得污染非 Go 语言。 |
| transport / rollout | 补 stale discovery fail-closed、raw JSON stream 容错、HTTP trusted metadata 预期、`make build-plain`。 |
| generic LS architecture | 新增 language adapter 合同与语言矩阵。 |
| generic LS implementation | 强制 fake/generic LS 矩阵，真实 language server smoke 仅作补充。 |

## 子计划

| 文档 | 主题 | 输出 |
| --- | --- | --- |
| [01-generic-language-service-architecture.md](01-generic-language-service-architecture.md) | 通用语言服务架构 | language adapter 合同、语言矩阵、通用不变量 |
| [02-tool-surface-error-compat.md](02-tool-surface-error-compat.md) | 工具 surface、schema、structured error | 兼容 `lsp_*` 暴露、错误 envelope、`force`、language routing |
| [03-manager-lifecycle-fault-tolerance.md](03-manager-lifecycle-fault-tolerance.md) | manager/pool/client 生命周期容错 | dead client 重建、scope cleanup、fail-closed fallback、clone eviction |
| [04-diagnostics-cache-protection.md](04-diagnostics-cache-protection.md) | diagnostics/cache/bootstrap 保护 | diagnostics(all) refresh、DidChange/DidClose 状态、cache corruption/atomic/tombstone |
| [05-go-workspace-fault-tolerance.md](05-go-workspace-fault-tolerance.md) | Go adapter / gopls 容错 | GOWORK auto、破损 go.work fallback、外部 GOWORK 冲突、隐藏目录过滤 |
| [06-transport-discovery-rollout.md](06-transport-discovery-rollout.md) | transport/discovery/rollout | stale peer discovery fail-closed、raw stdio 容错、HTTP 保留、验收矩阵 |

## 全局设计原则

### 1. P2 是通用语言服务修补

P2 manager/recycler/cache/tool 修补必须保持 language-service generic。Go-only 行为只允许存在于 Go adapter 子计划（P2-05）及对应 Go-specific helper 内。

禁止把以下 Go 语义写入通用层：

- `GOWORK` / `go.work` / `go.mod`。
- gopls-only workspace folders。
- Go module root / module hash。
- Go-only fallback languageID。

允许的通用层输入是 adapter 输出的 `ResolvedLanguageScope` / `RootOptions.LanguageSpecific` / `WorkspaceKey` / `LanguageID`。

通用层不得新增 `shouldUseGoWorkspace` / `shouldUseJSTSWorkspace` / `shouldUseJavaWorkspace` 这类语言专属分支；已有 Go/JSTS/Java 等 root/env/bootstrap 特例必须在 P2 中迁到 adapter registry。空 `languageID` 不得默认当作 Go，除非已经由 adapter registry 基于文件/URI/root 明确判定为 Go。

### 2. P2 继承 P1 trusted scope

所有补丁继续从服务端可信 `_agentId/_threadId/_callId/_cwd` 派生 scope。模型传入 `arguments.agent_id/cwd/session_id` 仍只能作为业务参数，不得覆盖 trusted scope。

### 3. v3 scoped cache 是事实来源

P2 不迁移 v2 的 cache key。v3 当前 cache key 已包含 `ScopeKey`、`WorkspaceKey`、`LanguageID`、`URI`、`LanguageSpecificHash`，见 `cmd/mcp-lsp/multilsp/cache.go:25-36` 与 `cmd/mcp-lsp/multilsp/cache.go:433-453`。

`LanguageSpecificHash` 是跨语言扩展点：Go go.work topology 只是一个实现。JS/TS、Python、Rust、Java、CSS 后续必须各自通过 adapter 输出 project/root/server-specific fingerprint，不得把 cache/fingerprint/tombstone 逻辑写死为 Go/gopls。

### 4. 容错必须 fail-closed

当无法确认 peer、scope、client、cache freshness 时，必须选择：

- 不跨 agent/thread/workspace fallback。
- 不跨 languageID 返回 cache/diagnostics。
- 不返回旧 generation diagnostics。
- 不复用疑似 dead client。
- 不读取疑似损坏或过期 cache。

### 5. 每个 fix 必须有锁定 bug 的测试

P2 不接受文档-only 修补。每个 P0/P1 fix 都要有同提交测试，且验收必须先证明测试存在。禁止 `[no tests to run]` 被当作 PASS。

## 通用测试存在性门禁

每个子文档列出的必要测试都必须先通过存在性校验，再运行测试。模板（`./pkg/path:TestName` 是占位示例；真实子计划必须使用 `./cmd/...` / `./internal/...` 等当前模块可执行本地包路径）：

```bash
set -euo pipefail
required_tests=(
  './pkg/path:TestName'
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

带 `lsp_integration` / `e2e` 的测试也必须使用仓库 wrapper（例如 `./scripts/test_with_guard.sh ./cmd/mcp-lsp/manager -tags lsp_integration ...`），除非文档明确标注为“附加 smoke，不作为完成门禁”。

禁止只用宽正则证明 gate 通过：`required_tests` 中每个测试必须使用可执行本地包路径（`./cmd/...` / `./internal/...`），先通过 `-list '^TestName$'` 证明进入当前 build-tag 下的 test binary，再用精确 `-run '^TestName$'` 执行。源码 `rg func Test...` 只能作为预检；任何 required test 只做源码存在性校验、未进入测试二进制、未精确执行、或输出 `--- SKIP: TestName`，均为 NO-GO。

### Dependency lanes

- 默认本地/CI lane：只运行 fake/generic mandatory gates，不依赖 `gopls`、`tsserver`、`pyright`、`rust-analyzer`、`jdtls` 等外部 language server。
- `lsp_integration` lane：先 preflight 外部二进制；缺依赖时必须按 job/env 明确记录 `SKIPPED` 或 `FAILED`，不能静默通过。
- `e2e` lane：只运行标记为 `//go:build e2e` 的端到端 smoke；不得和 `lsp_integration` 混用为同一条命令，除非测试文件同时声明两个 tag。
- optional real smoke 的报告必须输出 `SKIPPED/PASSED/FAILED`，且永远不能替代 fake/generic mandatory gate。

## 总体落地顺序

1. 建立 generic language adapter 合同与 fake language service 测试矩阵。
2. Tool surface / structured error：先修正模型可见的工具契约，避免继续空跑/错用。
3. Client health / manager lifecycle：避免 dead client、scope leak、错误 peer fallback。
4. Diagnostics/cache/bootstrap hardening：补 all refresh、DidChange/DidClose 状态、persistent cache 保护。
5. Go adapter fault tolerance：补 GOWORK 与破损 go.work 容错，不破坏其他语言 root/cache。
6. Transport/discovery/rollout：补 stale discovery fail-closed 与 raw stdio 容错，完善验收矩阵。

## 完成定义

- `mcp-lsp` 具备通用 language adapter 合同，Go/JS/TS/Python/Rust/Java/CSS 都有 fake/generic gate。
- `tools/list` 与系统提示词对齐，`lsp_*` 可见且调用 alias 不再只是隐藏兼容。
- LSP tool error 支持 machine-readable structured error/hint，timeout/panic/schema error 可恢复且语言无关。
- transport dead / client closed 后不复用旧 client；重建时保留 languageID、language-specific root 与 cache key。
- Agent/thread stop 能清理对应 scoped manager/cache/diagnostics/bootstrap，不影响其他 scope 或其他 languageID。
- `diagnostics(all)` 返回前刷新当前 scope 已知 URI；deleted/stale 不复活；同 URI 不跨语言泄漏。
- persistent cache 默认启用策略不变；启用时损坏会 quarantine/清理并重写，写入使用 tmp + rename，tombstone 能防重启复活。
- `GOWORK=auto`、破损 go.work、外部 GOWORK 冲突均只在 Go adapter 内有明确行为和测试。
- stale peer discovery 不导致 manifest 持续输出死 HTTP 地址；HTTP 保留但不是 P2 删除对象。
- 入口文档（至少 `README.md`、`AGENTS.md`、codemap）不再把 `mcp-lsp` 描述为 gopls-only，而是 generic multi-language LSP peer。
- 机械完成还要求所有子计划 `required_tests` gate、Wave A-E 子计划 gate、Wave F transport/discovery focused gate、Wave G mandatory fake/generic gate、docs sync gate、final `make test` 与 `make build-plain` 全部通过；任何 `[no tests to run]`、required test skip、build tag 未进入测试二进制、或 broad regex 未覆盖 required test 都是 NO-GO。
