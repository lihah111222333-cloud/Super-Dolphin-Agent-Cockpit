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

func TestCollectClosureFilesIncludesFrontendBuildInputsOnlyLocally(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	localFiles, ownerFiles, err := collectClosureFiles(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"frontend-app/src/App.jsx",
		"frontend-app/public/wails/runtime.js",
		"frontend-app/index.html",
		"frontend-app/recovery.html",
		"frontend-app/required-dist-entries.txt",
	} {
		if !slices.Contains(localFiles, path) {
			t.Fatalf("local closure files do not contain %s", path)
		}
	}
	for _, path := range []string{
		"frontend-app/src/App.jsx",
		"frontend-app/public/wails/runtime.js",
		"frontend-app/recovery.html",
		"frontend-app/required-dist-entries.txt",
	} {
		if slices.Contains(ownerFiles, path) {
			t.Fatalf("runtime owner files unexpectedly contain frontend build input %s", path)
		}
	}
}

func TestRenderDockerfileBuildsAndEmbedsFrontend(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	lock, err := readToolchainLock(filepath.Join(repositoryRoot, gateToolchain))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDeps, err := readRuntimeDepsLock(filepath.Join(repositoryRoot, gateRuntimeDepsLock))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := renderDockerfile(lock, runtimeDeps, []string{
		"frontend-app/index.html",
		"frontend-app/public/wails/runtime.js",
		"frontend-app/recovery.html",
		"frontend-app/required-dist-entries.txt",
		"frontend-app/src/App.jsx",
	}, []string{"frontend-app/package.json", "frontend-app/vite.config.js"})
	if err != nil {
		t.Fatal(err)
	}
	output := string(dockerfile)
	for _, wanted := range []string{
		"RUN --network=none ln -s /opt/super-dolphin-gate/runtime/frontend/node_modules /src/frontend-app/node_modules && \\\n",
		"    cd /src/frontend-app && npm run build && \\\n",
		"    test -f /src/cmd/agent-terminal/web-dist/index.html && \\\n",
		"    test -f /src/cmd/agent-terminal/web-dist/recovery.html && \\\n",
		"    chmod -R a-w /src/cmd/agent-terminal/web-dist\n",
		"COPY --from=build --chown=0:0 /src/cmd/agent-terminal/web-dist /opt/super-dolphin-gate/frontend-embed\n",
	} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("generated Dockerfile does not contain %q", wanted)
		}
	}
	finalStageMarker := "FROM $" + "{RUNTIME_DEPS_IMAGE}\nUSER root\n"
	finalStageStart := strings.LastIndex(output, finalStageMarker)
	if finalStageStart < 0 {
		t.Fatalf("generated Dockerfile does not contain final stage marker %q", finalStageMarker)
	}
	finalStage := output[finalStageStart:]
	for _, unwanted := range []string{"frontend-app/src/", "frontend-app/public/", "frontend-app/recovery.html"} {
		if strings.Contains(finalStage, unwanted) {
			t.Fatalf("final stage unexpectedly contains frontend build source %q", unwanted)
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
