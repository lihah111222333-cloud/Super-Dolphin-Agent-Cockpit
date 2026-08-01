package remoteci

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestGoTestFingerprintDoesNotTreatProcessLocalIOAsRepositoryObservation(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSubprocess(t *testing.T) {
	_ = exec.Command("git", "status")
	_, _ = os.ReadFile(filepath.Join(t.TempDir(), "result.txt"))
}
`)
	testWorkload := fingerprintGoTestWorkload(t, "TestSubprocess", "./internal/a")
	packageWorkload, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/a",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	workloads := []gate.Workload{testWorkload, packageWorkload}
	initial := fingerprintDigests(t, repository, workloads)

	commitFingerprintChange(t, repository, "docs/subprocess-note.md", "unrelated\n")
	got := fingerprintDigests(t, repository, workloads)
	assertFingerprintEqual(t, initial, got, "process-local IO without an explicit repository path")
}
