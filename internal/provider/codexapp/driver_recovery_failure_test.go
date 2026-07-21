package codexapp

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
)

func TestStartSessionRecoveryFailureStopsBeforeRemoteThreadStart(t *testing.T) {
	codes := []string{
		"MCP_SCHEMA_CAPACITY_EXHAUSTED",
		"MCP_SCHEMA_REAP_FAILED",
		"MCP_SCHEMA_DIGEST_MISMATCH",
		"MCP_SCHEMA_PROTOCOL_VIOLATION",
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			assertRecoveryFailureStopsRemoteStart(t, code)
		})
	}
}

func assertRecoveryFailureStopsRemoteStart(t *testing.T, code string) {
	t.Helper()
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		t.Fatalf("RecoveryFailureForCode(%q) ok = false", code)
	}
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	d := requireToolBridgeDriver(t, newDriver(nil, nil, testApprovalManager(), nil, &ServerManager{}, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, nil))
	d.prepareTools = func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		return nil, driverRecoveryCarrier{failure: failure, cause: errors.New("secret MCP admission details")}
	}

	session, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-recovery-failure",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if session != nil {
		t.Fatalf("StartSession() session = %#v, want nil", session)
	}
	got, found := contract.RecoveryFailureFromError(err)
	if !found || got != failure {
		t.Fatalf("StartSession() recovery failure = %#v, %v; want %#v, true", got, found, failure)
	}
	if calls := recorder.calls("thread/start"); calls != 0 {
		t.Fatalf("remote thread/start calls = %d, want 0", calls)
	}
}

type driverRecoveryCarrier struct {
	failure contract.RecoveryFailure
	cause   error
}

func (err driverRecoveryCarrier) Error() string { return err.cause.Error() }

func (err driverRecoveryCarrier) Unwrap() error { return err.cause }

func (err driverRecoveryCarrier) RecoveryFailure() contract.RecoveryFailure { return err.failure }
