package main

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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestValidateRequiresExplicitDescriptor 锁定 validate 的显式绝对 descriptor 边界。
func TestValidateRequiresExplicitDescriptor(t *testing.T) {
	t.Parallel()

	descriptorPath := filepath.Join(t.TempDir(), "descriptor.json")
	if err := os.WriteFile(descriptorPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nonCleanPath := filepath.Dir(descriptorPath) + string(os.PathSeparator) + "." + string(os.PathSeparator) + "descriptor.json"
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing command", args: nil},
		{name: "missing descriptor flag", args: []string{"validate"}},
		{name: "missing descriptor value", args: []string{"validate", "--descriptor"}},
		{name: "relative descriptor", args: []string{"validate", "--descriptor", "descriptor.json"}},
		{name: "non-clean descriptor", args: []string{"validate", "--descriptor", nonCleanPath}},
		{name: "extra argument", args: []string{"validate", "--descriptor", descriptorPath, "unexpected"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := executeCLI(tc.args)
			if code == 0 {
				t.Fatalf("runCLI(%q) unexpectedly succeeded", tc.args)
			}
			if stdout != "" {
				t.Fatalf("runCLI(%q) wrote stdout on rejection: %q", tc.args, stdout)
			}
			if stderr == "" {
				t.Fatalf("runCLI(%q) omitted a bounded diagnostic", tc.args)
			}
		})
	}
}

// TestValidateRejectsSymlinkDescriptor 确认 descriptor 文件自身不能是符号链接。
func TestValidateRejectsSymlinkDescriptor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	link := filepath.Join(root, "descriptor.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	code, stdout, stderr := executeCLI([]string{"validate", "--descriptor", link})
	if code == 0 {
		t.Fatal("validate accepted a symbolic-link descriptor")
	}
	if stdout != "" {
		t.Fatalf("validate wrote stdout for a symbolic-link descriptor: %q", stdout)
	}
	if strings.Contains(stderr, string(mustReadFile(t, target))) {
		t.Fatalf("diagnostic leaked descriptor contents: %q", stderr)
	}
}

