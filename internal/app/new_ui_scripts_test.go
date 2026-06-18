package app

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestNewUIDesktopScriptContract(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"`,
		`FRONTEND_APP_DIR="$PROJECT_DIR/frontend-app"`,
		`SUPER_DOLPHIN_HTTP_ADDR="${SUPER_DOLPHIN_HTTP_ADDR:-127.0.0.1:4512}"`,
		`GO_AGENT_CTL_RPC_ADDR="${GO_AGENT_CTL_RPC_ADDR:-127.0.0.1:8092}"`,
		`VITE_DEV_URL="${VITE_DEV_URL:-http://127.0.0.1:5175}"`,
		`FRONTEND_DEVSERVER_URL="${FRONTEND_DEVSERVER_URL:-$VITE_DEV_URL}"`,
		`GO_AGENT_PEER_BIN_DIR="${GO_AGENT_PEER_BIN_DIR:-$PROJECT_DIR}"`,
		`SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"`,
		`SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"`,
		`SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"`,
		`VITE_DEV_HOST="${VITE_DEV_URL#http://}"`,
		`VITE_DEV_PORT="${VITE_DEV_HOST##*:}"`,
		`fail_if_port_busy "$VITE_DEV_HOST:$VITE_DEV_PORT"`,
		`npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort`,
		`go run ./cmd/agent-terminal`,
		`start_desktop_backend`,
		`cleanup`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	if strings.Contains(text, "(cd \"$FRONTEND_APP_DIR\" && npm run dev) &") {
		t.Fatal("run-new-ui-desktop.sh must pass VITE_DEV_URL host and port into Vite")
	}
	if strings.Contains(text, `cmd/agent-terminal/frontend && npm run dev`) {
		t.Fatal("run-new-ui-desktop.sh must not start the legacy frontend dev server")
	}
	if strings.Contains(text, `cmd/agent-terminal/frontend && npm`) {
		t.Fatal("run-new-ui-desktop.sh must not build or mutate the legacy frontend package")
	}
}

func TestNewUIDesktopScriptWaitsForFrontendBeforeBackend(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`SUPER_DOLPHIN_BACKEND_LOG="${SUPER_DOLPHIN_BACKEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/backend.log}"`,
		`SUPER_DOLPHIN_FRONTEND_LOG="${SUPER_DOLPHIN_FRONTEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/frontend.log}"`,
		`: >"$SUPER_DOLPHIN_FRONTEND_LOG"`,
		`wait_for_backend`,
		`http://${SUPER_DOLPHIN_HTTP_ADDR}/metrics`,
		`process_exited "$DESKTOP_PID"`,
		`process_exited "$VITE_PID"`,
		`print_backend_log_tail`,
		`print_frontend_log_tail`,
		`wait_for_any_process_exit`,
		`CLEANUP_DONE`,
		`go run ./cmd/agent-terminal >>"$SUPER_DOLPHIN_BACKEND_LOG" 2>&1`,
		`npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `: >"$SUPER_DOLPHIN_FRONTEND_LOG"`, `(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &`)
	assertTextOrder(t, text, `(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &`, `wait_for_http "$FRONTEND_DEVSERVER_URL" "frontend-app vite"`)
	assertTextOrder(t, text, `wait_for_http "$FRONTEND_DEVSERVER_URL" "frontend-app vite"`, "\nstart_desktop_backend\n")
	assertTextOrder(t, text, "\nstart_desktop_backend\nwait_for_backend\n", "\n  wait_for_any_process_exit\n")
	if strings.Contains(text, "\nstart_desktop_backend\nwait_for_backend\n\n(cd \"$FRONTEND_APP_DIR\"") {
		t.Fatal("run-new-ui-desktop.sh must not launch the desktop backend before Vite is ready")
	}
	if strings.Contains(text, `wait_for_http "$VITE_DEV_URL" "frontend-app vite"`) {
		t.Fatal("run-new-ui-desktop.sh must wait for FRONTEND_DEVSERVER_URL, the URL Wails actually uses")
	}
}

func TestNewUIDesktopScriptRejectsDivergentFrontendDevURLs(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`FRONTEND_DEVSERVER_URL="${FRONTEND_DEVSERVER_URL:-$VITE_DEV_URL}"`,
		`if [ "$FRONTEND_DEVSERVER_URL" != "$VITE_DEV_URL" ]; then`,
		`FRONTEND_DEVSERVER_URL must match VITE_DEV_URL`,
		`wait_for_http "$FRONTEND_DEVSERVER_URL" "frontend-app vite"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `if [ "$FRONTEND_DEVSERVER_URL" != "$VITE_DEV_URL" ]; then`, `stop_stale_vite_for_port "$VITE_DEV_PORT"`)
}

func TestNewUIDesktopScriptRebuildsStalePeerBinaries(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`peer_binary_stale()`,
		`find "$path" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name '*.yaml' -o -name '*.yml' \) -newer "$bin" -print -quit`,
		`peer_binary_stale "$peer_dir/mcp-orch" cmd/mcp-orch internal pkg go.mod go.sum`,
		`peer_binary_stale "$peer_dir/mcp-lsp" cmd/mcp-lsp internal pkg go.mod go.sum`,
		`rebuild_peer_binaries`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `peer_binary_stale()`, `ensure_peer_binaries()`)
	assertTextOrder(t, text, `if [ ! -x "$peer_dir/$bin" ]; then`, `peer_binary_stale "$peer_dir/mcp-orch"`)
	assertTextOrder(t, text, `peer_binary_stale "$peer_dir/mcp-orch"`, `peer_binary_stale "$peer_dir/mcp-lsp"`)
	assertTextOrder(t, text, `peer_binary_stale "$peer_dir/mcp-lsp"`, `rebuild_peer_binaries`)
}

