# P2-04：Diagnostics / bootstrap / cache 保护

## 裁决

**MIGRATE TESTS；MIGRATE IDEAS；DO-NOT-MIGRATE v2 cache store。**

v3 的 scoped cache key 与 tombstone 方向正确；P2 只迁移 v2 在 stale refresh、DidChange/DidClose recovery、persistent cache 损坏处理、原子写、测试场景上的经验。

本子计划是通用 diagnostics/cache/bootstrap 保护，适用于所有 language adapters。Go go.work topology 只是 `LanguageSpecificHash` 的一个输入实例。

## 当前能力

v3 显式 URI diagnostics 已会在返回前 refresh：`cmd/mcp-lsp/multilsp/manager_diagnostics.go:70-108`。deleted cleanup 会清 diagnostics、cache tombstone、bootstrap state，见 `cmd/mcp-lsp/multilsp/manager_diagnostics.go:296-330`。

v3 cache key 已 scoped：`cmd/mcp-lsp/multilsp/cache.go:25-36`，string key 包含 scope/workspace/language/URI/hash：`cmd/mcp-lsp/multilsp/cache.go:433-453`。

## 当前差距

### P1：`diagnostics(all)` freshness 不足

显式 `uris` 会 refresh，但 `uris == nil` 时主要读取当前 diagnostic snapshots。P2 需要在 all diagnostics 前对当前 scope 已知 URI 做 budgeted preflight：existing file refresh，missing file cleanup。cache 只能作为 refresh/bootstrap candidate，不直接作为 diagnostics result。

### P1：DidChange/DidClose 不回写 bootstrap/cache 状态

