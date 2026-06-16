# Packaged App MVP Reset Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the macOS packaged app work on a clean VM by double-clicking the app and completing one Codex conversation without requiring user-installed PostgreSQL, Docker, or manual environment variables.

**Architecture:** Keep PostgreSQL for MVP because the repository is deeply coupled to pgx/sqlc/PostgreSQL migrations. Package a PostgreSQL runtime inside the app, make the desktop process the only owner of embedded PostgreSQL lifecycle, make MCP/Codex sidecars stateless DB clients, and add a packaged preflight that prepares Codex CLI/config before the UI claims readiness. If this worktree remains unstable, restart from a new worktree with the same ownership contract and only the macOS arm64 happy path first.

**Tech Stack:** Go 1.25.7, Wails desktop app, fx lifecycle, pgx/pgxpool, sqlc migrations, OpenAI Codex CLI/app-server, macOS `.app` bundle, shell packaging scripts.

---

## Scope and non-goals

This plan intentionally targets one narrow MVP path first:

- Platform: macOS arm64 packaged `.app`.
- Database: bundled local PostgreSQL runtime.
- Provider: Codex via app-managed Codex home and MVP relay configuration.
- User journey: clean VM, double-click app, send one Codex message.

Do not implement these in this plan:

- SQLite migration.
- Docker as the default user path.
- Windows packaging.
- Linux packaging beyond preserving existing files if already present.
- Multi-tenant cloud backend.
- Full installer/updater/notarization automation beyond package verification hooks.

Docker may stay as a developer-only helper, but the packaged user path must not require Docker.

## Current worktree assessment

Current worktree: `/Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg`

Observed direction is partially correct:

- Good: replacing default external `DATABASE_URL` with embedded PostgreSQL when env is missing.
- Good: packaging migrations and peer binaries into app resources.
- Good: adding packaged runtime env setup for GUI-launched apps.
- Risk: `cmd/mcp-orch` starts and stops embedded PostgreSQL, so a sidecar can stop the database owned by the desktop app.
- Risk: Codex CLI auto-install is not the same as Codex relay URL/key bootstrap; first Codex conversation can still fail on a clean VM.
- Risk: desktop preflight warns and continues when Codex is unavailable, so the UI can appear ready while Codex is unusable.
- Risk: macOS package checks rely on `pg_config --sharedir` being relocated, which is not guaranteed after copying a PostgreSQL prefix into `.app` resources.
- Risk: multiple resource layout conventions are mixed; peer binaries and app data ownership need one packaged runtime contract.

## File structure and responsibilities

### New or changed files for the preferred continuation path

- `internal/platform/runtimeenv/runtimeenv.go`
  - Owns packaged-mode detection and packaged resource path resolution.
  - Exposes one focused runtime description for macOS app bundles.

- `internal/platform/runtimeenv/runtimeenv_test.go`
  - Locks macOS `.app/Contents/MacOS` and `.app/Contents/Resources/bin` path detection.
  - Locks env variables applied in packaged mode.

- `internal/platform/embeddedpg/config.go`
  - Resolves embedded PostgreSQL paths from packaged runtime or explicit env.
  - Produces a PostgreSQL DSN only when no external `DATABASE_URL` is configured.

- `internal/platform/embeddedpg/runtime.go`
  - Starts/stops PostgreSQL for the owning process only.
  - Initializes data dir, writes runtime config, removes stale pid only when safe.

- `internal/platform/embeddedpg/runtime_test.go`
  - Unit tests for config, stale pid behavior, unsafe paths, and command arguments.

- `internal/platform/db/module.go`
  - Desktop/core DB lifecycle: start embedded PostgreSQL, ensure DB exists, run migrations, verify schema, ping.
  - Stop embedded PostgreSQL only when this process is the owner.

- `internal/platform/db/embedded_postgres_lifecycle_test.go`
  - Locks that failures after embedded start stop the owner runtime.
  - Locks that disabled embedded config does not call runtime start/stop.

- `cmd/mcp-orch/runtime.go`
  - Must not stop desktop-owned embedded PostgreSQL in sidecar mode.
  - If standalone mode keeps embedded PostgreSQL, it must use the full DB lifecycle contract or explicitly require external DB.

- `internal/provider/codexapp/codex_bootstrap.go`
  - New focused file for app-managed Codex home bootstrap.
  - Writes MVP relay configuration into Codex home.
  - Validates Codex CLI can launch `codex app-server --help` with that home.

