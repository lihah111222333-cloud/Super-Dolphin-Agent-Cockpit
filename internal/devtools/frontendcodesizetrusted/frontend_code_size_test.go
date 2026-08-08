package frontendcodesizetrusted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestRefreshUsesArchivedTreeAndAtomicallyPublishesBothBaselines 验证刷新不读取工作区前端源码。
func TestRefreshUsesArchivedTreeAndAtomicallyPublishesBothBaselines(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "candidate")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture")
	tree := strings.TrimSpace(runGit(t, root, "write-tree"))
	writeFrontendFixture(t, root, "workspace")
	if err := runWithAssets(context.Background(), root, tree, tree, Refresh, fakeTrustedRuntime()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{productionBaseline, testBaseline} {
		data, err := os.ReadFile(filepath.Join(root, "frontend-app", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "candidate") || strings.Contains(string(data), "workspace") {
			t.Fatalf("%s=%q", name, data)
		}
	}
}

// TestTrustedGitPathUsesGateGitConfiguration 验证前端守卫复用云端 worker 的统一 Git 身份。
func TestTrustedGitPathUsesGateGitConfiguration(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_FRONTEND_CODE_SIZE_GIT", "")
	configured := filepath.Join(t.TempDir(), "missing-git")
	t.Setenv("SUPER_DOLPHIN_GATE_GIT", configured)
	if _, err := trustedGitPath(); err == nil || !strings.Contains(err.Error(), configured) {
		t.Fatalf("trusted Git error = %v, want configured path %q", err, configured)
	}
}

// TestTrustedNodePathUsesGateNodeConfiguration 验证前端守卫复用云端 worker 的统一 Node 身份。
func TestTrustedNodePathUsesGateNodeConfiguration(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_FRONTEND_CODE_SIZE_NODE", "")
	configured := filepath.Join(t.TempDir(), "missing-node")
	t.Setenv("SUPER_DOLPHIN_GATE_NODE", configured)
	if _, err := trustedNodePath(); err == nil || !strings.Contains(err.Error(), configured) {
		t.Fatalf("trusted Node error = %v, want configured path %q", err, configured)
	}
}

// TestCheckLeavesRepositoryBaselinesUntouched 验证 check 不发布候选树的基线。
func TestCheckLeavesRepositoryBaselinesUntouched(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "candidate")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture")
	tree := strings.TrimSpace(runGit(t, root, "write-tree"))
	writeFrontendFixture(t, root, "workspace")
	if err := runWithAssets(context.Background(), root, tree, tree, Check, fakeTrustedRuntime()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "frontend-app", productionBaseline))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "workspace") {
		t.Fatalf("check changed workspace baseline: %q", data)
	}
}

