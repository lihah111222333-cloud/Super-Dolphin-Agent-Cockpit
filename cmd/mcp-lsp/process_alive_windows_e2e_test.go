//go:build e2e && windows

package main

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// processAliveForE2E 通过查询受限句柄检查 Windows 测试目标进程是否存活。
func processAliveForE2E(pid int) (alive bool, retErr error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		retErr = errors.Join(retErr, windows.CloseHandle(handle))
	}()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, err
	}
	return exitCode == windowsStillActive, nil
}

func requireWindowsGoplsStartIdentity(t *testing.T, pid int) windowsGoplsProvisionalIdentity {
	t.Helper()
	start, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		t.Fatalf("read Windows process %d start identity: %v", pid, err)
	}
	return windowsGoplsProvisionalIdentity{PID: pid, StartIdentity: start}
}

func windowsGoplsProcessStartIdentity(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	var creation, exit, kernel, user windows.Filetime
	identityErr := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
	closeErr := windows.CloseHandle(handle)
	if identityErr != nil || closeErr != nil {
		return "", errors.Join(identityErr, closeErr)
	}
	return strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10), nil
}

func requireWindowsGoplsExactIdentitiesGone(t *testing.T, timeout time.Duration, identities ...windowsGoplsProvisionalIdentity) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		allGone := true
		for _, identity := range identities {
			alive, err := windowsGoplsExactIdentityAlive(identity)
			if err != nil {
				t.Fatalf("inspect exact Windows process %+v: %v", identity, err)
			}
			allGone = allGone && !alive
		}
		if allGone {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Windows gopls provisional processes remained after %s: %+v", timeout, identities)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func windowsGoplsExactIdentityAlive(identity windowsGoplsProvisionalIdentity) (bool, error) {
	alive, err := processAliveForE2E(identity.PID)
	if err != nil || !alive {
		return alive, err
	}
	start, err := windowsGoplsProcessStartIdentity(identity.PID)
	return err == nil && start == identity.StartIdentity, err
}

func cleanupWindowsGoplsExactIdentity(t *testing.T, identity windowsGoplsProvisionalIdentity) {
	t.Helper()
	alive, err := windowsGoplsExactIdentityAlive(identity)
	if err != nil {
		t.Errorf("inspect exact Windows process during cleanup %+v: %v", identity, err)
		return
	}
	if !alive {
		return
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(identity.PID))
	if err != nil {
		t.Errorf("open exact Windows process for cleanup %+v: %v", identity, err)
		return
	}
	terminateErr := windows.TerminateProcess(handle, 1)
	closeErr := windows.CloseHandle(handle)
	if terminateErr != nil || closeErr != nil {
		t.Errorf("terminate exact Windows process %+v: %v", identity, errors.Join(terminateErr, closeErr))
	}
}

func formatWindowsGoplsIdentity(identity windowsGoplsProvisionalIdentity) string {
	return fmt.Sprintf("pid=%d/start=%s", identity.PID, identity.StartIdentity)
}