func TestNewUIDesktopScriptUsesValidatedConfigurableReadinessWindows(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS="${SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS:-300}"`,
		`SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS="${SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS:-300}"`,
		`SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS="${SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS:-0.2}"`,
		`require_positive_integer "SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS" "$SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS"`,
		`require_positive_integer "SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS" "$SUPER_DOLPHIN_BACKEND_READY_ATTEMPTS"`,
		`require_positive_number "SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS" "$SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS"`,
		`for _ in $(seq 1 "$attempts"); do`,
		`sleep "$SUPER_DOLPHIN_READY_POLL_INTERVAL_SECONDS"`,
		`wait_for_http "$FRONTEND_DEVSERVER_URL" "frontend-app vite" "$SUPER_DOLPHIN_FRONTEND_READY_ATTEMPTS"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	if strings.Contains(text, "for _ in $(seq 1 100); do") {
		t.Fatal("run-new-ui-desktop.sh must not use fixed readiness attempt windows")
	}
}

func TestNewUIDesktopScriptPrintsLogTailAfterNonZeroWait(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`capture_wait_status()`,
		`set +e`,
		`WAIT_STATUS="$?"`,
		`set -e`,
		`capture_wait_status "$DESKTOP_PID"`,
		`capture_wait_status "$VITE_PID"`,
		`print_backend_log_tail`,
		`print_frontend_log_tail`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	if strings.Contains(text, "wait \"$DESKTOP_PID\"\n      status=\"$?\"") ||
		strings.Contains(text, "wait \"$VITE_PID\"\n      status=\"$?\"") ||
		strings.Contains(text, "wait \"$DESKTOP_PID\"\n      local status=\"$?\"") ||
		strings.Contains(text, "wait \"$VITE_PID\"\n      local status=\"$?\"") {
		t.Fatal("run-new-ui-desktop.sh must capture wait status before set -e can skip log tail printing")
	}
	assertTextOrderAfter(t, text, `if [ -n "${DESKTOP_PID:-}" ] && process_exited "$DESKTOP_PID"; then`, `capture_wait_status "$DESKTOP_PID"`, `print_backend_log_tail`)
	assertTextOrderAfter(t, text, `if [ -n "${VITE_PID:-}" ] && process_exited "$VITE_PID"; then`, `capture_wait_status "$VITE_PID"`, `print_frontend_log_tail`)
}

func TestNewUIDesktopScriptReportsFrontendReadinessFailures(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`if [ "$label" = "frontend-app vite" ]; then`,
		`echo "❌ $label exited before readiness: $url" >&2`,
		`capture_wait_status "$VITE_PID"`,
		`print_frontend_log_tail`,
		`echo "❌ desktop backend exited before $label readiness: $url" >&2`,
		`capture_wait_status "$DESKTOP_PID"`,
		`print_backend_log_tail`,
		`ENOSPC: System limit for number of file watchers reached`,
		`frontend watcher limit hit`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `process_exited "$VITE_PID"`, `sleep 0.2`)
	assertTextOrderAfter(t, text, `echo "❌ timed out waiting for $label: $url" >&2`, `if [ "$label" = "frontend-app vite" ]; then`, `print_frontend_log_tail`)
}

