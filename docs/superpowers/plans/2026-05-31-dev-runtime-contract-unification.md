# Dev Runtime Contract Unification Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make development launches use the same runtime contract as packaged products, so business-code changes do not require separate dev/package environment fixes.

**Architecture:** Introduce a reusable dev runtime staging root under `.build-cache/dev-runtime/<platform>` with the same resource layout and manifest contract as packaged builds. Keep packaged mode as the only high-fidelity runtime behavior, use a dev-specific staging launcher to prepare resources and start debug binaries, and leave host-dependency development as an explicit low-fidelity escape hatch.

**Tech Stack:** Go 1.25.7, Wails desktop app, fx lifecycle, embedded PostgreSQL, OpenAI Codex CLI/app-server, MCP sidecars, LSP bundle scripts, shell packaging scripts, Go guard tests.

---

## 1. Executive Summary

The current repository has two runtime worlds:

- Development launchers define a `dev` runtime through `run-debug.sh` and `Makefile`, usually with host PostgreSQL, host Codex, local sidecar bins, and Vite HMR.
- Packaged launchers define a `packaged` runtime with `runtime-manifest.json`, embedded PostgreSQL, bundled Codex, bundled LSP, packaged sidecars, controlled `PATH`, and app-managed provider homes.

That split creates a structural consistency problem: a business change can pass in development while failing in the packaged product because the dependency source, provider home, database lifecycle, LSP toolchain, and sidecar process contract are different.

This plan changes the default development model from "host-deps dev runtime" to "fast staged packaged runtime":

```text
dev command
  -> build debug binaries
  -> prepare .build-cache/dev-runtime/<platform>
  -> write package-compatible runtime-manifest.json
  -> set package-root launch env
  -> start staged bin/agent-terminal with VITE_DEV_URL

release package command
  -> build release binaries
  -> prepare the same resource layout
  -> verify the same runtime-manifest.json contract
  -> wrap as .app / tar.gz / dmg
```

The user-facing development command may remain `./run-debug.sh`, but that script must become a launcher for the runtime contract, not the owner of separate runtime semantics.

## 2. Non-Negotiable Decisions

1. **Do not add a third behavior mode named `packaged-dev`.**
   - Use existing packaged resolution through `SUPER_DOLPHIN_PACKAGE_ROOT` and `SUPER_DOLPHIN_PACKAGED_LAUNCHER`.
   - A diagnostic marker such as `SUPER_DOLPHIN_DEV_STAGING=1` is allowed only for logs, metrics, or test assertions. It must not select different runtime behavior.
   - `SUPER_DOLPHIN_DEV_STAGED_RUNTIME=1` is allowed only as a temporary `run-debug.sh` shell opt-in during rollout Stage 2. It must not be consumed by Go runtime resolution and must not be propagated as a runtime mode.

2. **Do not silently fall back to host PostgreSQL, host Codex, or host LSP.**
   - Host dependencies are allowed only through an explicit low-fidelity escape hatch such as `SUPER_DOLPHIN_DEV_HOST_DEPS=1`.
   - If staged resources are missing, fail with an actionable error.

3. **Do not weaken packaged verification.**
   - Release package checks must remain strict.
   - Dev staging should reuse strict checks where practical; any reduced smoke must be named as a dev smoke and must not replace package verification.

4. **Do not let dev staging pollute real packaged app data.**
   - Dev launches must set isolated `SUPER_DOLPHIN_HOME` and `CODEX_HOME`.
   - Suggested default:

```bash
SUPER_DOLPHIN_HOME="$stage/home/super-dolphin"
CODEX_HOME="$stage/home/codex"
```

5. **Minimum launchable staged runtime requires the full current packaged resource contract.**
   - Current packaged resolution requires a manifest with bundled Codex, bundled gopls, LSP bundle, model registry, and embedded PostgreSQL resource path.
   - A PG-only staging milestone can be unit-tested, but it must not be advertised as a launchable packaged-mode dev runtime.

6. **Business modules must not learn about dev/package differences.**
   - `internal/module/**`, `internal/store/**`, provider business flows, and frontend business stores should consume runtime capabilities or existing config objects, not inspect shell variables directly.

## 3. Current Code Evidence

These files define the current split and are the main implementation surface:

- `run-debug.sh`
  - Currently exports `SUPER_DOLPHIN_RUNTIME_MODE=dev`, a dev `DATABASE_URL`, and starts Vite in debug mode.

- `Makefile`
  - `run-agent-terminal-debug*` exports dev runtime env and points `GO_AGENT_PEER_BIN_DIR` to repo-local `bin`.

- `internal/platform/runtimeenv/runtime_resolution.go`
  - Resolves owner and sidecar runtime modes.
  - Already supports `SUPER_DOLPHIN_PACKAGE_ROOT`, `SUPER_DOLPHIN_PACKAGED_LAUNCHER`, and packaged manifest detection.

- `internal/platform/runtimeenv/runtimeenv.go`
  - Applies packaged runtime env: controlled `PATH`, `GO_AGENT_PEER_BIN_DIR`, LSP env, `PROJECT_ROOT`, `SUPER_DOLPHIN_RUNTIME_MODE=packaged`, `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR`, `SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1`, app-managed homes, and model registry.

- `internal/platform/config/config.go`
  - Loads `.env`, treats packaged `.env` errors as fail-fast, and rejects unsafe `SUPER_DOLPHIN_RUNTIME_MODE=dev` downgrade for a packaged root.

- `internal/platform/embeddedpg/config.go`
  - Enables embedded PostgreSQL in packaged runtime or when explicit embedded PG env is set.

- `internal/provider/codexapp/sidecar_runtime_env.go`
  - Requires sidecars to inherit `SUPER_DOLPHIN_RUNTIME_MODE` and `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR`.

- `scripts/package_macos.sh` and `scripts/package_linux.sh`
  - Build package resource layouts and write `runtime-manifest.json`.
  - Contain logic that should be shared or wrapped for dev staging.

- `scripts/verify_packaged_app_macos.sh` and `scripts/verify_packaged_app_linux.sh`
  - Verify package resource integrity.
  - Dev staging needs a verifier path that checks the same manifest invariants without macOS app wrapper assumptions.

## 4. Target Runtime Model

### 4.1 Runtime Root Layout

The dev runtime root must use this layout:

