//go:build windows && e2e

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

const (
	realNodeAllLanguagesWindowsE2EEnv         = "MCP_LSP_REAL_NODE_ALL_LANGUAGES_WINDOWS_E2E"
	realNodePHPWindowsE2EEnv                  = "MCP_LSP_REAL_NODE_PHP_WINDOWS_E2E"
	realNodeDockerfileCapabilityWindowsE2EEnv = "MCP_LSP_REAL_NODE_DOCKERFILE_CAPABILITY_WINDOWS_E2E"
	realNodeJSONCapabilityWindowsE2EEnv       = "MCP_LSP_REAL_NODE_JSON_CAPABILITY_WINDOWS_E2E"
	realNodeVueWindowsE2EEnv                  = "MCP_LSP_REAL_NODE_VUE_WINDOWS_E2E"
	realNodePrismaDiagnosticE2EEnv            = "MCP_LSP_REAL_NODE_PRISMA_DIAGNOSTIC_WINDOWS_E2E"
	realNodeProductionWindowsE2EEnv           = "MCP_LSP_REAL_NODE_PRODUCTION_WINDOWS_E2E"
	realNodeWindowsReuseProductRootEnv        = "MCP_LSP_REAL_NODE_WINDOWS_REUSE_PRODUCT_ROOT"
	realNodeWindowsReuseRawNPMInstallRootEnv  = "MCP_LSP_REAL_NODE_WINDOWS_REUSE_RAW_NPM_INSTALL_ROOT"
)

var realNodeExpectedPins = map[string]string{
	"typescript-language-server":        typeScriptLanguageServerInstallVersion,
	"typescript":                        typeScriptInstallVersion,
	"vscode-langservers-extracted":      vscodeLangserversExtractedInstallVersion,
	"vscode-markdown-languageservice":   vscodeMarkdownLanguageServiceInstallVersion,
	"markdown-it":                       runtimeMarkdownItInstallVersion,
	"pyright":                           pyrightInstallVersion,
	"yaml-language-server":              yamlLanguageServerInstallVersion,
	"@vue/language-server":              vueLanguageServerInstallVersion,
	"svelte-language-server":            svelteLanguageServerInstallVersion,
	"intelephense":                      intelephenseInstallVersion,
	"dockerfile-language-server-nodejs": dockerfileLanguageServerInstallVersion,
	"graphql-language-service-cli":      graphqlLanguageServiceCLIInstallVersion,
	"@prisma/language-server":           prismaLanguageServerInstallVersion,
	"bash-language-server":              bashLanguageServerInstallVersion,
	"@ast-grep/cli":                     "0.43.0",
}

const realNodeVersion = installer.WindowsNodeRuntimeVersion

const realNodeDownloadTimeout = 10 * time.Minute

type realNodeAssetSpec struct {
	platformKey string
	archive     string
	sha256      string
}

var realNodeAssets = realNodeAssetFacts()

func realNodeAssetFacts() map[string]realNodeAssetSpec {
	facts := installer.WindowsNodeRuntimeAssetFacts()
	assets := make(map[string]realNodeAssetSpec, len(facts))
	for platformKey, fact := range facts {
		assets[platformKey] = realNodeAssetSpec{
			platformKey: platformKey,
			archive:     fact.Archive,
			sha256:      fact.SHA256,
		}
	}
	return assets
}

type realNodeServerCase struct {
	name        string
	languageID  string
	packageName string
	script      string
	args        []string
	fileName    string
	content     string
	line        int // raw LSP line (1-based).
	character   int // raw LSP character (0-based); MCP conversion adds one.
	// sourceDir/sourceFile 指向受版本控制的 bin/LSP/test 快照；真实矩阵会在
	// 任何 MCP 编辑动作前复制该快照。
	sourceDir            string
	sourceFile           string
	sourceSecondaryFile  string
	sourceIdentifier     string
	sourceWorkspaceQuery string
	sourceLine           int
	sourceCharacter      int
}

// TestMcpLSPBinaryRealNodeAllLanguagesWindowsE2E 在 Windows 原生架构上冷安装 Node、VC++ 运行库与精确 npm 语言服务，覆盖全部 Node 语言 ID、七类 MCP 工具及其公开小动作。
func TestMcpLSPBinaryRealNodeAllLanguagesWindowsE2E(t *testing.T) {
	if os.Getenv(realNodeAllLanguagesWindowsE2EEnv) != "1" {
		t.Skip("set MCP_LSP_REAL_NODE_ALL_LANGUAGES_WINDOWS_E2E=1 to run the real Windows npm cohort e2e")
	}
	started := time.Now()
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, npmBin := realNodeBundle(t, root)
	pins := realNodeScriptPins(t, root)
	installDir := realNodeInstallRootForE2E(t, npmBin, nodeDist, pins)

	servers := realNodeServerCases()
	requireRealNodeServerCaseClosure(t, servers)
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows native/process architecture for raw server tests: %v", err)
	}
	serverPlatform := fmt.Sprintf("windows-native-%s-process-%s", host.NativeArch, host.ProcessArch)
	for _, server := range servers {
		server := server
		t.Run(serverPlatform+"/"+server.name, func(t *testing.T) {
			if server.name == "vue" {
				// Vue v3 的 raw 进程没有 TypeScript companion；保留 17 个 language ID
				// closure，但跳过不能证明语义的 raw Vue 阶段，语义只由 production
				// product-owned bridge 证明，避免把假握手计入全量 PASS。
				t.Log("raw Vue semantic phase skipped; production product-owned bridge is the sole Vue semantic evidence")
				return
			}
			runRealNodeServer(t, root, nodeDist, installDir, server)
		})
	}

	binary := buildRealMcpLSPBinary(t, root)
	nonVueServers := make([]realNodeServerCase, 0, len(servers)-1)
	vueServers := make([]realNodeServerCase, 0, 1)
	for _, server := range servers {
		if server.name == "vue" {
			vueServers = append(vueServers, server)
			continue
		}
		nonVueServers = append(nonVueServers, server)
	}
	if len(nonVueServers)+len(vueServers) != realMCPExpectedLanguageCount || len(vueServers) != 1 {
		t.Fatalf("real MCP production split changed language closure: non_vue=%d vue=%d total=%d want=%d",
			len(nonVueServers), len(vueServers), len(nonVueServers)+len(vueServers), realMCPExpectedLanguageCount)
	}
	// 非 Vue 16 个 ID 继续使用临时 raw cohort；Vue 必须单独由已安装的产品根
	// 启动 production bridge，不能把只有 Vue 安装的 product root 传给其他语言。
	nonVueSummary := runRealMCPToolCoverageForServers(t, root, binary, nodeDist, installDir, nonVueServers, len(nonVueServers))
	productionProductRoot := prepareRealNodeProductionVueCohort(t)
	vueSummary := runRealMCPToolCoverageForServersWithProductRoot(t, root, binary, nodeDist, installDir, vueServers, 1, productionProductRoot)
	combinedSummary := realMCPMatrixSummary{
		total:                 nonVueSummary.total + vueSummary.total,
		succeeded:             nonVueSummary.succeeded + vueSummary.succeeded,
		legalEmpty:            nonVueSummary.legalEmpty + vueSummary.legalEmpty,
		capabilityUnsupported: nonVueSummary.capabilityUnsupported + vueSummary.capabilityUnsupported,
		unsupportedActions:    append(append([]string(nil), nonVueSummary.unsupportedActions...), vueSummary.unsupportedActions...),
	}
	expectedLanguageCount := len(nonVueServers) + len(vueServers)
	expectedActionTotal := realMCPExpectedMatrixActionTotal(expectedLanguageCount)
	if combinedSummary.total != expectedActionTotal || combinedSummary.succeeded+combinedSummary.legalEmpty+combinedSummary.capabilityUnsupported != combinedSummary.total {
		t.Fatalf("real MCP split matrix is not exact %dx%d: total=%d success=%d legal_empty=%d capability_unsupported=%d",
			expectedLanguageCount, realMCPExpectedActionCount, combinedSummary.total, combinedSummary.succeeded, combinedSummary.legalEmpty, combinedSummary.capabilityUnsupported)
	}
	t.Logf("real MCP action matrix aggregate platform=%s languages=%d actions=%d success=%d legal_empty=%d capability_unsupported=%d",
		serverPlatform, expectedLanguageCount, combinedSummary.total, combinedSummary.succeeded, combinedSummary.legalEmpty, combinedSummary.capabilityUnsupported)
	t.Logf("real Node all-language LSP E2E completed in %s", time.Since(started).Round(time.Millisecond))
}

func realNodeFocusedVueInstallRootForE2E(t *testing.T) string {
	t.Helper()
	configured := strings.TrimSpace(os.Getenv(realNodeWindowsReuseRawNPMInstallRootEnv))
	if configured == "" {
		t.Fatal("focused Vue E2E requires MCP_LSP_REAL_NODE_WINDOWS_REUSE_RAW_NPM_INSTALL_ROOT")
	}
	root, err := filepath.Abs(configured)
	if err != nil {
		t.Fatalf("resolve focused Vue npm cohort root: %v", err)
	}
	checks := []struct {
		name    string
		version string
	}{
		{name: "@vue/language-server", version: vueLanguageServerInstallVersion},
		{name: "@vue/typescript-plugin", version: vueLanguageServerInstallVersion},
		{name: "typescript", version: typeScriptInstallVersion},
	}
	for _, check := range checks {
		packageJSON := filepath.Join(root, "node_modules", filepath.FromSlash(check.name), "package.json")
		if !fileExists(packageJSON) {
			t.Fatalf("focused Vue npm cohort is missing %s: %s", check.name, packageJSON)
		}
		verifyRealNodePackageVersion(t, root, check.name, check.version)
	}
	return root
}

// TestMcpLSPBinaryRealNodePHPWindowsE2E runs only the real PHP fixture so a
// missing unrelated npm package cannot mask the PHP semantic-rename contract.
func TestMcpLSPBinaryRealNodePHPWindowsE2E(t *testing.T) {
	if os.Getenv(realNodePHPWindowsE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the focused real Windows PHP e2e", realNodePHPWindowsE2EEnv)
	}
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, _ := realNodeBundle(t, root)
	installDir, reused, err := realNodeReusableRawNPMInstallRoot()
	if err != nil || !reused {
		t.Fatalf("focused PHP E2E requires an existing raw npm cohort: reused=%t root=%q err=%v", reused, installDir, err)
	}
	server := realNodeServerCasesForLanguage("php")
	if len(server) != 1 {
		t.Fatalf("focused PHP server cases=%d, want 1", len(server))
	}
	binary := buildRealMcpLSPBinary(t, root)
	summary := runRealMCPToolCoverageForServers(t, root, binary, nodeDist, installDir, server, 1)
	if summary.total != realMCPExpectedActionCount {
		t.Fatalf("focused PHP three-tool matrix failed: total=%d success=%d legal_empty=%d unsupported=%v", summary.total, summary.succeeded, summary.legalEmpty, summary.unsupportedActions)
	}
}

// TestMcpLSPBinaryRealNodeDockerfileCapabilityWindowsE2E 在真实 Dockerfile 会话中
// 锁定 references 的服务端能力边界，并确认 diagnostics 不被误记为可选能力。
func TestMcpLSPBinaryRealNodeDockerfileCapabilityWindowsE2E(t *testing.T) {
	if os.Getenv(realNodeDockerfileCapabilityWindowsE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the targeted real Windows Dockerfile capability e2e", realNodeDockerfileCapabilityWindowsE2EEnv)
	}
	started := time.Now()
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, _ := realNodeBundle(t, root)
	installDir := realNodeFocusedVueInstallRootForE2E(t)
	servers := realNodeServerCasesForLanguage("dockerfile")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("targeted Dockerfile server cases=%d, want exactly 1", len(servers))
	}
	binary := buildRealMcpLSPBinary(t, root)
	summary := runRealMCPToolCoverageForServers(t, root, binary, nodeDist, installDir, servers, 1)
	if summary.total != realMCPExpectedActionCount {
		t.Fatalf("Dockerfile capability matrix total=%d, want %d", summary.total, realMCPExpectedActionCount)
	}
	if !slices.Contains(summary.unsupportedActions, "xref/references") {
		t.Fatalf("Dockerfile references must remain a typed server capability boundary: unsupported=%v", summary.unsupportedActions)
	}
	if slices.Contains(summary.unsupportedActions, "diagnostics/diagnostics") {
		t.Fatalf("Dockerfile diagnostics must reach the server, not capability_unsupported: unsupported=%v", summary.unsupportedActions)
	}
	t.Logf("targeted real Dockerfile capability E2E completed in %s: total=%d success=%d legal_empty=%d capability_unsupported=%d", time.Since(started).Round(time.Millisecond), summary.total, summary.succeeded, summary.legalEmpty, summary.capabilityUnsupported)
}

// TestMcpLSPBinaryRealNodeJSONCapabilityWindowsE2E 锁定 JSON server 的客户端能力门槛：
// diagnostics 必须可调用，references/semantic_tokens 仍按真实上游能力记账。
func TestMcpLSPBinaryRealNodeJSONCapabilityWindowsE2E(t *testing.T) {
	if os.Getenv(realNodeJSONCapabilityWindowsE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the targeted real Windows JSON capability e2e", realNodeJSONCapabilityWindowsE2EEnv)
	}
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, npmBin := realNodeBundle(t, root)
	pins := realNodeJSONScriptPins(t, root)
	installDir := realNodeFocusedJSONInstallRootForE2E(t, npmBin, nodeDist, pins)
	servers := realNodeServerCasesForLanguage("json")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("targeted JSON server cases=%d, want exactly 1", len(servers))
	}
	binary := buildRealMcpLSPBinary(t, root)
	summary := runRealMCPToolCoverageForServers(t, root, binary, nodeDist, installDir, servers, 1)
	if summary.total != realMCPExpectedActionCount {
		t.Fatalf("JSON capability matrix total=%d, want %d", summary.total, realMCPExpectedActionCount)
	}
	for _, action := range []string{"xref/references", "structure/semantic_tokens"} {
		if !slices.Contains(summary.unsupportedActions, action) {
			t.Fatalf("JSON %s must remain a typed upstream capability boundary: unsupported=%v", action, summary.unsupportedActions)
		}
	}
	if slices.Contains(summary.unsupportedActions, "diagnostics/diagnostics") {
		t.Fatalf("JSON diagnostics must be callable after initialize capability adaptation: unsupported=%v", summary.unsupportedActions)
	}
}

// TestMcpLSPBinaryRealNodeVueWindowsE2E 只运行 Vue 的真实安装、握手和 15-action 三工具矩阵；
// 正式全量测试仍固定要求 17 个 Node language ID，不受该 targeted selector 影响。
func TestMcpLSPBinaryRealNodeVueWindowsE2E(t *testing.T) {
	if os.Getenv(realNodeVueWindowsE2EEnv) != "1" {
		t.Skip("set MCP_LSP_REAL_NODE_VUE_WINDOWS_E2E=1 to run the targeted real Windows Vue e2e")
	}
	started := time.Now()
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, _ := realNodeBundle(t, root)
	installDir := realNodeFocusedVueInstallRootForE2E(t)
	servers := realNodeServerCasesForLanguage("vue")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("targeted Vue server cases = %d, want exactly 1", len(servers))
	}
	// targeted Vue 语义证据必须只来自 product-owned 生产 bridge；raw Vue
	// 进程没有 TypeScript companion，不能进入本轮 PASS 路径或伪造语义结果。
	t.Log("targeted Vue raw server semantic phase skipped; production product-owned bridge is the sole semantic evidence")
	productionProductRoot := prepareRealNodeProductionVueCohort(t)
	binary := buildRealMcpLSPBinary(t, root)
	runRealMCPToolCoverageForServersWithProductRoot(t, root, binary, nodeDist, installDir, servers, 1, productionProductRoot,
		"file/diagnostics", "structure/document_symbol", "completion/completion")
	t.Logf("targeted real Node Vue E2E completed in %s", time.Since(started).Round(time.Millisecond))
}

// TestMcpLSPBinaryRealNodePrismaDiagnosticWindowsE2E 只复核 Prisma raw 握手与 36-action 崩溃修复。
// 它明确是 NON_PASS 根因诊断，不替代正式 17x36 与十五分钟生命周期证明。
func TestMcpLSPBinaryRealNodePrismaDiagnosticWindowsE2E(t *testing.T) {
	if os.Getenv(realNodePrismaDiagnosticE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the targeted Prisma root-cause diagnostic", realNodePrismaDiagnosticE2EEnv)
	}
	started := time.Now()
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, npmBin := realNodeBundle(t, root)
	pins := realNodeScriptPins(t, root)
	installDir := realNodeInstallRootForE2E(t, npmBin, nodeDist, pins)
	servers := realNodeServerCasesForLanguage("prisma")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("targeted Prisma server cases=%d, want exactly 1", len(servers))
	}
	runRealNodeServer(t, root, nodeDist, installDir, servers[0])
	binary := buildRealMcpLSPBinary(t, root)
	summary := runRealMCPToolCoverageForServers(t, root, binary, nodeDist, installDir, servers, 1)
	if summary.total != realMCPExpectedActionCount || summary.succeeded+summary.capabilityUnsupported != summary.total {
		t.Fatalf("Prisma diagnostic matrix is not exact 1x36: total=%d success=%d legal_empty=%d unsupported=%d", summary.total, summary.succeeded, summary.legalEmpty, summary.capabilityUnsupported)
	}
	t.Logf("NON_PASS targeted Prisma root-cause diagnostic completed in %s; lifecycle proof intentionally absent", time.Since(started).Round(time.Millisecond))
}

// prepareRealNodeProductionVueCohort 在隔离产品根中执行真实 EnsureInstalled，随后 MCP
// 子进程只能消费该根内的 locked Node/npm/Vue/TypeScript cohort，不得回退 targeted 临时 npm。
func prepareRealNodeProductionVueCohort(t *testing.T) string {
	t.Helper()
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows platform for targeted production Vue cohort: %v", err)
	}
	configuredRoot := strings.TrimSpace(os.Getenv(realNodeWindowsReuseProductRootEnv))
	reusedRoot := configuredRoot != ""
	productRoot := configuredRoot
	if reusedRoot {
		productRoot, err = filepath.Abs(productRoot)
		if err != nil {
			t.Fatalf("resolve configured targeted production Vue product root: %v", err)
		}
		info, statErr := os.Stat(productRoot)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("configured targeted production Vue product root is not an existing directory: %q (%v)", productRoot, statErr)
		}
		if err := securefs.CheckPrivateOwnerOnly(productRoot, info); err != nil {
			t.Fatalf("configured targeted production Vue product root is not owner-only: %v", err)
		}
	} else {
		productRoot, err = os.MkdirTemp("", "sd-node-production-windows-"+platform.NativeArch+"-targeted-")
		if err != nil {
			t.Fatalf("create targeted production Vue product root: %v", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("remove targeted production Vue product root %s: %v", productRoot, err)
			}
		})
		if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
			t.Fatalf("restrict targeted production Vue product root: %v", err)
		}
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))
	for _, commandName := range []string{"node", "node.exe", "npm", "npm.cmd"} {
		if resolved, lookErr := exec.LookPath(commandName); lookErr == nil {
			t.Fatalf("targeted production PATH still resolves %s at %q", commandName, resolved)
		}
	}

	provider := setupInstaller()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "vue")
	if err != nil {
		t.Fatalf("targeted production EnsureInstalledDetailed(vue) failed: %v", err)
	}
	if result.Status == installer.InstallStatusInstalledFallback {
		t.Fatalf("targeted production Vue install unexpectedly used PATH fallback: %#v", result)
	}
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, result.Path); err != nil {
		t.Fatalf("targeted production Vue server escaped product root: %v", err)
	}
	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("construct targeted production Windows Node runtime: %v", err)
	}
	expectedPaths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve targeted production Windows Node cohort paths: %v", err)
	}
	wantVueBinary, err := nodeRuntime.BinaryPath(ctx, "vue-language-server")
	if err != nil {
		t.Fatalf("resolve targeted production Vue server path: %v", err)
	}
	if filepath.Clean(result.Path) != filepath.Clean(wantVueBinary) {
		t.Fatalf("targeted production Vue server path = %q, want locked cohort path %q", result.Path, wantVueBinary)
	}
	var packages []string
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		if !slices.Contains(spec.languages, "vue") {
			continue
		}
		packages, err = runtimeNPMExactPackages(spec.args)
		if err != nil {
			t.Fatalf("parse targeted production Vue exact package pins: %v", err)
		}
		break
	}
	if len(packages) == 0 {
		t.Fatal("targeted production Vue exact package pins are missing")
	}
	if err := nodeRuntime.ValidateExactPackages(ctx, packages); err != nil {
		t.Fatalf("validate targeted production Vue exact npm cohort: %v", err)
	}
	for _, specification := range packages {
		packageName, packageVersion, parseErr := productionExactPackageNameAndVersion(specification)
		if parseErr != nil {
			t.Fatalf("parse targeted production Vue package %q: %v", specification, parseErr)
		}
		verifyRealNodePackageVersion(t, expectedPaths.Prefix, packageName, packageVersion)
	}
	asset, err := installer.WindowsNodeRuntimeAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("select targeted production Node asset receipt: %v", err)
	}
	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("select targeted production VCLibs asset receipt: %v", err)
	}
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("resolve targeted production VCLibs ready root: %v", err)
	}
	vclibsProcessRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("resolve targeted production VCLibs process root: %v", err)
	}
	if same, sameErr := sameRealNodeFile(vclibsRoot, vclibsProcessRoot); sameErr != nil {
		t.Fatalf("compare targeted production VCLibs identities: %v", sameErr)
	} else if !same {
		t.Fatalf("targeted production VCLibs process root %q changed ready identity %q", vclibsProcessRoot, vclibsRoot)
	}
	vclibsPayload := filepath.Join(filepath.Dir(vclibsRoot), "payload.zip")
	gotVCLibsSHA, err := sha256File(vclibsPayload)
	if err != nil {
		t.Fatalf("hash targeted production VCLibs payload: %v", err)
	}
	if !strings.EqualFold(gotVCLibsSHA, vclibsAsset.SHA256) {
		t.Fatalf("targeted production VCLibs SHA256 = %s, want %s", gotVCLibsSHA, vclibsAsset.SHA256)
	}
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", vclibsProcessRoot)
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", expectedPaths.NodePath)
	bridgeSpec, err := runtimeServerWindowsVueTSBridgeSpec(result.Path)
	if err != nil {
		t.Fatalf("resolve targeted production Vue TypeScript bridge cohort: %v", err)
	}
	t.Logf("targeted production Vue cohort product_root=%s server=%s node=%s npm=%s prefix=%s platform_os=%s native_arch=%s process_arch=%s windows_version=%s windows_build=%d node_version=%s node_url=%s node_sha256=%s vclibs_root=%s vclibs_process_root=%s vclibs_version=%s vclibs_url=%s vclibs_sha256=%s typescript_binary=%s vue_plugin=%s",
		productRoot, result.Path, expectedPaths.NodePath, expectedPaths.NPMPath, expectedPaths.Prefix,
		platform.OS, platform.NativeArch, platform.ProcessArch, platform.WindowsVersion, platform.WindowsBuild,
		asset.Version, asset.URL, asset.SHA256, vclibsRoot, vclibsProcessRoot, vclibsAsset.Version,
		vclibsAsset.URL, vclibsAsset.SHA256, bridgeSpec.typescriptBinary, bridgeSpec.vuePluginLocation)
	return productRoot
}

