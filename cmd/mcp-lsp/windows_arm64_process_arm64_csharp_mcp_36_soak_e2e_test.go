//go:build windows && arm64 && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	windowsARM64ProcessARM64CSharpE2EEnv      = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_CSHARP_36_E2E"
	windowsARM64ProcessARM64CSharpInstallEnv  = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_CSHARP_INSTALL_PRECHECK"
	windowsARM64ProcessARM64CSharpPrecheckEnv = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_CSHARP_PRECHECK"
	windowsARM64ProcessARM64CSharpEvidenceDir = ".build-cache/codex-csharp-windows-proof"
	windowsARM64ProcessARM64CSharpReceiptName = "windows-arm64-process-arm64-csharp-mcp-36-soak-receipt.json"
	windowsARM64ProcessARM64CSharpWireName    = "windows-arm64-process-arm64-csharp-mcp-36-soak-wire.jsonl"
	windowsARM64ProcessARM64CSharpManagerIdle = 17 * time.Minute
	windowsARM64ProcessARM64CSharpProofIdle   = 15 * time.Minute
	windowsARM64ProcessARM64CSharpPrecheckMax = 30 * time.Second
	windowsARM64ProcessARM64CSharpFormalMax   = 3 * time.Hour
	windowsARM64ProcessARM64CSharpSDKVersion  = "10.0.400"
	windowsARM64ProcessARM64CSharpVersion     = "0.26.0"
	windowsARM64ProcessARM64CSharpSDKURL      = "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-arm64.zip"
	windowsARM64ProcessARM64CSharpSDKSHA512   = "9d4ecd7439f15c7797d6f46d368cb7aa6513755c5fc3d6de7621bc4878a1805f6b8ffb60ffb9d3e72a049cca87edb252f7c8c03023b643e333544c4606509d7f"
	windowsARM64ProcessARM64CSharpURL         = "https://api.nuget.org/v3-flatcontainer/csharp-ls/0.26.0/csharp-ls.0.26.0.nupkg"
	windowsARM64ProcessARM64CSharpSHA256      = "2b03987aef07bb708bfe56a7bfb370364c7c8203e69aa677a37594bbe21a15b0"
)

// windowsARM64ProcessARM64CSharpHTTPReceipt 只保存计数，不保存 URL、请求头、token 或绝对路径。
type windowsARM64ProcessARM64CSharpHTTPReceipt struct {
	Requests          int
	Attempts          int
	Responses         int
	RedirectResponses int
	TransportErrors   int
}

// windowsARM64ProcessARM64CSharpHTTPObserver 仅包裹真实生产安装使用的 DefaultTransport。
type windowsARM64ProcessARM64CSharpHTTPObserver struct {
	base              http.RoundTripper
	mu                sync.Mutex
	requests          int
	responses         int
	redirectResponses int
	transportErrors   int
}

func (o *windowsARM64ProcessARM64CSharpHTTPObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	if o == nil || o.base == nil {
		return nil, errors.New("C# HTTP observer has no base transport")
	}
	o.mu.Lock()
	o.requests++
	o.mu.Unlock()
	response, err := o.base.RoundTrip(request)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.transportErrors++
		return nil, err
	}
	o.responses++
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		o.redirectResponses++
	}
	return response, nil
}

func (o *windowsARM64ProcessARM64CSharpHTTPObserver) Snapshot() windowsARM64ProcessARM64CSharpHTTPReceipt {
	o.mu.Lock()
	defer o.mu.Unlock()
	return windowsARM64ProcessARM64CSharpHTTPReceipt{
		Requests: o.requests, Attempts: o.requests, Responses: o.responses,
		RedirectResponses: o.redirectResponses, TransportErrors: o.transportErrors,
	}
}

type windowsARM64ProcessARM64CSharpActionReceipt struct {
	Tool          string
	Action        string
	Status        string
	ContentBytes  int
	ContentSHA256 string
}

type windowsARM64ProcessARM64CSharpProcessReceipt struct {
	PID        int
	StartToken string
	Name       string
	Executable string
}

type windowsARM64ProcessARM64CSharpPostIdleClassification struct {
	Total           int
	SemanticSuccess int
	LegalEmpty      int
	Unsupported     int
	NullResult      int
	RuntimeErrors   int
	Complete        bool
	NonEmpty        bool
}

type windowsARM64ProcessARM64CSharpReceipt struct {
	Test                   string
	Status                 string
	FailurePhase           string
	FailureDigest          string
	FailureOutputClass     string
	FailureOutputSummary   string
	FailureExitCode        int
	FailureOutputBytes     int
	FailureOutputSHA256    string
	FailureArgsCount       int
	FailurePackageCount    int
	StartedAt              string
	FinishedAt             string
	Precheck               bool
	ManagerIdleTimeout     string
	ProofIdleDuration      string
	HostOS                 string
	WindowsVersion         string
	WindowsBuild           uint32
	NativeArch             string
	ProcessArch            string
	ProcessArchDiagnostic  bool
	Product                string
	SDKVersion             string
	CSharpLSVersion        string
	SDKURL                 string
	SDKSHA512              string
	CSharpLSURL            string
	CSharpLSSHA256         string
	InstallStatus          string
	Cohort                 string
	ServerPathRelative     string
	CacheBeforeEntries     int
	CacheAfterEntries      int
	CacheBeforeEmpty       bool
	CacheReadyAfterInstall bool
	HTTP                   windowsARM64ProcessARM64CSharpHTTPReceipt
	MCPPID                 int
	MCPStartToken          string
	CSharpPID              int
	CSharpStartToken       string
	MCPIdentityStable      bool
	CSharpIdentityStable   bool
	IdleHeartbeats         int
	PostIdle               windowsARM64ProcessARM64CSharpPostIdleClassification
	ShutdownResponse       bool
	ExitSent               bool
	ZeroResidual           bool
	ActionLedgerComplete   bool
	ActionTotal            int
	ExpectedActionTotal    int
	SemanticSuccess        int
	LegalEmpty             int
	CapabilityUnsupported  int
	NullResult             int
	RuntimeErrors          int
	WirePath               string
	ProcessIdentities      []windowsARM64ProcessARM64CSharpProcessReceipt
	Actions                []windowsARM64ProcessARM64CSharpActionReceipt
}

