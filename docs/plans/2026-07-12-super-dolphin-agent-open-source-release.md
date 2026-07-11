# Super Dolphin Agent Open-Source Release Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不创建公共仓库、不推送和不发布 Release 的前提下，将私有开发仓库改造成可重复生成、可审计、默认拒绝泄露的 Super Dolphin Agent Apache-2.0 公开候选，并补齐六语言 README、治理演示、五分钟启动和社区发行面。

**Architecture:** 私有仓库仍是唯一开发真源；`super-dolphin-source-export` 只读取指定已提交 Git tree，按严格 JSON 白名单将文件放入空候选目录。候选目录内运行固定的 public-profile codemap、project-map 与 capcontract 生成器，然后由 `seal` 写入不可手改的 `OPEN_SOURCE_EXPORT.json`，再由 `verify` 和 gitleaks 对密封后的树做闭环校验。策略文件只描述数据，不允许携带或执行命令。

**Tech Stack:** Go 1.24、Git plumbing、严格 JSON、现有 archtest/codemap/project-map/capcontract 生成器、Make、Bash、PowerShell、React/Vite、GitHub Actions、gitleaks。

**Verification Surface:** Go 单元与集成测试、真实临时 Git 仓库、六语言结构 guard、真实 backend-boundary evaluator RED→GREEN 演示、公开候选树重建与 receipt 校验、gitleaks、`make guard`、前端 lint/test/build，以及 worktree 本地 LSP 的定位/理解/引用/精读/diagnostics 证据。

---

## 执行约束

- [ ] 所有源码任务先写失败测试并观察预期 RED，再写最小实现；没有 RED 证据不得进入实现。
- [ ] 只在隔离 worktree `codex/open-source-release-readiness-20260712` 修改；不得覆盖主工作区已有变更。
- [ ] 策略解析、路径分类、身份检查、生成物检查、secret scan 均 fail-fast；不得增加 `force`、`skip`、`ignore-dirty` 或隐式默认值。
- [ ] 正式导出只接受 `git rev-parse <commit>^{commit}` 能解析的已提交对象，并要求源仓库无已跟踪文件变更。
- [ ] 每完成一个任务运行该任务的窄验证；阶段结束运行相关 package tests；最终才运行全量门禁。
- [ ] 当前任务未重新加载 worktree MCP/LSP，实施可以继续，但最终源码验收必须在新任务中取得 worktree 本地 LSP 证据；在此之前不得宣称最终 LSP 闭环完成。

## Task 0: 固定基线与工具证据

**Files:**

- Verify: `README.md`
- Verify: `docs/doc/codemap/README.md`
- Verify: `docs/internal-notes/LSP系统提示词.md`
- Verify: `internal/archtest/backend_boundary_registry.go`
- Verify: `internal/archtest/backend_boundary_evaluator.go`

- [ ] 记录 `git status --short`、`git rev-parse HEAD`、branch、Go/Node/gitleaks 版本和 worktree setup `ready`/`verify` 输出。
- [ ] 在新 Codex 任务中加载该 worktree 后，用 LSP 完成一组最小证据：`grep` 定位 `EvaluateBackendBoundaryFile`，`inspect` 读取签名，`xref` 检查调用方，`file(read_file)` 精读实现，`file(diagnostics)` 检查目标文件。
- [ ] 如果 LSP 仍指向主工作区，记录 tool/action、work_dir、目标符号、错误与收窄重试，停止最终验收，不使用 shell 或 `gopls check` 伪装 LSP PASS。

**Baseline command:**

```bash
go test ./internal/archtest ./internal/devtools/codemapindex ./internal/devtools/capcontract -count=1
git status --short
```

**Expected:** tests pass；除本计划与已批准规格修订外没有意外变更。

## Task 1: 先锁项目身份与策略契约

**Files:**

