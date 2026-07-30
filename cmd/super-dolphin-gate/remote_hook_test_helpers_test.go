package main

import (
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func assertRemotePushRunOptions(t *testing.T, options remoteRunOptions, repository string, head string, base string) {
	t.Helper()
	if options.RepositoryRoot != repository || options.Commit != head || options.Base != base || options.RemoteName != "origin" || options.RemoteURL != "ssh://git@example.invalid/repository.git" || options.Scenario != "push" || options.Entrypoint != string(gatecontract.CIEntrypointGitPrePush) || options.UpdateKind != string(gatecontract.UpdateKindFastForward) {
		t.Fatalf("options = %#v", options)
	}
}
