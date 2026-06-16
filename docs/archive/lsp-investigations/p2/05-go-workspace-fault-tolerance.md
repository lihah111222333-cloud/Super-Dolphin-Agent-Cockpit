# P2-05：Go adapter / gopls fault tolerance

## 裁决

**MIGRATE NARROW FIXES；DO-NOT-MIGRATE v2 resolver。**

v3 的 Go root resolver 已支持 go.work use list、GOWORK=off、linked worktree 防碰撞、language-specific topology hash。不要迁回 v2 的简化 root heuristic。P2-05 只负责 Go root/GOWORK/go.work/submodule/worktree 语义；persistent cache 原子写、tombstone、generic diagnostics/cache 保护由 P2-04 统一负责。

## Go adapter 边界

P2 是通用多语言 LSP 服务修补；本文件只是 Go language adapter lane。GOWORK/go.work 逻辑只能进入：

- `shouldUseGoWorkspace`
- `ResolveGoRoot`
- `registryGoScope`
- `workspaceConfigForGoRoot`
- Go adapter `LanguageSpecific`

不得影响 JS/TS、Java、Python、Rust、CSS、JSON、YAML、Markdown 的 root resolver、manager key、cache key、env policy。空 `languageID` 不得作为 Go adapter 入口；只有 adapter registry 基于 `.go` 文件、Go root marker 或显式 `language_id=go` 判定后，才能进入 Go root/GOWORK 逻辑。

## 当前差距

### P1：`GOWORK=auto` 误判

v3 `resolveGoRootFromGOWORK` 对非空 GOWORK 当显式路径处理，见 `cmd/mcp-lsp/multilsp/go_root_resolver.go:101-123`。Go 工具链中 `GOWORK=auto` 表示自动发现；P2 应把 `auto` 等同 unset。

### P1：破损 go.work 阻断 gopls

