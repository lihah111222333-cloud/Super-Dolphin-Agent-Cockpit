package archtest_test

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestMemoryPackageGlobalsAreScopedToAllowedImmutableForms(t *testing.T) {
	for _, relative := range []string{
		"internal/module/memory/auto_dream_task.go",
		"internal/module/memory/domain_bridges.go",
		"internal/module/memory/extract_transcript.go",
		"internal/module/memory/kairos.go",
		"internal/module/memory/path.go",
		"internal/module/memory/rules.go",
		"internal/module/memory/service.go",
		"internal/module/memory/truncate.go",
		"internal/module/memory/ui_rpc.go",
	} {
		if got := archtest.MeasureFileMetrics(filepath.Join(repoRoot(t), relative)).GlobalVars; got != 0 {
			t.Fatalf("%s has %d mutable package globals, want 0", relative, got)
		}
	}
}
