# Codex Worktree LSP Bootstrap Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为每个 Git worktree 提供仓库内、跨平台、fail-fast 的 Codex LSP 准备命令，并在新 Codex task 中验收七个短工具及 Go/JavaScript 语义能力。

**Architecture:** 先用 ignored 本地配置手动恢复新 task 的 LSP，解除仓库强制 LSP 工作流的自举死锁；随后以 TDD 新增 `cmd/codex-worktree-setup` 的 `configure/ready/verify` 子命令。命令只管理 worktree-local binary 与 TOML block，通过真实 MCP initialize/tools-list 及 Go/JavaScript `file(diagnostics)` probe 验证 sidecar 和语言服务器，不修改全局 Codex 配置。

**Tech Stack:** Go 1.25.7、BurntSushi TOML、MCP JSON-RPC stdio、Git linked worktree、Codex CLI、Make。

**Verification Surface:** `cmd/codex-worktree-setup`、`cmd/mcp-lsp`、`Makefile`、`README.md`、`.agents/skills/使用git工作区/SKILL.md`、新 task 的七个 LSP 工具。

---

## Files and boundaries

- Create: `cmd/codex-worktree-setup/main.go`
- Create: `cmd/codex-worktree-setup/paths.go`
- Create: `cmd/codex-worktree-setup/config.go`
- Create: `cmd/codex-worktree-setup/atomic_unix.go`
- Create: `cmd/codex-worktree-setup/atomic_windows.go`
- Create: `cmd/codex-worktree-setup/mcp_probe.go`
- Create: `cmd/codex-worktree-setup/*_test.go`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `.agents/skills/使用git工作区/SKILL.md`

Do not track or stage `.codex/config.toml`, `bin/mcp-lsp`, global npm packages, `node_modules`, or machine paths. This plan must reach **new-task runtime accepted** before Plans 2 and 3 start.

### Task 0: Bootstrap LSP without editing tracked source

**Files:**
- Local ignored create: `.codex/config.toml`
- Local ignored create: `bin/mcp-lsp`

- [ ] **Step 1: Record the stable RED symptom**

Run before local setup:

```bash
codex mcp get lsp
```

Expected: `No MCP server named 'lsp' found`.

- [ ] **Step 2: Verify/install required language servers**

Run:

```bash
command -v gopls || go install golang.org/x/tools/gopls@latest
command -v typescript-language-server || npm install -g typescript-language-server typescript
command -v gopls
command -v typescript-language-server
```

Expected: both final commands print executable paths.

- [ ] **Step 3: Build the current worktree sidecar**

Run:

```bash
root="$(git rev-parse --show-toplevel)"
mkdir -p "$root/bin"
go build -o "$root/bin/mcp-lsp" ./cmd/mcp-lsp
test -x "$root/bin/mcp-lsp"
```

Expected: all commands exit 0; binary lives under the current worktree.

- [ ] **Step 4: Verify the ignored project-local config prepared by this bootstrap session**

The current session has already created `.codex/config.toml`. Verify every managed value derives from the current root without recording that machine path in tracked documentation:

```bash
root="$(git rev-parse --show-toplevel)"
test -f .codex/config.toml
rg -F "command = \"$root/bin/mcp-lsp\"" .codex/config.toml
rg -F "cwd = \"$root\"" .codex/config.toml
rg -F "SUPER_DOLPHIN_RUNTIME_MODE = \"dev\"" .codex/config.toml
rg -F "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = \"$root\"" .codex/config.toml
rg -F "GO_AGENT_LSP_ROOT = \"$root\"" .codex/config.toml
rg -F "GO_AGENT_LSP_ROOTS = \"[\\\"$root\\\"]\"" .codex/config.toml
```

Expected: every search matches once; `chmod 600 .codex/config.toml` succeeds on Unix and `git status --short` does not list the ignored file.

- [ ] **Step 5: Verify Codex discovery**

Run:

```bash
codex mcp get lsp
codex mcp list
```

Expected: enabled stdio server `lsp`; command and cwd point to this worktree.

- [ ] **Step 6: Create a new Codex task and exercise the tool surface**

In the newly created task, call all of:

```text
grep(text_search, query="func main()", path="cmd/mcp-lsp")
structure(document_symbol, file_path="cmd/mcp-lsp/main.go")
inspect(definition, pos="cmd/mcp-lsp/main.go:31:23")
xref(references, pos="internal/platform/runtimeenv/runtimeenv.go:142:1")
file(read_file, pos="cmd/mcp-lsp/main.go:24", limit=20)
file(diagnostics, file_path="cmd/mcp-lsp/main.go")
completion(pos="cmd/mcp-lsp/main.go:31:23")
```