```text
.build-cache/dev-runtime/<platform>/
  .env
  runtime-manifest.json
  codex-manifest.json
  models.yaml
  bin/
    agent-terminal
    mcp-orch
    mcp-lsp
    mcp-ida
    codex
    gopls
    typescript-language-server
    vscode-css-language-server
    pyright-langserver
    rust-analyzer
    jdtls                    # present only for full LSP profile
  lsp/
    lsp-manifest.json
    lsp-checksums.sha256
    bin/
    node/
    node_modules/
  migrations/
  postgres/<platform>/
    bin/
    share/
  home/
    super-dolphin/
    codex/
```

The release package layouts remain:

```text
macOS:
  dist/package/macos/<App>.app/Contents/MacOS/agent-terminal
  dist/package/macos/<App>.app/Contents/Resources/<same resource layout>

Linux:
  dist/package/linux/<name>-<version>-<platform>/<same resource layout>
  dist/package/linux/<name>-<version>-<platform>/run.sh
```

### 4.2 Launch Environment

High-fidelity dev staging launch must set:

```bash
export SUPER_DOLPHIN_PACKAGE_ROOT="$stage"
export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1
export SUPER_DOLPHIN_DEV_STAGING=1
export SUPER_DOLPHIN_HOME="$stage/home/super-dolphin"
export CODEX_HOME="$stage/home/codex"
export VITE_DEV_URL="http://localhost:5173"
exec "$stage/bin/agent-terminal" --debug "$@"
```

`SUPER_DOLPHIN_DEV_STAGING=1` must not be used by runtime resolver behavior. It may be used by tests and logs to prove a launch was a dev staged launch.

### 4.3 Development Differences Allowed

Only these differences are allowed between dev staging and release package:

- Debug build flags such as `-gcflags="-N -l"` and optional Frida tags.
- Vite HMR through `VITE_DEV_URL`.
- Runtime root path under `.build-cache/dev-runtime/<platform>`.
- Dev-only `SUPER_DOLPHIN_HOME` and `CODEX_HOME` under the staging root.
- Local private relay bootstrap token in staged `.env`, clearly marked non-distributable.
- Optional explicit low-fidelity escape hatch `SUPER_DOLPHIN_DEV_HOST_DEPS=1`.

### 4.4 Development Differences Not Allowed

These differences must not exist in high-fidelity dev staging:

- Host PostgreSQL as the default DB.
- Host Codex binary as the runtime binary without staging/copying it into `bin/codex`.
- Host LSP servers discovered from `PATH`.
- Different sidecar resource root rules.
- Different runtime manifest schema.
- Separate code paths that let dev pass while release package fails.

## 5. Multi-Agent Ownership Map

Use separate agents for independent ownership lanes. Agents must not edit files outside their lane without handing off first.

| Lane | Owner | Primary Files | Dependencies |
| --- | --- | --- | --- |
| A | Runtime manifest contract | `internal/platform/runtimeenv/*`, `cmd/runtime-manifest/*` | none |
| B | Dev runtime staging script | `scripts/dev_runtime.sh`, `scripts/dev_runtime_guard_test.go` | A |
| C | Launch integration | `run-debug.sh`, `Makefile`, `scripts/run_debug_dev_dsn_guard_test.go` | B |
| D | Codex/LSP artifact staging | `scripts/dev_runtime.sh`, `scripts/prepare_lsp_bundle_*`, package guard tests | A, B |
| E | Verifier and smoke tests | `scripts/verify_dev_runtime.sh`, `scripts/*guard_test.go` | A, B, D |
| F | Docs and migration notes | `docs/packaging/*.md`, this plan updates | B, C, E |

Suggested execution order:

```text
A -> B -> E(PG/resource smoke)
B -> D -> E(full resource smoke)
B + E -> C
C -> F
```

Do not run C before E proves the staged root can be created and verified.

### 5.1 Orchestration Requirements

The coordinator may manage this plan with `mcp-go-agent-orchestration` lifecycle tools when persistent DAG state, retry/lease semantics, or structured handoff records are needed. Otherwise, native subagent dispatch plus explicit plan/report state is allowed.

1. If using mcp-orch, create one DAG with `task_create_dag` before assigning implementation work.
2. Start each lane only when its dependencies are satisfied; use the current DAG start/dispatch tools if mcp-orch is selected.
3. Update each lane state after every verification command, blocker, or handoff; use `task_update_node` only when mcp-orch is selected.
4. Do not mark a lane complete from an agent summary alone; inspect changed files and verification output first.

Required DAG shape:

```text
runtime-manifest-contract
  -> dev-runtime-staging-script
  -> dev-runtime-verifier
  -> launch-integration
  -> docs-migration

runtime-manifest-contract
  -> package-script-convergence

dev-runtime-staging-script
  -> codex-lsp-artifact-staging
  -> dev-runtime-verifier
```

Minimum node metadata:

| Node | Task(s) | Exit Evidence |
| --- | --- | --- |
| `runtime-manifest-contract` | Task 1, Task 2, Task 11 | `./scripts/test_with_guard.sh ./internal/platform/runtimeenv ./cmd/runtime-manifest -count=1` |
| `dev-runtime-staging-script` | Task 3, Task 5 | `./scripts/test_with_guard.sh ./scripts -run 'TestDevRuntimeScript' -count=1` |
| `codex-lsp-artifact-staging` | Task 3 resource stages, Task 5 resource copy | `scripts/dev_runtime.sh prepare` reaches manifest write on the target platform |
| `dev-runtime-verifier` | Task 4 | `scripts/verify_dev_runtime.sh <stage-root>` plus verifier guard tests |
| `launch-integration` | Task 6, Task 7, Task 8, Task 9 | run-debug guard tests and recorded manual smoke before default switch |
| `package-script-convergence` | Task 10 | package guard tests and existing packaged verifier tests |
| `docs-migration` | Task 12 | README and packaging docs updated after final command names stabilize |

## 6. File Structure and Responsibilities

### New Files

- `cmd/runtime-manifest/main.go`
  - CLI wrapper for writing and verifying runtime manifests.
  - Supports `write` and `verify` subcommands.
  - Used by package scripts and dev staging scripts to avoid duplicate shell JSON.

- `internal/platform/runtimeenv/manifest.go`
  - Defines the exported runtime manifest schema.
  - Writes manifest JSON.
  - Verifies relative paths and required resources.

- `internal/platform/runtimeenv/manifest_test.go`
  - Tests manifest JSON, path safety, missing resource failures, and platform-specific embedded PostgreSQL path.

