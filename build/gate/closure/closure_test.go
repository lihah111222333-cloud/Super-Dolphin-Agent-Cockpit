package gateclosure

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExtractArchiveAcceptsRegularFile(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("regular file\n")
	if err := writer.WriteHeader(&tar.Header{
		Name: "nested/input.txt", Mode: 0o640, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	destination := t.TempDir()
	if err := extractArchive(tar.NewReader(bytes.NewReader(archive.Bytes())), destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "nested", "input.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("extracted content = %q, want %q", got, content)
	}
}

func TestCollectClosureFilesIncludesPrecompiledGateTestInputs(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	sourceRoot := t.TempDir()
	if err := extractGitTree(repositoryRoot, "HEAD^{tree}", sourceRoot); err != nil {
		t.Fatal(err)
	}
	localFiles, gateCompileFiles, sourceSnapshotFiles, err := collectClosureFiles(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"build/gate/toolchain.lock",
		"build/gate/runtime-deps.lock",
		"cmd/super-dolphin-gate/main.go",
		"cmd/mcp-lsp/main.go",
	} {
		if !slices.Contains(localFiles, path) {
			t.Fatalf("local closure files do not contain %s", path)
		}
	}
	for _, path := range []string{
		"cmd/super-dolphin-gate/main.go",
		"go.mod",
		"go.sum",
	} {
		if !slices.Contains(gateCompileFiles, path) {
			t.Fatalf("gate compile closure does not contain %s", path)
		}
	}
	for _, path := range []string{"frontend-app/package-lock.json", "third_party/kelindar-event/event_test.go"} {
		if slices.Contains(gateCompileFiles, path) {
			t.Fatalf("gate compile closure unexpectedly contains %s", path)
		}
	}
	for _, path := range []string{
		"README.md",
		"LICENSE",
	} {
		if slices.Contains(localFiles, path) || slices.Contains(gateCompileFiles, path) {
			t.Fatalf("environment image closure unexpectedly contains ordinary job source %s", path)
		}
	}
	for _, path := range []string{"README.md", "LICENSE"} {
		if !slices.Contains(sourceSnapshotFiles, path) {
			t.Fatalf("source snapshot closure does not contain tracked path %s", path)
		}
	}
	trackedOutput, err := commandOutput(repositoryRoot, nil, "git", "ls-tree", "-rz", "--name-only", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshotFiles := strings.Split(strings.TrimSuffix(trackedOutput, "\x00"), "\x00")
	if !slices.Equal(sourceSnapshotFiles, wantSnapshotFiles) {
		t.Fatalf("source snapshot closure has %d paths, want exact tracked tree with %d paths", len(sourceSnapshotFiles), len(wantSnapshotFiles))
	}
}

func TestRenderManifestBindsExactSourceSnapshotInputs(t *testing.T) {
	data, err := renderManifest(
		[]string{"build/gate/Dockerfile"},
		[]string{"cmd/super-dolphin-gate/main.go"},
		[]string{"LICENSE", "README.md"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var manifest inputManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"LICENSE", "README.md"}
	if manifest.SchemaVersion != "4" || manifest.SourceSnapshotInputCount != len(wantPaths) || manifest.SourceSnapshotPathsSHA256 != sourceSnapshotPathsDigest(wantPaths) {
		t.Fatalf("manifest identity = schema %q count %d digest %q", manifest.SchemaVersion, manifest.SourceSnapshotInputCount, manifest.SourceSnapshotPathsSHA256)
	}
	if _, err := renderManifest(nil, nil, nil); err == nil {
		t.Fatal("renderManifest accepted a missing source snapshot field")
	}
	if _, err := renderManifest(nil, nil, []string{"README.md", "LICENSE"}); err == nil {
		t.Fatal("renderManifest accepted a non-canonical source snapshot path order")
	}
}

func TestRenderDockerfileBindsExactSourceClosure(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	lock, err := readToolchainLock(filepath.Join(repositoryRoot, gateToolchain))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDeps, err := readRuntimeDepsLock(filepath.Join(repositoryRoot, gateRuntimeDepsLock))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := renderDockerfile(lock, runtimeDeps, nil, []string{"LICENSE", "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	output := string(dockerfile)
	for _, required := range []string{
		"FROM scratch AS source-closure",
		"FROM source-closure AS final",
		"ARG BUILD_SOURCE_TREE",
		"ARG GATE_SOURCE_DIGEST",
		"ARG RUNTIME_DEPENDENCY_DIGEST",
		"ARG TOOLCHAIN_DIGEST",
		"trusted-gate-closure/v1",
		"org.super-dolphin.source-tree-sha=\"${BUILD_SOURCE_TREE}\"",
		"org.super-dolphin.runtime-dependency-digest=\"${RUNTIME_DEPENDENCY_DIGEST}\"",
		"COPY [\"LICENSE\",\"README.md\",\"/root/./\"]",
	} {
		if !strings.Contains(output, required) {
			t.Fatalf("source closure wire is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"remote-ci-" + "cache-material/v1",
		"non_" + "authoritative_material",
		"runtime_" + "image",
		"RUNTIME_DEPS_" + "IMAGE",
		"BASELINE_" + "CACHE_IMAGE",
		"COMPILED_" + "SEED_IMAGE",
		"compiled-" + "seed",
		"Image" + "Cache",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("source closure wire contains retired cloud material %q", forbidden)
		}
	}
}
func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return repositoryRoot
}

func TestCollectGoEmbedCompileFilesDiscoversEmbeddedFiles(t *testing.T) {
	tests := []struct {
		name      string
		directive string
		assets    map[string]string
		want      []string
		notWant   string
	}{
		{
			name:      "single quoted file",
			directive: `"assets/single file.txt"`,
			assets: map[string]string{
				"assets/single file.txt": "single\n",
			},
			want: []string{"fixture/assets/single file.txt"},
		},
		{
			name:      "glob",
			directive: "assets/*.txt",
			assets: map[string]string{
				"assets/first.txt":   "first\n",
				"assets/second.txt":  "second\n",
				"assets/ignored.bin": "ignored\n",
			},
			want:    []string{"fixture/assets/first.txt", "fixture/assets/second.txt"},
			notWant: "fixture/assets/ignored.bin",
		},
		{
			name:      "quoted raw and multiple patterns",
			directive: "\"assets/double file.txt\" `assets/raw file.txt` assets/plain.txt",
			assets: map[string]string{
				"assets/double file.txt": "double\n",
				"assets/raw file.txt":    "raw\n",
				"assets/plain.txt":       "plain\n",
			},
			want: []string{
				"fixture/assets/double file.txt",
				"fixture/assets/plain.txt",
				"fixture/assets/raw file.txt",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			writeClosureEmbedTestFile(t, sourceRoot, "fixture/embed.go", "package fixture\n\nimport \"embed\"\n\n//go:embed "+test.directive+"\nvar content embed.FS\n")
			for name, content := range test.assets {
				writeClosureEmbedTestFile(t, sourceRoot, filepath.ToSlash(filepath.Join("fixture", name)), content)
			}

			files, err := collectGoEmbedCompileFiles(sourceRoot, []string{"fixture/embed.go"})
			if err != nil {
				t.Fatalf("collect Go embed compile files: %v", err)
			}
			for _, want := range test.want {
				if !slices.Contains(files, want) {
					t.Fatalf("compile inputs do not contain embedded file %s", want)
				}
			}
			if test.notWant != "" {
				if slices.Contains(files, test.notWant) {
					t.Fatalf("compile inputs unexpectedly contain unmatched file %s", test.notWant)
				}
			}
		})
	}
}

func TestCollectGoEmbedCompileFilesRejectsInvalidOrEmptyPatterns(t *testing.T) {
	tests := []struct {
		name      string
		directive string
		wantError string
	}{
		{name: "path escape", directive: "../escape", wantError: "invalid pattern syntax"},
		{name: "malformed glob", directive: "assets/[", wantError: "invalid pattern syntax"},
		{name: "empty match", directive: "assets/*.missing", wantError: "no matching files found"},
		{name: "unterminated double quote", directive: `"assets/present.txt`, wantError: "invalid quoted string"},
		{name: "unterminated raw quote", directive: "`assets/present.txt", wantError: "invalid quoted string"},
		{name: "quoted patterns without separator", directive: `"assets/present.txt"assets/other.txt`, wantError: "invalid quoted string"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			writeClosureEmbedTestFile(t, sourceRoot, "fixture/embed.go", "package fixture\n\nimport \"embed\"\n\n//go:embed "+test.directive+"\nvar content embed.FS\n")
			writeClosureEmbedTestFile(t, sourceRoot, "fixture/assets/present.txt", "present\n")

			_, err := collectGoEmbedCompileFiles(sourceRoot, []string{"fixture/embed.go"})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("collectGoEmbedCompileFiles() error = %v, want text %q", err, test.wantError)
			}
		})
	}
}

func TestCollectGoEmbedCompileFilesIgnoresEmptyMatchedDirectories(t *testing.T) {
	sourceRoot := t.TempDir()
	writeClosureEmbedTestFile(t, sourceRoot, "fixture/embed.go", "package fixture\n\nimport \"embed\"\n\n//go:embed assets/*\nvar content embed.FS\n")
	for _, directory := range []string{
		"fixture/assets/empty",
		"fixture/assets/hidden-only",
		"fixture/assets/nested-only/module",
	} {
		if err := os.MkdirAll(filepath.Join(sourceRoot, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("create directory %s: %v", directory, err)
		}
	}
	writeClosureEmbedTestFile(t, sourceRoot, "fixture/assets/hidden-only/.excluded", "hidden\n")
	writeClosureEmbedTestFile(t, sourceRoot, "fixture/assets/nested-only/module/go.mod", "module example.invalid/nested\n")
	writeClosureEmbedTestFile(t, sourceRoot, "fixture/assets/full/file.txt", "full\n")

	files, err := collectGoEmbedCompileFiles(sourceRoot, []string{"fixture/embed.go"})
	if err != nil {
		t.Fatalf("collect Go embed compile files: %v", err)
	}
	want := []string{"fixture/assets/full/file.txt"}
	if !slices.Equal(files, want) {
		t.Fatalf("compile inputs = %v, want %v", files, want)
	}
}

func TestCollectGoEmbedCompileFilesRejectsPatternMatchingOnlyEmptyDirectories(t *testing.T) {
	sourceRoot := t.TempDir()
	writeClosureEmbedTestFile(t, sourceRoot, "fixture/embed.go", "package fixture\n\nimport \"embed\"\n\n//go:embed assets/*\nvar content embed.FS\n")
	for _, directory := range []string{"fixture/assets/empty-a", "fixture/assets/empty-b"} {
		if err := os.MkdirAll(filepath.Join(sourceRoot, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("create directory %s: %v", directory, err)
		}
	}

	_, err := collectGoEmbedCompileFiles(sourceRoot, []string{"fixture/embed.go"})
	if err == nil || !strings.Contains(err.Error(), "no matching files found") {
		t.Fatalf("collectGoEmbedCompileFiles() error = %v, want pattern-level no-match rejection", err)
	}
}

func TestCollectGoEmbedCompileFilesRejectsSymbolicLink(t *testing.T) {
	sourceRoot := t.TempDir()
	writeClosureEmbedTestFile(t, sourceRoot, "fixture/embed.go", "package fixture\n\nimport \"embed\"\n\n//go:embed assets/link.txt\nvar content embed.FS\n")
	writeClosureEmbedTestFile(t, sourceRoot, "outside.txt", "outside\n")
	assetDirectory := filepath.Join(sourceRoot, "fixture", "assets")
	if err := os.MkdirAll(assetDirectory, 0o755); err != nil {
		t.Fatalf("create asset directory: %v", err)
	}
	if err := os.Symlink(filepath.Join(sourceRoot, "outside.txt"), filepath.Join(assetDirectory, "link.txt")); err != nil {
		t.Fatalf("create embedded symbolic link: %v", err)
	}

	_, err := collectGoEmbedCompileFiles(sourceRoot, []string{"fixture/embed.go"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("collectGoEmbedCompileFiles() error = %v, want symbolic-link rejection", err)
	}
}

func TestCollectGateCompileFilesDiscoversProjectMapGeneratorAsset(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	files, err := collectGateCompileFiles(repositoryRoot)
	if err != nil {
		t.Fatalf("collect gate compile files: %v", err)
	}
	wantAsset := "internal/devtools/projectmaptrusted/assets/generate_ai_project_map.mjs.gz"
	if !slices.Contains(files, wantAsset) {
		t.Fatalf("compile inputs do not contain auto-discovered asset %s", wantAsset)
	}
}

func writeClosureEmbedTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", relative, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}
