package localci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestOrdinaryJobSourceChangeReusesAcceptedEnvironmentImage(t *testing.T) {
	entries := trackedGateClosureEntries(t)
	entries = append(entries, contextEntry("docs/README.md", "100644", "before\n"))
	baseRequest := candidateRequest(slices.Clone(entries), digest("f"), digest("e"))
	base, err := prepareCandidate(baseRequest)
	if err != nil {
		t.Fatal(err)
	}

	changedEntries := slices.Clone(entries)
	appendEntryByte(t, changedEntries, "docs/README.md")
	changed, err := prepareCandidate(candidateRequest(changedEntries, base.result.InputDigest, digest("8")))
	if err != nil {
		t.Fatal(err)
	}
	if changed.result.InputDigest != base.result.InputDigest || changed.result.ContextDigest != base.result.ContextDigest {
		t.Fatal("ordinary job source change altered the environment image identity")
	}
	runner := &recordingBuildKitRunner{digest: digest("9")}
	request := candidateRequest(changedEntries, base.result.InputDigest, digest("8"))
	request.AcceptedImageDigest = digest("7")
	request.AcceptedConfigDigest = digest("6")
	mustEnsureCandidate(t, runner, request)
	if len(runner.requests) != 0 {
		t.Fatalf("ordinary job source change triggered %d candidate builds, want 0", len(runner.requests))
	}
}

func TestEnvironmentRunnerChangeTriggersCandidateBuild(t *testing.T) {
	entries := trackedGateClosureEntries(t)
	base, err := prepareCandidate(candidateRequest(slices.Clone(entries), digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	changedEntries := slices.Clone(entries)
	appendEntryByte(t, changedEntries, "cmd/super-dolphin-gate/main.go")
	changed, err := prepareCandidate(candidateRequest(changedEntries, base.result.InputDigest, digest("8")))
	if err != nil {
		t.Fatal(err)
	}
	if changed.result.InputDigest == base.result.InputDigest || changed.result.ContextDigest == base.result.ContextDigest {
		t.Fatal("gate runner change did not alter the environment image identity")
	}
}

func TestRuntimeDependenciesDoNotCopyJobSource(t *testing.T) {
	root := repositoryRootForGateContract(t)
	data, err := os.ReadFile(filepath.Join(root, "build/gate/runtime-deps.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	for _, wanted := range []string{
		"FROM go-build-base AS repository-module-cache",
		"COPY go.mod go.sum ./",
		"COPY third_party/kelindar-event/ ./third_party/kelindar-event/",
		"go mod download all",
		"COPY --from=repository-module-cache /out/go-proxy /opt/super-dolphin-gate/runtime/go-proxy",
		"COPY --from=repository-module-cache /go/pkg/mod /opt/super-dolphin-gate/runtime/go-mod-cache",
	} {
		if !strings.Contains(dockerfile, wanted) {
			t.Fatalf("runtime dependency Dockerfile is missing %q", wanted)
		}
	}
	for _, unwanted := range []string{"go mod vendor", "COPY cmd/ ./cmd/", "COPY internal/ ./internal/", "/out/vendor"} {
		if strings.Contains(dockerfile, unwanted) {
			t.Fatalf("runtime dependency Dockerfile still binds ordinary job source through %q", unwanted)
		}
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
	entries := make([]sourceexport.TreeEntry, 0, len(manifest.Inputs))
	for _, name := range manifest.Inputs {
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
