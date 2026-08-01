package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoBuildCacheDirectSeedManifestBindsExactReadOnlyTreeAndRuntimeDeltas(t *testing.T) {
	root := realTempDir(t)
	registerDirectSeedWritableCleanup(t, root)
	writeTestFile(t, filepath.Join(root, "aa", "entry-a"), "cache", 0o444)
	if err := os.Chmod(filepath.Join(root, "aa"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildGoBuildCacheDirectSeedManifest(root, testDirectSeedDigest('a'), testDirectSeedDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoBuildCacheDirectSeed(root, manifest); err != nil {
		t.Fatalf("ValidateGoBuildCacheDirectSeed() error = %v", err)
	}
	if !manifest.MatchesRuntimeDeltas(testDirectSeedDigest('a'), testDirectSeedDigest('b')) || manifest.MatchesRuntimeDeltas(testDirectSeedDigest('c'), testDirectSeedDigest('b')) {
		t.Fatal("direct seed runtime delta binding did not reject changed runtime-go identity")
	}

	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "unexpected"), "extra", 0o444)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoBuildCacheDirectSeed(root, manifest); err == nil || !strings.Contains(err.Error(), "does not match manifest") {
		t.Fatalf("ValidateGoBuildCacheDirectSeed(extra file) error = %v", err)
	}
}

func TestGoBuildCacheDirectSeedRejectsWritableDirectoriesAndSymlinks(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, root string){
		"writable directory": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, "aa"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "target"), "cache", 0o444)
			if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
		},
		"writable file": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "entry"), "cache", 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := realTempDir(t)
			registerDirectSeedWritableCleanup(t, root)
			prepare(t, root)
			if err := os.Chmod(root, 0o555); err != nil {
				t.Fatal(err)
			}
			if _, err := BuildGoBuildCacheDirectSeedManifest(root, testDirectSeedDigest('a'), testDirectSeedDigest('b')); err == nil {
				t.Fatal("BuildGoBuildCacheDirectSeedManifest() error = nil")
			}
		})
	}
}

func TestValidateGoBuildCacheDirectSeedMountUsesAcceptedManifest(t *testing.T) {
	root := realTempDir(t)
	registerDirectSeedWritableCleanup(t, root)
	writeTestFile(t, filepath.Join(root, "entry"), "cache", 0o444)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildGoBuildCacheDirectSeedManifest(root, testDirectSeedDigest('a'), testDirectSeedDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoBuildCacheDirectSeedMount(root, manifest); err != nil {
		t.Fatalf("ValidateGoBuildCacheDirectSeedMount() error = %v", err)
	}
}

func registerDirectSeedWritableCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
}

func testDirectSeedDigest(character rune) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}
