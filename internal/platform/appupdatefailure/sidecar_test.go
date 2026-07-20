package appupdatefailure

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"golang.org/x/sync/errgroup"
)

func TestSidecarRoundTripUsesExactSafeSchemaAndMode(t *testing.T) {
	stageDir := realStageDir(t)
	failure := mustFailure(t, "UPDATE_SIGNATURE_INVALID")
	if err := Write(stageDir, failure); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	assertExactSidecarFile(t, stageDir, failure)
	assertSidecarRoundTripAndClear(t, stageDir, failure)
}

func assertExactSidecarFile(t *testing.T, stageDir string, failure contract.RecoveryFailure) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stageDir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || fields["version"] != float64(Version) || fields["code"] != failure.Code || fields["transaction_id"] != "" {
		t.Fatalf("sidecar fields = %#v", fields)
	}
	info, err := os.Stat(filepath.Join(stageDir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar mode = %o, want 600", info.Mode().Perm())
	}
}

func assertSidecarRoundTripAndClear(t *testing.T, stageDir string, failure contract.RecoveryFailure) {
	t.Helper()
	got, ok, err := Read(stageDir)
	if err != nil || !ok || got != failure {
		t.Fatalf("Read() = (%#v, %v, %v), want (%#v, true, nil)", got, ok, err, failure)
	}
	if err := Clear(stageDir); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Read(stageDir); err != nil || ok {
		t.Fatalf("Read() after Clear = (_, %v, %v), want false, nil", ok, err)
	}
}

func TestSidecarRejectsTampering(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		mode os.FileMode
	}{
		{name: "extra field", raw: `{"version":1,"code":"UPDATE_INTEGRITY_INVALID","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":"","raw_output":"secret"}`, mode: 0o600},
		{name: "unknown version", raw: `{"version":2,"code":"UPDATE_INTEGRITY_INVALID","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":""}`, mode: 0o600},
		{name: "unknown code", raw: `{"version":1,"code":"UNKNOWN","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":""}`, mode: 0o600},
		{name: "transaction id", raw: `{"version":1,"code":"UPDATE_INTEGRITY_INVALID","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":"fake"}`, mode: 0o600},
		{name: "unsafe mode", raw: `{"version":1,"code":"UPDATE_INTEGRITY_INVALID","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":""}`, mode: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stageDir := realStageDir(t)
			if err := os.WriteFile(filepath.Join(stageDir, Filename), []byte(tt.raw), tt.mode); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Read(stageDir); err == nil {
				t.Fatal("Read() error = nil, want tamper rejection")
			}
		})
	}
}

func TestSidecarRejectsSymlink(t *testing.T) {
	stageDir := realStageDir(t)
	target := filepath.Join(realStageDir(t), "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stageDir, Filename)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(stageDir); err == nil {
		t.Fatal("Read() error = nil, want symlink rejection")
	}
}

func TestConcurrentSidecarReadsObserveOnlyCompleteRecords(t *testing.T) {
	stageDir := realStageDir(t)
	failures := []contract.RecoveryFailure{mustFailure(t, "UPDATE_SIGNATURE_INVALID"), mustFailure(t, "UPDATE_INTEGRITY_INVALID")}
	if err := Write(stageDir, failures[0]); err != nil {
		t.Fatal(err)
	}
	var group errgroup.Group
	for worker := range 8 {
		group.Go(func() error { return exerciseSidecar(stageDir, failures, worker) })
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func exerciseSidecar(stageDir string, failures []contract.RecoveryFailure, worker int) error {
	for index := range 50 {
		if worker%2 == 0 {
			if err := Write(stageDir, failures[index%len(failures)]); err != nil {
				return err
			}
			continue
		}
		failure, ok, err := Read(stageDir)
		if err != nil || !ok || (failure != failures[0] && failure != failures[1]) {
			return errors.New("read observed incomplete sidecar")
		}
	}
	return nil
}

func TestSidecarRejectsAncestorSymlink(t *testing.T) {
	realDir := realStageDir(t)
	linkParent := realStageDir(t)
	link := filepath.Join(linkParent, "linked-stage")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, mustFailure(t, "UPDATE_SIGNATURE_INVALID")); err == nil {
		t.Fatal("Write() error = nil, want ancestor symlink rejection")
	}
}

func mustFailure(t *testing.T, code string) contract.RecoveryFailure {
	t.Helper()
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		t.Fatalf("RecoveryFailureForCode(%q) = false", code)
	}
	return failure
}

func realStageDir(t *testing.T) string {
	t.Helper()
	stageDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return stageDir
}
