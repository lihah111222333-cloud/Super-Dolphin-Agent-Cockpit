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

func TestCollectClosureFilesIncludesPrecompiledGateTestInputs(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	sourceRoot := t.TempDir()
	if err := extractGitTree(repositoryRoot, "HEAD^{tree}", sourceRoot); err != nil {
		t.Fatal(err)
	}
	localFiles, gateCompileFiles, err := collectClosureFiles(sourceRoot)
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
		"third_party/kelindar-event/event.go",
		"third_party/kelindar-event/go.mod",
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
		"cmd/super-dolphin-gate-executor/main.go",
	} {
		if slices.Contains(localFiles, path) || slices.Contains(gateCompileFiles, path) {
			t.Fatalf("environment image closure unexpectedly contains ordinary job source %s", path)
		}
	}
}

func TestRenderDockerfilePrecompilesGateModesIntoReadOnlyRuntimeCache(t *testing.T) {
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
		"ARG ACCEPTED_SNAPSHOT_ID",
		"record_provision_check()",
		"/out/generation-one-receipts/generation-one-build-receipt.json",
		"FROM scratch AS generation-one-build-receipt",
		"COPY --from=build /out/generation-one-receipts/generation-one-build-receipt.json /generation-one-build-receipt.json",
		"test \"$duration_ms\" -gt 0",
		"test \"$dependency_duration_ms\" -gt 0",
		"test \"$frontend_duration_ms\" -gt 0",
		"--mount=type=cache,target=/root/.cache/go-build,sharing=locked --mount=type=bind,from=baseline-cache,source=/,target=/baseline-cache,ro",
		"ARG BASELINE_CACHE_IMAGE",
		"FROM ${BASELINE_CACHE_IMAGE} AS baseline-cache",
		"worker go-cache-proxy --seed /baseline-cache/opt/super-dolphin/cache/go-build --private /root/.cache/go-build",
		"env GOCACHEPROG=\"$go_cache_proxy --metrics",
		"compile phase=%s elapsed_ms=%s",
		"cache phase={} private_hits={} baseline_hits={} misses={} puts={}",
		"warning phase=%s exceeds_target_ms=100000 elapsed_ms=%s",
		"cache-export elapsed_ms=%s cache_entries=%s",
		"compile_phase normal_compile env CGO_ENABLED=1 go test -mod=mod -run \"^$\" ./...",
		"compile_phase e2e_compile env CGO_ENABLED=1 go test -mod=mod -tags=e2e -run \"^$\" ./...",
		"worker race-package-patterns",
		"compile_phase race_compile env CGO_ENABLED=1 go test -mod=mod -race -run \"^$\" \"$@\"",
		"\"provision_checks\":records",
		"\"candidate_compile_not_applicable\"",
		"\"test_body_not_applicable\":True",
		"mkdir -p /out/source-snapshot/root; cp -a /src/. /out/source-snapshot/root/",
		"test ! -e /out/source-snapshot/root/frontend-app/node_modules",
		"test ! -e /out/source-snapshot/root/frontend-app/dist",
		"test -n \"$BUILD_SOURCE_TREE\" && test -n \"$ACCEPTED_SNAPSHOT_ID\" && test -n \"$IMAGE_INPUT_DIGEST\" && test -n \"$POLICY_DIGEST\" && test -n \"$TOOLCHAIN_DIGEST\" && test -n \"$TARGET_PLATFORM\"",
		"test \"${#BUILD_SOURCE_TREE}\" -eq 40",
		"for digest in \"$IMAGE_INPUT_DIGEST\" \"$POLICY_DIGEST\" \"$TOOLCHAIN_DIGEST\"; do case \"$digest\" in sha256:*) digest=${digest#sha256:};; *) exit 1;; esac; test \"${#digest}\" -eq 64",
		"test \"$TARGET_PLATFORM\" = linux/amd64",
		"source_tree",
		"closure_digest",
		"object_format",
		"blob_digest",
		"blob_oid",
		"size",
		"COPY --from=build /out/source-snapshot /opt/super-dolphin-gate/source-snapshot",
		"chmod -R a-w /opt/super-dolphin-gate/frontend-embed /opt/super-dolphin-gate/source-snapshot",
		"cp -a /root/.cache/go-build/. /out/go-build-cache",
		"COPY --from=build --chown=65532:65532 /out/go-build-cache /opt/super-dolphin/cache/go-build",
		"chmod -R a-w /opt/super-dolphin/cache/go-build",
		"vite optimize --root frontend-app --force",
		"test -s frontend-app/node_modules/.vite/deps/_metadata.json",
		"npm --prefix frontend-app ci --ignore-scripts --no-audit --no-fund",
		"npm --prefix frontend-app run build",
		"test -s frontend-app/dist/index.html",
		"COPY --from=build --chown=65532:65532 /out/frontend-node-modules /opt/super-dolphin-gate/runtime/frontend/node_modules",
		"COPY --from=build --chown=65532:65532 /out/vite-cache /opt/super-dolphin-gate/runtime/frontend/vite-cache",
		"COPY --from=build /out/frontend-embed /opt/super-dolphin-gate/frontend-embed",
	} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("generated Dockerfile does not contain %q", wanted)
		}
	}
	for _, unwanted := range []string{
		"\n')\n",
		"COPY . .",
		"gate compile seed",
		"super-dolphin-gate-executor",
		"/opt/super-dolphin-gate/owners",
		"nilness-guard",
		"/opt/super-dolphin-gate/cache-seed/go-build",
		"COPY --from=baseline-cache /opt/super-dolphin/cache/go-build /root/.cache/go-build",
		"cp -a /baseline-cache",
		"SOURCE_CLOSURE_DIGEST=",
		"content_digest",
		"frontend-build-cache",
		"/runtime/frontend/build-cache",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("generated environment Dockerfile unexpectedly contains job-source build step %q", unwanted)
		}
	}
	for _, check := range []string{"gate_build", "normal_compile", "e2e_compile", "race_compile"} {
		if count := strings.Count(output, "compile_phase "+check+" env"); count != 1 {
			t.Fatalf("generated Dockerfile has %d %s check invocations, want exactly one", count, check)
		}
	}
	if count := strings.Count(output, "record_provision_check "); count != 3 {
		t.Fatalf("generated Dockerfile has %d provision receipt call sites, want compile plus dependency and frontend", count)
	}
	if strings.Index(output, "test -n \"$BUILD_SOURCE_TREE\"") > strings.Index(output, "compile_phase gate_build") {
		t.Fatal("generated Dockerfile validates build identity after the first required check")
	}
	if strings.Index(output, "cp -a /src/. /out/source-snapshot/root/") > strings.Index(output, "npm --prefix frontend-app ci") {
		t.Fatal("generated Dockerfile snapshots source after frontend dependencies are materialized")
	}
	if strings.Index(output, "npm --prefix frontend-app run build") > strings.Index(output, "compile_phase normal_compile") {
		t.Fatal("generated Dockerfile compiles Go embed consumers before frontend assets exist")
	}
	if !strings.Contains(output, "touch -d \"@${SOURCE_DATE_EPOCH}\" /out/super-dolphin-gate\n\nFROM scratch AS generation-one-build-receipt\n") {
		t.Fatal("generated Dockerfile does not close the check RUN instruction before the receipt target")
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