- `internal/provider/codexapp/codex_bootstrap_test.go`
  - Locks config file content, permission mode, idempotency, and missing relay config failure.

- `internal/provider/codexapp/codex_autoinstall.go`
  - May remain only for obtaining the Codex CLI binary.
  - Must not be treated as full readiness.

- `internal/app/app.go`
  - Runs packaged desktop preflight before normal app startup.
  - Fails fast or exposes explicit preflight error instead of silently continuing when required packaged components are missing.

- `internal/app/desktop_preflight_test.go`
  - Locks preflight sequence and failure behavior.

- `scripts/package_macos.sh`
  - Builds `.app`, copies binaries/migrations/PostgreSQL runtime, verifies bundle closure.
  - Must not require `pg_config --sharedir` to point at the staged `.app` path after copying.

- `scripts/verify_packaged_app_macos.sh`
  - New script to verify a staged app bundle before VM testing.
  - Checks resource files, dylib references, executable bits, migrations, peer binaries, and PostgreSQL share files.

- `docs/运维发布/打包发布/embedded-postgres.md`
  - Documents the single supported MVP path, owner process, app data dirs, and VM acceptance test.

### Files to avoid broad rewrites

- `internal/store/sqlc/**`
  - Do not rewrite store layer for SQLite in this plan.

- `sql/queries/**` and `migrations/**`
  - Do not change schema unless a failing packaged-app test proves a migration issue.

- `cmd/agent-terminal/frontend/**`
  - Do not add UI polish until backend preflight states are reliable.

---

## Decision record

### Decision 1: Do not use Docker as the default packaged user path

Reason: a Docker-based PostgreSQL requires Docker Desktop/Engine and a running daemon on the user's device. That violates the MVP promise that the app can be used by double-clicking the packaged application without installing infrastructure dependencies.

Allowed use: Docker can remain in development docs and CI helpers.

### Decision 2: Do not migrate to SQLite for this MVP

Reason: current code is coupled to PostgreSQL through pgx, pgxpool, sqlc queries, PostgreSQL migrations, schema version checks, and store behavior. Replacing this with SQLite is a larger storage-portability project and should not block the packaging MVP.

### Decision 3: The desktop process owns embedded PostgreSQL

Only `agent-terminal` in packaged desktop mode starts and stops embedded PostgreSQL. MCP sidecars and Codex app-server children are clients only. A sidecar exit must not stop the database.

### Decision 4: Codex auto-install is not Codex readiness

Codex readiness requires all of these:

1. Codex CLI binary is executable.
2. App-managed Codex home exists.
3. MVP relay URL and key are written to local config by the app or installer.
4. `codex app-server --help` or an equivalent non-network health command succeeds with the app-managed home.
5. First message path can use the configured Codex home.

---

## Task 1: Add one packaged runtime contract

**Files:**
- Modify: `internal/platform/runtimeenv/runtimeenv.go`
- Test: `internal/platform/runtimeenv/runtimeenv_test.go`

- [ ] **Step 1: Write failing tests for macOS app resource resolution**

Add tests with these concrete cases:

```go
func TestPackagedRuntimeFromExecutableDetectsMacOSAppMainBinary(t *testing.T) {
	got, ok := PackagedRuntimeFromExecutable("/Applications/Super Dolphin.app/Contents/MacOS/agent-terminal", "/Users/alice")
	if !ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = false, want true")
	}
	if got.ResourcesDir != "/Applications/Super Dolphin.app/Contents/Resources" {
		t.Fatalf("ResourcesDir = %q", got.ResourcesDir)
	}
	if got.BinDir != "/Applications/Super Dolphin.app/Contents/Resources/bin" {
		t.Fatalf("BinDir = %q", got.BinDir)
	}
	if got.AppDataDir != "/Users/alice/Library/Application Support/Super Dolphin" {
		t.Fatalf("AppDataDir = %q", got.AppDataDir)
	}
}

func TestPackagedRuntimeFromExecutableDetectsMacOSResourcePeerBinary(t *testing.T) {
	got, ok := PackagedRuntimeFromExecutable("/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch", "/Users/alice")
	if !ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = false, want true")
	}
	if got.ResourcesDir != "/Applications/Super Dolphin.app/Contents/Resources" {
		t.Fatalf("ResourcesDir = %q", got.ResourcesDir)
	}
}

func TestPackagedRuntimeFromExecutableRejectsDevBinary(t *testing.T) {
	_, ok := PackagedRuntimeFromExecutable("/Users/alice/src/Super-Dolphin/bin/agent-terminal", "/Users/alice")
	if ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = true, want false")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -count=1
```

