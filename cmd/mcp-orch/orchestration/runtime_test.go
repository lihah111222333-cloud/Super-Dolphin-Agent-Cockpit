package orchestration

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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

// TestUpdateRuntimeUnknownProviderFailsFast 验证 runtime 上报非法 provider
// 直接返中英双语错误，不落 snapshot（取代旧 silent Warn 行为）。
//
// TestUpdateRuntimeUnknownProviderFailsFast verifies that an unknown runtime
// provider is rejected fail-fast with a bilingual error and leaves the
// snapshot untouched (replaces the prior silent Warn behavior).
func TestUpdateRuntimeUnknownProviderFailsFast(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := pkglogger.New(pkglogger.NewTextHandler(&logs, nil))
	svc, reported, cancel := newRuntimeTestService(logger, runtimeTestAgent())
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{AgentID: "agent-1", Provider: " Custom "})
	if err == nil {
		t.Fatalf("UpdateRuntime() unknown provider must error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"custom", "claude", "codex", "invalid"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("err %q missing %q", msg, want)
		}
	}

	// snapshot 不应被污染：仍是初始 inferred provider。
	// snapshot must be untouched: still the initial inferred provider.
	snapshot, snapErr := svc.Snapshot(context.Background(), "agent-1")
	if snapErr != nil {
		t.Fatalf("Snapshot() error = %v", snapErr)
	}
	if snapshot.Provider == "custom" {
		t.Fatalf("snapshot provider leaked unknown value: %#v", snapshot)
	}

	// fail-fast 不发 runtime event；通道应为空。
	// fail-fast must not publish a runtime event; channel should be empty.
	select {
	case ev := <-reported:
		t.Fatalf("unexpected runtime event after fail-fast: %#v", ev)
	default:
	}
	// 不应再有 "unknown runtime provider" Warn 日志（已替换为返 error 路径）。
	// The legacy "unknown runtime provider" warn line must be gone.
	if strings.Contains(logs.String(), "unknown runtime provider") {
		t.Fatalf("legacy warn line should be removed; logs=%q", logs.String())
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

func TestSnapshotBuildsAgentBoardFromAuthoritativeRuntimeFields(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(15 * time.Minute)
	outcome := &agentdto.Outcome{Kind: agentdto.OutcomeKindFailure, Reason: "provider failed", CompletedAt: updatedAt}
	agent := runtimeTestAgent()
	agent.threadID = "thread-1"
	agent.parentID = "agent-root"
	agent.name = "worker"
	agent.state = agentdto.StateFailed
	agent.startedAt = startedAt
	agent.updatedAt = updatedAt
	agent.name = "实现看板契约"
	agent.prompt = "只使用权威字段"
	agent.outcome = outcome
	svc, _, cancel := newRuntimeTestService(silentLogger(), agent)
	defer cancel()

	snapshot, err := svc.Snapshot(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Assignment == nil || snapshot.Assignment.Title != agent.name || snapshot.Assignment.Description != agent.prompt || snapshot.Assignment.AssignedAt != startedAt {
		t.Fatalf("Snapshot().Assignment = %#v, want launch request and startedAt", snapshot.Assignment)
	}
	if snapshot.Progress.Status != string(agent.state) || snapshot.Progress.UpdatedAt != updatedAt {
		t.Fatalf("Snapshot().Progress = %#v, want runtime state and updatedAt", snapshot.Progress)
	}
	if snapshot.Progress.CurrentStep != nil || snapshot.Progress.CompletedSteps != nil || snapshot.Progress.TotalSteps != nil {
		t.Fatalf("Snapshot().Progress = %#v, want unavailable structured steps", snapshot.Progress)
	}
	if snapshot.Outcome == nil || snapshot.Outcome.Kind != agentdto.OutcomeKindFailure || snapshot.Outcome.Reason != outcome.Reason {
		t.Fatalf("Snapshot().Outcome = %#v, want explicit runtime outcome", snapshot.Outcome)
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