- Create: `release/open-source-policy.json`
- Create: `release/open-source-export.schema.json`
- Create: `internal/devtools/sourceexport/policy.go`
- Create: `internal/devtools/sourceexport/errors.go`
- Create: `internal/devtools/sourceexport/policy_test.go`
- Create: `internal/devtools/sourceexport/identity_guard_test.go`

**Public types:**

```go
type Code string

type Error struct {
    Code Code
    Path string
    Err  error
}

func (e *Error) Error() string
func (e *Error) Unwrap() error

type ProjectIdentity struct {
    ProductName string `json:"product_name"`
    Repository  string `json:"repository"`
    GoModule    string `json:"go_module"`
    License     string `json:"license"`
}

type PathRule struct {
    Pattern string `json:"pattern"`
    Kind    string `json:"kind"`
}

type Policy struct {
    SchemaVersion          int        `json:"schema_version"`
    CanonicalProductName   string     `json:"canonical_product_name"`
    CanonicalRepository    string     `json:"canonical_repository"`
    CanonicalModulePath    string     `json:"canonical_module_path"`
    LicenseSPDX            string     `json:"license_spdx"`
    RequiredRootFiles      []string   `json:"required_root_files"`
    AllowRules             []PathRule `json:"allow_rules"`
    DenyRules              []PathRule `json:"deny_rules"`
    ForbiddenIdentities    []string   `json:"forbidden_identities"`
    RequiredReadmes        []string   `json:"required_readmes"`
    RequiredREADMESections []string   `json:"required_readme_sections"`
    ForbiddenFileNames     []string   `json:"forbidden_file_names"`
    GeneratedFiles         []string   `json:"generated_files"`
}

func LoadPolicy(path string) (Policy, error)
func ValidatePolicy(policy Policy) error
```

- [ ] RED: 表驱动测试锁定 unknown JSON field、重复规则、绝对路径、反斜杠、`..`、空 required/generated 项、非 Apache-2.0 身份、旧 module path；断言错误码是 `POLICY_INVALID`、`LICENSE_MISMATCH` 或 `MODULE_PATH_MISMATCH`。
- [ ] RED: 身份 guard 扫描活跃公开根，证明当前 `README.md`、`go.mod` 和根 `LICENSE` 失败；依赖锁文件内第三方 `Unlicense` 元数据不得被误判。
- [ ] GREEN: 用 `json.Decoder.DisallowUnknownFields()` 严格解析；实现稳定排序和重复检测；错误必须带 code 与 path。
- [ ] GREEN: 策略声明产品 `Super Dolphin Agent`、仓库/module `github.com/lihah111222333-cloud/super-dolphin-agent`、许可证 `Apache-2.0`；白名单按精确根文件与精确目录规则定义，deny 优先于 allow。

**Verify:**

```bash
go test ./internal/devtools/sourceexport -run 'TestLoadPolicy|TestValidatePolicy|TestIdentityGuard' -count=1
```

**Commit:** `test(release): 锁定开源策略与身份契约`

## Task 2: 迁移品牌、许可证与 Go module 身份

**Files:**

- Modify: `go.mod`
- Modify: all tracked active `*.go` imports under `cmd/`, `internal/`, `pkg/`, `scripts/`
- Modify: active non-Go files containing the old module or old product identity
- Replace: `LICENSE`
- Create: `NOTICE`
- Modify: `README.md`
- Modify: test fixtures containing `/Users/mima0000`
- Modify: repo-local skill text containing the absolute workspace path
- Refresh: `docs/doc/codemap/**`

- [ ] RED: run the identity guard and capture failures for `github.com/anthropic-ai/super-agent-v3`, `Super Agent v3`, `Anthropic, Inc.` and `/Users/mima0000` in public-eligible files.
- [ ] GREEN: use Go-aware module migration (`go mod edit -module=...` plus bounded import rewrite); do not rename runtime environment variables, database home names or compatibility identifiers unless they are public brand text.
- [ ] GREEN: replace root license with exact Apache License 2.0 text and add a project NOTICE without claiming third-party ownership.
- [ ] GREEN: replace real local fixture paths with `/Users/alice/...` or `<workspace>` while preserving test semantics.
- [ ] GREEN: run `gofmt` on changed Go files, `go mod tidy`, refresh generated artifacts through their canonical generators, and re-run identity guard.

