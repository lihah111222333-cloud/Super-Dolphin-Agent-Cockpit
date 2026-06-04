package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSiliconFlowAPIKeyLoadsVideoEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", home)
	t.Setenv("SILICONFLOW_API_KEY", "")
	if err := os.WriteFile(filepath.Join(home, "video.env"), []byte("SILICONFLOW_API_KEY=sk-test-video\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := siliconFlowAPIKey()
	if err != nil {
		t.Fatalf("siliconFlowAPIKey() error = %v", err)
	}
	if got != "sk-test-video" {
		t.Fatalf("siliconFlowAPIKey() = %q, want sk-test-video", got)
	}
}
