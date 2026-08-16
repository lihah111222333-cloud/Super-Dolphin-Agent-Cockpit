//go:build windows && e2e

package toolbridge

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// Windows Job Object LimitFlags：关闭 Job 时终止进程树，并按策略允许子进程脱离。
	testJobKillOnClose = 0x2000
	testJobBreakaway   = 0x0800
)

func TestStdioCreateKillOnCloseJobBreakawayPolicy(t *testing.T) {
	tests := []struct {
		name           string
		allowBreakaway bool
		wantFlags      uint32
	}{
		{
			name:           "managed_lsp",
			allowBreakaway: true,
			wantFlags:      testJobKillOnClose | testJobBreakaway,
		},
		{
			name:           "external_mcp",
			allowBreakaway: false,
			wantFlags:      testJobKillOnClose,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := stdioCreateKillOnCloseJob(tt.allowBreakaway)
			if err != nil {
				t.Fatalf("stdioCreateKillOnCloseJob() error = %v", err)
			}
			t.Cleanup(func() {
				if err := windows.CloseHandle(job); err != nil {
					t.Errorf("CloseHandle() error = %v", err)
				}
			})

			var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			if err := windows.QueryInformationJobObject(
				job,
				windows.JobObjectExtendedLimitInformation,
				uintptr(unsafe.Pointer(&info)),
				uint32(unsafe.Sizeof(info)),
				nil,
			); err != nil {
				t.Fatalf("QueryInformationJobObject() error = %v", err)
			}
			if got := info.BasicLimitInformation.LimitFlags; got != tt.wantFlags {
				t.Fatalf("LimitFlags = %#x, want %#x", got, tt.wantFlags)
			}
		})
	}
}
