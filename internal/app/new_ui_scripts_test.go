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
		`npm run dev`,
		`go run ./cmd/agent-terminal`,
		`cleanup`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("run-new-ui-desktop.sh missing %q", want)
		}
	}
	if strings.Contains(text, "cmd/agent-terminal/frontend") {
		t.Fatal("run-new-ui-desktop.sh must not start or mutate the legacy frontend package")
	}
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
