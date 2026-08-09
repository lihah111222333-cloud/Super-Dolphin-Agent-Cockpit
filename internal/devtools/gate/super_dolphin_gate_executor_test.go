package gate

import (
	"strings"
	"testing"
)

func TestExecutorRejectsCanonicalSubprocessHelper(t *testing.T) {
	workloadID, err := targetWorkloadID(
		GateIDBackendTestWithGuard,
		workloadTargetGoTest,
		encodeTestTargetForExecutor(t, GoTestTarget{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteHookConcurrentProcessHelper"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = executorProgramForWorkload(GateID(workloadID))
	if err == nil || !strings.Contains(err.Error(), "subprocess helper") {
		t.Fatalf("executorProgramForWorkload() error = %v, want subprocess helper rejection", err)
	}
}

func encodeTestTargetForExecutor(t *testing.T, target GoTestTarget) string {
	t.Helper()
	encoded, err := encodeGoTestTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