**Verify:**

```bash
go test ./internal/devtools/sourceexport -run TestIdentityGuard -count=1
go test ./cmd/... ./internal/... ./pkg/... ./scripts/... -run '^$'
go mod verify
```

**Commit:** `refactor(identity): 统一 Super Dolphin Agent 开源身份`

## Task 3: 实现 Git tree 枚举和默认拒绝路径分类

**Files:**

- Create: `internal/devtools/sourceexport/git_tree.go`
- Create: `internal/devtools/sourceexport/path_matcher.go`
- Create: `internal/devtools/sourceexport/git_tree_test.go`
- Create: `internal/devtools/sourceexport/path_matcher_test.go`

**Core types:**

```go
type TreeEntry struct {
    Path string
    Mode string
    Data []byte
    Hash string
}

type gitRunner interface {
    Run(ctx context.Context, repo string, args ...string) ([]byte, error)
}
```

- [ ] RED: 用真实临时 Git repo 覆盖普通文件、可执行文件、空文件、Unicode 路径、空格路径、嵌套目录、未分类路径、deny 覆盖 allow、symlink、submodule mode、大小写碰撞和 dirty tracked file。
- [ ] RED: 断言未分类、禁止路径、symlink、大小写碰撞和 dirty 源分别返回 `UNCLASSIFIED_PATH`、`FORBIDDEN_PATH`、`SYMLINK_REJECTED`、`CASE_COLLISION`、`SOURCE_DIRTY`。
- [ ] GREEN: 使用 `git ls-tree -rz --full-tree` 和 `git cat-file` 读取指定 commit，不从工作区复制文件；对命令失败的少量分支注入 `gitRunner`，正常语义测试继续使用真实 repo。
- [ ] GREEN: 对每一个 tree entry 做 slash-normalized、UTF-8、clean relative path、mode 和大小写唯一性检查；只有 allow 命中且 deny 不命中的路径进入 stage 集合。
- [ ] GREEN: 私有视图生成物即使在 allow 目录下也由 policy deny，留待候选树 public profile 重建。

**Verify:**

```bash
go test ./internal/devtools/sourceexport -run 'TestGitTree|TestClassifyPath|TestDirtySource' -count=1
```

**Commit:** `feat(release): 增加默认拒绝的 Git 树分类器`

## Task 4: 实现 stage、seal、receipt 与 verify

**Files:**

- Create: `internal/devtools/sourceexport/export.go`
- Create: `internal/devtools/sourceexport/receipt.go`
- Create: `internal/devtools/sourceexport/verify.go`
- Create: `internal/devtools/sourceexport/export_test.go`
- Create: `internal/devtools/sourceexport/receipt_test.go`
- Create: `internal/devtools/sourceexport/verify_test.go`

**Public API:**

```go
type StageOptions struct {
    Repo       string
    Commit     string
    PolicyPath string
    OutputDir  string
}

type SealOptions struct {
    Dir        string
    PolicyPath string
    Commit     string
}

type VerifyOptions struct {
    Dir        string
    PolicyPath string
}

type ReceiptFile struct {
    Path   string `json:"path"`
    SHA256 string `json:"sha256"`
    Mode   string `json:"mode"`
    Size   int64  `json:"size"`
}

type Receipt struct {
    SchemaVersion int           `json:"schema_version"`
    SourceCommit  string        `json:"source_commit"`
    PolicySHA256  string        `json:"policy_sha256"`
    Files         []ReceiptFile `json:"files"`
}

func Stage(ctx context.Context, options StageOptions) error
func Seal(options SealOptions) error
func Verify(options VerifyOptions) error
```

