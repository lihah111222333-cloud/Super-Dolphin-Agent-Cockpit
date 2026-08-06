package archtest_test

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestCodexAppSessionDispatchStaysWithinApprovedEffectiveLines(t *testing.T) {
	const maxApprovedLines = 802
	relative := "internal/provider/codexapp/session_dispatch.go"
	if got := archtest.MeasureFileMetrics(filepath.Join(repoRoot(t), relative)).Lines; got > maxApprovedLines {
		t.Fatalf("%s has %d effective lines, want <= %d", relative, got, maxApprovedLines)
	}
}