// TestRealNodeProductionEnsureInstalledFromEmptyWindowsAssetCacheE2E 从空产品缓存验证 Windows Node 生产安装器、精确 npm 语言服务和安装收据。
func TestRealNodeProductionEnsureInstalledFromEmptyWindowsAssetCacheE2E(t *testing.T) {
	if os.Getenv(realNodeProductionWindowsE2EEnv) != "1" {
		t.Skip("set MCP_LSP_REAL_NODE_PRODUCTION_WINDOWS_E2E=1 to run the production Windows Node installer e2e")
	}
	if runtime.GOOS != "windows" {
		t.Skip("locked production Node installer E2E is Windows-only")
	}
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform before production install: %v", err)
	}
	// 产品根目录独立于用户真实缓存，测试结束时按保留开关统一回收。
	productRoot, err := os.MkdirTemp("", "sd-node-production-windows-"+platform.NativeArch+"-")
	if err != nil {
		t.Fatalf("create production Node E2E product root: %v", err)
	}
	keepProductRoot := os.Getenv("MCP_LSP_REAL_NODE_KEEP_WINDOWS_ROOT") == "1"
	if !keepProductRoot {
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("cleanup production Node E2E product root: %v", err)
			}
		})
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict production Node E2E product root: %v", err)
	}
	if keepProductRoot {
		t.Logf("keeping production Node E2E product root for diagnosis: %s", productRoot)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))
	for _, commandName := range []string{"node", "node.exe", "npm", "npm.cmd"} {
		if resolved, lookErr := exec.LookPath(commandName); lookErr == nil {
			t.Fatalf("production E2E PATH still resolves %s at %q", commandName, resolved)
		}
	}

	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	entries, err := os.ReadDir(cacheRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect empty production AssetCache root: %v", err)
	}
	if err == nil && len(entries) != 0 {
		t.Fatalf("production AssetCache root was not empty before EnsureInstalled: %#v", entries)
	}

	provider := setupInstaller()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "typescript")
	if err != nil {
		t.Fatalf("production EnsureInstalledDetailed(typescript) failed: %v", err)
	}

	asset, err := installer.WindowsNodeRuntimeAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("select exact production Node asset: %v", err)
	}
	t.Logf("production receipt platform os=%s native_arch=%s process_arch=%s windows_version=%s windows_build=%d node_version=%s url=%s sha256=%s",
		platform.OS, platform.NativeArch, platform.ProcessArch, platform.WindowsVersion, platform.WindowsBuild,
		asset.Version, asset.URL, asset.SHA256)
	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("select production Windows VCLibs asset: %v", err)
	}
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("resolve production Windows VCLibs ready root: %v", err)
	}
	vclibsProcessRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("resolve production Windows VCLibs process root: %v", err)
	}
	if same, sameErr := sameRealNodeFile(vclibsRoot, vclibsProcessRoot); sameErr != nil {
		t.Fatalf("compare production Windows VCLibs cache/process identities: %v", sameErr)
	} else if !same {
		t.Fatalf("production Windows VCLibs process root %q changed cache identity %q", vclibsProcessRoot, vclibsRoot)
	}
	vclibsPayload := filepath.Join(filepath.Dir(vclibsRoot), "payload.zip")
	if got, hashErr := sha256File(vclibsPayload); hashErr != nil {
		t.Fatalf("hash production Windows VCLibs payload: %v", hashErr)
	} else if !strings.EqualFold(got, vclibsAsset.SHA256) {
		t.Fatalf("production Windows VCLibs payload SHA256 = %s, want %s", got, vclibsAsset.SHA256)
	}
	if !realNodePathContains(os.Getenv("PATH"), vclibsProcessRoot) {
		t.Fatalf("production install PATH does not contain verified Windows VCLibs process root %q", vclibsProcessRoot)
	}
	t.Logf("production receipt Windows VCLibs native_arch=%s version=%s url=%s sha256=%s cache_root=%s process_root=%s",
		platform.NativeArch, vclibsAsset.Version, vclibsAsset.URL, vclibsAsset.SHA256, vclibsRoot, vclibsProcessRoot)
	productionSpecs := make(map[string]runtimeInstallerSpec)
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		for _, language := range spec.languages {
			productionSpecs[language] = spec
		}
	}
	prefix := filepath.Dir(filepath.Dir(filepath.Dir(result.Path)))
	productionCases := []struct {
		language string
		packages map[string]string
	}{
		{language: "typescript", packages: map[string]string{
			"typescript-language-server": typeScriptLanguageServerInstallVersion,
			"typescript":                 typeScriptInstallVersion,
		}},
		{language: "python", packages: map[string]string{
			"pyright": pyrightInstallVersion,
		}},
		{language: "css", packages: map[string]string{
			"vscode-langservers-extracted": vscodeLangserversExtractedInstallVersion,
		}},
		{language: "markdown", packages: map[string]string{
			"vscode-markdown-languageservice": vscodeMarkdownLanguageServiceInstallVersion,
		}},
		{language: "yaml", packages: map[string]string{
			"yaml-language-server": yamlLanguageServerInstallVersion,
		}},
		{language: "vue", packages: map[string]string{
			"@vue/language-server": vueLanguageServerInstallVersion,
		}},
		{language: "svelte", packages: map[string]string{
			"svelte-language-server": svelteLanguageServerInstallVersion,
		}},
		{language: "php", packages: map[string]string{
			"intelephense": intelephenseInstallVersion,
		}},
		{language: "dockerfile", packages: map[string]string{
			"dockerfile-language-server-nodejs": dockerfileLanguageServerInstallVersion,
		}},
		{language: "graphql", packages: map[string]string{
			"graphql-language-service-cli": graphqlLanguageServiceCLIInstallVersion,
		}},
		{language: "prisma", packages: map[string]string{
			"@prisma/language-server": prismaLanguageServerInstallVersion,
		}},
	}
	verifiedPackages := make(map[string]string)
	for _, tc := range productionCases {
		spec, ok := productionSpecs[tc.language]
		if !ok {
			t.Fatalf("production npm spec missing representative language %q", tc.language)
		}
		languageResult := result
		if tc.language != "typescript" {
			languageResult, err = provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), tc.language)
			if err != nil {
				t.Fatalf("production EnsureInstalledDetailed(%s) failed: %v", tc.language, err)
			}
		}
		wantScript := filepath.Join(prefix, "node_modules", ".bin", spec.binaryName)
		if filepath.Clean(languageResult.Path) != filepath.Clean(wantScript) {
			t.Fatalf("production %s script path = %q, want explicit %q", tc.language, languageResult.Path, wantScript)
		}
		if languageResult.Status == installer.InstallStatusInstalledFallback {
			t.Fatalf("production %s unexpectedly used PATH fallback", tc.language)
		}
		for packageName, wantVersion := range tc.packages {
			verifyRealNodePackageVersion(t, prefix, packageName, wantVersion)
			verifiedPackages[packageName] = wantVersion
			t.Logf("production receipt package=%s version=%s prefix=%s", packageName, wantVersion, prefix)
		}
	}
	if platform.NativeArch == installer.WindowsHostArchX64 {
		shellResult, shellErr := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "shellscript")
		if shellErr != nil {
			t.Fatalf("production shellscript EnsureInstalledDetailed failed on native x64: %v", shellErr)
		}
		shellSpec := runtimeShellNPMInstallerConfigForTarget("windows", platform.NativeArch)
		wantShell := filepath.Join(prefix, "node_modules", ".bin", shellSpec.BinaryName)
		if filepath.Clean(shellResult.Path) != filepath.Clean(wantShell) {
			t.Fatalf("production shellscript path = %q, want explicit %q", shellResult.Path, wantShell)
		}
		verifyRealNodePackageVersion(t, prefix, "bash-language-server", bashLanguageServerInstallVersion)
		verifyRealNodePackageVersion(t, prefix, "shellcheck", shellcheckInstallVersion)
		verifiedPackages["bash-language-server"] = bashLanguageServerInstallVersion
		verifiedPackages["shellcheck"] = shellcheckInstallVersion
		t.Logf("production receipt package=%s version=%s prefix=%s", "bash-language-server", bashLanguageServerInstallVersion, prefix)
		t.Logf("production receipt package=%s version=%s prefix=%s", "shellcheck", shellcheckInstallVersion, prefix)
	} else {
		shellResult, shellErr := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "shellscript")
		if shellErr != nil {
			t.Fatalf("production bash-language-server install failed on native %s: %v", platform.NativeArch, shellErr)
		}
		shellSpec := runtimeShellNPMInstallerConfigForTarget("windows", platform.NativeArch)
		wantShell := filepath.Join(prefix, "node_modules", ".bin", shellSpec.BinaryName)
		if filepath.Clean(shellResult.Path) != filepath.Clean(wantShell) {
			t.Fatalf("production shellscript path = %q, want explicit %q", shellResult.Path, wantShell)
		}
		if shellResult.Status == installer.InstallStatusInstalledFallback {
			t.Fatalf("production shellscript unexpectedly used PATH fallback")
		}
		verifyRealNodePackageVersion(t, prefix, "bash-language-server", bashLanguageServerInstallVersion)
		verifiedPackages["bash-language-server"] = bashLanguageServerInstallVersion
		t.Logf("production receipt package=%s version=%s prefix=%s optional_shellcheck=native_%s", "bash-language-server", bashLanguageServerInstallVersion, prefix, platform.NativeArch)
		cfg, cfgErr := runtimeShellNPMInstallerConfigForProduction("windows")
		if cfgErr != nil {
			t.Fatalf("production shell optional capability config failed: %v", cfgErr)
		}
		var optionalGap *installer.UnsupportedPlatformError
		if !errors.As(cfg.OptionalUnsupportedPlatform, &optionalGap) {
			t.Fatalf("production shell native %s optional shellcheck gap = %v, want typed capability gap", platform.NativeArch, cfg.OptionalUnsupportedPlatform)
		}
		if len(cfg.RequiredBinaries) != 0 {
			t.Fatalf("production shell native %s required binaries = %#v, want bash-only config", platform.NativeArch, cfg.RequiredBinaries)
		}
		t.Logf("production receipt optional capability feature=%s native_arch=%s", optionalGap.Feature, optionalGap.NativeArch)
	}
	expectedPackages := make(map[string]string)
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		for _, packageSpecification := range spec.args[2:] {
			packageName, packageVersion, parseErr := productionExactPackageNameAndVersion(packageSpecification)
			if parseErr != nil {
				t.Fatalf("parse production package specification %q: %v", packageSpecification, parseErr)
			}
			expectedPackages[packageName] = packageVersion
		}
	}
	if platform.NativeArch == installer.WindowsHostArchX64 {
		for _, packageSpecification := range runtimeShellNPMInstallerConfigForTarget("windows", platform.NativeArch).InstallArgs[2:] {
			packageName, packageVersion, parseErr := productionExactPackageNameAndVersion(packageSpecification)
			if parseErr != nil {
				t.Fatalf("parse production shell package specification %q: %v", packageSpecification, parseErr)
			}
			expectedPackages[packageName] = packageVersion
		}
	}
	for packageName, wantVersion := range expectedPackages {
		if gotVersion, ok := verifiedPackages[packageName]; !ok {
			t.Fatalf("production exact package receipt missing %s@%s", packageName, wantVersion)
		} else if gotVersion != wantVersion {
			t.Fatalf("production exact package receipt %s=%s, want %s", packageName, gotVersion, wantVersion)
		}
	}
	fact := installer.WindowsNodeRuntimeAssetFacts()["windows-"+asset.Architecture]
	assetRoot := filepath.Join(cacheRoot, "node-runtime", asset.Version, asset.Architecture, strings.ToLower(asset.SHA256))
	payloadPath := filepath.Join(assetRoot, "payload.zip")
	if got, err := sha256File(payloadPath); err != nil {
		t.Fatalf("hash downloaded locked Node payload: %v", err)
	} else if !strings.EqualFold(got, asset.SHA256) {
		t.Fatalf("locked Node payload SHA256 = %s, want %s", got, asset.SHA256)
	}
	for _, relative := range []string{
		filepath.Join("ready", filepath.FromSlash(fact.NodePath)),
		filepath.Join("ready", filepath.FromSlash(fact.NPMPath)),
	} {
		if err := requireRealNodeNonEmptyFile(filepath.Join(assetRoot, relative)); err != nil {
			t.Fatal(err)
		}
	}

	wantPath := filepath.Join(prefix, "node_modules", ".bin", "typescript-language-server.cmd")
	if filepath.Clean(result.Path) != filepath.Clean(wantPath) {
		t.Fatalf("production EnsureInstalled returned %q, want explicit cohort path %q", result.Path, wantPath)
	}
	if result.Status != installer.InstallStatusInstalledPath {
		t.Fatalf("production EnsureInstalled status = %q, want explicit installed_path", result.Status)
	}
	if err := requireRealNodeNonEmptyFile(result.Path); err != nil {
		t.Fatal(err)
	}
	runtimeNodePath := filepath.Join(assetRoot, "ready", filepath.FromSlash(fact.NodePath))
	runtimeNPMPath := filepath.Join(assetRoot, "ready", filepath.FromSlash(fact.NPMPath))
	runProductionCommandReceipt(t, ctx, "node.exe", runtimeNodePath, "--version")
	comspec := strings.TrimSpace(os.Getenv("ComSpec"))
	if comspec == "" {
		t.Fatal("ComSpec is empty; cannot produce explicit npm.cmd receipt")
	}
	// 完整 SHA 路径继续作为缓存身份和回执事实；在 LongPathsEnabled=0 的
	// Windows 上，进程边界必须使用与它指向同一文件的 8.3 路径。
	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("create production Node runtime for npm receipt: %v", err)
	}
	runtimeNPMProcessPath, err := nodeRuntime.NPMCommand(ctx)
	if err != nil {
		t.Fatalf("resolve production npm process path: %v", err)
	}
	if same, sameErr := sameRealNodeFile(runtimeNPMPath, runtimeNPMProcessPath); sameErr != nil {
		t.Fatalf("compare production npm cache/process identities: %v", sameErr)
	} else if !same {
		t.Fatalf("production npm process path %q does not preserve cache identity %q", runtimeNPMProcessPath, runtimeNPMPath)
	}
	runProductionCommandReceipt(t, ctx, "npm.cmd", comspec, "/d", "/s", "/c", runtimeNPMProcessPath+" --version")
	if leftovers := realNodeTemporaryEntries(t, cacheRoot); len(leftovers) != 0 {
		t.Fatalf("production Node install left temporary entries: %v", leftovers)
	}
	t.Logf("production receipt entry language=typescript status=%s path=%s", result.Status, result.Path)
	t.Logf("production locked Node %s/%s asset SHA and exact npm cohort verified; script=%s", platform.NativeArch, asset.Version, result.Path)
}

func sameRealNodeFile(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, err
	}
	return os.SameFile(leftInfo, rightInfo), nil
}

// realNodePathContains 按 Windows 大小写不敏感路径语义检查进程 PATH 回执。
func realNodePathContains(pathValue, want string) bool {
	for _, entry := range filepath.SplitList(pathValue) {
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(want)) {
			return true
		}
	}
	return false
}

func runProductionCommandReceipt(t *testing.T, ctx context.Context, label, executable string, args ...string) {
	t.Helper()
	command := exec.CommandContext(ctx, executable, args...)
	output, runErr := command.CombinedOutput()
	pid := -1
	if command.Process != nil {
		pid = command.Process.Pid
	}
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	t.Logf("production receipt command=%s executable=%s pid=%d exit=%d output=%q", label, executable, pid, exitCode, strings.TrimSpace(string(output)))
	if runErr != nil {
		t.Fatalf("production receipt command %s failed: %v", label, runErr)
	}
	if exitCode != 0 {
		t.Fatalf("production receipt command %s exit=%d", label, exitCode)
	}
}

func productionExactPackageNameAndVersion(specification string) (string, string, error) {
	specification = strings.TrimSpace(specification)
	separator := strings.LastIndexByte(specification, '@')
	if separator <= 0 || separator == len(specification)-1 || (specification[0] == '@' && separator == 1) {
		return "", "", fmt.Errorf("invalid exact package specification %q", specification)
	}
	return specification[:separator], specification[separator+1:], nil
}

func realNodePathWithoutNodeNPM(pathValue string) string {
	entries := make([]string, 0)
	for _, entry := range strings.Split(pathValue, string(os.PathListSeparator)) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(filepath.Clean(entry)))
		if strings.Contains(base, "node") || strings.Contains(base, "npm") {
			continue
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, string(os.PathListSeparator))
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func requireRealNodeNonEmptyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("required production Node file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("required production Node path is not a regular non-empty file: %q", path)
	}
	return nil
}

func verifyRealNodePackageVersion(t *testing.T, prefix, packageName, wantVersion string) {
	t.Helper()
	path := filepath.Join(prefix, "node_modules", filepath.FromSlash(packageName), "package.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production npm package %s: %v", packageName, err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatalf("decode production npm package %s: %v", packageName, err)
	}
	if manifest.Version != wantVersion {
		t.Fatalf("production npm package %s = %q, want exact %q", packageName, manifest.Version, wantVersion)
	}
}

// removeRealWindowsProductRoot 只回收正式 Windows LSP E2E 在系统临时目录直属创建、
// 且产品与平台一眼可见的受控根；调用参数漂移时必须在递归删除前失败。
func removeRealWindowsProductRoot(productRoot string) error {
	cleanRoot, err := validatedRealWindowsProductRoot(productRoot)
	if err != nil {
		return err
	}
	// 删除前再次校验，缩小校验与 RemoveAll 之间被替换为 reparse point 的窗口。
	if _, err := validatedRealWindowsProductRoot(cleanRoot); err != nil {
		return fmt.Errorf("revalidate production Windows LSP E2E product root before removal: %w", err)
	}
	if err := installer.RemoveWindowsInstallerTreeChecked(os.TempDir(), cleanRoot); err != nil {
		return fmt.Errorf("remove production Windows LSP E2E product root %s: %w", cleanRoot, err)
	}
	if _, err := os.Stat(cleanRoot); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("production Windows LSP E2E product root %s still exists after cleanup", cleanRoot)
		}
		return fmt.Errorf("verify production Windows LSP E2E product root %s disappeared: %w", cleanRoot, err)
	}
	return nil
}

// validatedRealWindowsProductRoot 接受目前真实创建的 Node、gopls、C#、Java、
// EmmyLua 与 Ruby LSP 产品根；
// 精确前缀表是删除权限边界，禁止使用宽泛 sd-* 或仅含 windows 的匹配。
func validatedRealWindowsProductRoot(productRoot string) (string, error) {
	if !filepath.IsAbs(productRoot) {
		return "", fmt.Errorf("production Node E2E product root %q is not an absolute path", productRoot)
	}
	cleanRoot := filepath.Clean(productRoot)
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("resolve system temporary directory: %w", err)
	}
	relative, err := filepath.Rel(filepath.Clean(tempRoot), cleanRoot)
	if err != nil || relative == "." || filepath.IsAbs(relative) || filepath.Dir(relative) != "." {
		return "", fmt.Errorf("production Node E2E product root %q is not a direct child of the system temporary directory", productRoot)
	}
	base := filepath.Base(cleanRoot)
	trustedPrefix := false
	for _, prefix := range []string{
		"sd-node-production-windows-",
		"sd-gopls-production-windows-",
		"sd-csharp-production-windows-",
		"sd-java-production-windows-",
		"sd-emmylua-production-windows-",
		"sd-ruby-production-windows-",
	} {
		if strings.HasPrefix(base, prefix) {
			trustedPrefix = true
			break
		}
	}
	if !trustedPrefix {
		return "", fmt.Errorf("production Windows LSP E2E product root %q lacks an approved product/platform prefix", productRoot)
	}
	if info, err := os.Lstat(cleanRoot); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect production Node E2E product root %q: %w", productRoot, err)
		}
	} else {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("production Node E2E product root %q is a symlink/reparse point", productRoot)
		}
		isReparse, err := realNodeProductRootIsReparsePoint(cleanRoot)
		if err != nil {
			return "", fmt.Errorf("inspect production Node E2E product root attributes %q: %w", productRoot, err)
		}
		if isReparse {
			return "", fmt.Errorf("production Node E2E product root %q is a Windows reparse point", productRoot)
		}
	}
	return cleanRoot, nil
}

// realNodeProductRootIsReparsePoint 使用 Windows 属性 API 检查 junction 等非 symlink reparse point。
func realNodeProductRootIsReparsePoint(path string) (bool, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

func realNodeTemporaryEntries(t *testing.T, root string) []string {
	t.Helper()
	leftovers := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		for _, prefix := range []string{".ready-", ".verify-", ".download-", ".extract-", ".payload-", ".tmp-"} {
			if strings.HasPrefix(name, prefix) {
				leftovers = append(leftovers, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Node cache lifecycle: %v", err)
	}
	return leftovers
}

// TestRealWindowsProductRootCleanupApprovedPrefixesE2E 锁定所有正式 cohort 的精确
// 产品/平台前缀；新增产品若未登记会在递归删除前失败，不能靠宽泛 sd-* 放行。
func TestRealWindowsProductRootCleanupApprovedPrefixesE2E(t *testing.T) {
	for _, prefix := range []string{
		"sd-node-production-windows-cleanup-",
		"sd-node-production-windows-arm64-",
		"sd-node-production-windows-arm64-targeted-",
		"sd-node-production-windows-mcp-arm64-",
		"sd-node-production-windows-gosqls-",
		"sd-node-production-windows-arm64-process-arm64-markdown-soak-15m-",
		"sd-node-production-windows-native-catalog-15x36-",
		"sd-node-production-windows-arm64-process-arm64-17x36-",
		"sd-node-production-windows-arm64-tools-list-",
		"sd-gopls-production-windows-cleanup-",
		"sd-gopls-production-windows-arm64-",
		"sd-csharp-production-windows-cleanup-",
		"sd-csharp-production-windows-arm64-",
		"sd-java-production-windows-cleanup-",
		"sd-java-production-windows-arm64-",
		"sd-emmylua-production-windows-arm64-",
		"sd-ruby-production-windows-arm64-",
	} {
		prefix := prefix
		t.Run(prefix, func(t *testing.T) {
			productRoot, err := os.MkdirTemp("", prefix)
			if err != nil {
				t.Fatalf("create product-root cleanup fixture: %v", err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(productRoot); err != nil {
					t.Errorf("fallback cleanup product-root fixture: %v", err)
				}
			})
			if err := os.WriteFile(filepath.Join(productRoot, "receipt.json"), []byte("receipt"), 0o600); err != nil {
				t.Fatalf("create product-root cleanup fixture: %v", err)
			}
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Fatalf("removeRealWindowsProductRoot() error = %v", err)
			}
			if _, err := os.Stat(productRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("product root stat after cleanup = %v, want not-exist", err)
			}
		})
	}
}

// TestRealWindowsProductRootCleanupRequestedE2E 是失败 E2E 的显式恢复入口。
// 只有调用方设置单个绝对路径时才执行；路径仍须通过直属临时目录、精确产品/平台
// 前缀和非 reparse 校验，不能把环境变量当成任意递归删除权限。
func TestRealWindowsProductRootCleanupRequestedE2E(t *testing.T) {
	const cleanupEnv = "MCP_LSP_CLEANUP_VALIDATED_WINDOWS_PRODUCT_ROOT_E2E"
	productRoot := strings.TrimSpace(os.Getenv(cleanupEnv))
	if productRoot == "" {
		t.Skipf("set %s to one validated failed-E2E product root", cleanupEnv)
	}
	base := filepath.Base(filepath.Clean(productRoot))
	if err := removeRealWindowsProductRoot(productRoot); err != nil {
		t.Fatalf("cleanup requested Windows product root %s: %v", base, err)
	}
	if _, err := os.Stat(productRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("requested Windows product root %s remains after cleanup: %v", base, err)
	}
	t.Logf("removed validated failed-E2E product root %s", base)
}

func TestRealNodeProductRootCleanupRejectsUntrustedAbsoluteRootE2E(t *testing.T) {
	untrusted := t.TempDir()
	sentinel := filepath.Join(untrusted, "must-remain.txt")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("create untrusted cleanup sentinel: %v", err)
	}
	if err := removeRealWindowsProductRoot(untrusted); err == nil {
		t.Fatal("removeRealWindowsProductRoot accepted an absolute root without an approved product/platform prefix")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("untrusted cleanup sentinel was changed: %v", err)
	}
}

// TestRealNodeProductRootCleanupRejectsReparseRootE2E 证明 cleanup 不会沿目录 symlink/reparse point 删除外部目标。
func TestRealNodeProductRootCleanupRejectsReparseRootE2E(t *testing.T) {
	target := t.TempDir()
	sentinel := filepath.Join(target, "must-remain.txt")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("create reparse cleanup sentinel: %v", err)
	}
	link, err := os.MkdirTemp("", "sd-node-production-windows-reparse-")
	if err != nil {
		t.Fatalf("create direct-child reparse fixture path: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove direct-child reparse fixture placeholder: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if err := os.Symlink(target, link); err != nil {
		junctionErr := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run()
		if junctionErr != nil {
			t.Skipf("directory symlink/reparse fixture unavailable: symlink=%v junction=%v", err, junctionErr)
		}
	}
	if err := removeRealWindowsProductRoot(link); err == nil {
		t.Fatal("removeRealWindowsProductRoot accepted a reparse-point root")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("reparse cleanup sentinel was changed: %v", err)
	}
}

// TestRealWindowsProductRootCleanupRejectsNestedReparseE2E 证明受控根本身正常时，
// cleanup 仍会在递归删除前拒绝任意后代 symlink/junction，并保留外部 sentinel。
func TestRealWindowsProductRootCleanupRejectsNestedReparseE2E(t *testing.T) {
	external := t.TempDir()
	sentinel := filepath.Join(external, "must-remain.txt")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("create nested reparse external sentinel: %v", err)
	}
	productRoot, err := os.MkdirTemp("", "sd-node-production-windows-nested-reparse-")
	if err != nil {
		t.Fatalf("create nested reparse product root: %v", err)
	}
	link := filepath.Join(productRoot, "nested-link")
	t.Cleanup(func() {
		_ = os.Remove(link)
		_ = os.RemoveAll(productRoot)
	})
	if err := os.Symlink(external, link); err != nil {
		junctionErr := exec.Command("cmd.exe", "/c", "mklink", "/J", link, external).Run()
		if junctionErr != nil {
			t.Skipf("nested directory symlink/reparse fixture unavailable: symlink=%v junction=%v", err, junctionErr)
		}
	}
	if err := removeRealWindowsProductRoot(productRoot); err == nil {
		t.Fatal("removeRealWindowsProductRoot accepted a nested reparse point")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("nested reparse external sentinel was changed: %v", err)
	}
	if _, err := os.Stat(productRoot); err != nil {
		t.Fatalf("product root disappeared after nested reparse rejection: %v", err)
	}
}

func TestRealNodeTemporaryEntriesDetectsPayloadPrefixE2E(t *testing.T) {
	root := t.TempDir()
	payloadTemp := filepath.Join(root, "node-runtime", ".payload-staging")
	if err := os.MkdirAll(payloadTemp, 0o700); err != nil {
		t.Fatalf("create payload temporary fixture: %v", err)
	}
	leftovers := realNodeTemporaryEntries(t, root)
	if len(leftovers) != 1 || filepath.Clean(leftovers[0]) != filepath.Clean(payloadTemp) {
		t.Fatalf("realNodeTemporaryEntries() = %v, want payload temporary path %s", leftovers, payloadTemp)
	}
}

// TestRealNodeWindowsArchitectureNormalizationE2E 证明系统架构别名只会规范化为 arm64、x64 或 x86，未知值立即失败。
func TestRealNodeWindowsArchitectureNormalizationE2E(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{raw: "ARM64", want: "arm64"},
		{raw: "aarch64", want: "arm64"},
		{raw: "x64", want: "x64"},
		{raw: "AMD64", want: "x64"},
		{raw: "x86", want: "x86"},
		{raw: "i686", want: "x86"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := normalizeRealWindowsArchitecture(tc.raw)
			if err != nil || got != tc.want {
				t.Fatalf("normalizeRealWindowsArchitecture(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
			}
		})
	}
	for _, raw := range []string{"", "mips64", "windows-amd64"} {
		if _, err := normalizeRealWindowsArchitecture(raw); err == nil {
			t.Fatalf("normalizeRealWindowsArchitecture(%q) accepted unknown architecture", raw)
		}
	}
}

// TestRealNodeWindowsRuntimeSelectionE2E 证明 Windows 版本、原生架构与进程架构共同决定精确 Node 资产且禁止跨架构回退。
func TestRealNodeWindowsRuntimeSelectionE2E(t *testing.T) {
	cases := []struct {
		name    string
		version string
		arch    string
		want    string
		wantErr bool
	}{
		{name: "windows10-arm64", version: "10.0.19045", arch: "ARM64", want: "windows-arm64"},
		{name: "windows11-arm64", version: "10.0.26100", arch: "arm64", want: "windows-arm64"},
		{name: "windows10-x64", version: "10.0.19045", arch: "x64", want: "windows-x64"},
		{name: "windows11-x64", version: "10.0.26100", arch: "x64", want: "windows-x64"},
		{name: "windows10-x86", version: "10.0.19045", arch: "x86", want: "windows-x86"},
		{name: "windows11-x86", version: "10.0.26100", arch: "x86", want: "windows-x86"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := realWindowsRuntimeSelection("windows", tc.version, tc.arch)
			if tc.wantErr {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "no exact") {
					t.Fatalf("Windows x86 selection = %q, %v; want exact-runtime fail-fast", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Windows selection = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ osName, version, arch string }{
		{osName: "linux", version: "10.0.26100", arch: "x64"},
		{osName: "windows", version: "", arch: "x64"},
		{osName: "windows", version: "6.1.7601", arch: "x64"},
	} {
		if _, err := realWindowsRuntimeSelection(tc.osName, tc.version, tc.arch); err == nil {
			t.Fatalf("runtime selection accepted invalid identity os=%q version=%q arch=%q", tc.osName, tc.version, tc.arch)
		}
	}
}

// TestRealNodeWindowsAssetTableE2E 证明 arm64、x64 与 x86 都有平台可见且带精确 SHA-256 的锁定 Node 资产。
func TestRealNodeWindowsAssetTableE2E(t *testing.T) {
	for _, platformKey := range []string{"windows-arm64", "windows-x64", "windows-x86"} {
		t.Run(platformKey, func(t *testing.T) {
			asset, ok := realNodeAssets[platformKey]
			if !ok || asset.platformKey != platformKey || !strings.Contains(asset.archive, "node-v"+realNodeVersion) || !regexpHexSHA256(asset.sha256) {
				t.Fatalf("Node %s asset is not an exact URL/SHA entry: %#v", platformKey, asset)
			}
		})
	}
}

// TestRealNodeWindowsAllNativeArchitectureAssetsDownloadAndExtractE2E 只验证 Windows
// ARM64、x64、x86 资产的静态下载与解包；它不执行资产，也不宣称宿主机属于任何一种架构。
// 宿主架构专用行为由带精确 windows/arm64（或其他架构）标签的 E2E 覆盖。
func TestRealNodeWindowsAllNativeArchitectureAssetsDownloadAndExtractE2E(t *testing.T) {
	for _, testCase := range []struct {
		platformKey string
		archiveRoot string
	}{
		{platformKey: "windows-arm64", archiveRoot: "node-v22.22.0-win-arm64"},
		{platformKey: "windows-x64", archiveRoot: "node-v22.22.0-win-x64"},
		{platformKey: "windows-x86", archiveRoot: "node-v22.22.0-win-x86"},
	} {
		t.Run(testCase.platformKey, func(t *testing.T) {
			asset := realNodeAssets[testCase.platformKey]
			archiveBytes := realNodeTestArchive(t, testCase.archiveRoot, map[string]string{
				"node.exe": "synthetic-node",
				"npm.cmd":  "synthetic-npm",
			})
			hash := sha256.Sum256(archiveBytes)
			asset.sha256 = hex.EncodeToString(hash[:])
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/"+asset.archive {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(archiveBytes)))
				_, _ = w.Write(archiveBytes)
			}))
			defer server.Close()

			outputRoot := t.TempDir()
			downloadCtx, cancel, downloadClient := realNodeDownloadRequestConfig()
			defer cancel()
			got, err := downloadAndExtractRealNodeAsset(downloadCtx, downloadClient, outputRoot, server.URL, asset)
			if err != nil {
				t.Fatalf("downloadAndExtractRealNodeAsset() error = %v", err)
			}
			if filepath.Base(got) != testCase.archiveRoot {
				t.Fatalf("downloaded Node root = %q, want exact version/platform directory", got)
			}
			for _, name := range []string{"node.exe", "npm.cmd"} {
				if !fileExists(filepath.Join(got, name)) {
					t.Fatalf("downloaded Node asset is missing %s", name)
				}
			}
		})
	}
}

func realNodeDownloadRequestConfig() (context.Context, context.CancelFunc, *http.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), realNodeDownloadTimeout)
	return ctx, cancel, &http.Client{Timeout: realNodeDownloadTimeout}
}

func TestRealNodeDownloadRequestConfigE2E(t *testing.T) {
	ctx, cancel, client := realNodeDownloadRequestConfig()
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("real Node download context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > realNodeDownloadTimeout {
		t.Fatalf("real Node download context remaining timeout = %s, want >0 and <=%s", remaining, realNodeDownloadTimeout)
	}
	if client == http.DefaultClient {
		t.Fatal("real Node download must not use http.DefaultClient")
	}
	if client.Timeout <= 0 || client.Timeout > realNodeDownloadTimeout {
		t.Fatalf("real Node download HTTP timeout = %s, want >0 and <=%s", client.Timeout, realNodeDownloadTimeout)
	}
}

func TestRealNodeDownloadHonorsContextCancellationE2E(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	asset := realNodeAssets["windows-arm64"]
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client := &http.Client{Timeout: time.Second}
	_, err := downloadAndExtractRealNodeAsset(ctx, client, t.TempDir(), server.URL, asset)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("download cancellation error = %v, want context deadline exceeded", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("httptest server did not observe request cancellation")
	}
}

func TestRealNodeDownloadHonorsHTTPClientTimeoutE2E(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	asset := realNodeAssets["windows-arm64"]
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &http.Client{Timeout: 50 * time.Millisecond}
	_, err := downloadAndExtractRealNodeAsset(ctx, client, t.TempDir(), server.URL, asset)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("download HTTP timeout error = %v, want context deadline exceeded", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("httptest server did not observe HTTP client timeout cancellation")
	}
}

func realNodeTestArchive(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(filepath.ToSlash(filepath.Join(root, name)))
		if err != nil {
			t.Fatalf("create synthetic Node ZIP entry %s: %v", name, err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatalf("write synthetic Node ZIP entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close synthetic Node ZIP: %v", err)
	}
	return buffer.Bytes()
}

func realNodeRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate repository go.mod from %s", dir)
	return ""
}

func realNodeBundle(t *testing.T, root string) (string, string) {
	t.Helper()
	cacheDir := filepath.Join(root, ".build-cache")
	platformKey := realNodePlatformKey(t)
	asset, ok := realNodeAssets[platformKey]
	if !ok {
		t.Fatalf("no exact Node %s asset is published by the locked Node %s cohort; architecture fallback is forbidden", platformKey, realNodeVersion)
	}
	candidates := make([]string, 0)
	if value := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_NODE_DIST")); value != "" {
		if !realNodeBundleMatchesPlatform(filepath.Base(filepath.Clean(value)), platformKey) || !strings.Contains(filepath.Base(filepath.Clean(value)), "node-v"+realNodeVersion) {
			t.Fatalf("SUPER_DOLPHIN_NODE_DIST=%q is not the exact Node %s %s asset", value, realNodeVersion, platformKey)
		}
		candidates = append(candidates, filepath.Clean(value))
	} else if entries, err := os.ReadDir(cacheDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "node-v"+realNodeVersion+"-") && realNodeBundleMatchesPlatform(entry.Name(), platformKey) {
				candidates = append(candidates, filepath.Join(cacheDir, entry.Name()))
			}
		}
	}
	sort.Strings(candidates)
	for _, dir := range candidates {
		nodeName, npmName := "node", "npm"
		if runtime.GOOS == "windows" {
			nodeName, npmName = "node.exe", "npm.cmd"
		}
		nodePath := filepath.Join(dir, nodeName)
		npmPath := filepath.Join(dir, npmName)
		if fileExists(nodePath) && fileExists(npmPath) {
			return dir, npmPath
		}
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("exact automatic Node download is currently defined only for Windows assets; no %s asset is allowed", platformKey)
	}
	downloadRoot := t.TempDir()
	downloadCtx, cancel, downloadClient := realNodeDownloadRequestConfig()
	defer cancel()
	dir, err := downloadAndExtractRealNodeAsset(downloadCtx, downloadClient, downloadRoot, "https://nodejs.org/dist", asset)
	if err != nil {
		t.Fatalf("download exact Node %s asset from official nodejs.org: %v", platformKey, err)
	}
	npmPath := filepath.Join(dir, "npm.cmd")
	if !fileExists(filepath.Join(dir, "node.exe")) || !fileExists(npmPath) {
		t.Fatalf("downloaded exact Node %s asset lacks node.exe/npm.cmd: %s", platformKey, dir)
	}
	t.Logf("downloaded fresh Node asset %s into %s", asset.archive, dir)
	return dir, npmPath
}

