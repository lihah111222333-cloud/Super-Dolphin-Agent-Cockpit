package remoteci

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestWorkflowActionlintFingerprintTracksOnlyWorkflowInputs(t *testing.T) {
	gateID := gate.GateIDWorkflowActionlint
	baseline := workflowActionlintFingerprintSnapshot()
	want := frontendCanonicalDigest(t, baseline, gateID)

	workflowChanged := workflowActionlintFingerprintSnapshot()
	replaceFrontendFingerprintFile(workflowChanged, ".github/workflows/ci.yml", []byte("name: changed\n"))
	if got := frontendCanonicalDigest(t, workflowChanged, gateID); got == want {
		t.Fatal("workflow actionlint fingerprint ignored workflow source changes")
	}

	configChanged := workflowActionlintFingerprintSnapshot()
	replaceFrontendFingerprintFile(configChanged, ".github/actionlint.yaml", []byte("self-hosted-runner: changed\n"))
	if got := frontendCanonicalDigest(t, configChanged, gateID); got == want {
		t.Fatal("workflow actionlint fingerprint ignored actionlint config changes")
	}

	unrelatedChanged := workflowActionlintFingerprintSnapshot()
	replaceFrontendFingerprintFile(unrelatedChanged, "internal/fixture/unrelated.go", []byte("package fixture\n\nconst changed = true\n"))
	if got := frontendCanonicalDigest(t, unrelatedChanged, gateID); got != want {
		t.Fatal("workflow actionlint fingerprint changed after unrelated Go source edit")
	}
}

func workflowActionlintFingerprintSnapshot() *remoteGitTreeSnapshot {
	snapshot := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(snapshot, ".github/workflows/ci.yml", []byte("name: ci\n"))
	replaceFrontendFingerprintFile(snapshot, ".github/actionlint.yaml", []byte("self-hosted-runner:\n"))
	snapshot.entries = sortedRemoteGitTreeEntries(snapshot.byPath)
	return snapshot
}
