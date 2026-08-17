//go:build !windows

package pidregistry

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestCleanupStaleRejectsUntrustedRegistryProvenanceWithoutSignalingChildren(t *testing.T) {
	tests := []struct {
		name         string
		mode         os.FileMode
		filenamePID  int
		jsonAppPID   int
		includeNonce bool
	}{
		{
			name:         "group writable registry",
			mode:         0o660,
			filenamePID:  unusedPID(t, 1),
			jsonAppPID:   unusedPID(t, 1),
			includeNonce: true,
		},
		{
			name:         "world writable registry",
			mode:         0o666,
			filenamePID:  unusedPID(t, 2),
			jsonAppPID:   unusedPID(t, 2),
			includeNonce: true,
		},
		{
			name:         "filename JSON PID mismatch",
			mode:         0o600,
			filenamePID:  unusedPID(t, 3),
			jsonAppPID:   unusedPID(t, 4),
			includeNonce: true,
		},
		{
			name:         "missing nonce",
			mode:         0o600,
			filenamePID:  unusedPID(t, 5),
			jsonAppPID:   unusedPID(t, 5),
			includeNonce: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			childPID := startSleepChild(t)
			path := registryPath(tt.filenamePID)
			writeRawRegistryFile(t, path, tt.mode, tt.jsonAppPID, childPID, tt.includeNonce)
			t.Cleanup(func() { _ = os.Remove(path) })

			CleanupStaleWithProtectedPIDs(nil)

			if exited, err := reapExitedChild(childPID); err != nil {
				t.Fatalf("check child PID %d status: %v", childPID, err)
			} else if exited {
				t.Fatalf("cleanup signaled child PID %d for rejected registry %s", childPID, path)
			}
		})
	}
}

func startSleepChild(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep child: %v", err)
	}
	t.Cleanup(func() {
		if isProcessAlive(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func reapExitedChild(pid int) (bool, error) {
	var status syscall.WaitStatus
	got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err != nil {
		return false, err
	}
	return got == pid, nil
}

func writeRawRegistryFile(t *testing.T, path string, mode os.FileMode, appPID, childPID int, includeNonce bool) {
	t.Helper()
	nonceField := ""
	if includeNonce {
		nonceField = `"nonce":"test-nonce",`
	}
	raw := fmt.Sprintf(
		`{"app_pid":%d,%s"created_at":"2026-07-06T00:00:00Z","parent_executable_fingerprint":"sha256:test","children":[{"pid":%d,"kind":"probe"}]}`,
		appPID,
		nonceField,
		childPID,
	)
	if err := os.WriteFile(path, []byte(raw), mode); err != nil {
		t.Fatalf("write raw registry file: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod raw registry file: %v", err)
	}
}

func unusedPID(t *testing.T, salt int) int {
	t.Helper()
	pid := 90000000 + salt*1000 + os.Getpid()%1000
	for isProcessAlive(pid) {
		pid++
	}
	return pid
}
