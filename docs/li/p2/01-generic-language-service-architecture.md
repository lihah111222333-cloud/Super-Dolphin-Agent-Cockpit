# P2-01：通用语言服务架构与 adapter 合同

## 裁决

**P0 / MUST**：P2 必须先声明 v3 是 generic language service，而不是 Go/gopls 专用 LSP。所有从 v2 借鉴的经验只能转化成 v3 通用不变量或 language adapter policy。

## 当前事实

v3 已具备多语言底座：运行时注册 Go、JS/TS、Python、CSS、Rust、Java；manager/cache/tool 接口整体是语言无关的。P2 的风险不是代码完全 Go-only，而是文档和测试如果只围绕 gopls，会让后续实现继续把 Go 特例写进通用层。

## LanguageAdapter 合同

P2 实施时必须收敛出显式 language adapter 合同；这是 P0 交付物，不是建议项。目标形态：

```go
type LanguageAdapter interface {
    LanguageIDs() []string
    ResolveRoot(ctx context.Context, scope ToolScope, target string) (ResolvedLanguageScope, error)
    ServerCommand(ctx context.Context, scope ResolvedLanguageScope) (ServerCommand, error)
    InitOptions(scope ResolvedLanguageScope) map[string]any
    EnvPolicy(scope ResolvedLanguageScope) []string
    BootstrapPolicy(scope ResolvedLanguageScope) BootstrapPolicy
    CacheKeyParts(scope ResolvedLanguageScope) map[string]string
    CapabilityPolicy() ToolCapabilityPolicy
}
```

通用 manager/cache/tool 层只消费 adapter 输出：

```go
type ResolvedLanguageScope struct {
    LanguageID            string
    WorkspaceRoot         string
    LanguageWorkspaceRoot string
    ProjectRoot           string
    RootKind              string
    LanguageSpecific      map[string]string
}
```


## P0 交付边界

- 新增/抽取 adapter registry，所有语言的 `ResolveRoot`、`EnvPolicy`、`BootstrapPolicy`、`CacheKeyParts`、`ServerCommand`、`InitOptions` 都由 adapter 输出。
- 通用 manager/cache/tool 层只能消费 adapter registry 的结果；不得继续在通用层新增 Go/JSTS/Java/Python/Rust/CSS 专属 root/env/bootstrap 分支。
- 已有 `shouldUseGoWorkspace` / JSTS / Java workspace 逻辑必须收敛为 adapter-owned policy；Go adapter 以外不得读取 `GOWORK`。
- 空 `languageID` 是待判定状态，不是 Go；只能由 adapter registry 通过文件扩展名、URI、root marker 或显式 `language_id` override 判定。

## 通用不变量

- 所有语言都必须进入相同的 trusted scope 派生链。
- `WorkspaceKey` 必须由通用字段 + adapter `LanguageSpecific` 组成。
- `LanguageSpecificHash` 不得只有 Go 语义。
- diagnostics/filter/cache/tombstone 必须同时按 scope、workspace、language、URI 隔离。
- client health/restart/eviction/ReleaseScope 不得默认回退到 Go。
- root/env/bootstrap policy 只能由 adapter 决定，不能由通用层读取 `GOWORK` 等语言专属环境变量。

## 语言矩阵

| Language | Root markers / policy | Env policy | Bootstrap policy | Cache-specific hash | Mandatory fake gate | Optional real smoke |
| --- | --- | --- | --- | --- | --- | --- |
| Go | `go.work` / `go.mod` / submodule / worktree | `GOWORK` only inside Go adapter | gopls open target/siblings | goWork/goMod/module/workspaceFolders hash | yes | gopls with `lsp_integration` |
| JavaScript | `package.json` / `jsconfig` / source fallback | node/tsserver env only inside JSTS adapter | first JS source open | project config/root hash | yes | tsserver with `lsp_integration` |
| TypeScript | `tsconfig` / `package.json` / source fallback | node/tsserver env only inside JSTS adapter | first TS source open | tsconfig/package/root hash | yes | tsserver with `lsp_integration` |
| Python | `pyproject.toml` / setup / venv / source fallback | interpreter/venv env only inside Python adapter | pyright/pylsp target open | interpreter/root/config hash | yes | pyright or pylsp smoke |
| Rust | `Cargo.toml` / workspace / source fallback | cargo/rustup env only inside Rust adapter | rust-analyzer target open | cargo workspace hash | yes | rust-analyzer smoke |
| Java | Maven/Gradle/source root | jdtls workspace env inside Java adapter | jdtls project bootstrap | build root hash | yes | jdtls smoke |
| CSS | file/project fallback | none by default | target open when CSS server is registered | project/root hash | yes | optional CSS server smoke |
| JSON/YAML/Markdown | file/project fallback; optional adapter only | none by default | file-only/fallback unless adapter registered | project/root hash if adapter exists | no; capability fallback test instead | optional |

## 必要测试

这些测试是 P2 通用语言服务 gate，不能被 Go-only integration 替代：

- `TestGenericLanguageServicesBootstrapCacheDiagnosticsMatrix`
- `TestDeadClientRestartRebootstrapForRegisteredLanguageIDs`
- `TestDiagnosticsAllNoCrossLanguageCacheLeak`
- `TestCacheKeySeparatesLanguageIDAcrossSameURI`
- `TestToolErrorEnvelopeLanguageAgnostic`
- `TestGOWORKDoesNotAffectNonGoLanguageAdapters`
- `TestAdapterLanguageSpecificHashFeedsWorkspaceKey`
- `TestLanguageAdapterRegistryOwnsRootEnvBootstrapPolicy`
- `TestGenericManagerHasNoLanguageSpecificRootBranches`
- `TestGenericLanguageServicesMatrixCoversGoJSTSPythonRustJavaCSS`
- `TestNonLSPDocumentLanguagesUseCapabilityFallback`

## 验收命令

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

## 不迁移 v2 的部分

- 不迁移 v2 的 per-record workspace/language/URI cache key。
- 不迁移 v2 hidden meta。
- 不迁移 v2 Go-root-only heuristic 为通用 root policy。
- v2 只作为测试与故障模式来源，不作为结构迁移对象。