func windowsARM64ProcessARM64CSharpLockedPlan(t *testing.T, architecture string) installer.WindowsRuntimeDependencyCatalogEntry {
	t.Helper()
	plan, err := installer.WindowsRuntimeDependencyPlanForArchitecture(installer.WindowsRuntimeDependencyProductDotnetCsharpLS, architecture)
	if err != nil {
		t.Fatalf("resolve locked C# runtime plan for %s: %v", architecture, err)
	}
	if plan.Product != installer.WindowsRuntimeDependencyProductDotnetCsharpLS ||
		plan.Install.Command != "dotnet tool install" ||
		!slices.Equal(plan.Install.Args, []string{"tool", "install", "--tool-path", "tools", "--version", "0.26.0", "csharp-ls"}) ||
		plan.Install.RuntimeExecutablePath != "dotnet.exe" ||
		plan.Install.ServerPath != "tools/csharp-ls.exe" {
		t.Fatalf("C# install contract changed: product=%q command=%q runtime=%q args=%v server=%q", plan.Product, plan.Install.Command, plan.Install.RuntimeExecutablePath, plan.Install.Args, plan.Install.ServerPath)
	}
	assets := plan.AssetsByArchitecture[architecture]
	if len(assets) != 2 {
		t.Fatalf("C# %s asset count=%d, want 2", architecture, len(assets))
	}
	var sdk, languageServer installer.WindowsRuntimeDependencyAsset
	for _, asset := range assets {
		switch asset.Component {
		case "dotnet-sdk":
			sdk = asset
		case "csharp-ls":
			languageServer = asset
		}
	}
	if architecture == installer.WindowsHostArchARM64 {
		if sdk.Version != windowsARM64ProcessARM64CSharpSDKVersion || sdk.URL != windowsARM64ProcessARM64CSharpSDKURL || sdk.Checksum != windowsARM64ProcessARM64CSharpSDKSHA512 || sdk.ChecksumAlgorithm != installer.WindowsRuntimeDependencyChecksumSHA512 || !sdk.Native {
			t.Fatalf("locked ARM64 SDK asset changed: %#v", sdk)
		}
		if languageServer.Version != windowsARM64ProcessARM64CSharpVersion || languageServer.URL != windowsARM64ProcessARM64CSharpURL || languageServer.Checksum != windowsARM64ProcessARM64CSharpSHA256 || languageServer.ChecksumAlgorithm != installer.WindowsRuntimeDependencyChecksumSHA256 || languageServer.Native || languageServer.ArchivePath != "tools/net10.0/any/DotnetToolSettings.xml" || languageServer.BinaryPath != "tools/net10.0/any/CSharpLanguageServer.dll" {
			t.Fatalf("locked ARM64 csharp-ls asset changed: %#v", languageServer)
		}
	}
	if sdk.Architecture != architecture || languageServer.Architecture != architecture {
		t.Fatalf("C# plan selected a cross-architecture asset: sdk=%q server=%q want=%q", sdk.Architecture, languageServer.Architecture, architecture)
	}
	if plan.StatusByArchitecture[architecture] != installer.WindowsRuntimeDependencyStatusInstallable {
		t.Fatalf("C# architecture %s status=%q, want installable", architecture, plan.StatusByArchitecture[architecture])
	}
	return plan
}

func windowsARM64ProcessARM64CSharpValidatePE(path string, machine uint16) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open PE: %w", err)
	}
	defer file.Close()
	if uint16(file.FileHeader.Machine) != machine {
		return fmt.Errorf("PE machine=0x%04x want=0x%04x", file.FileHeader.Machine, machine)
	}
	return nil
}

func windowsARM64ProcessARM64CSharpCacheEntries(root string) (int, bool, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	return len(entries), len(entries) == 0, nil
}

func windowsARM64ProcessARM64CSharpRelative(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside product root")
	}
	return filepath.ToSlash(relative), nil
}

func windowsARM64ProcessARM64CSharpDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func windowsARM64ProcessARM64CSharpReceiptFailure(receipt *windowsARM64ProcessARM64CSharpReceipt, phase string, err error) {
	if receipt == nil {
		return
	}
	receipt.Status = "non_pass"
	receipt.FailurePhase = phase
	if err != nil {
		receipt.FailureDigest = windowsARM64ProcessARM64CSharpDigest(err.Error())
		var summary *installer.ProcessFailureError
		if errors.As(err, &summary) && summary != nil {
			receipt.FailureOutputClass = summary.OutputClass
			receipt.FailureOutputSummary = summary.OutputSummary
			receipt.FailureExitCode = summary.ExitCode
			receipt.FailureOutputBytes = summary.OutputBytes
			receipt.FailureOutputSHA256 = summary.OutputSHA256
			receipt.FailureArgsCount = summary.ArgsCount
			receipt.FailurePackageCount = summary.PackageCount
		}
	}
}

const windowsARM64ProcessARM64CSharpMain = "using System;\nnamespace CSharpProof {\n    public interface IGreeter { string Greet(string name); }\n    public sealed class Greeter : IGreeter {\n        public string Greet(string name) => FormatGreeting(name);\n        private static string FormatGreeting(string name) => \"Hello, \" + name;\n    }\n    public static class Program {\n        public static void Main() {\n            IGreeter greeter = new Greeter();\n            Console.WriteLine(greeter.Greet(\"world\"));\n        }\n    }\n}\n"

func windowsARM64ProcessARM64CSharpPosition(t *testing.T, path, content string, line int, needle string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	if line <= 0 || line > len(lines) {
		t.Fatalf("C# fixture line %d is outside %d lines", line, len(lines))
	}
	character := strings.Index(lines[line-1], needle)
	if character < 0 {
		t.Fatalf("C# fixture line %d lacks %q", line, needle)
	}
	return realMCPPositionFromLSP(path, line, character)
}

