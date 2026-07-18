//go:build windows

package main

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func runGuardProcessTreeFixtureIfRequested() (bool, int) {
	return false, 0
}

func TestGuardProcessTreeHandoffClosesWindowsJobHandle(t *testing.T) {
	handle, err := createGuardKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	lease := &guardProcessTreeLease{job: handle}
	if err := closeGuardJobHandle(lease, true); err != nil {
		t.Fatal(err)
	}
	if lease.job != 0 {
		t.Fatalf("Guard Job handle = %v, want closed zero handle", lease.job)
	}
	if err := windows.CloseHandle(handle); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("closing released Guard Job handle error = %v, want invalid handle", err)
	}
}