// TestCheckReplaysAcceptedBaselineAndRequiresExactCandidatePair 验证 check 既接受规范刷新结果，也拒绝候选自行放宽基线。
func TestCheckReplaysAcceptedBaselineAndRequiresExactCandidatePair(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "accepted")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "accepted")
	accepted := strings.TrimSpace(runGit(t, root, "write-tree"))

	for _, name := range []string{productionBaseline, testBaseline} {
		if err := os.WriteFile(filepath.Join(root, "frontend-app", name), []byte(`{"marker":"canonical"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, root, "add", "frontend-app/"+productionBaseline, "frontend-app/"+testBaseline)
	candidate := strings.TrimSpace(runGit(t, root, "write-tree"))
	if err := runWithAssets(context.Background(), root, candidate, accepted, Check, canonicalBaselineRuntime()); err != nil {
		t.Fatalf("canonical candidate check failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "frontend-app", productionBaseline), []byte(`{"marker":"inflated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "frontend-app/"+productionBaseline)
	tampered := strings.TrimSpace(runGit(t, root, "write-tree"))
	if err := runWithAssets(context.Background(), root, tampered, accepted, Check, canonicalBaselineRuntime()); err == nil || !strings.Contains(err.Error(), "accepted-baseline replay") {
		t.Fatalf("tampered candidate error = %v", err)
	}
}

// TestCheckSharesNodeModulesAcrossGitWorktrees 验证孤立工作树复用 common dir 下的唯一依赖缓存。
func TestCheckSharesNodeModulesAcrossGitWorktrees(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "candidate")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture")
	tree := strings.TrimSpace(runGit(t, root, "write-tree"))
	worktree := filepath.Join(t.TempDir(), "isolated-worktree")
	runGit(t, root, "worktree", "add", "--detach", worktree, "HEAD")
	t.Cleanup(func() {
		runGit(t, root, "worktree", "remove", "--force", worktree)
	})

	if _, err := os.Lstat(filepath.Join(worktree, "frontend-app", "node_modules")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated worktree unexpectedly has node_modules: %v", err)
	}
	first, err := runWithAssetsReceipt(context.Background(), root, tree, tree, Check, fakeTrustedRuntime())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runWithAssetsReceipt(context.Background(), worktree, tree, tree, Check, fakeTrustedRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if first.IdentitySHA256 != second.IdentitySHA256 {
		t.Fatalf("execution closure identities differ: %s != %s", first.IdentitySHA256, second.IdentitySHA256)
	}
}

// TestTrustedRuntimeAssetsMatchCanonicalSources 验证内嵌裁决代码与规范源码一致。
func TestTrustedRuntimeAssetsMatchCanonicalSources(t *testing.T) {
	t.Parallel()
	repository := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, assetPath := range []string{
		"scripts/frontend-code-size-guard.mjs",
		"scripts/lib/frontend-code-size-cli.mjs",
		"scripts/lib/frontend-code-size-baseline.mjs",
		"scripts/lib/frontend-code-size-baseline-transaction.mjs",
		"scripts/lib/frontend-code-size-guard-runner.mjs",
	} {
		embedded, err := trustedRuntimeAssets.ReadFile("assets/" + assetPath)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := os.ReadFile(filepath.Join(repository, "frontend-app", assetPath))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(embedded, canonical) {
			t.Fatalf("trusted runtime asset drifted from frontend-app/%s", assetPath)
		}
	}
}

// TestAcceptedParserClosureManifestMatchesRepository verifies the checked-in generator output is canonical Go input.
func TestAcceptedParserClosureManifestMatchesRepository(t *testing.T) {
	t.Parallel()
	repository := filepath.Clean(filepath.Join("..", "..", ".."))
	lock, err := os.ReadFile(filepath.Join(repository, "frontend-app", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repository, closureManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile(filepath.Join(repository, closureGeneratorPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseClosureManifest(data, lock, generator); err != nil {
		t.Fatal(err)
	}
}

func TestParseClosureManifestRejectsUnsafePackageAndTrailingJSON(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "fixture")
	app := filepath.Join(root, "frontend-app")
	lock, err := os.ReadFile(filepath.Join(app, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile(filepath.Join(app, "scripts", "lib", "generate-frontend-code-size-dependency-closure.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(app, "scripts", "lib", "frontend-code-size-dependency-closure.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseClosureManifest(append(append([]byte(nil), data...), []byte("\n{}")...), lock, generator); err == nil {
		t.Fatal("parser closure manifest accepted trailing JSON")
	}
	var manifest closureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Packages = []string{"../outside", "node_modules/@babel/parser"}
	canonical, err := canonicalClosureManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ClosureSHA256 = sha256Bytes(canonical)
	unsafe, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseClosureManifest(unsafe, lock, generator); err == nil {
		t.Fatal("parser closure manifest accepted a package outside node_modules")
	}
}

// TestVerifyParserClosureRejectsContentAndExtraFiles proves the seed is not trusted by location alone.
func TestVerifyParserClosureRejectsContentAndExtraFiles(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "fixture")
	app := filepath.Join(root, "frontend-app")
	lock, err := os.ReadFile(filepath.Join(app, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(app, "scripts", "lib", "frontend-code-size-dependency-closure.json"))
	if err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile(filepath.Join(app, "scripts", "lib", "generate-frontend-code-size-dependency-closure.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := parseClosureManifest(data, lock, generator)
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(app, "node_modules")
	if err := verifyParserClosure(seed, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "@babel", "parser", "extra.js"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyParserClosure(seed, manifest); err == nil {
		t.Fatal("extra parser closure file was accepted")
	}
	if err := os.Remove(filepath.Join(seed, "@babel", "parser", "extra.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "@babel", "parser", "index.js"), []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyParserClosure(seed, manifest); err == nil {
		t.Fatal("parser closure content drift was accepted")
	}
}

// TestMigrateAcceptsOnlyCandidateBoundClosure proves a dependency migration is verified against its staged tree.
func TestMigrateAcceptsOnlyCandidateBoundClosure(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "accepted")
	app := filepath.Join(root, "frontend-app")
	manifestPath := filepath.Join(app, "scripts", "lib", "frontend-code-size-dependency-closure.json")
	generatorPath := filepath.Join(app, "scripts", "lib", "generate-frontend-code-size-dependency-closure.mjs")
	generator, err := os.ReadFile(generatorPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(generatorPath); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture")
	accepted := strings.TrimSpace(runGit(t, root, "write-tree"))
	if err := os.WriteFile(generatorPath, generator, 0o644); err != nil {
		t.Fatal(err)
	}
	lock := []byte(`{"name":"fixture-migration","lockfileVersion":3,"packages":{"":{"name":"fixture"},"node_modules/@babel/parser":{"name":"@babel/parser","version":"1.0.1"}}}`)
	if err := os.WriteFile(filepath.Join(app, "package-lock.json"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFixtureClosureManifest(t, app, lock)
	runGit(t, root, "add", "frontend-app/package-lock.json", "frontend-app/scripts/lib/frontend-code-size-dependency-closure.json", "frontend-app/scripts/lib/generate-frontend-code-size-dependency-closure.mjs")
	candidate := strings.TrimSpace(runGit(t, root, "write-tree"))
	if _, err := runWithAssetsReceipt(context.Background(), root, candidate, accepted, Check, fakeTrustedRuntime()); err == nil {
		t.Fatal("normal check accepted a candidate package-lock migration")
	}
	if _, err := runWithAssetsReceipt(context.Background(), root, candidate, accepted, Migrate, fakeTrustedRuntime()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "scripts", "lib", "frontend-code-size-dependency-closure.json"), []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "frontend-app/scripts/lib/frontend-code-size-dependency-closure.json")
	malicious := strings.TrimSpace(runGit(t, root, "write-tree"))
	if _, err := runWithAssetsReceipt(context.Background(), root, malicious, accepted, Migrate, fakeTrustedRuntime()); err == nil {
		t.Fatal("migrate accepted a drifting manifest")
	}
}

// TestCheckRejectsCandidateManifestOrGeneratorDrift prevents deferred closure failures after a normal commit.
func TestCheckRejectsCandidateManifestOrGeneratorDrift(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "accepted")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture")
	accepted := strings.TrimSpace(runGit(t, root, "write-tree"))
	closureDir := filepath.Join(root, "frontend-app", "scripts", "lib")
	if err := os.WriteFile(filepath.Join(closureDir, "frontend-code-size-dependency-closure.json"), []byte(`{"schemaVersion":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "frontend-app/scripts/lib/frontend-code-size-dependency-closure.json")
	manifestOnly := strings.TrimSpace(runGit(t, root, "write-tree"))
	if _, err := runWithAssetsReceipt(context.Background(), root, manifestOnly, accepted, Check, fakeTrustedRuntime()); err == nil {
		t.Fatal("normal check accepted manifest-only drift")
	}
	runGit(t, root, "reset", "--", "frontend-app/scripts/lib/frontend-code-size-dependency-closure.json")
	runGit(t, root, "checkout", "--", "frontend-app/scripts/lib/frontend-code-size-dependency-closure.json")
	if err := os.WriteFile(filepath.Join(closureDir, "generate-frontend-code-size-dependency-closure.mjs"), []byte("export const drift = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "frontend-app/scripts/lib/generate-frontend-code-size-dependency-closure.mjs")
	generatorOnly := strings.TrimSpace(runGit(t, root, "write-tree"))
	if _, err := runWithAssetsReceipt(context.Background(), root, generatorOnly, accepted, Check, fakeTrustedRuntime()); err == nil {
		t.Fatal("normal check accepted generator-only drift")
	}
}

// TestMaterializedParserClosureIsPrivate proves seed writes cannot alter the executable closure after copy.
func TestMaterializedParserClosureIsPrivate(t *testing.T) {
	root := t.TempDir()
	writeFrontendFixture(t, root, "fixture")
	app := filepath.Join(root, "frontend-app")
	lock, err := os.ReadFile(filepath.Join(app, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(app, "scripts", "lib", "frontend-code-size-dependency-closure.json"))
	if err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile(filepath.Join(app, "scripts", "lib", "generate-frontend-code-size-dependency-closure.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := parseClosureManifest(data, lock, generator)
	if err != nil {
		t.Fatal(err)
	}
	runtime, digest, err := materializeTrustedRuntime(filepath.Join(app, "node_modules"), manifest, fakeTrustedRuntime())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	privateParser := filepath.Join(runtime, "node_modules", "@babel", "parser", "index.js")
	original, err := os.ReadFile(privateParser)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "node_modules", "@babel", "parser", "index.js"), []byte("seed drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(privateParser)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("private closure changed after seed drift: %q %v", actual, err)
	}
	if digest != manifest.ClosureSHA256 {
		t.Fatalf("private digest=%s manifest=%s", digest, manifest.ClosureSHA256)
	}
}

// TestTrustedNodePathIgnoresPATH verifies the migration generator cannot be redirected by PATH.
func TestTrustedNodePathIgnoresPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_FRONTEND_CODE_SIZE_NODE", "")
	node, err := TrustedNodePath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(node) {
		t.Fatalf("trusted node path is not absolute: %q", node)
	}
}

// TestRunRejectsAbbreviatedTree 验证候选树必须是完整对象标识。
func TestRunRejectsAbbreviatedTree(t *testing.T) {
	err := Run(context.Background(), t.TempDir(), "1234567", Check)
	var classifiedError *Error
	if !strings.Contains(err.Error(), "exact") || !errors.As(err, &classifiedError) || classifiedError.Kind != ErrorInput {
		t.Fatalf("err=%v", err)
	}
}

// writeFrontendFixture 创建可由 node 执行的最小 canonical guard 夹具。
func writeFrontendFixture(t *testing.T, root, marker string) {
	t.Helper()
	app := filepath.Join(root, "frontend-app")
	if err := os.MkdirAll(filepath.Join(app, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	parser := filepath.Join(app, "node_modules", "@babel", "parser")
	if err := os.MkdirAll(parser, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parser, "index.js"), []byte("export const parse = () => {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := []byte(`{"name":"fixture","lockfileVersion":3,"packages":{"":{"name":"fixture"},"node_modules/@babel/parser":{"name":"@babel/parser","version":"1.0.0"}}}`)
	if err := os.WriteFile(filepath.Join(app, "package-lock.json"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFixtureClosureManifest(t, app, lock)
	if err := os.MkdirAll(filepath.Join(app, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("frontend-app/node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `process.exit(42);`
	if err := os.WriteFile(filepath.Join(app, "scripts", "frontend-code-size-guard.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{productionBaseline, testBaseline} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(`{"marker":"`+marker+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFixtureClosureManifest(t *testing.T, app string, lock []byte) {
	t.Helper()
	closureDir := filepath.Join(app, "scripts", "lib")
	if err := os.MkdirAll(closureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	generator := []byte("export {};\n")
	if err := os.WriteFile(filepath.Join(closureDir, "generate-frontend-code-size-dependency-closure.mjs"), generator, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := closureManifest{SchemaVersion: 2, Generator: closureGeneratorPath, GeneratorSHA256: sha256Bytes(generator), PackageLockSHA256: sha256Bytes(lock), RootPackage: "@babel/parser", Packages: []string{"node_modules/@babel/parser"}, Files: []closureFile{{Path: "node_modules/@babel/parser/index.js", Mode: 0o644, SHA256: sha256Bytes([]byte("export const parse = () => {};\n"))}}}
	canonical, err := canonicalClosureManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ClosureSHA256 = sha256Bytes(canonical)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(closureDir, "frontend-code-size-dependency-closure.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeTrustedRuntime() fstest.MapFS {
	return fstest.MapFS{
		"assets/scripts/frontend-code-size-guard.mjs": {
			Data: []byte(`import fs from 'node:fs'; import path from 'node:path'; import process from 'node:process'; const root=process.env.SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT; const scope=process.argv[process.argv.indexOf('--scope')+1]; const target=path.join(root, scope === 'production' ? '.frontend_code_size_guard_baseline.json' : '.frontend_code_size_guard_baseline_test.json'); JSON.parse(fs.readFileSync(target, 'utf8')); if(process.argv.includes('--update')) fs.writeFileSync(target, fs.readFileSync(target));`),
			Mode: 0o644,
		},
		"assets/scripts/lib/frontend-code-size-cli.mjs": {
			Data: []byte(`import fs from 'node:fs'; import path from 'node:path'; import process from 'node:process'; const root=process.env.SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT; const scope=process.argv[process.argv.indexOf('--scope')+1]; const target=path.join(root, scope === 'production' ? '.frontend_code_size_guard_baseline.json' : '.frontend_code_size_guard_baseline_test.json'); JSON.parse(fs.readFileSync(target, 'utf8')); if(process.argv.includes('--update')) fs.writeFileSync(target, fs.readFileSync(target));`),
			Mode: 0o644,
		},
	}
}

func canonicalBaselineRuntime() fstest.MapFS {
	script := []byte(`import fs from 'node:fs'; import path from 'node:path'; const root=process.env.SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT; const names=['.frontend_code_size_guard_baseline.json','.frontend_code_size_guard_baseline_test.json']; if(process.argv.includes('--update')) for(const name of names) fs.writeFileSync(path.join(root,name),'{"marker":"canonical"}'); for(const name of names) { const value=JSON.parse(fs.readFileSync(path.join(root,name),'utf8')); if(value.marker !== 'canonical') process.exit(1); }`)
	return fstest.MapFS{
		"assets/scripts/frontend-code-size-guard.mjs":   {Data: script, Mode: 0o644},
		"assets/scripts/lib/frontend-code-size-cli.mjs": {Data: script, Mode: 0o644},
	}
}

// runGit 运行测试夹具所需的 Git 命令。
func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, data)
	}
	return string(data)
}