// windowsARM64ProcessARM64CSharpWriteFixture 保持 Program.cs、csproj 和 global.json 为自然 C# 工程；
// 其他文件只承载可编辑动作，不把另一个语言的 fixture 借来冒充 C# 语义。
func windowsARM64ProcessARM64CSharpWriteFixture(t *testing.T, root string) (realNodeServerCase, realMCPFixture) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create C# fixture root: %v", err)
	}
	target := filepath.Join(root, "Program.cs")
	writeRealFixture(t, target, windowsARM64ProcessARM64CSharpMain)
	writeRealFixture(t, filepath.Join(root, "CSharpProof.csproj"), "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <OutputType>Exe</OutputType>\n    <TargetFramework>net10.0</TargetFramework>\n    <Nullable>enable</Nullable>\n  </PropertyGroup>\n</Project>\n")
	writeRealFixture(t, filepath.Join(root, "global.json"), "{\"sdk\":{\"version\":\"10.0.400\",\"rollForward\":\"disable\",\"allowPrerelease\":false}}\n")
	secondary := filepath.Join(root, "Shared.cs")
	writeRealFixture(t, secondary, "namespace CSharpProof {\n    public static class Shared { public const string RealCSharpNeedle = \"FormatGreeting\"; }\n}\n")
	replace := filepath.Join(root, "Replace.cs")
	writeRealFixture(t, replace, "namespace CSharpProof {\n    // REAL_CSHARP_REPLACE_ME\n}\n")
	rename := filepath.Join(root, "Rename.cs")
	writeRealFixture(t, rename, "namespace CSharpProof {\n    public static class RenameTarget { public static string Value() => \"FormatGreeting\"; }\n}\n")
	codeAction := filepath.Join(root, "CodeAction.cs")
	writeRealFixture(t, codeAction, "namespace CSharpProof {\n    public static class CodeActionProbe {\n        public static string Value() => MissingCSharpIdentifier;\n    }\n}\n")
	format := filepath.Join(root, "Format.cs")
	writeRealFixture(t, format, "namespace CSharpProof {\n    public static class FormatProbe { public static string Value(){return \"format\";} }\n}\n")
	completion := filepath.Join(root, "Completion.cs")
	completionContent := "namespace CSharpProof {\n    public static class CompletionProbe { public static void Run() { var greeter = new Greeter(); greeter. } }\n}\n"
	writeRealFixture(t, completion, completionContent)
	// Program.Main 中的 new Greeter 类型使用点具备稳定的类型语义，
	// 比方法声明/私有实现位置更适合作为 hover/definition/references 锚点。
	semantic := windowsARM64ProcessARM64CSharpPosition(t, target, windowsARM64ProcessARM64CSharpMain, 10, "Greeter")
	renamePosition := windowsARM64ProcessARM64CSharpPosition(t, rename, "namespace CSharpProof {\n    public static class RenameTarget { public static string Value() => \"FormatGreeting\"; }\n}\n", 2, "RenameTarget")
	completionLine := strings.Split(completionContent, "\n")[1]
	completionPosition := realMCPPositionFromLSP(completion, 2, strings.Index(completionLine, "greeter.")+len("greeter."))
	codeActionPosition := windowsARM64ProcessARM64CSharpPosition(t, codeAction, "namespace CSharpProof {\n    public static class CodeActionProbe {\n        public static string Value() => MissingCSharpIdentifier;\n    }\n}\n", 3, "MissingCSharpIdentifier")
	server := realNodeServerCase{
		name: "csharp", languageID: "csharp", fileName: "Program.cs",
		content: windowsARM64ProcessARM64CSharpMain, line: 10,
		character: strings.Index(strings.Split(windowsARM64ProcessARM64CSharpMain, "\n")[9], "Greeter"),
	}
	fixture := realMCPFixture{
		targetFile: target, secondaryFile: secondary, replaceFile: replace, renameFile: rename,
		codeActionFile: codeAction, formatFile: format, completionFile: completion,
		semanticPosition: semantic, renamePosition: renamePosition,
		implementationPosition: semantic, typeDefinitionPosition: semantic,
		callHierarchyPosition: semantic, typeHierarchyPosition: semantic, signaturePosition: semantic,
		completionPosition: completionPosition, codeActionPosition: codeActionPosition,
		replacePatch: "@@\n-    // REAL_CSHARP_REPLACE_ME\n+    // REAL_CSHARP_REPLACED\n",
	}
	return server, fixture
}

// windowsARM64ProcessARM64CSharpActionContract 定义 C# 的真实 36-action 账本；
// unsupported、合法空和非空语义分别记账，任何 null/runtime error 都阻断正式生命周期。
func windowsARM64ProcessARM64CSharpActionContract(tool, action string) (bool, string, bool) {
	switch {
	case tool == "file" && action == "diagnostics", tool == "file" && action == "diagnostics-batch":
		return false, "无诊断时 diagnostics 的空数组是合法成功", false
	case tool == "file" && action == "read_file-function":
		return false, "C# 文件 scope 读取由服务器决定，空结果单列", false
	case tool == "grep" && action == "ast_search":
		return false, "ast-grep C# grammar 不是 C# LSP 语义，合法空单列", false
	case tool == "inspect" && (action == "hover" || action == "definition"):
		return false, "csharp-ls 当前不承诺稳定的 hover/definition payload；合法空单列", false
	case tool == "xref" && (action == "references" || action == "references-no-declaration"):
		return false, "csharp-ls 当前不承诺稳定的 references payload；合法空单列", false
	case tool == "xref" && strings.HasPrefix(action, "call_hierarchy-"):
		return false, "csharp-ls 当前不承诺 call hierarchy payload；合法空单列", false
	case tool == "xref" && strings.HasPrefix(action, "type_hierarchy-"):
		return false, "type hierarchy 依赖服务器 capability，合法空或协议 unsupported 单列", true
	case tool == "structure" && action == "semantic_tokens":
		return false, "csharp-ls 缺少 semantic tokens legend，必须单列 typed capability_unsupported", true
	case tool == "patch_edit" && action == "replace_range":
		return false, "replace_range 的 changed=false 是合法成功", false
	case tool == "patch_edit" && action == "format":
		return false, "已格式化文件允许空 format edit", false
	case tool == "patch_edit" && action == "code_action":
		return false, "quickfix 为空是合法成功", false
	case tool == "patch_edit" && action == "rename":
		return false, "rename 仅验证请求可达，空 edit 单列", false
	case tool == "inspect", tool == "structure", tool == "completion":
		return false, "C# 服务器未承诺该 action 的非空 payload，合法空单列", false
	default:
		return true, "", false
	}
}

func windowsARM64ProcessARM64CSharpActions(server realNodeServerCase, fixture realMCPFixture) []realMCPActionSpec {
	actions := realMCPActionSpecs(server, fixture, realMCPPositionPath(fixture.semanticPosition))
	for index := range actions {
		action := &actions[index]
		action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported = windowsARM64ProcessARM64CSharpActionContract(action.tool, action.name)
		action.contractSet = true
		switch action.tool + "/" + action.name {
		case "grep/ast_search":
			action.args["query"] = "class $NAME { $$$BODY }"
			// ast-grep 没有 csharp 注册 alias；使用已注册 parser 对 C# fixture
			// 做受控空匹配，避免把 invalid_params 错误伪装成 capability 缺失。
			action.args["ast_language"] = "javascript"
		case "structure/workspace_symbol-file":
			action.args["query"] = "FormatGreeting"
			action.args["file_path"] = fixture.targetFile
		case "structure/workspace_symbol-language":
			action.args["query"] = "FormatGreeting"
			action.args["workspace_language"] = "csharp"
		}
		if action.tool == "grep" {
			action.args["query"] = "FormatGreeting"
			action.args["paths"] = []string{fixture.targetFile}
			action.args["glob"] = filepath.Base(fixture.targetFile)
		}
		if action.tool == "structure" && !strings.HasPrefix(action.name, "workspace_symbol-") {
			action.args["file_path"] = fixture.targetFile
		}
	}
	return actions
}