Create a temporary ignored fixture under `.codex/lsp-smoke/`, exercise `patch_edit(replace_range)`, then remove it.

Expected: seven tools are present, every path resolves inside the current worktree, Go semantic calls use gopls, and diagnostics complete. Any diagnostic or tool failure blocks Task 1.

### Task 1: Resolve and preflight worktree-owned paths

**Files:**
- Create: `cmd/codex-worktree-setup/paths.go`
- Create: `cmd/codex-worktree-setup/paths_test.go`
- Create: `cmd/codex-worktree-setup/main.go`

- [ ] **Step 1: Write failing path/preflight tests**

```go
func TestResolvePathsRejectsBinaryOutsideWorktree(t *testing.T) {
	root := t.TempDir()
	_, err := resolvePaths(options{
		Worktree: root,
		Binary: filepath.Join(filepath.Dir(root), "mcp-lsp"),
	})
	if err == nil || !strings.Contains(err.Error(), "binary path must stay inside worktree") {
		t.Fatalf("error = %v", err)
	}
}

func TestPreflightRequiresGoAndTypeScriptServers(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "gopls" { return "/tools/gopls", nil }
		return "", exec.ErrNotFound
	}
	_, err := preflightLanguageServers(lookup)
	if err == nil || !strings.Contains(err.Error(), "npm install -g typescript-language-server typescript") {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run RED**

```bash
go test ./cmd/codex-worktree-setup -run 'TestResolvePaths|TestPreflight' -count=1
```

Expected: FAIL because `options`, `resolvePaths`, and `preflightLanguageServers` do not exist.

- [ ] **Step 3: Implement the minimal command model**

```go
type command string

const (
	commandConfigure command = "configure"
	commandReady command = "ready"
	commandVerify command = "verify"
)

type options struct {
	Command command
	Worktree string
	Binary string
	Config string
}

type paths struct {
	Worktree string
	Binary string
	Config string
}
```

`resolvePaths` must use `git rev-parse --show-toplevel` when worktree is empty, select `mcp-lsp.exe` on Windows, canonicalize paths including symlinks/junctions, and reject config/binary outside the canonical worktree.

`preflightLanguageServers` must require exact `gopls` and `typescript-language-server` executables and return the install commands from Task 0 in its errors.

- [ ] **Step 4: Run GREEN and diagnostics**

```bash
go test ./cmd/codex-worktree-setup -run 'TestResolvePaths|TestPreflight' -count=1
```

Expected: PASS. Run LSP diagnostics on `main.go`, `paths.go`, and tests; all severities must be empty.

- [ ] **Step 5: Commit the path boundary**

```bash
git add cmd/codex-worktree-setup/main.go cmd/codex-worktree-setup/paths.go cmd/codex-worktree-setup/paths_test.go
git commit -m "feat(codex): resolve worktree LSP paths"
```

### Task 2: Manage the project-local TOML atomically

**Files:**
- Create: `cmd/codex-worktree-setup/config.go`
- Create: `cmd/codex-worktree-setup/config_test.go`
- Create: `cmd/codex-worktree-setup/atomic_unix.go`
- Create: `cmd/codex-worktree-setup/atomic_windows.go`

- [ ] **Step 1: Write failing managed-block tests**

```go
func TestRenderConfigPreservesUserBytesAndIsIdempotent(t *testing.T) {
	original := []byte("# user setting\nservice_tier = \"fast\"\n")
	p := paths{Worktree: "/repo", Binary: "/repo/bin/mcp-lsp", Config: "/repo/.codex/config.toml"}
	first, err := renderConfig(original, p, "/usr/bin")
	if err != nil { t.Fatal(err) }
	second, err := renderConfig(first, p, "/usr/bin")
	if err != nil { t.Fatal(err) }
	if string(first) != string(second) { t.Fatalf("not idempotent") }
	if !bytes.HasPrefix(first, original) { t.Fatalf("user bytes changed") }
}

