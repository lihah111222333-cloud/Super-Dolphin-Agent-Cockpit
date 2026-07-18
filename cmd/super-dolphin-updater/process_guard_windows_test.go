//go:build windows

package main

import (
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureGuardProcessTreeSuspendsBeforeJobAttach(t *testing.T) {
	cmd := exec.Command("super-dolphin-guard")
	lease, err := configureGuardProcessTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if lease != nil {
		t.Fatal("Windows configure unexpectedly returned a Unix process-group lease")
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&(windows.CREATE_SUSPENDED|windows.CREATE_NEW_PROCESS_GROUP) != windows.CREATE_SUSPENDED|windows.CREATE_NEW_PROCESS_GROUP || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("Guard SysProcAttr = %+v, want suspended hidden new process group", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		t.Fatal("Guard must remain suspended until its kill-on-close Job is attached")
	}
}

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
