package claudecli

import (
	"os"
	"path/filepath"
	"strings"
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

func TestValidateClaudeSecurityConfigRejectsUnknownApprovalAndSandbox(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{name: "unknown approval_policy", cfg: map[string]any{"approval_policy": "later"}, want: "invalid approval policy"},
		{name: "unknown approvals alias", cfg: map[string]any{"approvals": "later"}, want: "invalid approval policy"},
		{name: "unknown sandbox string", cfg: map[string]any{"sandbox": "network-open"}, want: "invalid sandbox"},
		{name: "unknown sandbox object type", cfg: map[string]any{"sandbox": map[string]any{"type": "network-open"}}, want: "invalid sandbox"},
		{name: "sandbox mode alias bypass", cfg: map[string]any{"sandbox": map[string]any{"mode": "danger-full-access"}}, want: "invalid sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClaudeSecurityConfig(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateClaudeSecurityConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}
