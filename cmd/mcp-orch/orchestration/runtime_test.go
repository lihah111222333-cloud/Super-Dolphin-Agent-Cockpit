package orchestration

import (
	"bytes"
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
	"testing"
)

func TestUpdateRuntimePrefersReportedValues(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{
		AgentID:  "agent-1",
		Port:     9090,
		Provider: "claude",
	})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 9090 || snapshot.PortSource != "runtime" {
		t.Fatalf("snapshot port = (%d, %q), want (9090, runtime)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "claude" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (claude, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 9090 || ev.Provider != "claude" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestUpdateRuntimePartialPort(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Port: 9090})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 9090 || snapshot.PortSource != "runtime" {
		t.Fatalf("snapshot port = (%d, %q), want (9090, runtime)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "inferred" {
		t.Fatalf("snapshot provider = (%q, %q), want (codex, inferred)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 9090 || ev.Provider != "codex" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestUpdateRuntimeSourceOnlyChangeFiresEvent(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Port: 8080})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 8080 || snapshot.PortSource != "runtime" {
		t.Fatalf("snapshot port = (%d, %q), want (8080, runtime)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "inferred" {
		t.Fatalf("snapshot provider = (%q, %q), want (codex, inferred)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 8080 || ev.Provider != "codex" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestUpdateRuntimePartialProvider(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Provider: "claude"})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "claude" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (claude, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 8080 || ev.Provider != "claude" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestUpdateRuntimeIdempotent(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()
	first := RuntimeReport{AgentID: "agent-1", Port: 9090, Provider: "claude"}

	if err := svc.UpdateRuntime(context.Background(), first); err != nil {
		t.Fatalf("first UpdateRuntime() error = %v", err)
	}
	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 9090 || ev.Provider != "claude" {
		t.Fatalf("runtime event = %#v", ev)
	}

	if err := svc.UpdateRuntime(context.Background(), first); err != nil {
		t.Fatalf("second UpdateRuntime() error = %v", err)
	}
	expectNoRuntimeEvent(t, reported)
}

func TestUpdateRuntimeUnknownProviderWarns(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := pkglogger.New(pkglogger.NewTextHandler(&logs, nil))
	svc, reported, cancel := newRuntimeTestService(logger, runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Provider: " Custom "})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Provider != "custom" || snapshot.ProviderSource != "runtime-unverified" {
		t.Fatalf("snapshot provider = (%q, %q), want (custom, runtime-unverified)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 8080 || ev.Provider != "custom" {
		t.Fatalf("runtime event = %#v", ev)
	}
	if !strings.Contains(logs.String(), "unknown runtime provider") ||
		!strings.Contains(logs.String(), "agent_id=agent-1") ||
		!strings.Contains(logs.String(), "provider=custom") {
		t.Fatalf("warning log = %q", logs.String())
	}
}

func TestUpdateRuntimeExistingRuntimeOverridePartial(t *testing.T) {
	t.Parallel()

	agent := runtimeTestAgent()
	agent.port = 7000
	agent.portSource = "inferred"
	agent.runtimePort = 8080
	agent.provider = "claude"
	agent.providerSource = "inferred"
	agent.portSource = "runtime"
	svc, reported, cancel := newRuntimeTestService(silentLogger(), agent)
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Provider: "codex"})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 8080 || snapshot.PortSource != "runtime" {
		t.Fatalf("snapshot port = (%d, %q), want (8080, runtime)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (codex, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 8080 || ev.Provider != "codex" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestUpdateRuntimeZeroPortDoesNotClearRuntimePort(t *testing.T) {
	t.Parallel()

	agent := runtimeTestAgent()
	agent.runtimePort = 9090
	agent.portSource = "runtime"
	svc, reported, cancel := newRuntimeTestService(silentLogger(), agent)
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Port: 0, Provider: "codex"})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 9090 || snapshot.PortSource != "runtime" {
		t.Fatalf("snapshot port = (%d, %q), want (9090, runtime)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (codex, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 9090 || ev.Provider != "codex" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestUpdateRuntimePort0WithoutProvider(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Port: 0})
	if err == nil || !strings.Contains(err.Error(), "must include port or provider") {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}
	expectNoRuntimeEvent(t, reported)
}

func TestUpdateRuntimePort0WithProvider(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Port: 0, Provider: "claude"})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "claude" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (claude, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 8080 || ev.Provider != "claude" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func TestPrepareLaunchStateLockedClearsStaleRuntimeValues(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil)
	req := LaunchRequest{
		AgentID: "agent-1",
		Command: []string{"agent"},
		Env:     []string{"PORT=8080", "AGENT_PROVIDER=codex"},
	}
	agent := svc.agentForLaunchLocked(req)
	agent.runtimePort = 9090
	agent.runtimeProvider = "claude"
	agent.portSource = "runtime"
	agent.providerSource = "runtime"

	agent = svc.agentForLaunchLocked(req)
	if err := svc.prepareLaunchStateLocked(context.Background(), agent); err != nil {
		t.Fatalf("prepareLaunchStateLocked() error = %v", err)
	}

	snapshot := svc.snapshotLocked(context.Background(), agent)
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port after relaunch prep = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "inferred" {
		t.Fatalf("snapshot provider after relaunch prep = (%q, %q), want (codex, inferred)", snapshot.Provider, snapshot.ProviderSource)
	}
}
