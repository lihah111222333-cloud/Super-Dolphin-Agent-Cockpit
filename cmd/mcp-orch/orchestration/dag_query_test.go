package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// makeGetRunService 构造测试用 service：仅注入 runStore（GetRun 只用 runStore）。
// makeGetRunService builds the service under test for GetRun, wiring only the
// runStore (GetRun does not touch dagStore).
func makeGetRunService(runStore taskdag.RunStore) *service {
	// dagStore 也注入是为了让 service.dagStore != nil 通过初期防御检查，
	// 与 makeStartDAGService 保持一致。
	// dagStore is also wired so that service.dagStore != nil passes any future
	// defensive checks; mirrors makeStartDAGService.
	return &service{dagStore: &stubStartDAGStore{}, runStore: runStore}
}

// ---- happy path ----
func TestGetRun_HappyPath(t *testing.T) {
	now := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	finished := now.Add(2 * time.Minute)
	stub := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:                 42,
			RunKey:             "dag-1#run-abc",
			DagKey:             "dag-1",
			DagVersionSnapshot: 7,
			TriggerSource:      "manual",
			Status:             "running",
			StartedAt:          now,
			FinishedAt:         &finished,
			Events:             json.RawMessage(`[]`),
			BudgetUsed:         3,
			Metadata:           json.RawMessage(`{"x":1}`),
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if err != nil {
		t.Fatalf("GetRun() error = %v, want nil", err)
	}
	if got := len(stub.getRunCalls); got != 1 || stub.getRunCalls[0] != "dag-1#run-abc" {
		t.Errorf("getRunCalls = %v, want [\"dag-1#run-abc\"]", stub.getRunCalls)
	}
	if resp.Run.ID != 42 || resp.Run.RunKey != "dag-1#run-abc" || resp.Run.DagKey != "dag-1" {
		t.Errorf("resp.Run identity mismatch: %+v", resp.Run)
	}
	if resp.Run.DagVersionSnapshot != 7 || resp.Run.TriggerSource != "manual" || resp.Run.Status != "running" {
		t.Errorf("resp.Run scalar fields mismatch: %+v", resp.Run)
	}
	if !resp.Run.StartedAt.Equal(now) {
		t.Errorf("resp.Run.StartedAt = %v, want %v", resp.Run.StartedAt, now)
	}
	if resp.Run.FinishedAt == nil || !resp.Run.FinishedAt.Equal(finished) {
		t.Errorf("resp.Run.FinishedAt = %v, want %v", resp.Run.FinishedAt, finished)
	}
}

// ---- run_key 缺失 → required 校验 ----
func TestGetRun_BlankRunKey_Rejected(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "   "})
	if err == nil {
		t.Fatalf("GetRun() error = nil, want required error")
	}
	if got := len(stub.getRunCalls); got != 0 {
		t.Errorf("GetRun should short-circuit before runStore call, got %d calls", got)
	}
	if errors.Is(err, ErrRunNotFound) {
		t.Errorf("err = %v should NOT match ErrRunNotFound for blank input", err)
	}
	if resp.Run.RunKey != "" {
		t.Errorf("resp.Run.RunKey = %q, want empty on validation failure", resp.Run.RunKey)
	}
}

// ---- runStore 未注入 → ErrRunStoreUnset ----
func TestGetRun_RunStoreUnset(t *testing.T) {
	svc := &service{} // 裸 service，runStore == nil
	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if !errors.Is(err, ErrRunStoreUnset) {
		t.Fatalf("GetRun() error = %v, want errors.Is(ErrRunStoreUnset)", err)
	}
}

// ---- IsNotFound 域错误 → ErrRunNotFound ----
// pgx.ErrNoRows 走 platformdb.IsNotFound 命中路径，service 应包成 ErrRunNotFound。
// pgx.ErrNoRows is IsNotFound-matched; service must wrap it into
// ErrRunNotFound so the tool layer can translate to bilingual MCP error.
func TestGetRun_NotFound_WrapsErrRunNotFound(t *testing.T) {
	stub := &stubRunStore{getRunErr: pgx.ErrNoRows}
	svc := makeGetRunService(stub)

	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-missing"})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun() error = %v, want errors.Is(ErrRunNotFound)", err)
	}
}

// ---- runStore (nil run, nil err) 防御兜底 → ErrRunNotFound ----
// 实际 store 实现不会返 (nil, nil)，但 service 的防御分支必须可达。
// Real store impls never return (nil, nil), but the service's defensive
// branch must remain reachable for future regressions.
func TestGetRun_NilRunNilErr_DefensiveNotFound(t *testing.T) {
	stub := &stubRunStore{getRunReply: nil, getRunErr: nil}
	svc := makeGetRunService(stub)

	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun() error = %v, want errors.Is(ErrRunNotFound) for defensive (nil,nil) path", err)
	}
}

// ---- 非 NotFound 错误透传，不误转为 ErrRunNotFound ----
// Generic IO error must NOT be silently mapped to ErrRunNotFound.
func TestGetRun_OtherError_PassThrough(t *testing.T) {
	boom := errors.New("connection lost")
	stub := &stubRunStore{getRunErr: boom}
	svc := makeGetRunService(stub)

	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if err == nil {
		t.Fatalf("GetRun() error = nil, want pass-through error")
	}
	if errors.Is(err, ErrRunNotFound) {
		t.Errorf("err = %v should NOT match ErrRunNotFound for non-NotFound error", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want errors.Is(connection lost)", err)
	}
}

// ---- DTO 转换：Events / Metadata defensive copy ----
// service 层 dagRunDTO 应做 RawMessage 防御拷贝，避免 caller 修改 events
// 渗回 store 缓存。
// dagRunDTO must defensively copy RawMessage fields so callers cannot mutate
// store-backed slices.
func TestGetRun_DTO_DefensiveCopiesRawMessages(t *testing.T) {
	events := json.RawMessage(`[{"k":"v"}]`)
	metadata := json.RawMessage(`{"a":1}`)
	stub := &stubRunStore{
		getRunReply: &taskdag.Run{
			RunKey:   "dag-1#run-abc",
			DagKey:   "dag-1",
			Status:   "running",
			Events:   events,
			Metadata: metadata,
		},
	}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if string(resp.Run.Events) != string(events) || string(resp.Run.Metadata) != string(metadata) {
		t.Fatalf("resp.Run events/metadata content mismatch")
	}
	// 改 caller 拿到的 byte：原 store 行不应被影响。
	if len(resp.Run.Events) > 0 {
		resp.Run.Events[0] = 'X'
		if events[0] == 'X' {
			t.Errorf("dagRunDTO did not defensively copy Events; mutation leaked back to store row")
		}
	}
	if len(resp.Run.Metadata) > 0 {
		resp.Run.Metadata[0] = 'X'
		if metadata[0] == 'X' {
			t.Errorf("dagRunDTO did not defensively copy Metadata; mutation leaked back to store row")
		}
	}
}