- `scripts/dev_runtime.sh`
  - Prepares `.build-cache/dev-runtime/<platform>`.
  - Builds or copies staged resources.
  - Does not start the app unless invoked with `launch`.

- `scripts/verify_dev_runtime.sh`
  - Verifies a staged dev runtime root.
  - Delegates manifest checks to `go run ./cmd/runtime-manifest verify`.
  - Runs targeted smoke checks for staged binaries.

- `scripts/dev_runtime_guard_test.go`
  - Go tests that lock script contracts without requiring full staging.

### Modified Files

- `internal/platform/runtimeenv/runtime_resolution.go`
  - Reuse exported manifest verification helpers.
  - Keep existing packaged mode semantics.
  - Do not add `packaged-dev`.

- `internal/platform/runtimeenv/runtimeenv.go`
  - Continue applying packaged env.
  - Preserve override behavior for `SUPER_DOLPHIN_HOME` and `CODEX_HOME`; tests should lock that dev staging can provide isolated homes.

- `scripts/package_macos.sh`
  - Replace hand-written runtime manifest JSON with `go run ./cmd/runtime-manifest write`.
  - Keep macOS app packaging and codesign logic in shell.

- `scripts/package_linux.sh`
  - Replace hand-written runtime manifest JSON with `go run ./cmd/runtime-manifest write`.
  - Keep Linux tar staging and `run.sh` generation in shell.

- `run-debug.sh`
  - Add high-fidelity staged path behind an opt-in flag first.
  - Later switch the default debug choice to staged runtime after verifier is green.

- `Makefile`
  - Add `dev-runtime-prepare`, `dev-runtime-verify`, and `run-agent-terminal-debug-staged` targets.
  - Do not remove `run-agent-terminal-debug` until staged mode has passed a full local smoke.

- `docs/packaging/embedded-postgres.md`
  - Document high-fidelity dev staging and host-deps escape hatch.

## 7. Acceptance Criteria

The implementation is complete only when all criteria below pass.

### 7.1 Contract Criteria

- One Go manifest writer is used by both dev staging and release packaging.
- Dev staging root contains a valid `runtime-manifest.json`.
- Manifest paths are relative and cannot escape the runtime root.
- A missing staged binary or resource causes a clear failure.
- No new runtime behavior mode named `packaged-dev` exists.

### 7.2 Launch Criteria

- `scripts/dev_runtime.sh prepare` creates the staging root without starting the app.
- `scripts/verify_dev_runtime.sh .build-cache/dev-runtime/<platform>` passes.
- `scripts/dev_runtime.sh launch` starts `bin/agent-terminal` through packaged-mode resolution.
- `VITE_DEV_URL` continues to provide frontend HMR.
- The staged process uses embedded PostgreSQL, not external `DATABASE_URL`.
- The staged process uses staged `bin/codex`, not host `PATH` Codex.
- The staged process uses staged LSP env, not host LSP.

### 7.3 Isolation Criteria

- Staged launch sets `SUPER_DOLPHIN_HOME` under the staging root.
- Staged launch sets `CODEX_HOME` under the staging root.
- Staged launch does not write provider mirrors into the macOS app support directory used by release packages.
- Host-deps mode is possible only through an explicit flag and prints a low-fidelity warning.

### 7.4 Regression Criteria

- Existing release package verifier scripts remain strict.
- macOS and Linux package guard tests still pass.
- Dev mode escape hatch still starts the old path for emergency compatibility.
- Business modules do not add direct checks for `SUPER_DOLPHIN_DEV_STAGING`.

## 8. Implementation Tasks

### Task 1: Extract Runtime Manifest Contract

**Files:**
- Create: `internal/platform/runtimeenv/manifest.go`
- Create: `internal/platform/runtimeenv/manifest_test.go`
- Modify: `internal/platform/runtimeenv/runtime_resolution.go`

**Goal:** Move runtime manifest shape and verification rules out of shell-only writers and local unexported structs into one Go contract.

- [ ] **Step 1: Write failing manifest tests**

Add tests to `internal/platform/runtimeenv/manifest_test.go`:

```go
package runtimeenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeManifestWriteAndVerify(t *testing.T) {
	root := t.TempDir()
	platform := "darwin-arm64"
	writeRuntimeManifestFixtureFiles(t, root, platform)

	manifest := RuntimeManifest{
		BundledCodexPath:             "bin/codex",
		BundledGoplsPath:             "bin/gopls",
		LSPBundlePath:                "lsp",
		LSPManifestPath:              "lsp/lsp-manifest.json",
		ModelRegistryPath:            "models.yaml",
		EmbeddedPostgresResourcePath: filepath.Join("postgres", platform),
	}
	if err := WriteRuntimeManifest(filepath.Join(root, runtimeManifestName), manifest); err != nil {
		t.Fatalf("WriteRuntimeManifest() error = %v", err)
	}
	got, err := VerifyRuntimeManifest(root, "darwin", "arm64")
	if err != nil {
		t.Fatalf("VerifyRuntimeManifest() error = %v", err)
	}
	if got.EmbeddedPostgresResourcePath != filepath.Join("postgres", platform) {
		t.Fatalf("embedded postgres path = %q", got.EmbeddedPostgresResourcePath)
	}
}

func TestVerifyRuntimeManifestRejectsEscapingPath(t *testing.T) {
	root := t.TempDir()
	writeRuntimeManifestFixtureFiles(t, root, "darwin-arm64")
	manifest := RuntimeManifest{
		BundledCodexPath:             "../codex",
		BundledGoplsPath:             "bin/gopls",
		LSPBundlePath:                "lsp",
		LSPManifestPath:              "lsp/lsp-manifest.json",
		ModelRegistryPath:            "models.yaml",
		EmbeddedPostgresResourcePath: filepath.Join("postgres", "darwin-arm64"),
	}
	if err := WriteRuntimeManifest(filepath.Join(root, runtimeManifestName), manifest); err != nil {
		t.Fatalf("WriteRuntimeManifest() error = %v", err)
	}
	_, err := VerifyRuntimeManifest(root, "darwin", "arm64")
	if err == nil || !strings.Contains(err.Error(), "bundled_codex_path") {
		t.Fatalf("VerifyRuntimeManifest() error = %v, want path failure", err)
	}
}

func writeRuntimeManifestFixtureFiles(t *testing.T, root, platform string) {
	t.Helper()
	for _, dir := range []string{
		filepath.Join(root, "bin"),
		filepath.Join(root, "lsp"),
		filepath.Join(root, "postgres", platform),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "bin", "codex"),
		filepath.Join(root, "bin", "gopls"),
		filepath.Join(root, "lsp", "lsp-manifest.json"),
		filepath.Join(root, "models.yaml"),
	} {
		if err := os.WriteFile(path, []byte("fixture\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -run 'TestRuntimeManifest|TestVerifyRuntimeManifest' -count=1
```

