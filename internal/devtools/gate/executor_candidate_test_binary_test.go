package gate

import (
	"strings"
	"testing"
)

func TestCandidateTestBinaryExecutorProgramUsesExactTest2JSONArguments(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	program, err := candidateTestBinaryExecutorProgram(workload.ID, candidateTestBinaryBundleIndex{Binaries: []CandidateTestBinaryBundle{{Package: "./internal/archtest", Mode: "test", BinaryPath: "/opt/super-dolphin-gate/test-binaries/00.test-bin"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go", "tool", "test2json", "-p", "./internal/archtest", "-t", "/opt/super-dolphin-gate/test-binaries/00.test-bin", "-test.v=test2json", "-test.timeout=0", "-test.run=^TestBoundary$", "-test.count=1"}
	if len(program.Steps) != 1 || !sameStringSlice(program.Steps[0].Argv, want) {
		t.Fatalf("argv = %#v, want %#v", program.Steps, want)
	}
}

func TestCandidateTestBinaryExecutorProgramRejectsPackageWorkload(t *testing.T) {
	packageID, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoPackage, "./internal/archtest")
	if err != nil {
		t.Fatal(err)
	}
	index := candidateTestBinaryBundleIndex{Binaries: []CandidateTestBinaryBundle{{Package: "./internal/archtest", Mode: "test"}}}
	if _, err := candidateTestBinaryExecutorProgram(GateID(packageID), index); err == nil || !strings.Contains(err.Error(), "candidate test binary") {
		t.Fatalf("candidateTestBinaryExecutorProgram(%q) error = %v", packageID, err)
	}
}

func TestCandidateTestBinaryExecutorProgramRejectsUnknownParent(t *testing.T) {
	_, err := targetWorkloadID(GateID("unknown-parent"), workloadTargetGoTest, "./internal/archtest#TestBoundary")
	if err == nil {
		t.Fatal("targetWorkloadID() accepted unknown parent")
	}
	if _, err := candidateTestBinaryExecutorProgram(GateID("unknown-parent::go-test::Li9pbnRlcm5hbC9hcmNodGVzdCNTZXN0Qm91bmRhcnk"), candidateTestBinaryBundleIndex{}); err == nil || !strings.Contains(err.Error(), "candidate test binary") {
		t.Fatalf("unknown parent error = %v", err)
	}
}

func TestCandidateTestBinaryExecutorProgramRejectsMissingExactBundle(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateTestBinaryExecutorProgram(workload.ID, candidateTestBinaryBundleIndex{Binaries: []CandidateTestBinaryBundle{{Package: "./internal/other", Mode: "test"}}}); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("missing bundle error = %v", err)
	}
}

func sameStringSlice(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
