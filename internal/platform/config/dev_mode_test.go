package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevDSNRuntimeModeAcceptedFromTrustedEntrypoints(t *testing.T) {
	for _, entrypoint := range []string{"run-new-ui-desktop.sh", "run-new-ui-desktop.ps1", "goland", "make run-agent-terminal-debug", "make run-agent-terminal-debug-plain"} {
		t.Run(entrypoint, func(t *testing.T) {
			isolateConfigTestEnv(t)
			t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
			t.Setenv("SUPER_DOLPHIN_DEV_ENTRYPOINT", entrypoint)
			if _, err := PrimeProcessEnvironment(); err != nil {
				t.Fatalf("PrimeProcessEnvironment() error = %v", err)
			}
		})
	}
}

func TestDevDSNAmbientRuntimeModeCannotDowngradePackagedRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	t.Setenv("SUPER_DOLPHIN_DEV_ENTRYPOINT", "")
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime manifest marker: %v", err)
	}

	_, err := PrimeProcessEnvironment()
	if err == nil {
		t.Fatal("PrimeProcessEnvironment() error = nil, want ambient dev mode rejection for packaged root")
	}
	for _, want := range []string{"SUPER_DOLPHIN_RUNTIME_MODE=dev", "trusted dev entrypoint", "packaged"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("PrimeProcessEnvironment() error = %v, want %q", err, want)
		}
	}
}
