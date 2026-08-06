package archtest_test

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestCodexAppRuntimeOwnersDoNotRegressToPackageGlobals(t *testing.T) {
	for _, relative := range []string{
		"internal/provider/codexapp/codex_autoinstall.go",
		"internal/provider/codexapp/event_map.go",
		"internal/provider/codexapp/session.go",
	} {
		if got := archtest.MeasureFileMetrics(filepath.Join(repoRoot(t), relative)).GlobalVars; got != 0 {
			t.Fatalf("%s has %d mutable package globals, want 0", relative, got)
		}
	}
}
