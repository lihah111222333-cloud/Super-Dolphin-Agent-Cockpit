//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestStandaloneDirectPeerCodexRestartReplay 验证整个Fx服务重启后原生回执重放不重复模型输入。
func TestStandaloneDirectPeerCodexRestartReplay(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	first := startDirectPeerDeliveryApp(t, cfg, db)
	t.Cleanup(func() { stopStandaloneTestApp(t, first.app) })
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, first.registry, workspace, "codex", model)
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: first.registry}, workspace)
	waitDirectPeerAgentIdle(t, ctx, first.agents, "receiver")
	snapshot, err := first.agents.Snapshot(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	s := peerNativeScenario{app: first, db: db, ctx: ctx, workspace: workspace, home: home, thread: snapshot.ThreadID}
	marker := "RESTART_" + rand.Text()
	body := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", marker)
	message, err := sendPeerNativeScenario(t, s, "restart-key", body)
	if err != nil {
		t.Fatal(err)
	}
	receipt := assertPeerNativeReceipt(t, s, message)
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
	assertPeerRestartReplay(t, s, message, receipt, before, body, marker)
}

// assertPeerRestartReplay 核对服务重建后的回执不变，并用独立任务验证原线程可继续工作。
func assertPeerRestartReplay(t *testing.T, s peerNativeScenario, message directpeer.Message, receipt contract.ProviderSubmissionReceipt, before contract.AgentReportResult, body, marker string) {
	t.Helper()
	replayed, err := sendPeerNativeScenario(t, s, "restart-key", body)
	if err != nil || replayed.MessageID != message.MessageID || replayed.SubmissionID != message.SubmissionID {
		t.Fatalf("restart replay changed authoritative submission: %v", err)
	}
	if !reflect.DeepEqual(receipt, assertPeerNativeReceipt(t, s, replayed)) {
		t.Fatal("restart changed native receipt")
	}
	after, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil || after.ReportSeq != before.ReportSeq || after.Report != before.Report {
		t.Fatalf("restart replay advanced report: %v", err)
	}
	assertBinaryCodexInputCount(t, s.home, s.thread, marker, 1)
	assertDirectPeerDeliveryDirection(t, s.ctx, s.app, s.workspace, "sender", "receiver", "post-restart-independent")
	current, err := s.app.agents.Snapshot(s.ctx, "receiver")
	if err != nil || current.ThreadID != s.thread {
		t.Fatal("independent post-restart task lost native thread identity")
	}
	assertBinaryCodexInputCount(t, s.home, s.thread, marker, 1)
}