v2 `DidChange` 失败后会 close+reopen，再失败则 restart client，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/lsp/manager_bootstrap_document.go:287-306` 和 `:483-498`。v3 当前 bootstrap update 失败会直接返回，缺少 close+reopen/restart 退路。

但 P2 必须精确限定状态回写边界：只有 full-document、disk-backed、已知完整文本的 change 能更新 fingerprint/size/modTime/persistent cache。range/incremental 或 memory-only change 只能标记 dirty/stale，不得写 persistent cache。

### P1：bootstrap state 卡死风险

`bootstrapping` wait 缺 TTL/lease。异常 panic、context cancel、client dead 后，需要允许后续调用 retry，而不是永久等待旧 channel。

### P1：persistent cache 损坏/过期保护不足

v3 persistent cache 当前是单 JSON disk state，整体 `os.WriteFile`，见 `cmd/mcp-lsp/multilsp/cache.go:398-412`。v2 是 per-record file store，并有删除坏 record、tmp rename、过期 cleanup 的经验。P2 不迁移 v2 per-record store，只迁移保护策略。

### P2：tombstone 重启防复活语义不明确

v3 tombstone 是内存 map，见 `cmd/mcp-lsp/multilsp/cache.go:183-194`。若 persistent cache 文件中仍有旧 record，重启后是否复活需要明确测试和设计。

### P2：persistent cache 默认启用策略未定义

P2 hardening 不自动改变默认启用策略。所有 persistent cache 测试必须显式使用 temp dir 与 `AGENT_LSP_CACHE_PERSISTENT=1` / `AGENT_LSP_CACHE_DIR=...`；是否默认开启留给后续 rollout 决策。

## 设计

### 1. diagnostics(all) preflight

流程：

1. 解析 caller scope。
2. 收集当前 scope candidates：
   - 当前 scope diagnostic snapshots。
   - `cache.ScopeDocuments(scope)` 中仍存在的 disk-backed URI。
   - bootstrap/cache `LastResolvedScope` index 只用于清理旧 scope，不作为 diagnostics result 来源。
3. 对候选 URI 做 bounded refresh：
   - existing regular file：`bootstrapDocument`。
   - missing/deleted：`cleanupDeletedDocument` + tombstone。
   - unsupported language：跳过。
4. wait stable。
5. 只返回当前 scope + 当前 language/workspace diagnostics。

### 2. DidChange / DidClose 状态回写

- full-document + disk-backed DidChange 成功：更新 version/fingerprint/size/modTime/bootstrap ready，可写 persistent cache。
- range/incremental DidChange：只更新 open version 或标记 dirty/stale，不得写 persistent cache。
- memory-only DidChange：不得写 persistent cache，除非后续确认落盘并重新读取完整文件。
- DidChange 失败：close+reopen；仍失败则 restart client。
- DidClose 成功：清 open/ready 状态；下次 bootstrap 必须 reopen。
- 普通 DidClose 不等于 delete；只有确认磁盘 missing/deleted 时才 tombstone。

### 3. Persistent cache 保护

保留 v3 单文件 scoped disk state：

- JSON 整体 corrupt：quarantine/delete 整个 state，并重写空 state。
- decoded document entry 无效/expired：过滤该 entry 后 tmp+rename 持久化。
- write：tmp + fsync/rename（至少 tmp + rename）。
- load 时验证 disk file exists + size/mtime/fingerprint。
- tombstone 策略：deleted 记录必须防止旧 persistent record 重启复活；可选短 TTL tombstone 持久化。
- 不改回 v2 的 workspaceID/language/uri per-record key。

### 4. server abnormal exit recovery

- transport/client failure：detach workspace client。
- advance diagnostic generation。
- 清旧 generation snapshot。
- 重建 client 后 restore bootstrapped workspace。
- 重建必须保留 languageID / LanguageSpecificHash，不得默认 Go。

## 实现步骤

1. `manager_diagnostics.go`：新增 diagnostics(all) scoped preflight。
2. `bootstrap_doc.go`：DidChange failure close+reopen/restart；DidClose state update。
3. `state.go`：bootstrapping TTL/lease retry。
4. `cache.go`：corrupt quarantine、tmp+rename、expired persist cleanup、tombstone restart policy。
5. `tool_diagnostics.go`：meta 增加 generation/stability/refresh count/timeout 信息。
6. 增加跨语言 fake-client cache/diagnostics matrix。

## 必要测试

- `TestDiagnosticsAllRefreshesStaleScopedDiagnosticBeforeReturn`
- `TestDiagnosticsAllBootstrapsUntrackedExistingDiagnosticURI`
- `TestDiagnosticsAllDeletedFileClearsDiagnosticsAndTombstones`
- `TestDidChangeAdvancesBootstrapCacheVersionForFullDiskBackedText`
- `TestIncrementalDidChangeDoesNotWritePersistentCache`
- `TestMemoryOnlyDidChangeDoesNotWritePersistentCache`
- `TestDidChangeFailureFallsBackToReopenThenRestart`
- `TestDidCloseClearsBootstrapReadyAndNextOpenReopens`
- `TestDidCloseDoesNotTombstoneExistingFile`
- `TestBootstrapBootstrappingTimeoutAllowsRetry`
- `TestPersistentCacheCorruptFileQuarantinedAndRewritten`
- `TestPersistentCacheWritesAtomically`
- `TestPersistentCacheExpiredEntryFilteredAndPersisted`
- `TestDeletedPersistentCacheRecordDoesNotResurrectAfterRestart`
- `TestDiagnosticsAfterServerExitDoesNotReturnOldGeneration`
- `TestCacheKeySeparatesLanguageIDAcrossSameURI`
- `TestDiagnosticsAllDoesNotReturnCrossLanguageSameURI`
- `TestDeletedPersistentCacheRecordDoesNotResurrectAcrossLanguages`
- `TestBootstrapCacheMatrixForRegisteredLanguageIDs`

## 验收命令

```bash
set -euo pipefail
required_tests=(
  './cmd/mcp-lsp/multilsp:TestDiagnosticsAllRefreshesStaleScopedDiagnosticBeforeReturn'
  './cmd/mcp-lsp/multilsp:TestDiagnosticsAllBootstrapsUntrackedExistingDiagnosticURI'
  './cmd/mcp-lsp/multilsp:TestDiagnosticsAllDeletedFileClearsDiagnosticsAndTombstones'
  './cmd/mcp-lsp/multilsp:TestDidChangeAdvancesBootstrapCacheVersionForFullDiskBackedText'
  './cmd/mcp-lsp/multilsp:TestIncrementalDidChangeDoesNotWritePersistentCache'
  './cmd/mcp-lsp/multilsp:TestMemoryOnlyDidChangeDoesNotWritePersistentCache'
  './cmd/mcp-lsp/multilsp:TestDidChangeFailureFallsBackToReopenThenRestart'
  './cmd/mcp-lsp/multilsp:TestDidCloseClearsBootstrapReadyAndNextOpenReopens'
  './cmd/mcp-lsp/multilsp:TestDidCloseDoesNotTombstoneExistingFile'
  './cmd/mcp-lsp/multilsp:TestBootstrapBootstrappingTimeoutAllowsRetry'
  './cmd/mcp-lsp/multilsp:TestPersistentCacheCorruptFileQuarantinedAndRewritten'
  './cmd/mcp-lsp/multilsp:TestPersistentCacheWritesAtomically'
  './cmd/mcp-lsp/multilsp:TestPersistentCacheExpiredEntryFilteredAndPersisted'
  './cmd/mcp-lsp/multilsp:TestDeletedPersistentCacheRecordDoesNotResurrectAfterRestart'
  './cmd/mcp-lsp/multilsp:TestDiagnosticsAfterServerExitDoesNotReturnOldGeneration'
  './cmd/mcp-lsp/multilsp:TestCacheKeySeparatesLanguageIDAcrossSameURI'
  './cmd/mcp-lsp/multilsp:TestDiagnosticsAllDoesNotReturnCrossLanguageSameURI'
  './cmd/mcp-lsp/multilsp:TestDeletedPersistentCacheRecordDoesNotResurrectAcrossLanguages'
  './cmd/mcp-lsp/multilsp:TestBootstrapCacheMatrixForRegisteredLanguageIDs'
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
