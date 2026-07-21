package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestThreadStartReturnsRecoveryFailureAsSafeRPCData(t *testing.T) {
	failure, ok := contract.RecoveryFailureForCode("MCP_SCHEMA_REAP_FAILED", "")
	if !ok {
		t.Fatal("RecoveryFailureForCode() ok = false")
	}
	secret := "secret MCP stderr at /private/workspace"
	server := newThreadTestServer(&recoveryStartService{
		stubThreadService: &stubThreadService{},
		err: threadRecoveryCarrier{
			failure: failure,
			cause:   errors.New(secret),
		},
	})
	_, err := server.Dispatch(context.Background(), contract.ThreadRPCStart, json.RawMessage(`{"cwd":"/tmp/demo","modelProvider":"codex"}`))
	assertSafeThreadRecoveryError(t, err, failure, secret)
}

func assertSafeThreadRecoveryError(t *testing.T, err error, failure contract.RecoveryFailure, secret string) {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch(thread/start) error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(contract.CodeInvalidState) || rpcErr.Message != "recovery action is required" {
		t.Fatalf("RPC error = %#v", rpcErr)
	}
	var data map[string]any
	if decodeErr := json.Unmarshal(rpcErr.Data, &data); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	want := map[string]any{"code": failure.Code, "retryable": failure.Retryable, "action": string(failure.Action), "transaction_id": failure.TransactionID}
	if !mapsEqual(data, want) {
		t.Fatalf("RPC data = %#v, want %#v", data, want)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(string(rpcErr.Data), secret) {
		t.Fatalf("RPC error leaked secret: %v", err)
	}
}

func mapsEqual(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

type threadRecoveryCarrier struct {
	failure contract.RecoveryFailure
	cause   error
}

func (err threadRecoveryCarrier) Error() string { return err.cause.Error() }

func (err threadRecoveryCarrier) Unwrap() error { return err.cause }

func (err threadRecoveryCarrier) RecoveryFailure() contract.RecoveryFailure { return err.failure }

type recoveryStartService struct {
	*stubThreadService
	err error
}

func (service *recoveryStartService) Start(_ context.Context, _ StartRequest) (StartResult, error) {
	return StartResult{}, service.err
}