func TestNewUIDesktopScriptEnablesFrontendPollingWatchByDefault(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`parse_frontend_watch_bool()`,
		`resolve_frontend_watch_polling()`,
		`configure_frontend_watch_mode()`,
		`export SUPER_DOLPHIN_VITE_USE_POLLING`,
		`export CHOKIDAR_USEPOLLING`,
		`FRONTEND_WATCH_MODE="polling (SUPER_DOLPHIN_VITE_USE_POLLING=$SUPER_DOLPHIN_VITE_USE_POLLING, CHOKIDAR_USEPOLLING=$CHOKIDAR_USEPOLLING)"`,
		`configure_frontend_watch_mode`,
		`frontend watch: $FRONTEND_WATCH_MODE`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, "\nconfigure_frontend_watch_mode\nmkdir -p", `ensure_node_deps "$FRONTEND_APP_DIR"`)
	assertTextOrder(t, text, "\nconfigure_frontend_watch_mode\n", `(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &`)
}

func TestNewUIDesktopScriptFrontendWatchModeBooleanParsing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash snippet tests use WSL on Windows and cannot reliably access native temp paths")
	}
	tests := []struct {
		name       string
		env        map[string]string
		wantStatus int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "default polling",
			wantStatus: 0,
			wantStdout: "1|1|polling (SUPER_DOLPHIN_VITE_USE_POLLING=1, CHOKIDAR_USEPOLLING=1)",
		},
		{
			name:       "explicit super dolphin disables polling",
			env:        map[string]string{"SUPER_DOLPHIN_VITE_USE_POLLING": "0"},
			wantStatus: 0,
			wantStdout: "0|0|native fs events (SUPER_DOLPHIN_VITE_USE_POLLING=0, CHOKIDAR_USEPOLLING=0)",
		},
		{
			name:       "explicit chokidar disables polling",
			env:        map[string]string{"CHOKIDAR_USEPOLLING": "false"},
			wantStatus: 0,
			wantStdout: "0|0|native fs events (SUPER_DOLPHIN_VITE_USE_POLLING=0, CHOKIDAR_USEPOLLING=0)",
		},
		{
			name:       "invalid super dolphin boolean fails fast",
			env:        map[string]string{"SUPER_DOLPHIN_VITE_USE_POLLING": "sometimes"},
			wantStatus: 1,
			wantStderr: "SUPER_DOLPHIN_VITE_USE_POLLING must be a boolean",
		},
		{
			name:       "invalid chokidar boolean fails fast",
			env:        map[string]string{"CHOKIDAR_USEPOLLING": "sometimes"},
			wantStatus: 1,
			wantStderr: "CHOKIDAR_USEPOLLING must be a boolean",
		},
		{
			name:       "conflicting booleans fail fast",
			env:        map[string]string{"SUPER_DOLPHIN_VITE_USE_POLLING": "0", "CHOKIDAR_USEPOLLING": "1"},
			wantStatus: 1,
			wantStderr: "conflicting frontend watch config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, status := runFrontendWatchModeSnippet(t, tt.env)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d\nstdout:\n%s\nstderr:\n%s", status, tt.wantStatus, stdout, stderr)
			}
			if tt.wantStdout != "" && stdout != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout, tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestNewUIDesktopScriptCleansChildProcessesAndStaleVite(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`child_pids_of()`,
		`stop_process_tree()`,
		`stop_stale_vite_for_port()`,
		`process_workdir()`,
		`trap cleanup EXIT`,
		`trap 'cleanup; exit 130' INT TERM HUP`,
		`stop_process_tree "frontend-app vite" "$VITE_PID"`,
		`stop_process_tree "new UI desktop backend" "$DESKTOP_PID"`,
		`stop_stale_vite_for_port "$VITE_DEV_PORT"`,
		`frontend-app/node_modules/.bin/vite`,
		`"$FRONTEND_APP_DIR"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `stop_stale_vite_for_port "$VITE_DEV_PORT"`, `fail_if_port_busy "$VITE_DEV_HOST:$VITE_DEV_PORT"`)
	assertTextOrderAfter(t, text, "cleanup() {", `stop_process_tree "frontend-app vite" "$VITE_PID"`, `stop_process_tree "new UI desktop backend" "$DESKTOP_PID"`)
}

func TestNewUIDesktopScriptUsesSQLiteWithoutPostgresRuntime(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`ensure_sqlite_runtime`,
		`SUPER_DOLPHIN_SQLITE_PATH="${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}"`,
		`mkdir -p "$sqlite_parent"`,
		`export SUPER_DOLPHIN_SQLITE_PATH`,
		`SUPER_DOLPHIN_PROCESS_ROLE="${SUPER_DOLPHIN_PROCESS_ROLE:-desktop}"`,
		`sqlite:       $SUPER_DOLPHIN_SQLITE_PATH`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, "ensure_dev_control_session_token\nensure_sqlite_runtime", `ensure_node_deps "$FRONTEND_APP_DIR"`)
	assertTextOrder(t, text, "ensure_sqlite_runtime\nstop_stale_vite_for_port", "\nstart_desktop_backend\n")
	for _, forbidden := range []string{
		`configure_dev_postgres_runtime`,
		`ensure_local_postgres`,
		`resolve_postgres_bin_dir`,
		`resolve_postgres_share_dir`,
		`SUPER_DOLPHIN_POSTGRES_`,
		`SUPER_DOLPHIN_LOCAL_POSTGRES`,
		`DATABASE_URL`,
		`POSTGRES_CONNECTION_STRING`,
		`postgres_is_local_database_url`,
		`initialize_local_postgres_data_dir`,
		`pg_ctl`,
		`initdb`,
		`psql`,
		`seed_dev_preferences`,
		`DEV_LOCAL_POSTGRES_MANAGED`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("run-new-ui-desktop.sh must not contain PostgreSQL runtime dependency %q", forbidden)
		}
	}
	if strings.Contains(text, ",,}") {
		t.Fatal("run-new-ui-desktop.sh must remain compatible with macOS Bash 3.2 and avoid Bash 4 lowercase expansion")
	}
}

func TestNewUIDesktopScriptUsesShortDevHome(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/super-dolphin-home}"`,
		`export SUPER_DOLPHIN_HOME`,
		`mkdir -p "$(dirname "$SUPER_DOLPHIN_BACKEND_LOG")" "$(dirname "$SUPER_DOLPHIN_FRONTEND_LOG")" "$SUPER_DOLPHIN_HOME"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/super-dolphin-home}"`, "\nensure_dev_control_session_token\nensure_sqlite_runtime")
	assertTextOrder(t, text, `export SUPER_DOLPHIN_HOME`, "\nensure_dev_control_session_token\nensure_sqlite_runtime")
}

