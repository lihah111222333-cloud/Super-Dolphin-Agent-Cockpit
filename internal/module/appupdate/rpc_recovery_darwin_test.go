//go:build darwin

package appupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
)

func TestPreJournalSidecarReachesCheckRPCAsSafeRecoveryData(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	failure, _ := contract.RecoveryFailureForCode("UPDATE_SIGNATURE_INVALID", "")
	if err := appupdatefailure.Begin(stageDir, recoveryTestGeneration); err != nil {
		t.Fatal(err)
	}
	if err := appupdatefailure.Fail(stageDir, recoveryTestGeneration, failure); err != nil {
		t.Fatal(err)
	}
	svc := newService(Config{Enabled: true, StageDir: stageDir, Platform: "darwin-arm64"}, nil, nil)

	assertUpdateRecoveryRPCError(t, dispatchUpdateRPCService(t, "app/update/check", svc), "UPDATE_SIGNATURE_INVALID")
}

func TestMalformedPreJournalSidecarFailsCheckRPCGenerically(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	path := filepath.Join(stageDir, appupdatefailure.Filename)
	if err := os.WriteFile(path, []byte(`{"version":2,"generation":"00112233445566778899aabbccddeeff","state":"failure","code":"UPDATE_SIGNATURE_INVALID","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":"","raw_output":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := dispatchUpdateRPCService(t, "app/update/check", newService(Config{Enabled: true, StageDir: stageDir, Platform: "darwin-arm64"}, nil, nil))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Message != "app update request failed" || len(rpcErr.Data) != 0 {
		t.Fatalf("Dispatch(check) error = %#v, want generic failure", err)
	}
}

func TestExplicitUpdateRetriesClearStalePreJournalSidecar(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	failure, _ := contract.RecoveryFailureForCode("UPDATE_INTEGRITY_INVALID", "")
	if err := appupdatefailure.Begin(stageDir, recoveryTestGeneration); err != nil {
		t.Fatal(err)
	}
	if err := appupdatefailure.Fail(stageDir, recoveryTestGeneration, failure); err != nil {
		t.Fatal(err)
	}
	svc := newService(Config{Enabled: true, StageDir: stageDir, Platform: "darwin-arm64"}, nil, func() {})
	_, _ = svc.Download(context.Background())
	if _, exists, err := appupdatefailure.ReadFailure(stageDir); err != nil || exists {
		t.Fatalf("ReadFailure() after download retry = (_, %v, %v), want absent", exists, err)
	}
}

func TestPendingPreJournalSidecarDoesNotMaskCheck(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	stageDir := appUpdateRealTempDir(t)
	if err := appupdatefailure.Begin(stageDir, recoveryTestGeneration); err != nil {
		t.Fatal(err)
	}
	payload := testManifestPayload()
	svc := newService(testServiceConfig(publicKey, stageDir, payload.Version), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json": signTestManifest(t, privateKey, payload),
	}), nil)
	result, err := svc.Check(context.Background())
	if err != nil || result.Available {
		t.Fatalf("Check() = (%#v, %v), want no update and no pending failure", result, err)
	}
}
