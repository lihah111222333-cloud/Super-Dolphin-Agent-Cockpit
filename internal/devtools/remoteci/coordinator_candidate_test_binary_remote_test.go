package remoteci

import "testing"

func TestCandidateTestBinaryBuilderRequestUsesDedicatedOutputPrefix(t *testing.T) {
	request := candidateTestBinaryBuilderRequest(
		RunInput{},
		"job-builder-prefix",
		&remoteAssets{},
		nil,
		"remote-ci/jobs/",
	)
	if got, want := request.OutputPrefix, "remote-ci/jobs/job-builder-prefix/test-binaries/"; got != want {
		t.Fatalf("OutputPrefix = %q, want %q", got, want)
	}
}
