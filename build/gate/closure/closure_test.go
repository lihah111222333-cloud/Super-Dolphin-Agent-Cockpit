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
	if manifest.SchemaVersion != "3" || manifest.SourceSnapshotInputCount != len(wantPaths) || manifest.SourceSnapshotPathsSHA256 != sourceSnapshotPathsDigest(wantPaths) {
		t.Fatalf("manifest identity = schema %q count %d digest %q", manifest.SchemaVersion, manifest.SourceSnapshotInputCount, manifest.SourceSnapshotPathsSHA256)
	}
	if _, err := renderManifest(nil, nil, nil); err == nil {
		t.Fatal("renderManifest accepted a missing source snapshot field")
	}
	if _, err := renderManifest(nil, nil, []string{"README.md", "LICENSE"}); err == nil {
		t.Fatal("renderManifest accepted a non-canonical source snapshot path order")
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
	dockerfile, err := renderDockerfile(lock, runtimeDeps, nil, []string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	output := string(dockerfile)
	for _, wanted := range []string{
		"go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate",
		"ARG ACCEPTED_SNAPSHOT_ID",
		"ARG MAIN_COMMIT",
		"ARG GATE_SOURCE_DIGEST",
		"ARG RUNTIME_DEPENDENCY_DIGEST",
		"record_provision_check()",
		"generation-one-compiled-seed/v1",
		"compiled seed manifest fields mismatch",
		"compiled seed identity mismatch",
		"postprocess_step compiled-seed-identity verify_compiled_seed",
		"org.super-dolphin.gate-source-digest=\"${GATE_SOURCE_DIGEST}\"",
		"org.super-dolphin.main-commit-sha=\"${MAIN_COMMIT}\"",
		"COPY --from=postprocess /out/compiled-seed-manifest.json /opt/super-dolphin-gate/compiled-seed-manifest.json",
		"/out/generation-one-receipts/generation-one-build-receipt.json",
		"FROM scratch AS generation-one-build-receipt",
		"COPY --from=postprocess /out/generation-one-receipts/generation-one-build-receipt.json /generation-one-build-receipt.json",
		"test \"$duration_ms\" -gt 0",
		"test \"$dependency_duration_ms\" -gt 0",
		"test \"$frontend_duration_ms\" -gt 0",
		"--mount=type=bind,from=baseline-cache,source=/,target=/baseline-cache,ro",
		"ARG BASELINE_CACHE_IMAGE",
		"ARG COMPILED_SEED_IMAGE",
		"FROM ${BASELINE_CACHE_IMAGE} AS baseline-cache",
		"FROM ${COMPILED_SEED_IMAGE} AS compiled-seed-source",
		"COPY --from=compiled-seed-source /out/ /out/",
		"worker go-cache-proxy --seed /baseline-cache/opt/super-dolphin/cache/go-build --private /out/go-build-cache",
		"env GOCACHEPROG=\"$go_cache_proxy --metrics",
		"compile complete phase=%s elapsed_ms=%s",
		"cache phase={} private_hits={} baseline_hits={} misses={} puts={}",
		"warning phase=%s exceeds_target_ms=100000 elapsed_ms=%s",
		"compiled-seed complete go_cache_entries=%s",
		"compile_phase normal_compile env CGO_ENABLED=1 go test -mod=mod -run \"^$\" ./...",
		"compile_phase e2e_compile env CGO_ENABLED=1 go test -mod=mod -tags=e2e -run \"^$\" ./...",
		"worker race-package-patterns",
		"compile_phase race_compile env CGO_ENABLED=1 go test -mod=mod -race -run \"^$\" \"$@\"",
		"\"provision_checks\":records",
		"\"candidate_compile_not_applicable\"",
		"\"test_body_not_applicable\":True",
		"FROM scratch AS source-snapshot",
		"COPY --from=source-snapshot /root/ /out/source-snapshot/root/",
		"test -n \"$MAIN_COMMIT\" && test -n \"$BUILD_SOURCE_TREE\" && test -n \"$ACCEPTED_SNAPSHOT_ID\" && test -n \"$GATE_SOURCE_DIGEST\" && test -n \"$RUNTIME_DEPENDENCY_DIGEST\"",
		"for object_id in \"$MAIN_COMMIT\" \"$BUILD_SOURCE_TREE\"; do test \"${#object_id}\" -eq 40",
		"for digest in \"$GATE_SOURCE_DIGEST\" \"$RUNTIME_DEPENDENCY_DIGEST\" \"$IMAGE_INPUT_DIGEST\" \"$POLICY_DIGEST\" \"$TOOLCHAIN_DIGEST\"; do case \"$digest\" in sha256:*) digest=${digest#sha256:};; *) exit 1;; esac; test \"${#digest}\" -eq 64",
		"test \"$TARGET_PLATFORM\" = linux/amd64",
		"source_tree",
		"closure_digest",
		"object_format",
		"blob_digest",
		"blob_oid",
		"size",
		"git init --quiet --bare --template= --object-format=sha1 /out/source-baseline.git",
		"git --git-dir=/out/source-baseline.git --work-tree=/out/source-snapshot/root add --all --force",
		"source_tree_sha=$(git --git-dir=/out/source-baseline.git write-tree)",
		"test \"$source_tree_sha\" = \"$BUILD_SOURCE_TREE\"",
		"printf \"%s\\n\" \"super-dolphin accepted source baseline\"",
		"GIT_AUTHOR_NAME=\"Super Dolphin Source Baseline\"",
		"GIT_AUTHOR_EMAIL=\"source-baseline.invalid\"",
		"GIT_AUTHOR_DATE=\"2000-01-01T00:00:00Z\"",
		"GIT_COMMITTER_NAME=\"Super Dolphin Source Baseline\"",
		"GIT_COMMITTER_EMAIL=\"source-baseline.invalid\"",
		"GIT_COMMITTER_DATE=\"2000-01-01T00:00:00Z\"",
		"commit-tree \"$source_tree_sha\"",
		"rev-list --parents -n 1 \"$baseline_commit_sha\"",
		"update-ref refs/source/baseline \"$baseline_commit_sha\"",
		"source-tree-sha",
		"source-baseline-commit-sha",
		"rev-list --all --count",
		"COPY --from=postprocess /out/source-baseline.git /opt/super-dolphin-gate/source-baseline.git",
		"chmod -R a-w /opt/super-dolphin-gate/frontend-embed /opt/super-dolphin-gate/source-snapshot /opt/super-dolphin-gate/source-baseline.git",
		"COPY --from=postprocess /out/source-snapshot /opt/super-dolphin-gate/source-snapshot",
		"chmod -R a-w /opt/super-dolphin-gate/frontend-embed /opt/super-dolphin-gate/source-snapshot",
		"ENV GOCACHE=/out/go-build-cache",
		"COPY --from=postprocess /out/go-build-cache /opt/super-dolphin/cache/go-build",
		"chmod -R a-w /opt/super-dolphin/cache/go-build",
		"(cd frontend-app && ./node_modules/.bin/vite optimize --force)",
		"test -s frontend-app/node_modules/.vite/deps/_metadata.json",
		"npm --prefix frontend-app ci --ignore-scripts --no-audit --no-fund",
		"npm --prefix frontend-app run build",
		"test -s frontend-app/dist/index.html",
		"COPY --from=postprocess /out/frontend-node-modules /opt/super-dolphin-gate/runtime/frontend/node_modules",
		"COPY --from=postprocess /out/vite-cache /opt/super-dolphin-gate/runtime/frontend/vite-cache",
		"COPY --from=postprocess /out/frontend-embed /opt/super-dolphin-gate/frontend-embed",
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
	if strings.Index(output, "test -n \"$MAIN_COMMIT\"") > strings.Index(output, "compile_phase gate_build") {
		t.Fatal("generated Dockerfile validates build identity after the first required check")
	}
	compiledStart := strings.Index(output, "FROM ${RUNTIME_DEPS_IMAGE} AS compiled-seed")
	snapshotStart := strings.Index(output, "FROM scratch AS source-snapshot")
	if compiledStart < 0 || snapshotStart <= compiledStart {
		t.Fatal("the compiled seed target must finish before the complete source snapshot stage begins")
	}
	if strings.Index(output, "npm --prefix frontend-app run build") > strings.Index(output, "compile_phase normal_compile") {
		t.Fatal("generated Dockerfile compiles Go embed consumers before frontend assets exist")
	}
	if !strings.Contains(output, "touch -d \"@${SOURCE_DATE_EPOCH}\" /out/super-dolphin-gate\n\nFROM scratch AS source-snapshot\n") {
		t.Fatal("generated Dockerfile does not close the compiled seed before the complete source snapshot stage")
	}
	postprocessStart := strings.Index(output, "FROM ${RUNTIME_DEPS_IMAGE} AS postprocess")
	if postprocessStart < 0 || strings.Contains(output[postprocessStart:], "FROM compiled-seed AS postprocess") {
		t.Fatal("generation-one postprocessing must consume the published compiled seed image instead of rebuilding the local stage")
	}
	compiledEnd := strings.Index(output, "FROM ${COMPILED_SEED_IMAGE} AS compiled-seed-source")
	if compiledStart < 0 || compiledEnd <= compiledStart || strings.Contains(output[compiledStart:compiledEnd], "/out/source-snapshot/root") {
		t.Fatal("full source snapshot must not invalidate or be read by the expensive compiled seed stage")
	}
	if !strings.Contains(output, "postprocess failed step=%s exit_code=%s action=fail_fast") {
		t.Fatal("postprocess steps must log an explicit fail-fast result")
	}
}

func TestRenderDockerfileEmbedsMinimalReadOnlySourceBaseline(t *testing.T) {
	repositoryRoot := testRepositoryRoot(t)
	lock, err := readToolchainLock(filepath.Join(repositoryRoot, gateToolchain))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDeps, err := readRuntimeDepsLock(filepath.Join(repositoryRoot, gateRuntimeDepsLock))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := renderDockerfile(lock, runtimeDeps, nil, []string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	output := string(dockerfile)
	for _, forbidden := range []string{
		"git clone",
		"git fetch",
		"--mirror",
		"refs/heads/",
		"refs/tags/",
		"git log",
		"/src/.git",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("source baseline Dockerfile contains history or workspace fallback %q", forbidden)
		}
	}
	if strings.Count(output, "commit-tree \"$source_tree_sha\"") != 1 {
		t.Fatalf("source baseline Dockerfile must create exactly one parentless synthetic commit")
	}
	if strings.Count(output, "update-ref refs/source/baseline") != 1 {
		t.Fatalf("source baseline Dockerfile must publish exactly one fixed baseline ref")
	}
	if strings.Index(output, "source_tree_sha=$(git --git-dir=/out/source-baseline.git write-tree)") > strings.Index(output, "commit-tree \"$source_tree_sha\"") {
		t.Fatal("source baseline commit must be created after the accepted source tree is written")
	}
	if strings.Index(output, "test \"$source_tree_sha\" = \"$BUILD_SOURCE_TREE\"") > strings.Index(output, "commit-tree \"$source_tree_sha\"") {
		t.Fatal("source baseline commit must be blocked unless write-tree matches BUILD_SOURCE_TREE")
	}
	if strings.Index(output, "COPY --from=postprocess /out/source-baseline.git /opt/super-dolphin-gate/source-baseline.git") > strings.Index(output, "chmod -R a-w /opt/super-dolphin-gate/frontend-embed /opt/super-dolphin-gate/source-snapshot /opt/super-dolphin-gate/source-baseline.git") {
		t.Fatal("source baseline must be copied before its final recursive read-only enforcement")
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