// TestValidateRejectsDirectoryDescriptor 确认 descriptor 必须是 regular file。
func TestValidateRejectsDirectoryDescriptor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeCLI([]string{"validate", "--descriptor", t.TempDir()})
	if code == 0 || stdout != "" || stderr == "" {
		t.Fatalf("directory descriptor result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// TestValidateBindsExactRepositoryObjects 通过真实 Git ODB 验证独立 composition root 的成功路径。
func TestValidateBindsExactRepositoryObjects(t *testing.T) {
	t.Parallel()

	descriptorPath := portableCLIRealDescriptor(t)
	code, stdout, stderr := executeCLI([]string{"validate", "--descriptor", descriptorPath})
	if code != 0 || !strings.Contains(stdout, `"binding_sha256":"sha256:`) {
		t.Fatalf("validate result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	assertPortableProgressStages(t, stderr)
}

// portableCLIRealDescriptor 建立真实 Git ODB 与 authority 文件并返回 canonical descriptor。
func portableCLIRealDescriptor(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGitFixture(t, repository, "init")
	runGitFixture(t, repository, "config", "user.email", "portable@example.invalid")
	runGitFixture(t, repository, "config", "user.name", "Portable Test")
	manifestPath := filepath.Join(repository, "ci", "workloads.json")
	mustMkdirAll(t, filepath.Dir(manifestPath))
	manifest := []byte(`{"schema_version":"portable-workload-manifest/v1","repository_id":"fixture"}`)
	mustWriteTestFile(t, manifestPath, manifest)
	runGitFixture(t, repository, "add", "ci/workloads.json")
	runGitFixture(t, repository, "commit", "-m", "测试 portable manifest")
	commit := runGitFixture(t, repository, "rev-parse", "HEAD")
	tree := runGitFixture(t, repository, "rev-parse", "HEAD^{tree}")

	configPath := filepath.Join(t.TempDir(), "config.json")
	ledgerPath := filepath.Join(t.TempDir(), "ledger.sqlite")
	mustWriteTestFile(t, configPath, []byte(`{"schema":1}`))
	mustWriteTestFile(t, ledgerPath, []byte("sqlite-fixture"))
	reader := fileIdentityReader{}
	ledgerIdentity := mustFileIdentity(t, reader, ledgerPath)
	configDigest := mustFileDigest(t, reader, configPath)
	input := portableCLIFixtureInput(repository, commit, tree, manifest, configPath, configDigest, ledgerPath, ledgerIdentity)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(t.TempDir(), "descriptor.json")
	mustWriteTestFile(t, descriptorPath, raw)
	return descriptorPath
}

// assertPortableProgressStages 验证 validate 的 bounded stage 日志完整可观察。
func assertPortableProgressStages(t *testing.T, stderr string) {
	t.Helper()
	for _, stage := range []string{"descriptor path accepted", "descriptor decoded", "portable run bound"} {
		if !strings.Contains(stderr, stage) {
			t.Fatalf("validate log omitted stage %q: %q", stage, stderr)
		}
	}
}

// mustMkdirAll 创建测试目录，失败时终止当前测试。
func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// mustWriteTestFile 写入测试 regular file，失败时终止当前测试。
func mustWriteTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// mustFileIdentity 读取测试 ledger 身份，失败时终止当前测试。
func mustFileIdentity(t *testing.T, reader fileIdentityReader, path string) string {
	t.Helper()
	identity, err := reader.ReadFileIdentity(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// mustFileDigest 读取测试 config 摘要，失败时终止当前测试。
func mustFileDigest(t *testing.T, reader fileIdentityReader, path string) string {
	t.Helper()
	digest, err := reader.ReadFileSHA256(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// portableCLIFixtureInput 构造只用于真实适配器测试的完整 validate-only 描述符。
func portableCLIFixtureInput(repository, commit, tree string, manifest []byte, configPath, configDigest, ledgerPath, ledgerIdentity string) remoteci.PortableRunInput {
	digest := "sha256:" + strings.Repeat("a", 64)
	return remoteci.PortableRunInput{
		ExecutionProvider: remoteci.PortableExecutionProvider,
		ContractID:        remoteci.PortableRunContractID,
		Mode:              remoteci.PortableRunModeValidateOnly,
		Engine: remoteci.PortableRunEngine{RepositoryRoot: repository, CommitSHA: commit, TreeSHA: tree,
			ProtocolABIDigest: digest, RuntimeArtifactSHA256: digest, ToolchainSHA256: digest},
		Target: remoteci.PortableRunTarget{RepositoryRoot: repository, RepositoryID: "fixture", CommitSHA: commit, TreeSHA: tree,
			BaseCommitSHA: commit, ObjectFormat: "sha1", SourceBaseline: remoteci.PortableSourceBaseline{
				ObjectFormat: "sha1", BaselineTreeSHA: tree, BaselineCommitSHA: commit,
				BundleContractID: remoteci.PortableBundleContractID, ArtifactKind: "source-baseline",
				ArtifactKey: "fixture/baseline", ArtifactSHA256: digest, ArtifactSizeBytes: 1,
			}},
		WorkloadManifest: remoteci.PortableWorkloadManifest{RepoRelativePath: "ci/workloads.json", SHA256: digestBytes(manifest),
			SchemaVersion: remoteci.PortableWorkloadManifestSchemaVersion, RepositoryID: "fixture"},
		Authority: remoteci.PortableRunAuthority{ConfigPath: configPath, ConfigSHA256: configDigest,
			LedgerPath: ledgerPath, LedgerFileIdentity: ledgerIdentity},
	}
}

// runGitFixture 执行测试仓库内的只读或 fixture Git 命令并返回单行输出。
func runGitFixture(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// TestExecuteRequiresContractUpgrade 锁定 execute 的 typed fail-fast 语义。
func TestExecuteRequiresContractUpgrade(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	err := dispatchCLI([]string{"execute"}, stdout, stderr)
	if !errors.Is(err, ErrExecutionContractUpgradeRequired) {
		t.Fatalf("dispatchCLI(execute) error = %v, want ErrExecutionContractUpgradeRequired", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("execute wrote stdout: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("dispatchCLI should leave rendering to runCLI, wrote stderr: %q", stderr.String())
	}
}

// TestRetiredCommandsDoNotCreateExecutionPaths 确认旧命令不会获得兼容入口。
func TestRetiredCommandsDoNotCreateExecutionPaths(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"plan", "run", "status", "worker", "remote"} {
		code, stdout, stderr := executeCLI([]string{command})
		if code == 0 || stdout != "" || stderr == "" {
			t.Fatalf("command %q result = code %d stdout %q stderr %q", command, code, stdout, stderr)
		}
	}
}

// TestCompositionRootDoesNotImportRetiredGateBinary 检查独立 binary 的静态依赖边界。
func TestCompositionRootDoesNotImportRetiredGateBinary(t *testing.T) {
	t.Parallel()

	source := string(mustReadFile(t, "main.go"))
	for _, forbidden := range []string{
		"cmd/super-dolphin-gate",
		"syscall.Exec",
		"reexec",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("main.go contains forbidden composition-root dependency %q", forbidden)
		}
	}
}

// TestParseGitTreeEntryRejectsNonRegularOrAmbiguousEntries 锁定 exact-tree manifest 的 mode 边界。
func TestParseGitTreeEntryRejectsNonRegularOrAmbiguousEntries(t *testing.T) {
	t.Parallel()

	path := "ci/workloads.json"
	mode, objectType, err := parseGitTreeEntry([]byte("100644 blob 0123456789012345678901234567890123456789\t"+path+"\x00"), path)
	if err != nil || mode != "100644" || objectType != "blob" {
		t.Fatalf("parseGitTreeEntry() = %q, %q, %v", mode, objectType, err)
	}
	for _, raw := range [][]byte{
		[]byte("120000 blob 0123456789012345678901234567890123456789\t" + path),
		[]byte("100644 blob 0123456789012345678901234567890123456789\tother\x00"),
		[]byte("100644 blob a\t" + path + "\x00extra\x00"),
	} {
		parsedMode, parsedType, parseErr := parseGitTreeEntry(raw, path)
		if parseErr == nil && parsedMode == "100644" && parsedType == "blob" {
			t.Fatalf("parseGitTreeEntry() accepted ambiguous entry %q", raw)
		}
	}
}

// TestValidateAbsolutePathComponentsRejectsParentSymlink 确认 host authority 不跟随父目录别名。
func TestValidateAbsolutePathComponentsRejectsParentSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	filePath := filepath.Join(realDir, "authority.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateAbsolutePathComponents(filepath.Join(linkDir, "authority.json"), false); err == nil {
		t.Fatal("validateAbsolutePathComponents() accepted a parent symlink")
	}
}

// executeCLI 执行测试用的 CLI 并返回标准化输出。
func executeCLI(args []string) (int, string, string) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runCLI(args, stdout, stderr)
	return code, stdout.String(), stderr.String()
}

// mustReadFile 读取测试 fixture，失败时立即终止当前测试。
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
