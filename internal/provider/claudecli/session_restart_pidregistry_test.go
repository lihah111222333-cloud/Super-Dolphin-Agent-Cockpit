package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