Expected: fails because `RuntimeManifest`, `WriteRuntimeManifest`, and `VerifyRuntimeManifest` are not exported yet.

- [ ] **Step 3: Implement manifest contract**

Create `internal/platform/runtimeenv/manifest.go` with:

```go
package runtimeenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type RuntimeManifest struct {
	BundledCodexPath             string `json:"bundled_codex_path"`
	BundledGoplsPath             string `json:"bundled_gopls_path"`
	LSPBundlePath                string `json:"lsp_bundle_path"`
	LSPManifestPath              string `json:"lsp_manifest_path"`
	ModelRegistryPath            string `json:"model_registry_path"`
	EmbeddedPostgresResourcePath string `json:"embedded_postgres_resource_path"`
}

func DefaultRuntimeManifest(platform string) RuntimeManifest {
	return RuntimeManifest{
		BundledCodexPath:             "bin/codex",
		BundledGoplsPath:             "bin/gopls",
		LSPBundlePath:                lspBundleName,
		LSPManifestPath:              filepath.Join(lspBundleName, lspManifestName),
		ModelRegistryPath:            modelRegistryBundle,
		EmbeddedPostgresResourcePath: filepath.Join("postgres", platform),
	}
}

func WriteRuntimeManifest(path string, manifest RuntimeManifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write runtime manifest %s: %w", path, err)
	}
	return nil
}

func VerifyRuntimeManifest(resources, goos, goarch string) (RuntimeManifest, error) {
	resources = strings.TrimSpace(resources)
	if resources == "" {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest requires packaged resource root")
	}
	manifestPath := filepath.Join(resources, runtimeManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest %s: %w", manifestPath, err)
	}
	var manifest RuntimeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return RuntimeManifest{}, fmt.Errorf("parse runtime manifest %s: %w", manifestPath, err)
	}
	platform := firstNonEmpty(goos, runtime.GOOS) + "-" + firstNonEmpty(goarch, runtime.GOARCH)
	checks := []struct {
		label string
		value string
		want  string
		kind  string
	}{
		{"bundled_codex_path", manifest.BundledCodexPath, "bin/codex", "exec"},
		{"bundled_gopls_path", manifest.BundledGoplsPath, "bin/gopls", "exec"},
		{"lsp_bundle_path", manifest.LSPBundlePath, lspBundleName, "dir"},
		{"lsp_manifest_path", manifest.LSPManifestPath, filepath.Join(lspBundleName, lspManifestName), "file"},
		{"model_registry_path", manifest.ModelRegistryPath, modelRegistryBundle, "file"},
		{"embedded_postgres_resource_path", manifest.EmbeddedPostgresResourcePath, filepath.Join("postgres", platform), "dir"},
	}
	for _, check := range checks {
		if err := verifyManifestResource(resources, check.label, check.value, check.want, check.kind); err != nil {
			return RuntimeManifest{}, err
		}
	}
	return manifest, nil
}
```

Move the existing resource-path checking helper logic from `runtime_resolution.go` into the same file or keep private helpers in `runtime_resolution.go` if the exported function can reuse them without duplication.

- [ ] **Step 4: Update runtime resolution to use the exported verifier**

In `internal/platform/runtimeenv/runtime_resolution.go`, replace the local `runtimeManifest` struct and `verifyRuntimeManifest` body with calls to `VerifyRuntimeManifest`. Keep returned `manifestPath` semantics by returning `filepath.Join(resources, runtimeManifestName)` after verification.

- [ ] **Step 5: Run runtimeenv tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -count=1
```

Expected: pass.

### Task 2: Add Runtime Manifest CLI

**Files:**
- Create: `cmd/runtime-manifest/main.go`
- Create: `cmd/runtime-manifest/main_test.go`

**Goal:** Give shell scripts one stable CLI for writing and verifying manifests.

- [ ] **Step 1: Write CLI tests**

Create `cmd/runtime-manifest/main_test.go` with tests that execute the command through `go run` only if needed, or test the argument parser directly. Minimum parser behavior:

```go
package main

import "testing"