func TestRenderConfigRejectsUnmanagedLSPTree(t *testing.T) {
	_, err := renderConfig([]byte("[mcp_servers.lsp]\ncommand=\"user\"\n"), paths{}, "/usr/bin")
	if err == nil || !strings.Contains(err.Error(), "unmanaged mcp_servers.lsp") { t.Fatalf("error=%v", err) }
}
```

- [ ] **Step 2: Run RED**

```bash
go test ./cmd/codex-worktree-setup -run 'TestRenderConfig|TestWriteConfig' -count=1
```

Expected: FAIL because renderer/writer do not exist.

- [ ] **Step 3: Implement the owned block**

Use exact markers:

```go
const managedBegin = "# BEGIN SUPER-DOLPHIN MANAGED LSP"
const managedEnd = "# END SUPER-DOLPHIN MANAGED LSP"
```

Encode the managed subtree with typed BurntSushi TOML structs. Encode `GO_AGENT_LSP_ROOTS` with `json.Marshal([]string{worktree})`. Include `PATH` plus the four runtime/root env values from Task 0. Reject malformed/duplicate markers and any unmanaged `mcp_servers.lsp` subtree. Remove an existing owned block and append exactly one replacement at EOF.

Write in the same directory, fsync, set Unix mode `0600`, and atomically replace; Windows uses replace-existing/write-through semantics without loosening inherited ACLs.

- [ ] **Step 4: Run GREEN and cross-platform compile**

```bash
go test ./cmd/codex-worktree-setup -run 'TestRenderConfig|TestWriteConfig' -count=1
tmp="$(mktemp -d)"
GOOS=windows GOARCH=amd64 go test -c -o "$tmp/setup.test.exe" ./cmd/codex-worktree-setup
rm -rf "$tmp"
```

Expected: both commands PASS. LSP diagnostics on new production/test files are empty.

- [ ] **Step 5: Commit TOML ownership**

```bash
git add cmd/codex-worktree-setup/config.go cmd/codex-worktree-setup/config_test.go cmd/codex-worktree-setup/atomic_unix.go cmd/codex-worktree-setup/atomic_windows.go
git commit -m "feat(codex): manage worktree LSP config"
```

### Task 3: Implement ready/configure/verify and real MCP probe

**Files:**
- Create: `cmd/codex-worktree-setup/mcp_probe.go`
- Create: `cmd/codex-worktree-setup/mcp_probe_test.go`
- Modify: `cmd/codex-worktree-setup/main.go`
- Create: `cmd/codex-worktree-setup/main_test.go`

- [ ] **Step 1: Write RED orchestration tests**

```go
func TestReadyBuildsBeforeConfigureAndProbe(t *testing.T) {
	order := []string{}
	deps := fakeDeps{
		build: func() error { order = append(order, "build"); return nil },
		configure: func() error { order = append(order, "configure"); return nil },
		preflight: func() error { order = append(order, "preflight"); return nil },
		probe: func() error { order = append(order, "probe"); return nil },
	}
	if err := runReady(deps); err != nil { t.Fatal(err) }
	if got := strings.Join(order, ","); got != "build,configure,preflight,probe" {
		t.Fatalf("order = %q", got)
	}
}

