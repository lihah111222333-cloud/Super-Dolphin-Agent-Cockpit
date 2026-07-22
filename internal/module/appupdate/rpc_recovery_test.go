package appupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const recoveryTestGeneration = "00112233445566778899aabbccddeeff"

func TestUpdateRPCMethodsReturnExactRecoveryData(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { pkglogger.SetForTest(previousLogger) })
	methods := []string{"app/update/check", "app/update/download", "app/update/install", "app/update/installLatest"}
	failures := []struct {
		name string
		err  error
		code string
	}{
		{name: "signature", err: fmtSecretError(contract.ErrUpdateSignatureInvalid), code: "UPDATE_SIGNATURE_INVALID"},
		{name: "integrity", err: fmtSecretError(contract.ErrUpdateIntegrityInvalid), code: "UPDATE_INTEGRITY_INVALID"},
	}
	for _, method := range methods {
		for _, failure := range failures {
			t.Run(method+"/"+failure.name, func(t *testing.T) {
				err := dispatchUpdateRPCError(t, method, failure.err)
				assertUpdateRecoveryRPCError(t, err, failure.code)
			})
		}
	}
	logText := logs.String()
	if strings.Contains(logText, "/Users/alice/update.dmg") || strings.Contains(logText, "helper output") {
		t.Fatalf("known recovery log leaked verifier details: %s", logText)
	}
	for _, want := range []string{`"operation":"check"`, `"operation":"download"`, `"operation":"install"`, `"operation":"installLatest"`, `"code":"UPDATE_SIGNATURE_INVALID"`, `"code":"UPDATE_INTEGRITY_INVALID"`} {
		if !strings.Contains(logText, want) {
			t.Fatalf("known recovery log missing %s: %s", want, logText)
		}
	}
}

func TestManifestSignatureProducerReachesRPCAsSafeRecoveryData(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	var signed SignedManifest
	if err := json.Unmarshal(signTestManifest(t, privateKey, testManifestPayload()), &signed); err != nil {
		t.Fatal(err)
	}
	signed.Payload.Version = "v9.9.9"
	tampered, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(testServiceConfig(publicKey, appUpdateRealTempDir(t), "1.2.2"), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json": tampered,
	}), nil)

	assertUpdateRecoveryRPCError(t, dispatchUpdateRPCService(t, "app/update/check", svc), "UPDATE_SIGNATURE_INVALID")
}

func TestArtifactIntegrityProducerReachesRPCAsSafeRecoveryData(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	payload.Artifacts[0].URL = "https://updates.example.test/update.dmg"
	payload.Artifacts[0].Size = 3
	payload.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	manifest := signTestManifest(t, privateKey, payload)
	svc := newService(testServiceConfig(publicKey, appUpdateRealTempDir(t), "1.2.2"), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json": manifest,
		"https://updates.example.test/update.dmg":    []byte("dmg"),
	}), nil)

	assertUpdateRecoveryRPCError(t, dispatchUpdateRPCService(t, "app/update/download", svc), "UPDATE_INTEGRITY_INVALID")
}

func TestWindowsAuthenticodeProducersReachRPCAsSafeRecoveryData(t *testing.T) {
	valid := authenticodeSignature{Status: "Valid", Subject: "CN=Expected Publisher", Thumbprint: strings.Repeat("a", 40)}
	tests := []struct {
		name   string
		result authenticodeSignature
	}{
		{name: "invalid status", result: authenticodeSignature{Status: "NotSigned", Subject: valid.Subject, Thumbprint: valid.Thumbprint}},
		{name: "publisher mismatch", result: authenticodeSignature{Status: valid.Status, Subject: "CN=Unexpected", Thumbprint: valid.Thumbprint}},
		{name: "thumbprint mismatch", result: authenticodeSignature{Status: valid.Status, Subject: valid.Subject, Thumbprint: strings.Repeat("b", 40)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stageDir := appUpdateRealTempDir(t)
			installer := writeArgsHelperScriptAt(t, stageDir+"/installer.args", filepath.Join(stageDir, exeFilename))
			svc := newService(Config{
				Enabled:           true,
				StageDir:          stageDir,
				Platform:          "windows-amd64",
				WindowsPublisher:  "Expected Publisher",
				WindowsThumbprint: valid.Thumbprint,
			}, nil, func() {})
			svc.windowsSignatureVerifier = func(_, publisher, thumbprint string) error {
				return validateAuthenticodeSignature(tt.result, publisher, thumbprint)
			}
			writeSelectedInstallFixtureForPlatform(t, svc, "windows-amd64", installer)

			assertUpdateRecoveryRPCError(t, dispatchUpdateRPCService(t, "app/update/install", svc), "UPDATE_SIGNATURE_INVALID")
		})
	}
}

func TestUpdateRPCMethodsRedactUnknownFailures(t *testing.T) {
	methods := []string{"app/update/check", "app/update/download", "app/update/install", "app/update/installLatest"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			err := dispatchUpdateRPCError(t, method, errors.New("secret path /Users/alice/update.dmg and helper output"))
			var rpcErr *jrpc2.Error
			if !errors.As(err, &rpcErr) {
				t.Fatalf("Dispatch(%s) error = %T, want *jrpc2.Error", method, err)
			}
			if rpcErr.Code != jrpc2.Code(contract.CodeInvalidState) || rpcErr.Message != "app update request failed" || len(rpcErr.Data) != 0 {
				t.Fatalf("Dispatch(%s) error = %#v", method, rpcErr)
			}
		})
	}
}

func dispatchUpdateRPCError(t *testing.T, method string, serviceErr error) error {
	t.Helper()
	return dispatchUpdateRPCService(t, method, &stubUpdateService{err: serviceErr})
}

func dispatchUpdateRPCService(t *testing.T, method string, svc Service) error {
	t.Helper()
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	_, err := server.Dispatch(context.Background(), method, json.RawMessage(`{}`))
	return err
}

func assertUpdateRecoveryRPCError(t *testing.T, err error, code string) {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("RPC error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(contract.CodeInvalidState) || rpcErr.Message != "recovery action is required" {
		t.Fatalf("RPC error = %#v", rpcErr)
	}
	var data map[string]any
	if decodeErr := json.Unmarshal(rpcErr.Data, &data); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	want := map[string]any{"code": code, "retryable": false, "action": string(contract.RecoveryActionPreserveStateExportDiagnostics), "transaction_id": ""}
	if !appUpdateMapsEqual(data, want) {
		t.Fatalf("RPC data = %#v, want %#v", data, want)
	}
}

func appUpdateMapsEqual(got, want map[string]any) bool {
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

func fmtSecretError(sentinel error) error {
	return errors.Join(sentinel, errors.New("secret path /Users/alice/update.dmg and helper output"))
}

func appUpdateRealTempDir(t *testing.T) string {
	t.Helper()
	stageDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return stageDir
}
