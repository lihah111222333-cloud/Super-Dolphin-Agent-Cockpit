package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProviderHooksAreWiredThroughAppAssembly(t *testing.T) {
	t.Parallel()

	source := readProviderHooksContractFile(t, "internal", "app", "modules.go")
	required := []string{
		"providershared.SetCaptureToolResultHook",
		"turn.CaptureToolResult",
		"providershared.SetResetToolResultScopeHook",
		"turn.ResetToolResultScope",
		"providershared.SetTrimSkillBlocksHook",
		"skill.TrimInjectedSkillBlocks",
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Fatalf("modules.go missing provider hook contract token %q", token)
		}
	}
}

func readProviderHooksContractFile(t *testing.T, parts ...string) string {
	t.Helper()

	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	path := filepath.Join(append([]string{root}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
