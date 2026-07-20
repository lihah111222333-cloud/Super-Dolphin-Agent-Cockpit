package localci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const unrelatedBusinessSource = "docs/README.md"

func TestTrackedGateClosureTriggersCandidateOnlyForDeclaredInputs(t *testing.T) {
	entries := trackedGateClosureEntries(t)
	baseRequest := candidateRequest(slices.Clone(entries), digest("f"), digest("e"))
	base, err := prepareCandidate(baseRequest)
	if err != nil {
		t.Fatal(err)
	}

	sourceChanged := slices.Clone(entries)
	appendEntryByte(t, sourceChanged, "cmd/super-dolphin-gate/main.go")
	changed, err := prepareCandidate(candidateRequest(sourceChanged, base.result.InputDigest, digest("8")))
	if err != nil {
		t.Fatal(err)
	}
	if changed.result.InputDigest == base.result.InputDigest {
		t.Fatal("declared CLI source change did not change image input digest")
	}
	relevantRunner := &recordingBuildKitRunner{digest: digest("9")}
	mustEnsureCandidate(t, relevantRunner, candidateRequest(sourceChanged, base.result.InputDigest, digest("8")))
	if len(relevantRunner.requests) != 1 {
		t.Fatalf("declared source change triggered %d candidate builds, want 1", len(relevantRunner.requests))
	}

	unrelatedChanged := slices.Clone(entries)
	appendEntryByte(t, unrelatedChanged, unrelatedBusinessSource)
	unrelated, err := prepareCandidate(candidateRequest(unrelatedChanged, base.result.InputDigest, digest("8")))
	if err != nil {
		t.Fatal(err)
	}
	if unrelated.result.InputDigest != base.result.InputDigest || unrelated.result.ContextDigest != base.result.ContextDigest {
		t.Fatal("unrelated business source polluted gate image input closure")
	}
	unrelatedRunner := &recordingBuildKitRunner{digest: digest("9")}
	result := mustEnsureCandidate(t, unrelatedRunner, candidateRequest(unrelatedChanged, base.result.InputDigest, digest("8")))
	if len(unrelatedRunner.requests) != 0 || result.Built {
		t.Fatalf("unrelated business source triggered candidate build: requests=%d result=%+v", len(unrelatedRunner.requests), result)
	}
}

func trackedGateClosureEntries(t *testing.T) []sourceexport.TreeEntry {
	t.Helper()
	root := repositoryRootForGateContract(t)
	manifestData, err := os.ReadFile(filepath.Join(root, buildInputManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	var manifest buildInputManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	paths := append(slices.Clone(manifest.Inputs), unrelatedBusinessSource)
	entries := make([]sourceexport.TreeEntry, 0, len(paths))
	for _, name := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read tracked gate contract input %s: %v", name, err)
		}
		entries = append(entries, contextEntry(name, "100644", string(data)))
	}
	return entries
}

func repositoryRootForGateContract(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func appendEntryByte(t *testing.T, entries []sourceexport.TreeEntry, name string) {
	t.Helper()
	for index := range entries {
		if entries[index].Path == name {
			entries[index].Data = append(slices.Clone(entries[index].Data), '\n')
			hash, err := gitBlobHash(entries[index].Hash, entries[index].Data)
			if err != nil {
				t.Fatal(err)
			}
			entries[index].Hash = hash
			return
		}
	}
	t.Fatalf("entry %s not found", name)
}
