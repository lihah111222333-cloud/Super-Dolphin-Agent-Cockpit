# Backend Boundary AI Governance Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use 执行计划 to implement this plan task-by-task with TDD checkpoints.

**Goal:** 让后端边界违规包含精确行列，为每个含 Go 源码的后端顶层目录建立机器可校验的 rule/guard 治理归属，并从统一 registry 确定性生成 Archtest 规则地图和 README 测试统计。

**Architecture:** `BackendBoundaryRegistry` 是 owner、canonical rule、specialized guard、governed surface 的唯一事实源。evaluator 保持单文件，通过携带 `ImportSpec.Path` 位置生成精确违规；独立 `scripts/archtestmap` 只读取 registry 和源码 AST，生成规则地图与 README marker 内容；Makefile 保证 archtest 地图先于 codemap index 刷新和校验。

**Tech Stack:** Go AST/parser/token、现有 `internal/archtest` typed rule evaluator、Makefile、repo-local guarded test wrapper、Codemap generator。

**Execution boundary:** 当前工作在 `codex/backend-boundary-ai-governance-20260710` 隔离 worktree 内完成。未经用户明确要求，不执行 `git add`、`git commit`、`git push` 或 PR 操作。

---

### Task 1: 精确定位边界违规的行和列

**Files:**
- Modify: `internal/archtest/backend_boundary_guard_coverage_test.go`
- Modify: `internal/archtest/backend_boundary_evaluator.go`

**Step 1: 写失败测试**

在 evaluator 现有公开入口测试中加入单行 import 和 import block 两个夹具，断言完整违规前缀：

```go
func TestEvaluateBackendBoundaryFileReportsImportPosition(t *testing.T) {
	// single import: quote starts at 3:10
	// import block: quote starts at 4:7
	// Assert: internal/contract/leak.go:<line>:<column> imports ...
}
```

测试必须精确比较 `file:line:column`，不能只用正则断言“包含数字”。

**Step 2: 运行 focused test，确认 RED**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestEvaluateBackendBoundaryFileReportsImportPosition -count=1
```

Expected: FAIL；旧输出只有文件路径，没有 `:line:column`。

**Step 3: 实现最小位置模型**

保持 `internal/archtest/backend_boundary_evaluator.go` 不拆文件，增加私有值对象：

```go
type backendBoundaryImport struct {
	path   string
	line   int
	column int
}
```

`parseBackendBoundaryImports` 使用同一个 `token.FileSet` 解析并通过 `fset.Position(spec.Path.Pos())` 取得位置。candidate 和所有 typed rule evaluator 传递 `[]backendBoundaryImport`，规则判断仍只读取 `imp.path`，违规格式统一为：

```go
fmt.Sprintf("%s:%d:%d imports %s (rule=%s owner=%s reason=%s)", ...)
```

解析错误和无效位置继续 fail-fast，不增加默认位置。

**Step 4: 运行 focused test，确认 GREEN**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'TestEvaluateBackendBoundaryFileReportsImportPosition|TestBackendBoundary' -count=1
```

Expected: PASS。

**Step 5: LSP 诊断**

对修改后的 evaluator 和测试文件运行 `file(diagnostics)`，四种 severity 都必须为零。

---

### Task 2: 把 guard 与 surface 纳入统一 registry

**Files:**
- Create: `internal/archtest/backend_boundary_governance.go`
- Create: `internal/archtest/backend_boundary_governance_test.go`
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/boundary_registry_test.go`

**Step 1: 写 registry 深拷贝与治理校验失败测试**

先覆盖以下行为：

```go
func TestDefaultBackendBoundaryRegistryReturnsDeepCopy(t *testing.T)
func TestValidateDefaultBackendBoundaryGovernance(t *testing.T)
func TestValidateBackendBoundaryGovernanceRejectsUnregisteredSurface(t *testing.T)
func TestValidateBackendBoundaryGovernanceRejectsUnknownRule(t *testing.T)
func TestValidateBackendBoundaryGovernanceRejectsUnknownGuard(t *testing.T)
func TestValidateBackendBoundaryGovernanceRejectsMissingGuardTest(t *testing.T)
func TestValidateBackendBoundaryGovernanceRejectsOrphanGuard(t *testing.T)
func TestValidateBackendBoundaryGovernanceRejectsEmptyMechanism(t *testing.T)
```

临时仓库夹具至少包含 `cmd/<name>`、`internal/<name>`、`pkg/<name>`，证明发现逻辑是双向精确匹配，不是只校验清单本身。

**Step 2: 运行新测试，确认 RED**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'Test(DefaultBackendBoundaryRegistryReturnsDeepCopy|Validate.*BackendBoundaryGovernance)' -count=1
```