Expected: FAIL because `PackagedRuntimeFromExecutable` and the returned struct fields are not implemented or do not match this contract.

- [ ] **Step 3: Implement the minimal runtime contract**

Add this exported type and function in `internal/platform/runtimeenv/runtimeenv.go`:

```go
type PackagedRuntime struct {
	ResourcesDir string
	BinDir       string
	MigrationsDir string
	PostgresRoot string
	AppDataDir   string
}

func PackagedRuntimeFromExecutable(executablePath, userHome string) (PackagedRuntime, bool) {
	executablePath = strings.TrimSpace(executablePath)
	userHome = strings.TrimSpace(userHome)
	if executablePath == "" || userHome == "" {
		return PackagedRuntime{}, false
	}
	exeDir := filepath.Dir(executablePath)
	resources := ""
	if filepath.Base(exeDir) == "MacOS" && filepath.Base(filepath.Dir(exeDir)) == "Contents" {
		resources = filepath.Join(filepath.Dir(exeDir), "Resources")
	}
	if filepath.Base(exeDir) == "bin" && filepath.Base(filepath.Dir(exeDir)) == "Resources" && filepath.Base(filepath.Dir(filepath.Dir(exeDir))) == "Contents" {
		resources = filepath.Dir(exeDir)
	}
	if resources == "" {
		return PackagedRuntime{}, false
	}
	return PackagedRuntime{
		ResourcesDir: resources,
		BinDir: filepath.Join(resources, "bin"),
		MigrationsDir: filepath.Join(resources, "migrations"),
		PostgresRoot: filepath.Join(resources, "postgres"),
		AppDataDir: filepath.Join(userHome, "Library", "Application Support", "Super Dolphin"),
	}, true
}
```

Keep `ConfigurePackagedApp` as a thin wrapper around this contract.

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/runtimeenv/runtimeenv.go internal/platform/runtimeenv/runtimeenv_test.go
git commit -m "feat: define packaged runtime contract"
```

---

## Task 2: Make embedded PostgreSQL ownership explicit

**Files:**
- Modify: `internal/contract/config.go`
- Modify: `internal/platform/embeddedpg/config.go`
- Modify: `internal/platform/embeddedpg/runtime.go`
- Test: `internal/platform/embeddedpg/config_test.go`
- Test: `internal/platform/embeddedpg/runtime_test.go`

- [ ] **Step 1: Write failing config test for owner flag**

Add this test:

```go
func TestResolveConfigMarksDesktopAsEmbeddedPostgresOwner(t *testing.T) {
	cfg, dsn := ResolveConfig(ResolveInput{
		GOOS: "darwin",
		GOARCH: "arm64",
		Env: map[string]string{"SUPER_DOLPHIN_PROCESS_ROLE": "desktop"},
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/MacOS/agent-terminal",
		ProjectRoot: "/Applications/Super Dolphin.app/Contents/Resources",
		UserHome: "/Users/alice",
	})
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if !cfg.Owner {
		t.Fatal("Owner = false, want true for desktop role")
	}
	if !strings.Contains(dsn, "super_dolphin") {
		t.Fatalf("dsn = %q, want super_dolphin database", dsn)
	}
}

