package shared

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveCodexIdentityRequiresAllFields(t *testing.T) {
	realDir := t.TempDir()
	cases := []struct {
		name   string
		config map[string]any
		want   error
	}{
		{
			name:   "missing home",
			config: map[string]any{"codexInstanceKey": "glm", "codexModelProvider": "glm-compat"},
			want:   ErrCodexHomeRequired,
		},
		{
			name:   "empty home string",
			config: map[string]any{"codexHome": "   ", "codexInstanceKey": "glm", "codexModelProvider": "glm-compat"},
			want:   ErrCodexHomeRequired,
		},
		{
			name:   "nil home",
			config: map[string]any{"codexHome": nil, "codexInstanceKey": "glm", "codexModelProvider": "glm-compat"},
			want:   ErrCodexHomeRequired,
		},
		{
			name:   "missing instance key",
			config: map[string]any{"codexHome": realDir, "codexModelProvider": "glm-compat"},
			want:   ErrCodexInstanceKeyRequired,
		},
		{
			name:   "empty instance key",
			config: map[string]any{"codexHome": realDir, "codexInstanceKey": "", "codexModelProvider": "glm-compat"},
			want:   ErrCodexInstanceKeyRequired,
		},
		{
			name:   "missing model provider",
			config: map[string]any{"codexHome": realDir, "codexInstanceKey": "glm"},
			want:   ErrCodexModelProviderRequired,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveCodexIdentity(tc.config)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResolveCodexIdentity error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResolveCodexIdentityRejectsWrongType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		config map[string]any
	}{
		{"home is int", map[string]any{"codexHome": 123, "codexInstanceKey": "glm", "codexModelProvider": "glm-compat"}},
		{"instance key is bool", map[string]any{"codexHome": "/tmp", "codexInstanceKey": true, "codexModelProvider": "glm-compat"}},
		{"model provider is map", map[string]any{"codexHome": "/tmp", "codexInstanceKey": "glm", "codexModelProvider": map[string]any{}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveCodexIdentity(tc.config)
			if !errors.Is(err, ErrCodexIdentityInvalidType) {
				t.Fatalf("want ErrCodexIdentityInvalidType, got %v", err)
			}
		})
	}
}

func TestResolveCodexIdentityReturnsCanonicalRealpath(t *testing.T) {
	t.Parallel()

	realDir := t.TempDir()
	// realDir may itself traverse symlinks on macOS (/var -> /private/var); use
	// the OS-resolved realpath as the source of truth.
	wantReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(realDir) error = %v", err)
	}
	ident, err := ResolveCodexIdentity(map[string]any{
		"codexHome":          realDir,
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	})
	if err != nil {
		t.Fatalf("ResolveCodexIdentity error = %v", err)
	}
	if ident.Home != wantReal {
		t.Fatalf("Home = %q, want %q", ident.Home, wantReal)
	}
	if ident.InstanceKey != "glm" || ident.ModelProvider != "glm-compat" {
		t.Fatalf("identity fields mismatch: %+v", ident)
	}
}

func TestResolveCodexIdentityDedupsSymlinkAliases(t *testing.T) {
	t.Parallel()

	realDir := t.TempDir()
	aliasDir := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		skipCodexIdentitySymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink error = %v", err)
	}
	resolveHome := func(home string) string {
		ident, err := ResolveCodexIdentity(map[string]any{
			"codexHome":          home,
			"codexInstanceKey":   "glm",
			"codexModelProvider": "glm-compat",
		})
		if err != nil {
			t.Fatalf("ResolveCodexIdentity(%q) error = %v", home, err)
		}
		return ident.Home
	}
	if a, b := resolveHome(realDir), resolveHome(aliasDir); a != b {
		t.Fatalf("symlink alias and realpath canonicalized differently: %q vs %q", a, b)
	}
}

func TestResolveCodexIdentityExpandsTildeViaHome(t *testing.T) {
	// Not parallel: mutates HOME via t.Setenv.
	realDir := t.TempDir()
	t.Setenv("HOME", realDir)
	t.Setenv("USERPROFILE", realDir)
	wantReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks error = %v", err)
	}
	cases := []string{"~", "~/"}
	for _, input := range cases {
		input := input
		t.Run(input, func(t *testing.T) {
			ident, err := ResolveCodexIdentity(map[string]any{
				"codexHome":          input,
				"codexInstanceKey":   "glm",
				"codexModelProvider": "glm-compat",
			})
			if err != nil {
				t.Fatalf("ResolveCodexIdentity(%q) error = %v", input, err)
			}
			if ident.Home != wantReal {
				t.Fatalf("Home = %q, want %q", ident.Home, wantReal)
			}
		})
	}
}

func skipCodexIdentitySymlinkPrivilegeNotHeld(t *testing.T, err error) {
	t.Helper()
	if runtime.GOOS == "windows" && strings.Contains(err.Error(), "privilege") {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
}

func TestResolveCodexIdentityExpandsEnv(t *testing.T) {
	// Not parallel: mutates env via t.Setenv.
	realDir := t.TempDir()
	t.Setenv("TEST_CODEX_ROOT", realDir)
	wantReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks error = %v", err)
	}
	ident, err := ResolveCodexIdentity(map[string]any{
		"codexHome":          "$TEST_CODEX_ROOT",
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	})
	if err != nil {
		t.Fatalf("ResolveCodexIdentity error = %v", err)
	}
	if ident.Home != wantReal {
		t.Fatalf("Home = %q, want %q", ident.Home, wantReal)
	}
}

func TestResolveCodexIdentityRejectsMissingDirectory(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := ResolveCodexIdentity(map[string]any{
		"codexHome":          missing,
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	})
	if !errors.Is(err, ErrCodexHomeNotFound) {
		t.Fatalf("want ErrCodexHomeNotFound, got %v", err)
	}
}

func TestResolveCodexIdentityRejectsRelativePath(t *testing.T) {
	t.Parallel()

	_, err := ResolveCodexIdentity(map[string]any{
		"codexHome":          "relative/codex",
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	})
	if !errors.Is(err, ErrCodexIdentityInvalidType) {
		t.Fatalf("want ErrCodexIdentityInvalidType, got %v", err)
	}
}

func TestResolveCodexIdentityRejectsForeignHomeTilde(t *testing.T) {
	t.Parallel()

	_, err := ResolveCodexIdentity(map[string]any{
		"codexHome":          "~root/codex",
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	})
	if !errors.Is(err, ErrCodexIdentityInvalidType) {
		t.Fatalf("want ErrCodexIdentityInvalidType, got %v", err)
	}
}

func TestResolveCodexIdentityDoesNotCreateDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	missing := filepath.Join(parent, "to-be-created")
	_, err := ResolveCodexIdentity(map[string]any{
		"codexHome":          missing,
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	})
	if !errors.Is(err, ErrCodexHomeNotFound) {
		t.Fatalf("want ErrCodexHomeNotFound, got %v", err)
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("ResolveCodexIdentity must not create codexHome; stat = %v", statErr)
	}
}