v3 `resolveGoWorkRoot` 解析 go.work use list 失败会返回错误，见 `cmd/mcp-lsp/multilsp/go_root_resolver.go:181-190`。v2 只根据 root 是否存在 go.work/go.mod 做轻量判断，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/lsp/gomod_root.go:34-36`。P2 应 soft fallback 到 go.work 所在 root，让 gopls 自己报告 workspace 诊断，而不是 manager 无法启动。

### P1：外部 `GOWORK=/other/go.work` root 污染风险

v3 多入口会使用 ambient `os.Environ()`，见 `cmd/mcp-lsp/multilsp/go_root_resolver.go:95-99`。如果外部 GOWORK 不包含当前 target/projectRoot，可能把 gopls root 导向无关 workspace。

### P2：一级子模块扫描未过滤隐藏/下划线目录

v3 `findFirstLevelGoModRoots` 会扫描所有一级目录，见 `cmd/mcp-lsp/multilsp/go_root_resolver.go:237-260`。v2 跳过 `.` / `_` 前缀目录，见 `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/lsp/gomod_root.go:45-55`。

### P2：真实 gopls integration 仍不足

P1 的 `go_work_e2e_test.go` 使用 fake client；P2 可新增真实 gopls smoke，但不能把真实 gopls smoke 当唯一门禁。fake Go adapter 单测必须必跑；真实 gopls 测试用 `lsp_integration` build tag，缺少 gopls 时按文档选择 skip 或 CI hard fail。

## v3 保留能力

P2 不得破坏：

- `GOWORK=off` 行为。
- go.work use-list workspaceFolders。
- linked worktree physical path 防碰撞。
- workspace key 中的 `goModPath/goWorkPath/goworkMode/moduleRoot/moduleRootsHash/workspaceFoldersHash`，见 `cmd/mcp-lsp/multilsp/go_root_resolver.go:430-438`。
- scoped cache key 的 `LanguageSpecificHash`。
- 非 Go adapter 不读取 GOWORK。

## 设计

### 1. GOWORK normalization

规则：

```text
GOWORK unset or empty -> auto discovery
GOWORK=auto -> auto discovery
GOWORK=off -> ignore go.work, use go.mod/submodule resolver
GOWORK=/path/to/go.work -> explicit mode; must exist and include current target/project root, or conflict
```

### 2. Broken go.work soft fallback

当 `parseGoWorkModuleRoots` 失败：

- `RootKind=go_work`
- `WorkspaceRoot=dir(go.work)`
- `GoWorkPath=path`
- `GOWORKMode=auto/explicit`
- `ModuleRoots=nil`
- `LanguageSpecific` 中包含 parse error marker（可选）
- 不阻断 gopls client 创建；错误交给 diagnostics 返回。

### 3. External GOWORK conflict

若显式 GOWORK 不包含当前 target/projectRoot：

- 默认 fail-closed，返回明确错误：`GOWORK path ... does not contain target ...`。
- 或在配置允许时忽略 ambient GOWORK 并进入 auto discovery；必须有日志与测试。

### 4. Hidden/underscore directory filter

在一级子模块扫描中跳过：

- `.git`, `.cache`, `.worktrees`, `.tmp` 等隐藏目录。
- `_fixtures`, `_tools` 等下划线目录。

### 5. Non-Go pollution guard

所有 GOWORK/go.work 修改必须新增非 Go 防污染测试：同一环境下 JS/TS、Java、Python、Rust、CSS 都要有 fake/root test 证明 root/cache key 不读取 GOWORK；JSON/YAML/Markdown fallback/capability path 也不得读取 GOWORK。

## 实现步骤

1. `resolveGoRootFromGOWORK`：识别 `auto`。
2. `resolveGoWorkRoot`：parse error soft fallback。
3. `resolveGoRootFromGOWORK`：显式 go.work containment 检查。
4. `findFirstLevelGoModRoots`：跳过隐藏/下划线目录。
5. 非 Go pollution tests：证明 GOWORK 只影响 Go adapter。
6. integration test：可选 `-tags lsp_integration` 真实 gopls smoke。

## 必要测试

- `TestGoRootResolverGOWORKAutoUsesAutoDiscovery`
- `TestGoRootResolverBrokenGoWorkFallsBackToWorkspaceRoot`
- `TestGoRootResolverExplicitGoWorkOutsideProjectConflicts`
- `TestGoRootResolverSingleSubmoduleSkipsHiddenAndUnderscoreDirs`
- `TestGoRootResolverLinkedWorktreeSymlinkAliasCanonicalKey`
- `TestGOWORKDoesNotAffectJSTSWorkspaceRoot`
- `TestGOWORKDoesNotAffectJavaWorkspaceRoot`
- `TestGOWORKDoesNotAffectPythonWorkspaceRoot`
- `TestGOWORKDoesNotAffectRustWorkspaceRoot`
- `TestGOWORKDoesNotAffectCSSWorkspaceRoot`
- `TestGOWORKDoesNotAffectJSONYAMLMarkdownFallback`
- `TestEmptyLanguageIDDoesNotDefaultToGoAdapter`
- `TestGoLanguageSpecificHashNotAddedToNonGoCacheKey`

## 验收命令

```bash
set -euo pipefail
required_tests=(
  './cmd/mcp-lsp/multilsp:TestGoRootResolverGOWORKAutoUsesAutoDiscovery'
  './cmd/mcp-lsp/multilsp:TestGoRootResolverBrokenGoWorkFallsBackToWorkspaceRoot'
  './cmd/mcp-lsp/multilsp:TestGoRootResolverExplicitGoWorkOutsideProjectConflicts'
  './cmd/mcp-lsp/multilsp:TestGoRootResolverSingleSubmoduleSkipsHiddenAndUnderscoreDirs'
  './cmd/mcp-lsp/multilsp:TestGoRootResolverLinkedWorktreeSymlinkAliasCanonicalKey'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectJSTSWorkspaceRoot'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectJavaWorkspaceRoot'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectPythonWorkspaceRoot'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectRustWorkspaceRoot'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectCSSWorkspaceRoot'
  './cmd/mcp-lsp/multilsp:TestGOWORKDoesNotAffectJSONYAMLMarkdownFallback'
  './cmd/mcp-lsp/multilsp:TestEmptyLanguageIDDoesNotDefaultToGoAdapter'
  './cmd/mcp-lsp/multilsp:TestGoLanguageSpecificHashNotAddedToNonGoCacheKey'
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

附加真实 gopls smoke（不替代上面的 mandatory fake tests）：

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
optional_smoke ./cmd/mcp-lsp/manager TestGoWorkRealGoplsSmoke lsp_integration gopls
```
