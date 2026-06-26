//go:build !windows

package codexapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
)

func TestRunPoolSpawnAbortsAndCleansChildWhenPidregistryPersistFails(t *testing.T) {
	helper := installPoolPidregistryCodexHelper(t)
	tmpBlocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmpBlocker, []byte("block registry dir"), 0o600); err != nil {
		t.Fatalf("write tmp blocker: %v", err)
	}
	t.Setenv("TMPDIR", tmpBlocker)
	registry := pidregistry.New()
	t.Setenv("TMPDIR", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	transport, err := runPoolSpawn(ctx, t.TempDir(), "openai", registry, slog.Default())
	if err == nil {
		if transport != nil {
			_ = transport.Kill()
		}
		t.Fatal("runPoolSpawn() error = nil, want pidregistry persist failure")
	}
	if !strings.Contains(err.Error(), "pidregistry") {
		t.Fatalf("runPoolSpawn() error = %v, want pidregistry context", err)
	}

	childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
	waitForProcessExit(t, childPID, 5*time.Second)
}

func TestServerManagerStartLockedAbortsWhenPidregistryPersistFails(t *testing.T) {
	helper := installPoolPidregistryCodexHelper(t)
	tmpBlocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmpBlocker, []byte("block registry dir"), 0o600); err != nil {
		t.Fatalf("write tmp blocker: %v", err)
	}
	t.Setenv("TMPDIR", tmpBlocker)
	registry := pidregistry.New()
	t.Setenv("TMPDIR", t.TempDir())

	m := &ServerManager{pidRegistry: registry}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := m.startLocked(ctx)
	if err == nil {
		if m.process != nil {
			_ = m.process.Kill()
		}
		childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
		waitForProcessExit(t, childPID, 5*time.Second)
		t.Fatal("startLocked() error = nil, want pidregistry persist failure")
	}
	if !strings.Contains(err.Error(), "pidregistry") {
		t.Fatalf("startLocked() error = %v, want pidregistry context", err)
	}
	if m.err == nil {
		t.Fatal("ServerManager.err = nil, want pidregistry failure")
	}
	if m.ready || m.process != nil || m.serverURL != "" {
		t.Fatalf("manager state after failure: ready=%v process=%v serverURL=%q", m.ready, m.process, m.serverURL)
	}
	childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
	waitForProcessExit(t, childPID, 5*time.Second)
}

func TestPeerSupervisorInitialTrackAbortsWhenPidregistryRegisterFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	s, launcher, tracker := newTestSupervisor(t)
	tracker.failOnRegisterCall = 1
	tracker.registerErr = errors.New("pidregistry persist failed")

	err := s.Run(ctx)
	if err == nil {
		t.Fatal("Run() error = nil, want pidregistry registration failure")
	}
	if !strings.Contains(err.Error(), "pidregistry") {
		t.Fatalf("Run() error = %v, want pidregistry context", err)
	}
	handles := launcher.snapshotHandles("test-peer")
	if len(handles) != 1 || !handles[0].isClosed() {
		t.Fatalf("initial peer cleanup failed: handles=%d closed=%v", len(handles), len(handles) == 1 && handles[0].isClosed())
	}
	if len(s.snapshotPeers()) != 0 {
		t.Fatalf("tracked peers = %d, want none after registration failure", len(s.snapshotPeers()))
	}
	if tracker.has(handles[0].PID()) {
		t.Fatalf("pid %d registered despite injected failure", handles[0].PID())
	}
}

func TestPeerSupervisorRestartRegisterFailureAbortsNewPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, launcher, tracker := newTestSupervisor(t)
	tracker.failOnRegisterCall = 2
	tracker.registerErr = errors.New("pidregistry persist failed on restart")
	done := runSupervisor(ctx, s)

	launcher.waitLaunch(t, "test-peer", time.Second)
	first := launcher.snapshotHandles("test-peer")[0]
	first.triggerExit(errors.New("boom"))
	launcher.waitLaunch(t, "test-peer", time.Second)

	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("Run did not surface pidregistry registration failure")
	}
	if err == nil || !strings.Contains(err.Error(), "pidregistry") {
		t.Fatalf("Run() error = %v, want pidregistry context", err)
	}
	handles := launcher.snapshotHandles("test-peer")
	if len(handles) != 2 || !handles[1].isClosed() {
		t.Fatalf("restart peer cleanup failed: handles=%d secondClosed=%v", len(handles), len(handles) == 2 && handles[1].isClosed())
	}
	if tracker.has(handles[1].PID()) {
		t.Fatalf("pid %d registered despite injected failure", handles[1].PID())
	}
	if len(s.snapshotPeers()) != 0 {
		t.Fatalf("tracked peers = %d, want none after restart registration failure", len(s.snapshotPeers()))
	}
}

func installPoolPidregistryCodexHelper(t *testing.T) localCodexHelper {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	helper := localCodexHelper{
		logPath:      filepath.Join(dir, "events.log"),
		childPIDPath: filepath.Join(dir, "child.pid"),
	}
	scriptPath := filepath.Join(dir, "codex")
	script := fmt.Sprintf(
		"#!/bin/sh\nexec env GO_WANT_CODEX_HELPER=1 CODEX_HELPER_MODE=serve-with-child CODEX_HELPER_LOG=%s CODEX_HELPER_CHILD_PID_FILE=%s %s -test.run '^TestCodexHelperProcess$' -- \"$@\"\n",
		shellQuoteArg(helper.logPath),
		shellQuoteArg(helper.childPIDPath),
		shellQuoteArg(exe),
	)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return helper
}