// TestWindowsARM64ProcessARM64CSharpContract 完全不联网，仅锁定生产 catalog、自然 C# fixture 和 36-action 合同。
func TestWindowsARM64ProcessARM64CSharpContract(t *testing.T) {
	// 冷缓存需要下载并校验 .NET SDK/csharp-ls，随后还要完成 36 动作和至少 15 分钟空闲证明；
	// 正式预算不得被收窄成几分钟的预检预算。
	if windowsARM64ProcessARM64CSharpFormalMax < 2*time.Hour {
		t.Fatalf("C# formal timeout=%s, want at least 2h", windowsARM64ProcessARM64CSharpFormalMax)
	}
	plan := windowsARM64ProcessARM64CSharpLockedPlan(t, installer.WindowsHostArchARM64)
	if plan.StatusByArchitecture[installer.WindowsHostArchX64] != installer.WindowsRuntimeDependencyStatusInstallable ||
		plan.StatusByArchitecture[installer.WindowsHostArchX86] != installer.WindowsRuntimeDependencyStatusInstallable {
		t.Fatalf("C# catalog lost x64/x86 installable verdicts")
	}
	server, fixture := windowsARM64ProcessARM64CSharpWriteFixture(t, t.TempDir())
	if filepath.Base(fixture.targetFile) != "Program.cs" || server.languageID != "csharp" {
		t.Fatalf("natural C# target is not Program.cs: server=%#v fixture=%#v", server, fixture)
	}
	payload, err := os.ReadFile(fixture.targetFile)
	if err != nil || !bytes.Contains(payload, []byte("FormatGreeting")) || !bytes.Contains(payload, []byte("IGreeter")) {
		t.Fatalf("natural C# fixture missing semantic symbols: err=%v", err)
	}
	actions := windowsARM64ProcessARM64CSharpActions(server, fixture)
	if len(actions) != realMCPExpectedActionCount {
		t.Fatalf("C# action count=%d want=%d", len(actions), realMCPExpectedActionCount)
	}
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("C# canonical 36-action closure: %v", err)
	}
	for _, action := range actions {
		if !action.contractSet {
			t.Fatalf("%s/%s has no explicit C# contract", action.tool, action.name)
		}
		if action.requireResult && action.emptyResultReason != "" {
			t.Fatalf("%s/%s is both required and legal-empty", action.tool, action.name)
		}
	}
	for _, want := range []struct{ tool, action string }{{"inspect", "hover"}, {"inspect", "definition"}, {"xref", "references"}, {"xref", "references-no-declaration"}} {
		found := false
		for _, action := range actions {
			if action.tool == want.tool && action.name == want.action {
				found = !action.requireResult && action.emptyResultReason != ""
			}
		}
		if !found {
			t.Fatalf("C# %s/%s must have an explicit legal-empty contract", want.tool, want.action)
		}
	}
}

func windowsARM64ProcessARM64CSharpPostIdlePass(classification windowsARM64ProcessARM64CSharpPostIdleClassification, identityStable bool) bool {
	if !identityStable || !classification.Complete || classification.Total != 1 || classification.SemanticSuccess != 1 || !classification.NonEmpty || classification.LegalEmpty != 0 || classification.Unsupported != 0 || classification.NullResult != 0 || classification.RuntimeErrors != 0 {
		return false
	}
	return true
}

// TestWindowsARM64ProcessARM64CSharpPostIdleContract 锁定三次真实非空 semantic health 及身份稳定门禁，不启动 MCP。
func TestWindowsARM64ProcessARM64CSharpPostIdleContract(t *testing.T) {
	tests := []struct {
		name     string
		value    windowsARM64ProcessARM64CSharpPostIdleClassification
		identity bool
		want     bool
	}{
		{"one_nonempty_stable", windowsARM64ProcessARM64CSharpPostIdleClassification{Total: 1, SemanticSuccess: 1, Complete: true, NonEmpty: true}, true, true},
		{"legal_empty_not_semantic_pass", windowsARM64ProcessARM64CSharpPostIdleClassification{Total: 1, LegalEmpty: 1, Complete: true, NonEmpty: false}, true, false},
		{"unsupported_not_semantic_pass", windowsARM64ProcessARM64CSharpPostIdleClassification{Total: 1, Unsupported: 1, Complete: true}, true, false},
		{"null_fails", windowsARM64ProcessARM64CSharpPostIdleClassification{Total: 1, NullResult: 1, Complete: false}, true, false},
		{"identity_change_fails", windowsARM64ProcessARM64CSharpPostIdleClassification{Total: 1, SemanticSuccess: 1, Complete: true, NonEmpty: true}, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := windowsARM64ProcessARM64CSharpPostIdlePass(test.value, test.identity); got != test.want {
				t.Fatalf("post-idle predicate got=%v want=%v value=%#v identity=%v", got, test.want, test.value, test.identity)
			}
		})
	}
}

func windowsARM64ProcessARM64CSharpReceiptArgumentsFree() map[string]any {
	return map[string]any{"language_id": "csharp", "tool_family": "seven-family-36-action-contract"}
}

func windowsARM64ProcessARM64CSharpWriteEvidence(repoRoot string, receipt *windowsARM64ProcessARM64CSharpReceipt) error {
	if receipt == nil {
		return errors.New("nil C# receipt")
	}
	directory := filepath.Join(repoRoot, filepath.FromSlash(windowsARM64ProcessARM64CSharpEvidenceDir))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, windowsARM64ProcessARM64CSharpReceiptName), append(payload, '\n'), 0o600); err != nil {
		return err
	}
	wire, err := os.OpenFile(filepath.Join(directory, windowsARM64ProcessARM64CSharpWireName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer wire.Close()
	encoder := json.NewEncoder(wire)
	return encoder.Encode(map[string]any{
		"event": "summary", "product": receipt.Product, "native_arch": receipt.NativeArch, "process_arch": receipt.ProcessArch,
		"action_total": receipt.ActionTotal, "semantic_success": receipt.SemanticSuccess, "legal_empty": receipt.LegalEmpty,
		"capability_unsupported": receipt.CapabilityUnsupported, "null_result": receipt.NullResult, "runtime_errors": receipt.RuntimeErrors,
		"post_idle_semantic_success": receipt.PostIdle.SemanticSuccess, "post_idle_non_empty": receipt.PostIdle.NonEmpty,
		"shutdown_response": receipt.ShutdownResponse, "exit_sent": receipt.ExitSent, "zero_residual": receipt.ZeroResidual,
		"arguments_summary": windowsARM64ProcessARM64CSharpReceiptArgumentsFree(),
	})
}

func windowsARM64ProcessARM64CSharpEnsureObserved(ctx context.Context, provider *installer.Provider, language string) (installer.InstallResult, windowsARM64ProcessARM64CSharpHTTPReceipt, error) {
	csharpHTTPTransportMu.Lock()
	defer csharpHTTPTransportMu.Unlock()
	base := http.DefaultTransport
	if base == nil {
		base = &http.Transport{}
	}
	observer := &windowsARM64ProcessARM64CSharpHTTPObserver{base: base}
	http.DefaultTransport = observer
	defer func() { http.DefaultTransport = base }()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), language)
	return result, observer.Snapshot(), err
}

