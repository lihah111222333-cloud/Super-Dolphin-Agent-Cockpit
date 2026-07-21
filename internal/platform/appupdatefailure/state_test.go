//go:build darwin

package appupdatefailure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"golang.org/x/sync/errgroup"
)

const (
	testGenerationOld = "00112233445566778899aabbccddeeff"
	testGenerationNew = "ffeeddccbbaa99887766554433221100"
)

func TestGenerationStateMachineTransitions(t *testing.T) {
	stageDir := privateStageDir(t)
	failure := mustFailure(t, "UPDATE_SIGNATURE_INVALID")
	if err := Begin(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	assertNoVisibleFailure(t, stageDir)
	if err := Fail(stageDir, testGenerationOld, failure); err != nil {
		t.Fatal(err)
	}
	assertVisibleFailure(t, stageDir, failure)
	if err := Clear(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	assertNoVisibleFailure(t, stageDir)
}

func TestFailRequiresMatchingExistingGeneration(t *testing.T) {
	stageDir := privateStageDir(t)
	failure := mustFailure(t, "UPDATE_INTEGRITY_INVALID")
	if err := Fail(stageDir, testGenerationOld, failure); err == nil {
		t.Fatal("Fail(absent) error = nil")
	}
	if err := Begin(stageDir, testGenerationNew); err != nil {
		t.Fatal(err)
	}
	if err := Fail(stageDir, testGenerationOld, failure); err == nil {
		t.Fatal("Fail(mismatched generation) error = nil")
	}
	assertNoVisibleFailure(t, stageDir)
}

func TestClearRequiresMatchingGeneration(t *testing.T) {
	stageDir := privateStageDir(t)
	if err := Clear(stageDir, testGenerationOld); err == nil {
		t.Fatal("Clear(absent) error = nil")
	}
	if err := Begin(stageDir, testGenerationNew); err != nil {
		t.Fatal(err)
	}
	if err := Clear(stageDir, testGenerationOld); err == nil {
		t.Fatal("Clear(mismatched generation) error = nil")
	}
	assertNoVisibleFailure(t, stageDir)
}

func TestInvalidateAllRemovesAnyGeneration(t *testing.T) {
	stageDir := privateStageDir(t)
	if err := Begin(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	if err := InvalidateAll(stageDir); err != nil {
		t.Fatal(err)
	}
	if err := Fail(stageDir, testGenerationOld, mustFailure(t, "UPDATE_SIGNATURE_INVALID")); err == nil {
		t.Fatal("late Fail() error = nil")
	}
	assertNoVisibleFailure(t, stageDir)
}

func TestLateOldWriterCannotOverwriteRetryGeneration(t *testing.T) {
	stageDir := privateStageDir(t)
	if err := Begin(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var group errgroup.Group
	group.Go(func() error {
		<-start
		return Fail(stageDir, testGenerationOld, mustFailure(t, "UPDATE_SIGNATURE_INVALID"))
	})
	if err := Begin(stageDir, testGenerationNew); err != nil {
		t.Fatal(err)
	}
	close(start)
	if err := group.Wait(); err == nil {
		t.Fatal("late old Fail() error = nil")
	}
	assertNoVisibleFailure(t, stageDir)
}

func TestClearWinsAgainstLateFail(t *testing.T) {
	stageDir := privateStageDir(t)
	if err := Begin(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var group errgroup.Group
	group.Go(func() error {
		<-start
		return Fail(stageDir, testGenerationOld, mustFailure(t, "UPDATE_INTEGRITY_INVALID"))
	})
	if err := Clear(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	close(start)
	if err := group.Wait(); err == nil {
		t.Fatal("Fail() after Clear error = nil")
	}
	assertNoVisibleFailure(t, stageDir)
}

func TestGenerationValidationFailsClosed(t *testing.T) {
	stageDir := privateStageDir(t)
	for _, generation := range []string{"", "short", "../../escape", "00112233445566778899AABBCCDDEEFF"} {
		if err := Begin(stageDir, generation); err == nil {
			t.Fatalf("Begin(%q) error = nil", generation)
		}
	}
}

func assertVisibleFailure(t *testing.T, stageDir string, want contract.RecoveryFailure) {
	t.Helper()
	got, ok, err := ReadFailure(stageDir)
	if err != nil || !ok || got != want || got.TransactionID != "" {
		t.Fatalf("ReadFailure() = (%#v, %v, %v), want (%#v, true, nil)", got, ok, err, want)
	}
}

func assertNoVisibleFailure(t *testing.T, stageDir string) {
	t.Helper()
	got, ok, err := ReadFailure(stageDir)
	if err != nil || ok || got != (contract.RecoveryFailure{}) {
		t.Fatalf("ReadFailure() = (%#v, %v, %v), want zero, false, nil", got, ok, err)
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

func privateStageDir(t *testing.T) string {
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