func realNodePlatformKey(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return runtime.GOOS + "-" + runtime.GOARCH
	}
	version, build, nativeArch := realWindowsIdentity(t)
	t.Logf("native Windows identity: version=%s build=%s architecture=%s", version, build, nativeArch)
	key, err := realWindowsRuntimeSelection(runtime.GOOS, version, nativeArch)
	if err != nil {
		t.Fatalf("Windows runtime selection: %v", err)
	}
	return key
}

func realNodeBundleMatchesPlatform(base, platformKey string) bool {
	base = strings.ToLower(base)
	platformKey = strings.ToLower(platformKey)
	if strings.Contains(base, platformKey) {
		return true
	}
	return strings.Replace(platformKey, "windows-", "win-", 1) != platformKey &&
		strings.Contains(base, strings.Replace(platformKey, "windows-", "win-", 1))
}

func realWindowsIdentity(t *testing.T) (string, string, string) {
	t.Helper()
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("discover native Windows version/build/architecture: %v", err)
	}
	if platform.OS != installer.WindowsHostOSWindows || platform.NativeArch == "" || platform.WindowsVersion == "" || platform.WindowsBuild == 0 {
		t.Fatalf("native Windows identity is incomplete: %#v", platform)
	}
	return fmt.Sprintf("%s.%d", platform.WindowsVersion, platform.WindowsBuild), strconv.FormatUint(uint64(platform.WindowsBuild), 10), platform.NativeArch
}

func normalizeRealWindowsArchitecture(raw string) (string, error) {
	return installer.NormalizeWindowsArchitectureAlias(raw)
}

func realWindowsRuntimeSelection(osName, version, rawArch string) (string, error) {
	if strings.ToLower(strings.TrimSpace(osName)) != "windows" {
		return "", fmt.Errorf("runtime selection requires Windows, got %q", osName)
	}
	versionParts := strings.Split(strings.TrimSpace(version), ".")
	if len(versionParts) < 3 || versionParts[0] != "10" || versionParts[1] != "0" {
		return "", fmt.Errorf("no exact locked Node asset for Windows version %q", version)
	}
	arch, err := normalizeRealWindowsArchitecture(rawArch)
	if err != nil {
		return "", err
	}
	build, err := strconv.ParseUint(versionParts[2], 10, 32)
	if err != nil || build == 0 {
		return "", fmt.Errorf("no exact locked Node asset for Windows build %q", version)
	}
	asset, err := installer.WindowsNodeRuntimeAssetForPlatform(installer.WindowsHostPlatform{
		OS:             installer.WindowsHostOSWindows,
		NativeArch:     arch,
		WindowsVersion: "10.0",
		WindowsBuild:   uint32(build),
	})
	if err != nil {
		return "", fmt.Errorf("no exact locked Node asset for Windows %s/%s: %w", version, arch, err)
	}
	return "windows-" + asset.Architecture, nil
}

func realNodeScriptPins(t *testing.T, root string) []string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, "scripts", "prepare_lsp_bundle_windows.ps1"))
	if err != nil {
		t.Fatalf("read Windows bundle script for exact npm pins: %v", err)
	}
	text := string(source)
	start := strings.Index(text, "$LSPNpmPackages = @(")
	if start < 0 {
		t.Fatal("Windows bundle script has no $LSPNpmPackages source block")
	}
	endRel := strings.Index(text[start:], "\n)")
	if endRel < 0 {
		t.Fatal("Windows bundle script npm source block is unterminated")
	}
	block := text[start : start+endRel]
	found := make(map[string]string)
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
		if !strings.HasPrefix(line, "'") || !strings.HasSuffix(line, "'") {
			continue
		}
		token := strings.Trim(line, "'")
		at := strings.LastIndex(token, "@")
		if at <= 0 || at == len(token)-1 {
			t.Fatalf("invalid npm pin in Windows bundle script: %q", token)
		}
		found[token[:at]] = token[at+1:]
	}
	for packageName, expected := range realNodeExpectedPins {
		if actual := found[packageName]; actual != expected {
			t.Fatalf("Windows bundle pin %s = %q, want %q", packageName, actual, expected)
		}
	}
	keys := make([]string, 0, len(realNodeExpectedPins))
	for packageName := range realNodeExpectedPins {
		keys = append(keys, packageName+"@"+found[packageName])
	}
	sort.Strings(keys)
	return keys
}

// realNodeReusableRawNPMInstallRoot 解析显式 raw npm cohort 根目录；设置后只允许
// 复用已有绝对目录，不能创建、清空或替换调用方提供的目录。
func realNodeReusableRawNPMInstallRoot() (string, bool, error) {
	configured := strings.TrimSpace(os.Getenv(realNodeWindowsReuseRawNPMInstallRootEnv))
	if configured == "" {
		return "", false, nil
	}
	configured = filepath.Clean(configured)
	if !filepath.IsAbs(configured) {
		return "", true, fmt.Errorf("%s must be an absolute Windows raw npm install root: %q", realNodeWindowsReuseRawNPMInstallRootEnv, configured)
	}
	info, err := os.Stat(configured)
	if err != nil {
		return "", true, fmt.Errorf("stat reusable Windows raw npm install root %q: %w", configured, err)
	}
	if !info.IsDir() {
		return "", true, fmt.Errorf("reusable Windows raw npm install root %q is not a directory", configured)
	}
	return configured, true, nil
}

// realNodeInstallRootForE2E 选择 raw npm cohort：未设置环境变量时冷安装并由测试
// 清理临时根；设置后先验证全部精确 pin、Windows .bin shim 与 ast-grep 原生文件，
// 随后直接复用已有根，绝不执行 npm install 或注册 cleanup。
func realNodeInstallRootForE2E(t *testing.T, npmBin, nodeDist string, pins []string) string {
	t.Helper()
	installDir, reused, err := realNodeReusableRawNPMInstallRoot()
	if err != nil {
		t.Fatalf("resolve reusable raw npm install root: %v", err)
	}
	if !reused {
		installDir = t.TempDir()
		registerRealMCPTempRootCleanup(t, installDir)
		realNodeInstall(t, npmBin, nodeDist, installDir, pins)
		t.Logf("real npm cohort cold-installed in private directory %s with pins %v", installDir, pins)
	} else {
		t.Logf("reusing existing raw npm cohort without npm install or cleanup: %s", installDir)
	}
	realNodeVerifyInstall(t, installDir)
	realNodeVerifyNPMBinEntries(t, installDir)
	realNodeVerifyNativeAstGrepRuntime(t, installDir)
	return installDir
}

// realNodeInstall 冷安装固定 npm 语言服务，并每 30 秒输出阶段心跳，避免长网络步骤无证据静默。
func realNodeInstall(t *testing.T, npmBin, nodeDist, installDir string, pins []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	args := append([]string{"install", "--prefix", installDir, "--save-exact"}, pins...)
	cmd := exec.CommandContext(ctx, npmBin, args...)
	cmd.Env = realNodeEnvironment(os.Environ(), nodeDist, installDir)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	started := time.Now()
	t.Logf("starting exact Windows npm language-server install: packages=%d prefix=%s", len(pins), installDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start exact npm install: %v\ncommand=%s %s", err, npmBin, strings.Join(args, " "))
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			elapsed := time.Since(started).Round(time.Second)
			if ctxErr := ctx.Err(); ctxErr != nil {
				t.Fatalf("exact npm install exceeded timeout after %s: %v\ncommand=%s %s\n%s", elapsed, ctxErr, npmBin, strings.Join(args, " "), output.String())
			}
			if err != nil {
				t.Fatalf("exact npm install failed after %s: %v\ncommand=%s %s\n%s", elapsed, err, npmBin, strings.Join(args, " "), output.String())
			}
			t.Logf("exact Windows npm language-server install completed: packages=%d elapsed=%s", len(pins), elapsed)
			return
		case <-ticker.C:
			t.Logf("exact Windows npm language-server install still running: packages=%d elapsed=%s", len(pins), time.Since(started).Round(time.Second))
		}
	}
}

func downloadAndExtractRealNodeAsset(ctx context.Context, client *http.Client, outputRoot, baseURL string, spec realNodeAssetSpec) (string, error) {
	if client == nil {
		return "", errors.New("Node asset HTTP client is nil")
	}
	if spec.platformKey == "" || spec.archive == "" || !regexpHexSHA256(spec.sha256) {
		return "", fmt.Errorf("Node asset specification is incomplete: %#v", spec)
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return "", fmt.Errorf("create Node download root: %w", err)
	}
	archivePath := filepath.Join(outputRoot, spec.archive)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/"+spec.archive, nil)
	if err != nil {
		return "", fmt.Errorf("create Node asset request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download Node asset %s: %w", spec.archive, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Node asset %s returned HTTP %d", spec.archive, response.StatusCode)
	}
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create Node archive %s: %w", archivePath, err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(response.Body, 512<<20+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("save Node asset %s: %w", spec.archive, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close Node asset %s: %w", spec.archive, closeErr)
	}
	if written > 512<<20 {
		return "", fmt.Errorf("Node asset %s exceeds 512 MiB download limit", spec.archive)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, spec.sha256) {
		return "", fmt.Errorf("Node asset %s SHA-256 = %s, want %s", spec.archive, actual, spec.sha256)
	}
	root, err := extractRealNodeZip(archivePath, outputRoot, spec)
	if err != nil {
		return "", err
	}
	return root, nil
}

func regexpHexSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func extractRealNodeZip(archivePath, outputRoot string, spec realNodeAssetSpec) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open Node asset ZIP %s: %w", archivePath, err)
	}
	defer reader.Close()
	rootName := ""
	for _, entry := range reader.File {
		name := strings.TrimPrefix(filepath.ToSlash(entry.Name), "./")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			return "", fmt.Errorf("unsafe Node ZIP entry %q", entry.Name)
		}
		relative := filepath.Clean(filepath.FromSlash(name))
		if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return "", fmt.Errorf("unsafe Node ZIP entry %q", entry.Name)
		}
		parts := strings.Split(name, "/")
		if rootName == "" {
			rootName = parts[0]
		} else if parts[0] != rootName {
			return "", fmt.Errorf("Node ZIP has multiple roots %q and %q", rootName, parts[0])
		}
		destination := filepath.Join(outputRoot, relative)
		info := entry.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return "", fmt.Errorf("create Node ZIP directory %s: %w", destination, err)
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Node ZIP entry %q is a symlink", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return "", fmt.Errorf("create Node ZIP parent for %s: %w", destination, err)
		}
		input, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("open Node ZIP entry %q: %w", entry.Name, err)
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			input.Close()
			return "", fmt.Errorf("create Node ZIP file %s: %w", destination, err)
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		outputCloseErr := output.Close()
		if copyErr != nil {
			return "", fmt.Errorf("extract Node ZIP entry %q: %w", entry.Name, copyErr)
		}
		if inputCloseErr != nil || outputCloseErr != nil {
			return "", fmt.Errorf("close Node ZIP entry %q: input=%v output=%v", entry.Name, inputCloseErr, outputCloseErr)
		}
	}
	if rootName == "" || !strings.Contains(rootName, "node-v"+realNodeVersion+"-") || !realNodeBundleMatchesPlatform(rootName, spec.platformKey) {
		return "", fmt.Errorf("Node ZIP root %q is not exact version %s platform %s", rootName, realNodeVersion, spec.platformKey)
	}
	return filepath.Join(outputRoot, rootName), nil
}

func realNodeVerifyInstall(t *testing.T, installDir string) {
	t.Helper()
	for packageName, expected := range realNodeExpectedPins {
		path := filepath.Join(installDir, "node_modules", filepath.FromSlash(packageName), "package.json")
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read installed npm metadata %s: %v", packageName, err)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(payload, &manifest); err != nil {
			t.Fatalf("decode installed npm metadata %s: %v", packageName, err)
		}
		if manifest.Version != expected {
			t.Fatalf("installed npm package %s = %q, want exact %q", packageName, manifest.Version, expected)
		}
	}
}

// realNodeVerifyNPMBinEntries 从每个精确 pin 的 package.json 读取 bin 声明，逐一
// 验证 Windows npm 生成的 .cmd shim，避免只检查版本却复用不完整的 raw cohort。
func realNodeVerifyNPMBinEntries(t *testing.T, installDir string) {
	t.Helper()
	binRoot := filepath.Join(installDir, "node_modules", ".bin")
	if info, err := os.Stat(binRoot); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("path is not a directory")
		}
		t.Fatalf("installed npm .bin directory is unavailable: %s: %v", binRoot, err)
	}
	for packageName := range realNodeExpectedPins {
		manifestPath := filepath.Join(installDir, "node_modules", filepath.FromSlash(packageName), "package.json")
		payload, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("read installed npm package %s for bin verification: %v", packageName, err)
		}
		var manifest struct {
			Bin json.RawMessage `json:"bin"`
		}
		if err := json.Unmarshal(payload, &manifest); err != nil {
			t.Fatalf("decode installed npm package %s for bin verification: %v", packageName, err)
		}
		rawBin := bytes.TrimSpace(manifest.Bin)
		if len(rawBin) == 0 || bytes.Equal(rawBin, []byte("null")) {
			continue
		}
		entries := make(map[string]string)
		if rawBin[0] == '"' {
			var target string
			if err := json.Unmarshal(rawBin, &target); err != nil {
				t.Fatalf("decode string bin for installed npm package %s: %v", packageName, err)
			}
			entries[filepath.Base(filepath.FromSlash(packageName))] = target
		} else if err := json.Unmarshal(rawBin, &entries); err != nil {
			t.Fatalf("decode map bin for installed npm package %s: %v", packageName, err)
		}
		for binName, target := range entries {
			if strings.TrimSpace(binName) == "" || strings.TrimSpace(target) == "" {
				t.Fatalf("installed npm package %s has an empty bin declaration: name=%q target=%q", packageName, binName, target)
			}
			shim := filepath.Join(binRoot, binName+".cmd")
			if !fileExists(shim) {
				t.Fatalf("installed npm package %s bin %q is missing Windows shim %q", packageName, binName, shim)
			}
		}
	}
}

func realNodeVerifyNativeAstGrepRuntime(t *testing.T, installDir string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	_, _, nativeArch := realWindowsIdentity(t)
	arch, err := normalizeRealWindowsArchitecture(nativeArch)
	if err != nil {
		t.Fatalf("normalize native Windows architecture for ast-grep: %v", err)
	}
	if arch == installer.WindowsHostArchX86 {
		t.Fatalf("ast-grep Windows x86 has no supported exact native cohort")
	}
	for _, executable := range []string{"sg.exe", "ast-grep.exe"} {
		if !fileExists(filepath.Join(installDir, "node_modules", "@ast-grep", "cli", executable)) {
			t.Fatalf("ast-grep %s native executable %s is missing; no architecture fallback is allowed", arch, executable)
		}
	}
	if realNodeAstGrepRuntimePaths(installDir) == "" {
		t.Fatalf("ast-grep %s native executable has no automatically provisioned vcruntime140.dll", arch)
	}
}

func realNodeServerCases() []realNodeServerCase {
	return []realNodeServerCase{
		{name: "javascript", languageID: "javascript", packageName: "typescript-language-server", script: "typescript-language-server/lib/cli.mjs", args: []string{"--stdio"}, fileName: "fixture.js", content: "const answer = 42;\nfunction greet(name) { return \"Hello \" + name; }\nconsole.log(greet(\"world\"));\n", line: 2, character: 9, sourceDir: "javascript", sourceFile: "module-examples/top-level-await/main.js", sourceSecondaryFile: "module-examples/top-level-await/modules/triangle.js", sourceIdentifier: "myCanvas", sourceWorkspaceQuery: "myCanvas", sourceLine: 9, sourceCharacter: 4},
		{name: "javascriptreact", languageID: "javascriptreact", packageName: "typescript-language-server", script: "typescript-language-server/lib/cli.mjs", args: []string{"--stdio"}, fileName: "fixture.jsx", content: "function Greeting(props) { return <h1>Hello {props.name}</h1>; }\nconst view = <Greeting name=\"world\" />;\n", line: 1, character: 10, sourceDir: "javascriptreact", sourceFile: "src/component/App.js", sourceSecondaryFile: "src/component/Button.js", sourceIdentifier: "handleClick", sourceWorkspaceQuery: "handleClick", sourceLine: 14, sourceCharacter: 2},
		{name: "typescript", languageID: "typescript", packageName: "typescript-language-server", script: "typescript-language-server/lib/cli.mjs", args: []string{"--stdio"}, fileName: "fixture.ts", content: "interface Greeting { text: string }\nconst answer: number = 42;\nfunction greet(name: string): Greeting { return { text: name }; }\nconsole.log(greet(\"world\").text);\n", line: 3, character: 9, sourceDir: "typescript", sourceFile: "src/mathematic.ts", sourceSecondaryFile: "src/index.ts", sourceIdentifier: "Mathematic", sourceWorkspaceQuery: "Mathematic", sourceLine: 1, sourceCharacter: 13},
		{name: "typescriptreact", languageID: "typescriptreact", packageName: "typescript-language-server", script: "typescript-language-server/lib/cli.mjs", args: []string{"--stdio"}, fileName: "fixture.tsx", content: "interface Props { name: string }\nfunction Greeting(props: Props) { return <h1>Hello {props.name}</h1>; }\nconst view = <Greeting name=\"world\" />;\n", line: 2, character: 10, sourceDir: "typescriptreact", sourceFile: "src/App.tsx", sourceSecondaryFile: "src/main.tsx", sourceIdentifier: "App", sourceWorkspaceQuery: "App", sourceLine: 7, sourceCharacter: 9},
		{name: "css", languageID: "css", packageName: "vscode-langservers-extracted", script: "vscode-langservers-extracted/bin/vscode-css-language-server", args: []string{"--stdio"}, fileName: "fixture.css", content: ".button { color: red; padding: 4px; }\n", line: 1, character: 2, sourceDir: "css", sourceFile: "styles/style.css", sourceSecondaryFile: "index.html", sourceIdentifier: "body", sourceWorkspaceQuery: "body", sourceLine: 23, sourceCharacter: 0},
		{name: "html", languageID: "html", packageName: "vscode-langservers-extracted", script: "vscode-langservers-extracted/bin/vscode-html-language-server", args: []string{"--stdio"}, fileName: "fixture.html", content: "<!doctype html>\n<html>\n  <body>\n    <main id=\"app\">\n      <h1>Hello</h1>\n    </main>\n  </body>\n</html>\n", line: 4, character: 10, sourceDir: "html", sourceFile: "index.html", sourceSecondaryFile: "styles/style.css", sourceIdentifier: "body", sourceWorkspaceQuery: "body", sourceLine: 9, sourceCharacter: 3},
		{name: "json", languageID: "json", packageName: "vscode-langservers-extracted", script: "vscode-langservers-extracted/bin/vscode-json-language-server", args: []string{"--stdio"}, fileName: "fixture.json", content: "{\n  \"name\": \"dolphin\",\n  \"version\": \"1.0\",\n  \"items\": [1, 2, 3]\n}\n", line: 2, character: 5, sourceDir: "json", sourceFile: "tests/v1/uniqueItems.json", sourceSecondaryFile: "tests/v1/anyOf.json", sourceIdentifier: "uniqueItems", sourceWorkspaceQuery: "uniqueItems", sourceLine: 6, sourceCharacter: 13},
		{name: "markdown", languageID: "markdown", packageName: "vscode-langservers-extracted", script: "vscode-markdown-language-server/bin/vscode-markdown-language-server", args: []string{"--stdio"}, fileName: "fixture.md", content: "# Real LSP Fixture\n\n## Section\n\nA [link](https://example.com/docs).\n", line: 3, character: 3, sourceDir: "markdown", sourceFile: "README.md", sourceSecondaryFile: "README.md", sourceIdentifier: "Markups", sourceWorkspaceQuery: "Markups", sourceLine: 16, sourceCharacter: 0},
		{name: "python", languageID: "python", packageName: "pyright", script: "pyright/langserver.index.js", args: []string{"--stdio"}, fileName: "fixture.py", content: "def greet(name: str) -> str:\n    return f\"Hello {name}\"\n\nanswer: int = 42\nprint(greet(\"world\"))\n", line: 1, character: 4, sourceDir: "python", sourceFile: "src/sample/simple.py", sourceSecondaryFile: "tests/test_simple.py", sourceIdentifier: "add_one", sourceWorkspaceQuery: "add_one", sourceLine: 1, sourceCharacter: 4},
		{name: "yaml", languageID: "yaml", packageName: "yaml-language-server", script: "yaml-language-server/bin/yaml-language-server", args: []string{"--stdio"}, fileName: "fixture.yaml", content: "name: dolphin\nversion: \"1.0\"\nservices:\n  web:\n    image: node:24\n", line: 1, character: 1, sourceDir: "yaml", sourceFile: "src/4JVG.yaml", sourceSecondaryFile: "src/36F6.yaml", sourceIdentifier: "name", sourceWorkspaceQuery: "name", sourceLine: 2, sourceCharacter: 2},
		{name: "vue", languageID: "vue", packageName: "@vue/language-server", script: "@vue/language-server/bin/vue-language-server.js", args: []string{"--stdio"}, fileName: "fixture.vue", content: "<template><button>{{ message }}</button></template>\n<script setup lang=\"ts\">\nconst message = 'hello'\n</script>\n", line: 3, character: 6, sourceDir: "vue", sourceFile: "src/App.vue", sourceSecondaryFile: "src/components/Item.vue", sourceIdentifier: "header", sourceWorkspaceQuery: "header", sourceLine: 38, sourceCharacter: 1},
		{name: "svelte", languageID: "svelte", packageName: "svelte-language-server", script: "svelte-language-server/bin/server.js", args: []string{"--stdio"}, fileName: "fixture.svelte", content: "<script lang=\"ts\">\nlet count = 0;\n</script>\n<button>{count}</button>\n", line: 2, character: 5, sourceDir: "svelte", sourceFile: "src/App.svelte", sourceSecondaryFile: "src/main.js", sourceIdentifier: "name", sourceWorkspaceQuery: "name", sourceLine: 2, sourceCharacter: 12},
		{name: "php", languageID: "php", packageName: "intelephense", script: "intelephense/lib/intelephense.js", args: []string{"--stdio"}, fileName: "fixture.php", content: "<?php\nfunction greet(string $name): string { return \"Hello \" . $name; }\necho greet(\"world\");\n", line: 2, character: 9, sourceDir: "php", sourceFile: "src/VersionParser.php", sourceSecondaryFile: "src/Semver.php", sourceIdentifier: "VersionParser", sourceWorkspaceQuery: "VersionParser", sourceLine: 24, sourceCharacter: 6},
		// Docker 0.15.0 只为跨行 instruction 返回 folding range；RUN continuation
		// 是真实协议输入，避免把没有可折叠结构的单行 fixture 误判为服务失败。
		{name: "dockerfile", languageID: "dockerfile", packageName: "dockerfile-language-server-nodejs", script: "dockerfile-language-server-nodejs/bin/docker-langserver", args: []string{"--stdio"}, fileName: "Dockerfile", content: "FROM node:24\nWORKDIR /app\nCOPY package.json .\nRUN echo preparing \\\n    && npm install\nCMD [\"node\", \"index.js\"]\n", line: 1, character: 2, sourceDir: "dockerfile", sourceFile: "Dockerfile", sourceSecondaryFile: "Dockerfile", sourceIdentifier: "FROM", sourceWorkspaceQuery: "FROM", sourceLine: 3, sourceCharacter: 0},
		{name: "graphql", languageID: "graphql", packageName: "graphql-language-service-cli", script: "graphql-language-service-cli/bin/graphql.js", args: []string{"server", "-m", "stream"}, fileName: "fixture.graphql", content: "type User { id: ID! name: String }\ntype Query { user: User }\nquery GetUser { user { id name } }\n", line: 3, character: 5, sourceDir: "graphql", sourceFile: "schema.graphql", sourceSecondaryFile: "handler/graphql.mjs", sourceIdentifier: "Film", sourceWorkspaceQuery: "Film", sourceLine: 6, sourceCharacter: 5},
		{name: "prisma", languageID: "prisma", packageName: "@prisma/language-server", script: "@prisma/language-server/dist/bin.js", args: []string{"--stdio"}, fileName: "schema.prisma", content: realMCPPrismaNaturalFixture, line: 16, character: 13, sourceDir: "prisma", sourceFile: "orm/starter/prisma/schema.prisma", sourceSecondaryFile: "orm/starter/prisma/schema.prisma", sourceIdentifier: "User", sourceWorkspaceQuery: "User", sourceLine: 13, sourceCharacter: 6},
		{name: "shellscript", languageID: "shellscript", packageName: "bash-language-server", script: "bash-language-server/out/cli.js", args: []string{"start"}, fileName: "fixture.sh", content: "#!/usr/bin/env bash\ngreet() { echo \"Hello $1\"; }\ngreet world\n", line: 2, character: 2, sourceDir: "shellscript", sourceFile: "test.sh", sourceSecondaryFile: "test.sh", sourceIdentifier: "assert_equals", sourceWorkspaceQuery: "assert_equals", sourceLine: 208, sourceCharacter: 0},
	}
}

// realMCPPrismaNaturalFixture 是 Windows Node E2E 的公共 Prisma 主 schema。
// Prisma 多文件解析器只把独占一行的右花括号视为 block 结束；单行 block 会把状态带入下一文件并产生负行号。
const realMCPPrismaNaturalFixture = `datasource db {
  provider = "sqlite"
  url      = "file:dev.db"
}
generator client {
  provider = "prisma-client-js"
}
/// User documentation
model User {
  id    Int    @id
  posts Post[]
}
/// Post documentation
model Post {
  id       Int  @id
  author   User @relation(fields: [authorId], references: [id])
  authorId Int
}
`

func realNodeServerCasesForLanguage(languageID string) []realNodeServerCase {
	var selected []realNodeServerCase
	for _, server := range realNodeServerCases() {
		if server.languageID == languageID {
			selected = append(selected, server)
		}
	}
	return selected
}

// TestRealNodeWindowsServerCaseClosureE2E 证明 Windows 精确 npm cohort 的全部 17 个 language ID 都有真实语言服务进程用例，不会被代表性子集掩盖。
func TestRealNodeWindowsServerCaseClosureE2E(t *testing.T) {
	servers := realNodeServerCases()
	requireRealNodeServerCaseClosure(t, servers)
	if len(servers) != 17 {
		t.Fatalf("real Windows Node server cases = %d, want 17 exact npm-backed language IDs", len(servers))
	}
}

// TestRealMCPDockerfileWindowsFixtureContainsFoldableInstruction 锁定 Docker
// language service 0.15.0 的真实 folding 输入：只有跨行 instruction 才会产出 range。
func TestRealMCPDockerfileWindowsFixtureContainsFoldableInstruction(t *testing.T) {
	servers := realNodeServerCasesForLanguage("dockerfile")
	if len(servers) != 1 {
		t.Fatalf("Dockerfile server cases=%d, want exactly 1", len(servers))
	}
	if !strings.Contains(servers[0].content, "\\\n") {
		t.Fatalf("Dockerfile folding fixture has no continuation instruction: %q", servers[0].content)
	}
}

// TestRealMCPBinSourceMappingsAreRealSemanticsE2E 是 Node17 fixture 映射的 RED/GREEN
// 守卫：错误 query 或 position 必须先失败，17 个真实快照映射随后全部通过。
func TestRealMCPBinSourceMappingsAreRealSemanticsE2E(t *testing.T) {
	sourceRoot := filepath.Join(realNodeRepoRoot(t), "bin", "LSP", "test")
	for _, server := range realNodeServerCases() {
		server := server
		t.Run(server.languageID, func(t *testing.T) {
			if _, _, err := realMCPNodeSourceMapping(sourceRoot, server); err != nil {
				t.Fatalf("real bin/LSP/test mapping rejected: %v", err)
			}

			badQuery := server
			badQuery.sourceWorkspaceQuery = "__missing_real_node17_query__"
			if _, _, err := realMCPNodeSourceMapping(sourceRoot, badQuery); err == nil {
				t.Fatal("missing workspace query unexpectedly passed the RED guard")
			}
			badPosition := server
			badPosition.sourceCharacter++
			if _, _, err := realMCPNodeSourceMapping(sourceRoot, badPosition); err == nil {
				t.Fatal("shifted semantic position unexpectedly passed the RED guard")
			}
		})
	}
}

