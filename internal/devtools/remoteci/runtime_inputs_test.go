package remoteci

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func TestRuntimeLockDigestIgnoresOnlyIncrementalSeedControlPlane(t *testing.T) {
	lock := runtimeDependencyLock{Inputs: map[string]string{
		"toolchain_lock_sha256":              "sha256:toolchain",
		"runtime_seed_script_runtime_sha256": "sha256:runtime-a",
		"runtime_seed_script_browser_sha256": "sha256:browser-a",
		"runtime_seed_script_tail_sha256":    "sha256:tail-a",
	}, RecipeInputs: map[string]string{
		"runtime_seed_recipe_sha256": "sha256:recipe-a",
		"runtime_seed_script_sha256": "sha256:script-a",
	}}
	baseline := runtimeLockDigest(lock)
	for _, name := range []string{"runtime_seed_recipe_sha256", "runtime_seed_script_sha256"} {
		changed := runtimeDependencyLock{Inputs: maps.Clone(lock.Inputs), RecipeInputs: maps.Clone(lock.RecipeInputs)}
		changed.RecipeInputs[name] += "-changed"
		if digest := runtimeLockDigest(changed); digest != baseline {
			t.Fatalf("control-plane input %s invalidated reusable runtime digest", name)
		}
	}
	for _, name := range []string{"toolchain_lock_sha256", "runtime_seed_script_runtime_sha256", "runtime_seed_script_browser_sha256", "runtime_seed_script_tail_sha256"} {
		changed := runtimeDependencyLock{Inputs: maps.Clone(lock.Inputs), RecipeInputs: maps.Clone(lock.RecipeInputs)}
		changed.Inputs[name] += "-changed"
		if digest := runtimeLockDigest(changed); digest == baseline {
			t.Fatalf("runtime content input %s did not invalidate reusable runtime digest", name)
		}
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
	accepted, err := ResolveAcceptedRuntimeDependencyDigest(withNested, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if accepted != nested {
		t.Fatalf("accepted runtime dependency digest = %q, want %q", accepted, nested)
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

func TestResolveRuntimeDependencyBuildRejectsRuntimeSeedRecipeDrift(t *testing.T) {
	for _, recipePath := range []string{
		"cmd/super-dolphin-gate/remote_refresh_seed.go",
		"cmd/super-dolphin-gate/remote_refresh_seed_script.go",
		"cmd/super-dolphin-gate/remote_refresh_seed_script_browser.go",
		"cmd/super-dolphin-gate/remote_refresh_seed_script_runtime.go",
	} {
		t.Run(filepath.Base(recipePath), func(t *testing.T) {
			entries := loadRuntimeDependencyEntries(t)
			for index := range entries {
				if entries[index].Path == recipePath {
					entries[index].Data = append(entries[index].Data, []byte("\n// drift\n")...)
				}
			}
			if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
				t.Fatal("ResolveRuntimeDependencyBuild() unexpectedly accepted runtime seed recipe drift")
			}
		})
	}
}

func TestResolveRuntimeDependencyBuildKeepsRecipeChangesAuditableWithoutChangingRuntimeIdentity(t *testing.T) {
	for field, targetPath := range map[string]string{
		"runtime_seed_recipe_sha256": "cmd/super-dolphin-gate/remote_refresh_seed.go",
		"runtime_seed_script_sha256": "cmd/super-dolphin-gate/remote_refresh_seed_script.go",
	} {
		t.Run(field, func(t *testing.T) {
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
				if entries[index].Path == targetPath {
					entries[index].Data = append(entries[index].Data, []byte("\n// audited recipe change\n")...)
					recipeInputs[field] = remoteBytesDigest(entries[index].Data)
				}
			}
			updateRuntimeDependencyLock(t, entries, lockIndex, document)
			changed, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64")
			if err != nil {
				t.Fatal(err)
			}
			if changed != baseline {
				t.Fatal("audited recipe change invalidated reusable runtime dependency identity")
			}
		})
	}
}

func TestResolveAcceptedRuntimeDependencyDigestAllowsLegacyV5ThroughV10(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	current, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	assertAcceptedRuntimeDependencyDigest(t, entries, current)
	lockIndex, document, inputs := runtimeDependencyLockDocument(t, entries)
	recipeInputs, ok := document["recipe_inputs"].(map[string]any)
	if !ok {
		t.Fatal("runtime dependency fixture recipe inputs are not an object")
	}
	inputs["runtime_seed_worker_sha256"] = recipeInputs["runtime_seed_worker_sha256"]
	delete(recipeInputs, "runtime_seed_worker_sha256")
	delete(recipeInputs, "runtime_seed_script_control_sha256")
	document["schema_version"] = "11"
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	legacyV11 := assertAcceptedLegacyRuntimeDigest(t, entries, "v11")
	if legacyV11 != current {
		t.Fatalf("legacy v11 dependency digest = %q, want v12 %q", legacyV11, current)
	}
	inputs["runtime_seed_script_sha256"] = recipeInputs["runtime_seed_script_sha256"]
	delete(recipeInputs, "runtime_seed_script_sha256")
	delete(inputs, "runtime_seed_script_tail_sha256")
	document["schema_version"] = "10"
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	legacyV10 := assertAcceptedLegacyRuntimeDigest(t, entries, "v10")
	if legacyV10 == current {
		t.Fatal("legacy v10 digest unexpectedly ignored the seed orchestration script")
	}
	maps.Copy(inputs, recipeInputs)
	delete(document, "recipe_inputs")
	document["schema_version"] = "9"
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	legacyV9 := assertAcceptedLegacyRuntimeDigest(t, entries, "v9")
	if legacyV9 != legacyV10 {
		t.Fatalf("legacy v9 dependency digest = %q, want v10 %q", legacyV9, legacyV10)
	}
	seen := map[string]string{current: "v12", legacyV10: "v10"}
	reusable := legacyV10
	migrations := []struct {
		version         string
		removedInput    string
		rejectedCurrent bool
		wantDistinct    bool
	}{
		{version: "v8", removedInput: "runtime_seed_script_browser_sha256", rejectedCurrent: true, wantDistinct: true},
		{version: "v7", removedInput: "runtime_seed_script_runtime_sha256", rejectedCurrent: true, wantDistinct: true},
		{version: "v6", removedInput: "runtime_seed_script_sha256", rejectedCurrent: true, wantDistinct: true},
		{version: "v5", removedInput: "runtime_seed_recipe_sha256"},
	}
	for _, migration := range migrations {
		document["schema_version"] = strings.TrimPrefix(migration.version, "v")
		delete(inputs, migration.removedInput)
		updateRuntimeDependencyLock(t, entries, lockIndex, document)
		digest := acceptedLegacyRuntimeDigest(t, entries, migration)
		if migration.wantDistinct {
			assertDistinctRuntimeDependencyDigest(t, seen, digest, migration.version)
			reusable = digest
		} else if digest != reusable {
			t.Fatalf("legacy control-plane schema %s digest = %q, want reusable %q", migration.version, digest, reusable)
		}
	}
	entries[lockIndex].Data = []byte(`{"schema_version":"3"}`)
	if _, err := ResolveAcceptedRuntimeDependencyDigest(entries, "linux/amd64"); err == nil {
		t.Fatal("accepted runtime dependency resolver accepted unsupported schema")
	}
}

func TestResolveAcceptedRuntimeDependencyDigestAllowsLegacyV4(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	lockIndex, document, inputs := runtimeDependencyLockDocument(t, entries)
	document["schema_version"] = "4"
	manifestBuilder := sourceexport.TreeEntry{
		Path: "build/gate/cmd/runtime-seed-manifest/main.go", Mode: "100644", Data: []byte("package main\n"),
	}
	recipeInputs, ok := document["recipe_inputs"].(map[string]any)
	if !ok {
		t.Fatal("runtime dependency fixture recipe inputs are not an object")
	}
	manifestAPI, ok := recipeInputs["runtime_seed_worker_sha256"]
	if !ok {
		t.Fatal("current runtime dependency fixture is missing runtime seed worker digest")
	}
	for _, field := range []string{
		"runtime_seed_worker_sha256", "runtime_seed_recipe_sha256", "runtime_seed_script_sha256",
		"runtime_seed_script_browser_sha256", "runtime_seed_script_runtime_sha256",
		"runtime_seed_script_tail_sha256",
	} {
		delete(inputs, field)
	}
	inputs["manifest_builder_sha256"] = remoteBytesDigest(manifestBuilder.Data)
	inputs["manifest_api_sha256"] = manifestAPI
	delete(document, "recipe_inputs")
	entries = append(entries, manifestBuilder)
	updateRuntimeDependencyLock(t, entries, lockIndex, document)

	if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
		t.Fatal("current runtime dependency resolver accepted schema v4")
	}
	if _, err := ResolveAcceptedRuntimeDependencyDigest(entries, "linux/amd64"); err != nil {
		t.Fatalf("accepted schema v4: %v", err)
	}
	if !SupportsBaselineRuntimeDependencySchema("4") {
		t.Fatal("baseline runtime dependency resolver does not report schema v4 support")
	}
	entries[len(entries)-1].Data = []byte("package main\n// drift\n")
	if _, err := ResolveAcceptedRuntimeDependencyDigest(entries, "linux/amd64"); err == nil {
		t.Fatal("accepted schema v4 resolver accepted manifest builder drift")
	}
}

func acceptedLegacyRuntimeDigest(
	t *testing.T,
	entries []sourceexport.TreeEntry,
	migration struct {
		version         string
		removedInput    string
		rejectedCurrent bool
		wantDistinct    bool
	},
) string {
	t.Helper()
	if migration.rejectedCurrent {
		return assertRejectedCurrentAndAcceptedLegacyRuntimeDigest(t, entries, migration.version)
	}
	return assertAcceptedLegacyRuntimeDigest(t, entries, migration.version)
}

func assertDistinctRuntimeDependencyDigest(t *testing.T, seen map[string]string, digest, version string) {
	t.Helper()
	if previous, exists := seen[digest]; exists {
		t.Fatalf("legacy schema %s digest equals schema %s", version, previous)
	}
	seen[digest] = version
}

func assertAcceptedRuntimeDependencyDigest(t *testing.T, entries []sourceexport.TreeEntry, want string) {
	t.Helper()
	accepted, err := ResolveAcceptedRuntimeDependencyDigest(entries, "linux/amd64")
	if err != nil || accepted != want {
		t.Fatalf("current accepted digest = %q, %v; want %q", accepted, err, want)
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

func assertRejectedCurrentAndAcceptedLegacyRuntimeDigest(t *testing.T, entries []sourceexport.TreeEntry, version string) string {
	t.Helper()
	if _, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64"); err == nil {
		t.Fatalf("current runtime dependency resolver accepted schema %s", version)
	}
	return assertAcceptedLegacyRuntimeDigest(t, entries, version)
}

func assertAcceptedLegacyRuntimeDigest(t *testing.T, entries []sourceexport.TreeEntry, version string) string {
	t.Helper()
	legacy, err := ResolveAcceptedRuntimeDependencyDigest(entries, "linux/amd64")
	if err != nil {
		t.Fatalf("accepted schema %s: %v", version, err)
	}
	baseline, arguments, schemaVersion, err := ResolveBaselineRuntimeDependencyBuild(entries, "linux/amd64")
	if err != nil {
		t.Fatalf("baseline schema %s: %v", version, err)
	}
	if baseline != legacy || len(arguments) == 0 || schemaVersion != strings.TrimPrefix(version, "v") {
		t.Fatalf("baseline schema %s = %q, args=%d, reported=%s; want %q with build args", version, baseline, len(arguments), schemaVersion, legacy)
	}
	return legacy
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