var csharpHTTPTransportMu sync.Mutex

func windowsARM64ProcessARM64CSharpRequireIdentity(pid int, startToken, label string) error {
	if pid <= 0 || strings.TrimSpace(startToken) == "" {
		return fmt.Errorf("%s identity incomplete", label)
	}
	alive, err := processAliveForE2E(pid)
	if err != nil {
		return fmt.Errorf("%s PID liveness: %w", label, err)
	}
	if !alive {
		return fmt.Errorf("%s PID is not alive", label)
	}
	current, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		return fmt.Errorf("%s start identity: %w", label, err)
	}
	if current != startToken {
		return fmt.Errorf("%s start identity changed", label)
	}
	return nil
}

func windowsARM64ProcessARM64CSharpSplitCommandLine(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if command[0] == '"' {
		if end := strings.IndexByte(command[1:], '"'); end >= 0 {
			return command[1 : end+1]
		}
	}
	if index := strings.IndexAny(command, " \t"); index >= 0 {
		return command[:index]
	}
	return command
}

func windowsARM64ProcessARM64CSharpFindServerIdentity(tracked map[realMCPProcessKey]realMCPProcessIdentity, serverPath string) (realMCPProcessIdentity, error) {
	shortPath := ""
	if resolved, err := installer.WindowsShortProcessPath(serverPath); err == nil {
		shortPath = resolved
	}
	return windowsARM64ProcessARM64CSharpFindServerIdentityWithShortPath(tracked, serverPath, shortPath)
}

func windowsARM64ProcessARM64CSharpFindServerIdentityWithShortPath(tracked map[realMCPProcessKey]realMCPProcessIdentity, serverPath, shortPath string) (realMCPProcessIdentity, error) {
	want := filepath.Clean(serverPath)
	wantBase := strings.ToLower(filepath.Base(want))
	wantDir := strings.ToLower(filepath.Dir(want))
	wantDLL := strings.ToLower(filepath.Join(filepath.Dir(want), strings.TrimSuffix(filepath.Base(want), filepath.Ext(want))+".dll"))
	for _, identity := range tracked {
		command := identity.Name + " " + identity.CommandLine
		lower := strings.ToLower(command)
		executable := windowsARM64ProcessARM64CSharpSplitCommandLine(identity.CommandLine)
		if shortPath != "" && executable != "" && strings.EqualFold(filepath.Clean(executable), filepath.Clean(shortPath)) {
			return identity, nil
		}
		if !strings.Contains(lower, "csharp-ls") {
			continue
		}
		if executable != "" && strings.EqualFold(filepath.Clean(executable), want) {
			return identity, nil
		}
		// Windows 8.3 apphost basename（例如 CSHARP~1.EXE）仍必须由同一文件
		// 的 canonical→short path 转换得到，禁止仅按短文件名或任意 dotnet 接受。
		// dotnet tool 的 apphost 可能在 CIM 中表现为 dotnet.exe，命令行只暴露
		// resolver-owned tools/csharp-ls.dll；仍要求 DLL 与已解析 server 同目录，
		// 且该进程已由 MCP 后代树捕获，禁止按任意 dotnet 进程猜测归属。
		if strings.Contains(lower, wantDLL) && strings.Contains(lower, wantDir) {
			return identity, nil
		}
		if strings.Contains(lower, wantBase) && (strings.Contains(lower, strings.ToLower(filepath.Dir(want))) || strings.EqualFold(filepath.Base(identity.Name), wantBase)) {
			return identity, nil
		}
	}
	return realMCPProcessIdentity{}, errors.New("tracked MCP tree has no resolver-owned csharp-ls identity")
}

// windowsARM64ProcessARM64CSharpIdentitySummary 只输出可审计的低敏进程事实，
// 不持久化命令行路径；调用方可据此区分 apphost、dotnet+DLL 与错误的任意 dotnet。
func windowsARM64ProcessARM64CSharpIdentitySummary(identity realMCPProcessIdentity) string {
	name := filepath.Base(strings.ReplaceAll(identity.Name, "/", "\\"))
	return fmt.Sprintf("pid=%d parent_pid=%d name=%s command_args=%d command_sha256=%s", identity.PID, identity.ParentPID, name, windowsARM64ProcessARM64CSharpCommandArgCount(identity.CommandLine), identity.CommandSHA256)
}

func windowsARM64ProcessARM64CSharpCommandArgCount(command string) int {
	return len(strings.Fields(command))
}

func TestWindowsARM64ProcessARM64CSharpFindServerIdentityAcceptsDotnetDLL(t *testing.T) {
	serverPath := `C:\product\cache\tools\csharp-ls.exe`
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: 101, StartToken: "dotnet"}: {
			PID: 101, ParentPID: 100, StartToken: "dotnet", Name: "dotnet.exe",
			CommandLine: `C:\product\dotnet.exe C:\product\cache\tools\csharp-ls.dll`,
		},
	}
	got, err := windowsARM64ProcessARM64CSharpFindServerIdentity(tracked, serverPath)
	if err != nil || got.PID != 101 {
		t.Fatalf("dotnet apphost identity = %#v, err=%v", got, err)
	}
}

func TestWindowsARM64ProcessARM64CSharpFindServerIdentityRejectsForeignDotnet(t *testing.T) {
	serverPath := `C:\product\cache\tools\csharp-ls.exe`
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: 202, StartToken: "foreign"}: {
			PID: 202, ParentPID: 100, StartToken: "foreign", Name: "dotnet.exe",
			CommandLine: `C:\other\dotnet.exe C:\other\tools\csharp-ls.dll`,
		},
	}
	if _, err := windowsARM64ProcessARM64CSharpFindServerIdentity(tracked, serverPath); err == nil {
		t.Fatal("foreign dotnet/csharp-ls identity was accepted")
	}
}

func TestWindowsARM64ProcessARM64CSharpFindServerIdentityAcceptsResolverShortApphost(t *testing.T) {
	serverPath := `C:\product\cache\tools\csharp-ls.exe`
	shortPath := `C:\PRODUC~1\CACHE\TOOLS\CSHARP~1.EXE`
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: 303, StartToken: "short"}: {
			PID: 303, ParentPID: 100, StartToken: "short", Name: "CSHARP~1.EXE",
			CommandLine: shortPath,
		},
	}
	got, err := windowsARM64ProcessARM64CSharpFindServerIdentityWithShortPath(tracked, serverPath, shortPath)
	if err != nil || got.PID != 303 {
		t.Fatalf("resolver short apphost identity = %#v, err=%v", got, err)
	}
}