// TestRealMCPFixtureSourcesTraceToBinLSPTestE2E 保证 17 个真实 Node case
// 的所有动作输入都能追溯到仓库内 bin/LSP/test 快照，并且复制后的编辑不会
// 触碰上游快照。这个合同不启动语言服务器，也不下载运行时。
func TestRealMCPFixtureSourcesTraceToBinLSPTestE2E(t *testing.T) {
	repoRoot := realNodeRepoRoot(t)
	sourceRoot := filepath.Join(repoRoot, "bin", "LSP", "test")
	for _, server := range realNodeServerCases() {
		server := server
		t.Run(server.languageID, func(t *testing.T) {
			if strings.TrimSpace(server.sourceDir) == "" || strings.TrimSpace(server.sourceFile) == "" || strings.TrimSpace(server.sourceSecondaryFile) == "" || strings.TrimSpace(server.sourceIdentifier) == "" || strings.TrimSpace(server.sourceWorkspaceQuery) == "" || server.sourceLine <= 0 || server.sourceCharacter < 0 {
				t.Fatalf("%s source mapping is incomplete: dir=%q file=%q secondary=%q identifier=%q query=%q line=%d character=%d", server.languageID, server.sourceDir, server.sourceFile, server.sourceSecondaryFile, server.sourceIdentifier, server.sourceWorkspaceQuery, server.sourceLine, server.sourceCharacter)
			}
			sourceDir := filepath.Join(sourceRoot, filepath.FromSlash(server.sourceDir))
			sourcePath := filepath.Join(sourceDir, filepath.FromSlash(server.sourceFile))
			if !realMCPPathWithinRoot(sourceRoot, sourceDir) || !realMCPPathWithinRoot(sourceRoot, sourcePath) {
				t.Fatalf("%s source mapping escapes bin/LSP/test: dir=%q file=%q", server.languageID, sourceDir, sourcePath)
			}
			fixture := writeRealMCPLanguageFixture(t, t.TempDir(), server)
			if fixture.workspaceQuery != server.sourceWorkspaceQuery || fixture.readLine != server.sourceLine {
				t.Fatalf("%s fixture semantic mapping changed: query=%q line=%d want_query=%q want_line=%d", server.languageID, fixture.workspaceQuery, fixture.readLine, server.sourceWorkspaceQuery, server.sourceLine)
			}
			if filepath.Clean(fixture.sourceRoot) != filepath.Clean(sourceRoot) {
				t.Fatalf("%s fixture source root is not bin/LSP/test: %q", server.languageID, fixture.sourceRoot)
			}
			if !realMCPPathWithinRoot(sourceRoot, fixture.sourcePath) {
				t.Fatalf("%s fixture source path is not under bin/LSP/test: %q", server.languageID, fixture.sourcePath)
			}
			if filepath.Clean(fixture.sourcePath) != filepath.Clean(sourcePath) {
				t.Fatalf("%s fixture source path does not match case mapping: got=%q want=%q", server.languageID, fixture.sourcePath, sourcePath)
			}
			if !realMCPPathWithinRoot(sourceRoot, fixture.sourceSecondaryPath) {
				t.Fatalf("%s secondary source path is not under bin/LSP/test: %q", server.languageID, fixture.sourceSecondaryPath)
			}
			if filepath.Clean(fixture.targetFile) == filepath.Clean(fixture.sourcePath) {
				t.Fatalf("%s fixture target aliases the checked-in source: %q", server.languageID, fixture.targetFile)
			}
			files := []struct {
				name        string
				fixturePath string
				sourcePath  string
			}{
				{name: "target", fixturePath: fixture.targetFile, sourcePath: fixture.sourcePath},
				{name: "secondary", fixturePath: fixture.secondaryFile, sourcePath: fixture.sourceSecondaryPath},
				{name: "replace", fixturePath: fixture.replaceFile, sourcePath: fixture.sourcePath},
				{name: "rename", fixturePath: fixture.renameFile, sourcePath: fixture.sourcePath},
				{name: "code_action", fixturePath: fixture.codeActionFile, sourcePath: fixture.sourcePath},
				{name: "format", fixturePath: fixture.formatFile, sourcePath: fixture.sourcePath},
				{name: "completion", fixturePath: fixture.completionFile, sourcePath: fixture.sourcePath},
			}
			for _, file := range files {
				if !realMCPPathWithinRoot(fixture.workDir, file.fixturePath) {
					t.Fatalf("%s %s fixture path escapes isolated workspace: %q", server.languageID, file.name, file.fixturePath)
				}
				if filepath.Clean(file.fixturePath) == filepath.Clean(file.sourcePath) {
					t.Fatalf("%s %s fixture aliases its checked-in source: %q", server.languageID, file.name, file.fixturePath)
				}
				source, err := os.ReadFile(file.sourcePath)
				if err != nil {
					t.Fatalf("read %s %s checked-in source: %v", server.languageID, file.name, err)
				}
				fixtureBytes, err := os.ReadFile(file.fixturePath)
				if err != nil {
					t.Fatalf("read %s %s copied fixture: %v", server.languageID, file.name, err)
				}
				if !bytes.Equal(source, fixtureBytes) {
					t.Fatalf("%s %s copied fixture differs from checked-in source", server.languageID, file.name)
				}
			}
			assertRealMCPNativeFixtureInputs(t, fixture)
			actions := realMCPActionSpecs(server, fixture, "")
			var workspaceQueries []string
			for _, action := range actions {
				if action.tool == "structure" && strings.HasPrefix(action.name, "workspace_symbol-") {
					query, _ := action.args["query"].(string)
					workspaceQueries = append(workspaceQueries, query)
				}
			}
			if len(workspaceQueries) != 2 || workspaceQueries[0] != server.sourceWorkspaceQuery || workspaceQueries[1] != server.sourceWorkspaceQuery {
				t.Fatalf("%s workspace_symbol actions do not use the mapped real query: %v", server.languageID, workspaceQueries)
			}
			for _, action := range actions {
				if action.tool != "structure" && action.tool != "xref" && action.tool != "diagnostics" {
					t.Fatalf("%s action %q exposes removed MCP tool %q", server.languageID, action.name, action.tool)
				}
			}
		})
	}
}

func TestRealMCPTypeScriptRenameFixtureIncludesExcludedTestConsumerE2E(t *testing.T) {
	for _, languageID := range []string{"typescript"} {
		t.Run(languageID, func(t *testing.T) {
			server := realNodeServerCasesForLanguage(languageID)[0]
			fixture := writeRealMCPLanguageFixture(t, t.TempDir(), server)
			actionRoot := filepath.Dir(filepath.Dir(fixture.renameFile))
			for _, relative := range []string{"package.json", "tsconfig.json", "src/index.ts", "src/mathematic.test.ts"} {
				if _, err := os.Stat(filepath.Join(actionRoot, filepath.FromSlash(relative))); err != nil {
					t.Fatalf("rename fixture missing project consumer %s: %v", relative, err)
				}
			}
		})
	}
}

func requireRealNodeServerCaseClosure(t *testing.T, servers []realNodeServerCase) {
	t.Helper()
	want := make(map[string]struct{})
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		for _, languageID := range spec.languages {
			want[languageID] = struct{}{}
		}
	}
	want["shellscript"] = struct{}{}

	got := requireRealNodeServerCaseIdentities(t, servers)
	if !maps.Equal(got, want) {
		t.Fatalf("real Node server language closure = %v, want every Windows npm-backed ID %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}

// TestRealNodeReusableRawNPMInstallRootE2E 锁定 raw npm cohort 复用入口：显式根必须
// 是已有绝对目录；未设置时返回冷安装模式，不能把缺失或相对路径静默变成新目录。
func TestRealNodeReusableRawNPMInstallRootE2E(t *testing.T) {
	t.Run("unset keeps cold-install mode", func(t *testing.T) {
		t.Setenv(realNodeWindowsReuseRawNPMInstallRootEnv, "")
		got, reused, err := realNodeReusableRawNPMInstallRoot()
		if err != nil {
			t.Fatalf("realNodeReusableRawNPMInstallRoot() error = %v", err)
		}
		if got != "" || reused {
			t.Fatalf("realNodeReusableRawNPMInstallRoot() = (%q, %t), want (empty, false)", got, reused)
		}
	})

	t.Run("existing absolute root is reused", func(t *testing.T) {
		root := t.TempDir()
		sentinel := filepath.Join(root, "raw-npm-reuse-sentinel")
		if err := os.WriteFile(sentinel, []byte("must remain"), 0o600); err != nil {
			t.Fatalf("write raw npm reuse sentinel: %v", err)
		}
		t.Setenv(realNodeWindowsReuseRawNPMInstallRootEnv, root)
		got, reused, err := realNodeReusableRawNPMInstallRoot()
		if err != nil {
			t.Fatalf("realNodeReusableRawNPMInstallRoot() error = %v", err)
		}
		if filepath.Clean(got) != filepath.Clean(root) || !reused {
			t.Fatalf("realNodeReusableRawNPMInstallRoot() = (%q, %t), want (%q, true)", got, reused, root)
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("raw npm reuse sentinel was changed: %v", err)
		}
	})

	for name, configured := range map[string]string{
		"relative root": "relative-raw-npm-root",
		"missing root":  filepath.Join(t.TempDir(), "missing-raw-npm-root"),
	} {
		t.Run(name+" fails fast", func(t *testing.T) {
			t.Setenv(realNodeWindowsReuseRawNPMInstallRootEnv, configured)
			if got, _, err := realNodeReusableRawNPMInstallRoot(); err == nil {
				t.Fatalf("realNodeReusableRawNPMInstallRoot() accepted %q and returned %q", configured, got)
			}
		})
	}
}

func requireRealNodeServerCaseIdentities(t *testing.T, servers []realNodeServerCase) map[string]struct{} {
	t.Helper()
	got := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		if strings.TrimSpace(server.languageID) == "" {
			t.Fatal("real Node server case has an empty language ID")
		}
		if _, duplicate := got[server.languageID]; duplicate {
			t.Fatalf("real Node server case duplicates language ID %q", server.languageID)
		}
		got[server.languageID] = struct{}{}
	}
	return got
}

func runRealNodeServer(t *testing.T, root, nodeDist, installDir string, server realNodeServerCase) {
	t.Helper()
	fixtureRoot := t.TempDir()
	// Raw Node17 probes must consume the same checked-in bin/LSP/test snapshot as
	// the MCP matrix; server.content is only a contract-test fixture.
	fixture := writeRealMCPBinSourceFixture(t, fixtureRoot, server)
	fixturePath := fixture.targetFile
	fixtureBytes := readRealMCPBinSourceFile(t, fixturePath)
	fixtureContent := string(fixtureBytes)
	fixtureRoot = fixture.workDir
	if server.name == "vue" {
		if err := os.WriteFile(filepath.Join(fixtureRoot, "tsconfig.json"), []byte(`{"compilerOptions":{"allowJs":true,"jsx":"preserve","module":"ESNext","moduleResolution":"Bundler","target":"ESNext","strict":true},"include":["*.vue"]}`), 0o600); err != nil {
			t.Fatalf("write Vue tsconfig: %v", err)
		}
	}
	script := filepath.Join(installDir, "node_modules", filepath.FromSlash(server.script))
	if !fileExists(script) {
		t.Fatalf("installed %s server script is missing: %s", server.name, script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePathForDist(nodeDist), append([]string{script}, server.args...)...)
	if server.name == "graphql" {
		cmd.Args = append(cmd.Args, "--configDir", fixtureRoot)
	}
	cmd.Dir = fixtureRoot
	cmd.Env = realNodeEnvironment(os.Environ(), nodeDist, installDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("%s stdin pipe: %v", server.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("%s stdout pipe: %v", server.name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("%s stderr pipe: %v", server.name, err)
	}
	client := &realLSPClient{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReader(stdout),
		stderr:    &realNodeBuffer{},
		documents: map[string]string{realFileURI(fixturePath): fixtureContent},
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start real %s server: %v", server.name, err)
	}
	pid := cmd.Process.Pid
	trackedProcesses := make(map[realMCPProcessKey]realMCPProcessIdentity)
	if runtime.GOOS == "windows" {
		start, err := windowsGoplsProcessStartIdentity(pid)
		if err != nil {
			t.Fatalf("capture raw %s server PID %d start identity: %v", server.name, pid, err)
		}
		trackedProcesses[realMCPProcessKey{PID: pid, StartToken: start}] = realMCPProcessIdentity{
			PID: pid, StartToken: start, Name: "node-" + server.name, Language: server.languageID,
		}
	}
	t.Logf("%s real server PID=%d", server.name, pid)
	go func() { _, _ = io.Copy(client.stderr, stderr) }()
	defer func() {
		treeCaptured := true
		if runtime.GOOS == "windows" {
			treeCaptured = trackRealMCPProcessTree(t, pid, "raw-"+server.languageID, trackedProcesses)
		}
		client.close(t)
		if runtime.GOOS == "windows" {
			if !treeCaptured {
				t.Errorf("raw %s server process tree snapshot failed; zero-residual proof is incomplete", server.name)
			}
			requireRealMCPProcessIdentitiesGone(t, trackedProcesses)
		} else if processExists(pid) {
			t.Errorf("%s server PID %d remains after shutdown; stderr=%s", server.name, pid, client.stderr.String())
		}
	}()

	rootURI := realFileURI(fixtureRoot)
	initialized, err := client.request(ctx, "initialize", realInitializeParams(rootURI))
	if err != nil {
		t.Fatalf("%s initialize: %v; stderr=%s", server.name, err, client.stderr.String())
	}
	var initializeResult struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(initialized, &initializeResult); err != nil || len(initializeResult.Capabilities) == 0 {
		t.Fatalf("%s initialize returned no capabilities: %s; err=%v; stderr=%s", server.name, initialized, err, client.stderr.String())
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		t.Fatalf("%s initialized notification: %v", server.name, err)
	}
	if server.name == "markdown" {
		if err := client.notify("workspace/didChangeConfiguration", realWorkspaceSettings()); err != nil {
			t.Fatalf("%s configuration notification: %v", server.name, err)
		}
	}
	if server.name == "vue" {
		// Vue v3 的 tsserver/request 只能由生产 MCP bridge 转发到真实 TypeScript LS；
		// raw 进程阶段只证明 initialize/capability handshake，禁止伪造 body 或 null response，
		// 也不得把该阶段计入语义通过。真实 36-action 语义账本由下方 production bridge 负责。
		t.Logf("%s initialize capabilities=%d handshake_only=true semantic_probe=skipped", server.name, len(initializeResult.Capabilities))
	} else {
		if err := client.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": realFileURI(fixturePath), "languageId": server.languageID, "version": 1, "text": fixtureContent},
		}); err != nil {
			t.Fatalf("%s didOpen notification: %v", server.name, err)
		}
		if server.name != "graphql" {
			if err := client.notify("textDocument/didChange", map[string]any{
				"textDocument":   map[string]any{"uri": realFileURI(fixturePath), "version": 2},
				"contentChanges": []map[string]any{{"text": fixtureContent}},
			}); err != nil {
				t.Fatalf("%s didChange notification: %v", server.name, err)
			}
		}
		method, err := realSemanticProbe(ctx, client, fixturePath, server)
		if err != nil {
			t.Fatalf("%s has no non-empty semantic result or diagnostics: %v; results=%v; errors=%v; serverMessages=%v; capabilities=%s; stderr=%s", server.name, err, client.semanticResults, client.semanticErrors, client.serverMessages, initialized, client.stderr.String())
		}
		t.Logf("%s initialize capabilities=%d semantic=%s diagnostics=%d", server.name, len(initializeResult.Capabilities), method, client.diagnostics)
	}
	if _, err := client.request(ctx, "shutdown", nil); err != nil {
		t.Fatalf("%s shutdown request: %v; stderr=%s", server.name, err, client.stderr.String())
	}
	if err := client.notify("exit", nil); err != nil {
		t.Fatalf("%s exit notification: %v", server.name, err)
	}
}

func realInitializeParams(rootURI string) map[string]any {
	return map[string]any{
		"processId":             os.Getpid(),
		"rootPath":              "",
		"rootUri":               rootURI,
		"capabilities":          map[string]any{"textDocument": map[string]any{"hover": map[string]any{"contentFormat": []string{"plaintext", "markdown"}}, "completion": map[string]any{}, "definition": map[string]any{}, "documentSymbol": map[string]any{}, "semanticTokens": map[string]any{}}},
		"workspaceFolders":      []map[string]any{{"uri": rootURI, "name": "real-node-e2e"}},
		"initializationOptions": map[string]any{},
	}
}

func realSemanticProbe(ctx context.Context, client *realLSPClient, fixture string, server realNodeServerCase) (string, error) {
	if strings.TrimSpace(server.sourceIdentifier) == "" || strings.TrimSpace(server.sourceWorkspaceQuery) == "" || server.sourceLine <= 0 || server.sourceCharacter < 0 {
		return "", fmt.Errorf("%s real source semantic mapping is incomplete: identifier=%q query=%q line=%d character=%d", server.languageID, server.sourceIdentifier, server.sourceWorkspaceQuery, server.sourceLine, server.sourceCharacter)
	}
	uri := realFileURI(fixture)
	position := map[string]any{"line": server.sourceLine - 1, "character": server.sourceCharacter}
	workspaceQueries := []string{server.sourceWorkspaceQuery}
	if server.sourceIdentifier != server.sourceWorkspaceQuery {
		workspaceQueries = append(workspaceQueries, server.sourceIdentifier)
	}
	requests := []struct {
		method string
		params map[string]any
	}{
		{method: "textDocument/documentSymbol", params: map[string]any{"textDocument": map[string]any{"uri": uri}}},
		{method: "textDocument/foldingRange", params: map[string]any{"textDocument": map[string]any{"uri": uri}}},
		{method: "textDocument/documentLink", params: map[string]any{"textDocument": map[string]any{"uri": uri}}},
		{method: "textDocument/selectionRange", params: map[string]any{"textDocument": map[string]any{"uri": uri}, "positions": []map[string]any{position}}},
		{method: "textDocument/semanticTokens/full", params: map[string]any{"textDocument": map[string]any{"uri": uri}}},
	}
	for _, query := range workspaceQueries {
		requests = append(requests, struct {
			method string
			params map[string]any
		}{method: "workspace/symbol", params: map[string]any{"query": query}})
	}
	requests = append(requests,
		struct {
			method string
			params map[string]any
		}{method: "textDocument/hover", params: map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position}},
		struct {
			method string
			params map[string]any
		}{method: "textDocument/completion", params: map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position, "context": map[string]any{"triggerKind": 1}}},
	)
	for _, request := range requests {
		attempts := 1
		if server.name == "graphql" && request.method == "textDocument/documentSymbol" {
			attempts = 20
		}
		for attempt := 0; attempt < attempts; attempt++ {
			probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			result, err := client.request(probeCtx, request.method, request.params)
			cancel()
			if err == nil && realJSONNonEmpty(result) {
				return request.method, nil
			}
			if err == nil && attempt+1 < attempts {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if err == nil {
				if client.semanticResults == nil {
					client.semanticResults = make(map[string]string)
				}
				client.semanticResults[request.method] = string(result)
			} else {
				if client.semanticErrors == nil {
					client.semanticErrors = make(map[string]string)
				}
				client.semanticErrors[request.method] = err.Error()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return "", fmt.Errorf("%s timed out; pending server requests=%v results=%v", request.method, client.serverRequests, client.semanticResults)
			}
		}
	}
	if client.diagnostics > 0 {
		return "textDocument/publishDiagnostics", nil
	}
	return "", errors.New("documentSymbol, hover, completion, and publishDiagnostics were all empty")
}

type realNodeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *realNodeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *realNodeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type realLSPClient struct {
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          *bufio.Reader
	stderr          *realNodeBuffer
	writeMu         sync.Mutex
	nextID          int64
	diagnostics     int
	serverRequests  []string
	semanticResults map[string]string
	semanticErrors  map[string]string
	serverMessages  []string
	documents       map[string]string
}

func (c *realLSPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		raw, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, fmt.Errorf("decode LSP response: %w", err)
		}
		if message.Method != "" {
			c.serverRequests = append(c.serverRequests, message.Method)
			if message.Method == "tsserver/request" {
				return nil, errors.New("raw Vue handshake received tsserver/request; production MCP bridge must provide the real TypeScript response")
			}
			if message.Method == "markdown/parse" {
				if len(message.ID) == 0 || string(message.ID) == "null" {
					return nil, errors.New("markdown/parse server request has no id")
				}
				var parseParams struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(message.Params, &parseParams); err != nil {
					return nil, fmt.Errorf("decode markdown/parse params: %w", err)
				}
				if parseParams.Text == "" && c.documents != nil {
					parseParams.Text = c.documents[parseParams.URI]
				}
				if err := c.write(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(message.ID),
					"result":  realMarkdownTokens(parseParams.Text),
				}); err != nil {
					return nil, err
				}
				continue
			}
			if message.Method == "textDocument/publishDiagnostics" {
				var params struct {
					Diagnostics []json.RawMessage `json:"diagnostics"`
				}
				if json.Unmarshal(message.Params, &params) == nil {
					c.diagnostics += len(params.Diagnostics)
				}
			}
			if len(message.Params) != 0 && message.Method != "$/progress" {
				c.serverMessages = append(c.serverMessages, message.Method+" "+string(message.Params))
			}
			if len(message.ID) != 0 && string(message.ID) != "null" {
				result := any(nil)
				if message.Method == "workspace/configuration" {
					result = realWorkspaceConfiguration(message.Params)
				} else if message.Method == "workspace/workspaceFolders" {
					result = []any{}
				}
				if err := c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": result}); err != nil {
					return nil, err
				}
			}
			continue
		}
		if string(message.ID) != strconv.FormatInt(id, 10) {
			continue
		}
		if len(message.Error) != 0 && string(message.Error) != "null" {
			return nil, fmt.Errorf("LSP %s error: %s", method, message.Error)
		}
		return message.Result, nil
	}
}

func realWorkspaceSettings() map[string]any {
	return map[string]any{"settings": realMarkdownSettings()}
}

