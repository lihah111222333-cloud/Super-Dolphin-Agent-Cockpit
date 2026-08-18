//go:build windows

package hiddenexec

import (
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsGoplsBrokerBootstrapCreationFlags 验证仅在宿主 Job 允许时请求脱离。
func TestWindowsGoplsBrokerBootstrapCreationFlags(t *testing.T) {
	base := uint32(createSuspended | createNewProcessGroup | createNoWindow)
	if got := windowsGoplsBrokerBootstrapCreationFlagsForJob(false, jobObjectLimitBreakawayOK); got != base {
		t.Fatalf("bootstrap creation flags without host Job = %#x, want %#x", got, base)
	}
	if got := windowsGoplsBrokerBootstrapCreationFlagsForJob(true, 0); got != base {
		t.Fatalf("bootstrap creation flags without breakaway permission = %#x, want %#x", got, base)
	}
	if got, want := windowsGoplsBrokerBootstrapCreationFlagsForJob(true, jobObjectLimitBreakawayOK), base|windows.CREATE_BREAKAWAY_FROM_JOB; got != want {
		t.Fatalf("bootstrap creation flags with breakaway permission = %#x, want %#x", got, want)
	}
}