func windowsARM64ProcessARM64CSharpSanitizeIdentities(tracked map[realMCPProcessKey]realMCPProcessIdentity) []windowsARM64ProcessARM64CSharpProcessReceipt {
	result := make([]windowsARM64ProcessARM64CSharpProcessReceipt, 0, len(tracked))
	for _, identity := range tracked {
		executable := windowsARM64ProcessARM64CSharpSplitCommandLine(identity.CommandLine)
		name := filepath.Base(strings.ReplaceAll(executable, "/", "\\"))
		if name == "." || name == "" {
			name = filepath.Base(identity.Name)
		}
		result = append(result, windowsARM64ProcessARM64CSharpProcessReceipt{PID: identity.PID, StartToken: identity.StartToken, Name: filepath.Base(identity.Name), Executable: name})
	}
	slices.SortFunc(result, func(left, right windowsARM64ProcessARM64CSharpProcessReceipt) int { return left.PID - right.PID })
	return result
}

func windowsARM64ProcessARM64CSharpWaitIdle(ctx context.Context, t *testing.T, mcpPID int, mcpStart string, server realMCPProcessIdentity, duration time.Duration) int {
	t.Helper()
	if duration < windowsARM64ProcessARM64CSharpProofIdle {
		t.Fatalf("C# formal idle duration=%s below production minimum=%s", duration, windowsARM64ProcessARM64CSharpProofIdle)
	}
	started := time.Now()
	heartbeats := 0
	sample := func() {
		if err := windowsARM64ProcessARM64CSharpRequireIdentity(mcpPID, mcpStart, "MCP idle"); err != nil {
			t.Fatalf("MCP identity changed during C# idle: %v", err)
		}
		if err := windowsARM64ProcessARM64CSharpRequireIdentity(server.PID, server.StartToken, "csharp-ls idle"); err != nil {
			t.Fatalf("csharp-ls identity changed during idle: %v", err)
		}
		heartbeats++
		t.Logf("Windows runtime-dependency E2E heartbeat product=dotnet-csharp-ls platform=windows-native-arm64-process-arm64 elapsed=%s mcp_pid=%d csharp_pid=%d", time.Since(started).Round(time.Second), mcpPID, server.PID)
	}
	sample()
	deadline := started.Add(duration)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return heartbeats
		}
		wait := remaining
		if wait > time.Minute {
			wait = time.Minute
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			t.Fatalf("C# idle sampling stopped before %s: %v", duration, ctx.Err())
		case <-timer.C:
		}
		sample()
	}
}

func windowsARM64ProcessARM64CSharpCloseClient(t *testing.T, client *mcpLSPBinaryClient) bool {
	t.Helper()
	if client == nil || client.cmd == nil {
		return false
	}
	cmd := client.cmd
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	if err != nil {
		return false
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		return false
	}
	if err := client.stdin.Close(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		client.cmd = nil
		return err == nil || errors.Is(err, os.ErrProcessDone)
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		client.cmd = nil
		t.Errorf("C# MCP process required bounded kill after exit")
		return false
	}
}

// windowsARM64ProcessARM64CSharpNotify 发送 JSON-RPC notification，不等待不存在的 response。
func windowsARM64ProcessARM64CSharpNotify(client *mcpLSPBinaryClient, method string, params map[string]any) error {
	if client == nil || client.cmd == nil {
		return errors.New("C# MCP client is not live")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("marshal C# notification %s: %w", method, err)
	}
	if _, err := client.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write C# notification %s: %w", method, err)
	}
	return nil
}

func windowsARM64ProcessARM64CSharpRunActions(t *testing.T, client *mcpLSPBinaryClient, server realNodeServerCase, fixture realMCPFixture, workDir string, receipt *windowsARM64ProcessARM64CSharpReceipt, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, mcpStart string) {
	t.Helper()
	actions := windowsARM64ProcessARM64CSharpActions(server, fixture)
	if err := validateRealMCPActionClosure(actions); err != nil {
		windowsARM64ProcessARM64CSharpReceiptFailure(receipt, "action_closure", err)
		t.Fatalf("C# canonical action closure: %v", err)
	}
	for _, action := range actions {
		key := action.tool + "/" + action.name
		args := realMCPWindowsToolArguments(server.languageID, workDir, action.tool, action.name, action.args)
		if action.tool == "patch_edit" {
			path := fixture.replaceFile
			if action.name != "replace_range" {
				path = fixture.targetFile
			}
			setup := client.callTool(t, "file", realMCPWindowsToolArguments(server.languageID, workDir, "file", "open_file", map[string]any{"action": "open_file", "file_path": path}))
			if setup.Result.IsError {
				t.Fatalf("C# patch setup failed for %s", key)
			}
		}
		response := client.callTool(t, action.tool, args)
		status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "C# "+key)
		content := response.Result.ContentText()
		contentDigest := windowsARM64ProcessARM64CSharpDigest(content)
		record := windowsARM64ProcessARM64CSharpActionReceipt{Tool: action.tool, Action: action.name, Status: string(status), ContentBytes: len(content), ContentSHA256: contentDigest}
		receipt.Actions = append(receipt.Actions, record)
		receipt.ActionTotal++
		switch status {
		case realMCPActionSucceeded:
			receipt.SemanticSuccess++
		case realMCPActionLegalEmpty:
			receipt.LegalEmpty++
		case realMCPActionUnsupported:
			receipt.CapabilityUnsupported++
		default:
			receipt.RuntimeErrors++
			t.Fatalf("C# %s returned unclassified status=%q", key, status)
		}
		if !trackRealMCPProcessTree(t, mcpPID, "csharp-"+key, tracked) {
			t.Fatalf("capture C# process tree after %s failed", key)
		}
		if err := windowsARM64ProcessARM64CSharpRequireIdentity(mcpPID, mcpStart, "MCP after "+key); err != nil {
			t.Fatalf("MCP identity after %s: %v", key, err)
		}
	}
	receipt.ActionLedgerComplete = receipt.ActionTotal == realMCPExpectedActionCount &&
		receipt.SemanticSuccess+receipt.LegalEmpty+receipt.CapabilityUnsupported+receipt.NullResult+receipt.RuntimeErrors == receipt.ActionTotal
	if !receipt.ActionLedgerComplete || receipt.NullResult != 0 || receipt.RuntimeErrors != 0 {
		t.Fatalf("C# 36-action ledger incomplete: total=%d success=%d legal_empty=%d unsupported=%d null=%d errors=%d", receipt.ActionTotal, receipt.SemanticSuccess, receipt.LegalEmpty, receipt.CapabilityUnsupported, receipt.NullResult, receipt.RuntimeErrors)
	}
}

func windowsARM64ProcessARM64CSharpPrecheck(t *testing.T, repoRoot string) {
	t.Helper()
	server, fixture := windowsARM64ProcessARM64CSharpWriteFixture(t, t.TempDir())
	actions := windowsARM64ProcessARM64CSharpActions(server, fixture)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("C# precheck action closure: %v", err)
	}
	t.Logf("NON_PASS C# bounded precheck max=%s; no install, download, MCP, action call or lifecycle proof", windowsARM64ProcessARM64CSharpPrecheckMax)
	_ = repoRoot
	t.Skip("NON_PASS bounded C# structure precheck; formal install/lifecycle requires explicit E2E env")
}

