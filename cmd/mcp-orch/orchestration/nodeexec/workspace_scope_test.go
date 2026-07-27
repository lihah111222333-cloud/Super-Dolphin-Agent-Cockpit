package nodeexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAutomationCommandWorkspaceScope(t *testing.T) {
	base := t.TempDir()
	trustedRoot := filepath.Join(base, "trusted")
	trustedCWD := filepath.Join(trustedRoot, "project")
	trustedSibling := filepath.Join(trustedRoot, "sibling")
	outsideRoot := filepath.Join(base, "outside")
	outsideCWD := filepath.Join(outsideRoot, "project")
	for _, path := range []string{trustedCWD, trustedSibling, outsideCWD} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("create workspace fixture %q: %v", path, err)
		}
	}

	tests := []struct {
		name         string
		cwd          string
		configRoots  []string
		trustedRoots []string
		wantErr      string
	}{
		{
			name:         "trusted child",
			cwd:          trustedCWD,
			configRoots:  []string{trustedRoot},
			trustedRoots: []string{trustedRoot},
		},
		{
			name:        "missing trusted roots",
			cwd:         trustedCWD,
			configRoots: []string{trustedRoot},
			wantErr:     "trusted workspace roots are required",
		},
		{
			name:         "config root outside trusted roots",
			cwd:          outsideCWD,
			configRoots:  []string{outsideRoot},
			trustedRoots: []string{trustedRoot},
			wantErr:      "outside trusted workspace roots",
		},
		{
			name:         "filesystem root cannot widen trusted roots",
			cwd:          trustedCWD,
			configRoots:  []string{string(filepath.Separator)},
			trustedRoots: []string{trustedRoot},
			wantErr:      "outside trusted workspace roots",
		},
		{
			name:         "cwd must remain inside config roots",
			cwd:          trustedSibling,
			configRoots:  []string{trustedCWD},
			trustedRoots: []string{trustedRoot},
			wantErr:      "outside config workspace roots",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := automationWorkspaceScopeConfig(t, tc.cwd, tc.configRoots)
			err := ValidateAutomationCommandWorkspaceScope(raw, tc.trustedRoots)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAutomationCommandWorkspaceScope() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateAutomationCommandWorkspaceScope() error = %v, want phrase %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAutomationCommandWorkspaceScopeRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	trustedRoot := filepath.Join(base, "trusted")
	outsideRoot := filepath.Join(base, "outside")
	outsideCWD := filepath.Join(outsideRoot, "project")
	for _, path := range []string{trustedRoot, outsideCWD} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("create workspace fixture %q: %v", path, err)
		}
	}
	link := filepath.Join(trustedRoot, "escape")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Fatalf("create escape symlink: %v", err)
	}

	raw := automationWorkspaceScopeConfig(t, filepath.Join(link, "project"), []string{link})
	err := ValidateAutomationCommandWorkspaceScope(raw, []string{trustedRoot})
	if err == nil || !strings.Contains(err.Error(), "outside trusted workspace roots") {
		t.Fatalf("ValidateAutomationCommandWorkspaceScope() error = %v, want symlink escape rejected", err)
	}
}

func automationWorkspaceScopeConfig(t *testing.T, cwd string, roots []string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"exec": map[string]any{
			"kind":            "command_card",
			"command_ref":     "build",
			"cwd":             cwd,
			"workspace_roots": roots,
		},
	})
	if err != nil {
		t.Fatalf("marshal automation workspace config: %v", err)
	}
	return raw
}
