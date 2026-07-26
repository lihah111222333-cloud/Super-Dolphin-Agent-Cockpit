package gateclosure

import (
	"archive/tar"
	"bytes"
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

func TestCollectClosureFilesIncludesOnlyEnvironmentImageInputs(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	sourceRoot := t.TempDir()
	if err := extractGitTree(repositoryRoot, "HEAD^{tree}", sourceRoot); err != nil {
		t.Fatal(err)
	}
	localFiles, ownerFiles, err := collectClosureFiles(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"build/gate/toolchain.lock",
		"build/gate/runtime-deps.lock",
		"cmd/super-dolphin-gate/main.go",
		"cmd/super-dolphin-gate-executor/main.go",
		"internal/devtools/localci/image_builder.go",
	} {
		if !slices.Contains(localFiles, path) {
			t.Fatalf("local closure files do not contain %s", path)
		}
	}
	for _, path := range []string{
		"README.md",
		"cmd/mcp-lsp/main.go",
		"frontend-app/src/App.jsx",
		"internal/module/skill/service.go",
	} {
		if slices.Contains(localFiles, path) || slices.Contains(ownerFiles, path) {
			t.Fatalf("environment image closure unexpectedly contains ordinary job source %s", path)
		}
	}
}

func TestRenderDockerfileBuildsOnlyGateRuntime(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	lock, err := readToolchainLock(filepath.Join(repositoryRoot, gateToolchain))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDeps, err := readRuntimeDepsLock(filepath.Join(repositoryRoot, gateRuntimeDepsLock))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := renderDockerfile(lock, runtimeDeps, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := string(dockerfile)
	for _, wanted := range []string{
		"go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate",
		"go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate-executor ./cmd/super-dolphin-gate-executor",
		"printf '<!doctype html><title>gate compile seed</title>\\n' > /opt/super-dolphin-gate/frontend-embed/index.html",
	} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("generated Dockerfile does not contain %q", wanted)
		}
	}
	for _, unwanted := range []string{
		"COPY . .",
		"npm run build",
		"frontend-app",
		"/opt/super-dolphin-gate/owners",
		"nilness-guard",
		"go test",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("generated environment Dockerfile unexpectedly contains job-source build step %q", unwanted)
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
