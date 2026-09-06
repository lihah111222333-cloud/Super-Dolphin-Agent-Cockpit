//go:build windows

package securefs

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// TestLockPinnedWindowsSentinelOps 验证 Windows 锁边界传递句柄、overlapped 与错误。
func TestLockPinnedWindowsSentinelOps(t *testing.T) {
	wantErr := errors.New("lock failed")
	wantHandle := windows.Handle(42)
	wantOverlapped := &windows.Overlapped{}
	called := false
	err := lockPinnedWindowsSentinel(wantHandle, wantOverlapped, pinnedDirectoryWindowsOps{
		lock: func(handle windows.Handle, overlapped *windows.Overlapped) error {
			called = true
			if handle != wantHandle || overlapped != wantOverlapped {
				t.Fatalf("lock args = (%v, %p), want (%v, %p)", handle, overlapped, wantHandle, wantOverlapped)
			}
			return wantErr
		},
	})
	if !called || !errors.Is(err, wantErr) {
		t.Fatalf("lockPinnedWindowsSentinel() called=%v error=%v, want wrapped test error", called, err)
	}
}

// TestUnlockPinnedWindowsSentinelOps 验证 Windows 解锁边界传递参数并拒绝缺失操作。
func TestUnlockPinnedWindowsSentinelOps(t *testing.T) {
	wantHandle := windows.Handle(43)
	wantOverlapped := &windows.Overlapped{}
	called := false
	err := unlockPinnedWindowsSentinel(wantHandle, wantOverlapped, pinnedDirectoryWindowsOps{
		unlock: func(handle windows.Handle, overlapped *windows.Overlapped) error {
			called = true
			if handle != wantHandle || overlapped != wantOverlapped {
				t.Fatalf("unlock args = (%v, %p), want (%v, %p)", handle, overlapped, wantHandle, wantOverlapped)
			}
			return nil
		},
	})
	if err != nil || !called {
		t.Fatalf("unlockPinnedWindowsSentinel() called=%v error=%v", called, err)
	}
	if err := unlockPinnedWindowsSentinel(wantHandle, wantOverlapped, pinnedDirectoryWindowsOps{}); err == nil {
		t.Fatal("unlockPinnedWindowsSentinel() accepted missing unlock operation")
	}
}
