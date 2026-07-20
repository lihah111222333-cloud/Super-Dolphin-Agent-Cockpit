package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func writeProductionBuildInputs(t *testing.T, repository string) {
	t.Helper()
	files := productionBuildInputFiles()
	files["build/gate/runtime-deps.lock"] = productionRuntimeDepsLock(t, files)
	inputs := make([]string, 0, len(files)+1)
	for path := range files {
		inputs = append(inputs, path)
	}
	inputs = append(inputs, "build/gate/inputs.json")
	sort.Strings(inputs)
	files["build/gate/inputs.json"] = productionFixtureJSON(t, map[string]any{
		"schema_version": "1", "dockerfile": "build/gate/Dockerfile", "inputs": inputs,
	})
	for path, data := range files {
		absolute := filepath.Join(repository, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func productionBuildInputFiles() map[string]string {
	return map[string]string{
		"build/gate/Dockerfile": "ARG RUNTIME_DEPS_IMAGE\n" +
			"ARG SOURCE_DATE_EPOCH=0\nFROM ${RUNTIME_DEPS_IMAGE} AS build\nARG SOURCE_DATE_EPOCH\nCOPY go.mod go.sum ./\n" +
			"COPY cmd/super-dolphin-gate/main.go ./cmd/super-dolphin-gate/main.go\n" +
			"RUN --network=none go build -o /out/gate ./cmd/super-dolphin-gate\n" +
			"FROM scratch\nCOPY --from=build /out/gate /gate\nENTRYPOINT [\"/gate\"]\n",
		"build/gate/runtime-deps.Dockerfile":           "FROM scratch\n",
		"build/gate/runtime-lsp/package-lock.json":     "{}\n",
		"build/gate/runtime-proxy/go.mod":              "module example.invalid/proxy\n",
		"build/gate/runtime-proxy/go.sum":              "proxy sum\n",
		"build/gate/runtime-tools/go.mod":              "module example.invalid/tools\n",
		"build/gate/runtime-tools/go.sum":              "tools sum\n",
		"build/gate/cmd/runtime-seed-manifest/main.go": "package main\n",
		"frontend-app/package-lock.json":               "{}\n",
		"go.mod":                                       "module example.invalid/gate\n",
		"go.sum":                                       "sum\n",
		"internal/devtools/gate/executor_seed.go":      "package gate\n",
		"internal/devtools/nilnessrunner/runner.go":    "package nilnessrunner\n",
		"scripts/nilness_guard.go":                     "package main\n",
		"cmd/super-dolphin-gate/main.go":               "package main\n",
		"build/gate/toolchain.lock":                    productionToolchainLock(),
	}
}

func productionToolchainLock() string {
	return "{\n  \"schema_version\": \"1\",\n  \"buildkit_version\": \"v0.26.2\",\n" +
		"  \"dockerfile_frontend\": \"builtin:dockerfile.v1\",\n  \"source_date_epoch\": \"0\",\n" +
		"  \"target_platforms\": [\"linux/amd64\",\"linux/arm64\"],\n" +
		"  \"base_images\": [{\"name\":\"GO_IMAGE\",\"reference\":\"registry.example.invalid/base/golang@sha256:" + strings.Repeat("b", 64) + "\"}],\n" +
		"  \"dependency_sources\": [\"go.sum\"],\n  \"runtime_deps_lock\": \"build/gate/runtime-deps.lock\",\n" +
		"  \"runtime_tools\": {\"node_version\":\"v1\",\"npm_version\":\"1\",\"python_version\":\"3\",\"ripgrep\":\"/opt/super-dolphin-gate/runtime/bin/rg@13.0.0-4+b2\",\"sqruff\":\"/opt/super-dolphin-gate/runtime/bin/sqruff@0.38.0\",\"sqruff_artifacts\":[{\"platform\":\"linux/amd64\",\"url\":\"https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-x86_64-musl.tar.gz\",\"sha256\":\"" + strings.Repeat("a", 64) + "\"},{\"platform\":\"linux/arm64\",\"url\":\"https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-aarch64-musl.tar.gz\",\"sha256\":\"" + strings.Repeat("c", 64) + "\"}],\"gopls\":\"gopls@v1\",\"sqlc\":\"sqlc@v1\",\"npm_lsp_packages\":[\"typescript@1\"]},\n" +
		"  \"network_policy\": \"none\"\n}\n"
}

func productionRuntimeDepsLock(t *testing.T, files map[string]string) string {
	t.Helper()
	const runtimeDepsRegistry = "ghcr.io/super-dolphin/runtime-deps"
	inputs := make(map[string]string, len(productionRuntimeDepsInputPaths()))
	for field, path := range productionRuntimeDepsInputPaths() {
		inputs[field] = productionFixtureDigest(files[path])
	}
	return productionFixtureJSON(t, map[string]any{
		"schema_version": "3", "registry_pull_policy": "anonymous",
		"images": []map[string]any{
			{
				"platform": "linux/amd64", "image_size_bytes": 1,
				"image": map[string]any{
					"registry": runtimeDepsRegistry, "oci_index_digest": productionDigest("1"),
					"platform_manifest_digest": productionDigest("2"), "config_digest": productionDigest("3"),
					"rootfs_diff_ids": []string{productionDigest("4")}, "os": "linux", "architecture": "amd64",
				},
			},
			{
				"platform": "linux/arm64", "image_size_bytes": 2,
				"image": map[string]any{
					"registry": runtimeDepsRegistry, "oci_index_digest": productionDigest("1"),
					"platform_manifest_digest": productionDigest("5"), "config_digest": productionDigest("6"),
					"rootfs_diff_ids": []string{productionDigest("7")}, "os": "linux", "architecture": "arm64",
				},
			},
		},
		"inputs": inputs, "paths": productionRuntimePaths(),
	})
}

func changeProductionBuildInput(t *testing.T, repository string, path string, content string) {
	t.Helper()
	absolute := filepath.Join(repository, filepath.FromSlash(path))
	if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(productionRuntimeDepsInputPaths()))
	for _, inputPath := range productionRuntimeDepsInputPaths() {
		data, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(inputPath)))
		if err != nil {
			t.Fatal(err)
		}
		files[inputPath] = string(data)
	}
	lock := filepath.Join(repository, filepath.FromSlash("build/gate/runtime-deps.lock"))
	if err := os.WriteFile(lock, []byte(productionRuntimeDepsLock(t, files)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func productionRuntimeDepsInputPaths() map[string]string {
	return map[string]string{
		"dockerfile_sha256": "build/gate/runtime-deps.Dockerfile", "toolchain_lock_sha256": "build/gate/toolchain.lock",
		"go_mod_sha256": "go.mod", "go_sum_sha256": "go.sum",
		"nilness_runner_sha256": "internal/devtools/nilnessrunner/runner.go", "nilness_guard_sha256": "scripts/nilness_guard.go",
		"frontend_package_lock_sha256": "frontend-app/package-lock.json", "lsp_package_lock_sha256": "build/gate/runtime-lsp/package-lock.json",
		"proxy_go_mod_sha256": "build/gate/runtime-proxy/go.mod", "proxy_go_sum_sha256": "build/gate/runtime-proxy/go.sum",
		"tools_go_mod_sha256": "build/gate/runtime-tools/go.mod", "tools_go_sum_sha256": "build/gate/runtime-tools/go.sum",
		"manifest_builder_sha256": "build/gate/cmd/runtime-seed-manifest/main.go", "manifest_api_sha256": "internal/devtools/gate/executor_seed.go",
	}
}

func productionRuntimePaths() map[string]string {
	return map[string]string{
		"manifest": "/opt/super-dolphin-gate/runtime/manifest.json", "vendor": "/opt/super-dolphin-gate/runtime/vendor",
		"go_module_proxy": "/opt/super-dolphin-gate/runtime/go-proxy", "frontend_node_modules": "/opt/super-dolphin-gate/runtime/frontend/node_modules",
		"playwright_browsers": "/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright",
		"lsp_node_modules":    "/opt/super-dolphin-gate/runtime/lsp/node_modules", "sqlc": "/opt/super-dolphin-gate/runtime/bin/sqlc",
		"ripgrep": "/opt/super-dolphin-gate/runtime/bin/rg", "sqruff": "/opt/super-dolphin-gate/runtime/bin/sqruff",
		"gopls": "/usr/local/bin/gopls", "go": "/usr/local/go/bin/go", "node": "/usr/local/bin/node",
		"npm": "/usr/local/bin/npm", "git": "/usr/bin/git", "make": "/usr/bin/make",
	}
}

func productionFixtureDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func productionFixtureJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func TestProductionSourceMaterializerUsesGitObjectSnapshot(t *testing.T) {
	fixture := newProductionTestFixture(t)
	candidate := productionGitLine(
		t, fixture.sourceRepo,
		"-c", "user.name=Production Materializer", "-c", "user.email=materializer@example.invalid",
		"commit-tree", fixture.tree, "-p", fixture.commit, "-m", "candidate",
	)
	if err := os.WriteFile(filepath.Join(fixture.sourceRepo, "trusted.txt"), []byte("tampered checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	materializer, result, outputRoot := materializeProductionCandidateSource(t, fixture, candidate)
	assertProductionCandidateSource(t, materializer, result, fixture)
	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(outputRoot); !os.IsNotExist(err) {
		t.Fatalf("output root still exists: %v", err)
	}
}

func materializeProductionCandidateSource(
	t *testing.T,
	fixture productionTestFixture,
	candidate string,
) (*productionSourceMaterializer, materializedJobSource, string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	outputParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outputRoot := filepath.Join(outputParent, "job")
	materializer := &productionSourceMaterializer{gitPath: gitPath}
	source := productionSourceSpec(fixture)
	source.Commit.SHA = candidate
	result, err := materializer.Materialize(context.Background(), sourceMaterializeRequest{
		RepositoryRoot: fixture.sourceRepo, OutputRoot: outputRoot, Source: source,
	})
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	return materializer, result, outputRoot
}

func assertProductionCandidateSource(
	t *testing.T,
	materializer *productionSourceMaterializer,
	result materializedJobSource,
	fixture productionTestFixture,
) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(result.SnapshotDir, "trusted.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "trusted object content\n" {
		t.Fatalf("snapshot content = %q", got)
	}
	if result.SourceTreeSHA != fixture.tree {
		t.Fatalf("SourceTreeSHA = %s, want %s", result.SourceTreeSHA, fixture.tree)
	}
	base, err := materializer.gitLine(
		context.Background(), result.SnapshotDir,
		"rev-parse", "--verify", "--end-of-options", "refs/source/base^{commit}",
	)
	if err != nil {
		t.Fatalf("resolve canonical source base ref: %v", err)
	}
	if base != fixture.commit {
		t.Fatalf("canonical source base = %s, want candidate parent %s", base, fixture.commit)
	}
}

// newProductionTestFixture 组装 production 测试所需的仓库、密钥与权威配置。
func newProductionTestFixture(t *testing.T) productionTestFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, root)
	repository := prepareProductionRepository(t, root)
	authority := prepareProductionAuthority(t, root)
	bootstrapRootFile := filepath.Join(root, "bootstrap-root.json")
	bootstrapControllerFile := writeProductionTestBootstrapController(t, root)
	bootstrapControllerKeyFile := filepath.Join(root, "bootstrap-controller-key.json")
	config := productionCoordinatorConfig{
		AcceptedImageRoot:  makePrivateDirectory(t, root, "accepted"),
		CandidateStateRoot: makePrivateDirectory(t, root, "candidate-state"),
		BootstrapRootFile:  bootstrapRootFile, BootstrapControllerFile: bootstrapControllerFile,
		BootstrapControllerKeyFile: bootstrapControllerKeyFile,
		CandidateBuildRoot:         makePrivateDirectory(t, root, "build"),
		TrustedSourceRoot:          prepareProductionTrustedSourceRoot(t),
		SeccompProfile:             writeProductionSeccompProfile(t, root),
		Platform:                   "linux/arm64", RepoID: "example/repository",
		TrustedRef: "refs/heads/main", TrustedRepository: repository.trustedRepository,
		AcceptedImageSigners: []productionTrustedKey{{
			Signer: authority.signer, PublicKey: base64.StdEncoding.EncodeToString(authority.publicKey),
		}},
		ResultReceiptAuthority: productionResultReceiptAuthorityConfig{
			Signer:         authority.receiptSigner,
			PublicKey:      base64.StdEncoding.EncodeToString(authority.receiptPublicKey),
			PrivateKeyFile: authority.receiptKeyPath,
		},
		ActionGrantAuthority: productionActionGrantAuthorityConfig{
			Signer: authority.grantSigner, PublicKey: base64.StdEncoding.EncodeToString(authority.grantPublicKey),
			PrivateKeyFile: authority.grantKeyPath, TTLSeconds: 60,
		},
		PromotionSigner: productionPromotionKey{
			Signer: authority.signer, PrivateKeyFile: authority.promotionKeyPath,
		},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 20,
	}
	bootstrapRoot, rootTrust, rootPrivateKey := productionBootstrapRootForFixture(
		t, config, repository.commit, repository.tree, authority.signer, authority.publicKey,
	)
	config.AcceptedImageSigners = append(config.AcceptedImageSigners, rootTrust)
	writePrivateJSON(t, bootstrapControllerKeyFile, productionBootstrapControllerPrivateKey{
		Signer: authority.signer, PrivateKey: base64.StdEncoding.EncodeToString(authority.privateKey),
	})
	writeProductionBootstrapRootFixture(t, bootstrapRootFile, bootstrapRoot, rootPrivateKey)
	fixture := productionTestFixture{
		config: config, signer: authority.signer, privateKey: authority.privateKey,
		bootstrapRootKey: rootPrivateKey,
		receiptKey:       authority.receiptPrivateKey, grantKey: authority.grantPrivateKey,
		commit: repository.commit, tree: repository.tree, sourceRepo: repository.sourceRepo,
	}
	baseTree, err := localci.LoadReadOnlyGitTree(
		context.Background(), repository.sourceRepo, productionSourceSpec(fixture),
	)
	if err != nil {
		t.Fatal(err)
	}
	baseInputs, err := localci.ResolveGateImageInputs(baseTree, productionDigest("1"), config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapInputs, err := localci.ResolveGateImageInputs(baseTree, bootstrapRoot.PolicyDigest, config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRoot.ImageInputDigest = bootstrapInputs.ImageInputDigest
	bootstrapRoot.ToolchainDigest = bootstrapInputs.ToolchainDigest
	bootstrapRoot.ImageSchemaVersion = bootstrapInputs.ImageSchemaVersion
	writeProductionBootstrapRootFixture(t, bootstrapRootFile, bootstrapRoot, rootPrivateKey)
	fixture.acceptedInputDigest = baseInputs.ImageInputDigest
	bootstrapAcceptedState(t, fixture)
	fixture.configPath = filepath.Join(root, "production.json")
	writePrivateJSON(t, fixture.configPath, config)
	return fixture
}

func writeProductionTestBootstrapController(t *testing.T, root string) string {
	t.Helper()
	bootstrapControllerFile := filepath.Join(root, "bootstrap-controller")
	if err := os.WriteFile(bootstrapControllerFile, []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bootstrapControllerFile
}