func TestProbeRejectsMissingShortTool(t *testing.T) {
	err := requireToolNames([]string{"file", "inspect", "xref", "grep", "structure", "patch_edit"})
	if err == nil || !strings.Contains(err.Error(), "completion") { t.Fatalf("error=%v", err) }
}
```

- [ ] **Step 2: Run RED**

```bash
go test ./cmd/codex-worktree-setup -run 'TestReady|TestProbe|TestCLI' -count=1
```

Expected: FAIL because orchestration and probe are absent.

- [ ] **Step 3: Implement minimal pipelines**

```text
configure: resolve -> validate existing worktree binary -> render/write config
ready: resolve -> unconditional `go build -o resolvedBinaryPath ./cmd/mcp-lsp` -> configure -> preflight -> probe
verify: resolve -> read/validate exact config -> validate binary -> preflight -> probe (no writes)
```

The probe must start the real binary with the generated runtime env, send MCP `initialize`, `notifications/initialized`, `tools/list`, then call `file(diagnostics)` for `cmd/mcp-lsp/main.go` and `frontend-app/src/main.jsx` before closing cleanly. Require exact names `file`, `inspect`, `xref`, `grep`, `structure`, `patch_edit`, `completion`, and reject either MCP-level or tool-level diagnostic errors.

`main.go` must require an explicit subcommand, parse `--worktree`, `--binary`, `--config`, print resolved paths/language binaries/tools, return exit 2 for CLI misuse and exit 1 for runtime failure, and state that a new task is required.

- [ ] **Step 4: Run GREEN**

```bash
go test ./cmd/codex-worktree-setup -count=1
```

Expected: PASS with real/fake MCP probe coverage. LSP diagnostics on the package are empty.

- [ ] **Step 5: Commit command orchestration**

```bash
git add cmd/codex-worktree-setup/main.go cmd/codex-worktree-setup/main_test.go cmd/codex-worktree-setup/mcp_probe.go cmd/codex-worktree-setup/mcp_probe_test.go
git commit -m "feat(codex): verify worktree LSP readiness"
```

### Task 4: Linked-worktree integration and workflow documentation

**Files:**
- Create: `cmd/codex-worktree-setup/worktree_integration_test.go`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `.agents/skills/使用git工作区/SKILL.md`

- [ ] **Step 1: Write the linked-worktree RED test**

Create a temporary Git repository, commit a minimal fake `cmd/mcp-lsp` plus Go and JavaScript probe files, create a linked worktree, put fake executable `gopls`, `typescript-language-server`, and `tsserver` on `PATH`, run `ready`, then `verify`. The fake MCP server must require both diagnostics calls before shutdown and expose a diagnostic-error mode so `verify` is proven to reject an unusable language-server path.

Assertions:

```go
for _, output := range []string{report.Binary, report.Config} {
	rel, err := filepath.Rel(linkedRoot, output)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("output escaped worktree: %q rel=%q err=%v", output, rel, err)
	}
}
before, _ := os.ReadFile(report.Config)
verified, err := execute(commandVerify, linkedRoot)
if err != nil { t.Fatal(err) }
after, _ := os.ReadFile(verified.Config)
if !bytes.Equal(before, after) { t.Fatal("verify modified config") }
```

- [ ] **Step 2: Run RED, implement fixture helpers, then GREEN**

```bash
go test ./cmd/codex-worktree-setup -run TestReadyAndVerifyLinkedWorktree -count=1 -v
```

Expected RED: fixture/helper behavior is missing. Expected GREEN after implementation: PASS with binary/config rooted in linked worktree.

- [ ] **Step 3: Add the convenience target**

```make
.PHONY: codex-worktree-ready
codex-worktree-ready:
	go run ./cmd/codex-worktree-setup ready
```

Run `make -n codex-worktree-ready`; expected command is exactly the Go invocation above.

- [ ] **Step 4: Update README and canonical worktree skill**

Document both:

```bash
go run ./cmd/codex-worktree-setup ready
go run ./cmd/codex-worktree-setup verify
```

State that config/binary are ignored local artifacts, global config is untouched, the command never reuses another checkout, and a new Codex task is mandatory. Update only the canonical repo-local skill, then run the existing mirror consistency check.

- [ ] **Step 5: Validate docs/skill and commit**

```bash
python3 scripts/validate_super_agent_skills.py
git diff --check
git add cmd/codex-worktree-setup/worktree_integration_test.go Makefile README.md .agents/skills/使用git工作区/SKILL.md
git commit -m "docs(codex): add worktree LSP readiness gate"
```

Expected: validator passes and no provider/runtime mirror file is changed unless the validator explicitly requires it.

### Task 5: Implementation-complete and new-task runtime acceptance

**Files:**
- Verify: all Plan 1 files
- Temporary ignored fixture: `.codex/lsp-smoke/fixture.go`

- [ ] **Step 1: Run fresh package and guard verification**

```bash
gofmt -w cmd/codex-worktree-setup/*.go
go test ./cmd/codex-worktree-setup -count=1
./scripts/test_with_guard.sh ./cmd/codex-worktree-setup ./cmd/mcp-lsp -count=1
python3 scripts/validate_super_agent_skills.py
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 2: Run the real ready/verify commands**

```bash
go run ./cmd/codex-worktree-setup ready
go run ./cmd/codex-worktree-setup verify
codex mcp get lsp
codex mcp list
```

Expected: current worktree paths, both language servers, seven short tools, enabled LSP, and new-task reminder.

- [ ] **Step 3: Verify Git integrity**

```bash
git status --short
git diff --check
git ls-files .codex bin frontend-app/node_modules frontend-app/dist
```

Expected: local generated artifacts are not tracked/staged; no machine path enters tracked diff.

- [ ] **Step 4: Create the new task and repeat Task 0 Step 6**

Expected: all seven tools execute against this worktree, `patch_edit` only touches/removes the ignored fixture, and `file(diagnostics)` reports no outstanding severity.

- [ ] **Step 5: Record the gate**

```text
implementation_complete=true
new_task_runtime_accepted=true
server=lsp
tools=file,inspect,xref,grep,structure,patch_edit,completion
go_semantics=gopls
typescript_semantics=typescript-language-server
paths_scoped_to_current_worktree=true
global_codex_config_modified=false
other_checkout_binary_reused=false
```

Only after every value is backed by fresh output may Plans 2 and 3 begin.
