package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIAgentTokenContractHasOneOwner 确保生产链不保留令牌词汇。
// 原始令牌仅是 CLI 内存秘密；所有持久化和跨进程身份均为 cicontract 持有的摘要。
func TestRemoteCIAgentTokenContractHasOneOwner(t *testing.T) {
	root := findRepoRoot(t)
	assertRemoteCIAgentTokenGateHasNoOwner(t, root)
	assertRemoteCIAgentTokenCanonicalHelpers(t, root)
	document := readRemoteCIAgentTokenContract(t, root)
	assertRemoteCIAgentTokenContractDocumentsBoundary(t, document)
	assertRemoteCIAgentTokenDigestBoundary(t)
	assertRemoteCIAgentTokenRequestPhases(t)
	assertRemoteCIAgentTokenApplicationGuidance(t)
}

// assertRemoteCIAgentTokenCanonicalHelpers 确保 CLI 只拥有 wire adapter，阶段语义仍由 cicontract 产生。
func assertRemoteCIAgentTokenCanonicalHelpers(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "cmd", "super-dolphin-gate", "remote_agent_token.go")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, required := range []string{
		"cicontract.ClassifyAgentTokenRequest(",
		"cicontract.IssueAgentTokenBootstrap()",
		"cicontract.AgentTokenApplicationResponse()",
		"cicontract.ValidateGitHookAgentToken(",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("CLI token handshake must consume canonical helper %q", required)
		}
	}
	if strings.Contains(text, "cicontract.GenerateAgentToken()") {
		t.Error("CLI token handshake must not generate a second token owner")
	}
}

// assertRemoteCIAgentTokenGateHasNoOwner 确认 gate 不持有第二个 token owner。
func assertRemoteCIAgentTokenGateHasNoOwner(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "internal", "devtools", "gate", "agent_token.go")); !os.IsNotExist(err) {
		t.Fatal("gate must not retain a second agent-token owner; consume cicontract instead")
	}
}

// readRemoteCIAgentTokenContract 读取已接受的 token 契约正文。
func readRemoteCIAgentTokenContract(t *testing.T, root string) string {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cicontract.DocumentPath)))
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

// assertRemoteCIAgentTokenContractDocumentsBoundary 保留契约词汇的正向守卫。
func assertRemoteCIAgentTokenContractDocumentsBoundary(t *testing.T, document string) {
	t.Helper()
	for _, want := range []string{
		"--agent-token",
		"--agent-token=issue",
		"SUPER_DOLPHIN_CI_AGENT_TOKEN",
		"SUPER_DOLPHIN_CI_AGENT_TOKEN=issue",
		"agent_token_digest",
		"RequesterFingerprint",
		"execute_ci=false",
	} {
		if !strings.Contains(document, want) {
			t.Errorf("accepted contract must document agent-token boundary %q", want)
		}
	}
}

// assertRemoteCIAgentTokenDigestBoundary 保留原始 token 不得越过摘要边界的断言。
func assertRemoteCIAgentTokenDigestBoundary(t *testing.T) {
	t.Helper()
	if err := cicontract.ValidateAgentTokenDigest("sha256:" + strings.Repeat("0", 64)); err != nil {
		t.Fatalf("canonical digest rejected: %v", err)
	}
	if err := cicontract.ValidateAgentTokenDigest("token"); err == nil {
		t.Fatal("raw token must not satisfy a digest-only boundary")
	}
}

// assertRemoteCIAgentTokenRequestPhases 保留申请与签发阶段的正反例。
func assertRemoteCIAgentTokenRequestPhases(t *testing.T) {
	t.Helper()
	if phase, err := cicontract.ClassifyAgentTokenRequest("", ""); err != nil || phase != cicontract.AgentTokenPhaseApplication {
		t.Fatalf("no-token request = %q, %v; want application", phase, err)
	}
	if phase, err := cicontract.ClassifyAgentTokenRequest(cicontract.AgentTokenIssueValue, ""); err != nil || phase != cicontract.AgentTokenPhaseIssued {
		t.Fatalf("explicit issue request = %q, %v; want issued", phase, err)
	}
	if _, err := cicontract.ClassifyAgentTokenRequest(cicontract.AgentTokenIssueValue, cicontract.AgentTokenIssueValue); err == nil {
		t.Fatal("dual issue sources must fail closed even with equal values")
	}
}

