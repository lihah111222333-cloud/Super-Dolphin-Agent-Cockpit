package app

import (
	"os"
	"strings"
	"testing"
)

func TestRunDebugShellKeepsCleanupTrapAfterDebugBinaryExit(t *testing.T) {
	const path = "../../run-debug.sh"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)

	if strings.Contains(text, `exec "$BUILD_DIR/super-agent-debug" --debug "$@"`) {
		t.Fatal("run-debug.sh must not exec the debug binary; exec bypasses EXIT trap cleanup for vite")
	}
	for _, want := range []string{
		`"$BUILD_DIR/super-agent-debug" --debug "$@"`,
		"AGENT_EXIT=$?",
		"cleanup_vite",
		"trap - EXIT INT TERM",
		"exit $AGENT_EXIT",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("run-debug.sh missing debug cleanup sequence %q", want)
		}
	}
}
