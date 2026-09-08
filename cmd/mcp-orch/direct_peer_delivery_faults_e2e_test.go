//go:build e2e

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

type gatedPeerNativeSubmitter struct {
	golden.OrchestrationStub
	calls   atomic.Int32
	entered chan struct{}
	release <-chan struct{}
}

// SubmitTurn 拒绝测试意外走error-only端口，不将其冒充原生确认。
func (*gatedPeerNativeSubmitter) SubmitTurn(context.Context, contract.TurnSubmission) error {
	return errors.New("unexpected error-only submission")
}

// SubmitTurnWithReceipt 用显式闸门控制原生确认窗口；仅此替身是模拟，Registry与SQLite保持真实。
func (s *gatedPeerNativeSubmitter) SubmitTurnWithReceipt(ctx context.Context, req contract.TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	s.calls.Add(1)
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return contract.ProviderSubmissionReceipt{}, ctx.Err()
		}
	}
	if req.AgentID != "receiver" || req.ThreadID != "receiver-thread" {
		return contract.ProviderSubmissionReceipt{}, errors.New("wrong native submission identity")
	}
	return contract.ProviderSubmissionReceipt{Provider: "codex", AgentID: req.AgentID, ThreadID: req.ThreadID, TurnID: "native-turn-1", ReceiptID: "native-receipt-1", LaunchSeq: 7}, nil
}

type peerFaultFixture struct {
	db        *sql.DB
	store     *directpeer.Store
	registry  tools.Registry
	ctx       context.Context
	workspace string
}