Expected: 编译或断言失败；registry 尚无 Guards/Surfaces。

**Step 3: 扩展唯一事实源类型**

在 registry 增加 typed ID 和结构：

```go
type BoundaryGuardID string

type BackendBoundaryGuard struct {
	ID        BoundaryGuardID
	File      string
	TestNames []string
	Reason    string
}

type BackendBoundarySurface struct {
	Path     string
	RuleIDs  []BoundaryRuleID
	GuardIDs []BoundaryGuardID
	Reason   string
}

type BackendBoundaryRegistry struct {
	Owners   []BackendBoundaryOwner
	Rules    []BackendBoundaryRule
	Guards   []BackendBoundaryGuard
	Surfaces []BackendBoundarySurface
}
```

`DefaultBackendBoundaryRegistry` 和 clone 逻辑必须深拷贝所有切片，包括嵌套 `TestNames`、`RuleIDs`、`GuardIDs`。

**Step 4: 增加 public pkg canonical rule**

为所有 `pkg/<name>` 增加 `pkg_no_internal_imports` canonical rule，规则文件模式为 `pkg/**/*.go`，禁止导入当前模块的 `internal` 与 `cmd`，同时补齐 owner、registry validation 和 typed evaluator 测试。这样公共库边界由真正的 canonical rule 治理，不把 unrelated guard 当作占位符。

**Step 5: 实现治理发现和 fail-fast 校验**

`ValidateBackendBoundaryGovernance(root, registry)` 必须：

1. 发现 `cmd/*`、`internal/*`、`pkg/*` 中递归包含 `.go` 的一级目录。
2. 与 `Surfaces` 做双向精确匹配。
3. 校验每个 surface 至少有 rule 或 guard，并拒绝重复/未知 ID、空 reason。
4. 校验被引用 canonical rule 确实能匹配该 surface 下至少一个生产 Go 文件；policy 驱动规则还必须有实际作用于该文件的执行策略。
5. 校验 guard 文件严格位于 `internal/archtest`、以 `_test.go` 结尾、是普通文件，且解析实体路径后仍位于该目录；拒绝文件或父目录 symlink 逃逸。
6. 先按当前 Go build context 校验 build tag、GOOS/GOARCH 文件选择，再镜像 `cmd/go` 校验每个 `TestNames` 都是该文件内真实、顶层、无类型参数且可发现的 Test 函数；接受 Go 工具认可的裸 `Test`、`*T`、`*pkg.T` 和空结果列表。
7. 拒绝重复 guard、孤儿 guard 和重复测试名。

**Step 6: 填充当前仓库完整治理清单**

登记所有实际 Go surface：

- `cmd/*`、`internal/*` 使用能真实匹配的现有 canonical rules，并仅在需要表达专项语义时补充 guard。
- `pkg/*` 使用新 `pkg_no_internal_imports` rule。
- guard 只引用已存在且语义匹配的 archtest 测试入口，例如 dependency direction、Fx graph、UI/Wails 或 backend boundary single-source guard。

不得创建宽泛 `all_backend` 默认项，不得用 guard 文件存在代替测试入口证明。

**Step 7: 运行 focused 与包测试，确认 GREEN**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'Test(DefaultBackendBoundaryRegistryReturnsDeepCopy|Validate.*BackendBoundaryGovernance|PkgNoInternalImports)' -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

Expected: PASS。

**Step 8: LSP 导航与诊断**

对新类型、默认 registry、validator 做 definition/references 复核；对全部修改 Go 文件运行 diagnostics，四种 severity 都必须为零。

---

### Task 3: 实现确定性 Archtest 地图与 README 统计生成器

**Files:**
- Create: `scripts/archtestmap/main.go`
- Create: `scripts/archtestmap/main_test.go`
- Modify: `README.md`
- Generate: `docs/doc/codemap/13-archtest-boundaries.md`

**Step 1: 写生成器失败测试**

覆盖：

```go
func TestRenderRuleMapIsDeterministic(t *testing.T)
func TestCollectArchtestStatsUsesRunnableTopLevelTests(t *testing.T)
func TestReplaceREADMEStatsOnlyTouchesInlineMarker(t *testing.T)
func TestReplaceREADMEStatsRejectsMissingDuplicateOrReversedMarkers(t *testing.T)
func TestRunCheckReportsDriftWithoutWriting(t *testing.T)
func TestRunRefreshThenCheck(t *testing.T)
```

测试输入必须故意打乱 registry 顺序，以证明输出按 surface、rule ID、guard file/test name 稳定排序；check 测试需在调用前后比较文件内容，证明只读。

**Step 2: 运行生成器测试，确认 RED**

Run:

```bash
./scripts/test_with_guard.sh ./scripts/archtestmap -count=1
```

