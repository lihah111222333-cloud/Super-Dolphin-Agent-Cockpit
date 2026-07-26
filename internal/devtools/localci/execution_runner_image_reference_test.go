package localci

import (
	"context"
	"testing"
)

func TestRunFreshContainerAcceptsDockerHubFamiliarDigestReference(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.Image.Registry = "docker.io/library/gate-test"
	stub.request = request
	stub.imageMutation = "familiar repository"
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	assertFreshContainerEvidence(t, result)
}

func TestRunFreshContainerAcceptsLegacyLocalConfigDigest(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.Image.Registry = candidateImageRepository
	stub.request = request
	stub.imageMutation = "legacy local store"
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	assertFreshContainerEvidence(t, result)
}