func realWorkspaceConfiguration(raw json.RawMessage) []any {
	var request struct {
		Items []struct {
			Section string `json:"section"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return []any{}
	}
	result := make([]any, 0, len(request.Items))
	for _, item := range request.Items {
		switch item.Section {
		case "graphql-config", "vscode-graphql":
			result = append(result, map[string]any{})
		default:
			result = append(result, realMarkdownSettings())
		}
	}
	return result
}

func realMarkdownSettings() map[string]any {
	return map[string]any{
		"markdown": map[string]any{
			"suggest": map[string]any{
				"paths": map[string]any{
					"enabled":                           true,
					"includeWorkspaceHeaderCompletions": "onSingleOrDoubleHash",
				},
			},
		},
	}
}

func realMarkdownTokens(text string) []map[string]any {
	lines := strings.Split(text, "\n")
	tokens := make([]map[string]any, 0, len(lines)*3)
	for lineNumber, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		headingLevel := 0
		for headingLevel < len(trimmed) && trimmed[headingLevel] == '#' {
			headingLevel++
		}
		if headingLevel > 0 && headingLevel <= 6 && headingLevel < len(trimmed) && trimmed[headingLevel] == ' ' {
			title := strings.TrimSpace(trimmed[headingLevel:])
			markup := strings.Repeat("#", headingLevel)
			tokens = append(tokens,
				map[string]any{"type": "heading_open", "tag": "h" + strconv.Itoa(headingLevel), "nesting": 1, "level": 0, "map": []int{lineNumber, lineNumber + 1}, "markup": markup, "block": true, "hidden": false},
				map[string]any{"type": "inline", "tag": "", "nesting": 0, "level": 1, "map": []int{lineNumber, lineNumber + 1}, "content": title, "children": []map[string]any{{"type": "text", "tag": "", "nesting": 0, "level": 0, "content": title}}},
				map[string]any{"type": "heading_close", "tag": "h" + strconv.Itoa(headingLevel), "nesting": -1, "level": 0, "markup": markup, "block": true, "hidden": false},
			)
			continue
		}
		tokens = append(tokens,
			map[string]any{"type": "paragraph_open", "tag": "p", "nesting": 1, "level": 0, "map": []int{lineNumber, lineNumber + 1}, "markup": "", "block": true, "hidden": false},
			map[string]any{"type": "inline", "tag": "", "nesting": 0, "level": 1, "map": []int{lineNumber, lineNumber + 1}, "content": line, "children": []map[string]any{{"type": "text", "tag": "", "nesting": 0, "level": 0, "content": line}}},
			map[string]any{"type": "paragraph_close", "tag": "p", "nesting": -1, "level": 0, "markup": "", "block": true, "hidden": false},
		)
	}
	return tokens
}

func (c *realLSPClient) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *realLSPClient) write(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(raw)); err != nil {
		return err
	}
	_, err = c.stdin.Write(raw)
	return err
}

func (c *realLSPClient) read(ctx context.Context) ([]byte, error) {
	type readResult struct {
		payload []byte
		err     error
	}
	results := make(chan readResult, 1)
	go func() {
		payload, err := readRealLSPFrame(c.stdout)
		results <- readResult{payload: payload, err: err}
	}()
	select {
	case result := <-results:
		return result.payload, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func readRealLSPFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("parse Content-Length: %w", err)
			}
		}
	}
	if contentLength < 0 {
		return nil, errors.New("LSP response has no Content-Length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *realLSPClient) close(t *testing.T) {
	t.Helper()
	if c == nil || c.cmd == nil {
		return
	}
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("real LSP server exited with %v; stderr=%s", err, c.stderr.String())
		}
	case <-time.After(15 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
		t.Errorf("real LSP server did not exit after exit notification; stderr=%s", c.stderr.String())
	}
}

func realJSONNonEmpty(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return realValueNonEmpty(value)
}

func realValueNonEmpty(value any) bool {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if realValueNonEmpty(item) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, item := range typed {
			if key == "isIncomplete" || key == "kind" || key == "range" || key == "selectionRange" {
				continue
			}
			if realValueNonEmpty(item) {
				return true
			}
		}
	case string:
		return strings.TrimSpace(typed) != ""
	case float64:
		return typed != 0
	case bool:
		return typed
	}
	return false
}

// TestRealMCPContractGuardsE2E 在不启动 Node/MCP 的情况下锁定矩阵合同；真实矩阵仍由上层 E2E 负责执行。
func TestRealMCPContractGuardsE2E(t *testing.T) {
	if got := realMCPExpectedMatrixActionTotal(16); got != 16*realMCPExpectedActionCount {
		t.Fatalf("base language matrix total = %d, want %d", got, 16*realMCPExpectedActionCount)
	}
	if got := realMCPExpectedMatrixActionTotal(16 + 1); got != 17*realMCPExpectedActionCount {
		t.Fatalf("base plus Vue companion matrix total = %d, want %d", got, 17*realMCPExpectedActionCount)
	}
	if got := realMCPPositionFromLSP("fixture.js", 2, 0); got != "fixture.js:2:1" {
		t.Fatalf("raw LSP character 0 converted to MCP position = %q, want fixture.js:2:1", got)
	}
	servers := realNodeServerCases()
	if len(servers) != 17 {
		t.Fatalf("real Node server count = %d, want 17", len(servers))
	}
	for _, server := range servers {
		server := server
		if server.name == "html" {
			content := realMCPFixtureContent(server)
			if !strings.Contains(content, "<html>\n") || !strings.Contains(content, "<main id=\"app\">\n") || strings.Count(content, "\n") < 8 {
				t.Fatalf("html folding fixture must keep nested tags on distinct lines, got %q", content)
			}
		}
		fixture := realMCPContractTestFixture(server)
		actions := realMCPActionSpecs(server, fixture, "ast_fixture.js")
		if err := validateRealMCPActionClosure(actions); err != nil {
			t.Fatalf("%s action closure: %v", server.languageID, err)
		}
		realFixture := writeRealMCPLanguageFixture(t, t.TempDir(), server)
		requireRealMCPFixturePositions(t, realFixture, server)
		for _, action := range actions {
			if action.tool == "diagnostics" && action.allowCapabilityUnsupported {
				t.Fatalf("%s/%s must not escape through capability_unsupported", action.tool, action.name)
			}
			if action.tool == "xref" && strings.HasPrefix(action.name, "type_hierarchy-") && !action.allowCapabilityUnsupported {
				t.Fatalf("%s/%s must preserve optional LSP capability_unsupported accounting", server.languageID, action.name)
			}
		}
	}

	server := servers[0]
	actions := realMCPActionSpecs(server, realMCPContractTestFixture(server), "ast_fixture.js")
	if err := validateRealMCPActionClosure(actions[:len(actions)-1]); err == nil {
		t.Fatal("shortened action matrix unexpectedly passed exact closure guard")
	}
	duplicated := append(slices.Clone(actions), actions[0])
	if err := validateRealMCPActionClosure(duplicated); err == nil {
		t.Fatal("duplicated action matrix unexpectedly passed exact closure guard")
	}
}

// TestRealMCPHierarchyDirectionContractsE2E 锁定带方向后缀的层级动作仍使用同一结果合同族。
// 该回归只构造 fixture 和合同，不启动 MCP/语言服务器，也不访问网络。
func TestRealMCPHierarchyDirectionContractsE2E(t *testing.T) {
	serversByName := make(map[string]realNodeServerCase)
	for _, server := range realNodeServerCases() {
		serversByName[server.name] = server
	}
	for _, fixtureCase := range []struct {
		name          string
		callHierarchy bool
		typeHierarchy bool
	}{
		{name: "svelte", callHierarchy: false, typeHierarchy: false},
		{name: "javascript", callHierarchy: true, typeHierarchy: true},
		{name: "typescript", callHierarchy: true, typeHierarchy: true},
	} {
		server, ok := serversByName[fixtureCase.name]
		if !ok {
			t.Fatalf("missing contract fixture server %q", fixtureCase.name)
		}
		if got := realMCPFixtureHasCallHierarchy(server); got != fixtureCase.callHierarchy {
			t.Fatalf("%s call hierarchy fixture support=%t, want %t", fixtureCase.name, got, fixtureCase.callHierarchy)
		}
		if got := realMCPFixtureHasInheritance(server); got != fixtureCase.typeHierarchy {
			t.Fatalf("%s type hierarchy fixture support=%t, want %t", fixtureCase.name, got, fixtureCase.typeHierarchy)
		}

		for _, direction := range []string{"incoming", "outgoing", "both"} {
			action := "call_hierarchy-" + direction
			requireResult, emptyReason := realMCPActionResultContract(server, "xref", action)
			if requireResult != fixtureCase.callHierarchy {
				t.Fatalf("%s/%s require_non_empty=%t, want %t", fixtureCase.name, action, requireResult, fixtureCase.callHierarchy)
			}
			if fixtureCase.callHierarchy && emptyReason != "" {
				t.Fatalf("%s/%s unexpectedly has legal-empty reason %q", fixtureCase.name, action, emptyReason)
			}
			if !fixtureCase.callHierarchy && strings.TrimSpace(emptyReason) == "" {
				t.Fatalf("%s/%s missing legal-empty reason", fixtureCase.name, action)
			}
			if got := realMCPActionAllowsCapabilityUnsupported(server, "xref", action); got != !fixtureCase.callHierarchy {
				t.Fatalf("%s/%s allow_capability_unsupported=%t, want %t", fixtureCase.name, action, got, !fixtureCase.callHierarchy)
			}
		}

		for _, direction := range []string{"supertypes", "subtypes", "both"} {
			action := "type_hierarchy-" + direction
			requireResult, emptyReason := realMCPActionResultContract(server, "xref", action)
			if requireResult != fixtureCase.typeHierarchy {
				t.Fatalf("%s/%s require_non_empty=%t, want %t", fixtureCase.name, action, requireResult, fixtureCase.typeHierarchy)
			}
			if fixtureCase.typeHierarchy && emptyReason != "" {
				t.Fatalf("%s/%s unexpectedly has legal-empty reason %q", fixtureCase.name, action, emptyReason)
			}
			if !fixtureCase.typeHierarchy && strings.TrimSpace(emptyReason) == "" {
				t.Fatalf("%s/%s missing legal-empty reason", fixtureCase.name, action)
			}
			if !realMCPActionAllowsCapabilityUnsupported(server, "xref", action) {
				t.Fatalf("%s/%s must preserve optional capability_unsupported accounting", fixtureCase.name, action)
			}
		}
	}

	javascript := serversByName["javascript"]
	actions := realMCPActionSpecs(javascript, realMCPContractTestFixture(javascript), "ast_fixture.js")
	if len(actions) != realMCPExpectedActionCount {
		t.Fatalf("hierarchy contract regression changed action count=%d, want %d", len(actions), realMCPExpectedActionCount)
	}
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("hierarchy contract regression changed 15-action closure: %v", err)
	}
}

// TestRealMCPCapabilityUnsupportedAccountingE2E 锁定 typed unsupported 必须有能力快照
// 证据；fixture 的静态预期不能单独把一次 action 记入 36 项账本。
func TestRealMCPCapabilityUnsupportedAccountingE2E(t *testing.T) {
	meta := map[string]any{
		"capabilities_known":  true,
		"capability_snapshot": "semantic_tokens=false,signature_help=false,references=false,completion=true",
	}
	for _, key := range []string{"semantic_tokens", "signature_help", "references"} {
		if !realMCPCapabilityUnsupportedAccounted(meta, key, false) {
			t.Fatalf("capability snapshot false must account typed unsupported for %s", key)
		}
	}
	if realMCPCapabilityUnsupportedAccounted(meta, "completion", false) {
		t.Fatal("capability snapshot true must not account typed unsupported")
	}
	if realMCPCapabilityUnsupportedAccounted(nil, "semantic_tokens", false) {
		t.Fatal("missing capability snapshot must not account fixture-only unsupported")
	}
	if !realMCPCapabilityUnsupportedAccounted(nil, "type_hierarchy", true) {
		t.Fatal("explicit protocol-optional fallback must account typed unsupported")
	}
	if got := realMCPActionCapabilityKey("structure", "semantic_tokens"); got != "semantic_tokens" {
		t.Fatalf("semantic_tokens capability key=%q", got)
	}
	if got := realMCPActionCapabilityKey("diagnostics", "diagnostics"); got != "diagnostics" {
		t.Fatalf("diagnostics capability key=%q", got)
	}

	var unsupported mcpLSPBinaryResponse
	if err := json.Unmarshal([]byte(`{"result":{"content":[{"type":"text","text":"ERROR code=capability_unsupported retryable=0\nMESSAGE\tmethod unavailable"}],"isError":true}}`), &unsupported); err != nil {
		t.Fatalf("decode content-only capability_unsupported fixture: %v", err)
	}
	if got := requireRealMCPActionResult(t, unsupported, true, "", true, "semantic_tokens", false, "semantic_tokens content-only unsupported"); got != realMCPActionUnsupported {
		t.Fatalf("typed capability_unsupported status=%q, want %q", got, realMCPActionUnsupported)
	}
	var remoteEmptyUnsupported mcpLSPBinaryResponse
	if err := json.Unmarshal([]byte(`{"result":{"content":[{"type":"text","text":"workspace symbol is not available for markdown. use structure action=document_symbol file_path=<markdown file>."}],"isError":false}}`), &remoteEmptyUnsupported); err != nil {
		t.Fatalf("decode remote content-only capability-empty fixture: %v", err)
	}
	if got := requireRealMCPActionResult(t, remoteEmptyUnsupported, false, "", true, "workspace_symbol", false, "remote content-only capability empty"); got != realMCPActionUnsupported {
		t.Fatalf("remote capability-empty status=%q, want %q", got, realMCPActionUnsupported)
	}

	var legalEmpty mcpLSPBinaryResponse
	if err := json.Unmarshal([]byte(`{"result":{"content":[{"type":"text","text":"OK total=0 showing=0 truncated=0 unit=diagnostic\nMESSAGE\tChecked file: fixture.go"}],"isError":false}}`), &legalEmpty); err != nil {
		t.Fatalf("decode content-only legal-empty fixture: %v", err)
	}
	if got := requireRealMCPActionResult(t, legalEmpty, false, "zero diagnostics is a legal empty response", false, "", false, "diagnostics legal-empty"); got != realMCPActionLegalEmpty {
		t.Fatalf("legal empty status=%q, want %q", got, realMCPActionLegalEmpty)
	}

	var semantic mcpLSPBinaryResponse
	if err := json.Unmarshal([]byte(`{"result":{"content":[{"type":"text","text":"OK total=1 showing=1 truncated=0 unit=location\nROW\tfile=fixture.go\tline=1\tcol=1"}],"isError":false}}`), &semantic); err != nil {
		t.Fatalf("decode content-only semantic fixture: %v", err)
	}
	if got := requireRealMCPActionResult(t, semantic, true, "", false, "references", false, "xref content-only semantic success"); got != realMCPActionSucceeded {
		t.Fatalf("content-only semantic status=%q, want %q", got, realMCPActionSucceeded)
	}
}

// TestWindowsRealMCPPublicToolArgumentSchemasE2E 锁定 Windows 真实矩阵对三个
// 公共工具族的参数合成边界。
func TestWindowsRealMCPPublicToolArgumentSchemasE2E(t *testing.T) {
	tests := []struct {
		name           string
		tool           string
		actionName     string
		args           map[string]any
		wantLanguageID bool
	}{
		{name: "xref", tool: "xref", actionName: "references", args: map[string]any{"action": "references"}, wantLanguageID: true},
		{name: "structure-document", tool: "structure", actionName: "document_symbol", args: map[string]any{"action": "document_symbol"}, wantLanguageID: true},
		{name: "structure-workspace-file", tool: "structure", actionName: "workspace_symbol-file", args: map[string]any{"action": "workspace_symbol", "file_path": "fixture.js"}},
		{name: "structure-workspace-language", tool: "structure", actionName: "workspace_symbol-language", args: map[string]any{"action": "workspace_symbol", "workspace_language": "javascript"}},
		{name: "diagnostics", tool: "diagnostics", actionName: "diagnostics", args: map[string]any{"file_path": "fixture.js"}, wantLanguageID: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := realMCPWindowsToolArguments("javascript", `C:\fixture`, tc.tool, tc.actionName, tc.args)
			_, hasLanguageID := got["language_id"]
			if hasLanguageID != tc.wantLanguageID {
				t.Fatalf("language_id present=%v, want %v; args=%#v", hasLanguageID, tc.wantLanguageID, got)
			}
			if got["work_dir"] != `C:\fixture` {
				t.Fatalf("work_dir=%#v, want Windows fixture root", got["work_dir"])
			}
			if _, mutated := tc.args["work_dir"]; mutated {
				t.Fatal("public action argument synthesis mutated the reusable action spec")
			}
			if tc.tool == "structure" && strings.HasPrefix(tc.actionName, "workspace_symbol-") {
				_, hasFilePath := got["file_path"]
				_, hasWorkspaceLanguage := got["workspace_language"]
				if hasFilePath == hasWorkspaceLanguage {
					t.Fatalf("workspace_symbol must carry exactly one scope selector; args=%#v", got)
				}
			}
		})
	}
}

// requireRealMCPFixturePositions 校验所有 MCP 1-based position 都落在对应 fixture 的真实行列内。
func requireRealMCPFixturePositions(t *testing.T, fixture realMCPFixture, server realNodeServerCase) {
	t.Helper()
	for _, position := range []string{
		fixture.semanticPosition, fixture.renamePosition, fixture.implementationPosition,
		fixture.typeDefinitionPosition, fixture.callHierarchyPosition, fixture.typeHierarchyPosition,
		fixture.signaturePosition, fixture.completionPosition, fixture.codeActionPosition,
	} {
		last := strings.LastIndex(position, ":")
		beforeColumn := position[:last]
		secondLast := strings.LastIndex(beforeColumn, ":")
		line, lineErr := strconv.Atoi(beforeColumn[secondLast+1:])
		column, columnErr := strconv.Atoi(position[last+1:])
		if lineErr != nil || columnErr != nil || line <= 0 || column <= 0 {
			t.Fatalf("%s has malformed MCP position %q", server.languageID, position)
		}
		path := realMCPPositionPath(position)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s position %q reads %s: %v", server.languageID, position, path, err)
		}
		lines := strings.Split(string(payload), "\n")
		if line > len(lines) || column > len(lines[line-1])+1 {
			t.Fatalf("%s MCP position %q is outside %s (line length=%d)", server.languageID, position, path, len(lines[line-1]))
		}
	}
}

func realMCPContractTestFixture(server realNodeServerCase) realMCPFixture {
	workspaceQuery := strings.TrimSpace(server.sourceWorkspaceQuery)
	if workspaceQuery == "" {
		workspaceQuery = realMCPWorkspaceQuery(server)
	}
	return realMCPFixture{
		searchNeedle:           "realMCPNeedle",
		replaceExpectation:     "REAL_MCP_REPLACED",
		targetFile:             server.fileName,
		secondaryFile:          "secondary." + server.fileName,
		replaceFile:            "replace." + server.fileName,
		renameFile:             "rename." + server.fileName,
		codeActionFile:         "code_action." + server.fileName,
		formatFile:             "format." + server.fileName,
		completionFile:         "completion." + server.fileName,
		workspaceQuery:         workspaceQuery,
		semanticPosition:       server.fileName + ":1:1",
		renamePosition:         server.fileName + ":1:1",
		implementationPosition: server.fileName + ":1:1",
		typeDefinitionPosition: server.fileName + ":1:1",
		callHierarchyPosition:  server.fileName + ":1:1",
		typeHierarchyPosition:  server.fileName + ":1:1",
		signaturePosition:      server.fileName + ":1:1",
		completionPosition:     server.fileName + ":1:1",
		codeActionPosition:     server.fileName + ":1:1",
	}
}

func nodePathForDist(nodeDist string) string {
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	return filepath.Join(nodeDist, name)
}

func realNodeEnvironment(base []string, nodeDist, installDir string) []string {
	path := nodeDist + string(os.PathListSeparator) + filepath.Join(installDir, "node_modules", ".bin")
	if nativePaths := realNodeAstGrepRuntimePaths(installDir); nativePaths != "" {
		path = nativePaths + string(os.PathListSeparator) + path
	}
	if existing := lookupEnv(base, "PATH"); existing != "" {
		path += string(os.PathListSeparator) + existing
	}
	// Windows cmd.exe resolves the conventional environment key as Path. Keep
	// the key's platform spelling after case-insensitive replacement so npm
	// lifecycle scripts can resolve `node` without creating duplicate PATH keys.
	base = replaceEnv(base, "Path", path)
	base = replaceEnv(base, "NODE_PATH", filepath.Join(installDir, "node_modules"))
	return base
}

func TestRealNodeEnvironmentReplacesWindowsPathCaseInsensitively(t *testing.T) {
	nodeDist := filepath.Join(t.TempDir(), "node")
	installDir := filepath.Join(t.TempDir(), "packages")
	env := realNodeEnvironment([]string{"Path=C:\\host-bin", "NODE_PATH=C:\\stale-modules"}, nodeDist, installDir)
	pathCount := 0
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") {
			pathCount++
		}
	}
	if pathCount != 1 {
		t.Fatalf("real Node environment PATH key count = %d, want exactly one case-insensitive key: %q", pathCount, env)
	}
	if got := lookupEnv(env, "PATH"); !slices.Contains(strings.Split(got, string(os.PathListSeparator)), nodeDist) || !strings.Contains(got, "C:\\host-bin") {
		t.Fatalf("real Node environment PATH = %q, want Node runtime and inherited mixed-case Path", got)
	}
}

func TestRealNodeEnvironmentCmdLifecycleResolvesManagedNode(t *testing.T) {
	nodeDist := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_NODE_DIST"))
	if nodeDist == "" {
		t.Skip("SUPER_DOLPHIN_NODE_DIST is required for the Windows cmd lifecycle probe")
	}
	nodePath := filepath.Join(nodeDist, "node.exe")
	if !fileExists(nodePath) {
		t.Fatalf("managed Node executable is missing: %s", nodePath)
	}
	env := realNodeEnvironment([]string{"Path=C:\\host-bin"}, nodeDist, t.TempDir())
	pathCount := 0
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") {
			pathCount++
		}
	}
	if pathCount != 1 {
		t.Fatalf("managed Node environment has %d PATH keys: %q", pathCount, env)
	}
	cmd := exec.Command("cmd.exe", "/d", "/s", "/c", "node --version")
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd.exe could not resolve managed node: %v; output=%s; env=%q", err, output, env)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(output)), "v") {
		t.Fatalf("cmd.exe node version output = %q", output)
	}
}

func realNodeAstGrepRuntimePaths(installDir string) string {
	paths := make([]string, 0, 2)
	for _, relative := range []string{"@ast-grep/cli", "@ast-grep/cli-win32-arm64-msvc", "@ast-grep/cli-win32-x64-msvc"} {
		dir := filepath.Join(installDir, "node_modules", filepath.FromSlash(relative))
		if !fileExists(filepath.Join(dir, "vcruntime140.dll")) {
			continue
		}
		paths = append(paths, dir)
	}
	if configured := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR")); configured != "" && fileExists(filepath.Join(configured, "vcruntime140.dll")) {
		paths = append(paths, configured)
	}
	if windir := strings.TrimSpace(os.Getenv("WINDIR")); windir != "" {
		system32 := filepath.Join(windir, "System32")
		if fileExists(filepath.Join(system32, "vcruntime140.dll")) {
			paths = append(paths, system32)
		}
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func lookupEnv(env []string, key string) string {
	for _, entry := range env {
		entryKey, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(entryKey, key) {
			return value
		}
	}
	return ""
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		entryKey, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.EqualFold(entryKey, key) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func processExists(pid int) bool {
	if runtime.GOOS == "windows" {
		output, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").CombinedOutput()
		return err == nil && bytes.Contains(output, []byte(strconv.Itoa(pid)))
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=").CombinedOutput()
	return err == nil && strings.TrimSpace(string(output)) != ""
}

func buildRealMcpLSPBinary(t *testing.T, root string) string {
	t.Helper()
	goBin := realBundledGo(t, root)
	if managedRoot := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_GO_SDK_ROOT")); managedRoot != "" {
		t.Setenv("GOROOT", managedRoot)
	}
	platformKey := realNodePlatformKey(t)
	outputName := "mcp-lsp-" + platformKey + "-real-node-e2e"
	if runtime.GOOS == "windows" {
		outputName += ".exe"
	}
	output := filepath.Join(t.TempDir(), outputName)
	cache := filepath.Join(root, ".build-cache", "go-cache")
	tmp := filepath.Join(root, ".build-cache", "go-tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatalf("create bundled Go temp directory: %v", err)
	}
	cmd := exec.Command(goBin, "build", "-o", output, "./cmd/mcp-lsp")
	cmd.Dir = root
	cmd.Env = replaceEnv(replaceEnv(os.Environ(), "GOCACHE", cache), "GOTMPDIR", tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd = exec.CommandContext(ctx, goBin, "build", "-o", output, "./cmd/mcp-lsp")
	cmd.Dir = root
	cmd.Env = replaceEnv(replaceEnv(os.Environ(), "GOCACHE", cache), "GOTMPDIR", tmp)
	result, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build mcp-lsp with bundled Go %s: %v\n%s", goBin, err, result)
	}
	return output
}

func realBundledGo(t *testing.T, root string) string {
	t.Helper()
	goBin, err := resolveRealBundledGo(root, os.Getenv("SUPER_DOLPHIN_GO_BIN"))
	if err != nil {
		t.Fatalf("resolve bundled Go: %v", err)
	}
	return goBin
}

func resolveRealBundledGo(root, configured string) (string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		info, err := os.Stat(value)
		if err != nil {
			return "", fmt.Errorf("SUPER_DOLPHIN_GO_BIN=%q is not a usable file: %w", value, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("SUPER_DOLPHIN_GO_BIN=%q is not a regular file", value)
		}
		return filepath.Clean(value), nil
	}
	cacheDir := filepath.Join(root, ".build-cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read bundled Go cache %s: %w", cacheDir, err)
		}
		return "", fmt.Errorf("bundled Go executable not found under %s; set SUPER_DOLPHIN_GO_BIN", cacheDir)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "go") {
			continue
		}
		for _, name := range []string{"go.exe", "go"} {
			candidate := filepath.Join(cacheDir, entry.Name(), "go", "bin", name)
			if fileExists(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("bundled Go executable not found under %s; set SUPER_DOLPHIN_GO_BIN", cacheDir)
}

func TestResolveRealBundledGoRejectsInvalidOverrideE2E(t *testing.T) {
	root := t.TempDir()
	cachedGo := filepath.Join(root, ".build-cache", "go1", "go", "bin", "go.exe")
	if err := os.MkdirAll(filepath.Dir(cachedGo), 0o700); err != nil {
		t.Fatalf("create cached Go fixture: %v", err)
	}
	if err := os.WriteFile(cachedGo, []byte("cached-go"), 0o700); err != nil {
		t.Fatalf("write cached Go fixture: %v", err)
	}
	missing := filepath.Join(root, "missing-go.exe")
	if _, err := resolveRealBundledGo(root, missing); err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_GO_BIN") {
		t.Fatalf("resolveRealBundledGo(missing override) error = %v, want immediate SUPER_DOLPHIN_GO_BIN failure", err)
	}
	directory := filepath.Join(root, "go-directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create invalid Go override directory: %v", err)
	}
	if _, err := resolveRealBundledGo(root, directory); err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_GO_BIN") {
		t.Fatalf("resolveRealBundledGo(directory override) error = %v, want immediate SUPER_DOLPHIN_GO_BIN failure", err)
	}
}

func TestResolveRealBundledGoUsesExplicitOverrideAndEmptyFallbackE2E(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "go.exe")
	if err := os.WriteFile(override, []byte("override-go"), 0o700); err != nil {
		t.Fatalf("write explicit Go override fixture: %v", err)
	}
	got, err := resolveRealBundledGo(root, override)
	if err != nil || filepath.Clean(got) != filepath.Clean(override) {
		t.Fatalf("resolveRealBundledGo(explicit override) = %q, %v; want %q", got, err, override)
	}

	cachedGo := filepath.Join(root, ".build-cache", "go1", "go", "bin", "go.exe")
	if err := os.MkdirAll(filepath.Dir(cachedGo), 0o700); err != nil {
		t.Fatalf("create empty-config cached Go fixture: %v", err)
	}
	if err := os.WriteFile(cachedGo, []byte("cached-go"), 0o700); err != nil {
		t.Fatalf("write empty-config cached Go fixture: %v", err)
	}
	got, err = resolveRealBundledGo(root, "")
	if err != nil || filepath.Clean(got) != filepath.Clean(cachedGo) {
		t.Fatalf("resolveRealBundledGo(empty override) = %q, %v; want %q", got, err, cachedGo)
	}
}

type realMCPFixture struct {
	workDir                string
	sourceRoot             string
	sourcePath             string
	sourceSecondaryPath    string
	searchNeedle           string
	replaceExpectation     string
	workspaceQuery         string
	readLine               int
	targetFile             string
	secondaryFile          string
	replaceFile            string
	renameFile             string
	codeActionFile         string
	formatFile             string
	completionFile         string
	semanticPosition       string
	renamePosition         string
	implementationPosition string
	typeDefinitionPosition string
	callHierarchyPosition  string
	typeHierarchyPosition  string
	signaturePosition      string
	completionPosition     string
	codeActionPosition     string
	replacePatch           string
}

type realMCPActionSpec struct {
	name                       string
	tool                       string
	args                       map[string]any
	requireResult              bool
	emptyResultReason          string
	allowCapabilityUnsupported bool
	contractSet                bool
}

const (
	realMCPExpectedLanguageCount = 17
	realMCPExpectedActionCount   = 15
)

// realMCPExpectedMatrixActionTotal 从实际拆分后的语言数推导矩阵总量，避免把 Vue companion
// 纳入正式矩阵后仍错误沿用基础语言数量的硬编码门禁。
func realMCPExpectedMatrixActionTotal(languageCount int) int {
	return languageCount * realMCPExpectedActionCount
}

var realMCPExpectedActionKeys = map[string]struct{}{
	"xref/references": {}, "xref/references-no-declaration": {}, "xref/call_hierarchy-incoming": {}, "xref/call_hierarchy-outgoing": {}, "xref/call_hierarchy-both": {}, "xref/type_hierarchy-supertypes": {}, "xref/type_hierarchy-subtypes": {},
	"xref/type_hierarchy-both":  {},
	"structure/document_symbol": {}, "structure/workspace_symbol-file": {}, "structure/workspace_symbol-language": {}, "structure/folding_range": {}, "structure/semantic_tokens": {},
	"diagnostics/diagnostics": {}, "diagnostics/diagnostics-batch": {},
}

var realMCPExpectedActionToolCounts = map[string]int{
	"structure": 5, "xref": 8, "diagnostics": 2,
}

// validateRealMCPActionClosure 校验 36 个 action 的精确键闭包、唯一性、工具分布和结果合同完整性。
func validateRealMCPActionClosure(actions []realMCPActionSpec) error {
	if len(actions) != realMCPExpectedActionCount {
		return fmt.Errorf("action count=%d, want exact %d", len(actions), realMCPExpectedActionCount)
	}
	seen := make(map[string]struct{}, len(actions))
	counts := make(map[string]int, len(realMCPExpectedActionToolCounts))
	for _, action := range actions {
		if strings.TrimSpace(action.name) == "" || strings.TrimSpace(action.tool) == "" {
			return fmt.Errorf("action has empty tool/name: %#v", action)
		}
		key := action.tool + "/" + action.name
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate action key %q", key)
		}
		seen[key] = struct{}{}
		if _, expected := realMCPExpectedActionKeys[key]; !expected {
			return fmt.Errorf("unexpected action key %q", key)
		}
		counts[action.tool]++
		if action.requireResult && strings.TrimSpace(action.emptyResultReason) != "" {
			return fmt.Errorf("%s marks both required and legal-empty", key)
		}
		if !action.requireResult && strings.TrimSpace(action.emptyResultReason) == "" {
			return fmt.Errorf("%s has neither required-result nor legal-empty reason", key)
		}
		if !action.contractSet {
			return fmt.Errorf("%s has no explicit capability contract", key)
		}
	}
	for key := range realMCPExpectedActionKeys {
		if _, present := seen[key]; !present {
			return fmt.Errorf("missing action key %q", key)
		}
	}
	if !maps.Equal(counts, realMCPExpectedActionToolCounts) {
		return fmt.Errorf("tool distribution=%v, want %v", counts, realMCPExpectedActionToolCounts)
	}
	return nil
}

type realMCPActionStatus string

const (
	realMCPActionSucceeded   realMCPActionStatus = "success"
	realMCPActionLegalEmpty  realMCPActionStatus = "legal_empty_success"
	realMCPActionUnsupported realMCPActionStatus = "capability_unsupported"
)

type realMCPMatrixSummary struct {
	total                 int
	succeeded             int
	legalEmpty            int
	capabilityUnsupported int
	unsupportedActions    []string
}

type realMCPProcessRecord struct {
	PID         int    `json:"ProcessId"`
	ParentPID   int    `json:"ParentProcessId"`
	Name        string `json:"Name"`
	CommandLine string `json:"CommandLine"`
}

type realMCPProcessIdentity struct {
	PID              int
	ParentPID        int
	ParentStartToken string
	StartToken       string
	Name             string
	CommandLine      string
	CommandSHA256    string
	ProcessHandle    windows.Handle
	Language         string
}

type realMCPProcessKey struct {
	PID        int
	StartToken string
}

const realMCPStillActiveExitCode = 259

var (
	realMCPObservedExitPID  atomic.Int64
	realMCPObservedExitCode atomic.Uint32
	realMCPObservedExitSet  atomic.Bool
)

// realMCPPowerShellExecutable 从 Win32 系统目录解析观察器，不读取 PATH。
// 正式自动安装 E2E 会故意给 sidecar 空 PATH；观察器使用绝对路径只保证取证可用，
// 不会改变已经启动的 sidecar 或其语言服务器子进程环境。
func realMCPPowerShellExecutable() (string, error) {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("resolve Windows system directory for MCP process observer: %w", err)
	}
	powershellPath := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
	info, err := os.Stat(powershellPath)
	if err != nil {
		return "", fmt.Errorf("inspect absolute Windows PowerShell observer %s: %w", powershellPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("absolute Windows PowerShell observer is a directory: %s", powershellPath)
	}
	return powershellPath, nil
}

// realMCPProcessTreeSnapshot 读取本次 MCP 进程的真实子孙树；不按进程名猜测或杀进程。
// realMCPProcessTreeSnapshot 通过父子关系枚举 MCP 或 raw Node 根进程可观察到的全部后代，并为每个 PID 绑定启动时间。
func realMCPProcessTreeSnapshot(rootPID int) ([]realMCPProcessIdentity, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	powershellPath, err := realMCPPowerShellExecutable()
	if err != nil {
		return nil, err
	}
	command := `$ErrorActionPreference = 'Stop'; @(Get-CimInstance -ClassName Win32_Process | Select-Object ProcessId, ParentProcessId, Name, CommandLine | ConvertTo-Json -Compress)`
	output, err := exec.Command(powershellPath, "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("enumerate Windows MCP process tree: %w: %s", err, strings.TrimSpace(string(output)))
	}
	raw := bytes.TrimSpace(output)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("enumerate Windows MCP process tree returned no process records")
	}
	var records []realMCPProcessRecord
	if raw[0] == '{' {
		var record realMCPProcessRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("decode one Windows MCP process record: %w; raw=%s", err, raw)
		}
		records = []realMCPProcessRecord{record}
	} else if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode Windows MCP process records: %w; raw=%s", err, raw)
	}
	children := make(map[int][]realMCPProcessRecord, len(records))
	for _, record := range records {
		if record.PID > 0 && record.ParentPID > 0 {
			children[record.ParentPID] = append(children[record.ParentPID], record)
		}
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool { return children[parent][i].PID < children[parent][j].PID })
	}
	queue := []int{rootPID}
	seen := map[int]bool{rootPID: true}
	startByPID := map[int]string{}
	identities := make([]realMCPProcessIdentity, 0)
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if seen[child.PID] {
				continue
			}
			seen[child.PID] = true
			start, startErr := windowsGoplsProcessStartIdentity(child.PID)
			if startErr != nil {
				alive, aliveErr := processAliveForE2E(child.PID)
				if aliveErr != nil {
					return nil, fmt.Errorf("inspect Windows MCP descendant PID %d start identity: %w", child.PID, errors.Join(startErr, aliveErr))
				}
				if alive {
					return nil, fmt.Errorf("inspect live Windows MCP descendant PID %d start identity: %w", child.PID, startErr)
				}
				continue
			}
			parentStart := startByPID[child.ParentPID]
			if parentStart == "" {
				parentStart = "unavailable"
			}
			startByPID[child.PID] = start
			var handle windows.Handle
			if realMCPRetainedHandlesEnabled() {
				var handleErr error
				handle, handleErr = retainRealMCPProcessHandle(child.PID)
				if handleErr != nil {
					return nil, fmt.Errorf("retain Windows MCP descendant PID %d handle: %w", child.PID, handleErr)
				}
			}
			identities = append(identities, realMCPProcessIdentity{
				PID: child.PID, ParentPID: child.ParentPID, ParentStartToken: parentStart,
				StartToken: start, Name: child.Name, CommandLine: child.CommandLine,
				CommandSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(child.CommandLine))),
				ProcessHandle: handle,
			})
			queue = append(queue, child.PID)
		}
	}
	return identities, nil
}

// trackRealMCPProcessTree 记录每次语言 action 观察到的真实子孙，并保留语言归属。
// trackRealMCPProcessTree 追加一次进程树快照；PID 复用由 PID+启动时间键隔离。
func trackRealMCPProcessTree(t *testing.T, rootPID int, language string, tracked map[realMCPProcessKey]realMCPProcessIdentity) bool {
	t.Helper()
	identities, err := realMCPProcessTreeSnapshot(rootPID)
	if err != nil {
		t.Errorf("capture real MCP process tree language=%s: %v", language, err)
		return false
	}
	for _, identity := range identities {
		key := realMCPProcessKey{PID: identity.PID, StartToken: identity.StartToken}
		identity.Language = language
		previous, ok := tracked[key]
		if ok && language != "final-before-close" && previous.Language != language && !strings.Contains(","+previous.Language+",", ","+language+",") {
			previous.Language += "," + language
			tracked[key] = previous
			continue
		}
		if !ok {
			tracked[key] = identity
		} else if identity.ProcessHandle != 0 {
			// 重复快照只保留基线句柄，临时句柄必须立即关闭，避免每分钟泄漏。
			_ = windows.CloseHandle(identity.ProcessHandle)
		}
	}
	t.Logf("real MCP process tree language=%s captured_descendants=%d tracked_total=%d", language, len(identities), len(tracked))
	return true
}

// requireRealMCPProcessIdentitiesGone 关闭 stdio 后按 PID 加启动时间逐一确认本次进程已消失。
// requireRealMCPProcessIdentitiesGone 在关闭后逐个确认 root/后代 PID+启动时间均不存在。
func requireRealMCPProcessIdentitiesGone(t *testing.T, tracked map[realMCPProcessKey]realMCPProcessIdentity) {
	t.Helper()
	defer closeRealMCPProcessHandles(tracked)
	if runtime.GOOS != "windows" {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		remaining := make([]realMCPProcessIdentity, 0)
		for key, identity := range tracked {
			alive, err := processAliveForE2E(key.PID)
			if err != nil {
				t.Fatalf("inspect exact Windows MCP process PID %d language=%s after close: %v", key.PID, identity.Language, err)
			}
			if !alive {
				continue
			}
			current, err := windowsGoplsProcessStartIdentity(key.PID)
			if err == nil && current != key.StartToken {
				// PID 已被新进程复用；启动时间不同表示本次 MCP 进程已退出。
				continue
			}
			remaining = append(remaining, identity)
		}
		if len(remaining) == 0 {
			return
		}
		if time.Now().After(deadline) {
			parts := make([]string, 0, len(remaining))
			for _, identity := range remaining {
				parts = append(parts, fmt.Sprintf("%s(pid=%d,start=%s,name=%s)", identity.Language, identity.PID, identity.StartToken, identity.Name))
			}
			sort.Strings(parts)
			t.Fatalf("exact Windows MCP process identities remained after close: %s", strings.Join(parts, "; "))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// realMCPRetainedHandlesEnabled 仅为 Node17 正式/合同 E2E 打开句柄，避免污染其他 E2E。
func realMCPRetainedHandlesEnabled() bool {
	return os.Getenv("MCP_LSP_REAL_NODE_WINDOWS_ARM64_PROCESS_ARM64_17X36_SOAK_15M") == "1" || os.Getenv("MCP_LSP_REAL_NODE_WINDOWS_ARM64_PROCESS_ARM64_HANDLE_TEST") == "1"
}

// retainRealMCPProcessHandle 为 Windows E2E 观察器保留查询与等待权限，直到生命周期证据闭包完成。
func retainRealMCPProcessHandle(pid int) (windows.Handle, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid process PID %d", pid)
	}
	return windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
}

// closeRealMCPProcessHandles 关闭 retained process handle；它只属于 Windows E2E 观察器。
func closeRealMCPProcessHandles(tracked map[realMCPProcessKey]realMCPProcessIdentity) {
	for key, identity := range tracked {
		if identity.ProcessHandle == 0 {
			continue
		}
		_ = windows.CloseHandle(identity.ProcessHandle)
		identity.ProcessHandle = 0
		tracked[key] = identity
	}
}

// realMCPRetainedExitCode 从 retained handle 读取已退出 child 的 Win32 exit code。
// STILL_ACTIVE 表示进程仍在运行；调用方不得把它当作退出码写入证据。
func realMCPRetainedExitCode(identity realMCPProcessIdentity) (uint32, bool, error) {
	if identity.ProcessHandle == 0 {
		return 0, false, errors.New("retained process handle is unavailable")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(identity.ProcessHandle, &code); err != nil {
		return 0, false, err
	}
	if code == realMCPStillActiveExitCode {
		return code, false, nil
	}
	return code, true, nil
}

// recordRealMCPObservedExit 只保留本轮首个 child 退出码，供 Node17 失败收据引用。
func recordRealMCPObservedExit(pid int, code uint32) {
	if realMCPObservedExitSet.CompareAndSwap(false, true) {
		realMCPObservedExitPID.Store(int64(pid))
		realMCPObservedExitCode.Store(code)
	}
}

func realMCPObservedExit() (int, uint32, bool) {
	if !realMCPObservedExitSet.Load() {
		return 0, 0, false
	}
	return int(realMCPObservedExitPID.Load()), realMCPObservedExitCode.Load(), true
}

// logRealMCPProcessIdentities 在关闭生产 MCP 前输出 PID、启动令牌和命令行，
// 让 Vue Node、TypeScript companion 与 typingsInstaller 的同 cohort 进程证据可复查。
func logRealMCPProcessIdentities(t *testing.T, tracked map[realMCPProcessKey]realMCPProcessIdentity) {
	t.Helper()
	identities := make([]realMCPProcessIdentity, 0, len(tracked))
	for _, identity := range tracked {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].PID != identities[j].PID {
			return identities[i].PID < identities[j].PID
		}
		return identities[i].StartToken < identities[j].StartToken
	})
	for _, identity := range identities {
		t.Logf("real MCP process identity language=%s pid=%d start=%s name=%s command=%s", identity.Language, identity.PID, identity.StartToken, identity.Name, identity.CommandLine)
	}
}

// requireRealMCPTypeScriptUserDataWithinRoot 检查所有真实 typingsInstaller 的命令行参数，
// 确认全局 typings cache 与 typesMap 都在本次 product root；只看到 Vue/TS PID
// 而没有 typingsInstaller 或只验证 product root 中另一参数，均不能算隔离证明。
func requireRealMCPTypeScriptUserDataWithinRoot(t *testing.T, tracked map[realMCPProcessKey]realMCPProcessIdentity, productRoot string) {
	t.Helper()
	if runtime.GOOS != "windows" || strings.TrimSpace(productRoot) == "" {
		return
	}
	foundTypingsInstaller := false
	for _, identity := range tracked {
		command := identity.CommandLine
		if !strings.Contains(strings.ToLower(command), "typingsinstaller.js") {
			continue
		}
		foundTypingsInstaller = true
		for _, flag := range []string{"--globalTypingsCacheLocation", "--typesMapLocation"} {
			value := realMCPCommandLineFlagValue(command, flag)
			if value == "" {
				t.Errorf("TypeScript typingsInstaller PID %d start=%s has no %s in command=%q", identity.PID, identity.StartToken, flag, command)
				continue
			}
			if !realMCPPathWithinRoot(productRoot, value) {
				t.Errorf("TypeScript typingsInstaller PID %d start=%s %s=%q escaped product root %q", identity.PID, identity.StartToken, flag, value, productRoot)
			}
		}
	}
	if !foundTypingsInstaller {
		t.Errorf("production process tree had no typingsInstaller.js descendant; TypeScript cache isolation proof is incomplete")
	}
}

func realMCPCommandLineFlagValue(command, flag string) string {
	index := strings.Index(strings.ToLower(command), strings.ToLower(flag))
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(command[index+len(flag):])
	if value == "" {
		return ""
	}
	if value[0] == '"' || value[0] == '\'' {
		quote := value[0]
		if end := strings.IndexByte(value[1:], quote); end >= 0 {
			return value[1 : end+1]
		}
		return ""
	}
	return strings.Fields(value)[0]
}

func realMCPPathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	candidate = filepath.Clean(filepath.FromSlash(strings.TrimSpace(candidate)))
	if root == "." || candidate == "." || !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func runRealMCPToolCoverageForServers(t *testing.T, root, binary, nodeDist, installDir string, servers []realNodeServerCase, expectedLanguageCount int) realMCPMatrixSummary {
	t.Helper()
	for _, server := range servers {
		if server.name == "vue" {
			t.Fatalf("raw MCP coverage helper cannot run Vue; use product-owned production bridge entrypoint")
		}
	}
	productRoot := strings.TrimSpace(os.Getenv(realNodeWindowsReuseProductRootEnv))
	if productRoot != "" {
		productRoot = filepath.Clean(productRoot)
		if !filepath.IsAbs(productRoot) {
			t.Fatalf("%s must be an absolute Windows product root: %q", realNodeWindowsReuseProductRootEnv, productRoot)
		}
		info, err := os.Stat(productRoot)
		if err != nil {
			t.Fatalf("stat reusable Windows product root %q: %v", productRoot, err)
		}
		if !info.IsDir() {
			t.Fatalf("reusable Windows product root %q is not a directory", productRoot)
		}
		t.Logf("reusing validated Windows product root for real MCP matrix: %s", productRoot)
	}
	return runRealMCPToolCoverageForServersWithProductRoot(t, root, binary, nodeDist, installDir, servers, expectedLanguageCount, productRoot)
}

func runRealMCPToolCoverageForServersWithProductRoot(t *testing.T, root, binary, nodeDist, installDir string, servers []realNodeServerCase, expectedLanguageCount int, productionProductRoot string, focusedActions ...string) realMCPMatrixSummary {
	t.Helper()
	fixtureRoot := t.TempDir()
	registerRealMCPTempRootCleanup(t, fixtureRoot)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, root, nodeDist, installDir, productionProductRoot)
	mcpPID := client.cmd.Process.Pid
	trackedProcesses := make(map[realMCPProcessKey]realMCPProcessIdentity)
	if runtime.GOOS == "windows" {
		start, err := windowsGoplsProcessStartIdentity(mcpPID)
		if err != nil {
			t.Fatalf("capture mcp-lsp root PID %d start identity: %v", mcpPID, err)
		}
		trackedProcesses[realMCPProcessKey{PID: mcpPID, StartToken: start}] = realMCPProcessIdentity{
			PID: mcpPID, StartToken: start, Name: "mcp-lsp", Language: "mcp-lsp",
		}
	}
	defer func() {
		treeCaptured := true
		if runtime.GOOS == "windows" {
			treeCaptured = trackRealMCPProcessTree(t, mcpPID, "final-before-close", trackedProcesses)
			if treeCaptured {
				if strings.TrimSpace(productionProductRoot) != "" && len(servers) == 1 && servers[0].name == "vue" {
					localAppData := filepath.Join(productionProductRoot, "runtime-state", "localappdata")
					appData := filepath.Join(productionProductRoot, "runtime-state", "appdata")
					for _, item := range []struct {
						label string
						path  string
					}{
						{label: "LOCALAPPDATA", path: localAppData},
						{label: "APPDATA", path: appData},
					} {
						info, err := os.Stat(item.path)
						if err != nil || !info.IsDir() {
							t.Errorf("production Vue %s directory was not created under product root %q: path=%q err=%v", item.label, productionProductRoot, item.path, err)
						}
					}
					t.Logf("production Vue scoped user-data env LOCALAPPDATA=%s APPDATA=%s", localAppData, appData)
				}
				if strings.TrimSpace(productionProductRoot) != "" && len(servers) == 1 && servers[0].name == "vue" {
					requireRealMCPTypeScriptUserDataWithinRoot(t, trackedProcesses, productionProductRoot)
				}
				logRealMCPProcessIdentities(t, trackedProcesses)
			}
		}
		client.close(t)
		if runtime.GOOS == "windows" {
			if !treeCaptured {
				t.Errorf("real MCP process tree snapshot failed; zero-residual proof is incomplete")
			}
			if len(trackedProcesses) <= 1 {
				t.Errorf("real MCP process tree captured no server/node descendant; tracked=%d", len(trackedProcesses))
			}
			requireRealMCPProcessIdentitiesGone(t, trackedProcesses)
		} else if processExists(mcpPID) {
			t.Errorf("mcp-lsp PID %d remains after stdio exit", mcpPID)
		}
	}()
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))

	platform := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		host, err := installer.DetectWindowsHostPlatform()
		if err != nil {
			t.Fatalf("detect Windows native/process architecture for MCP matrix: %v", err)
		}
		platform = fmt.Sprintf("windows-native-%s-process-%s", host.NativeArch, host.ProcessArch)
	}
	var matrix realMCPMatrixSummary
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != expectedLanguageCount {
		t.Fatalf("real MCP matrix languages=%d, want exact %d", len(servers), expectedLanguageCount)
	}
	for _, server := range servers {
		server := server
		var languageSummary realMCPMatrixSummary
		t.Run(platform+"/"+server.languageID, func(t *testing.T) {
			fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
			assertRealMCPNativeFixtureInputs(t, fixture)
			actions := realMCPActionSpecs(server, fixture, "")
			if err := validateRealMCPActionClosure(actions); err != nil {
				t.Fatalf("%s action closure: %v", server.languageID, err)
			}
			if len(focusedActions) > 0 {
				allowed := make(map[string]struct{}, len(focusedActions))
				for _, action := range focusedActions {
					allowed[action] = struct{}{}
				}
				filtered := actions[:0]
				for _, action := range actions {
					if _, ok := allowed[action.tool+"/"+action.name]; ok {
						filtered = append(filtered, action)
					}
				}
				actions = filtered
			}
			languageSummary.total = len(actions)
			for _, action := range actions {
				action := action
				t.Run(action.tool+"/"+action.name, func(t *testing.T) {
					requestArgs := realMCPWindowsToolArguments(server.languageID, fixture.workDir, action.tool, action.name, action.args)
					response := client.callTool(t, action.tool, requestArgs)
					status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptionalForServer(server, action.tool, action.name), server.languageID+" "+action.tool+" "+action.name)
					switch status {
					case realMCPActionSucceeded:
						languageSummary.succeeded++
					case realMCPActionLegalEmpty:
						// 合法空单独记账；聚合门禁显式把它计入闭合，不冒充非空语义成功。
						languageSummary.legalEmpty++
					case realMCPActionUnsupported:
						languageSummary.capabilityUnsupported++
						languageSummary.unsupportedActions = append(languageSummary.unsupportedActions, action.tool+"/"+action.name)
					default:
						t.Fatalf("%s returned unclassified action status %q", action.tool+"/"+action.name, status)
					}
				})
			}
			t.Logf("real MCP action matrix platform=%s language=%s total=%d success=%d legal_empty=%d capability_unsupported=%d unsupported_actions=%v",
				platform, server.languageID, languageSummary.total, languageSummary.succeeded, languageSummary.legalEmpty, languageSummary.capabilityUnsupported, languageSummary.unsupportedActions)
			if runtime.GOOS == "windows" {
				trackRealMCPProcessTree(t, mcpPID, server.languageID, trackedProcesses)
			}
		})
		matrix.total += languageSummary.total
		matrix.succeeded += languageSummary.succeeded
		matrix.legalEmpty += languageSummary.legalEmpty
		matrix.capabilityUnsupported += languageSummary.capabilityUnsupported
		matrix.unsupportedActions = append(matrix.unsupportedActions, languageSummary.unsupportedActions...)
	}
	expectedActionCount := realMCPExpectedActionCount
	if len(focusedActions) > 0 {
		expectedActionCount = len(focusedActions)
	}
	if matrix.total != expectedLanguageCount*expectedActionCount {
		t.Fatalf("real MCP matrix total=%d, want exact %d languages x %d actions", matrix.total, expectedLanguageCount, expectedActionCount)
	}
	t.Logf("real MCP action matrix summary platform=%s languages=%d actions=%d success=%d legal_empty=%d capability_unsupported=%d unsupported_actions=%v tracked_processes=%d mcp_pid=%d",
		platform, len(servers), matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported, matrix.unsupportedActions, len(trackedProcesses), mcpPID)
	// 这条真实 E2E 是“七族全部小动作均可调用”的交付门禁。LSP 可选能力不由
	// MCP 层伪造：明确且预期的 capability_unsupported 单独记账，不算语义 PASS；
	// schema、运行时、ACL、超时、进程退出或未分类错误均不会进入这两个计数。
	if matrix.succeeded+matrix.legalEmpty+matrix.capabilityUnsupported != matrix.total {
		t.Fatalf("real MCP action matrix has unaccounted failures on %s: total=%d success=%d legal_empty=%d capability_unsupported=%d unsupported_actions=%v",
			platform, matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported, matrix.unsupportedActions)
	}
	return matrix
}

// realMCPWindowsToolArguments 为 Windows 真实矩阵克隆并补齐公共工具参数。
// grep 的公开 schema 没有 language_id；structure/workspace_symbol 的 file_path
// 与 language 必须二选一，因此两种 workspace action 都使用自身显式作用域。
func realMCPWindowsToolArguments(languageID, workDir, tool, actionName string, args map[string]any) map[string]any {
	result := maps.Clone(args)
	result["work_dir"] = workDir
	if tool == "diagnostics" || tool == "xref" || (tool == "structure" && actionName != "workspace_symbol-language") {
		result["language_id"] = languageID
	}
	return result
}

// writeRealMCPLanguageFixture 为每个真实 Node server 建立独立文件。
// 文件准备、读取和断言均由测试进程原生完成，MCP 只承担三种语义请求。
func writeRealMCPLanguageFixture(t *testing.T, root string, server realNodeServerCase) realMCPFixture {
	t.Helper()
	if strings.TrimSpace(server.sourceDir) != "" {
		return writeRealMCPBinSourceFixture(t, root, server)
	}
	if strings.TrimSpace(server.packageName) != "" {
		t.Fatalf("%s real Node server is missing its bin/LSP/test source mapping", server.languageID)
	}
	if server.name == "prisma" {
		return writeRealMCPPrismaLanguageFixture(t, root, server)
	}
	if server.name == "rust" {
		return writeRealMCPRustLanguageFixture(t, root, server)
	}
	target := filepath.Join(root, server.fileName)
	content := realMCPFixtureContent(server)
	writeRealFixture(t, target, content)
	secondary := realMCPFixturePath(root, server, "secondary")
	writeRealFixture(t, secondary, content+"\nrealMCPNeedle_"+server.languageID+"\n")
	replace := realMCPFixturePath(root, server, "replace")
	writeRealFixture(t, replace, "REAL_MCP_REPLACE_ME\n")
	rename := realMCPFixturePath(root, server, "rename")
	writeRealFixture(t, rename, content)
	codeAction := realMCPFixturePath(root, server, "code_action")
	writeRealFixture(t, codeAction, realMCPCodeActionFixtureContent(server))
	format := realMCPFixturePath(root, server, "format")
	writeRealFixture(t, format, content)
	completion := realMCPFixturePath(root, server, "completion")
	completionContent, completionLine, completionCharacter := realMCPCompletionFixtureContent(server)
	writeRealFixture(t, completion, completionContent)
	if server.name == "vue" {
		writeRealFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"allowJs":true,"jsx":"preserve","module":"ESNext","moduleResolution":"Bundler","target":"ESNext","strict":true},"include":["*.vue"]}`)
	}
	if server.name == "graphql" {
		writeRealFixture(t, filepath.Join(root, "schema.graphql"), "type User { id: ID! name: String }\ntype Query { user: User }\n")
		writeRealFixture(t, filepath.Join(root, ".graphqlrc.yml"), "schema: schema.graphql\ndocuments: '*.graphql'\n")
	}
	semanticPosition := realMCPPositionFromLSP(target, server.line, server.character)
	renamePosition := realMCPPositionFromLSP(rename, server.line, server.character)
	implementationPosition := semanticPosition
	typeDefinitionPosition := semanticPosition
	callHierarchyPosition := semanticPosition
	typeHierarchyPosition := semanticPosition
	signaturePosition := semanticPosition
	completionPosition := realMCPPositionFromLSP(completion, completionLine, completionCharacter)
	codeActionPosition := realMCPPosition(codeAction, 1, 1)
	switch server.name {
	case "html":
		semanticPosition = realMCPPositionFromLSP(target, 4, 10) // id attribute in the real multiline <main> element.
		renamePosition = realMCPPositionFromLSP(rename, 4, 10)
		completionPosition = realMCPPositionFromLSP(completion, 4, 10)
	case "markdown":
		semanticPosition = realMCPPositionFromLSP(target, 3, 3) // Section heading text.
		renamePosition = realMCPPositionFromLSP(rename, 3, 3)
		completionPosition = realMCPPositionFromLSP(completion, 3, 3)
	case "vue":
		semanticPosition = realMCPPositionFromLSP(target, 9, 31) // MCP 第 9 行的 message 使用点（UTF-16 character=31）；声明和模板使用点仍是真实引用。
		renamePosition = realMCPPositionFromLSP(rename, 9, 31)
		completionPosition = realMCPPositionFromLSP(completion, 9, 31)
	case "graphql":
		semanticPosition = realMCPPositionFromLSP(target, 3, 17) // user field has a schema definition and query use.
		renamePosition = realMCPPositionFromLSP(rename, 3, 17)
		completionPosition = realMCPPositionFromLSP(completion, 3, 17)
		typeDefinitionPosition = semanticPosition
	case "javascript":
		implementationPosition = realMCPPositionFromLSP(target, 6, 6)
		typeHierarchyPosition = implementationPosition
		signaturePosition = realMCPPositionFromLSP(target, 3, 18)
	case "javascriptreact":
		signaturePosition = realMCPPositionFromLSP(target, 4, 15)
	case "typescript", "typescriptreact":
		implementationPosition = realMCPPositionFromLSP(target, 1, 10)
		typeHierarchyPosition = implementationPosition
		if server.name == "typescript" {
			typeDefinitionPosition = realMCPPositionFromLSP(target, 3, 30)
			signaturePosition = realMCPPositionFromLSP(target, 4, 18)
		} else {
			typeDefinitionPosition = realMCPPositionFromLSP(target, 2, 25)
			signaturePosition = realMCPPositionFromLSP(target, 8, 15)
		}
	case "python":
		implementationPosition = realMCPPositionFromLSP(target, 13, 6)
		typeHierarchyPosition = implementationPosition
		signaturePosition = realMCPPositionFromLSP(target, 5, 12)
	case "prisma":
		// MCP 行号为 1-based；character=13 落在自然 schema 的 User token 内。
		typeDefinitionPosition = realMCPPositionFromLSP(target, 16, 13)
	case "php":
		implementationPosition = realMCPPositionFromLSP(target, 5, 10)
		typeHierarchyPosition = implementationPosition
		signaturePosition = realMCPPositionFromLSP(target, 3, 11)
	case "shellscript":
		signaturePosition = realMCPPositionFromLSP(target, 3, 0)
	}
	return realMCPFixture{
		targetFile:             target,
		secondaryFile:          secondary,
		replaceFile:            replace,
		renameFile:             rename,
		codeActionFile:         codeAction,
		formatFile:             format,
		completionFile:         completion,
		semanticPosition:       semanticPosition,
		renamePosition:         renamePosition,
		implementationPosition: implementationPosition,
		typeDefinitionPosition: typeDefinitionPosition,
		callHierarchyPosition:  callHierarchyPosition,
		typeHierarchyPosition:  typeHierarchyPosition,
		signaturePosition:      signaturePosition,
		completionPosition:     completionPosition,
		codeActionPosition:     codeActionPosition,
		replacePatch:           "@@\n-REAL_MCP_REPLACE_ME\n+REAL_MCP_REPLACED\n",
	}
}