// assertRemoteCIAgentTokenApplicationGuidance 确认首阶段响应同时提供签发和复用入口。
func assertRemoteCIAgentTokenApplicationGuidance(t *testing.T) {
	t.Helper()
	guidance := cicontract.AgentTokenApplicationResponse().Guidance
	if guidance.IssueArgument != cicontract.AgentTokenFlag+"="+cicontract.AgentTokenIssueValue || guidance.IssueEnvironment != cicontract.AgentTokenEnvironment+"="+cicontract.AgentTokenIssueValue || guidance.ReuseFlag != cicontract.AgentTokenFlag || guidance.ReuseEnvironment != cicontract.AgentTokenEnvironment {
		t.Fatalf("stage-one guidance must contain both issue and actual-token flag/env use: %#v", guidance)
	}
}

// TestRemoteCIAgentTokenLegacyRequesterPathsAreAbsent 阻止已退役请求者身份
// 回到任何可执行 CI 路径。
func TestRemoteCIAgentTokenLegacyRequesterPathsAreAbsent(t *testing.T) {
	root := findRepoRoot(t)
	for _, path := range remoteCIAgentTokenProductionFiles(t, root) {
		if filepath.Base(path) == "remote_agent_token.go" {
			assertRemoteCIAgentTokenRejectsLegacyInputsOnly(t, path)
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		if violations := remoteCIAgentTokenLegacyViolations(filepath.ToSlash(relative), parsed); len(violations) != 0 {
			t.Errorf("%s retains retired requester identity: %s", filepath.ToSlash(relative), strings.Join(violations, ", "))
		}
	}
}

func assertRemoteCIAgentTokenRejectsLegacyInputsOnly(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, want := range []string{
		"--requester-fingerprint is retired; use --agent-token",
		"SUPER_DOLPHIN_GATE_REQUESTER_FINGERPRINT",
		"is retired; use %s",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("legacy token input rejection is incomplete: missing %q", want)
		}
	}
}

// TestRemoteCIAgentTokenGuardCounterexamples 验证原始旧身份写法被拒绝，
// 而只携带摘要的载体可以接受。
func TestRemoteCIAgentTokenGuardCounterexamples(t *testing.T) {
	safe := remoteCIAgentTokenParseFixture(t, `package fixture
type RunInput struct { AgentTokenDigest string }
func run(digest string) { _ = digest }
`)
	if got := remoteCIAgentTokenLegacyViolations("fixture.go", safe); len(got) != 0 {
		t.Fatalf("digest-only fixture has false legacy violations: %v", got)
	}
	legacy := remoteCIAgentTokenParseFixture(t, `package fixture
const RequesterFingerprintEnvironment = "SUPER_DOLPHIN_GATE_REQUESTER_FINGERPRINT"
type RunInput struct { RequesterFingerprint string }
`)
	if got := remoteCIAgentTokenLegacyViolations("fixture.go", legacy); len(got) == 0 {
		t.Fatal("legacy requester fixture must be rejected")
	}
	for name, source := range map[string]string{
		"legacy SQL table":      `package gate; func migrate() { _ = "SELECT job_id FROM ci_run_requesters" }`,
		"legacy JSON field":     `package gate; const field = "requester_fingerprint"`,
		"runtime field revival": `package gate; type RunInput struct { RequesterFingerprint string }`,
	} {
		if got := remoteCIAgentTokenLegacyViolations("fixture.go", remoteCIAgentTokenParseFixture(t, source)); len(got) == 0 {
			t.Errorf("%s mutation must be rejected", name)
		}
	}
}

// TestGitHooksKeepAgentTokensCallerOwned 防止 hook 脚本成为签发、持久化或令牌串联权威。
// hook 可透明继承环境，但绝不能生成或写入令牌。
func TestGitHooksKeepAgentTokensCallerOwned(t *testing.T) {
	root := findRepoRoot(t)
	for _, name := range []string{"pre-commit", "pre-push"} {
		contents, err := os.ReadFile(filepath.Join(root, ".githooks", name))
		if err != nil {
			t.Fatal(err)
		}
		if violations := remoteCIHookAgentTokenViolations(string(contents)); len(violations) != 0 {
			t.Errorf(".githooks/%s must remain token-stateless: %s", name, strings.Join(violations, ", "))
		}
	}
}

