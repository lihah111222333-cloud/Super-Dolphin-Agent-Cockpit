package localci

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestResolveGateImageInputsIsDeterministicAndIgnoresOrdinarySource(t *testing.T) {
	baseEntries := candidateEntries(validCandidateDockerfile())
	base := mustResolveGateImageInputs(t, readOnlyImageTree(t, baseEntries))

	reordered := append([]sourceexport.TreeEntry(nil), baseEntries...)
	slices.Reverse(reordered)
	second := mustResolveGateImageInputs(t, readOnlyImageTree(t, reordered))
	if base.ImageInputDigest != second.ImageInputDigest || base.ContextDigest != second.ContextDigest {
		t.Fatal("reordered Git entries changed deterministic image inputs")
	}

	ordinaryChange := append(reordered, contextEntry("internal/module/example/service.go", "100644", "package example\n"))
	ordinary := mustResolveGateImageInputs(t, readOnlyImageTree(t, ordinaryChange))
	if ordinary.SubmittedSourceTree == base.SubmittedSourceTree {
		t.Fatal("ordinary source change did not change submitted Git tree provenance")
	}
	if ordinary.ImageInputDigest != base.ImageInputDigest || ordinary.ContextDigest != base.ContextDigest {
		t.Fatal("ordinary source change altered canonical image inputs")
	}
}

func TestResolveGateImageInputsChangesForDeclaredInput(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	base := mustResolveGateImageInputs(t, readOnlyImageTree(t, entries))
	changeEntry(t, entries, "go.mod", "module example.invalid/changed\n")
	changed := mustResolveGateImageInputs(t, readOnlyImageTree(t, entries))
	if changed.ImageInputDigest == base.ImageInputDigest || changed.ContextDigest == base.ContextDigest {
		t.Fatal("declared image input change did not alter canonical digests")
	}
}

func TestResolveGateImageInputsRejectsTreeDriftAndSymlink(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	tree := readOnlyImageTree(t, entries)
	tree.Entries[0].Data = []byte("drifted without a Git object update\n")
	if _, err := ResolveGateImageInputs(tree, digest("d"), "linux/arm64"); err == nil || !strings.Contains(err.Error(), "does not match Git blob") {
		t.Fatalf("tree data drift error = %v", err)
	}

	tree = readOnlyImageTree(t, entries)
	tree.Entries[0].Mode = "120000"
	if _, err := ResolveGateImageInputs(tree, digest("d"), "linux/arm64"); err == nil || !strings.Contains(err.Error(), "unsupported mode") {
		t.Fatalf("symlink tree error = %v", err)
	}
}

func TestResolveGateImageInputsRejectsMaliciousDockerfile(t *testing.T) {
	dockerfile := validCandidateDockerfile() + "COPY hidden.txt /tmp/hidden.txt\n"
	entries := append(candidateEntries(dockerfile), contextEntry("hidden.txt", "100644", "hidden\n"))
	_, err := ResolveGateImageInputs(readOnlyImageTree(t, entries), digest("d"), "linux/arm64")
	if err == nil || !strings.Contains(err.Error(), "Dockerfile COPY source") {
		t.Fatalf("undeclared Dockerfile COPY error = %v", err)
	}
}

func TestGateImageInputFieldRegistryIsComplete(t *testing.T) {
	assertRegisteredFields(t, reflect.TypeFor[GateImageInputs](), map[string]string{
		"SubmittedSourceTree": "job provenance", "PolicyDigest": "policy binding",
		"ImageSchemaVersion": "schema binding", "Platform": "platform binding",
		"SourceEntries": "candidate builder input", "ImageInputDigest": "accepted reuse decision",
		"ContextDigest": "context evidence", "InputManifestDigest": "manifest evidence",
		"ToolchainDigest": "toolchain evidence", "DockerfileDigest": "Dockerfile evidence",
	})
}

func readOnlyImageTree(t *testing.T, entries []sourceexport.TreeEntry) ReadOnlyGitTree {
	t.Helper()
	cloned := cloneTreeEntries(entries)
	treeOID, err := calculateGitTreeOID(gate.GitObjectFormatSHA1, cloned)
	if err != nil {
		t.Fatal(err)
	}
	return ReadOnlyGitTree{
		Source: gate.SourceSpec{
			Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
			Tree: &gate.TreeSource{SHA: treeOID}, SourceTreeSHA: treeOID,
		},
		Entries: cloned,
	}
}

func mustResolveGateImageInputs(t *testing.T, tree ReadOnlyGitTree) GateImageInputs {
	t.Helper()
	inputs, err := ResolveGateImageInputs(tree, digest("d"), "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}