func TestNewUIDesktopScriptDoesNotSeedDevProviderPreferencesThroughDatabase(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`SUPER_DOLPHIN_DEV_PROVIDER="${SUPER_DOLPHIN_DEV_PROVIDER:-codex}"`,
		`SUPER_DOLPHIN_DEV_CODEX_MODEL="${SUPER_DOLPHIN_DEV_CODEX_MODEL:-gpt-5.5}"`,
		`SUPER_DOLPHIN_DEV_CODEX_EFFORT="${SUPER_DOLPHIN_DEV_CODEX_EFFORT:-xhigh}"`,
		`SUPER_DOLPHIN_DEV_CODEX_HOME="${SUPER_DOLPHIN_DEV_CODEX_HOME:-$HOME/.codex}"`,
		`SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY="${SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY:-default}"`,
		`SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER="${SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER:-openai}"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`seed_dev_preferences`,
		`settings.provider.active`,
		`settings.provider.codex.model`,
		`settings.provider.codex.effort`,
		`settings.provider.codex.codexHome`,
		`settings.provider.codex.codexInstanceKey`,
		`settings.provider.codex.codexModelProvider`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("run-new-ui-desktop.sh must not seed provider preferences through DB: %q", forbidden)
		}
	}
	assertTextOrder(t, text, "\nstart_desktop_backend\nwait_for_backend\n", "\nif backend_hot_reload_enabled; then\n  run_backend_hot_supervisor_loop")
}

func TestNewUIDesktopPowerShellScriptUsesSQLiteWithoutPostgresRuntime(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.ps1")

	required := []string{
		`$script:DefaultSuperDolphinHome = Join-Path $script:RunLogDir 'super-dolphin-home'`,
		`Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER' -Value 'openai'`,
		`function Protect-OwnerOnlyDirectory`,
		`Ensure-SqliteRuntime`,
		`Set-DefaultEnv -Name 'SUPER_DOLPHIN_SQLITE_PATH' -Value (Join-Path $env:SUPER_DOLPHIN_HOME 'super-dolphin.db')`,
		`Set-DefaultEnv -Name 'SUPER_DOLPHIN_HOME' -Value $script:DefaultSuperDolphinHome`,
		`Protect-OwnerOnlyDirectory -Path $parent`,
		`Write-Host "  sqlite:       $($env:SUPER_DOLPHIN_SQLITE_PATH)"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`Configure-DevPostgresRuntime`,
		`Ensure-LocalPostgres`,
		`Resolve-PostgresBinDir`,
		`SUPER_DOLPHIN_POSTGRES_`,
		`SUPER_DOLPHIN_LOCAL_POSTGRES`,
		`DATABASE_URL`,
		`POSTGRES_CONNECTION_STRING`,
		`pg_ctl`,
		`initdb`,
		`psql`,
		`Seed-DevPreferences`,
		`DEV_LOCAL_POSTGRES_MANAGED`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("run-new-ui-desktop.ps1 must not contain PostgreSQL runtime dependency %q", forbidden)
		}
	}
	assertTextOrderAfter(t, text, "Add-CodexCliToPath", "Ensure-SqliteRuntime", "Stop-StaleViteForPort")
	assertTextOrderAfter(t, text, "$script:DesktopProcess = Start-Process", "Wait-ForBackend", "Wait-ForAnyProcessExit")
}

func TestNewUIDesktopPowerShellScriptReinstallsIncompleteNodeModules(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.ps1")

	required := []string{
		`$npmInstallLock = Join-Path $nodeModules '.package-lock.json'`,
		`$viteShim = Join-Path $nodeModules '.bin\vite.cmd'`,
		`$vitePackageJson = Join-Path $nodeModules 'vite\package.json'`,
		`$viteCli = Join-Path $nodeModules 'vite\bin\vite.js'`,
		`Write-Host '  -> npm ci (node_modules incomplete)'`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing incomplete node_modules guard %q", want)
		}
	}
	assertTextOrder(t, text, `Write-Host '  -> npm ci (node_modules incomplete)'`, `Write-Host '  -> npm ci (package-lock changed)'`)
	assertTextOrder(t, text, `Write-Host '  -> npm ci (node_modules incomplete)'`, `Write-Host '  -> dependencies unchanged'`)
}

func TestNewUIDesktopPowerShellScriptStopsFrontendViteBeforeNpmCi(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.ps1")

	required := []string{
		`function Stop-StaleFrontendViteProcesses`,
		`$processes = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)`,
		`$commandLine.IndexOf($frontendPath, [StringComparison]::OrdinalIgnoreCase)`,
		`Stop-ProcessTree -Label 'stale frontend-app vite'`,
		`Stop-StaleFrontendViteProcesses`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing stale frontend Vite cleanup %q", want)
		}
	}
	assertTextOrder(t, text, `Stop-StaleFrontendViteProcesses`, `Ensure-NodeDeps -Dir $FrontendAppDir`)
	assertTextOrder(t, text, `Stop-StaleFrontendViteProcesses`, `Stop-StaleViteForPort -Port $ViteDevPort`)
}

func TestNewUIDesktopPowerShellScriptRebuildsStalePeerBinaries(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.ps1")

	required := []string{
		`Set-DefaultEnv -Name 'GO_AGENT_PEER_BIN_DIR' -Value $ProjectDir`,
		`function Resolve-PeerBinDir`,
		`[Environment]::GetEnvironmentVariable('GO_AGENT_PEER_BIN_DIR', 'Process')`,
		`$env:GO_AGENT_PEER_BIN_DIR = $ProjectDir`,
		`$rawPeerBinDir -split [regex]::Escape([string][IO.Path]::PathSeparator)`,
		`function Test-PeerBinaryStale`,
		`function Build-PeerBinaries`,
		`param([Parameter(Mandatory)][string]$PeerBinDir)`,
		`New-Item -ItemType Directory -Force -Path $PeerBinDir | Out-Null`,
		`& go build -o (Join-Path $PeerBinDir 'mcp-orch.exe') './cmd/mcp-orch/'`,
		`& go build -o (Join-Path $PeerBinDir 'mcp-lsp.exe') './cmd/mcp-lsp/'`,
		`$peerSourcePaths = @{`,
		`'mcp-orch' = @('cmd\mcp-orch', 'internal', 'pkg', 'go.mod', 'go.sum')`,
		`'mcp-lsp' = @('cmd\mcp-lsp', 'internal', 'pkg', 'go.mod', 'go.sum')`,
		`$peerBinDir = Resolve-PeerBinDir`,
		`Get-ChildItem -LiteralPath $sourcePath -Recurse -File`,
		`$binaryPath = Join-Path $peerBinDir "$name.exe"`,
		`Test-PeerBinaryStale -BinaryPath $binaryPath -SourcePaths $peerSourcePaths[$name]`,
		`Build-PeerBinaries -PeerBinDir $peerBinDir`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`$binaryPath = Join-Path $ProjectDir "$name.exe"`,
		`Join-Path $ProjectDir 'mcp-orch.exe'`,
		`Join-Path $ProjectDir 'mcp-lsp.exe'`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("run-new-ui-desktop.ps1 must use GO_AGENT_PEER_BIN_DIR for peer binaries, found %q", forbidden)
		}
	}
	assertTextOrderAfter(t, text, `Import-DotEnvFile -Path (Join-Path $ProjectDir '.env')`, `Set-DefaultEnv -Name 'GO_AGENT_PEER_BIN_DIR' -Value $ProjectDir`, `Ensure-PeerBinaries`)
	assertTextOrder(t, text, `function Test-PeerBinaryStale`, `function Ensure-PeerBinaries`)
	assertTextOrderAfter(t, text, `function Build-PeerBinaries`, `Push-Location -LiteralPath $ProjectDir`, `New-Item -ItemType Directory -Force -Path $PeerBinDir`)
	assertTextOrder(t, text, `$peerBinDir = Resolve-PeerBinDir`, `$binaryPath = Join-Path $peerBinDir "$name.exe"`)
	assertTextOrder(t, text, `$peerSourcePaths = @{`, `Test-PeerBinaryStale -BinaryPath $binaryPath -SourcePaths $peerSourcePaths[$name]`)
	assertTextOrderAfter(t, text, `function Ensure-PeerBinaries`, `Test-PeerBinaryStale -BinaryPath $binaryPath -SourcePaths $peerSourcePaths[$name]`, `Build-PeerBinaries -PeerBinDir $peerBinDir`)
}

func TestNewUIDesktopPowerShellScriptSkipsInvalidPathEntries(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.ps1")

	required := []string{
		"function Add-ProcessPathEntry",
		"try {",
		"Test-Path -LiteralPath $entry -PathType Container",
		"skipping invalid PATH entry",
		"return",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.ps1 missing %q", want)
		}
	}
	assertTextOrderAfter(t, text, "function Add-ProcessPathEntry", "try {", "skipping invalid PATH entry")
}

func TestNewUIDesktopScriptReadmeMatchesStartupOrder(t *testing.T) {
	script := readRootScript(t, "../../run-new-ui-desktop.sh")
	readme := readRootScript(t, "../../frontend-app/README.md")

	assertTextOrder(t, script, `wait_for_http "$FRONTEND_DEVSERVER_URL" "frontend-app vite"`, "\nstart_desktop_backend\n")
	required := []string{
		"The selected root script starts this app's Vite server, waits for it to become ready, then launches `cmd/agent-terminal`",
		"`FRONTEND_DEVSERVER_URL`",
	}
	for _, want := range required {
		if !strings.Contains(readme, want) {
			t.Fatalf("frontend-app/README.md missing %q", want)
		}
	}
	if strings.Contains(readme, "with `VITE_DEV_URL` so the Wails desktop host proxies") {
		t.Fatal("frontend-app/README.md must describe the actual Wails dev server URL contract")
	}
}

func TestRootDevStartupScriptsAreScopedToMacOSAndWindows(t *testing.T) {
	for _, rel := range []string{
		"../../run-new-ui-desktop.sh",
		"../../run-new-ui-desktop.ps1",
	} {
		if _, err := os.Stat(rel); err != nil {
			t.Fatalf("expected startup script %s: %v", rel, err)
		}
	}
	for _, rel := range []string{
		"../../run-new-ui-web.sh",
		"../../run-new-ui-desktop-hot.sh",
		"../../run-new-ui-desktop.cmd",
		"../../run-debug.sh",
		"../../run-debug.ps1",
	} {
		if _, err := os.Stat(rel); err == nil {
			t.Fatalf("obsolete startup script still exists: %s", rel)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect obsolete startup script %s: %v", rel, err)
		}
	}
}