func TestParseWriteArgs(t *testing.T) {
	args := []string{"write", "--root", "/tmp/runtime", "--platform", "darwin-arm64"}
	cfg, err := parseArgs(args)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if cfg.command != "write" || cfg.root != "/tmp/runtime" || cfg.platform != "darwin-arm64" {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestParseRejectsMissingRoot(t *testing.T) {
	_, err := parseArgs([]string{"verify", "--platform", "darwin-arm64"})
	if err == nil {
		t.Fatal("parseArgs() error = nil, want missing root")
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/runtime-manifest -count=1
```

Expected: fails because command does not exist.

- [ ] **Step 3: Implement CLI**

`cmd/runtime-manifest/main.go` must support:

```bash
go run ./cmd/runtime-manifest write --root <runtime-root> --platform <goos-goarch>
go run ./cmd/runtime-manifest verify --root <runtime-root> --platform <goos-goarch>
```

Implementation contract:

```go
type cliConfig struct {
	command  string
	root     string
	platform string
}
```

Behavior:

- `write` calls `runtimeenv.WriteRuntimeManifest(filepath.Join(root, "runtime-manifest.json"), runtimeenv.DefaultRuntimeManifest(platform))`.
- `verify` splits platform into `goos` and `goarch`, then calls `runtimeenv.VerifyRuntimeManifest(root, goos, goarch)`.
- Missing or malformed args return non-zero with a direct error.
- Do not infer platform from host if `--platform` is missing; scripts must pass it.

- [ ] **Step 4: Run command tests**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/runtime-manifest ./internal/platform/runtimeenv -count=1
```

Expected: pass.

### Task 3: Add Dev Runtime Staging Script

**Files:**
- Create: `scripts/dev_runtime.sh`
- Create: `scripts/dev_runtime_guard_test.go`

**Goal:** Prepare a full high-fidelity dev runtime root without changing existing `run-debug.sh` behavior.

- [ ] **Step 1: Write guard tests for script contract**

Create `scripts/dev_runtime_guard_test.go`:

```go
package main

import "testing"

func TestDevRuntimeScriptUsesPackagedLaunchContract(t *testing.T) {
	script := readScript(t, "dev_runtime.sh")
	assertScriptContains(t, script, "SUPER_DOLPHIN_PACKAGE_ROOT")
	assertScriptContains(t, script, "SUPER_DOLPHIN_PACKAGED_LAUNCHER=1")
	assertScriptContains(t, script, "SUPER_DOLPHIN_DEV_STAGING=1")
	assertScriptContains(t, script, "SUPER_DOLPHIN_HOME=\"$stage/home/super-dolphin\"")
	assertScriptContains(t, script, "CODEX_HOME=\"$stage/home/codex\"")
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_RUNTIME_MODE=dev")
}

func TestDevRuntimeScriptFailsWithoutRequiredArtifacts(t *testing.T) {
	script := readScript(t, "dev_runtime.sh")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_ARTIFACT")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN")
	assertScriptContains(t, script, "SUPER_DOLPHIN_POSTGRES_DIST")
	assertScriptContains(t, script, "missing Codex artifact")
	assertScriptContains(t, script, "missing PostgreSQL dist")
}
```

- [ ] **Step 2: Run the failing script tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestDevRuntimeScript' -count=1
```

Expected: fails because `scripts/dev_runtime.sh` does not exist.

- [ ] **Step 3: Implement `scripts/dev_runtime.sh`**

Required CLI:

```bash
scripts/dev_runtime.sh prepare
scripts/dev_runtime.sh verify
scripts/dev_runtime.sh launch --debug
```

Required top-level behavior:

```bash
#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
platform="$goos-$goarch"
stage="${SUPER_DOLPHIN_DEV_RUNTIME_ROOT:-$root/.build-cache/dev-runtime/$platform}"
lsp_profile="${SUPER_DOLPHIN_LSP_PROFILE:-standard}"
```

Required resource inputs:

- `SUPER_DOLPHIN_POSTGRES_DIST`, or a platform default that already exists.
- `SUPER_DOLPHIN_CODEX_ARTIFACT`, unless `SUPER_DOLPHIN_DEV_USE_HOST_CODEX_ARTIFACT=1` is set.
- `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL`.
- `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN`.

Required stages:

1. Create `bin`, `home/super-dolphin`, `home/codex`, `postgres/<platform>`.
2. Copy debug binaries if they exist under `bin/` or build them when `prepare` is invoked with `SUPER_DOLPHIN_DEV_BUILD_BINARIES=1`.
3. Copy `migrations`.
4. Copy `cmd/mcp-orch/tools/modelregistry/models.yaml`.
5. Prepare LSP bundle by calling the existing platform script:

```bash
case "$goos" in
  darwin) lsp_prepare_script="$root/scripts/prepare_lsp_bundle_macos.sh" ;;
  linux) lsp_prepare_script="$root/scripts/prepare_lsp_bundle_linux.sh" ;;
  *) echo "unsupported dev runtime LSP platform: $goos" >&2; exit 1 ;;
esac

SUPER_DOLPHIN_LSP_PROFILE="$lsp_profile" \
SUPER_DOLPHIN_LSP_BUNDLE_DIR="$root/.build-cache/lsp/$lsp_profile/$platform" \
  "$lsp_prepare_script"
```

6. Copy LSP bundle into `$stage/lsp`.
7. Copy required LSP executables into `$stage/bin` if package scripts require that layout.
8. Copy PostgreSQL dist into `$stage/postgres/$platform`.
9. Copy Codex artifact into `$stage/bin/codex` and mark executable.
10. Write `$stage/.env` with relay base URL and bootstrap token only:

```bash
SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=<value>
SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<value>
```

11. Write `codex-manifest.json` with path, version, source SHA-256, and package SHA-256.
12. Run:

```bash
go run ./cmd/runtime-manifest write --root "$stage" --platform "$platform"
```

- [ ] **Step 4: Add explicit host-deps refusal**

If `SUPER_DOLPHIN_DEV_HOST_DEPS=1` is set, `scripts/dev_runtime.sh prepare` must fail with:

```text
dev runtime staging is disabled by SUPER_DOLPHIN_DEV_HOST_DEPS=1; run run-debug host-deps path instead
```

This prevents mixed high-fidelity staging and low-fidelity host mode in one process.

- [ ] **Step 5: Run script guard tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestDevRuntimeScript' -count=1
```

Expected: pass.

### Task 4: Add Dev Runtime Verifier

**Files:**
- Create: `scripts/verify_dev_runtime.sh`
- Modify: `scripts/dev_runtime_guard_test.go`

**Goal:** Verify staged runtime roots before any launch integration.

- [ ] **Step 1: Add guard tests for verifier requirements**

Extend `scripts/dev_runtime_guard_test.go`:

```go
func TestVerifyDevRuntimeScriptChecksRuntimeManifestAndHomes(t *testing.T) {
	script := readScript(t, "verify_dev_runtime.sh")
	assertScriptContains(t, script, "runtime-manifest")
	assertScriptContains(t, script, "codex-manifest.json")
	assertScriptContains(t, script, "lsp/lsp-manifest.json")
	assertScriptContains(t, script, "home/super-dolphin")
	assertScriptContains(t, script, "home/codex")
	assertScriptContains(t, script, "bin/agent-terminal")
	assertScriptContains(t, script, "bin/codex")
}
```

- [ ] **Step 2: Implement `scripts/verify_dev_runtime.sh`**

Required CLI:

```bash
scripts/verify_dev_runtime.sh <stage-root>
```

Required checks:

- `<stage-root>` exists and is a directory.
- `runtime-manifest.json` passes:

```bash
go run ./cmd/runtime-manifest verify --root "$stage" --platform "$(go env GOOS)-$(go env GOARCH)"
```

- `.env` exists and contains relay base URL and bootstrap token.
- `.env` does not contain `SUPER_DOLPHIN_CODEX_RELAY_API_KEY`.
- `codex-manifest.json` exists.
- `bin/codex app-server --help` succeeds with external Codex paths hidden:

```bash
env -i PATH="$stage/bin:/usr/bin:/bin:/usr/sbin:/sbin" HOME="$stage/home/super-dolphin" CODEX_HOME="$stage/home/codex" "$stage/bin/codex" app-server --help
```

- `postgres/<platform>/bin/postgres`, `initdb`, `pg_ctl`, and `pg_config` are executable.
- `postgres/<platform>/share` contains `postgres.bki`.
- `home/super-dolphin` and `home/codex` exist and are directories.

- [ ] **Step 3: Run verifier tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestVerifyDevRuntimeScript|TestDevRuntimeScript' -count=1
```

Expected: pass.

### Task 5: Build Debug Binaries Into Dev Runtime

**Files:**
- Modify: `scripts/dev_runtime.sh`
- Modify: `run-debug.sh` only if needed for shared build helpers

**Goal:** Make the staged runtime launchable by compiling debug binaries into `$stage/bin`.

- [ ] **Step 1: Add binary build mode to `scripts/dev_runtime.sh`**

Support:

```bash
scripts/dev_runtime.sh build-binaries
```

This command must compile:

```bash
go build -gcflags="-N -l" -o "$stage/bin/agent-terminal" ./cmd/agent-terminal
go build -gcflags="-N -l" -o "$stage/bin/mcp-orch" ./cmd/mcp-orch
go build -gcflags="-N -l" -o "$stage/bin/mcp-lsp" ./cmd/mcp-lsp
go build -gcflags="-N -l" -o "$stage/bin/mcp-ida" ./cmd/mcp-ida
```

If Frida debug is required, the script may accept `SUPER_DOLPHIN_DEV_FRIDA=1` and use the existing `cmd/frida-bootstrap` flow. The first implementation should support plain debug binaries before Frida.

- [ ] **Step 2: Ensure frontend embed is available**

Before `go build ./cmd/agent-terminal`, ensure `cmd/agent-terminal/frontend/dist` exists. If `VITE_DEV_URL` will be used and `dist` is missing, run:

```bash
(cd "$root/cmd/agent-terminal/frontend" && npm ci && npm run build)
```

This build is only to satisfy Go embed; Vite HMR remains the active UI path at runtime.

- [ ] **Step 3: Verify binaries**

Run:

```bash
SUPER_DOLPHIN_DEV_BUILD_BINARIES=1 scripts/dev_runtime.sh prepare
scripts/verify_dev_runtime.sh ".build-cache/dev-runtime/$(go env GOOS)-$(go env GOARCH)"
```

Expected: verifier passes.

### Task 6: Launch Staged Runtime Without Changing `run-debug` Default

**Files:**
- Modify: `scripts/dev_runtime.sh`
- Create or modify: `scripts/dev_runtime_launch_guard_test.go`

**Goal:** Add a launch path for manual smoke while keeping current developer entrypoints unchanged.

- [ ] **Step 1: Add launch command contract tests**

Create `scripts/dev_runtime_launch_guard_test.go`:

```go
package main

import "testing"

func TestDevRuntimeLaunchUsesPackagedRootAndVite(t *testing.T) {
	script := readScript(t, "dev_runtime.sh")
	assertScriptContains(t, script, "launch)")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_PACKAGE_ROOT=\"$stage\"")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1")
	assertScriptContains(t, script, "export VITE_DEV_URL=\"${VITE_DEV_URL:-http://localhost:5173}\"")
	assertScriptContains(t, script, "exec \"$stage/bin/agent-terminal\"")
}
```

- [ ] **Step 2: Implement launch**

`scripts/dev_runtime.sh launch` must:

1. Verify the stage root.
2. Start or require Vite dev server.
3. Export packaged launch env.
4. Export isolated homes.
5. Execute `$stage/bin/agent-terminal --debug`.

Required launch snippet:

```bash
export SUPER_DOLPHIN_PACKAGE_ROOT="$stage"
export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1
export SUPER_DOLPHIN_DEV_STAGING=1
export SUPER_DOLPHIN_HOME="$stage/home/super-dolphin"
export CODEX_HOME="$stage/home/codex"
export VITE_DEV_URL="${VITE_DEV_URL:-http://localhost:5173}"
exec "$stage/bin/agent-terminal" --debug "$@"
```

- [ ] **Step 3: Run launch guard tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestDevRuntime.*Launch|TestDevRuntimeScript' -count=1
```

