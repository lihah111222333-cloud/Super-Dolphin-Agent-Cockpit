package claudecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemPromptDumpDisabledByDefault(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	t.Setenv("TEMP", tempRoot)
	t.Setenv("TMP", tempRoot)

	logSystemPromptArgs([]string{"--system-prompt", "secret prompt body"})

	dumpDir := filepath.Join(os.TempDir(), "super-agent-systemprompt")
	entries, err := os.ReadDir(dumpDir)
	if err == nil && len(entries) > 0 {
		t.Fatalf("system prompt dump files = %d, want none by default in %s", len(entries), dumpDir)
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir system prompt dump dir: %v", err)
	}
}
