package gate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rewriteRuntimeViteCacheDigest(t *testing.T, manifestPath string, viteCacheRoot string) {
	t.Helper()
	manifest, err := LoadRuntimeSeedManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ViteCacheTreeSHA256 = mustRuntimeSeedTreeDigest(t, viteCacheRoot)
	var encoded bytes.Buffer
	if err := EncodeRuntimeSeedManifest(&encoded, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorFrontendWorkerUsesPrivateViteCacheDir(t *testing.T) {
	script := "#!/bin/sh\nset -eu\n[ \"$SUPER_DOLPHIN_VITE_CACHE_DIR\" = \"$TMPDIR/.vite-temp\" ]\n[ -L frontend-app/node_modules/.vite ]\n[ ! -e frontend-app/node_modules/.vite-temp ]\n[ -d \"$SUPER_DOLPHIN_VITE_CACHE_DIR\" ]\n[ \"$SUPER_DOLPHIN_VITE_CACHE_DIR\" != \"$PWD/frontend-app/node_modules/.vite\" ]\nseed_frontend=$(cd \"$SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED/..\" && pwd)\nseed_vite=\"$seed_frontend/vite-cache\"\nseed_metadata_before=$(cat \"$seed_vite/deps/_metadata.json\")\n[ -L \"$SUPER_DOLPHIN_VITE_CACHE_DIR/deps\" ]\n[ \"$(readlink \"$SUPER_DOLPHIN_VITE_CACHE_DIR/deps\")\" = \"$seed_vite/deps\" ]\nrm -rf \"$SUPER_DOLPHIN_VITE_CACHE_DIR/deps\"\ntest -d \"$seed_vite/deps\"\nmkdir \"$SUPER_DOLPHIN_VITE_CACHE_DIR/deps\"\nprintf '{\"hash\":\"private\"}\\n' > \"$SUPER_DOLPHIN_VITE_CACHE_DIR/deps/_metadata.json\"\ntest \"$(cat \"$seed_vite/deps/_metadata.json\")\" = \"$seed_metadata_before\"\ntest -s \"$SUPER_DOLPHIN_VITE_CACHE_DIR/deps/_metadata.json\"\nprintf '%s\\n' vite-cache-probe-ok\n"
	source := newExecutorGitSnapshot(t, map[string]string{
		"vite-cache-probe.sh":            script,
		"go.sum":                         "module sum\n",
		"frontend-app/package-lock.json": "{\"lockfileVersion\":3}\n",
		".gitignore":                     "/frontend-app/node_modules\n",
	})
	if err := os.Chmod(filepath.Join(source, "vite-cache-probe.sh"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	makeRuntimeSeedTreeReadOnly(t, filepath.Join(runtimeRoot, "frontend", "vite-cache"))
	rewriteRuntimeViteCacheDigest(t, manifestPath, filepath.Join(runtimeRoot, "frontend", "vite-cache"))
	commitExecutorSnapshot(t, source, "frontend Vite cache worker fixture")
	config := newTestExecutorConfig(t, source)
	config.runtimeSeedRoot = runtimeRoot
	config.runtimeSeedManifest = manifestPath
	var output bytes.Buffer
	config.stdout = &output
	if err := executeProgram(context.Background(), config, GateIDFrontendE2E, ExecutorProgram{
		Strategy:          ExecutorStrategyCommands,
		Steps:             []ExecutorStep{{Argv: []string{"./vite-cache-probe.sh"}}},
		RequiredPaths:     []string{"vite-cache-probe.sh", "frontend-app/package-lock.json"},
		NeedsFrontendSeed: true,
	}); err != nil {
		t.Fatalf("execute frontend worker cache fixture: %v", err)
	}
	if strings.TrimSpace(output.String()) != "vite-cache-probe-ok" {
		t.Fatalf("frontend worker probe output = %q", output.String())
	}
}