Expected: pass.

### Task 7: Add Makefile Targets for Staged Dev Runtime

**Files:**
- Modify: `Makefile`
- Modify: `scripts/dev_control_token_guard_test.go` or create `scripts/dev_runtime_makefile_guard_test.go`

**Goal:** Provide stable non-interactive entrypoints for multi-agent verification and CI-like local usage.

- [ ] **Step 1: Add Makefile guard tests**

Create `scripts/dev_runtime_makefile_guard_test.go`:

```go
package main

import "testing"

func TestMakefileExposesDevRuntimeTargets(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	assertScriptContains(t, makefile, "dev-runtime-prepare:")
	assertScriptContains(t, makefile, "dev-runtime-verify:")
	assertScriptContains(t, makefile, "run-agent-terminal-debug-staged:")
	assertScriptContains(t, makefile, "./scripts/dev_runtime.sh prepare")
	assertScriptContains(t, makefile, "./scripts/verify_dev_runtime.sh")
	assertScriptContains(t, makefile, "./scripts/dev_runtime.sh launch")
}
```

- [ ] **Step 2: Modify `Makefile`**

Add targets:

```make
.PHONY: dev-runtime-prepare dev-runtime-verify run-agent-terminal-debug-staged

dev-runtime-prepare:
	./scripts/dev_runtime.sh prepare

dev-runtime-verify:
	./scripts/verify_dev_runtime.sh ".build-cache/dev-runtime/$$(go env GOOS)-$$(go env GOARCH)"

run-agent-terminal-debug-staged: dev-runtime-prepare dev-runtime-verify
	./scripts/dev_runtime.sh launch
```

- [ ] **Step 3: Run Makefile guard tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestMakefileExposesDevRuntimeTargets' -count=1
```

Expected: pass.

### Task 8: Integrate Staged Runtime Into `run-debug.sh` Behind Opt-In

**Files:**
- Modify: `run-debug.sh`
- Modify: `scripts/run_debug_dev_dsn_guard_test.go`
- Create: `scripts/run_debug_staged_guard_test.go`

**Goal:** Let developers test staged runtime through the familiar script without switching the default yet.

- [ ] **Step 1: Add staged run-debug guard test**

Create `scripts/run_debug_staged_guard_test.go`:

```go
package main

import "testing"