func TestResolveConfigMarksSidecarAsClientOnly(t *testing.T) {
	cfg, _ := ResolveConfig(ResolveInput{
		GOOS: "darwin",
		GOARCH: "arm64",
		Env: map[string]string{"SUPER_DOLPHIN_PROCESS_ROLE": "sidecar"},
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch",
		ProjectRoot: "/Applications/Super Dolphin.app/Contents/Resources",
		UserHome: "/Users/alice",
	})
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.Owner {
		t.Fatal("Owner = true, want false for sidecar role")
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/embeddedpg -count=1
```

Expected: FAIL because `EmbeddedPostgresConfig.Owner` does not exist or is not populated.

- [ ] **Step 3: Add the owner field**

Modify `internal/contract/config.go`:

```go
type EmbeddedPostgresConfig struct {
	Enabled      bool
	Owner        bool
	BinDir       string
	ShareDir     string
	DataDir      string
	RuntimeDir   string
	LogPath      string
	DatabaseName string
	UserName     string
	Port         int
	ResolveError string
}
```

Modify `internal/platform/embeddedpg/config.go` to set `Owner` from `SUPER_DOLPHIN_PROCESS_ROLE`:

```go
func resolveOwner(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env["SUPER_DOLPHIN_PROCESS_ROLE"]), "desktop")
}
```

When constructing `contract.EmbeddedPostgresConfig`, set:

```go
Owner: resolveOwner(input.Env),
```

- [ ] **Step 4: Make Stop owner-only**

Modify `internal/platform/embeddedpg/runtime.go`:

```go
func Stop(ctx context.Context, cfg contract.EmbeddedPostgresConfig) error {
	if !cfg.Enabled || !cfg.Owner {
		return nil
	}
	// existing pg_ctl stop flow remains below
}
```

Keep `Start` enabled for owner only unless a test proves sidecar standalone requires start. The MVP packaged sidecar path must not start or stop the shared DB.

- [ ] **Step 5: Run tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/embeddedpg -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/contract/config.go internal/platform/embeddedpg/config.go internal/platform/embeddedpg/runtime.go internal/platform/embeddedpg/config_test.go internal/platform/embeddedpg/runtime_test.go
git commit -m "fix: make embedded postgres ownership explicit"
```

---

## Task 3: Prevent mcp-orch sidecars from owning embedded PostgreSQL

**Files:**
- Modify: `cmd/agent-terminal/main.go`
- Modify: `cmd/mcp-orch/main.go`
- Modify: `cmd/mcp-lsp/main.go`
- Modify: `cmd/mcp-ida/main.go`
- Modify: `cmd/mcp-orch/runtime.go`
- Test: `cmd/mcp-orch/runtime_test.go`

- [ ] **Step 1: Write failing role tests**

Create or extend `cmd/mcp-orch/runtime_test.go` with:

```go
func TestMCPOrchEmbeddedPostgresLifecycleDoesNotStopWhenNotOwner(t *testing.T) {
	cfg := &platformconfig.Config{
		EmbeddedPostgres: contract.EmbeddedPostgresConfig{Enabled: true, Owner: false},
	}
	lc := &testLifecycle{}
	registerPoolLifecycle(lc, slog.Default(), nil, cfg)
	if len(lc.hooks) != 0 {
		t.Fatalf("hooks = %d, want 0 when pool is nil", len(lc.hooks))
	}
}
```

If `testLifecycle` does not exist, add a small implementation in the test file:

```go
type testLifecycle struct{ hooks []fx.Hook }
func (l *testLifecycle) Append(h fx.Hook) { l.hooks = append(l.hooks, h) }
```

- [ ] **Step 2: Run test and verify current behavior**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1
```

Expected: FAIL if current lifecycle still starts/stops embedded PostgreSQL in sidecar/client-only mode.

- [ ] **Step 3: Set explicit process role in entrypoints**

In `cmd/agent-terminal/main.go`, before `runtimeenv.ConfigurePackagedApp()`:

```go
_ = os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop")
runtimeenv.ConfigurePackagedApp()
```

In `cmd/mcp-orch/main.go`, `cmd/mcp-lsp/main.go`, and `cmd/mcp-ida/main.go`, before `runtimeenv.ConfigurePackagedApp()`:

```go
_ = os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar")
runtimeenv.ConfigurePackagedApp()
```

- [ ] **Step 4: Remove embedded Start/Stop from mcp-orch sidecar lifecycle**

Modify `cmd/mcp-orch/runtime.go` so `registerPoolLifecycle` only closes the pool:

```go
func registerPoolLifecycle(lc fx.Lifecycle, logger *slog.Logger, pool *pgxpool.Pool, cfg *platformconfig.Config) {
	if pool == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			pool.Close()
			if logger != nil {
				logger.Info("mcp-orch db pool closed")
			}
			return nil
		},
	})
}
```

If standalone mcp-orch still needs embedded PostgreSQL later, add a separate explicit mode after this MVP passes.

- [ ] **Step 5: Run tests**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch ./internal/platform/config ./internal/platform/embeddedpg -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-terminal/main.go cmd/mcp-orch/main.go cmd/mcp-lsp/main.go cmd/mcp-ida/main.go cmd/mcp-orch/runtime.go cmd/mcp-orch/runtime_test.go
git commit -m "fix: keep sidecars from owning embedded postgres"
```