// TestRemoteGitHookRequestsTokenGuidanceBeforeHeavyWork 阻止无身份 pre-push
// 读取远程配置或启动 CI；pre-commit 不进入远程路径，因此不得读取 token。
func TestRemoteGitHookRequestsTokenGuidanceBeforeHeavyWork(t *testing.T) {
	root := findRepoRoot(t)
	preCommit, err := os.ReadFile(filepath.Join(root, ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(preCommit), "SUPER_DOLPHIN_CI_AGENT_TOKEN") {
		t.Fatal(".githooks/pre-commit must not read the remote CI agent token")
	}
	for name, firstHeavyOperation := range map[string]string{"pre-push": "remote_config="} {
		contents, err := os.ReadFile(filepath.Join(root, ".githooks", name))
		if err != nil {
			t.Fatal(err)
		}
		assertGitHookTokenHandshakePrecedesHeavyWork(t, name, string(contents), firstHeavyOperation)
	}
}

func assertGitHookTokenHandshakePrecedesHeavyWork(t *testing.T, name, contents, firstHeavyOperation string) {
	t.Helper()
	condition := `[[ "${SUPER_DOLPHIN_CI_AGENT_TOKEN-}" == "issue" || -z "${SUPER_DOLPHIN_CI_AGENT_TOKEN+x}" ]]`
	handshake := `exec "$gate_bin" remote hook ` + name
	conditionAt := strings.Index(contents, condition)
	handshakeAt := strings.Index(contents, handshake)
	heavyWorkAt := strings.Index(contents, firstHeavyOperation)
	if conditionAt < 0 || handshakeAt < 0 || heavyWorkAt < 0 || conditionAt > handshakeAt || handshakeAt > heavyWorkAt {
		t.Fatalf(".githooks/%s must invoke the token guidance or issue rejection before %q", name, firstHeavyOperation)
	}
}

func TestGitHookAgentTokenGuardCounterexamples(t *testing.T) {
	safe := `exec "$gate_bin" remote hook pre-push "$@"`
	if got := remoteCIHookAgentTokenViolations(safe); len(got) != 0 {
		t.Fatalf("stateless hook fixture has violations: %v", got)
	}
	unsafe := `
SUPER_DOLPHIN_CI_AGENT_TOKEN=issue
token=$(GenerateAgentToken)
git config --local super-dolphin.agent-token "$token"
`
	if got := remoteCIHookAgentTokenViolations(unsafe); len(got) == 0 {
		t.Fatal("hook issuance/cache fixture must be rejected")
	}
}

func remoteCIHookAgentTokenViolations(contents string) []string {
	lower := strings.ToLower(contents)
	violations := make([]string, 0, 4)
	for _, forbidden := range []string{
		"--agent-token=issue",
		"generateagenttoken",
		"super_dolphin_ci_agent_token=",
		"super-dolphin.agent-token",
		"keychain",
		"add-generic-password",
	} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			violations = append(violations, forbidden)
		}
	}
	return violations
}

func remoteCIAgentTokenProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, relativeRoot := range []string{"cmd/super-dolphin-gate", "internal/devtools/gate", "internal/devtools/remoteci"} {
		base := filepath.Join(root, filepath.FromSlash(relativeRoot))
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			if relativeRoot == "cmd/super-dolphin-gate" && !strings.HasPrefix(entry.Name(), "remote_") && entry.Name() != "test_cli.go" {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func remoteCIAgentTokenLegacyViolations(_ string, file *ast.File) []string {
	violations := map[string]struct{}{}
	ast.Inspect(file, func(node ast.Node) bool {
		if violation := remoteCIAgentTokenLegacyNodeViolation(node); violation != "" {
			violations[violation] = struct{}{}
		}
		return true
	})
	return remoteCIAgentTokenSortedViolations(violations)
}

func remoteCIAgentTokenLegacyNodeViolation(node ast.Node) string {
	switch value := node.(type) {
	case *ast.Ident:
		if strings.Contains(strings.ToLower(value.Name), "requesterfingerprint") {
			return "identifier " + value.Name
		}
	case *ast.BasicLit:
		return remoteCIAgentTokenLegacyLiteralViolation(value)
	}
	return ""
}

func remoteCIAgentTokenLegacyLiteralViolation(value *ast.BasicLit) string {
	if value.Kind != token.STRING {
		return ""
	}
	literal, err := strconv.Unquote(value.Value)
	if err != nil {
		return "invalid string literal " + value.Value
	}
	lower := strings.ToLower(literal)
	if strings.Contains(lower, "requester_fingerprint") || strings.Contains(lower, "ci_run_requesters") {
		return "literal " + value.Value
	}
	return ""
}

func remoteCIAgentTokenSortedViolations(violations map[string]struct{}) []string {
	result := make([]string, 0, len(violations))
	for violation := range violations {
		result = append(result, violation)
	}
	sort.Strings(result)
	return result
}

func remoteCIAgentTokenParseFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "agent_token_guard_fixture.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