func TestRunDebugSupportsExplicitStagedRuntime(t *testing.T) {
	script := readScript(t, "../run-debug.sh")
	assertScriptContains(t, script, "SUPER_DOLPHIN_DEV_STAGED_RUNTIME")
	assertScriptContains(t, script, "./scripts/dev_runtime.sh prepare")
	assertScriptContains(t, script, "./scripts/dev_runtime.sh launch")
	assertScriptContains(t, script, "SUPER_DOLPHIN_DEV_HOST_DEPS")
}
```

- [ ] **Step 2: Add opt-in branch to `run-debug.sh`**

Near the top, after project root resolution and `.env` loading:

```bash
if [ "${SUPER_DOLPHIN_DEV_STAGED_RUNTIME:-0}" = "1" ]; then
  if [ "${SUPER_DOLPHIN_DEV_HOST_DEPS:-0}" = "1" ]; then
    echo "SUPER_DOLPHIN_DEV_STAGED_RUNTIME=1 conflicts with SUPER_DOLPHIN_DEV_HOST_DEPS=1" >&2
    exit 1
  fi
  "$PROJECT_DIR/scripts/dev_runtime.sh" prepare
  exec "$PROJECT_DIR/scripts/dev_runtime.sh" launch "$@"
fi
```

Keep existing behavior unchanged when `SUPER_DOLPHIN_DEV_STAGED_RUNTIME` is not set.

- [ ] **Step 3: Run run-debug guard tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestRunDebug.*Staged|TestRunDebugShellExportsDevDSNAndDevMode' -count=1
```

Expected: pass.

### Task 9: Switch `run-debug.sh` Default After Staged Smoke

**Files:**
- Modify: `run-debug.sh`
- Modify: `scripts/run_debug_dev_dsn_guard_test.go`
- Modify: `scripts/run_debug_staged_guard_test.go`
- Modify: `README.md`

**Goal:** Make staged runtime the default only after Task 8 has been manually smoked.

Prerequisite evidence:

```bash
SUPER_DOLPHIN_DEV_STAGED_RUNTIME=1 ./run-debug.sh
```

Manual smoke must verify:

- Desktop opens.
- Embedded PG starts and migrations apply.
- A Codex conversation can start using staged `CODEX_HOME`.
- `mcp-orch` and `mcp-lsp` are launched from `$stage/bin`.
- Vite HMR still updates the frontend.

- [ ] **Step 1: Change default branch**

At the top of `run-debug.sh`, default to staged unless host-deps is explicitly requested:

```bash
if [ "${SUPER_DOLPHIN_DEV_HOST_DEPS:-0}" != "1" ]; then
  "$PROJECT_DIR/scripts/dev_runtime.sh" prepare
  exec "$PROJECT_DIR/scripts/dev_runtime.sh" launch "$@"
fi
```

When `SUPER_DOLPHIN_DEV_HOST_DEPS=1`, print:

```text
WARNING: SUPER_DOLPHIN_DEV_HOST_DEPS=1 runs low-fidelity host dependency mode. It is not accepted as packaged runtime validation.
```

- [ ] **Step 2: Update tests**

Update `scripts/run_debug_dev_dsn_guard_test.go` so old dev DSN assertions apply only to host-deps branch. Keep a test that proves host-deps remains explicit.

- [ ] **Step 3: Run script tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestRunDebug|TestDevRuntime' -count=1
```

Expected: pass.

### Task 10: Reuse Manifest CLI in Package Scripts

**Files:**
- Modify: `scripts/package_macos.sh`
- Modify: `scripts/package_linux.sh`
- Modify: `scripts/package_macos_guard_test.go`
- Modify: `scripts/package_linux_guard_test.go`

**Goal:** Prevent dev staging and release packages from writing different runtime manifests.

- [ ] **Step 1: Update package guard tests**

Modify existing package guard tests to require:

```text
go run ./cmd/runtime-manifest write --root
```

and to reject hand-written `cat > "$resources/runtime-manifest.json"` or `cat > "$bundle_root/runtime-manifest.json"` blocks.

- [ ] **Step 2: Modify macOS package script**

Replace `write_runtime_manifest` shell JSON with:

```bash
write_runtime_manifest() {
  local bundle_root="$1"
  local platform="$2"
  (
    cd "$root"
    go run ./cmd/runtime-manifest write --root "$bundle_root" --platform "$platform"
  )
}
```

- [ ] **Step 3: Modify Linux package script**

Replace `write_runtime_manifest` shell JSON with:

```bash
write_runtime_manifest() {
  local bundle_root="$1"
  (
    cd "$root"
    go run ./cmd/runtime-manifest write --root "$bundle_root" --platform "$platform"
  )
}
```

- [ ] **Step 4: Run package guard tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackage.*RuntimeManifest|TestPackage.*ScriptWritesRuntimeManifest' -count=1
```

Expected: pass.

### Task 11: Add Runtime Capability Smoke Test

**Files:**
- Create: `internal/platform/runtimeenv/dev_staging_contract_test.go`
- Modify: `internal/platform/runtimeenv/runtimeenv_test.go`

**Goal:** Lock that a dev staging root uses packaged mode without a new mode and honors isolated homes.

- [ ] **Step 1: Add test**

Create a fixture that writes a valid runtime root, then calls `ResolveRuntime` with env:

```go
env := map[string]string{
	"SUPER_DOLPHIN_PACKAGE_ROOT":      resources,
	"SUPER_DOLPHIN_PACKAGED_LAUNCHER": "1",
	"SUPER_DOLPHIN_DEV_STAGING":       "1",
	"SUPER_DOLPHIN_HOME":              filepath.Join(resources, "home", "super-dolphin"),
	"CODEX_HOME":                      filepath.Join(resources, "home", "codex"),
}
```

Assert:

- `RuntimeMode == RuntimeModePackaged`.
- `PackageResources == resources`.
- `Capabilities.BundledCodex == true`.
- No `RuntimeModeDev` is returned.

- [ ] **Step 2: Run runtime tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -run 'Test.*DevStaging|Test.*Packaged' -count=1
```

Expected: pass.

### Task 12: Documentation and Migration Notes

**Files:**
- Modify: `README.md`
- Modify: `docs/packaging/embedded-postgres.md`
- Create: `docs/packaging/dev-runtime-staging.md`

**Goal:** Make the new model discoverable and set the right expectations for developers and agents.

- [ ] **Step 1: Create dev staging docs**

Create `docs/packaging/dev-runtime-staging.md` with:

- What high-fidelity dev runtime means.
- Required env vars.
- How to prepare and verify.
- How to launch.
- How to use host-deps escape hatch.
- Why host-deps results do not validate package behavior.
- Data isolation rules.

Include commands:

```bash
export SUPER_DOLPHIN_POSTGRES_DIST="$PWD/.build-cache/postgres/16.14/$(go env GOOS)-$(go env GOARCH)"
export SUPER_DOLPHIN_CODEX_ARTIFACT="/absolute/path/to/codex"
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://relay.example"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="local-bootstrap-token"