- [ ] RED: stage 拒绝不存在/非空/位于源仓库内部的 output；验证普通与可执行 mode、原子清理、确定性字节、禁止文件缺席和私有视图生成物缺席。
- [ ] RED: seal 在 required 文件或 public generated 文件缺失、身份字符串不一致、module path 不一致、根 license 不一致时失败；verify 在增加、删除、改写、chmod 或伪造 receipt 时返回 `EXPORT_RECEIPT_MISMATCH`。
- [ ] GREEN: stage 先校验完整 Git tree 再创建输出；写入失败删除本次新建候选，不留下部分公开树；拒绝跟随 symlink。
- [ ] GREEN: seal 只遍历候选目录，不读取私有工作区；receipt 排除自身后按路径字节序排序，记录 SHA-256、mode 和 size，并用原子 rename 写入。
- [ ] GREEN: verify 严格解析 receipt 和 policy，重新计算全树集合/摘要/mode，重新执行路径、身份和 required/generated 检查；receipt 不允许 unknown field。

**Verify:**

```bash
go test ./internal/devtools/sourceexport -run 'TestStage|TestSeal|TestVerify|TestReceipt' -count=1
```

**Commit:** `feat(release): 实现可密封的公开源码导出`

## Task 5: 添加导出 CLI、Make 编排和 command 边界登记

**Files:**