// writeRealMCPBinSourceFixture 把 bin/LSP/test 的完整语言快照复制到临时
// workspace。所有初始文件内容均来自快照；读取、搜索和写入准备由测试进程原生完成。
func writeRealMCPBinSourceFixture(t *testing.T, root string, server realNodeServerCase) realMCPFixture {
	t.Helper()
	repoRoot := realNodeRepoRoot(t)
	sourceRoot := filepath.Join(repoRoot, "bin", "LSP", "test")
	sourceDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(server.sourceDir)))
	if sourceDir == "." || filepath.IsAbs(sourceDir) {
		t.Fatalf("%s bin/LSP/test source directory must be relative: %q", server.languageID, server.sourceDir)
	}
	sourceProjectRoot := filepath.Join(sourceRoot, sourceDir)
	workspaceRoot := filepath.Join(root, server.name)
	// 必须先验证源目录和隔离目标的边界，再读取或复制任何文件；错误映射不能先
	// 扫描 bin/LSP/test 外部内容，错误 server 名也不能写出本次临时根。
	if !realMCPPathWithinRoot(sourceRoot, sourceProjectRoot) {
		t.Fatalf("%s source project escaped bin/LSP/test: %q", server.languageID, sourceProjectRoot)
	}
	if !realMCPPathWithinRoot(root, workspaceRoot) {
		t.Fatalf("%s isolated workspace escaped fixture root: %q", server.languageID, workspaceRoot)
	}
	sourcePath, _, err := realMCPNodeSourceMapping(sourceRoot, server)
	if err != nil {
		t.Fatalf("%s validate bin/LSP/test semantic mapping: %v", server.languageID, err)
	}
	copyRealMCPBinSourceTree(t, sourceProjectRoot, workspaceRoot)

	target := filepath.Join(workspaceRoot, filepath.FromSlash(server.sourceFile))
	if !realMCPPathWithinRoot(sourceRoot, sourcePath) || !realMCPPathWithinRoot(workspaceRoot, target) {
		t.Fatalf("%s source/target path escaped its root: source=%q target=%q", server.languageID, sourcePath, target)
	}
	languageWorkspaceRoot := workspaceRoot
	if server.name == "swift" {
		// Swift resolves a file to the nearest Package.swift. Keep every action
		// copy under the package containing the real target, so one MCP client
		// is reused instead of creating a second workspace client.
		languageWorkspaceRoot = filepath.Dir(filepath.Dir(filepath.Dir(target)))
		if !realMCPPathWithinRoot(workspaceRoot, languageWorkspaceRoot) {
			t.Fatalf("%s language workspace escaped isolated root: %q", server.languageID, languageWorkspaceRoot)
		}
	}
	workspaceQuery := strings.TrimSpace(server.sourceWorkspaceQuery)

	if strings.TrimSpace(server.sourceSecondaryFile) == "" {
		t.Fatalf("%s secondary source mapping is empty", server.languageID)
	}
	secondarySourcePath := filepath.Join(sourceProjectRoot, filepath.FromSlash(server.sourceSecondaryFile))
	if !realMCPPathWithinRoot(sourceProjectRoot, secondarySourcePath) {
		t.Fatalf("%s secondary source path escaped language snapshot: %q", server.languageID, secondarySourcePath)
	}
	if _, err := os.Stat(secondarySourcePath); err != nil {
		t.Fatalf("%s secondary source file %q is unavailable: %v", server.languageID, secondarySourcePath, err)
	}
	secondary := filepath.Join(languageWorkspaceRoot, ".mcp-secondary", filepath.Base(secondarySourcePath))
	secondaryRelative, err := filepath.Rel(sourceProjectRoot, secondarySourcePath)
	if err != nil {
		t.Fatalf("resolve %s secondary source relative path: %v", server.languageID, err)
	}
	copyRealMCPBinSourceFileWithinRoot(t, sourceRoot, filepath.ToSlash(filepath.Join(server.sourceDir, secondaryRelative)), workspaceRoot, secondary)
	secondaryBytes := readRealMCPBinSourceFile(t, secondarySourcePath)
	searchNeedle := realMCPSourceNeedle(string(secondaryBytes))
	if searchNeedle == "" {
		t.Fatalf("%s secondary source fixture has no stable search token", server.languageID)
	}

	copyAction := func(name string) string {
		if name == "rename" && (server.name == "javascript" || server.name == "javascriptreact" || server.name == "typescript" || server.name == "typescriptreact") {
			// Keep the rename action in a complete isolated project. The real
			// TypeScript fixture has consumers outside the declaration file,
			// including mathematic.test.ts excluded by tsconfig; copying only the
			// declaration would make a false single-file rename pass.
			actionRoot := filepath.Join(workspaceRoot, ".mcp-actions", name)
			copyRealMCPBinSourceTree(t, sourceProjectRoot, actionRoot)
			return filepath.Join(actionRoot, filepath.FromSlash(server.sourceFile))
		}
		actionRoot := filepath.Join(workspaceRoot, ".mcp-actions", name)
		if server.name == "swift" {
			actionRoot = filepath.Join(languageWorkspaceRoot, ".mcp-actions", name)
		}
		destination := filepath.Join(actionRoot, filepath.Base(target))
		copyRealMCPBinSourceFileWithinRoot(t, sourceRoot, filepath.ToSlash(filepath.Join(server.sourceDir, server.sourceFile)), workspaceRoot, destination)
		return destination
	}
	replace := copyAction("replace")
	replaceBytes := readRealMCPBinSourceFile(t, replace)
	replaceLine, _ := realMCPSourcePosition(string(replaceBytes))
	oldLine := strings.Split(strings.ReplaceAll(string(replaceBytes), "\r\n", "\n"), "\n")[replaceLine-1]
	replaceExpectation := "REAL_MCP_REPLACED"
	replacePatch := "@@\n-" + oldLine + "\n+" + replaceExpectation + "\n"
	rename := copyAction("rename")
	codeAction := copyAction("code_action")
	format := copyAction("format")
	completion := copyAction("completion")
	semanticPosition := realMCPPositionFromLSP(target, server.sourceLine, server.sourceCharacter)
	completionPosition := realMCPPositionFromLSP(completion, server.sourceLine, server.sourceCharacter)
	if server.languageID == "json" {
		completionPosition = realMCPPositionFromLSP(completion, 6, 5)
	}
	return realMCPFixture{
		workDir:                workspaceRoot,
		sourceRoot:             sourceRoot,
		sourcePath:             sourcePath,
		sourceSecondaryPath:    secondarySourcePath,
		searchNeedle:           searchNeedle,
		replaceExpectation:     replaceExpectation,
		workspaceQuery:         workspaceQuery,
		readLine:               server.sourceLine,
		targetFile:             target,
		secondaryFile:          secondary,
		replaceFile:            replace,
		renameFile:             rename,
		codeActionFile:         codeAction,
		formatFile:             format,
		completionFile:         completion,
		semanticPosition:       semanticPosition,
		renamePosition:         realMCPPositionFromLSP(rename, server.sourceLine, server.sourceCharacter),
		implementationPosition: semanticPosition,
		typeDefinitionPosition: semanticPosition,
		callHierarchyPosition:  semanticPosition,
		typeHierarchyPosition:  semanticPosition,
		signaturePosition:      semanticPosition,
		completionPosition:     completionPosition,
		codeActionPosition:     realMCPPosition(codeAction, 1, 1),
		replacePatch:           replacePatch,
	}
}

