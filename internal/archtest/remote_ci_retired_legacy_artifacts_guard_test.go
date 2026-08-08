package archtest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIRetiredLegacyArtifactsDoNotReturn keeps removed JSON-manifest
// migration and result-fingerprint artifacts from recreating a second remote-CI
// cache/protocol path beside the accepted SQLite/ImageCache flow.
func TestRemoteCIRetiredLegacyArtifactsDoNotReturn(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"internal/devtools/remoteci/baseline_manifest.go",
		"internal/devtools/remoteci/baseline_manifest_schema.go",
		"internal/devtools/remoteci/baseline_manifest_test.go",
		"internal/devtools/remoteci/workload_fingerprint_execution_test.go",
		"internal/devtools/remoteci/workload_fingerprint_process_io_test.go",
		"internal/devtools/remoteci/workload_fingerprint_sources_test.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("remote CI must not retain retired manifest or result-fingerprint artifact %s", relative)
		}
	}
}

// TestRemoteCIRetiredTopLevelPlanCLIStaysAbsent keeps the coordinator's
// retired standalone plan command from becoming a second plan authority.
func TestRemoteCIRetiredTopLevelPlanCLIStaysAbsent(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "cmd", "super-dolphin-gate", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gate CLI source: %v", err)
	}
	for _, marker := range []string{"case \"plan\":", "runPlan", "parsePlan", "planFlags", "registerPlanFlags"} {
		if strings.Contains(string(source), marker) {
			t.Errorf("gate CLI must not restore retired top-level plan marker %q", marker)
		}
	}
}

// TestRemoteCIRejectsRetiredFullBuildContextSymbols keeps the thin bundle as
// the only transferable source payload; retired full-context and image-input
// APIs must not return.
func TestRemoteCIRejectsRetiredFullBuildContextSymbols(t *testing.T) {
	root := findRepoRoot(t)
	for _, symbol := range []string{"ContextTar", "PrepareOCIBuildContext", "OCIBuildContext", "buildCanonicalContext", "GateImageInputs"} {
		hasSymbolInRemoteCIProductionSource(t, root, symbol)
	}
}

func hasSymbolInRemoteCIProductionSource(t *testing.T, root string, symbol string) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "internal", "devtools", "remoteci"), func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), symbol) {
			t.Errorf("remote CI production source must not restore retired full build-context symbol %q in %s", symbol, filepath.ToSlash(filePath))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan remote CI production source for %q: %v", symbol, err)
	}
}
