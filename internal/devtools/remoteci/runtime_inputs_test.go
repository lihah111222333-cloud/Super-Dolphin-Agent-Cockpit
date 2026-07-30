package remoteci

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestResolveAcceptedRuntimeDependencyDigestAllowsLegacyV5AndV6(t *testing.T) {
	entries := loadRuntimeDependencyEntries(t)
	current, _, err := ResolveRuntimeDependencyBuild(entries, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	assertAcceptedRuntimeDependencyDigest(t, entries, current)
	lockIndex, document, inputs := runtimeDependencyLockDocument(t, entries)
	document["schema_version"] = "6"
	delete(inputs, "runtime_seed_script_sha256")
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	legacyV6 := assertRejectedCurrentAndAcceptedLegacyRuntimeDigest(t, entries, "v6")
	if legacyV6 == current {
		t.Fatal("legacy schema v6 digest unexpectedly equals the schema v7 digest")
	}
	document["schema_version"] = "5"
	delete(inputs, "runtime_seed_recipe_sha256")
	updateRuntimeDependencyLock(t, entries, lockIndex, document)
	legacyV5 := assertAcceptedLegacyRuntimeDigest(t, entries, "v5")
	if legacyV5 == current || legacyV5 == legacyV6 {
		t.Fatal("legacy schema v5 digest is not distinct from newer schemas")
	}
	entries[lockIndex].Data = []byte(`{"schema_version":"4"}`)
	if _, err := ResolveAcceptedRuntimeDependencyDigest(entries, "linux/amd64"); err == nil {
		t.Fatal("accepted runtime dependency resolver accepted unsupported schema")
	}
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
	return legacy
}

func loadRuntimeDependencyEntries(t *testing.T) []sourceexport.TreeEntry {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	paths := append(runtimeDependencyPaths(), "build/gate/runtime-deps.lock")
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
