package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
)

func TestRestartIfNeededLockedReRegistersPIDRegistry(t *testing.T) {
	reg := pidregistry.New()
	t.Cleanup(reg.Close)
	oldTransport := newInterruptTestTransport(t, "while :; do sleep 1; done")
	nextTransport := newInterruptTestTransport(t, "while :; do sleep 1; done")
	registerTransportPID(reg, oldTransport, "agent-1")
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return nextTransport, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := assumeSessionLaunchOverride(&session{
		agentID:         "agent-1",
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		threadReady:     oldReady,
		transport:       oldTransport,
		cleanup:         func() {},
		pidRegistry:     reg,
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.mu.Lock()
	err := s.restartIfNeededLocked(ctx, turnRequest("claude-new"))
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("restartIfNeededLocked() error = %v", err)
	}

	oldPID := oldTransport.cmd.Process.Pid
	newPID := nextTransport.cmd.Process.Pid
	pids := currentRegistryChildPIDs(t, "claude-cli")
	if containsPID(pids, oldPID) {
		t.Fatalf("registry still contains old PID %d: %v", oldPID, pids)
	}
	if !containsPID(pids, newPID) {
		t.Fatalf("registry missing new PID %d: %v", newPID, pids)
	}
}

func TestRegisterTransportPIDReturnsPersistError(t *testing.T) {
	reg := newFailingPIDRegistry(t)
	tr := newInterruptTestTransport(t, "while :; do sleep 1; done")

	err := registerTransportPID(reg, tr, "agent-1")
	if err == nil {
		t.Fatal("registerTransportPID() error = nil, want persist failure")
	}
	if !strings.Contains(err.Error(), "register claude-cli pid") {
		t.Fatalf("registerTransportPID() error = %v, want claude pid context", err)
	}
}

func TestAwaitStartedSessionStopsTransportWhenPIDRegistryFails(t *testing.T) {
	reg := newFailingPIDRegistry(t)
	tr := newInterruptTestTransport(t, "while :; do sleep 1; done")
	ready := make(chan struct{})
	close(ready)
	s := &session{
		agentID:         "agent-1",
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		threadReady:     ready,
		transport:       tr,
		pidRegistry:     reg,
		suppressedTurns: map[string]struct{}{},
	}
	d := &driver{pidRegistry: reg}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := d.awaitStartedSession(ctx, s, tr)
	if err == nil {
		t.Fatal("awaitStartedSession() error = nil, want pid registry failure")
	}
	if tr.Running() {
		t.Fatal("transport still running after pid registry registration failure")
	}
}

func TestRestartIfNeededLockedKeepsOldTransportWhenPIDRegistryFails(t *testing.T) {
	reg := newFailingPIDRegistry(t)
	oldTransport := newInterruptTestTransport(t, "while :; do sleep 1; done")
	nextTransport := newInterruptTestTransport(t, "while :; do sleep 1; done")
	cleanupCalled := false
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return nextTransport, func() { cleanupCalled = true }, nil
	})
	oldReady := make(chan struct{})
	close(oldReady)
	s := assumeSessionLaunchOverride(&session{
		agentID:         "agent-1",
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		threadReady:     oldReady,
		transport:       oldTransport,
		cleanup:         func() {},
		pidRegistry:     reg,
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
		transportModel:  "claude-old",
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	s.mu.Lock()
	err := s.restartIfNeededLocked(ctx, turnRequest("claude-new"))
	currentTransport := s.transport
	currentReady := s.threadReady
	s.mu.Unlock()
	if err == nil {
		t.Fatal("restartIfNeededLocked() error = nil, want pid registry failure")
	}
	if currentTransport != oldTransport {
		t.Fatal("restart switched transport after pid registry registration failure")
	}
	if currentReady != oldReady {
		t.Fatal("restart replaced ready channel after pid registry registration failure")
	}
	if !cleanupCalled {
		t.Fatal("restart failure did not call new transport cleanup")
	}
	if nextTransport.Running() {
		t.Fatal("new transport still running after pid registry registration failure")
	}
}

func newFailingPIDRegistry(t *testing.T) *pidregistry.Registry {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block registry dir"), 0o644); err != nil {
		t.Fatalf("write registry dir blocker: %v", err)
	}
	t.Setenv("TMPDIR", blocker)
	t.Setenv("TMP", blocker)
	t.Setenv("TEMP", blocker)
	return pidregistry.New()
}

func currentRegistryChildPIDs(t *testing.T, kind string) []int {
	t.Helper()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("super-agent-pids-%d.json", os.Getpid()))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry file %s: %v", path, err)
	}
	var snapshot struct {
		Children []struct {
			PID  int    `json:"pid"`
			Kind string `json:"kind"`
		} `json:"children"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode registry file %s: %v", path, err)
	}
	pids := make([]int, 0, len(snapshot.Children))
	for _, child := range snapshot.Children {
		if child.Kind != kind {
			continue
		}
		pids = append(pids, child.PID)
	}
	return pids
}

func containsPID(pids []int, target int) bool {
	for _, pid := range pids {
		if pid == target {
			return true
		}
	}
	return false
}