// newPeerFaultFixture 使用真实迁移数据库并通过operator Registry授予消息权限。
func newPeerFaultFixture(t *testing.T, submitter *gatedPeerNativeSubmitter) peerFaultFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	f := peerFaultFixture{db: directPeerStandaloneDatabase(t, t.TempDir()), ctx: ctx, workspace: t.TempDir()}
	f = rebuildPeerFaultRegistry(t, f, submitter)
	def, ok := f.registry.Lookup("manage_direct_peer")
	if !ok {
		t.Fatal("manage_direct_peer absent")
	}
	_, err := def.Handler(mcpcommon.WithToolScope(ctx, mcpcommon.ToolScope{CWD: f.workspace}), json.RawMessage(`{"action":"grant","sender_agent_id":"sender","receiver_agent_id":"receiver","idempotency_key":"grant","can_send":true,"can_read":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// rebuildPeerFaultRegistry 重建owner capability和handler实例，保留真实持久数据库。
func rebuildPeerFaultRegistry(t *testing.T, f peerFaultFixture, submitter *gatedPeerNativeSubmitter) peerFaultFixture {
	t.Helper()
	store, err := directpeer.NewStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	agents := &golden.OrchestrationStub{SnapshotFunc: func(_ context.Context, id string) (contract.AgentSnapshot, error) {
		return contract.AgentSnapshot{AgentID: id, ThreadID: id + "-thread", Cwd: f.workspace, State: "idle"}, nil
	}}
	f.store = store
	f.registry = tools.NewRegistry(tools.Dependencies{DirectPeer: store, DirectPeerAgents: agents, DirectPeerTurns: submitter})
	if _, err := f.registry.List(); err != nil {
		t.Fatal(err)
	}
	return f
}

// sendPeerFaultMessage 执行真实Registry handler，固定业务消息便于跨owner重放证明。
func sendPeerFaultMessage(f peerFaultFixture) (any, error) {
	def, ok := f.registry.Lookup("send_direct_peer")
	if !ok {
		return nil, errors.New("send_direct_peer absent")
	}
	return def.Handler(mcpcommon.WithToolScope(f.ctx, mcpcommon.ToolScope{CWD: f.workspace, AgentID: "sender"}), json.RawMessage(`{"receiver_agent_id":"receiver","idempotency_key":"message","message":"fault-window-message"}`))
}

// TestStandaloneDirectPeerConcurrentSubmissionFault 验证同key并发在原生确认前只有一个提交者，竞争者unknown后可读原确认。
func TestStandaloneDirectPeerConcurrentSubmissionFault(t *testing.T) {
	release := make(chan struct{})
	submitter := &gatedPeerNativeSubmitter{entered: make(chan struct{}, 1), release: release}
	f := newPeerFaultFixture(t, submitter)
	type result struct {
		value any
		err   error
	}
	first := make(chan result, 1)
	var workers sync.WaitGroup
	workers.Go(func() { value, err := sendPeerFaultMessage(f); first <- result{value, err} })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		workers.Wait()
	})
	select {
	case <-submitter.entered:
	case <-f.ctx.Done():
		t.Fatal("first submission did not reach gate")
	}
	_, err := sendPeerFaultMessage(f)
	assertPeerFaultUnknown(t, err)
	assertPeerFaultRows(t, f, "accepted", 0)
	if submitter.calls.Load() != 1 {
		t.Fatalf("concurrent native calls=%d", submitter.calls.Load())
	}
	close(release)
	var completed result
	select {
	case completed = <-first:
	case <-f.ctx.Done():
		t.Fatal("first submission did not complete")
	}
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	assertPeerFaultSubmittedReplay(t, f, submitter, completed.value)
}

// TestStandaloneDirectPeerReceiptCommitFailureRestartFault 验证真实native确认后event/outbox写失败全回滚，重建owner也不重发。
func TestStandaloneDirectPeerReceiptCommitFailureRestartFault(t *testing.T) {
	for _, table := range []string{"orchestration_direct_peer_events", "orchestration_direct_peer_event_outbox"} {
		t.Run(table, func(t *testing.T) {
			submitter := &gatedPeerNativeSubmitter{}
			f := newPeerFaultFixture(t, submitter)
			installPeerReceiptFailure(t, f, table)
			_, err := sendPeerFaultMessage(f)
			assertPeerFaultUnknown(t, err)
			if submitter.calls.Load() != 1 {
				t.Fatalf("acknowledged native calls=%d", submitter.calls.Load())
			}
			assertPeerFaultRows(t, f, "accepted", 0)
			if _, err := f.db.ExecContext(f.ctx, "DROP TRIGGER fail_peer_native_receipt"); err != nil {
				t.Fatal(err)
			}
			restarted := &gatedPeerNativeSubmitter{}
			f = rebuildPeerFaultRegistry(t, f, restarted)
			for range 2 {
				_, err := sendPeerFaultMessage(f)
				assertPeerFaultUnknown(t, err)
			}
			if restarted.calls.Load() != 0 || submitter.calls.Load() != 1 {
				t.Fatal("owner rebuild resubmitted unknown delivery")
			}
			assertPeerFaultRows(t, f, "accepted", 0)
		})
	}
}

// installPeerReceiptFailure 只在native submitted写入触发故障，不阻止此前accepted意图事务。
func installPeerReceiptFailure(t *testing.T, f peerFaultFixture, table string) {
	t.Helper()
	condition := "NEW.event_type='message.submitted'"
	if table == "orchestration_direct_peer_event_outbox" {
		condition = "EXISTS (SELECT 1 FROM orchestration_direct_peer_events e WHERE e.event_id=NEW.event_id AND e.event_type='message.submitted')"
	}
	statement := "CREATE TRIGGER fail_peer_native_receipt BEFORE INSERT ON " + table + " WHEN " + condition + " BEGIN SELECT RAISE(ABORT, 'injected native receipt failure'); END"
	if _, err := f.db.ExecContext(f.ctx, statement); err != nil {
		t.Fatal(err)
	}
}

// assertPeerFaultUnknown 保留unknown的不可自动重试语义。
func assertPeerFaultUnknown(t *testing.T, err error) {
	t.Helper()
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "delivery_outcome_unknown" || coded.Retryable {
		t.Fatalf("error=%v want nonretryable delivery_outcome_unknown", err)
	}
}

// assertPeerFaultRows 验证消息、submitted事件及outbox原子闭包，没有假确认残留。
func assertPeerFaultRows(t *testing.T, f peerFaultFixture, status string, submitted int) {
	t.Helper()
	var count int
	var gotStatus, submission string
	if err := f.db.QueryRowContext(f.ctx, "SELECT count(*),status,submission_id FROM orchestration_direct_peer_messages").Scan(&count, &gotStatus, &submission); err != nil {
		t.Fatal(err)
	}
	if count != 1 || gotStatus != status {
		t.Fatalf("message count/status=%d/%s want=1/%s", count, gotStatus, status)
	}
	if status == "accepted" && submission != "" {
		t.Fatal("accepted message has false native submission")
	}
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_events WHERE event_type='message.submitted'", submitted)
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_event_outbox", 2+submitted)
	assertPeerFaultCount(t, f, "SELECT count(*) FROM orchestration_direct_peer_events", 2+submitted)
}

// assertPeerFaultCount 精确验证事务各表数量，不用异步event投影代替durable真值。
func assertPeerFaultCount(t *testing.T, f peerFaultFixture, query string, want int) {
	t.Helper()
	var count int
	if err := f.db.QueryRowContext(f.ctx, query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("durable count=%d want=%d query=%s", count, want, query)
	}
}

// assertPeerFaultSubmittedReplay 校验原确认持久化后同key返回同message/turn，native调用保持一次。
func assertPeerFaultSubmittedReplay(t *testing.T, f peerFaultFixture, submitter *gatedPeerNativeSubmitter, value any) {
	t.Helper()
	var first directpeer.Message
	if err := json.Unmarshal(mustJSON(t, value), &first); err != nil {
		t.Fatal(err)
	}
	repeated, err := sendPeerFaultMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	var replay directpeer.Message
	if err := json.Unmarshal(mustJSON(t, repeated), &replay); err != nil {
		t.Fatal(err)
	}
	assertPeerFaultReplayIdentity(t, first, replay)
	receipt, err := f.store.ReadNativeReceipt(f.ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ReceiptID != "native-receipt-1" || receipt.LaunchSeq != 7 || submitter.calls.Load() != 1 {
		t.Fatalf("receipt=%+v native calls=%d", receipt, submitter.calls.Load())
	}
	assertPeerFaultRows(t, f, "submitted", 1)
}

// assertPeerFaultReplayIdentity 绑定同一持久message与native turn，不能以状态字符串冒充幂等。
func assertPeerFaultReplayIdentity(t *testing.T, first, replay directpeer.Message) {
	t.Helper()
	if first.MessageID == "" || first.MessageID != replay.MessageID || first.SubmissionID != "native-turn-1" || replay.SubmissionID != first.SubmissionID {
		t.Fatal("replay changed native/message identities")
	}
}