Expected: FAIL；命令尚不存在。

**Step 3: 实现生成器核心**

实现 `run(root string, check bool) error` 及纯函数 renderer：

- 只读取 `archtest.DefaultBackendBoundaryRegistry()`。
- 先调用 registry 与 filesystem governance validators。
- 输出 owner、rule、kind、file patterns、allow/deny/scope/exception 摘要，以及 surface → rule/guard 映射。
- 使用 Go AST 统计 `internal/archtest/**/*_test.go` 中符合 Go testing 规则的顶层 Test 函数和含测试文件数。
- 不写时间戳、绝对路径或 map 顺序结果。
- refresh 使用完整目标内容写入；check 仅比较并返回明确 drift 错误。

**Step 4: 建立 README 狭窄 marker**

只把现有 Architecture Tests 单元格替换为：

```markdown
| Architecture Tests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: ...<!-- END GENERATED ARCHTEST STATS --> |
```

生成器必须要求 begin/end marker 恰好各一个且顺序正确，只替换 marker 内文本。

**Step 5: 运行测试与首次 refresh，确认 GREEN**

Run:

```bash
./scripts/test_with_guard.sh ./scripts/archtestmap -count=1
go run ./scripts/archtestmap
go run ./scripts/archtestmap --check
```

Expected: tests PASS；生成 `13-archtest-boundaries.md`；README 数量来自 AST；check PASS 且不产生额外 diff。

**Step 6: LSP 诊断**

对 `scripts/archtestmap/main.go` 和 `main_test.go` 运行 diagnostics，四种 severity 都必须为零。

---

### Task 4: Makefile 与 Codemap 生成链闭环

**Files:**
- Modify: `Makefile`
- Generate: `docs/doc/codemap/README.md`
- Generate: `docs/doc/codemap/ai-index.json`
- Generate as required: `docs/doc/codemap/project-map.json`
- Generate as required: `docs/doc/codemap/project-map.md`

**Step 1: 写 Makefile 接线**

新增 `.PHONY` 目标：

```make
archtest-map-refresh:
	go run ./scripts/archtestmap

archtest-map-check:
	go run ./scripts/archtestmap --check
```

`codemap-refresh` 必须先执行 `archtest-map-refresh`，再执行 `codemap_index.go`；`codemap-check` 同理先执行只读 `archtest-map-check`。

**Step 2: 刷新完整生成链**

Run:

```bash
make codemap-refresh
./scripts/refresh_generated_artifacts.sh project-map
```

Expected: 第 13 卷被 codemap README 和 `ai-index.json` 收录；如 project-map 受新增源码影响，则由官方生成器刷新。

**Step 3: 验证 check 全程只读**

先记录 `git status --short`，然后运行：

```bash
make archtest-map-check
make codemap-check
./scripts/refresh_generated_artifacts.sh project-map --check
```

Expected: 全部 PASS；check 前后 `git status --short` 完全一致。

---

### Task 5: 完成前全量验证与人工复核

**Files:**
- Verify all files changed by Tasks 1–4

**Step 1: 格式化与 diff 卫生**

Run:

```bash
gofmt -w internal/archtest/backend_boundary_evaluator.go internal/archtest/backend_boundary_guard_coverage_test.go internal/archtest/backend_boundary_registry.go internal/archtest/boundary_registry_test.go internal/archtest/backend_boundary_governance.go internal/archtest/backend_boundary_governance_test.go scripts/archtestmap/main.go scripts/archtestmap/main_test.go
git diff --check
```

Expected: PASS；不修改无关文件。

**Step 2: LSP 完整证据**

对全部修改 Go 文件执行：符号定位、definition/hover、references/call hierarchy、精读和 diagnostics。任何 Error、Warning、Information、Hint 都必须修复或明确列为 blocker。

**Step 3: 运行匹配变更面的验证**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./scripts/archtestmap -count=1
make archtest-map-check
make codemap-check
./scripts/refresh_generated_artifacts.sh project-map --check
make guard
```

Expected: 全部 PASS。

**Step 4: 验收不变量**

人工确认：

1. `internal/archtest/backend_boundary_evaluator.go` 仍是原文件，未拆分。
2. 违规输出包含真实 import token 的 `file:line:column`。
3. 当前每个后端顶层 Go surface 恰好登记一次，并绑定真实 canonical rule 或真实 guard test。
4. `13-archtest-boundaries.md` 和 README 统计没有手写事实、时间戳、绝对路径。
5. check 命令没有写工作区。
6. 没有 stage、commit、push。

**Step 5: 报告结果**

列出变更面、实际测试统计、完整验证命令及结果、未提交 worktree/branch 状态；如任一 gate 失败，按真实 blocker 报告，不声称完成。