---

## Task 4: Add app-managed Codex bootstrap for MVP relay config

**Files:**
- Create: `internal/provider/codexapp/codex_bootstrap.go`
- Test: `internal/provider/codexapp/codex_bootstrap_test.go`
- Modify: `internal/provider/codexapp/module.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing tests for config bootstrap**

Create `internal/provider/codexapp/codex_bootstrap_test.go`:

```go
func TestEnsureCodexBootstrapWritesRelayConfig(t *testing.T) {
	home := t.TempDir()
	cfg := CodexBootstrapConfig{
		Home: home,
		RelayBaseURL: "https://relay.example.test/v1",
		RelayAPIKey: "test-key",
		ModelProvider: "super-dolphin-relay",
	}
	if err := EnsureCodexBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("EnsureCodexBootstrap() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`model_provider = "super-dolphin-relay"`,
		`base_url = "https://relay.example.test/v1"`,
		`api_key = "test-key"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, text)
		}
	}
}

func TestEnsureCodexBootstrapRequiresRelayURLAndKey(t *testing.T) {
	err := EnsureCodexBootstrap(context.Background(), CodexBootstrapConfig{Home: t.TempDir()})
	if err == nil {
		t.Fatal("EnsureCodexBootstrap() error = nil, want missing relay config error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Codex relay base URL is required") || !strings.Contains(msg, "Codex relay API key is required") {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestEnsureCodexBootstrap' -count=1
```

Expected: FAIL because `CodexBootstrapConfig` and `EnsureCodexBootstrap` do not exist.

- [ ] **Step 3: Implement minimal bootstrap**

Create `internal/provider/codexapp/codex_bootstrap.go`:

```go
package codexapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CodexBootstrapConfig struct {
	Home string
	RelayBaseURL string
	RelayAPIKey string
	ModelProvider string
}

func EnsureCodexBootstrap(ctx context.Context, cfg CodexBootstrapConfig) error {
	var problems []error
	home := strings.TrimSpace(cfg.Home)
	baseURL := strings.TrimSpace(cfg.RelayBaseURL)
	apiKey := strings.TrimSpace(cfg.RelayAPIKey)
	provider := strings.TrimSpace(cfg.ModelProvider)
	if home == "" {
		problems = append(problems, errors.New("Codex home is required"))
	}
	if baseURL == "" {
		problems = append(problems, errors.New("Codex relay base URL is required"))
	}
	if apiKey == "" {
		problems = append(problems, errors.New("Codex relay API key is required"))
	}
	if provider == "" {
		provider = "super-dolphin-relay"
	}
	if err := errors.Join(problems...); err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create Codex home: %w", err)
	}
	content := strings.Join([]string{
		`model_provider = "` + tomlEscape(provider) + `"`,
		`base_url = "` + tomlEscape(baseURL) + `"`,
		`api_key = "` + tomlEscape(apiKey) + `"`,
		``,
	}, "\n")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write Codex config.toml: %w", err)
	}
	return nil
}

func tomlEscape(value string) string {
	value = strings.ReplaceAll(value, `\\`, `\\\\`)
	return strings.ReplaceAll(value, `"`, `\\"`)
}
```

If Codex requires a different exact config schema, replace only the content writer with the schema verified against the installed Codex CLI, and keep the test assertions aligned to that schema.

- [ ] **Step 4: Wire bootstrap into desktop preflight**

In `internal/app/app.go`, update preflight to call a small wrapper that obtains:

- app-managed Codex home from provider shared app home.
- relay URL from `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL`.
- relay key from `SUPER_DOLPHIN_CODEX_RELAY_API_KEY`.

Use this concrete behavior:

```go
func runDesktopPreflight(ctx context.Context) error {
	platformconfig.PrimeProcessEnvironment()
	if err := ensureCodexCLIAvailableForDesktop(ctx); err != nil {
		return err
	}
	if err := ensurePackagedCodexBootstrap(ctx); err != nil {
		return err
	}
	return nil
}
```

Do not warn-and-continue for required packaged MVP readiness.

- [ ] **Step 5: Run package provider tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/app -run 'TestEnsureCodexBootstrap|TestRunDesktopPreflight' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/codexapp/codex_bootstrap.go internal/provider/codexapp/codex_bootstrap_test.go internal/provider/codexapp/module.go internal/app/app.go internal/app/desktop_preflight_test.go
git commit -m "feat: bootstrap codex relay config for packaged app"
```

---

## Task 5: Replace silent desktop preflight warnings with observable readiness failures

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/desktop_preflight_test.go`

- [ ] **Step 1: Write failing test for fail-fast preflight**

Add this test:

```go
func TestRunDesktopPreflightReturnsCodexCLIError(t *testing.T) {
	prev := ensureCodexCLIAvailableForDesktop
	ensureCodexCLIAvailableForDesktop = func(context.Context) error {
		return errors.New("codex cli missing")
	}
	t.Cleanup(func() { ensureCodexCLIAvailableForDesktop = prev })

	err := runDesktopPreflight(context.Background())
	if err == nil {
		t.Fatal("runDesktopPreflight() error = nil, want codex cli missing")
	}
	if !strings.Contains(err.Error(), "codex cli missing") {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run test and verify it fails if warning behavior remains**

Run:

```bash
./scripts/test_with_guard.sh ./internal/app -run TestRunDesktopPreflightReturnsCodexCLIError -count=1
```

Expected: FAIL when preflight logs and returns nil.

- [ ] **Step 3: Return required preflight errors**

Modify `runDesktopPreflight` so required checks return errors. The minimal body should be:

```go
func runDesktopPreflight(ctx context.Context) error {
	platformconfig.PrimeProcessEnvironment()
	if err := ensureCodexCLIAvailableForDesktop(ctx); err != nil {
		return fmt.Errorf("desktop preflight: Codex CLI unavailable: %w", err)
	}
	if err := ensurePackagedCodexBootstrap(ctx); err != nil {
		return fmt.Errorf("desktop preflight: Codex bootstrap failed: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run app tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/desktop_preflight_test.go
git commit -m "fix: fail fast when packaged desktop preflight fails"
```

---

## Task 6: Fix macOS package verification around PostgreSQL share files

**Files:**
- Modify: `scripts/package_macos.sh`
- Create: `scripts/verify_packaged_app_macos.sh`
- Modify: `docs/运维发布/打包发布/embedded-postgres.md`

- [ ] **Step 1: Add bundle verification script**

Create `scripts/verify_packaged_app_macos.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

app="${1:-}"
if [[ -z "$app" || ! -d "$app/Contents" ]]; then
  echo "usage: $0 /path/to/Super Dolphin.app" >&2
  exit 2
fi

resources="$app/Contents/Resources"
macos="$app/Contents/MacOS"
platform="$(go env GOOS)-$(go env GOARCH)"
pg="$resources/postgres/$platform"

required_execs=(
  "$macos/agent-terminal"
  "$resources/bin/mcp-orch"
  "$resources/bin/mcp-lsp"
  "$resources/bin/mcp-ida"
  "$pg/bin/postgres"
  "$pg/bin/initdb"
  "$pg/bin/pg_ctl"
  "$pg/bin/pg_config"
)

for path in "${required_execs[@]}"; do
  if [[ ! -x "$path" ]]; then
    echo "missing executable: $path" >&2
    exit 1
  fi
done

if [[ ! -d "$resources/migrations" ]]; then
  echo "missing migrations directory: $resources/migrations" >&2
  exit 1
fi

if ! find "$pg/share" -name postgres.bki -type f | grep -q .; then
  echo "missing postgres.bki under $pg/share" >&2
  exit 1
fi

if find -L "$app" -type l -print | grep -q .; then
  echo "packaged app contains broken symlinks" >&2
  find -L "$app" -type l -print >&2
  exit 1
fi

if find "$app" -type f -perm -111 -print0 | while IFS= read -r -d '' file; do otool -L "$file" 2>/dev/null || true; done | grep -q '/opt/homebrew/'; then
  echo "packaged executable still references /opt/homebrew" >&2
  exit 1
fi

echo "packaged app verification passed: $app"
```

- [ ] **Step 2: Run script against current package and capture failure**

Run after building a package:

```bash
chmod +x scripts/verify_packaged_app_macos.sh
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

Expected before package fixes: FAIL with a specific missing executable, missing `postgres.bki`, broken symlink, or dylib reference if the bundle is incomplete.

- [ ] **Step 3: Remove invalid `pg_config --sharedir` relocation requirement**

In `scripts/package_macos.sh`, replace the `verify_postgres_runtime` block that rejects `pg_config --sharedir` outside `$pg_bundle/share` with checks that use the staged files directly:

```bash
verify_postgres_runtime() {
  local pg_bundle="$1"
  "$pg_bundle/bin/initdb" --version >/dev/null
  "$pg_bundle/bin/postgres" --version >/dev/null
  "$pg_bundle/bin/pg_ctl" --version >/dev/null
  if ! find "$pg_bundle/share" -name postgres.bki -type f | grep -q .; then
    echo "packaged PostgreSQL missing postgres.bki under $pg_bundle/share" >&2
    exit 1
  fi
}
```

- [ ] **Step 4: Call verification script from package script**

After `codesign` and before `hdiutil create`, add:

```bash
"$root/scripts/verify_packaged_app_macos.sh" "$app"
```

- [ ] **Step 5: Run packaging script**

Run:

```bash
SUPER_DOLPHIN_SKIP_FRONTEND_BUILD=1 ./scripts/package_macos.sh
```

Expected: package script reaches `packaged app verification passed` or fails with a concrete missing bundle input.

- [ ] **Step 6: Commit**

```bash
git add scripts/package_macos.sh scripts/verify_packaged_app_macos.sh docs/运维发布/打包发布/embedded-postgres.md
git commit -m "fix: verify macos package from staged runtime files"
```

---

## Task 7: Add clean-VM acceptance checklist

**Files:**
- Modify: `docs/运维发布/打包发布/embedded-postgres.md`
- Create: `docs/运维发布/打包发布/macos-clean-vm-checklist.md`

- [ ] **Step 1: Create the checklist document**

Create `docs/运维发布/打包发布/macos-clean-vm-checklist.md`:

```markdown
# macOS Clean VM Packaged App Checklist

## VM baseline

- macOS arm64 VM.
- No `DATABASE_URL` in shell or launch environment.
- No PostgreSQL server installed or running.
- No Docker Desktop required.
- No `~/.codex` directory.
- No `codex` binary on PATH.
- No Super Dolphin app data directory:
  - `~/Library/Application Support/Super Dolphin`

## Install

1. Copy `dist/package/macos/Super Dolphin.dmg` to the VM.
2. Mount the DMG.
3. Drag `Super Dolphin.app` to `/Applications`.
4. Launch `/Applications/Super Dolphin.app` by double-clicking.

## Expected first launch behavior

- App creates `~/Library/Application Support/Super Dolphin`.
- App initializes embedded PostgreSQL data dir.
- App runs migrations to the binary's required schema version.
- App prepares app-managed Codex home.
- App does not require Docker.
- App does not require a user-provided `DATABASE_URL`.

## Acceptance test

1. Open the app.
2. Create a Codex conversation.
3. Send: `Say hello from packaged Super Dolphin.`
4. Receive a Codex response.
5. Quit the app.
6. Reopen the app.
7. Confirm the previous conversation is visible or the app can create a second Codex conversation without reinitializing the database.

## Failure evidence to collect

Run these commands in Terminal and save output:

```bash
ls -la "$HOME/Library/Application Support/Super Dolphin"
find "$HOME/Library/Application Support/Super Dolphin" -maxdepth 4 -type f | sort | sed -n '1,120p'
ps aux | grep -E 'postgres|agent-terminal|mcp-orch|mcp-lsp|codex' | grep -v grep
log show --predicate 'process == "agent-terminal"' --last 10m
```

Also copy the app's PostgreSQL log from:

```text
~/Library/Application Support/Super Dolphin/logs/postgres.log
```
```

- [ ] **Step 2: Link the checklist from embedded Postgres docs**

Add to `docs/运维发布/打包发布/embedded-postgres.md`:

```markdown
## Clean VM acceptance

Before calling the package ready, run the checklist in:

- `docs/运维发布/打包发布/macos-clean-vm-checklist.md`
```

- [ ] **Step 3: Commit**

```bash
git add docs/运维发布/打包发布/embedded-postgres.md docs/运维发布/打包发布/macos-clean-vm-checklist.md
git commit -m "docs: add clean vm packaged app checklist"
```

---

## Task 8: Run the MVP validation commands

**Files:**
- No source files changed unless validation exposes a defect.

- [ ] **Step 1: Run Go package tests for changed backend packages**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv ./internal/platform/embeddedpg ./internal/platform/config ./internal/platform/db ./internal/provider/codexapp ./internal/app ./cmd/mcp-orch -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend build if packaging uses embedded frontend dist**

Run:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: all commands PASS and `cmd/agent-terminal/frontend/dist/index.html` exists.

- [ ] **Step 3: Build plain app and peers**

Run from repo root:

```bash
make build-peer-binaries
make build-agent-terminal-plain
go build -o bin/mcp-ida ./cmd/mcp-ida
```

Expected: PASS and binaries exist in `bin/`.

- [ ] **Step 4: Package macOS app**

Run:

```bash
./scripts/package_macos.sh
```

Expected: PASS and output path printed:

```text
macOS package ready: dist/package/macos/Super Dolphin.dmg
```

- [ ] **Step 5: Verify staged app**

Run:

```bash
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

Expected:

```text
packaged app verification passed: dist/package/macos/Super Dolphin.app
```

- [ ] **Step 6: Run clean VM checklist**

Follow `docs/运维发布/打包发布/macos-clean-vm-checklist.md`.

Expected: one Codex conversation succeeds on a clean VM.

- [ ] **Step 7: Commit validation docs or fixes only if files changed**

If validation required source fixes, commit the owned files with a focused message:

```bash
git add <owned-files>
git commit -m "fix: pass clean vm packaged app validation"
```

---

## Reset route if this worktree remains unstable

Use this route if the current `package-embedded-pg` worktree keeps accumulating unrelated fixes or cannot pass the clean VM checklist after the ownership and bootstrap tasks above.

### Reset principles

- Start from `main` in a new worktree.
- Do not copy the current branch wholesale.
- Cherry-pick only small, reviewed pieces that match this plan.
- Keep each commit independently testable.
- Implement only macOS arm64 first.

### New worktree creation

Run from primary repo:

```bash
cd /Users/ai/Desktop/Super-Dolphin
git fetch origin
git worktree add .worktrees/packaged-app-mvp-reset main
cd .worktrees/packaged-app-mvp-reset
git switch -c codex/packaged-app-mvp-reset
```

### New worktree task order

1. Implement Task 1 packaged runtime contract.
2. Implement Task 2 embedded PostgreSQL owner flag.
3. Implement Task 3 sidecar no-ownership rule.
4. Implement minimal embedded PostgreSQL config/runtime from the current branch only after reviewing each file.
5. Implement Task 4 Codex bootstrap.
6. Implement Task 5 fail-fast preflight.
7. Implement Task 6 package verification.
8. Run Task 8 validation.

### Allowed cherry-pick candidates from current worktree

Review and copy manually instead of blind cherry-picking:

- `internal/platform/runtimeenv/runtimeenv.go`
- `internal/platform/embeddedpg/config.go`
- `internal/platform/embeddedpg/runtime.go`
- `scripts/package_macos.sh`
- `scripts/build_relocatable_postgres_macos.sh`
- `docs/运维发布/打包发布/embedded-postgres.md`

Do not blindly copy:

- `cmd/mcp-orch/runtime.go` embedded PostgreSQL lifecycle changes.
- warning-and-continue preflight behavior in `internal/app/app.go`.
- Codex auto-install code as a substitute for relay config bootstrap.

---

## Review checklist before claiming completion

- [ ] No ordinary user path requires Docker.
- [ ] No ordinary user path requires external PostgreSQL.
- [ ] No ordinary user path requires manually setting `DATABASE_URL`.
- [ ] Desktop process is the only embedded PostgreSQL owner.
- [ ] mcp-orch sidecar does not stop embedded PostgreSQL.
- [ ] Codex readiness includes local relay config, not only CLI existence.
- [ ] Desktop preflight returns required readiness errors.
- [ ] macOS package verification checks staged files, not developer-machine paths.
- [ ] Clean VM checklist passes with one Codex conversation.

## Specification coverage self-check

- PostgreSQL coupling acknowledged: Decision 2 and Tasks 2, 3, 6 preserve PostgreSQL and avoid SQLite.
- Docker question answered in implementation terms: Decision 1 excludes Docker from packaged user path.
- Current worktree risks documented: Current worktree assessment lists lifecycle, Codex bootstrap, and package verification risks.
- Task breakdown provided: Tasks 1 through 8 are ordered and testable.
- Acceptance criteria provided: Task 8 and clean VM checklist define pass/fail.
- Restart route provided: Reset route gives exact worktree commands and copy boundaries.
