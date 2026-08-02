package remoteci

import (
	"context"
	"strings"
	"testing"
)

func TestCoordinatorRejectsRetiredBaselineCandidateCLIRehydration(t *testing.T) {
	coordinator := &Coordinator{store: &coordinatorStore{}}
	_, _, _, _, err := coordinator.rehydrateRemoteCandidateCLIArtifact(context.Background(), RunInput{ReuseBaselineGateCLI: true}, "job-0123456789abcdef01234567", t.TempDir(), "jobs/")
	if err == nil || !strings.Contains(err.Error(), "OCI-only") {
		t.Fatalf("rehydrate error = %v", err)
	}
}
