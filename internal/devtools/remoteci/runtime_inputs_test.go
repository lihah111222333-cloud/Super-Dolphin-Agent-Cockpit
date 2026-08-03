package remoteci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestResolveRuntimeDependencyBuildUsesLockedGitTree(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	digest, arguments, err := ResolveRuntimeDependencyBuild(entries, "linux/arm64")
	if err != nil {
		t.Fatalf("ResolveRuntimeDependencyBuild() error = %v", err)
	}
	if !remoteDigestPattern.MatchString(digest) {
		t.Fatalf("ResolveRuntimeDependencyBuild() digest = %q", digest)
	}
	joined := strings.Join(arguments, "\n")
	for _, required := range []string{
		"GO_IMAGE=", "NODE_IMAGE=", "SQRUFF_ARCHIVE_URL_AMD64=",
		"SQRUFF_ARCHIVE_SHA256_AMD64=", "SQRUFF_ARCHIVE_URL_ARM64=",
		"SQRUFF_ARCHIVE_SHA256_ARM64=",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("ResolveRuntimeDependencyBuild() arguments missing %q", required)
		}
	}
}

func TestRuntimeDependencyPlatformsUseProductionContractAndRetainArm64Artifact(t *testing.T) {
	if !validRuntimePlatform(cicontract.TargetPlatform) {
		t.Fatalf("runtime artifacts rejected remote CI target %q", cicontract.TargetPlatform)
	}
	if !validRuntimePlatform("linux/arm64") {
		t.Fatal("runtime artifacts rejected official linux/arm64 support")
	}
}

func TestRuntimeLockDigestIgnoresSeedWorkerRecipe(t *testing.T) {
	lock := runtimeDependencyLock{Inputs: map[string]string{
		"toolchain_lock_sha256": "sha256:toolchain",
	}, RecipeInputs: map[string]string{
		"runtime_seed_worker_sha256": "sha256:worker-a",
	}}
	baseline := runtimeLockDigest(lock)
	changedRecipe := runtimeDependencyLock{Inputs: map[string]string{"toolchain_lock_sha256": "sha256:toolchain"}, RecipeInputs: map[string]string{"runtime_seed_worker_sha256": "sha256:worker-b"}}
	if digest := runtimeLockDigest(changedRecipe); digest != baseline {
		t.Fatal("seed worker recipe invalidated reusable runtime digest")
	}
	changedContent := runtimeDependencyLock{Inputs: map[string]string{"toolchain_lock_sha256": "sha256:toolchain-b"}, RecipeInputs: lock.RecipeInputs}
	if digest := runtimeLockDigest(changedContent); digest == baseline {
		t.Fatal("runtime content input did not invalidate reusable runtime digest")
	}
}

func TestResolveRuntimeDependencyBuildIndexesEveryGoModuleManifest(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	baseline, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}

	withNested := append([]sourceexport.TreeEntry(nil), entries...)
	withNested = append(withNested,
		sourceexport.TreeEntry{Path: "nested/new-module/go.mod", Mode: "100644", Data: []byte("module example.com/nested\n\ngo 1.26.0\n")},
		sourceexport.TreeEntry{Path: "nested/new-module/go.sum", Mode: "100644", Data: []byte("example.com/dependency v1.0.0 h1:first\n")},
	)
	nested, _, err := ResolveRuntimeDependencyBuild(withNested, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if nested == baseline {
		t.Fatal("nested Go module manifests did not change runtime dependency identity")
	}
	withNested[len(withNested)-1].Data = []byte("example.com/dependency v1.0.0 h1:changed\n")
	changed, _, err := ResolveRuntimeDependencyBuild(withNested, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if changed == nested {
		t.Fatal("nested go.sum change did not invalidate runtime dependency identity")
	}
}

func TestRuntimeGoModuleManifestsRejectsDuplicatePaths(t *testing.T) {
	entries := []sourceexport.TreeEntry{
		{Path: "go.mod", Mode: "100644", Data: []byte("module example.com/one\n")},
		{Path: "go.mod", Mode: "100644", Data: []byte("module example.com/two\n")},
	}
	if _, err := runtimeGoModuleManifests(entries); err == nil {
		t.Fatal("runtimeGoModuleManifests() accepted duplicate manifest paths")
	}
}

func TestRuntimeGoModuleManifestsIgnoreOnlyGoDirective(t *testing.T) {
	entries := []sourceexport.TreeEntry{
		{Path: "go.mod", Mode: "100644", Data: []byte("module example.com/root\n\ngo 1.26.5\n\nrequire example.com/dependency v1.0.0\n")},
		{Path: "go.sum", Mode: "100644", Data: []byte("example.com/dependency v1.0.0 h1:first\n")},
	}
	baseline, err := runtimeGoModuleManifests(entries)
	if err != nil {
		t.Fatal(err)
	}
	directiveChanged := append([]sourceexport.TreeEntry(nil), entries...)
	directiveChanged[0].Data = []byte(strings.ReplaceAll(string(entries[0].Data), "go 1.26.5", "go 1.26.0"))
	directiveManifests, err := runtimeGoModuleManifests(directiveChanged)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(directiveManifests, baseline) {
		t.Fatal("Go directive invalidated reusable dependency content")
	}
	requireChanged := append([]sourceexport.TreeEntry(nil), entries...)
	requireChanged[0].Data = []byte(strings.ReplaceAll(string(entries[0].Data), "v1.0.0", "v1.1.0"))
	requireManifests, err := runtimeGoModuleManifests(requireChanged)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(requireManifests, baseline) {
		t.Fatal("Go requirement change did not invalidate reusable dependency content")
	}
}

func TestResolveRuntimeDependencyBuildRejectsLockDrift(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	for index := range entries {
		if entries[index].Path == "go.mod" {
			entries[index].Data = append(entries[index].Data, []byte("\n// drift\n")...)
		}
	}
	if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
		t.Fatal("ResolveRuntimeDependencyBuild() unexpectedly accepted drift")
	}
}

