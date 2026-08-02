package remoteci

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoordinatorRehydratesExactBaselineCandidateCLI(t *testing.T) {
	binary := []byte("baseline candidate cli\n")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(binary))
	prefix := "baseline-artifacts/64/"
	store := &coordinatorStore{objects: map[string][]byte{prefix + "output/bin/super-dolphin-gate": binary}}
	coordinator := &Coordinator{store: store}
	input := RunInput{
		Commit:                       "0123456789012345678901234567890123456789",
		Tree:                         "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		CandidateGateSourceSHA256:    "sha256:" + strings.Repeat("1", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("2", 64),
		GateBinarySHA256:             digest,
		ReuseBaselineGateCLI:         true,
		BaselineDeltas: []BaselineDeltaLayer{{
			Generation: 64, ObjectPrefix: prefix,
			MainCommit: "0123456789012345678901234567890123456789",
			MainTree:   "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		}},
	}
	tempRoot := t.TempDir()
	ref, binaryPath, manifestPath, binaryKey, err := coordinator.rehydrateRemoteCandidateCLIArtifact(context.Background(), input, "job-0123456789abcdef01234567", tempRoot, "jobs/")
	if err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(binaryPath); readErr != nil || string(data) != string(binary) {
		t.Fatalf("rehydrated binary = %q, %v", data, readErr)
	}
	if info, statErr := os.Stat(binaryPath); statErr != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("rehydrated binary mode = %v, %v", info, statErr)
	}
	if filepath.Dir(manifestPath) != tempRoot || ref.BinarySHA256 != strings.TrimPrefix(digest, "sha256:") || ref.BinaryKey != binaryKey {
		t.Fatalf("rehydrated candidate ref = %+v manifest=%q binary_key=%q", ref, manifestPath, binaryKey)
	}
}

func TestCoordinatorRejectsDriftedBaselineCandidateCLI(t *testing.T) {
	coordinator := &Coordinator{store: &coordinatorStore{objects: map[string][]byte{"baseline-artifacts/64/output/bin/super-dolphin-gate": []byte("wrong")}}}
	input := RunInput{
		Commit: "0123456789012345678901234567890123456789", Tree: "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("1", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("2", 64),
		GateBinarySHA256: "sha256:" + strings.Repeat("3", 64), ReuseBaselineGateCLI: true,
		BaselineDeltas: []BaselineDeltaLayer{{ObjectPrefix: "baseline-artifacts/64/", MainCommit: "0123456789012345678901234567890123456789", MainTree: "abcdefabcdefabcdefabcdefabcdefabcdefabcd"}},
	}
	if _, _, _, _, err := coordinator.rehydrateRemoteCandidateCLIArtifact(context.Background(), input, "job-0123456789abcdef01234567", t.TempDir(), "jobs/"); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("rehydrate drift error = %v", err)
	}
}