make dev-runtime-prepare
make dev-runtime-verify
make run-agent-terminal-debug-staged
```

- [ ] **Step 2: Update README**

In README development section, replace dev DB-first guidance with:

```bash
make dev-runtime-prepare
make dev-runtime-verify
make run-agent-terminal-debug-staged
```

Keep host-deps escape hatch documented below it:

```bash
SUPER_DOLPHIN_DEV_HOST_DEPS=1 ./run-debug.sh
```

- [ ] **Step 3: Run docs-independent checks**

No docs-specific generator is required unless code map references are updated. If code map files are modified, run:

```bash
make codemap-check
```

## 9. Verification Matrix

Run the narrow checks after each ownership lane:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -count=1
./scripts/test_with_guard.sh ./cmd/runtime-manifest -count=1
./scripts/test_with_guard.sh ./scripts -run 'TestDevRuntime|TestVerifyDevRuntime|TestPackage.*RuntimeManifest|TestRunDebug' -count=1
```

Run staged runtime verification:

```bash
scripts/dev_runtime.sh prepare
scripts/verify_dev_runtime.sh ".build-cache/dev-runtime/$(go env GOOS)-$(go env GOARCH)"
```

Run release script guard checks:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOS|TestPackageLinux|TestVerifyPackaged' -count=1
```

Before declaring the migration complete:

```bash
make guard
./scripts/test_with_guard.sh ./internal/platform/runtimeenv ./internal/platform/embeddedpg ./internal/provider/codexapp ./scripts ./cmd/runtime-manifest -count=1
```

If package scripts are changed:

```bash
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
scripts/verify_packaged_app_linux.sh "dist/package/linux/<stage-dir>"
```

Use the exact stage path produced by the package script.

## 10. Rollout Plan

### Stage 1: Non-invasive

- Add manifest contract and CLI.
- Add `scripts/dev_runtime.sh prepare`.
- Add `scripts/verify_dev_runtime.sh`.
- Do not change `run-debug.sh` default.

Expected risk: low.

### Stage 2: Manual high-fidelity smoke

- Use `SUPER_DOLPHIN_DEV_STAGED_RUNTIME=1 ./run-debug.sh`.
- Verify embedded PG, staged Codex, staged LSP, sidecars, and Vite HMR.

Expected risk: medium, isolated to opt-in users.

### Stage 3: Default switch

- Make staged runtime default.
- Keep `SUPER_DOLPHIN_DEV_HOST_DEPS=1` as an explicit escape hatch.
- Update README.

Expected risk: medium.

### Stage 4: Package script convergence

- Make package scripts call manifest CLI.
- Verify macOS and Linux package scripts.

Expected risk: medium-high because package scripts are release critical.

## 11. Risk Register

| Risk | Severity | Mitigation |
| --- | --- | --- |
| Dev staging uses app-managed release data directories | High | Launcher must set `SUPER_DOLPHIN_HOME` and `CODEX_HOME` under stage. Runtime tests must lock env override behavior. |
| Codex relay token leaks through docs or git | High | `.env` stays in `.build-cache`; docs use fake examples; verifier rejects privileged API key. |
| Staging script silently uses host Codex | High | Require `SUPER_DOLPHIN_CODEX_ARTIFACT` unless `SUPER_DOLPHIN_DEV_USE_HOST_CODEX_ARTIFACT=1` is explicitly set; always copy into `$stage/bin/codex`. |
| Dev staging and package scripts diverge again | High | Package scripts and dev staging both call `cmd/runtime-manifest`; guard tests reject hand-written manifest JSON. |
| Vite HMR masks missing embedded frontend dist | Medium | Ensure `cmd/agent-terminal/frontend/dist` exists before Go build; runtime still uses Vite proxy when set. |
| LSP bundle preparation is slow | Medium | Reuse `.build-cache/lsp/<profile>/<platform>` and checksums; only recopy when manifest/checksum changes. |
| Existing developers lose quick host-deps path | Medium | Keep explicit `SUPER_DOLPHIN_DEV_HOST_DEPS=1` and document it as low-fidelity. |
| Release package scripts regress | High | Keep existing package verifier tests and run package-specific guard tests before merging. |

## 12. Completion Criteria

This plan is complete when:

- `make run-agent-terminal-debug-staged` prepares, verifies, and launches a staged runtime.
- `run-debug.sh` default is switched only after staged manual smoke evidence is recorded.
- `SUPER_DOLPHIN_DEV_HOST_DEPS=1 ./run-debug.sh` remains available and prints a low-fidelity warning.
- `runtime-manifest.json` is generated by one Go contract for dev staging and release packaging.
- No behavior branch named `packaged-dev` exists.
- Staged dev runtime uses embedded PG, staged Codex, staged LSP, staged sidecars, and isolated homes.
- Package verifier tests still pass.

## 13. Handoff Notes for Multi-Agent Execution

Use these assignment boundaries:

1. Assign Runtime Manifest Contract to one agent.
   - It owns `internal/platform/runtimeenv` and `cmd/runtime-manifest`.
   - It must finish before staging scripts depend on the CLI.

2. Assign Dev Runtime Script to one agent after manifest CLI lands.
   - It owns `scripts/dev_runtime.sh` and related guard tests.
   - It must not edit `run-debug.sh` default.

3. Assign Verifier to one agent in parallel with script implementation after the CLI interface is known.
   - It owns `scripts/verify_dev_runtime.sh` and verifier guard tests.

4. Assign Launch Integration to one agent after staging and verifier pass.
   - It owns `run-debug.sh`, `Makefile`, and run-debug tests.

5. Assign Package Script Convergence to one agent after manifest CLI is stable.
   - It owns `scripts/package_macos.sh`, `scripts/package_linux.sh`, and package guard tests.

6. Assign Docs to one agent after the command names and env names are stable.
   - It owns README and packaging docs.

Every agent must start with `git status --short`, must not revert unrelated worktree changes, and must report changed files and verification commands. Do not use `git add .`.
