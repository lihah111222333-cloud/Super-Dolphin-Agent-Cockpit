//go:build windows

package hiddenexec

import "testing"

// TestWindowsGoplsBrokerBootstrapCreationFlagsKeepsHostJob 验证 broker 不请求脱离宿主 Job。
func TestWindowsGoplsBrokerBootstrapCreationFlagsKeepsHostJob(t *testing.T) {
	got := windowsGoplsBrokerBootstrapCreationFlags()
	want := uint32(createSuspended | createNewProcessGroup | createNoWindow)
	if got != want {
		t.Fatalf("bootstrap creation flags = %#x, want %#x", got, want)
	}
}
