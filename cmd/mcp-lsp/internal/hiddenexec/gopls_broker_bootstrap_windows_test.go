//go:build windows

package hiddenexec

import (
	"strings"
	"testing"
)

// TestWindowsGoplsBrokerJobLimitFactsReportsBreakawayPolicy 验证 broker 拒绝原因完整记录 Job flags。
func TestWindowsGoplsBrokerJobLimitFactsReportsBreakawayPolicy(t *testing.T) {
	limits := uint32(jobObjectLimitKillOnJobClose | jobObjectLimitBreakawayOK)
	facts := windowsGoplsBrokerJobLimitFacts(limits)
	for _, want := range []string{
		"limit_flags=0x00002800",
		"kill_on_close=true",
		"breakaway_ok=true",
		"silent_breakaway_ok=false",
	} {
		if !strings.Contains(facts, want) {
			t.Fatalf("Job policy facts missing %q: %s", want, facts)
		}
	}
}