// writeRealMCPRustLanguageFixture 建立最小真实 Cargo workspace，使 rust-analyzer
// 能加载 crate graph 并对引用符号提供 hover/definition；不把 workspace 外的空结果伪装成成功。
func writeRealMCPRustLanguageFixture(t *testing.T, root string, server realNodeServerCase) realMCPFixture {
	t.Helper()
	writeRealFixture(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"real-mcp-rust-fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[workspace]\nresolver = \"2\"\n")
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o700); err != nil {
		t.Fatalf("create Rust workspace src: %v", err)
	}
	target := filepath.Join(srcRoot, "main.rs")
	content := "pub struct RealMcpTarget { pub value: i32 }\nfn main() {\n    let target = RealMcpTarget { value: 1 };\n    println!(\"{}\", target.value);\n}\n"
	writeRealFixture(t, target, content)
	// rust-analyzer 的 Windows ARM64 bundle 不保证附带 cargo；保留 Cargo.toml
	// 作为真实 workspace 根，并提供同一 crate 的 rust-project 描述，让语义索引不依赖机器级 cargo。
	rustProject := map[string]any{"crates": []any{map[string]any{
		"root_module": target,
		"edition":     "2021",
		"deps":        []any{},
		"cfg":         []string{},
	}}}
	rustProjectBytes, err := json.Marshal(rustProject)
	if err != nil {
		t.Fatalf("marshal Rust project: %v", err)
	}
	writeRealFixture(t, filepath.Join(root, "rust-project.json"), string(rustProjectBytes)+"\n")
	secondary := filepath.Join(srcRoot, "secondary.rs")
	writeRealFixture(t, secondary, "pub fn real_mcp_secondary() -> i32 { 2 }\n")
	replace := filepath.Join(srcRoot, "replace.rs")
	writeRealFixture(t, replace, "pub fn real_mcp_replace() { /* REAL_MCP_REPLACE_ME */ }\n")
	rename := filepath.Join(srcRoot, "rename.rs")
	writeRealFixture(t, rename, content)
	codeAction := filepath.Join(srcRoot, "code_action.rs")
	writeRealFixture(t, codeAction, content)
	format := filepath.Join(srcRoot, "format.rs")
	writeRealFixture(t, format, content)
	completion := filepath.Join(srcRoot, "completion.rs")
	writeRealFixture(t, completion, content)
	// line 3, character 18 targets the RealMcpTarget type, not the preceding space.
	position := realMCPPositionFromLSP(target, 3, 18)
	return realMCPFixture{
		targetFile: target, secondaryFile: secondary, replaceFile: replace, renameFile: rename,
		codeActionFile: codeAction, formatFile: format, completionFile: completion,
		semanticPosition: position, renamePosition: position, implementationPosition: position,
		typeDefinitionPosition: position, callHierarchyPosition: position, typeHierarchyPosition: position,
		signaturePosition: position, completionPosition: position, codeActionPosition: position,
		replacePatch: "@@\n-pub fn real_mcp_replace() { /* REAL_MCP_REPLACE_ME */ }\n+pub fn real_mcp_replace() { /* REAL_MCP_REPLACED */ }\n",
	}
}

// writeRealMCPPrismaLanguageFixture 为 Prisma 写入互不重名且逐文件闭合的自然 schema。
// 这些 fixture 防止诊断、格式化或补全文件的 parser state 泄漏到下一文档，同时保留真实定义、引用与重命名关系。
func writeRealMCPPrismaLanguageFixture(t *testing.T, root string, server realNodeServerCase) realMCPFixture {
	t.Helper()
	// Prisma 会按当前文档目录加载关联 schema；每个动作必须使用独立目录，避免故意
	// 损坏的 code_action schema 或其他动作的文档进入语义 target 的 multi-file 集合。
	target := filepath.Join(root, "semantic", server.fileName)
	writeRealFixture(t, target, realMCPPrismaNaturalFixture)
	secondary := realMCPFixturePath(root, server, "secondary")
	writeRealFixture(t, secondary, "model RealMCPSecondary {\n  id                   Int    @id\n  realMCPNeedle_prisma String\n}\n")
	replace := realMCPFixturePath(root, server, "replace")
	writeRealFixture(t, replace, "model RealMCPReplace {\n  id    Int    @id\n  value String // REAL_MCP_REPLACE_ME\n}\n")
	rename := realMCPFixturePath(root, server, "rename")
	renameContent := "model RealMCPRenameTarget {\n  id       Int                @id\n  children RealMCPRenameUse[]\n}\nmodel RealMCPRenameUse {\n  id       Int                 @id\n  parent   RealMCPRenameTarget @relation(fields: [parentId], references: [id])\n  parentId Int\n}\n"
	writeRealFixture(t, rename, renameContent)
	codeAction := realMCPFixturePath(root, server, "code_action")
	codeActionContent := realMCPCodeActionFixtureContent(server)
	writeRealFixture(t, codeAction, codeActionContent)
	format := realMCPFixturePath(root, server, "format")
	writeRealFixture(t, format, "model RealMCPFormat {\n id Int @id\n value String\n}\n")
	completion := realMCPFixturePath(root, server, "completion")
	writeRealFixture(t, completion, "model RealMCPCompletion {\n  id    Int    @id\n  value String\n}\n")
	// server.line 使用 MCP 的 1-based 行号；server.character 保留 raw LSP 的 0-based 字符列。
	// Prisma 语义动作必须落在自然 fixture 的真实 User token 内；空格负控由 raw 矩阵专门覆盖。
	semantic := realMCPPositionFromLSP(target, 16, 13)
	codeActionLine := strings.Split(codeActionContent, "\n")[1]
	return realMCPFixture{
		targetFile: target, secondaryFile: secondary, replaceFile: replace, renameFile: rename,
		codeActionFile: codeAction, formatFile: format, completionFile: completion,
		semanticPosition: semantic, renamePosition: realMCPPositionFromLSP(rename, 1, 6),
		implementationPosition: semantic, typeDefinitionPosition: semantic,
		callHierarchyPosition: semantic, typeHierarchyPosition: semantic, signaturePosition: semantic,
		completionPosition: realMCPPositionFromLSP(completion, 3, 2),
		codeActionPosition: realMCPPositionFromLSP(codeAction, 2, strings.Index(codeActionLine, "@unknown")),
		replacePatch:       "@@\n-  value String // REAL_MCP_REPLACE_ME\n+  value String // REAL_MCP_REPLACED\n",
	}
}

// TestRealMCPPrismaWindowsFixturesCloseBlocksBeforeDocumentBoundary 锁定本次真实崩溃的输入根因。
// 上游解析器会为每个文档重置 lineIndex；任一未闭合 block 都可能在下一文档第 0 行生成 -1 range。
func TestRealMCPPrismaWindowsFixturesCloseBlocksBeforeDocumentBoundary(t *testing.T) {
	server := realNodeServerCasesForLanguage("prisma")[0]
	fixture := writeRealMCPPrismaLanguageFixture(t, t.TempDir(), server)
	paths := []string{fixture.targetFile, fixture.secondaryFile, fixture.replaceFile, fixture.renameFile, fixture.codeActionFile, fixture.formatFile, fixture.completionFile}
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read Prisma fixture %s: %v", filepath.Base(path), err)
		}
		if err := realMCPPrismaBlockBoundaryError(string(payload)); err != nil {
			t.Fatalf("Prisma fixture %s can leak parser state: %v", filepath.Base(path), err)
		}
	}
}

// TestRealMCPPrismaWindowsFixturesUseIsolatedLoaderDirectoriesE2E 锁定每个 Prisma
// 动作各自只有一个 loader 目录，防止故意错误的诊断/code-action schema 污染语义动作。
func TestRealMCPPrismaWindowsFixturesUseIsolatedLoaderDirectoriesE2E(t *testing.T) {
	server := realNodeServerCasesForLanguage("prisma")[0]
	fixture := writeRealMCPPrismaLanguageFixture(t, t.TempDir(), server)
	paths := []string{fixture.targetFile, fixture.secondaryFile, fixture.replaceFile, fixture.renameFile, fixture.codeActionFile, fixture.formatFile, fixture.completionFile}
	seenDirectories := make(map[string]string, len(paths))
	for _, path := range paths {
		directory := filepath.Dir(path)
		if previous, exists := seenDirectories[directory]; exists {
			t.Fatalf("Prisma fixtures share loader directory %q: %s and %s", directory, previous, path)
		}
		seenDirectories[directory] = path
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read Prisma loader directory %s: %v", directory, err)
		}
		var schemaFiles []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".prisma") {
				schemaFiles = append(schemaFiles, entry.Name())
			}
		}
		if len(schemaFiles) != 1 || schemaFiles[0] != filepath.Base(path) {
			t.Fatalf("Prisma loader directory %q schemas=%v, want only %q", directory, schemaFiles, filepath.Base(path))
		}
	}
}

// TestRealMCPPrismaWindowsSemanticPositionTargetsModelReference 防止语义坐标再次
// 漂移到字段名与类型名之间的空白；该门禁只检查 fixture，不冒充真实 server PASS。
func TestRealMCPPrismaWindowsSemanticPositionTargetsModelReference(t *testing.T) {
	server := realNodeServerCasesForLanguage("prisma")[0]
	lines := strings.Split(server.content, "\n")
	if server.line <= 0 || server.line > len(lines) {
		t.Fatalf("Prisma semantic line=%d is outside %d fixture lines", server.line, len(lines))
	}
	line := lines[server.line-1]
	tokenStart := strings.Index(line, "User")
	if tokenStart < 0 {
		t.Fatalf("Prisma semantic line %d has no User model reference: %q", server.line, line)
	}
	if server.character < tokenStart || server.character >= tokenStart+len("User") {
		t.Fatalf("Prisma semantic character=%d does not target User range [%d,%d) in %q", server.character, tokenStart, tokenStart+len("User"), line)
	}
}

// assertRealMCPPrismaRenamePreservesMapping 锁定 Prisma rename 的语义合同：模型名
// 可以改变，但数据库映射必须保留。这里不要求 rename→原名的字节级 round-trip，
// 因为上游为避免数据库 identity 改变而保留 @@map 是有意的语义编辑，MCP 不得删除它。
func assertRealMCPPrismaRenamePreservesMapping(t *testing.T, path, renamedModel, mappedModel string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Prisma rename result %s: %v", path, err)
	}
	content := strings.ReplaceAll(string(payload), "\r\n", "\n")
	modelStart := strings.Index(content, "model "+renamedModel+" {")
	if modelStart < 0 {
		t.Fatalf("Prisma rename result missing renamed model %q: %s", renamedModel, path)
	}
	modelBody := content[modelStart:]
	modelEnd := strings.Index(modelBody, "\n}")
	if modelEnd < 0 {
		t.Fatalf("Prisma rename result has unterminated model %q: %s", renamedModel, path)
	}
	modelBody = modelBody[:modelEnd]
	wantMapping := `@@map("` + mappedModel + `")`
	if !strings.Contains(modelBody, wantMapping) {
		t.Fatalf("Prisma rename result for %q dropped database mapping %s", renamedModel, wantMapping)
	}
}

func realMCPPrismaBlockBoundaryError(content string) error {
	open := ""
	for index, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		isBlockStart := false
		for _, keyword := range []string{"datasource ", "generator ", "model ", "type ", "enum ", "view "} {
			if strings.HasPrefix(trimmed, keyword) && strings.Contains(trimmed, "{") {
				isBlockStart = true
				break
			}
		}
		if isBlockStart {
			if open != "" {
				return fmt.Errorf("line %d starts a block before %s closes", index+1, open)
			}
			if strings.Contains(trimmed, "}") {
				return fmt.Errorf("line %d uses an upstream-unsafe inline block", index+1)
			}
			open = fmt.Sprintf("line %d", index+1)
			continue
		}
		if strings.HasPrefix(trimmed, "}") {
			if open == "" {
				return fmt.Errorf("line %d closes no block", index+1)
			}
			open = ""
		}
	}
	if open != "" {
		return fmt.Errorf("%s remains open at document boundary", open)
	}
	return nil
}

// realMCPCompletionFixtureContent 为声明了稳定补全合同的 JavaScript/TypeScript
// 语言写入“对象属性尚未输入完整”的真实上下文；在完整标识符上请求补全会合法
// 返回空列表，不能据此误判 completion 不可用。其余语言沿用各自语义 fixture。
func realMCPCompletionFixtureContent(server realNodeServerCase) (string, int, int) {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		prefix := "realMCPCompletionTarget.realMCPCom"
		return "const realMCPCompletionTarget = { realMCPCompletionCandidate: 1 };\n" + prefix + "\n", 1, len(prefix)
	default:
		return realMCPFixtureContent(server), server.line, server.character
	}
}

// realMCPPosition 接受 MCP 对外约定的 1-based 行列；非法列立即失败，避免把 column=0 发给工具。
func realMCPPosition(path string, line, column int) string {
	if line <= 0 || column <= 0 {
		panic(fmt.Sprintf("invalid MCP position line=%d column=%d", line, column))
	}
	return fmt.Sprintf("%s:%d:%d", path, line, column)
}

// realMCPPositionFromLSP 把 raw LSP 的 0-based character 转成 MCP 的 1-based column。
func realMCPPositionFromLSP(path string, line, character int) string {
	if line <= 0 || character < 0 {
		panic(fmt.Sprintf("invalid raw LSP position line=%d character=%d", line, character))
	}
	return realMCPPosition(path, line, character+1)
}

// realMCPPositionPath 从 Windows 驱动器路径的 line:column 位置中安全取回文件路径。
func realMCPPositionPath(position string) string {
	last := strings.LastIndex(position, ":")
	if last <= 0 {
		return ""
	}
	if _, err := strconv.Atoi(position[last+1:]); err != nil {
		return ""
	}
	beforeColumn := position[:last]
	secondLast := strings.LastIndex(beforeColumn, ":")
	if secondLast <= 0 {
		return beforeColumn
	}
	if _, err := strconv.Atoi(beforeColumn[secondLast+1:]); err != nil {
		// 兼容只携带 :line 的原生位置；Windows 盘符中的冒号不是第二个位置分隔符。
		return beforeColumn
	}
	return beforeColumn[:secondLast]
}

// realMCPFixtureContent 为语义工具构造真实的定义、调用和继承关系，避免用空文件制造假阳性。
func realMCPFixtureContent(server realNodeServerCase) string {
	switch server.name {
	case "javascript":
		content := strings.Replace(server.content, `return "Hello " + name;`, "return formatGreeting(name);", 1)
		return content + "\nfunction formatGreeting(name) { return \"Hello \" + name; }\nclass BaseGreeter { greet(name) { return name; } }\nclass DerivedGreeter extends BaseGreeter {}\nconst derived = new DerivedGreeter();\nderived.greet(\"world\");\n"
	case "javascriptreact":
		return server.content + "function formatGreeting(name) { return name; }\nformatGreeting(\"world\");\n"
	case "typescript":
		content := strings.Replace(server.content, "return { text: name };", "return formatGreeting(name);", 1)
		return content + "\nfunction formatGreeting(name: string): Greeting { return { text: name }; }\nclass GreetingImpl implements Greeting {\n  greet(name: string): Greeting { return formatGreeting(name); }\n}\nconst implementation: Greeting = new GreetingImpl();\n"
	case "typescriptreact":
		content := strings.Replace(server.content, "return <h1>Hello {props.name}</h1>;", "return renderGreeting(props.name);", 1)
		return content + "\nfunction renderGreeting(name: string) { return <h1>Hello {name}</h1>; }\nclass PropsAdapter implements Props { name = \"world\"; }\nconst propsAdapter: Props = new PropsAdapter();\nrenderGreeting(\"world\");\n"
	case "python":
		content := strings.Replace(server.content, `return f"Hello {name}"`, "return format_greeting(name)", 1)
		return content + "def format_greeting(name: str) -> str:\n    return f\"Hello {name}\"\n\nclass BaseGreeter:\n    def render(self):\n        return greet(\"world\")\n\nclass DerivedGreeter(BaseGreeter):\n    pass\n"
	case "php":
		content := strings.Replace(server.content, `return "Hello " . $name;`, "return formatGreeting($name);", 1)
		return content + "function formatGreeting(string $name): string { return \"Hello \" . $name; }\ninterface Greeter { public function greet(string $name): string; }\nclass GreeterImpl implements Greeter { public function greet(string $name): string { return formatGreeting($name); } }\n$greeter = new GreeterImpl();\n$greeter->greet(\"world\");\n"
	case "shellscript":
		content := strings.Replace(server.content, `greet() { echo "Hello $1"; }`, `greet() { format_greeting "$1"; }`, 1)
		return content + "\nformat_greeting() { echo \"Hello $1\"; }\n"
	case "vue":
		// Vue 3.3.9 的 TS companion 需要在 script setup 内命中真实声明与使用点；
		// 模板绑定仍保留第二个使用点，但语义 action 统一定位到 script 中的 message 使用。
		return "<template>\n  <button>{{ message }}</button>\n</template>\n<script setup lang=\"ts\">\nfunction formatMessage(value: string): string {\n  return value\n}\nconst message: string = 'hello'\nconst rendered = formatMessage(message)\n</script>\n"
	default:
		return server.content
	}
}

// realMCPCodeActionFixtureContent 为每种真实语言服务器写入可诊断的源码；空 quickfix 结果仍须按语言契约审计。
func realMCPCodeActionFixtureContent(server realNodeServerCase) string {
	switch server.name {
	case "javascript", "javascriptreact":
		return "const broken = ;\n"
	case "typescript", "typescriptreact":
		return "const broken: string = 42;\n"
	case "css":
		return ".broken { color: ; }\n"
	case "html":
		return "<div>\n"
	case "json":
		return "{\"broken\": }\n"
	case "markdown":
		return "[broken](\n"
	case "python":
		return "def broken(:\n"
	case "yaml":
		return "broken: [\n"
	case "vue":
		return "<template><div>{{ missingRealMCPValue }}</div></template>\n<script setup lang=\"ts\">\nconst broken: string = 42\n</script>\n"
	case "svelte":
		return "<script lang=\"ts\">\nconst broken: string = 42\n</script>\n<div>{broken}</div>\n"
	case "php":
		return "<?php\nfunction broken( { }\n"
	case "dockerfile":
		return "FROM\n"
	case "graphql":
		return "query Broken { missingRealMCPField }\n"
	case "prisma":
		return "model RealMCPBroken {\n  id String @id @unknown\n}\n"
	case "shellscript":
		return "if then\n"
	default:
		return server.content
	}
}

func realMCPFixturePath(root string, server realNodeServerCase, stem string) string {
	if server.name == "dockerfile" {
		return filepath.Join(root, stem, "Dockerfile")
	}
	if server.name == "prisma" {
		// Prisma 的同目录文件属于同一个关联 schema 集合；动作 fixture 必须目录隔离。
		return filepath.Join(root, stem, stem+filepath.Ext(server.fileName))
	}
	return filepath.Join(root, stem+filepath.Ext(server.fileName))
}

// realMCPActionSpecs 返回七个公开工具族的全部公开小动作；参数指向当前真实语言 fixture。
// realMCPValidateUTF16Identifier 校验 1-based 行号与 0-based UTF-16 列确实落在
// 指定 identifier 上；不能用 Go 字节下标冒充 LSP position。
func realMCPValidateUTF16Identifier(content []byte, lineNumber, character int, identifier string) error {
	if lineNumber <= 0 || character < 0 || strings.TrimSpace(identifier) == "" {
		return fmt.Errorf("invalid semantic mapping line=%d character=%d identifier=%q", lineNumber, character, identifier)
	}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if lineNumber > len(lines) {
		return fmt.Errorf("semantic line=%d exceeds %d lines", lineNumber, len(lines))
	}
	lineUnits := utf16.Encode([]rune(lines[lineNumber-1]))
	identifierUnits := utf16.Encode([]rune(identifier))
	end := character + len(identifierUnits)
	if end > len(lineUnits) || !slices.Equal(lineUnits[character:end], identifierUnits) {
		return fmt.Errorf("semantic anchor does not target %q at %d:%d", identifier, lineNumber, character)
	}
	return nil
}

// realMCPNodeSourceMapping 在复制快照前收紧 source root、真实文件与语义映射；
// 所有 Node17 real fixture 都必须先通过这一道 fail-fast 守卫。
func realMCPNodeSourceMapping(sourceRoot string, server realNodeServerCase) (string, []byte, error) {
	if strings.TrimSpace(server.sourceDir) == "" || strings.TrimSpace(server.sourceFile) == "" || strings.TrimSpace(server.sourceSecondaryFile) == "" || strings.TrimSpace(server.sourceIdentifier) == "" || strings.TrimSpace(server.sourceWorkspaceQuery) == "" {
		return "", nil, fmt.Errorf("%s source mapping is incomplete: dir=%q file=%q secondary=%q identifier=%q query=%q", server.languageID, server.sourceDir, server.sourceFile, server.sourceSecondaryFile, server.sourceIdentifier, server.sourceWorkspaceQuery)
	}
	if server.sourceLine <= 0 || server.sourceCharacter < 0 {
		return "", nil, fmt.Errorf("%s source mapping has invalid position line=%d character=%d", server.languageID, server.sourceLine, server.sourceCharacter)
	}
	absSourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return "", nil, fmt.Errorf("resolve source root: %w", err)
	}
	info, err := os.Stat(absSourceRoot)
	if err != nil {
		return "", nil, fmt.Errorf("stat source root %s: %w", absSourceRoot, err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("source root is not a directory: %s", absSourceRoot)
	}
	sourceDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(server.sourceDir)))
	if sourceDir == "." || filepath.IsAbs(sourceDir) {
		return "", nil, fmt.Errorf("source directory must be relative: %q", server.sourceDir)
	}
	sourceProjectRoot := filepath.Join(absSourceRoot, sourceDir)
	if !realMCPPathWithinRoot(absSourceRoot, sourceProjectRoot) {
		return "", nil, fmt.Errorf("source project escapes source root: %q", sourceProjectRoot)
	}
	projectInfo, err := os.Stat(sourceProjectRoot)
	if err != nil {
		return "", nil, fmt.Errorf("stat source project %s: %w", sourceProjectRoot, err)
	}
	if !projectInfo.IsDir() {
		return "", nil, fmt.Errorf("source project is not a directory: %s", sourceProjectRoot)
	}
	sourceRelative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(server.sourceFile)))
	if sourceRelative == "." || filepath.IsAbs(sourceRelative) {
		return "", nil, fmt.Errorf("source file must be relative: %q", server.sourceFile)
	}
	sourcePath := filepath.Join(sourceProjectRoot, sourceRelative)
	if !realMCPPathWithinRoot(absSourceRoot, sourcePath) || !realMCPPathWithinRoot(sourceProjectRoot, sourcePath) {
		return "", nil, fmt.Errorf("source file escapes source root: %q", sourcePath)
	}
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return "", nil, fmt.Errorf("stat source file %s: %w", sourcePath, err)
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("source file is not a regular file: %s", sourcePath)
	}
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", nil, fmt.Errorf("read source file %s: %w", sourcePath, err)
	}
	if len(sourceBytes) == 0 {
		return "", nil, fmt.Errorf("source file is empty: %s", sourcePath)
	}
	if err := realMCPValidateUTF16Identifier(sourceBytes, server.sourceLine, server.sourceCharacter, strings.TrimSpace(server.sourceIdentifier)); err != nil {
		return "", nil, fmt.Errorf("%s: %w", server.languageID, err)
	}
	if !strings.Contains(string(sourceBytes), strings.TrimSpace(server.sourceWorkspaceQuery)) {
		return "", nil, fmt.Errorf("workspace query %q is absent from source file %s", server.sourceWorkspaceQuery, sourcePath)
	}
	secondaryRelative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(server.sourceSecondaryFile)))
	if secondaryRelative == "." || filepath.IsAbs(secondaryRelative) {
		return "", nil, fmt.Errorf("secondary source file must be relative: %q", server.sourceSecondaryFile)
	}
	secondaryPath := filepath.Join(sourceProjectRoot, secondaryRelative)
	if !realMCPPathWithinRoot(absSourceRoot, secondaryPath) || !realMCPPathWithinRoot(sourceProjectRoot, secondaryPath) {
		return "", nil, fmt.Errorf("secondary source file escapes source project: %q", secondaryPath)
	}
	secondaryInfo, err := os.Lstat(secondaryPath)
	if err != nil {
		return "", nil, fmt.Errorf("stat secondary source file %s: %w", secondaryPath, err)
	}
	if secondaryInfo.Mode()&os.ModeSymlink != 0 || !secondaryInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("secondary source file is not a regular file: %s", secondaryPath)
	}
	return sourcePath, sourceBytes, nil
}

