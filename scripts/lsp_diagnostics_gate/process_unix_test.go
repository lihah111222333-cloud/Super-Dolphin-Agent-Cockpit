//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPeerCloseKillsLanguageServerProcessGroup(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "child.pid")
	client, err := startPeer(t.Context(), root, []string{
		"/bin/sh", "-c", fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidFile),
	})
	if err != nil {
		t.Fatal(err)
	}
	childPID := waitPeerChildPID(t, pidFile)
	client.close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer child process %d survived process-group cancellation", childPID)
}

func waitPeerChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				t.Fatalf("parse peer child PID: %v", parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read peer child PID: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("peer child PID was not written")
	return 0
}
