package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// F4.0 顶层校验测试：ApplyOps 在解码 ops + 形状校验阶段必须能区分四类失败
// （unmarshal / op_kind 非法 / 缺 op_kind / base_version 负数），并对合法
// ops 透传到下层 stub（返回 ErrLifecycleNotImplemented）。
//
// F4.0 top-level validation tests: ApplyOps must distinguish four failure
// shapes (unmarshal / invalid op / missing op / negative base_version) before
// any business work, and valid ops should fall through to the lifecycle stub.

// TestApplyOps_UpdateDAGDispatches 验证 F4.4 后 update_dag 已接入业务层，
// 不再走 ErrLifecycleNotImplemented。
func TestApplyOps_UpdateDAGDispatches(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	ops := json.RawMessage(`[
		{"op":"update_dag","patch":{"title":"t"}}
	]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 1, Ops: ops}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Fatalf("NewVersion = %d, want 2", resp.NewVersion)
	}
}

// TestApplyOps_InvalidOpKind 验证 op_kind=unknown 返回 InvalidArgument 类错误
// （命中 ErrApplyOpsInvalid sentinel），且错误信息含 op_kind 字面量。
func TestApplyOps_InvalidOpKind(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"op":"unknown_kind","whatever":1}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("ApplyOps unknown op should NOT return ErrLifecycleNotImplemented; got %v", err)
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if !strings.Contains(err.Error(), "unknown_kind") {
		t.Fatalf("ApplyOps err should mention the offending op kind, got: %v", err)
	}
}

// TestApplyOps_MissingOpKind 验证缺 op 字段 → InvalidArgument。
func TestApplyOps_MissingOpKind(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"node_key":"n1"}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("ApplyOps missing op should NOT return ErrLifecycleNotImplemented; got %v", err)
	}
}

// TestApplyOps_UnmarshalFails 验证非合法 JSON → InvalidArgument。
func TestApplyOps_UnmarshalFails(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`not a json array`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
}

// TestApplyOps_NegativeBaseVersion 验证 base_version<0 → InvalidArgument。
func TestApplyOps_NegativeBaseVersion(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"op":"remove_node","node_key":"n1"}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: -1, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if !strings.Contains(err.Error(), "base_version") {
		t.Fatalf("ApplyOps err should mention base_version, got: %v", err)
	}
}

// TestApplyOps_StoreNotConfigured 验证 service.dagStore 未设时返 sentinel。
// 未接裸构造路径。
func TestApplyOps_StoreNotConfigured(t *testing.T) {
	t.Parallel()
	s := &service{}
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops:         json.RawMessage(`[]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if !errors.Is(err, ErrApplyOpsStoreNotConfigured) {
		t.Fatalf("ApplyOps without dag store err = %v, want ErrApplyOpsStoreNotConfigured", err)
	}
}