- Create: `cmd/super-dolphin-source-export/main.go`
- Create: `cmd/super-dolphin-source-export/main_test.go`
- Modify: `Makefile`
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_registry_test.go`

**CLI contract:**

```text
super-dolphin-source-export stage --repo <abs> --commit <sha> --policy <path> --out <abs>
super-dolphin-source-export seal --dir <abs> --policy <path> --commit <sha>
super-dolphin-source-export verify --dir <abs> --policy <path>
```

- [ ] RED: `runCLI(args, stdout, stderr)` 测试锁定无子命令、未知 flag、相对 repo/out、缺参数、coded runtime error；参数错误 exit 2，运行错误 exit 1，成功 exit 0；stderr 输出稳定错误码且不泄露文件内容。
- [ ] RED: backend-boundary registry 测试先因新 command 未登记或 allow seam 不精确而失败。
- [ ] GREEN: CLI 只调用三个固定 API，不提供策略命令、shell、force/skip；Make 目标只接受绝对 `OUT` 与 commit，顺序固定为 stage → public-generated-refresh → seal → verify → gitleaks。
- [ ] GREEN: 正式目标缺少 gitleaks 时以 `SECRET_SCAN_FAILED` 失败并打印精确安装指令；测试目标不要求机器预装 gitleaks。
- [ ] GREEN: registry 为 `cmd/super-dolphin-source-export` 增加 exact pattern、`internal/devtools/sourceexport` narrow allow policy 和 surface；不得放宽其他 command。

**Verify:**

```bash
go test ./cmd/super-dolphin-source-export ./internal/archtest -count=1
make -n open-source-export OUT=/tmp/super-dolphin-agent-public COMMIT=$(git rev-parse HEAD)
```

**Commit:** `feat(release): 编排公开源码导出命令`

## Task 6: 为三个生成器增加 public profile

**Files:**

- Modify: `scripts/codemap_index.go`
- Modify: `internal/devtools/codemapindex/**`
- Modify: `scripts/generate_ai_project_map.mjs`
- Modify: `scripts/generate_ai_project_map_guard_test.go`
- Modify: `scripts/capcontract/main.go`
- Modify: `internal/devtools/capcontract/**`
- Modify: `Makefile`
- Modify: generated files under `docs/doc/codemap/**`

- [ ] RED: 在 fixture 中同时放置允许的公开路径与 `docs/plans/**`、`docs/archive/**`、`.agent/**`，证明三个现有生成器会将私有路径带入结果或缺少 public profile。
- [ ] RED: 候选树缺失 public generated artifacts 时 seal 必须失败；public profile 输出含任一 deny path/forbidden identity 时测试失败。
- [ ] GREEN: 为三个生成器增加显式 `--profile public`；public profile 只从已 stage 的候选树扫描，使用共享的公开目录分类规则或等价的固定 profile，不读取私有源 repo。
- [ ] GREEN: 增加 `make public-generated-refresh`，固定执行 codemap、project-map、capcontract public profile；该目标不可接受额外命令字符串。
- [ ] GREEN: 默认 profile 行为保持现有私有开发生成结果；public profile 的输出排序稳定，连续执行两次 diff 为空。

**Verify:**

```bash
go test ./internal/devtools/codemapindex ./internal/devtools/capcontract ./scripts -run 'PublicProfile|ProjectMap' -count=1
make public-generated-refresh
git diff --exit-code -- docs/doc/codemap
```

**Commit:** `feat(governance): 增加公开项目地图生成配置`

## Task 7: 构建真实 RED→GREEN 治理演示

**Files:**

- Create: `internal/devtools/governancedemo/demo.go`
- Create: `internal/devtools/governancedemo/report.go`
- Create: `internal/devtools/governancedemo/demo_test.go`
- Create: `cmd/super-dolphin-governance-demo/main.go`
- Create: `cmd/super-dolphin-governance-demo/main_test.go`
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_registry_test.go`
- Modify: `Makefile`

**Report contract:**

```go
type PhaseReport struct {
    Name       string   `json:"name"`
    Violations []string `json:"violations"`
}

type DemoReport struct {
    SchemaVersion int         `json:"schema_version"`
    RuleID       string      `json:"rule_id"`
    Red          PhaseReport `json:"red"`
    Green        PhaseReport `json:"green"`
}
```

- [ ] RED: 临时 fixture 的 `internal/module/demo/service.go` 直接 import `internal/store`，调用 `archtest.EvaluateBackendBoundaryFile(..., "module_no_store_imports")`；断言恰好命中该规则并包含精确文件位置。
- [ ] RED: 演示在 RED 未失败、命中错误 rule、缺少位置、GREEN 仍违规或两次 JSON 不一致时返回非零。
- [ ] GREEN: 将 GREEN fixture 改为依赖本地 contract port；使用同一个真实 evaluator 得到零违规；报告只包含相对路径，稳定排序，不写时间戳和 temp absolute path。
- [ ] GREEN: `make demo-governance` 在临时目录输出 `demo-report.json`，不改工作区；command registry 只允许 `internal/devtools/governancedemo` 与 `internal/archtest` 的精确 seam。

**Verify:**

```bash
go test ./internal/devtools/governancedemo ./cmd/super-dolphin-governance-demo ./internal/archtest -count=1
make demo-governance
```

**Commit:** `feat(governance): 增加真实边界治理演示`

## Task 8: 重写英文 README 并建立六语言一致性 guard

**Files:**

- Rewrite: `README.md`
- Create: `README.zh-CN.md`
- Create: `README.ja.md`
- Create: `README.ko.md`
- Create: `README.es.md`
- Create: `README.de.md`
- Create: `internal/devtools/readmeguard/guard.go`
- Create: `internal/devtools/readmeguard/guard_test.go`
- Create: `cmd/super-dolphin-readme-guard/main.go`
- Create: `cmd/super-dolphin-readme-guard/main_test.go`
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `Makefile`

**Stable sections:**

```text
<!-- sd:why -->
<!-- sd:architecture -->
<!-- sd:quick-start -->
<!-- sd:governance-demo -->
<!-- sd:security -->
<!-- sd:community -->
```

- [ ] RED: guard 覆盖缺语言、导航不闭环、section 顺序漂移、命令/URL/module/license 不一致、占位符、失效相对链接、生成 marker 不一致和禁止身份。
- [ ] GREEN: 英文根 README 首屏回答产品是什么、适合谁、与普通 agent framework 的区别、三分钟证据；Quick Start 使用真实 clone URL 和固定 bootstrap 命令。
- [ ] GREEN: 完成人工自然语言的简体中文、日语、韩语、西班牙语、德语版本；不翻译命令、路径、环境变量、rule ID 和生成 marker。
- [ ] GREEN: 六份 README 保持同一章节顺序与机器事实；详细内容链接到 `docs/open-source/ARCHITECTURE.md` 和 `GOVERNANCE.md`，不把内部迁移史放进首屏。
- [ ] GREEN: Make 增加 `readme-guard`，command registry 对新 guard command 只放行 `internal/devtools/readmeguard`。

**Verify:**

```bash
go test ./internal/devtools/readmeguard ./cmd/super-dolphin-readme-guard ./internal/archtest -count=1
make readme-guard
```

**Commit:** `docs(readme): 发布六语言项目入口`

## Task 9: 实现五分钟 bootstrap

**Files:**

- Create: `scripts/bootstrap.sh`
- Create: `scripts/bootstrap.ps1`
- Create: `scripts/bootstrap_test.go`
- Modify: `README.md`
- Modify: five translated README files

- [ ] RED: shell fixture 覆盖缺 Go、Go 版本不足、缺 Node/npm、Node 版本不足、依赖安装失败、构建失败和成功路径；断言每种失败都有明确阶段和非零退出。
- [ ] RED: 静态测试锁定 Bash `set -euo pipefail`、PowerShell `$ErrorActionPreference = 'Stop'`、无 curl-pipe-shell、无 sudo、无隐式包管理器安装、无 telemetry 或凭据读取。
- [ ] GREEN: 两个脚本只做环境检查、依赖安装、构建和下一步提示；每一步打印确定性阶段名；不修改用户全局环境。
- [ ] GREEN: README 的 macOS/Linux/Windows 命令与脚本真实参数一致，并说明首次安装依赖的网络要求。

**Verify:**

```bash
go test ./scripts -run TestBootstrap -count=1
bash -n scripts/bootstrap.sh
```

**Commit:** `feat(onboarding): 增加跨平台五分钟启动脚本`

## Task 10: 补齐社区与开源架构文档

**Files:**

- Create: `CONTRIBUTING.md`
- Create: `SECURITY.md`
- Create: `CODE_OF_CONDUCT.md`
- Create: `CHANGELOG.md`
- Create: `SUPPORT.md`
- Create: `docs/open-source/ROADMAP.md`
- Create: `docs/open-source/ARCHITECTURE.md`
- Create: `docs/open-source/GOVERNANCE.md`
- Create: `docs/open-source/RELEASE_CHECKLIST.md`
- Create: `.github/ISSUE_TEMPLATE/bug_report.yml`
- Create: `.github/ISSUE_TEMPLATE/feature_request.yml`
- Create: `.github/ISSUE_TEMPLATE/config.yml`
- Create: `.github/PULL_REQUEST_TEMPLATE.md`
- Create: `internal/devtools/sourceexport/community_guard_test.go`

- [ ] RED: guard 先证明 required community 文件缺失；覆盖 placeholder、内部绝对路径、私有 docs 链接、旧身份、非公开邮箱承诺和无法执行的命令。
- [ ] GREEN: CONTRIBUTING 描述 fork/branch/tests/atomic commits；SECURITY 提供不虚构 SLA 的私下报告方式；CODE_OF_CONDUCT 使用 Contributor Covenant 2.1 并保留上游 attribution；CHANGELOG 从 `Unreleased` 开始。
- [ ] GREEN: ARCHITECTURE 解释 agent/runtime/provider/store/contract 与生成地图；GOVERNANCE 解释默认拒绝、backend boundary、guard 和证据链；ROADMAP 区分已存在与计划项；RELEASE_CHECKLIST 锁定 prepare/publish 的人工分界。
- [ ] GREEN: issue/PR 模板要求复现、预期、证据、风险和验证，不要求公开 secret、trace payload 或私有日志。

**Verify:**

```bash
go test ./internal/devtools/sourceexport -run TestCommunitySurface -count=1
make readme-guard
```

**Commit:** `docs(community): 补齐开源协作与治理文档`

## Task 11: 新增公开 CI 与可复现安全门禁

**Files:**

- Create: `.github/workflows/open-source-gates.yml`
- Modify: `release/open-source-policy.json`
- Modify: `internal/devtools/sourceexport/policy_test.go`

- [ ] RED: workflow contract test 锁定 action 必须使用完整 commit SHA、最小 permissions、无 pull_request_target、无持久化凭据、无私有 workflow/paths、无未固定下载 URL。
- [ ] GREEN: workflow 使用 `actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10` 和 `gitleaks/gitleaks-action@e0c47f4f8be36e29cdc102c57e68cb5cbf0e8d1e`，只读 contents 权限。
- [ ] GREEN: gates 运行身份/README/community/sourceexport tests、Go tests、frontend lint/test/build、`make demo-governance`、候选导出、候选树 gitleaks 和 receipt verify。
- [ ] GREEN: public policy 只允许 `.github/workflows/ci.yml` 与 `open-source-gates.yml` 等明确公开 workflow；不得导出引用 `docs/cc` 或私有发布材料的 workflow。

**Verify:**

```bash
go test ./internal/devtools/sourceexport -run 'TestOpenSourceWorkflow|TestPolicyRequiredFiles' -count=1
```

**Commit:** `ci(open-source): 增加公开候选安全门禁`

## Task 12: 生成候选并完成最终证据闭环

**Files:**

- Verify: all changed files
- Generate in external temp directory: `OPEN_SOURCE_EXPORT.json`
- Do not commit: external public candidate directory

- [ ] 清理计划执行中的临时 fixture，确认 worktree 没有非预期 untracked 文件；检查生成物由规范生成器拥有。
- [ ] 运行 Go package tests、race-sensitive targeted tests、guard、archtest、frontend lint/test/build。
- [ ] 在外部空目录执行两次 `make open-source-export`，比较除 source commit 外应稳定的文件清单与摘要；第二次必须使用另一个空目录。
- [ ] 对候选树执行 `rg` 检查旧品牌、旧 module、`/Users/mima0000`、私有目录名和禁止身份；运行 gitleaks `--no-git --redact`。
- [ ] 在候选树运行 bootstrap 静态检查、README guard、治理演示和可运行的公开测试；确认 receipt verify 在篡改一个字节后失败，再重新生成干净候选。
- [ ] 在加载 worktree 的新 Codex 任务中，对新增/修改 Go 文件取得 LSP 定位、理解、影响面、精读和 diagnostics；所有 Error/Warning/Information/Hint 必须修复或记录精确 blocker。
- [ ] 独立审查最终 diff：重点检查白名单是否过宽、策略是否能执行命令、错误是否被吞、receipt 是否可绕过、公开生成物是否泄露私有路径、六语言事实是否漂移。
- [ ] 按原子边界提交剩余变更；只准备本地分支，不创建仓库、不 push、不创建 PR、不发 Release。

**Full verification:**

```bash
go test ./internal/devtools/sourceexport ./internal/devtools/governancedemo ./internal/devtools/readmeguard ./cmd/super-dolphin-source-export ./cmd/super-dolphin-governance-demo ./cmd/super-dolphin-readme-guard -count=1
go test ./internal/archtest -count=1
make demo-governance
make guard
cd frontend-app && npm run lint && npm test && npm run build
cd ..
OUT1=$(mktemp -d)/super-dolphin-agent
OUT2=$(mktemp -d)/super-dolphin-agent
make open-source-export OUT="$OUT1" COMMIT=$(git rev-parse HEAD)
make open-source-export OUT="$OUT2" COMMIT=$(git rev-parse HEAD)
diff -ru "$OUT1" "$OUT2"
```

**Expected final state:** 本地分支包含可审计实现与验证证据；两个外部候选树逐字一致；未创建或修改任何公共 GitHub 仓库、远端分支、PR 或 Release。

**Commit:** `chore(release): 完成开源候选验收`