func TestResolveRuntimeDependencyBuildRejectsRuntimeSeedWorkerRecipeDrift(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	for index := range entries {
		if entries[index].Path == "internal/devtools/gate/executor_seed.go" {
			entries[index].Data = append(entries[index].Data, []byte("\n// drift\n")...)
		}
	}
	if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
		t.Fatal("ResolveRuntimeDependencyBuild() unexpectedly accepted runtime seed worker recipe drift")
	}
}

func TestResolveRuntimeDependencyBuildKeepsSeedWorkerRecipeChangesAuditableWithoutChangingRuntimeIdentity(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	baseline, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	lockIndex, document, _ := runtimeDependencyLockDocument(t, entries)
	recipeInputs, ok := document["recipe_inputs"].(map[string]any)
	if !ok {
		t.Fatal("runtime dependency fixture recipe inputs are not an object")
	}
	for index := range entries {
		if entries[index].Path == "internal/devtools/gate/executor_seed.go" {
			entries[index].Data = append(entries[index].Data, []byte("\n// audited recipe change\n")...)
			recipeInputs["runtime_seed_worker_sha256"] = remoteBytesDigest(entries[index].Data)
		}
	}
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	changed, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if changed != baseline {
		t.Fatal("audited seed worker recipe change invalidated reusable runtime dependency identity")
	}
}

func TestResolveRuntimeDependencyBuildRejectsRemovedSeedLockShape(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	lockIndex, document, inputs := runtimeDependencyLockDocument(t, entries)
	inputs["runtime_seed_script_tail_sha256"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
		t.Fatal("current runtime dependency resolver accepted removed seed input")
	}
	entries = loadRuntimeDependencyEntries(t)
	lockIndex, document, _ = runtimeDependencyLockDocument(t, entries)
	document["schema_version"] = "12"
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
		t.Fatal("current runtime dependency resolver accepted schema v12")
	}
}

func runtimeDependencyLockDocument(t *testing.T, entries []sourceexport.TreeEntry) (int, map[string]any, map[string]any) {
	t.Helper()
	for index := range entries {
		if entries[index].Path == "build/gate/runtime-deps.lock" {
			var document map[string]any
			if err := json.Unmarshal(entries[index].Data, &document); err != nil {
				t.Fatal(err)
			}
			inputs, ok := document["inputs"].(map[string]any)
			if !ok {
				t.Fatal("runtime dependency fixture inputs are not an object")
			}
			return index, document, inputs
		}
	}
	t.Fatal("runtime dependency fixture does not include lock file")
	return 0, nil, nil
}

func updateRuntimeDependencyLock(t *testing.T, entries []sourceexport.TreeEntry, index int, document map[string]any) {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	entries[index].Data = data
}

func loadRuntimeDependencyEntries(t *testing.T) []sourceexport.TreeEntry {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	paths := append(runtimeDependencyPaths(), runtimeDependencyRecipePaths()...)
	paths = append(paths, "build/gate/runtime-deps.lock")
	entries := make([]sourceexport.TreeEntry, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		entries = append(entries, sourceexport.TreeEntry{Path: path, Mode: "100644", Data: data})
	}
	return entries
}
