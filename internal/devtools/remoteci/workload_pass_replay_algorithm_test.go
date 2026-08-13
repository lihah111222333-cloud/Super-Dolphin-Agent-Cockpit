package remoteci

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteReplayInputAlgorithmCompatibilityAndTreeRank(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "target-unrelated")
	same := testRemoteReplayAlgorithmSnapshot("same", "same", "source-unrelated")
	changed := testRemoteReplayAlgorithmSnapshot("changed", "changed", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := cache.inputAlgorithmsCompatible(same, target)
	if err != nil || !compatible {
		t.Fatalf("same input algorithm compatible=%t err=%v", compatible, err)
	}
	compatible, err = cache.inputAlgorithmsCompatible(changed, target)
	if err != nil || compatible {
		t.Fatalf("changed input algorithm compatible=%t err=%v", compatible, err)
	}
	if remoteReplayTreeDistance(same, target) >= remoteReplayTreeDistance(changed, target) {
		t.Fatal("tree distance did not prefer the closer immutable source")
	}
	candidates := []gate.WorkloadPassEvidence{
		{OriginSourceTreeSHA: "changed"}, {OriginSourceTreeSHA: "same"},
	}
	ranked := rankedRemoteWorkloadPassSourceCandidates(candidates, map[string]int{"changed": 2, "same": 1})
	if ranked[0].OriginSourceTreeSHA != "same" {
		t.Fatalf("ranked source tree = %q, want same", ranked[0].OriginSourceTreeSHA)
	}
}

func TestRemoteReplayInputAlgorithmRejectsEveryProducerChange(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "same-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, filePath := range remoteWorkloadInputAlgorithmRequiredPaths() {
		t.Run(filePath, func(t *testing.T) {
			source := testRemoteReplayAlgorithmSnapshot("source:"+filePath, "same", "same-unrelated")
			entry := source.byPath[filePath]
			entry.objectID = "changed"
			source.byPath[filePath] = entry
			for index := range source.entries {
				if source.entries[index].path == filePath {
					source.entries[index] = entry
					break
				}
			}
			compatible, err := cache.inputAlgorithmsCompatible(source, target)
			if err != nil || compatible {
				t.Fatalf("producer %q compatible=%t err=%v", filePath, compatible, err)
			}
		})
	}
}

func TestVerifyRemoteWorkloadPassSourceInputReusesCompatibleAuthoritativeDigest(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "target-unrelated")
	source := testRemoteReplayAlgorithmSnapshot("source", "same", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	identity := gate.WorkloadPassIdentity{InputDigest: "sha256:input"}
	candidate := gate.WorkloadPassEvidence{
		Identity: identity, OriginSourceTreeSHA: "source",
	}
	matched, err := verifyRemoteWorkloadPassSourceInput(
		context.Background(), RunInput{RepositoryRoot: "repo"}, identity, candidate,
		gate.Workload{}, source, target, cache,
	)
	if err != nil || !matched {
		t.Fatalf("compatible authoritative input matched=%t err=%v", matched, err)
	}
	if cache.inputComputations != 0 {
		t.Fatalf("compatible authoritative input recomputed %d digests", cache.inputComputations)
	}
}

func TestPrewarmRemoteWorkloadPassSourceInputsSkipsCompatibleAlgorithm(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "target-unrelated")
	source := testRemoteReplayAlgorithmSnapshot("source", "same", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := prewarmRemoteWorkloadPassSourceInputs(
		context.Background(), "repo", "source", []gate.Workload{{ID: "would-require-full-input"}},
		source, target, cache,
	)
	if err != nil || !compatible {
		t.Fatalf("compatible prewarm skipped=%t err=%v", compatible, err)
	}
	if cache.inputComputations != 0 {
		t.Fatalf("compatible prewarm computed %d source inputs", cache.inputComputations)
	}
}

func testRemoteReplayAlgorithmSnapshot(tree, algorithmObject, unrelatedObject string) *remoteGitTreeSnapshot {
	entries := make([]remoteGitTreeEntry, 0, len(remoteWorkloadInputAlgorithmRequiredPaths())+1)
	for _, filePath := range remoteWorkloadInputAlgorithmRequiredPaths() {
		objectID := algorithmObject
		if filePath != "internal/devtools/remoteci/workload_fingerprint_digests.go" {
			objectID = "stable"
		}
		entries = append(entries, remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: objectID, path: filePath})
	}
	entries = append(entries, remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: unrelatedObject, path: "docs/unrelated.md"})
	byPath := make(map[string]remoteGitTreeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.path] = entry
	}
	return &remoteGitTreeSnapshot{repositoryRoot: "repo", tree: tree, entries: entries, byPath: byPath}
}
