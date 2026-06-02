package app

import (
	"os"
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
	if strings.Contains(text, "cmd/agent-terminal/frontend") {
		t.Fatal("run-new-ui-desktop.sh must not build or mutate the legacy frontend package")
	}
}

func TestNewUIDesktopScriptWaitsForBackendBeforeVite(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`SUPER_DOLPHIN_BACKEND_LOG="${SUPER_DOLPHIN_BACKEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/backend.log}"`,
		`SUPER_DOLPHIN_FRONTEND_LOG="${SUPER_DOLPHIN_FRONTEND_LOG:-$PROJECT_DIR/.tmp/run-new-ui-desktop/frontend.log}"`,
		`wait_for_backend`,
		`http://${SUPER_DOLPHIN_HTTP_ADDR}/metrics`,
		`process_exited "$DESKTOP_PID"`,
		`print_backend_log_tail`,
		`wait_for_any_process_exit`,
		`CLEANUP_DONE`,
		`go run ./cmd/agent-terminal >"$SUPER_DOLPHIN_BACKEND_LOG" 2>&1`,
		`npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, `(cd "$PROJECT_DIR" && go run ./cmd/agent-terminal >"$SUPER_DOLPHIN_BACKEND_LOG" 2>&1) &`, "\nwait_for_backend\n")
	assertTextOrder(t, text, "\nwait_for_backend\nseed_dev_preferences\n\n", `(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &`)
	assertTextOrder(t, text, `wait_for_http "$VITE_DEV_URL" "frontend-app vite"`, "\nwait_for_any_process_exit\n")
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
	assertTextOrder(t, text, `stop_process_tree "frontend-app vite" "$VITE_PID"`, `stop_process_tree "new UI desktop backend" "$DESKTOP_PID"`)
}

func TestNewUIDesktopScriptAutostartsLocalPostgresBeforeBackend(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`configure_dev_postgres_runtime`,
		`ensure_local_postgres`,
		`resolve_postgres_bin_dir`,
		`resolve_postgres_share_dir`,
		`SUPER_DOLPHIN_POSTGRES_DIST`,
		`SUPER_DOLPHIN_POSTGRES_BIN_DIR`,
		`SUPER_DOLPHIN_POSTGRES_SHARE_DIR`,
		`SUPER_DOLPHIN_PROCESS_ROLE="${SUPER_DOLPHIN_PROCESS_ROLE:-desktop}"`,
		`SUPER_DOLPHIN_LOCAL_POSTGRES_PORT="${SUPER_DOLPHIN_LOCAL_POSTGRES_PORT:-55433}"`,
		`SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR="${SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR:-$PROJECT_DIR/.tmp/pgdata}"`,
		`SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR="${SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR:-$PROJECT_DIR/.tmp/pgsocket}"`,
		`SUPER_DOLPHIN_LOCAL_POSTGRES_LOG="${SUPER_DOLPHIN_LOCAL_POSTGRES_LOG:-$PROJECT_DIR/.tmp/postgres.log}"`,
		`DATABASE_URL="${DATABASE_URL:-postgres://super_dolphin@127.0.0.1:${SUPER_DOLPHIN_LOCAL_POSTGRES_PORT}/super_dolphin?sslmode=disable}"`,
		`export DATABASE_URL`,
		`postgres_is_local_database_url`,
		`initialize_local_postgres_data_dir`,
		`initdb" -D "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR"`,
		`pg_ctl" -D "$SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR"`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	assertTextOrder(t, text, "ensure_dev_control_session_token\nconfigure_dev_postgres_runtime", `ensure_node_deps "$FRONTEND_APP_DIR"`)
	assertTextOrder(t, text, "ensure_local_postgres\nensure_node_deps", `(cd "$PROJECT_DIR" && go run ./cmd/agent-terminal >"$SUPER_DOLPHIN_BACKEND_LOG" 2>&1) &`)
	if strings.Contains(text, `export DATABASE_URL="${DATABASE_URL:-`) {
		t.Fatal("run-new-ui-desktop.sh must not overwrite an explicit DATABASE_URL")
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
	assertTextOrder(t, text, `SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-/tmp/sd-new-ui-${USER:-user}/super-dolphin-home}"`, "\nensure_dev_control_session_token\nconfigure_dev_postgres_runtime")
	assertTextOrder(t, text, `export SUPER_DOLPHIN_HOME`, "\nensure_dev_control_session_token\nconfigure_dev_postgres_runtime")
}

func TestNewUIDesktopScriptSeedsDevProviderPreferencesBeforeFrontend(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-desktop.sh")

	required := []string{
		`SUPER_DOLPHIN_DEV_PROVIDER="${SUPER_DOLPHIN_DEV_PROVIDER:-codex}"`,
		`SUPER_DOLPHIN_DEV_CODEX_MODEL="${SUPER_DOLPHIN_DEV_CODEX_MODEL:-gpt-5.5}"`,
		`SUPER_DOLPHIN_DEV_CODEX_EFFORT="${SUPER_DOLPHIN_DEV_CODEX_EFFORT:-xhigh}"`,
		`SUPER_DOLPHIN_DEV_CODEX_HOME="${SUPER_DOLPHIN_DEV_CODEX_HOME:-$HOME/.codex}"`,
		`SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY="${SUPER_DOLPHIN_DEV_CODEX_INSTANCE_KEY:-default}"`,
		`SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER="${SUPER_DOLPHIN_DEV_CODEX_MODEL_PROVIDER:-openai}"`,
		`DEV_LOCAL_POSTGRES_MANAGED`,
		`seed_dev_preferences`,
		`"$SUPER_DOLPHIN_POSTGRES_BIN_DIR/psql"`,
		`settings.provider.active`,
		`settings.provider.codex.model`,
		`settings.provider.codex.effort`,
		`settings.provider.codex.codexHome`,
		`settings.provider.codex.codexInstanceKey`,
		`settings.provider.codex.codexModelProvider`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	seedCall := "\nseed_dev_preferences\n\n" + `(cd "$FRONTEND_APP_DIR" && npm run dev -- --host "$VITE_DEV_HOST" --port "$VITE_DEV_PORT" --strictPort >"$SUPER_DOLPHIN_FRONTEND_LOG" 2>&1) &`
	assertTextOrder(t, text, "\nwait_for_backend\n", seedCall)
}

func TestNewUIWebScriptContract(t *testing.T) {
	text := readRootScript(t, "../../run-new-ui-web.sh")

	required := []string{
		`PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"`,
		`FRONTEND_DIR="$PROJECT_DIR/frontend"`,
		`WEB_PORT="${WEB_PORT:-5178}"`,
		`SUPER_DOLPHIN_HTTP_ADDR="${SUPER_DOLPHIN_HTTP_ADDR:-127.0.0.1:4511}"`,
		`npm run dev -- --host "$WEB_HOST" --port "$WEB_PORT" --strictPort`,
		`http://$WEB_HOST:$WEB_PORT/`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-web.sh missing %q", want)
		}
	}
	if strings.Contains(text, "go run ./cmd/agent-terminal") {
		t.Fatal("run-new-ui-web.sh must not start another desktop backend")
	}
	if strings.Contains(text, "cmd/agent-terminal/frontend") {
		t.Fatal("run-new-ui-web.sh must not start or mutate the legacy frontend package")
	}
}

func readRootScript(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func assertTextOrder(t *testing.T, text, first, second string) {
	t.Helper()
	firstIndex := strings.Index(text, first)
	if firstIndex < 0 {
		t.Fatalf("missing first text %q", first)
	}
	secondIndex := strings.Index(text, second)
	if secondIndex < 0 {
		t.Fatalf("missing second text %q", second)
	}
	if firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q", first, second)
	}
}
