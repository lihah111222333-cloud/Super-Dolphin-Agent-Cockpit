package remotecicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
				t.Fatalf("Run(%q) unexpectedly succeeded", tc.args)
			}
			if stdout != "" {
				t.Fatalf("Run(%q) wrote stdout on rejection: %q", tc.args, stdout)
			}
			if stderr == "" {
				t.Fatalf("Run(%q) omitted a bounded diagnostic", tc.args)
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
	if code != 0 || !strings.Contains(stdout, `"binding_sha256":"sha256:`) ||
		!strings.Contains(stdout, `"repository_id":"fixture"`) || !strings.Contains(stdout, `"status":"NOT_VERIFIED"`) {
		t.Fatalf("validate result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, forbidden := range []string{"repository_root", "config_path", "ledger_path"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("validate output leaked %q: %q", forbidden, stdout)
		}
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
	manifest := portableCLIManifest(t)
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
	input := portableCLIFixtureInput(t, repository, commit, tree, manifest, configPath, configDigest, ledgerPath, ledgerIdentity)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(t.TempDir(), "descriptor.json")
	mustWriteTestFile(t, descriptorPath, raw)
	return descriptorPath
}

// portableCLIManifest 生成 target tree 所拥有的 strict workload manifest。
func portableCLIManifest(t *testing.T) []byte {
	t.Helper()
	manifest := remoteci.RepositoryWorkloadManifest{
		SchemaVersion:         remoteci.PortableWorkloadManifestSchemaVersion,
		RepositoryID:          "fixture",
		Modules:               []remoteci.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/fixture"}},
		ExcludedNestedModules: []string{},
		Profiles:              []remoteci.RepositoryWorkloadProfile{{Profile: "local-fast", RequiredWorkloadIDs: []string{"go-test/example"}}},
		Workloads: []remoteci.RepositoryWorkload{{
			ID: "go-test/example", Kind: remoteci.RepositoryWorkloadKindGoTest, ModuleRoot: ".",
			Package: "./example", Name: "TestExample", Platform: remoteci.RepositoryWorkloadPlatformLinuxAMD64,
			Runtime:             remoteci.RepositoryWorkloadRuntimeUbuntuContainer,
			BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: remoteci.RepositoryWorkloadExecutionPolicyNormal,
		}},
	}
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
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
func portableCLIFixtureInput(t *testing.T, repository, commit, tree string, manifest []byte, configPath, configDigest, ledgerPath, ledgerIdentity string) remoteci.PortableRunInput {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	abiDigest, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatalf("derive workload manifest ABI digest: %v", err)
	}
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
		WorkloadManifest: remoteci.PortableWorkloadManifest{RepoRelativePath: "ci/workloads.json", SHA256: digestBytes(manifest), ABIDigest: abiDigest,
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
		t.Fatalf("dispatchCLI should leave rendering to Run, wrote stderr: %q", stderr.String())
	}
}

// TestRetiredCommandsDoNotCreateExecutionPaths 确认旧命令不会获得兼容入口。
func TestRetiredCommandsDoNotCreateExecutionPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		args    []string
	}{
		{command: "plan", args: []string{"plan"}},
		{command: "run", args: []string{"run", "--config", "/tmp/config", "--repository", "/tmp/repository", "--run-id", "retired-run"}},
		{command: "route", args: []string{"route", "--config", "/tmp/config", "--repository", "/tmp/repository"}},
		{command: "status", args: []string{"status"}},
		{command: "worker", args: []string{"worker"}},
		{command: "remote", args: []string{"remote"}},
	}
	for _, testCase := range cases {
		code, stdout, stderr := executeCLI(testCase.args)
		if code != 1 || stdout != "" || stderr != "super-dolphin-remote-ci: unknown command\n" {
			t.Fatalf("command %q result = code %d stdout %q stderr %q", testCase.command, code, stdout, stderr)
		}
	}
}

// TestRemoteCIPublicDispatchASTGuard 静态锁定公开分派不会重新接入本地 workflow。
func TestRemoteCIPublicDispatchASTGuard(t *testing.T) {
	t.Parallel()

	cases := parseRemoteCIDispatchCases(t)
	assertRemoteCIDispatchAllowlist(t, cases)
	assertExecuteUpgradeSentinelCase(t, cases["execute"])
	assertRemoteCIUsage(t)
}

