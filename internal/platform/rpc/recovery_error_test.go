package rpc

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestRecoveryActionErrorReturnsExactSafeData(t *testing.T) {
	failure, ok := contract.RecoveryFailureForCode("MCP_SCHEMA_DIGEST_MISMATCH", "")
	if !ok {
		t.Fatal("RecoveryFailureForCode() ok = false")
	}
	cause := errors.New("secret schema digest and /private/workspace")
	err, ok := RecoveryActionError(recoveryCarrierError{failure: failure, cause: cause})
	if !ok {
		t.Fatal("RecoveryActionError() ok = false")
	}
	assertRecoveryActionRPCError(t, err, failure, cause)
}

func assertRecoveryActionRPCError(t *testing.T, err error, failure contract.RecoveryFailure, cause error) {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("RecoveryActionError() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(CodeInvalidState) || rpcErr.Message != "recovery action is required" {
		t.Fatalf("RPC error = %#v", rpcErr)
	}
	var data map[string]any
	if decodeErr := json.Unmarshal(rpcErr.Data, &data); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	want := map[string]any{"code": failure.Code, "retryable": failure.Retryable, "action": string(failure.Action), "transaction_id": failure.TransactionID}
	if !sameRecoveryData(data, want) {
		t.Fatalf("RPC data = %#v, want %#v", data, want)
	}
	if string(rpcErr.Data) == "" || errors.Is(err, cause) {
		t.Fatalf("RPC error retained unsafe cause: %v", err)
	}
}

func sameRecoveryData(got, want map[string]any) bool {
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

func TestRecoveryActionErrorRejectsUnknownError(t *testing.T) {
	if err, ok := RecoveryActionError(errors.New("unknown secret")); ok || err != nil {
		t.Fatalf("RecoveryActionError() = %v, %v; want nil, false", err, ok)
	}
}

type recoveryCarrierError struct {
	failure contract.RecoveryFailure
	cause   error
}

func (err recoveryCarrierError) Error() string { return err.cause.Error() }

func (err recoveryCarrierError) Unwrap() error { return err.cause }

func (err recoveryCarrierError) RecoveryFailure() contract.RecoveryFailure { return err.failure }
