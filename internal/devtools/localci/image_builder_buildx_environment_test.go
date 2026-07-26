package localci

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestSanitizedBuildxEnvironmentPreservesDockerConfigAndLocksControls(t *testing.T) {
	environment := []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://proxy.invalid",
		"https_proxy=http://proxy.invalid",
		"BUILDX_METADATA_PROVENANCE=disabled",
		"BUILDX_METADATA_WARNINGS=1",
		"BUILDX_BUILDER=attacker-builder",
		"DOCKER_CONTEXT=attacker-context",
		"DOCKER_HOST=tcp://attacker.invalid:2376",
		"DOCKER_CONFIG=/tmp/docker-config",
	}
	actual := sanitizedBuildxEnvironment(environment)
	wanted := []string{
		"PATH=/usr/bin",
		"DOCKER_CONFIG=/tmp/docker-config",
		"BUILDX_METADATA_PROVENANCE=max",
		"BUILDX_METADATA_WARNINGS=0",
		"BUILDX_NO_DEFAULT_ATTESTATIONS=1",
	}
	if !slices.Equal(actual, wanted) {
		t.Fatalf("buildx environment = %v, want %v", actual, wanted)
	}
}

func TestExecBuildxCommandExecutorReportsActualCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (execBuildxCommandExecutor{}).Run(
		context.Background(), bytes.NewReader(nil), "buildx", "ls", "--format", "{{.Name}}",
	)
	if err == nil || !strings.Contains(err.Error(), "docker buildx ls:") {
		t.Fatalf("buildx list error = %v", err)
	}
}

func validBuildxRequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	prepared, err := prepareCandidate(candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.buildRequest
}

func localBuildxSmokeRequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	var lock toolchainLock
	if err := decodeStrictJSON(readRepoFile(t, toolchainLockPath), &lock); err != nil {
		t.Fatal(err)
	}
	reference := ""
	for _, image := range lock.BaseImages {
		if image.Name == "GO_IMAGE" {
			reference = image.Reference
			break
		}
	}
	if reference == "" {
		t.Fatal("toolchain lock is missing GO_IMAGE")
	}
	dockerfile := strings.Replace(validCandidateDockerfile(), lockedGoImageReference(), reference, 1)
	entries := candidateEntries(dockerfile)
	replaceEntryText(t, entries, toolchainLockPath, lockedGoImageReference(), reference)
	replaceEntryText(t, entries, toolchainLockPath, "mirror.gcr.io/moby/buildkit@"+digest("c"), lock.BuildKitImage)
	changeEntry(t, entries, "go.sum", "")
	changeEntry(t, entries, "cmd/super-dolphin-gate/main.go", "package main\n\nfunc main() {}\n")
	refreshRuntimeDepsLock(t, entries)
	prepared, err := prepareCandidate(candidateRequest(entries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.buildRequest
}
