package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
)

func TestRecoverySurfaceRedactsUnknownFailureReason(t *testing.T) {
	secret := "postgres://admin:password@localhost/db PRIVATE KEY sk-live-secret /Users/alice/private.db stdout stderr"
	state := newRecoverySurfaceState(app.RecoveryProjection{Reason: secret}, "state")
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"postgres://", "PRIVATE KEY", "sk-live-secret", "/Users/alice", "stdout", "stderr"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("unknown Recovery failure leaked %q in %s", leaked, raw)
		}
	}
}

func TestRecoverySurfaceExposesOnlySafeFailureMetadata(t *testing.T) {
	secret := "postgres://admin:password@localhost/db PRIVATE KEY sk-live-secret /Users/alice/private.db"
	projection := app.RecoveryProjection{
		TransactionID: "transaction-1",
		Reason:        "update failed: " + secret,
	}
	state := newRecoverySurfaceStateWithFailure(projection, "state", app.RecoveryFailure{
		Code: "UPDATE_SIGNATURE_INVALID", Action: app.RecoveryActionPreserveStateExportDiagnostics,
	})
	if state.Failure.TransactionID != "transaction-1" || state.Failure.Code != "UPDATE_SIGNATURE_INVALID" {
		t.Fatalf("failure = %#v", state.Failure)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"postgres://", "PRIVATE KEY", "sk-live-secret", "/Users/alice"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("Recovery state leaked %q in %s", leaked, raw)
		}
	}
	var payload struct {
		Failure map[string]any `json:"failure"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Failure) != 4 {
		t.Fatalf("failure fields = %v, want exactly four", payload.Failure)
	}
}

func TestRecoverySurfaceDoesNotSerializeRawProjectionReason(t *testing.T) {
	const rawCause = "secret=sk-recovery path=/private/recovery dsn=postgres://private"
	state := newRecoverySurfaceState(app.RecoveryProjection{Reason: rawCause}, "state")
	wire, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal Recovery surface state: %v", err)
	}
	for _, sensitive := range []string{"secret=sk-recovery", "/private/recovery", "dsn=postgres://private"} {
		if strings.Contains(string(wire), sensitive) {
			t.Fatalf("Recovery Wails state leaked %q: %s", sensitive, wire)
		}
	}
	if !strings.HasPrefix(state.Projection.Reason, "RECOVERY_STARTUP_FAILED|") {
		t.Fatalf("Recovery Wails reason = %q, want stable public code", state.Projection.Reason)
	}
}

func TestRecoveryWailsActionFailuresNeverPublishRawCause(t *testing.T) {
	const rawCause = "secret=sk-recovery path=/private/recovery dsn=postgres://private"
	for _, code := range []app.RecoveryPublicErrorCode{
		app.RecoveryPublicCodeCheckFailed,
		app.RecoveryPublicCodeRetryFailed,
		app.RecoveryPublicCodeRestoreFailed,
	} {
		t.Run(string(code), func(t *testing.T) {
			err := recoveryPublicFailure(code, errors.New(rawCause))
			for _, sensitive := range []string{"secret=sk-recovery", "/private/recovery", "dsn=postgres://private"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("Recovery Wails error leaked %q: %q", sensitive, err)
				}
			}
			if !strings.HasPrefix(err.Error(), string(code)+"|") {
				t.Fatalf("Recovery Wails error = %q, want public wire code", err)
			}
		})
	}
}

func TestRecoveryWailsFailureLogNeverPublishesRawCause(t *testing.T) {
	const rawCause = "secret=sk-recovery path=/private/recovery dsn=postgres://private"
	for _, test := range []struct {
		name string
		code app.RecoveryPublicErrorCode
		want app.RecoveryPublicErrorCode
	}{
		{name: "allowlisted_action", code: app.RecoveryPublicCodeCheckFailed, want: app.RecoveryPublicCodeCheckFailed},
		{name: "unallowlisted_code", code: app.RecoveryPublicErrorCode(rawCause), want: app.RecoveryPublicCodeUnknownFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			err := recoveryPublicFailureWithLogger(
				slog.New(slog.NewTextHandler(&logs, nil)),
				test.code,
				errors.New(rawCause),
			)
			if !strings.HasPrefix(err.Error(), string(test.want)+"|") {
				t.Fatalf("Recovery Wails error = %q, want public wire code %q", err, test.want)
			}
			output := logs.String()
			for _, sensitive := range []string{"secret=sk-recovery", "path=/private/recovery", "dsn=postgres://private"} {
				if strings.Contains(output, sensitive) {
					t.Fatalf("Recovery Wails log leaked %q: %s", sensitive, output)
				}
			}
			for _, publicField := range []string{
				"recovery_error_code=" + string(test.want),
				"public_message=",
				"diagnostic_id=",
			} {
				if !strings.Contains(output, publicField) {
					t.Fatalf("Recovery Wails log = %q, missing public field %q", output, publicField)
				}
			}
		})
	}
}