// assertRemoteCIDispatchAllowlist 拒绝未知或缺失的生产命令 case。
func assertRemoteCIDispatchAllowlist(t *testing.T, cases map[string]*ast.CaseClause) {
	t.Helper()

	want := map[string]bool{"validate": true, "_artifact-build": true, "_artifact-identity": true, "builder-simulate": true, "execute": true}
	if len(cases) != len(want) {
		t.Fatalf("dispatch command count = %d, want %d: %v", len(cases), len(want), cases)
	}
	for command := range cases {
		if !want[command] {
			t.Fatalf("dispatch exposes forbidden command %q", command)
		}
	}
}

// assertRemoteCIUsage 锁定 usage 只宣传获准的公开命令。
func assertRemoteCIUsage(t *testing.T) {
	t.Helper()

	for _, retired := range []string{" route ", " run "} {
		if strings.Contains(usageText, retired) {
			t.Fatalf("usageText advertises retired local command %q: %q", retired, usageText)
		}
	}
	for _, public := range []string{" validate ", " builder-simulate "} {
		if !strings.Contains(" "+usageText, public) {
			t.Fatalf("usageText is missing public command %q: %q", public, usageText)
		}
	}
}

// parseRemoteCIDispatchCases 从生产 switch 动态枚举全部命令 case。
func parseRemoteCIDispatchCases(t *testing.T) map[string]*ast.CaseClause {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "portable_validate.go", nil, 0)
	if err != nil {
		t.Fatalf("parse portable_validate.go: %v", err)
	}
	function := findRemoteCIDispatchFunction(t, parsed)
	cases := make(map[string]*ast.CaseClause)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok || len(clause.List) == 0 {
			return true
		}
		recordRemoteCIDispatchClause(t, cases, clause)
		return false
	})
	return cases
}

// findRemoteCIDispatchFunction 定位生产 dispatchCLI 声明。
func findRemoteCIDispatchFunction(t *testing.T, parsed *ast.File) *ast.FuncDecl {
	t.Helper()

	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if function.Name.Name == "dispatchCLI" {
			return function
		}
	}
	t.Fatal("dispatchCLI declaration is missing")
	return nil
}

// recordRemoteCIDispatchClause 登记一个 case 的全部字符串命令并拒绝歧义。
func recordRemoteCIDispatchClause(t *testing.T, cases map[string]*ast.CaseClause, clause *ast.CaseClause) {
	t.Helper()

	for _, expression := range clause.List {
		literal, ok := expression.(*ast.BasicLit)
		if !ok {
			t.Fatalf("dispatch case is not a string literal: %T", expression)
		}
		if literal.Kind != token.STRING {
			t.Fatalf("dispatch case literal kind = %s, want string", literal.Kind)
		}
		command, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatalf("unquote dispatch command: %v", err)
		}
		if cases[command] != nil {
			t.Fatalf("dispatch command %q is duplicated", command)
		}
		cases[command] = clause
	}
}

// assertExecuteUpgradeSentinelCase 锁定 execute 只能直接返回升级哨兵。
func assertExecuteUpgradeSentinelCase(t *testing.T, clause *ast.CaseClause) {
	t.Helper()

	if clause == nil || len(clause.Body) != 1 {
		t.Fatalf("execute case body is not one direct return: %#v", clause)
	}
	statement, ok := clause.Body[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		t.Fatalf("execute case is not one direct return: %T", clause.Body[0])
	}
	identifier, ok := statement.Results[0].(*ast.Ident)
	if !ok || identifier.Name != "ErrExecutionContractUpgradeRequired" {
		t.Fatalf("execute case returns %T %v, want upgrade sentinel", statement.Results[0], statement.Results[0])
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
	code := Run(args, stdout, stderr)
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

// TestArtifactBuildDispatchIsStrict 锁定内部 builder 子命令的 fail-fast 分派。
func TestArtifactBuildDispatchIsStrict(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeCLI([]string{"_artifact-build"})
	if code == 0 || stdout != "" || stderr == "" {
		t.Fatalf("artifact-build dispatch result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// TestArtifactIdentityDispatchIsStrict locks the internal observed identity command.
func TestArtifactIdentityDispatchIsStrict(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeCLI([]string{"_artifact-identity", "--unexpected"})
	if code == 0 || stdout != "" || stderr == "" {
		t.Fatalf("artifact-identity unknown flag result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
