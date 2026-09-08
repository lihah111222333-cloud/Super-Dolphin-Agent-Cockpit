//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// TestStandaloneDirectPeerClaudeRestartReplay 验证整个Fx服务重启后Claude原生UUID回执重放不重复输入。
func TestStandaloneDirectPeerClaudeRestartReplay(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "claude", Executable: cli, Home: home, Models: []string{model}, AuthMode: standalone.ClaudeAuthHostDefault}}}
	db := managedProviderDatabase(t, state)
	first := startDirectPeerDeliveryApp(t, cfg, db)
	t.Cleanup(func() { stopStandaloneTestApp(t, first.app) })
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, first.registry, workspace, "claude", model)
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: first.registry}, workspace)
	waitDirectPeerAgentIdle(t, ctx, first.agents, "receiver")
	snapshot, err := first.agents.Snapshot(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	s := peerNativeScenario{app: first, db: db, ctx: ctx, workspace: workspace, home: home, thread: snapshot.ThreadID}
	marker := "CLAUDE_RESTART_" + rand.Text()
	body := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", marker)
	message, err := sendPeerNativeScenario(t, s, "restart-key", body)
	if err != nil {
		t.Fatal(err)
	}
	receipt := assertClaudePeerReceipt(t, s, message)
	waitDirectPeerAgentIdle(t, ctx, first.agents, "receiver")
	assertDirectPeerDeliveryReport(t, ctx, first, "receiver", 0, marker)
	before, err := first.reports.GetReport(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.execution.Close(); err != nil {
		t.Fatal(err)
	}
	stopStandaloneTestApp(t, first.app)
	second := startDirectPeerDeliveryApp(t, cfg, db)
	defer stopStandaloneTestApp(t, second.app)
	s.app = second
	assertClaudePeerRestartReplay(t, s, message, receipt, before, body, marker)
}

// assertClaudePeerRestartReplay 核对服务重建后的回执不变，并明确检查Claude重激活能力边界。
func assertClaudePeerRestartReplay(t *testing.T, s peerNativeScenario, message directpeer.Message, receipt contract.ProviderSubmissionReceipt, before contract.AgentReportResult, body, marker string) {
	t.Helper()
	replayed, err := sendPeerNativeScenario(t, s, "restart-key", body)
	if err != nil || replayed.MessageID != message.MessageID || replayed.SubmissionID != message.SubmissionID {
		t.Fatalf("restart replay changed authoritative submission: %v", err)
	}
	if !reflect.DeepEqual(receipt, assertClaudePeerReceipt(t, s, replayed)) {
		t.Fatal("restart changed native receipt")
	}
	after, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil || after.ReportSeq != before.ReportSeq || after.Report != before.Report {
		t.Fatalf("restart replay advanced report: %v", err)
	}
	assertClaudePeerInputCount(t, s, marker, receipt.ReceiptID, 1)
	assertClaudeRestartCapabilityBoundary(t, s)
	assertClaudePeerInputCount(t, s, marker, receipt.ReceiptID, 1)
}

// assertClaudeRestartCapabilityBoundary 回执读取不需要恢复provider；Claude尚未提供持久线程重激活能力。
func assertClaudeRestartCapabilityBoundary(t *testing.T, s peerNativeScenario) {
	t.Helper()
	err := s.app.execution.ActivatePersistedRuntime(s.ctx, orchestration.PersistedRuntimeActivation{Provider: "claude", AgentID: "receiver", ThreadID: s.thread})
	var capability *contract.CapabilityError
	if !errors.Is(err, standalone.ErrExecutionNotReady) || !errors.As(err, &capability) || capability.Driver != "claude" || capability.Capability != providerdto.CapThreadResume {
		t.Fatalf("Claude restart must expose its unsupported capability: %v", err)
	}
}