// TestWindowsARM64ProcessARM64CSharp36SoakE2E is the opt-in production C# proof.
// It is intentionally not run by default; a formal run must download from an empty private
// product root and keep the real MCP/csharp-ls PID+start identity alive for 15 minutes.
func TestWindowsARM64ProcessARM64CSharp36SoakE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64CSharpE2EEnv) != "1" {
		t.Skipf("set %s=1 to enable the networked Windows ARM64/process ARM64 C# E2E", windowsARM64ProcessARM64CSharpE2EEnv)
	}
	if os.Getenv(windowsARM64ProcessARM64CSharpPrecheckEnv) == "1" {
		windowsARM64ProcessARM64CSharpPrecheck(t, realNodeRepoRoot(t))
	}
	if testing.Short() {
		t.Skip("formal C# E2E is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("C# formal proof requires Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("C# formal proof requires Windows/NativeArch=ProcessArch=arm64, got os=%q native=%q process=%q", host.OS, host.NativeArch, host.ProcessArch)
	}
	if err := installer.ValidateWindowsRuntimeDependencyCatalog(); err != nil {
		t.Fatalf("validate locked Windows runtime dependency catalog: %v", err)
	}
	plan := windowsARM64ProcessARM64CSharpLockedPlan(t, host.NativeArch)
	repoRoot := realNodeRepoRoot(t)
	productRoot, err := os.MkdirTemp("", "sd-csharp-production-windows-arm64-")
	if err != nil {
		t.Fatalf("create private C# product root: %v", err)
	}
	t.Cleanup(func() {
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			t.Errorf("cleanup C# Windows ARM64 product root: %v", err)
		}
	})
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict private C# product root: %v", err)
	}
	cacheRoot := windowsRuntimeDependencyCacheRoot(productRoot)
	beforeEntries, beforeEmpty, err := windowsARM64ProcessARM64CSharpCacheEntries(cacheRoot)
	if err != nil || !beforeEmpty {
		t.Fatalf("C# product cache must start empty: entries=%d empty=%t err=%v", beforeEntries, beforeEmpty, err)
	}
	fixtureRoot := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(fixtureRoot, 0o700); err != nil {
		t.Fatalf("restrict C# fixture root: %v", err)
	}
	server, fixture := windowsARM64ProcessARM64CSharpWriteFixture(t, fixtureRoot)
	receipt := &windowsARM64ProcessARM64CSharpReceipt{
		Test: "windows-arm64-process-arm64-csharp-mcp-36-soak", Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ManagerIdleTimeout: windowsARM64ProcessARM64CSharpManagerIdle.String(), ProofIdleDuration: windowsARM64ProcessARM64CSharpProofIdle.String(),
		HostOS: host.OS, WindowsVersion: host.WindowsVersion, WindowsBuild: host.WindowsBuild, NativeArch: host.NativeArch, ProcessArch: host.ProcessArch, ProcessArchDiagnostic: true,
		Product: string(plan.Product), SDKVersion: windowsARM64ProcessARM64CSharpSDKVersion, CSharpLSVersion: windowsARM64ProcessARM64CSharpVersion,
		SDKURL: windowsARM64ProcessARM64CSharpSDKURL, SDKSHA512: windowsARM64ProcessARM64CSharpSDKSHA512, CSharpLSURL: windowsARM64ProcessARM64CSharpURL, CSharpLSSHA256: windowsARM64ProcessARM64CSharpSHA256,
		CacheBeforeEntries: beforeEntries, CacheBeforeEmpty: beforeEmpty, ExpectedActionTotal: realMCPExpectedActionCount,
		WirePath: filepath.ToSlash(filepath.Join(windowsARM64ProcessARM64CSharpEvidenceDir, windowsARM64ProcessARM64CSharpWireName)),
	}
	defer func() {
		if receipt.Status == "running" {
			receipt.Status = "non_pass"
			receipt.FailurePhase = "test_exit_before_formal_completion"
		}
		receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := windowsARM64ProcessARM64CSharpWriteEvidence(repoRoot, receipt); err != nil {
			t.Errorf("write C# receipt/wire: %v", err)
		}
	}()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", windowsARM64ProcessARM64CSharpManagerIdle.String())
	ctx, cancel := context.WithTimeout(context.Background(), windowsARM64ProcessARM64CSharpFormalMax)
	defer cancel()
	provider := setupInstaller()
	result, httpReceipt, err := windowsARM64ProcessARM64CSharpEnsureObserved(ctx, provider, "csharp")
	receipt.HTTP = httpReceipt
	if err != nil {
		windowsARM64ProcessARM64CSharpReceiptFailure(receipt, "ensure_installed", err)
		t.Fatalf("production EnsureInstalledDetailed(csharp) from empty product root: %v", err)
	}
	if result.Status != installer.InstallStatusInstalledPath {
		err := fmt.Errorf("status=%s want=%s", result.Status, installer.InstallStatusInstalledPath)
		windowsARM64ProcessARM64CSharpReceiptFailure(receipt, "ensure_status", err)
		t.Fatalf("production C# install status: %v", err)
	}
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductDotnetCsharpLS, cacheRoot)
	if err != nil {
		windowsARM64ProcessARM64CSharpReceiptFailure(receipt, "resolver", err)
		t.Fatalf("resolve ready C# cohort: %v", err)
	}
	if filepath.Clean(result.Path) != filepath.Clean(resolved.ServerPath) {
		t.Fatalf("production C# resolver identity mismatch")
	}
	if resolved.Architecture != host.NativeArch || resolved.Platform.NativeArch != host.NativeArch || resolved.Platform.ProcessArch != host.ProcessArch {
		t.Fatalf("C# resolver architecture mismatch: result=%#v", resolved)
	}
	serverRelative, err := windowsARM64ProcessARM64CSharpRelative(productRoot, resolved.ServerPath)
	if err != nil {
		t.Fatalf("C# server escaped product root: %v", err)
	}
	receipt.ServerPathRelative, receipt.Cohort, receipt.InstallStatus = serverRelative, resolved.Cohort, string(result.Status)
	if err := windowsARM64ProcessARM64CSharpValidatePE(resolved.ServerPath, installer.WindowsImageFileMachineARM64); err != nil {
		windowsARM64ProcessARM64CSharpReceiptFailure(receipt, "csharp_pe", err)
		t.Fatalf("installed csharp-ls is not ARM64 PE: %v", err)
	}
	if err := windowsARM64ProcessARM64CSharpValidatePE(resolved.ExecutablePath, installer.WindowsImageFileMachineARM64); err != nil {
		windowsARM64ProcessARM64CSharpReceiptFailure(receipt, "dotnet_pe", err)
		t.Fatalf("installed dotnet runtime is not ARM64 PE: %v", err)
	}
	afterEntries, afterEmpty, err := windowsARM64ProcessARM64CSharpCacheEntries(cacheRoot)
	if err != nil || afterEmpty || receipt.HTTP.Requests == 0 || receipt.HTTP.Attempts == 0 || receipt.HTTP.Responses == 0 || receipt.HTTP.TransportErrors != 0 {
		t.Fatalf("C# empty-cache download contract failed: entries=%d empty=%t http=%#v err=%v", afterEntries, afterEmpty, receipt.HTTP, err)
	}
	receipt.CacheAfterEntries, receipt.CacheReadyAfterInstall = afterEntries, !afterEmpty
	dotnetRoot := filepath.Dir(resolved.ExecutablePath)
	t.Setenv("DOTNET_ROOT", dotnetRoot)
	t.Setenv("DOTNET_ROOT_ARM64", dotnetRoot)
	t.Setenv("DOTNET_MULTILEVEL_LOOKUP", "0")
	t.Setenv("DOTNET_CLI_HOME", filepath.Join(productRoot, "dotnet-home"))
	t.Setenv("NUGET_PACKAGES", filepath.Join(productRoot, "nuget-packages"))
	t.Setenv("NUGET_HTTP_CACHE_PATH", filepath.Join(productRoot, "nuget-http-cache"))
	t.Setenv("APPDATA", filepath.Join(productRoot, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(productRoot, "localappdata"))
	versionOutput, err := exec.Command(resolved.ExecutablePath, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOutput), windowsARM64ProcessARM64CSharpSDKVersion) {
		t.Fatalf("product-owned dotnet version check failed output=%q err=%v", strings.TrimSpace(string(versionOutput)), err)
	}
	if os.Getenv(windowsARM64ProcessARM64CSharpInstallEnv) == "1" {
		// 安装 precheck 只证明锁定资产、NuGet 本地源和 ARM64 PE；不冒充 36-action/lifecycle PASS。
		receipt.Status = "install_precheck_pass"
		return
	}
	binary := buildRealMcpLSPBinary(t, repoRoot)
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	tracked := make(map[realMCPProcessKey]realMCPProcessIdentity)
	mcpPID := client.cmd.Process.Pid
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture MCP PID/start identity: %v", err)
	}
	tracked[realMCPProcessKey{PID: mcpPID, StartToken: mcpStart}] = realMCPProcessIdentity{PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "csharp-mcp"}
	receipt.MCPPID, receipt.MCPStartToken, receipt.MCPIdentityStable = mcpPID, mcpStart, true
	defer func() {
		if client != nil && client.cmd != nil {
			_ = windowsARM64ProcessARM64CSharpCloseClient(t, client)
		}
	}()
	initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "super-dolphin-csharp-windows-arm64", "version": "1"}})
	if initialize.Error != nil {
		t.Fatalf("C# MCP initialize returned error")
	}
	if err := windowsARM64ProcessARM64CSharpNotify(client, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("C# initialized notification failed: %v", err)
	}
	tools := callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{})
	requireRealMCPToolFamilies(t, tools)
	windowsARM64ProcessARM64CSharpRunActions(t, client, server, fixture, fixtureRoot, receipt, tracked, mcpPID, mcpStart)
	trackRealMCPProcessTree(t, mcpPID, "csharp-final-before-idle", tracked)
	csharpIdentity, err := windowsARM64ProcessARM64CSharpFindServerIdentity(tracked, resolved.ServerPath)
	if err != nil {
		for _, identity := range tracked {
			t.Logf("C# resolver identity candidate %s", windowsARM64ProcessARM64CSharpIdentitySummary(identity))
		}
		t.Fatalf("capture resolver-owned csharp-ls PID/start identity: %v", err)
	}
	receipt.CSharpPID, receipt.CSharpStartToken, receipt.CSharpIdentityStable = csharpIdentity.PID, csharpIdentity.StartToken, true
	receipt.IdleHeartbeats = windowsARM64ProcessARM64CSharpWaitIdle(ctx, t, mcpPID, mcpStart, csharpIdentity, windowsARM64ProcessARM64CSharpProofIdle)
	receipt.PostIdle.Total = 1
	for _, post := range []struct {
		tool   string
		action string
		args   map[string]any
	}{
		// 未定义符号会产生稳定的真实 diagnostics；它仍通过 LSP 子进程证明 post-idle
		// 语义链路，不把文件读取或共享文本结果冒充语义成功。
		{"file", "diagnostics", map[string]any{"action": "diagnostics", "file_path": fixture.codeActionFile}},
	} {
		response := client.callTool(t, post.tool, realMCPWindowsToolArguments("csharp", fixtureRoot, post.tool, post.action, post.args))
		status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(post.tool, post.action), realMCPActionProtocolOptional(post.tool, post.action), "C# post-idle "+post.tool+"/"+post.action)
		if status != realMCPActionSucceeded || !realMCPActionSemanticContentNonEmpty(t, response, "C# post-idle "+post.tool+"/"+post.action) {
			t.Fatalf("C# post-idle %s/%s was not non-empty semantic success: status=%s", post.tool, post.action, status)
		}
		receipt.PostIdle.SemanticSuccess++
		if err := windowsARM64ProcessARM64CSharpRequireIdentity(mcpPID, mcpStart, "MCP post-idle"); err != nil {
			t.Fatalf("MCP identity after C# post-idle: %v", err)
		}
		if err := windowsARM64ProcessARM64CSharpRequireIdentity(csharpIdentity.PID, csharpIdentity.StartToken, "csharp-ls post-idle"); err != nil {
			t.Fatalf("csharp-ls identity after post-idle: %v", err)
		}
	}
	receipt.PostIdle.Complete, receipt.PostIdle.NonEmpty = true, true
	if !windowsARM64ProcessARM64CSharpPostIdlePass(receipt.PostIdle, receipt.CSharpIdentityStable && receipt.MCPIdentityStable) {
		t.Fatalf("C# post-idle semantic health predicate failed: %#v", receipt.PostIdle)
	}
	if !trackRealMCPProcessTree(t, mcpPID, "csharp-final-before-close", tracked) {
		t.Fatalf("capture final C# process tree before shutdown failed")
	}
	receipt.ProcessIdentities = windowsARM64ProcessARM64CSharpSanitizeIdentities(tracked)
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.Error != nil {
		t.Fatalf("C# shutdown returned JSON-RPC error")
	}
	receipt.ShutdownResponse = true
	receipt.ExitSent = windowsARM64ProcessARM64CSharpCloseClient(t, client)
	if !receipt.ExitSent {
		t.Fatalf("C# exit did not complete cleanly")
	}
	requireRealMCPProcessIdentitiesGone(t, tracked)
	receipt.ZeroResidual = true
	receipt.Status = "pass"
	receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
}
