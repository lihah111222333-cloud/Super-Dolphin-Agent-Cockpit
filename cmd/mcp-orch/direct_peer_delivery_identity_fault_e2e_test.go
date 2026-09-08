//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

type identityFaultNativeSubmitter struct {
	golden.OrchestrationStub
	mode           string
	calls          atomic.Int32
	generation     atomic.Uint64
	capturedThread string
}

// SubmitTurn 拒绝意外走无原生确认接口，不能用nil错误伪造provider确认。
func (*identityFaultNativeSubmitter) SubmitTurn(context.Context, contract.TurnSubmission) error {
	return errors.New("unexpected error-only submission")
}

// SubmitTurnWithReceipt 在Snapshot之后改变受控owner代际；线程漂移由Registry核对，代际由native owner拒绝。
func (s *identityFaultNativeSubmitter) SubmitTurnWithReceipt(_ context.Context, req contract.TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	s.calls.Add(1)
	s.capturedThread = req.ThreadID
	s.generation.Add(1)
	receipt := contract.ProviderSubmissionReceipt{Provider: "codex", AgentID: req.AgentID, ThreadID: req.ThreadID, TurnID: "identity-turn", ReceiptID: "identity-receipt", LaunchSeq: 8}
	switch s.mode {
	case "replacement-thread":
		receipt.ThreadID = "replacement-thread"
	case "old-thread":
		receipt.ThreadID = "prior-thread"
	case "generation-replaced":
		return contract.ProviderSubmissionReceipt{}, errors.New("native submission session generation changed")
	default:
		return contract.ProviderSubmissionReceipt{}, errors.New("unsupported controlled identity fault")
	}
	return receipt, nil
}

// TestStandaloneDirectPeerReceiverIdentityFault 真实SQLite/Registry拒绝漂移确认及owner报告的代际变化，不调用模型。
func TestStandaloneDirectPeerReceiverIdentityFault(t *testing.T) {
	for _, mode := range []string{"replacement-thread", "old-thread", "generation-replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newPeerFaultFixture(t, &gatedPeerNativeSubmitter{})
			native := &identityFaultNativeSubmitter{mode: mode}
			native.generation.Store(7)
			f.registry = identityFaultRegistry(t, f, native)
			_, err := sendPeerFaultMessage(f)
			assertPeerFaultUnknown(t, err)
			if native.calls.Load() != 1 || native.capturedThread != "receiver-thread" || native.generation.Load() != 8 {
				t.Fatalf("identity fault not reached: calls=%d thread=%s generation=%d", native.calls.Load(), native.capturedThread, native.generation.Load())
			}
			assertPeerFaultRows(t, f, "accepted", 0)
			restarted := &gatedPeerNativeSubmitter{}
			f = rebuildPeerFaultRegistry(t, f, restarted)
			_, err = sendPeerFaultMessage(f)
			assertPeerFaultUnknown(t, err)
			if restarted.calls.Load() != 0 {
				t.Fatal("unknown identity delivery resubmitted after registry rebuild")
			}
			assertPeerFaultRows(t, f, "accepted", 0)
		})
	}
}

// identityFaultRegistry 仅替换native提交边界，Snapshot和迁移数据库/真实handler保持固定旧身份。
func identityFaultRegistry(t *testing.T, f peerFaultFixture, turns contract.TurnSubmissionPort) tools.Registry {
	t.Helper()
	agents := &golden.OrchestrationStub{SnapshotFunc: func(_ context.Context, id string) (contract.AgentSnapshot, error) {
		return contract.AgentSnapshot{AgentID: id, ThreadID: id + "-thread", Cwd: f.workspace, State: "idle"}, nil
	}}
	registry := tools.NewRegistry(tools.Dependencies{DirectPeer: f.store, DirectPeerAgents: agents, DirectPeerTurns: turns})
	if _, err := registry.List(); err != nil {
		t.Fatal(err)
	}
	return registry
}

// TestStandaloneDirectPeerFailedReplayRecoveryFault 缺投递端口形成持久失败；重建端口不重发原key，独立新任务可提交。
func TestStandaloneDirectPeerFailedReplayRecoveryFault(t *testing.T) {
	f := newPeerFaultFixture(t, &gatedPeerNativeSubmitter{})
	f.registry = identityFaultRegistry(t, f, nil)
	if _, err := sendPeerFaultMessage(f); err == nil {
		t.Fatal("missing delivery port reported success")
	}
	var messageID, status string
	if err := f.db.QueryRowContext(f.ctx, `SELECT message_id,status FROM orchestration_direct_peer_messages`).Scan(&messageID, &status); err != nil {
		t.Fatal(err)
	}
	if messageID == "" || status != "delivery_failed" {
		t.Fatalf("missing port did not persist failure: id=%s status=%s", messageID, status)
	}
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_events WHERE event_type='message.delivery_failed'", 1)
	restored := &gatedPeerNativeSubmitter{}
	f = rebuildPeerFaultRegistry(t, f, restored)
	value, err := sendPeerFaultMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	assertIdentityFaultFailedReplay(t, value, messageID, restored)
	value, err = sendIdentityFaultFreshTask(f)
	if err != nil {
		t.Fatal(err)
	}
	assertIdentityFaultFreshTask(t, f, value, messageID, restored)
}

// assertIdentityFaultFailedReplay 原失败结果应原样可读，但没有新native调用或submitted事件。
func assertIdentityFaultFailedReplay(t *testing.T, value any, messageID string, native *gatedPeerNativeSubmitter) {
	t.Helper()
	var replay directpeer.Message
	if err := json.Unmarshal(mustJSON(t, value), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.MessageID != messageID || replay.Status != "delivery_failed" || replay.SubmissionID != "" || native.calls.Load() != 0 {
		t.Fatalf("failed replay changed result or resubmitted: %+v calls=%d", replay, native.calls.Load())
	}
}

// sendIdentityFaultFreshTask 使用显式独立新key和新正文，不将unknown或失败旧任务换key盲目重发。
func sendIdentityFaultFreshTask(f peerFaultFixture) (any, error) {
	def, ok := f.registry.Lookup("send_direct_peer")
	if !ok {
		return nil, errors.New("send_direct_peer absent")
	}
	return def.Handler(mcpcommon.WithToolScope(f.ctx, mcpcommon.ToolScope{CWD: f.workspace, AgentID: "sender"}), json.RawMessage(`{"receiver_agent_id":"receiver","idempotency_key":"independent-new-task","message":"separate authorized new task"}`))
}

// assertIdentityFaultFreshTask 绑定独立新message与原生回执，并证明原失败行和事件没有被覆盖。
func assertIdentityFaultFreshTask(t *testing.T, f peerFaultFixture, value any, failedID string, native *gatedPeerNativeSubmitter) {
	t.Helper()
	var fresh directpeer.Message
	if err := json.Unmarshal(mustJSON(t, value), &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.MessageID == failedID || fresh.Status != "submitted" || native.calls.Load() != 1 {
		t.Fatalf("new independent task invalid: %+v calls=%d", fresh, native.calls.Load())
	}
	receipt, err := f.store.ReadNativeReceipt(f.ctx, fresh)
	if err != nil || receipt.ThreadID != "receiver-thread" || receipt.LaunchSeq != 7 {
		t.Fatalf("new native receipt=%+v err=%v", receipt, err)
	}
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_messages WHERE status='delivery_failed'", 1)
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_messages", 2)
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_events WHERE event_type='message.submitted'", 1)
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_event_outbox", 5)
}
