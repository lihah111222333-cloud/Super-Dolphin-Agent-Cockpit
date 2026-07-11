package thread

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestBuildStartCtxReadsScratchpadDirFromConfig(t *testing.T) {
	ctx := buildStartCtx(StartRequest{Config: map[string]any{"scratchpadDir": "/tmp/custom-scratchpad"}}, nil, nil)
	if ctx.ScratchpadDir != "/tmp/custom-scratchpad" {
		t.Fatalf("ScratchpadDir = %q, want config value", ctx.ScratchpadDir)
	}
}

func TestEnsureManagedScratchpadDirCreatesSessionScopedPath(t *testing.T) {
	cwd := t.TempDir()
	dir, err := ensureManagedScratchpadDir(contract.BuildCtx{CWD: cwd}, StartRequest{CWD: cwd}, "agent-1", nil)
	if err != nil {
		t.Fatalf("ensureManagedScratchpadDir() error = %v", err)
	}
	defer func() { _ = cleanupManagedScratchpadDir(dir) }()
	if !filepath.IsAbs(dir) {
		t.Fatalf("scratchpad dir = %q, want absolute path", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Stat(%q) error = %v", dir, err)
	}
	if filepath.Base(dir) != "scratchpad" {
		t.Fatalf("scratchpad leaf = %q, want scratchpad", filepath.Base(dir))
	}
	if got := filepath.Base(filepath.Dir(dir)); got != "agent-1" {
		t.Fatalf("scratchpad session segment = %q, want agent-1", got)
	}
}

func TestBuildStartSessionConfigPersistsOutputStyleAndScratchpad(t *testing.T) {
	keepCoding := true
	cfg := buildStartSessionConfig(StartRequest{}, contract.StartInput{
		OutputStyleConfig: &contract.OutputStyleConfig{
			Name:                   "Explanatory",
			Prompt:                 "Explain each decision.",
			KeepCodingInstructions: &keepCoding,
		},
		ScratchpadDir: "/tmp/agent/scratchpad",
	}, contract.StartAssembly{})
	style, ok := cfg["outputStyleConfig"].(map[string]any)
	if !ok || style["name"] != "Explanatory" || style["prompt"] != "Explain each decision." || style["keepCodingInstructions"] != true {
		t.Fatalf("outputStyleConfig = %#v", cfg["outputStyleConfig"])
	}
	if cfg["scratchpadDir"] != "/tmp/agent/scratchpad" || cfg["scratchpad_dir"] != "/tmp/agent/scratchpad" {
		t.Fatalf("scratchpad config = %#v", cfg)
	}
}