func realMCPActionSpecs(server realNodeServerCase, fixture realMCPFixture, _ string) []realMCPActionSpec {
	semantic := fixture.semanticPosition
	workspaceQuery := strings.TrimSpace(fixture.workspaceQuery)
	if fixture.sourceRoot != "" {
		if workspaceQuery == "" || workspaceQuery != strings.TrimSpace(server.sourceWorkspaceQuery) {
			panic(fmt.Sprintf("%s real fixture is missing its mapped workspace query: fixture=%q server=%q", server.languageID, fixture.workspaceQuery, server.sourceWorkspaceQuery))
		}
	} else if workspaceQuery == "" {
		workspaceQuery = realMCPWorkspaceQuery(server)
	}
	callIncomingResult, callIncomingEmpty := realMCPActionResultContract(server, "xref", "call_hierarchy")
	typeHierarchyResult, typeHierarchyEmpty := realMCPActionResultContract(server, "xref", "type_hierarchy")
	referencesResult, referencesEmpty := realMCPActionResultContract(server, "xref", "references")
	referencesNoDeclarationResult, referencesNoDeclarationEmpty := realMCPActionResultContract(server, "xref", "references-no-declaration")
	actions := []realMCPActionSpec{
		{name: "references", tool: "xref", args: map[string]any{"action": "references", "pos": semantic, "include_declaration": true, "max_results": 20}, requireResult: referencesResult, emptyResultReason: referencesEmpty},
		{name: "references-no-declaration", tool: "xref", args: map[string]any{"action": "references", "pos": semantic, "include_declaration": false, "max_results": 20}, requireResult: referencesNoDeclarationResult, emptyResultReason: referencesNoDeclarationEmpty},
		{name: "call_hierarchy-incoming", tool: "xref", args: map[string]any{"action": "call_hierarchy", "pos": fixture.callHierarchyPosition, "direction": "incoming"}, requireResult: callIncomingResult, emptyResultReason: callIncomingEmpty},
		{name: "call_hierarchy-outgoing", tool: "xref", args: map[string]any{"action": "call_hierarchy", "pos": fixture.callHierarchyPosition, "direction": "outgoing"}, requireResult: callIncomingResult, emptyResultReason: callIncomingEmpty},
		{name: "call_hierarchy-both", tool: "xref", args: map[string]any{"action": "call_hierarchy", "pos": fixture.callHierarchyPosition, "direction": "both"}, requireResult: callIncomingResult, emptyResultReason: callIncomingEmpty},
		{name: "type_hierarchy-supertypes", tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": fixture.typeHierarchyPosition, "direction": "supertypes"}, requireResult: typeHierarchyResult, emptyResultReason: typeHierarchyEmpty},
		{name: "type_hierarchy-subtypes", tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": fixture.typeHierarchyPosition, "direction": "subtypes"}, requireResult: typeHierarchyResult, emptyResultReason: typeHierarchyEmpty},
		{name: "type_hierarchy-both", tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": fixture.typeHierarchyPosition, "direction": "both"}, requireResult: typeHierarchyResult, emptyResultReason: typeHierarchyEmpty},

		{name: "document_symbol", tool: "structure", args: map[string]any{"action": "document_symbol", "file_path": fixture.targetFile, "max_results": 20}, requireResult: true},
		{name: "workspace_symbol-file", tool: "structure", args: map[string]any{"action": "workspace_symbol", "file_path": fixture.targetFile, "query": workspaceQuery, "max_results": 20}, requireResult: true},
		{name: "workspace_symbol-language", tool: "structure", args: map[string]any{"action": "workspace_symbol", "workspace_language": server.languageID, "query": workspaceQuery, "max_results": 20}, requireResult: true},
		{name: "folding_range", tool: "structure", args: map[string]any{"action": "folding_range", "file_path": fixture.targetFile, "max_results": 20}, requireResult: true},
		{name: "semantic_tokens", tool: "structure", args: map[string]any{"action": "semantic_tokens", "file_path": fixture.targetFile, "max_results": 20}, requireResult: true},
		{name: "diagnostics", tool: "diagnostics", args: map[string]any{"file_path": fixture.codeActionFile}, emptyResultReason: "真实诊断 fixture 已写入；语言服务器可以合法报告零条诊断"},
		{name: "diagnostics-batch", tool: "diagnostics", args: map[string]any{"file_paths": []string{fixture.codeActionFile, fixture.targetFile}}, emptyResultReason: "批量诊断允许其中一个合法文件没有诊断"},
	}
	for i := range actions {
		action := &actions[i]
		action.requireResult, action.emptyResultReason = realMCPActionResultContract(server, action.tool, action.name)
		action.allowCapabilityUnsupported = realMCPActionAllowsCapabilityUnsupported(server, action.tool, action.name)
		action.contractSet = true
	}
	return actions
}

// realMCPActionFamily 将带方向后缀的层级动作归一到稳定的结果合同族。
// 方向只改变请求参数，不应改变同一 LSP 能力的非空或合法空判断。
func realMCPActionFamily(tool, action string) string {
	if tool != "xref" {
		return action
	}
	switch {
	case action == "call_hierarchy" || strings.HasPrefix(action, "call_hierarchy-"):
		return "call_hierarchy"
	case action == "type_hierarchy" || strings.HasPrefix(action, "type_hierarchy-"):
		return "type_hierarchy"
	default:
		return action
	}
}

// realMCPActionResultContract 明确区分必须非空的成功、合法空结果和协议 capability_unsupported。
func realMCPActionResultContract(server realNodeServerCase, tool, action string) (bool, string) {
	actionFamily := realMCPActionFamily(tool, action)
	switch tool {
	case "diagnostics":
		return false, "fixture 诊断文件可以合法返回零条诊断；diagnostics 工具本身必须成功"
	case "structure":
		switch action {
		case "document_symbol", "workspace_symbol-file", "workspace_symbol-language":
			if !realMCPFixtureHasDocumentSymbols(server) {
				return false, "该语言 fixture 没有稳定的 document/workspace symbol；合法空结果明确允许"
			}
		case "folding_range":
			if !realMCPFixtureHasFoldingRange(server) {
				return false, "该语言 fixture 没有可折叠的稳定多行结构；合法空 folding_range 明确允许"
			}
		case "semantic_tokens":
			if !realMCPFixtureHasSemanticTokens(server) {
				return false, "该语言 fixture 没有稳定 semantic token 合同；合法空 semantic_tokens 明确允许"
			}
		}
	}
	if tool == "xref" && action == "references" && !realMCPFixtureHasReferences(server) {
		return false, "fixture has no repeated cross-reference symbol; empty references is expected"
	}
	if tool == "xref" && action == "references-no-declaration" && !realMCPFixtureHasReferenceUse(server) {
		return false, "fixture has no non-declaration cross-reference use; empty references(include_declaration=false) is expected"
	}
	switch {
	case tool == "xref" && actionFamily == "call_hierarchy" && !realMCPFixtureHasCallHierarchy(server):
		return false, "fixture 没有可调用的函数层级关系；空 call_hierarchy 是该语言的明确预期"
	case tool == "xref" && actionFamily == "type_hierarchy" && !realMCPFixtureHasInheritance(server):
		return false, "fixture 没有可查询的类型继承关系；空 type_hierarchy 是该语言的明确预期"
	default:
		return true, ""
	}
}

// realMCPFixtureHasDefinition 标记 fixture 中确实存在可跳转的定义符号。
// realMCPActionAllowsCapabilityUnsupported 记录 fixture 的静态结果预期；真实 typed
// capability_unsupported 仍须通过服务端 capability snapshot 或协议可选性门禁。
func realMCPActionAllowsCapabilityUnsupported(server realNodeServerCase, tool, action string) bool {
	switch tool {
	case "diagnostics":
		return false
	case "xref":
		switch action {
		case "references":
			return !realMCPFixtureHasReferences(server)
		case "references-no-declaration":
			return !realMCPFixtureHasReferenceUse(server)
		case "call_hierarchy-incoming", "call_hierarchy-outgoing", "call_hierarchy-both":
			return !realMCPFixtureHasCallHierarchy(server)
		case "type_hierarchy-supertypes", "type_hierarchy-subtypes", "type_hierarchy-both":
			// type hierarchy 是 LSP 可选能力；即使 fixture 有真实继承关系，当前
			// language server 也可能未声明 prepareTypeHierarchy。调用必须成功到达
			// 能力裁决点，并以明确 unsupported 记账，禁止伪造层级结果。
			return true
		}
	case "structure":
		switch action {
		case "document_symbol", "workspace_symbol-file", "workspace_symbol-language":
			return !realMCPFixtureHasDocumentSymbols(server)
		case "folding_range":
			return !realMCPFixtureHasFoldingRange(server)
		case "semantic_tokens":
			return !realMCPFixtureHasSemanticTokens(server)
		}
	}
	return false
}

// realMCPFixtureHasHover 只把有真实符号文档的语言标为必须非空；其余语言允许明确的合法空结果。
func realMCPFixtureHasHover(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "vue", "svelte", "php", "graphql", "prisma":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasCompletion 以 fixture 的 token/上下文是否能提供稳定候选来决定非空合同。
func realMCPFixtureHasCompletion(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "json", "php", "prisma":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasDocumentSymbols 标记 fixture 中拥有可查询 document/workspace symbol 的语言。
func realMCPFixtureHasDocumentSymbols(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "vue", "svelte", "php", "graphql", "prisma", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasFoldingRange 标记 fixture 中确实包含稳定的多行折叠结构。
func realMCPFixtureHasFoldingRange(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "html", "json", "markdown", "python", "yaml", "vue", "svelte", "php", "dockerfile", "graphql", "prisma", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasSemanticTokens 标记 fixture 中声明了可稳定分类的语义 token。
func realMCPFixtureHasSemanticTokens(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "css", "html", "json", "python", "yaml", "vue", "svelte", "php", "graphql", "prisma", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasRename 标记 rename fixture 中拥有声明和真实使用点的语言。
func realMCPFixtureHasRename(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "vue", "svelte", "php", "graphql", "prisma", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasCodeAction 标记诊断 fixture 中有稳定 quickfix 能力的语言；空列表仍是合法成功。
func realMCPFixtureHasCodeAction(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "dockerfile":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasFormat 标记 format fixture 中有稳定 formatter 能力的语言。
func realMCPFixtureHasFormat(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "css", "html", "json", "markdown", "python", "yaml", "vue", "svelte", "php":
		return true
	default:
		return false
	}
}

func realMCPFixtureHasDefinition(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "vue", "svelte", "php", "graphql", "prisma", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasSignatureHelp 标记 fixture 中确实存在函数调用签名位置。
func realMCPFixtureHasSignatureHelp(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "php":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasReferences 标记 fixture 中有声明和至少一个真实使用点。
func realMCPFixtureHasReferences(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "vue", "svelte", "php", "graphql", "prisma", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasReferenceUse 标记去掉声明后仍有真实使用位置的 fixture。
func realMCPFixtureHasReferenceUse(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "javascriptreact", "typescript", "typescriptreact", "python", "vue", "svelte", "php", "graphql", "shellscript":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasInheritance 标记 fixture 中有可验证的接口或类继承关系。
func realMCPFixtureHasInheritance(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "typescript", "typescriptreact", "python", "php":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasTypeDefinition 标记 fixture 中有稳定的类型定义跳转目标。
func realMCPFixtureHasTypeDefinition(server realNodeServerCase) bool {
	switch server.name {
	case "typescript", "typescriptreact", "graphql":
		return true
	default:
		return false
	}
}

// realMCPFixtureHasCallHierarchy 标记 fixture 中有调用方和被调用方关系。
func realMCPFixtureHasCallHierarchy(server realNodeServerCase) bool {
	switch server.name {
	case "javascript", "typescript", "typescriptreact", "python", "php", "shellscript":
		return true
	default:
		return false
	}
}

func realMCPWorkspaceQuery(server realNodeServerCase) string {
	switch server.name {
	case "css":
		return "button"
	case "html":
		return "main"
	case "json":
		return "name"
	case "markdown":
		return "Section"
	case "python", "php", "shellscript":
		return "greet"
	case "yaml":
		return "services"
	case "vue":
		return "message"
	case "svelte":
		return "count"
	case "javascriptreact", "typescriptreact":
		return "Greeting"
	case "dockerfile":
		return "FROM"
	case "graphql", "prisma":
		return "User"
	default:
		return "greet"
	}
}

// requireRealMCPToolFamilies 锁定真实 stdio tools/list 必须同时暴露的三个公开工具族。
// tools/list 是 MCP 协议结果，不是 tools/call 的 CallToolResult，必须直接解析 result 对象。
func requireRealMCPToolFamilies(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode real tools/list protocol result: %v; raw=%s", err, raw)
	}
	got := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	want := []string{"diagnostics", "structure", "xref"}
	if !slices.Equal(got, want) {
		t.Fatalf("real tools/list names=%v, want exact three public tool families=%v; raw=%s", got, want, raw)
	}
}

func writeRealFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create real MCP fixture directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write real MCP fixture %s: %v", path, err)
	}
}

// assertRealMCPNativeFixtureInputs 用原生文件操作验证 MCP 语义调用的输入快照。
// 文件读取、文本定位和夹具写入不占用 MCP 工具面。
func assertRealMCPNativeFixtureInputs(t *testing.T, fixture realMCPFixture) {
	t.Helper()
	for _, path := range []string{fixture.targetFile, fixture.secondaryFile, fixture.codeActionFile} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("native read fixture %s: %v", path, err)
		}
		if len(content) == 0 {
			t.Fatalf("native read fixture %s returned empty content", path)
		}
	}
	secondary, err := os.ReadFile(fixture.secondaryFile)
	if err != nil {
		t.Fatalf("native search fixture %s: %v", fixture.secondaryFile, err)
	}
	if needle := strings.TrimSpace(fixture.searchNeedle); needle != "" && !bytes.Contains(secondary, []byte(needle)) {
		t.Fatalf("native search fixture %s missing %q", fixture.secondaryFile, needle)
	}
}

// copyRealMCPBinSourceTree 复制受版本控制的语言快照，不跟随或重建符号链接。
func copyRealMCPBinSourceTree(t *testing.T, sourceRoot, destinationRoot string) {
	t.Helper()
	var err error
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		t.Fatalf("resolve bin/LSP/test source root: %v", err)
	}
	destinationRoot, err = filepath.Abs(destinationRoot)
	if err != nil {
		t.Fatalf("resolve isolated source snapshot root: %v", err)
	}
	if filepath.Clean(sourceRoot) == filepath.Clean(destinationRoot) {
		t.Fatalf("refuse to copy source snapshot onto itself: %s", sourceRoot)
	}
	info, err := os.Stat(sourceRoot)
	if err != nil {
		t.Fatalf("stat bin/LSP/test source directory %s: %v", sourceRoot, err)
	}
	if !info.IsDir() {
		t.Fatalf("bin/LSP/test source path is not a directory: %s", sourceRoot)
	}
	if err := filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source snapshot contains unsupported symlink %s", path)
		}
		if !realMCPPathWithinRoot(sourceRoot, path) {
			return fmt.Errorf("source snapshot path escaped source root: %s", path)
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return fmt.Errorf("resolve source snapshot relative path %s: %w", path, err)
		}
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("source snapshot relative path escaped source root: %s", relative)
		}
		destination := filepath.Join(destinationRoot, relative)
		if !realMCPPathWithinRoot(destinationRoot, destination) {
			return fmt.Errorf("source snapshot destination escaped target root: %s", destination)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read source snapshot %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return fmt.Errorf("create source snapshot destination %s: %w", destination, err)
		}
		if err := os.WriteFile(destination, payload, 0o600); err != nil {
			return fmt.Errorf("write isolated source snapshot %s: %w", destination, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("copy bin/LSP/test source snapshot %s: %v", sourceRoot, err)
	}
}

// copyRealMCPBinSourceFile 将 bin/LSP/test 下的相对路径复制到隔离 workspace，
// 不向调用方暴露或复用受版本控制的源文件路径。
func copyRealMCPBinSourceFile(t *testing.T, sourceRoot, relativePath, destination string) {
	t.Helper()
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		t.Fatalf("resolve bin/LSP/test source root: %v", err)
	}
	relativePath = filepath.Clean(filepath.FromSlash(relativePath))
	if relativePath == "." || filepath.IsAbs(relativePath) {
		t.Fatalf("bin/LSP/test source file must be relative: %q", relativePath)
	}
	source := filepath.Join(sourceRoot, relativePath)
	if !realMCPPathWithinRoot(sourceRoot, source) {
		t.Fatalf("bin/LSP/test source file escapes source root: %q", source)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		t.Fatalf("resolve isolated source file destination: %v", err)
	}
	if filepath.Clean(sourceRoot) == filepath.Clean(destination) {
		t.Fatalf("refuse to copy source file onto source root: %s", destination)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatalf("stat bin/LSP/test source file %s: %v", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("bin/LSP/test source file is not a regular file: %s", source)
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read bin/LSP/test source file %s: %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("create isolated source file parent %s: %v", destination, err)
	}
	if err := os.WriteFile(destination, payload, 0o600); err != nil {
		t.Fatalf("write isolated source file %s: %v", destination, err)
	}
}

// copyRealMCPBinSourceFileWithinRoot 在复制单文件前验证目标仍位于指定隔离根，
// 防止错误映射把真实 bin/LSP/test 内容写到 workspace 外部。
func copyRealMCPBinSourceFileWithinRoot(t *testing.T, sourceRoot, relativePath, targetRoot, destination string) {
	t.Helper()
	absoluteTargetRoot, err := filepath.Abs(targetRoot)
	if err != nil {
		t.Fatalf("resolve isolated target root: %v", err)
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		t.Fatalf("resolve isolated target path: %v", err)
	}
	if !realMCPPathWithinRoot(absoluteTargetRoot, absoluteDestination) {
		t.Fatalf("isolated source file destination escaped target root: root=%q destination=%q", absoluteTargetRoot, absoluteDestination)
	}
	copyRealMCPBinSourceFile(t, sourceRoot, relativePath, absoluteDestination)
}

func readRealMCPBinSourceFile(t *testing.T, path string) []byte {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat source fixture %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("source fixture is not a regular file: %s", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source fixture %s: %v", path, err)
	}
	return payload
}

func realMCPSourcePosition(content string) (int, int) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for lineNumber, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return lineNumber + 1, len(line) - len(strings.TrimLeft(line, " \t"))
	}
	return 1, 0
}

func realMCPSourceNeedle(content string) string {
	for _, token := range strings.FieldsFunc(content, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if len(token) >= 4 {
			return token
		}
	}
	return ""
}

// registerRealMCPTempRootCleanup 在进程退出后精确删除 fixture 根目录，并断言删除确实完成。
// 这样临时目录残留不会被测试框架的宽泛清理静默掩盖。
func registerRealMCPTempRootCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove real MCP temporary root %s: %v", root, err)
			return
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("real MCP temporary root %s remains after cleanup: %v", root, err)
		}
	})
}

// realMCPActionCapabilityKey 将公共工具 action 映射到 LSP initialize 能力快照字段。
// 没有对应 LSP 能力的核心文件、grep 和 replace action 不得用 unsupported 伪造成功。
func realMCPActionCapabilityKey(tool, action string) string {
	switch tool {
	case "diagnostics":
		return "diagnostics"
	case "xref":
		switch {
		case action == "references" || action == "references-no-declaration":
			return "references"
		case strings.HasPrefix(action, "call_hierarchy-"):
			return "call_hierarchy"
		case strings.HasPrefix(action, "type_hierarchy-"):
			return "type_hierarchy"
		}
	case "structure":
		switch {
		case action == "document_symbol":
			return "document_symbol"
		case strings.HasPrefix(action, "workspace_symbol-"):
			return "workspace_symbol"
		case action == "folding_range":
			return "folding_range"
		case action == "semantic_tokens":
			return "semantic_tokens"
		}
	}
	return ""
}

// realMCPActionProtocolOptional 只标记协议层明确允许缺失的 action；fixture 没有
// 结果不能代替运行时能力证明，真实 unsupported 仍须由 capability snapshot 佐证。
func realMCPActionProtocolOptional(tool, action string) bool {
	return tool == "xref" && strings.HasPrefix(action, "type_hierarchy-")
}

// realMCPActionProtocolOptionalForServer 只放行已核对为 LSP 可选能力的语言服务器动作；
// 文件、grep、基础符号等核心动作仍必须按真实结果合同失败，不得因语言服务器缺能力而伪造成功。
func realMCPActionProtocolOptionalForServer(server realNodeServerCase, tool, action string) bool {
	if realMCPActionProtocolOptional(tool, action) {
		return true
	}
	switch server.name {
	case "graphql":
		return (tool == "xref" && (action == "references" || action == "references-no-declaration")) ||
			(tool == "structure" && (action == "folding_range" || action == "semantic_tokens"))
	case "prisma":
		return tool == "structure" && (action == "workspace_symbol-file" || action == "workspace_symbol-language" || action == "folding_range" || action == "semantic_tokens")
	case "shellscript":
		return (tool == "xref" && strings.HasPrefix(action, "call_hierarchy-")) ||
			(tool == "structure" && (action == "folding_range" || action == "semantic_tokens"))
	case "css", "html":
		return tool == "structure" && action == "semantic_tokens"
	case "json":
		return (tool == "xref" && (action == "references" || action == "references-no-declaration")) ||
			(tool == "structure" && action == "semantic_tokens")
	case "python", "yaml":
		return tool == "structure" && (action == "folding_range" || action == "semantic_tokens")
	case "php":
		return (tool == "xref" && strings.HasPrefix(action, "call_hierarchy-")) ||
			(tool == "structure" && (action == "folding_range" || action == "semantic_tokens"))
	case "vue":
		return tool == "structure" && (action == "workspace_symbol-file" || action == "workspace_symbol-language")
	default:
		return false
	}
}

func TestRealMCPOptionalCapabilityContractsE2E(t *testing.T) {
	servers := map[string]realNodeServerCase{}
	for _, server := range realNodeServerCases() {
		servers[server.name] = server
	}
	if realMCPActionProtocolOptionalForServer(servers["json"], "diagnostics", "diagnostics") {
		t.Fatal("diagnostics must not become capability-optional")
	}
	if !realMCPActionProtocolOptionalForServer(servers["json"], "xref", "references") ||
		!realMCPActionProtocolOptionalForServer(servers["json"], "structure", "semantic_tokens") {
		t.Fatal("json references and semantic_tokens must remain explicitly optional upstream capabilities")
	}
	for _, test := range []struct {
		server string
		tool   string
		action string
	}{
		{"graphql", "xref", "references"},
		{"graphql", "structure", "folding_range"},
		{"prisma", "structure", "workspace_symbol-file"},
		{"prisma", "structure", "semantic_tokens"},
		{"shellscript", "xref", "call_hierarchy-incoming"},
		{"shellscript", "structure", "folding_range"},
		{"css", "structure", "semantic_tokens"},
		{"html", "structure", "semantic_tokens"},
		{"json", "structure", "semantic_tokens"},
		{"python", "structure", "folding_range"},
		{"python", "structure", "semantic_tokens"},
		{"yaml", "structure", "semantic_tokens"},
		{"php", "xref", "call_hierarchy-incoming"},
		{"php", "xref", "call_hierarchy-outgoing"},
		{"php", "xref", "call_hierarchy-both"},
		{"php", "structure", "folding_range"},
		{"php", "structure", "semantic_tokens"},
		{"vue", "structure", "workspace_symbol-file"},
		{"vue", "structure", "workspace_symbol-language"},
	} {
		server, ok := servers[test.server]
		if !ok || !realMCPActionProtocolOptionalForServer(server, test.tool, test.action) {
			t.Fatalf("%s/%s/%s must be explicitly optional", test.server, test.tool, test.action)
		}
	}
	if realMCPActionProtocolOptionalForServer(servers["graphql"], "diagnostics", "diagnostics") ||
		realMCPActionProtocolOptionalForServer(servers["vue"], "structure", "document_symbol") {
		t.Fatal("core diagnostics/document_symbol actions must not become optional")
	}
}

type realMCPCapabilitySnapshot struct {
	known  bool
	values map[string]bool
}

// realMCPCapabilitySnapshotFromMeta 解析 multilsp 注入的有限能力快照；未知、缺失或
// 格式错误的字段均不算 false，避免把不完整元数据误记成 capability_unsupported。
func realMCPCapabilitySnapshotFromMeta(meta map[string]any) realMCPCapabilitySnapshot {
	known, _ := meta["capabilities_known"].(bool)
	snapshot := realMCPCapabilitySnapshot{known: known, values: map[string]bool{}}
	if !known {
		return snapshot
	}
	raw, _ := meta["capability_snapshot"].(string)
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		value, err := strconv.ParseBool(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		snapshot.values[strings.TrimSpace(parts[0])] = value
	}
	return snapshot
}

// realMCPCapabilityUnsupportedAccounted 只接受快照明确为 false，或协议明确可选的
// fallback；fixture contract 的 allowCapabilityUnsupported 不参与运行时放行。
func realMCPCapabilityUnsupportedAccounted(meta map[string]any, capabilityKey string, protocolOptional bool) bool {
	snapshot := realMCPCapabilitySnapshotFromMeta(meta)
	if snapshot.known && capabilityKey != "" {
		if supported, ok := snapshot.values[capabilityKey]; ok {
			return !supported
		}
	}
	return protocolOptional
}

// requireRealMCPActionResult 区分真实成功 payload、显式合法空结果和预期的
// capability_unsupported。超时、进程失败、invalid_target、schema 错误及未分类
// 空结果均必须失败；unsupported 只进入独立记账，绝不冒充语义 PASS。
func requireRealMCPActionResult(t *testing.T, response mcpLSPBinaryResponse, requireResult bool, emptyResultReason string, fixtureAllowsCapabilityUnsupported bool, capabilityKey string, protocolOptional bool, label string) realMCPActionStatus {
	t.Helper()
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("%s returned deprecated structuredContent; remote content-only contract was rolled back: %s", label, response.Result.StructuredContent)
	}
	text := response.Result.ContentText()
	combined := strings.ToLower(strings.TrimSpace(text))
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		// 远程 67f7a40bd 将能力缺失保留为成功的纯文本空结果；该既有输出不是
		// ERROR 行协议，E2E 只能适配并独立记账，禁止反向恢复 structuredContent。
		if !response.Result.IsError && realMCPRemoteCapabilityEmptyMessage(combined) {
			if !fixtureAllowsCapabilityUnsupported && !protocolOptional {
				t.Fatalf("%s returned capability-unavailable outside its action contract: text=%q", label, text)
			}
			return realMCPActionUnsupported
		}
		t.Fatalf("%s content is not valid line protocol: %v; text=%q", label, err, text)
	}
	if doc.Error != nil && doc.Error.Code == "capability_unsupported" {
		if !response.Result.IsError {
			t.Fatalf("%s advertised capability_unsupported without MCP isError: text=%q", label, text)
		}
		if !fixtureAllowsCapabilityUnsupported && !protocolOptional {
			t.Fatalf("%s returned capability_unsupported outside its action contract: text=%q", label, text)
		}
		t.Logf("%s capability_unsupported: %s", label, text)
		return realMCPActionUnsupported
	}
	if doc.Error != nil && doc.Error.Code == "tool_error" && fixtureAllowsCapabilityUnsupported && realMCPGoplsNaturalFileCapabilityError(label, combined) {
		t.Logf("%s gopls natural-file capability unavailable: %s", label, text)
		return realMCPActionUnsupported
	}
	if doc.Error != nil {
		code := doc.Error.Code
		if strings.HasPrefix(code, "lsp_timeout") || code == "lsp_unavailable" || code == "process_exit" || code == "process_failed" {
			t.Fatalf("%s returned a runtime failure code instead of success/capability_unsupported: %s", label, text)
		}
		t.Fatalf("%s returned an unclassified MCP error: %s", label, text)
	}
	if response.Result.IsError {
		t.Fatalf("%s returned an unclassified MCP error: text=%q", label, text)
	}
	if realMCPRemoteCapabilityEmptyMessage(combined) {
		if !fixtureAllowsCapabilityUnsupported && !protocolOptional {
			t.Fatalf("%s returned capability-unavailable outside its action contract: text=%q", label, text)
		}
		return realMCPActionUnsupported
	}
	if strings.Contains(combined, "timed out") || strings.Contains(combined, "process exited") {
		t.Fatalf("%s returned an unstructured runtime failure: text=%q", label, text)
	}
	nonEmpty := realMCPActionContentNonEmpty(doc)
	if requireResult && !nonEmpty {
		t.Fatalf("%s returned an empty successful result; content=%s", label, text)
	}
	if !requireResult && emptyResultReason != "" && !nonEmpty {
		t.Logf("%s returned the explicitly expected empty successful result: %s", label, emptyResultReason)
		return realMCPActionLegalEmpty
	}
	return realMCPActionSucceeded
}

// realMCPGoplsNaturalFileCapabilityError 只适配 gopls 对自然 Go workspace 文件返回的
// “for file type / in file of type”能力缺失。该 E2E 双重要求精确语言标签和精确文件类型，禁止把一般
// tool_error、位置错误或运行时错误降级成 capability unsupported。
func realMCPGoplsNaturalFileCapabilityError(label, text string) bool {
	fileType := ""
	switch {
	case strings.HasPrefix(label, "gopls gomod "):
		fileType = "go.mod"
	case strings.HasPrefix(label, "gopls gosum "):
		fileType = "go.sum"
	case strings.HasPrefix(label, "gopls gowork "):
		fileType = "go.work"
	default:
		return false
	}
	return strings.Contains(text, "for file type "+fileType) ||
		strings.Contains(text, "in file of type "+fileType) ||
		(strings.Contains(text, "unsupported file type:") && strings.Contains(text, fileType))
}

func realMCPRemoteCapabilityEmptyMessage(text string) bool {
	return strings.Contains(text, "unsupported by current language server") || strings.Contains(text, "not available for ")
}

// realMCPActionContentNonEmpty 只依据远程纯文本行协议判断语义 payload 是否非空。
// structuredContent 已从 mcp-lsp wire 契约移除，不能作为 action 分类或生命周期证明依据。
func realMCPActionContentNonEmpty(doc lineprotocol.Document) bool {
	if doc.Header.Total > 0 || doc.Header.Showing > 0 {
		return true
	}
	for _, record := range doc.Records {
		switch record.Kind {
		case "ROW", "FILE", "LEGEND":
			return true
		case "MESSAGE":
			if strings.TrimSpace(record.Value) != "" && !strings.HasPrefix(strings.ToLower(record.Value), "checked+") {
				return true
			}
		}
	}
	return false
}

func realMCPActionSemanticContentNonEmpty(t *testing.T, response mcpLSPBinaryResponse, label string) bool {
	t.Helper()
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("%s returned deprecated structuredContent: %s", label, response.Result.StructuredContent)
	}
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil {
		t.Fatalf("%s semantic content is not valid line protocol: %v; text=%q", label, err, response.Result.ContentText())
	}
	return realMCPActionContentNonEmpty(doc)
}

func assertRealFileContains(t *testing.T, path, want, label string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s read %s: %v", label, path, err)
	}
	if !bytes.Contains(payload, []byte(want)) {
		t.Fatalf("%s did not write %q to %s: %q", label, want, path, payload)
	}
}

func startRealMcpLSPBinary(t *testing.T, ctx context.Context, binary, fixtureRoot, repoRoot, nodeDist, installDir, productionProductRoot string) *mcpLSPBinaryClient {
	t.Helper()
	rawRoots, err := json.Marshal([]string{fixtureRoot})
	if err != nil {
		t.Fatalf("marshal real MCP workspace roots: %v", err)
	}
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = fixtureRoot
	env := os.Environ()
	if strings.TrimSpace(productionProductRoot) == "" {
		env = realNodeEnvironment(env, nodeDist, installDir)
	} else {
		// 生产 cohort 已在调用方通过 EnsureInstalled 写入同一产品根；MCP
		// 不能把 raw phase 的临时 npm/node_modules 注入生产子进程 PATH。
		env = replaceEnv(env, "NODE_PATH", "")
	}
	if runtime.GOOS == "windows" {
		platform, err := installer.DetectWindowsHostPlatform()
		if err != nil {
			t.Fatalf("detect Windows platform for isolated MCP product home: %v", err)
		}
		productHome := strings.TrimSpace(productionProductRoot)
		if productHome == "" {
			productHome, err = os.MkdirTemp("", "sd-node-production-windows-mcp-"+platform.NativeArch+"-")
			if err != nil {
				t.Fatalf("create isolated MCP Windows product home: %v", err)
			}
			// MCP 进程退出后只回收本次测试创建的产品根，并验证没有残留。
			t.Cleanup(func() {
				if err := removeRealWindowsProductRoot(productHome); err != nil {
					t.Errorf("remove isolated MCP Windows product home %s: %v", productHome, err)
				}
			})
			if err := securefs.RestrictPrivateOwnerOnly(productHome, 0o700); err != nil {
				t.Fatalf("restrict isolated MCP Windows product home: %v", err)
			}
		} else {
			info, statErr := os.Stat(productHome)
			if statErr != nil {
				t.Fatalf("stat caller-provisioned MCP Windows product home %q: %v", productHome, statErr)
			}
			if !info.IsDir() {
				t.Fatalf("caller-provisioned MCP Windows product home %q is not a directory", productHome)
			}
		}
		env = replaceEnv(env, "SUPER_DOLPHIN_HOME", productHome)
	}
	env = replaceEnv(env, "GO_AGENT_LSP_ROOT", fixtureRoot)
	env = replaceEnv(env, "GO_AGENT_LSP_ROOTS", string(rawRoots))
	env = replaceEnv(env, "GO_AGENT_PEER_MODE", "0")
	env = replaceEnv(env, "SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	env = replaceEnv(env, "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	env = replaceEnv(env, "SUPER_DOLPHIN_DEPENDENCY_PROFILE", "production")
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("real MCP stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("real MCP stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("real MCP stderr pipe: %v", err)
	}
	client := &mcpLSPBinaryClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start real mcp-lsp binary: %v", err)
	}
	stderrSink := io.Writer(&client.stderr)
	if os.Getenv("MCP_LSP_TRACE_TIMING") == "1" {
		// 诊断轮把受控 child stderr 同步镜像到测试日志，保留 didOpen/didChange/request
		// 状态屏障证据；默认轮仍只缓存 stderr，避免改变正常 E2E 的输出契约。
		stderrSink = io.MultiWriter(&client.stderr, os.Stderr)
	}
	go func() { _, _ = io.Copy(stderrSink, stderr) }()
	return client
}

// realFileURI 将 Windows E2E 的本机路径编码成与生产 multilsp 及 vscode-uri 一致的 file URI。
// 该共享测试入口必须保留小写驱动器和 %3A 冒号，否则 Prisma 会把 schema URI 规范化后与请求 URI 分裂。
func realFileURI(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(path) >= 2 && path[1] == ':' {
		drive := strings.ToLower(path[:1])
		uriPath := "/" + drive + ":" + path[2:]
		fileURL := &url.URL{Scheme: "file", Path: uriPath}
		escapedPath := fileURL.EscapedPath()
		fileURL.RawPath = "/" + drive + "%3A" + escapedPath[3:]
		return fileURL.String()
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
