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
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	windowsARM64ProcessARM64JavaE2EEnv            = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_JAVA_36_E2E"
	windowsARM64ProcessARM64JavaPrecheckEnv       = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_JAVA_PRECHECK"
	windowsARM64ProcessARM64JavaRenameProbeEnv    = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_JAVA_RENAME_PROBE"
	windowsARM64ProcessARM64JavaCrossToolProbeEnv = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_JAVA_CROSS_TOOL_PROBE"
	windowsARM64ProcessARM64JavaCacheRootEnv      = "SUPER_DOLPHIN_WINDOWS_ARM64_JAVA_EXISTING_PRODUCT_ROOT"
	windowsARM64ProcessARM64JavaEvidenceDir       = ".build-cache/codex-java-windows-proof"
	windowsARM64ProcessARM64JavaEvidenceDirEnv    = "MCP_LSP_WINDOWS_ARM64_JAVA_EVIDENCE_DIR"
	windowsARM64ProcessARM64JavaReceiptName       = "windows-arm64-process-arm64-java-mcp-36-soak-receipt.json"
	windowsARM64ProcessARM64JavaWireName          = "windows-arm64-process-arm64-java-mcp-36-soak-wire.jsonl"
	windowsARM64ProcessARM64JavaManagerIdle       = 17 * time.Minute
	windowsARM64ProcessARM64JavaProofIdle         = 15 * time.Minute
	windowsARM64ProcessARM64JavaPrecheckMax       = 30 * time.Second
	windowsARM64ProcessARM64JavaFormalMax         = 3 * time.Hour
	windowsARM64ProcessARM64JavaRenameProbeMax    = 6 * time.Minute
	windowsARM64ProcessARM64JavaJDKVersion        = "21.0.12"
	windowsARM64ProcessARM64JavaJDTLSVersion      = "1.60.0"
	windowsARM64ProcessARM64JavaJDKURL            = "https://aka.ms/download-jdk/microsoft-jdk-21.0.12-windows-aarch64.zip"
	windowsARM64ProcessARM64JavaJDKSHA256         = "2118bb60b19002a0bcc420267518352f10d2be25ce1c79c51701b87b209bbc2a"
	windowsARM64ProcessARM64JavaJDKURLX64         = "https://aka.ms/download-jdk/microsoft-jdk-21.0.12-windows-x64.zip"
	windowsARM64ProcessARM64JavaJDKSHA256X64      = "bf27a5d6298c736af8daf5b8c883098e83291446e5766118d8a5ea6a2617195d"
	windowsARM64ProcessARM64JavaJDTLSURL          = "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz"
	windowsARM64ProcessARM64JavaJDTLSSHA256       = "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d"
)

// windowsARM64ProcessARM64JavaHTTPReceipt 只保存计数，不保存 URL、请求头、token 或绝对路径。
type windowsARM64ProcessARM64JavaHTTPReceipt struct {
	Requests          int
	Attempts          int
	Responses         int
	RedirectResponses int
	TransportErrors   int
}

// windowsARM64ProcessARM64JavaHTTPObserver 仅包裹真实生产安装使用的 DefaultTransport。
type windowsARM64ProcessARM64JavaHTTPObserver struct {
	base              http.RoundTripper
	mu                sync.Mutex
	requests          int
	responses         int
	redirectResponses int
	transportErrors   int
}

// windowsARM64ProcessARM64JavaScopeInputEvent 记录请求输入的低敏身份摘要；不输出路径正文。
type windowsARM64ProcessARM64JavaScopeInputEvent struct {
	InputFileDigest     string `json:"input_file_digest"`
	WorkDirDigest       string `json:"work_dir_digest"`
	WorkDirPresent      bool   `json:"work_dir_present"`
	RootDepth           int    `json:"root_depth"`
	RootUTF16Units      int    `json:"root_utf16_units"`
	ContainsTilde       bool   `json:"contains_tilde"`
	WindowsVolumeDigest string `json:"windows_volume_digest"`
	FileIDDigest        string `json:"file_id_digest"`
	FinalPathDigest     string `json:"final_path_digest"`
	FileIDAvailable     bool   `json:"file_id_available"`
	FinalPathAvailable  bool   `json:"final_path_available"`
}

func windowsARM64ProcessARM64JavaScopeInput(t *testing.T, filePath, workDir string) {
	t.Helper()
	event := windowsARM64ProcessARM64JavaScopeInputEvent{
		InputFileDigest:     windowsARM64ProcessARM64JavaDigest(filePath),
		WorkDirDigest:       windowsARM64ProcessARM64JavaDigest(workDir),
		WorkDirPresent:      strings.TrimSpace(workDir) != "",
		RootDepth:           strings.Count(filepath.Clean(workDir), string(filepath.Separator)),
		RootUTF16Units:      len([]rune(workDir)),
		ContainsTilde:       strings.Contains(workDir, "~"),
		WindowsVolumeDigest: windowsARM64ProcessARM64JavaDigest(filepath.VolumeName(workDir)),
	}
	if finalPath, err := filepath.EvalSymlinks(filePath); err == nil {
		event.FinalPathDigest = windowsARM64ProcessARM64JavaDigest(finalPath)
		event.FinalPathAvailable = true
	}
	if output, err := exec.Command("fsutil", "file", "queryfileid", filePath).CombinedOutput(); err == nil && len(output) > 0 {
		event.FileIDDigest = windowsARM64ProcessARM64JavaDigest(string(output))
		event.FileIDAvailable = true
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal Java scope input observation: %v", err)
	}
	t.Logf("Java scope input event=%s", payload)
}

func (o *windowsARM64ProcessARM64JavaHTTPObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	if o == nil || o.base == nil {
		return nil, errors.New("Java HTTP observer has no base transport")
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

func (o *windowsARM64ProcessARM64JavaHTTPObserver) Snapshot() windowsARM64ProcessARM64JavaHTTPReceipt {
	o.mu.Lock()
	defer o.mu.Unlock()
	return windowsARM64ProcessARM64JavaHTTPReceipt{
		Requests: o.requests, Attempts: o.requests, Responses: o.responses,
		RedirectResponses: o.redirectResponses, TransportErrors: o.transportErrors,
	}
}

type windowsARM64ProcessARM64JavaActionReceipt struct {
	Tool          string
	Action        string
	Status        string
	ContentBytes  int
	ContentSHA256 string
}

type windowsARM64ProcessARM64JavaProcessReceipt struct {
	PID        int
	StartToken string
	Name       string
	Executable string
}

type windowsARM64ProcessARM64JavaPostIdleClassification struct {
	Total           int
	SemanticSuccess int
	LegalEmpty      int
	Unsupported     int
	NullResult      int
	RuntimeErrors   int
	Complete        bool
	NonEmpty        bool
}

type windowsARM64ProcessARM64JavaReceipt struct {
	Test                    string
	Status                  string
	FailurePhase            string
	FailureDigest           string
	FailureOperation        string
	FailureExitCategory     string
	FailureElapsedMillis    int64
	FailureContextCause     string
	BuildCommandID          string
	StartedAt               string
	FinishedAt              string
	Precheck                bool
	ManagerIdleTimeout      string
	ProofIdleDuration       string
	HostOS                  string
	WindowsVersion          string
	WindowsBuild            uint32
	NativeArch              string
	ProcessArch             string
	ProcessArchDiagnostic   bool
	Product                 string
	JDKVersion              string
	JDTLSVersion            string
	ServerProfileExpected   string
	ServerProfileApplied    bool
	ServerProfilePredicates map[string]bool
	TypeDefinitionSent      bool
	JDKURL                  string
	JDKSHA256               string
	JDTLSURL                string
	JDTLSSHA256             string
	InstallStatus           string
	Cohort                  string
	ServerPathRelative      string
	CacheBeforeEntries      int
	CacheAfterEntries       int
	CacheBeforeEmpty        bool
	CacheReadyAfterInstall  bool
	CacheDecision           string
	CacheDecisionReason     string
	HTTP                    windowsARM64ProcessARM64JavaHTTPReceipt
	MCPPID                  int
	MCPStartToken           string
	JavaPID                 int
	JavaStartToken          string
	MCPIdentityStable       bool
	JavaIdentityStable      bool
	IdleHeartbeats          int
	PostIdle                windowsARM64ProcessARM64JavaPostIdleClassification
	ShutdownResponse        bool
	ExitSent                bool
	ZeroResidual            bool
	ActionLedgerComplete    bool
	ActionTotal             int
	ExpectedActionTotal     int
	SemanticSuccess         int
	LegalEmpty              int
	CapabilityUnsupported   int
	NullResult              int
	RuntimeErrors           int
	WirePath                string
	ProcessIdentities       []windowsARM64ProcessARM64JavaProcessReceipt
	Actions                 []windowsARM64ProcessARM64JavaActionReceipt
}

func parseJavaReceiptStart(value string) time.Time {
	started, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Now().UTC()
	}
	return started
}

func contextCause(ctx context.Context) string {
	if ctx == nil || context.Cause(ctx) == nil {
		return "none"
	}
	return context.Cause(ctx).Error()
}

func windowsARM64ProcessARM64JavaLockedPlan(t *testing.T, architecture string) installer.WindowsRuntimeDependencyCatalogEntry {
	t.Helper()
	plan, err := installer.WindowsRuntimeDependencyPlanForArchitecture(installer.WindowsRuntimeDependencyProductJDKJDTLS, architecture)
	if err != nil {
		t.Fatalf("resolve locked Java/JDTLS runtime plan for %s: %v", architecture, err)
	}
	wantArgs := []string{
		"-Declipse.application=org.eclipse.jdt.ls.core.id1",
		"-Dosgi.bundles.defaultStartLevel=4",
		"-Declipse.product=org.eclipse.jdt.ls.core.product",
		"-Dlog.protocol=true",
		"-Dlog.level=ALL",
		"-jar",
		"plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar",
		"-configuration",
		"config_win",
	}
	// 生产目录中的 catalog 版本实际没有重复 v1.7.200；保留独立断言以便发现锁定漂移。
	if plan.Product != installer.WindowsRuntimeDependencyProductJDKJDTLS ||
		plan.Install.Command != "java" ||
		!slices.Equal(plan.Install.Args, wantArgs) ||
		plan.Install.RuntimeExecutablePath != "jdk-21.0.12+8/bin/java.exe" ||
		plan.Install.ServerPath != "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar" {
		t.Fatalf("Java/JDTLS install contract changed: product=%q command=%q runtime=%q args=%v server=%q", plan.Product, plan.Install.Command, plan.Install.RuntimeExecutablePath, plan.Install.Args, plan.Install.ServerPath)
	}
	assets := plan.AssetsByArchitecture[architecture]
	if architecture == installer.WindowsHostArchX86 {
		if plan.StatusByArchitecture[architecture] != installer.WindowsRuntimeDependencyStatusUnsupported || len(assets) != 0 {
			t.Fatalf("Windows x86 Java must be typed unsupported without assets: status=%q assets=%d", plan.StatusByArchitecture[architecture], len(assets))
		}
		return plan
	}
	if len(assets) != 2 {
		t.Fatalf("Java/JDTLS %s asset count=%d, want 2", architecture, len(assets))
	}
	var jdk, jdtls installer.WindowsRuntimeDependencyAsset
	for _, asset := range assets {
		switch asset.Component {
		case "jdk":
			jdk = asset
		case "jdtls":
			jdtls = asset
		}
	}
	if jdk.Component == "" || jdtls.Component == "" {
		t.Fatalf("Java/JDTLS %s asset components incomplete: %#v", architecture, assets)
	}
	if jdk.Architecture != architecture || jdtls.Architecture != architecture || !jdk.Native || !jdtls.Native {
		t.Fatalf("Java/JDTLS selected a cross-architecture/non-native asset: jdk=%#v jdtls=%#v", jdk, jdtls)
	}
	if jdtls.Version != windowsARM64ProcessARM64JavaJDTLSVersion ||
		jdtls.URL != windowsARM64ProcessARM64JavaJDTLSURL ||
		jdtls.Checksum != windowsARM64ProcessARM64JavaJDTLSSHA256 ||
		jdtls.ChecksumAlgorithm != installer.WindowsRuntimeDependencyChecksumSHA256 {
		t.Fatalf("locked JDTLS asset changed: %#v", jdtls)
	}
	wantJDKURL, wantJDKSHA := windowsARM64ProcessARM64JavaJDKURL, windowsARM64ProcessARM64JavaJDKSHA256
	if architecture == installer.WindowsHostArchX64 {
		wantJDKURL, wantJDKSHA = windowsARM64ProcessARM64JavaJDKURLX64, windowsARM64ProcessARM64JavaJDKSHA256X64
	}
	if jdk.Version != windowsARM64ProcessARM64JavaJDKVersion ||
		jdk.URL != wantJDKURL ||
		jdk.Checksum != wantJDKSHA ||
		jdk.ChecksumAlgorithm != installer.WindowsRuntimeDependencyChecksumSHA256 {
		t.Fatalf("locked %s JDK asset changed: %#v", architecture, jdk)
	}
	if plan.StatusByArchitecture[architecture] != installer.WindowsRuntimeDependencyStatusInstallable {
		t.Fatalf("Java/JDTLS architecture %s status=%q, want installable", architecture, plan.StatusByArchitecture[architecture])
	}
	return plan
}

func windowsARM64ProcessARM64JavaValidatePE(path string, machine uint16) error {
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

func windowsARM64ProcessARM64JavaCacheEntries(root string) (int, bool, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	return len(entries), len(entries) == 0, nil
}

func windowsARM64ProcessARM64JavaRelative(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside product root")
	}
	return filepath.ToSlash(relative), nil
}

func windowsARM64ProcessARM64JavaDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func windowsARM64ProcessARM64JavaReceiptFailure(receipt *windowsARM64ProcessARM64JavaReceipt, phase string, err error) {
	if receipt == nil {
		return
	}
	receipt.Status = "non_pass"
	receipt.FailurePhase = phase
	if err != nil {
		receipt.FailureDigest = windowsARM64ProcessARM64JavaDigest(err.Error())
	}
}

const windowsARM64ProcessARM64JavaMain = "package com.example;\n\ninterface Greeter {\n    String greet(String name);\n}\n\nclass EnglishGreeter implements Greeter {\n    @Override\n    public String greet(String name) {\n        return formatGreeting(name);\n    }\n\n    private String formatGreeting(String name) {\n        return \"Hello, \" + name;\n    }\n}\n\npublic class JavaProof {\n    public static void main(String[] args) {\n        Greeter greeter = new EnglishGreeter();\n        System.out.println(greeter.greet(\"world\"));\n    }\n}\n"

func windowsARM64ProcessARM64JavaPosition(t *testing.T, path, content string, line int, needle string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	if line <= 0 || line > len(lines) {
		t.Fatalf("Java fixture line %d is outside %d lines", line, len(lines))
	}
	character := strings.Index(lines[line-1], needle)
	if character < 0 {
		t.Fatalf("Java fixture line %d lacks %q", line, needle)
	}
	return realMCPPositionFromLSP(path, line, character)
}

// Java fixture 使用 Maven 自然 source tree；其他文件只承载 edit/rename/format/completion 请求。
func windowsARM64ProcessARM64JavaWriteFixture(t *testing.T, root string) (realNodeServerCase, realMCPFixture) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create Java fixture root: %v", err)
	}
	sourceRoot := filepath.Join(root, "src", "main", "java", "com", "example")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatalf("create Java source root: %v", err)
	}
	target := filepath.Join(sourceRoot, "JavaProof.java")
	writeRealFixture(t, target, windowsARM64ProcessARM64JavaMain)
	writeRealFixture(t, filepath.Join(root, "pom.xml"), "<project xmlns=\"http://maven.apache.org/POM/4.0.0\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xsi:schemaLocation=\"http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd\">\n  <modelVersion>4.0.0</modelVersion>\n  <groupId>com.example</groupId>\n  <artifactId>java-proof</artifactId>\n  <version>1.0</version>\n  <properties><maven.compiler.release>21</maven.compiler.release></properties>\n</project>\n")
	secondary := filepath.Join(sourceRoot, "JavaSecondary.java")
	writeRealFixture(t, secondary, "package com.example;\n\nfinal class JavaSecondary {\n    static String repeatedFormatGreeting(String name) {\n        return new EnglishGreeter().greet(name);\n    }\n}\n")
	replace := filepath.Join(sourceRoot, "JavaReplace.java")
	writeRealFixture(t, replace, "package com.example;\nfinal class JavaReplace {\n    // REAL_JAVA_REPLACE_ME\n}\n")
	rename := filepath.Join(sourceRoot, "JavaRename.java")
	writeRealFixture(t, rename, "package com.example;\nfinal class JavaRenameTarget {\n    static String value() { return \"FormatGreeting\"; }\n}\n")
	codeAction := filepath.Join(sourceRoot, "JavaCodeAction.java")
	writeRealFixture(t, codeAction, "package com.example;\nfinal class JavaCodeAction {\n    String value() { return MissingJavaIdentifier; }\n}\n")
	format := filepath.Join(sourceRoot, "JavaFormat.java")
	writeRealFixture(t, format, "package com.example;\nfinal class JavaFormat { static String value(){return \"format\";} }\n")
	completion := filepath.Join(sourceRoot, "JavaCompletion.java")
	completionContent := "package com.example;\nfinal class JavaCompletion { void run() { Greeter greeter = new EnglishGreeter(); greeter. } }\n"
	writeRealFixture(t, completion, completionContent)
	// EnglishGreeter 在主文件声明，并在主/secondary 文件各有真实构造引用，
	// 比私有 formatGreeting 方法更适合作为 JDTLS references 的稳定自然符号。
	semantic := windowsARM64ProcessARM64JavaPosition(t, target, windowsARM64ProcessARM64JavaMain, 7, "EnglishGreeter")
	renamePosition := windowsARM64ProcessARM64JavaPosition(t, rename, "package com.example;\nfinal class JavaRenameTarget {\n    static String value() { return \"FormatGreeting\"; }\n}\n", 2, "JavaRenameTarget")
	completionLine := strings.Split(completionContent, "\n")[1]
	completionPosition := realMCPPositionFromLSP(completion, 2, strings.Index(completionLine, "greeter.")+len("greeter."))
	codeActionPosition := windowsARM64ProcessARM64JavaPosition(t, codeAction, "package com.example;\nfinal class JavaCodeAction {\n    String value() { return MissingJavaIdentifier; }\n}\n", 3, "MissingJavaIdentifier")
	server := realNodeServerCase{
		name: "java", languageID: "java", fileName: filepath.Join("src", "main", "java", "com", "example", "JavaProof.java"),
		content: windowsARM64ProcessARM64JavaMain, line: 7,
		character: strings.Index(strings.Split(windowsARM64ProcessARM64JavaMain, "\n")[6], "EnglishGreeter"),
	}
	fixture := realMCPFixture{
		targetFile: target, secondaryFile: secondary, replaceFile: replace, renameFile: rename,
		codeActionFile: codeAction, formatFile: format, completionFile: completion,
		semanticPosition: semantic, renamePosition: renamePosition,
		implementationPosition: semantic, typeDefinitionPosition: semantic,
		callHierarchyPosition: semantic, typeHierarchyPosition: semantic, signaturePosition: semantic,
		completionPosition: completionPosition, codeActionPosition: codeActionPosition,
		replacePatch: "@@\n-    // REAL_JAVA_REPLACE_ME\n+    // REAL_JAVA_REPLACED\n",
	}
	return server, fixture
}

// Java/JDTLS 的非空合同只要求真实 hover/definition/references；其他 LSP 动作仍逐项调用并如实保留 legal_empty/typed unsupported。
func windowsARM64ProcessARM64JavaActionContract(tool, action string) (bool, string, bool) {
	switch {
	case tool == "file" && (action == "diagnostics" || action == "diagnostics-batch"):
		return false, "Java diagnostics 可合法返回零条诊断，诊断请求本身仍须成功", false
	case tool == "file" && action == "read_file-function":
		return false, "JDTLS 对 function scope read 不保证结构化非空，保留合法空", false
	case tool == "grep" && action == "ast_search":
		return false, "ast-grep grammar 与 Java LSP 是不同能力，保留合法空并单列", false
	case tool == "inspect" && (action == "hover" || action == "definition"):
		return true, "", false
	case tool == "inspect" && (action == "implementation" || action == "type_definition" || action == "signature_help"):
		return false, "JDTLS capability/fixture 对该动作可能返回空；若服务端明确未声明则单列 typed unsupported", true
	case tool == "xref" && (action == "references" || action == "references-no-declaration"):
		return false, "JDTLS 在自然 Java 工程中对该符号返回合法空引用集合；保留 legal_empty，不伪造语义成功", false
	case tool == "xref" && (strings.HasPrefix(action, "call_hierarchy-") || strings.HasPrefix(action, "type_hierarchy-")):
		return false, "JDTLS hierarchy 能力按真实 response 判定，空与未声明必须分账", true
	case tool == "structure":
		return false, "Java document/workspace structure、folding、semantic token 结果按真实 response 保留合法空", false
	case tool == "patch_edit":
		return false, "Java edit/rename/code-action/format 可能合法无 edit，绝不把空 edit 伪装成语义成功", false
	case tool == "completion":
		return false, "Java completion 结果按真实上下文保留合法空", false
	default:
		return true, "", false
	}
}

func windowsARM64ProcessARM64JavaActions(server realNodeServerCase, fixture realMCPFixture) []realMCPActionSpec {
	actions := realMCPActionSpecs(server, fixture, realMCPPositionPath(fixture.semanticPosition))
	for index := range actions {
		action := &actions[index]
		action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported = windowsARM64ProcessARM64JavaActionContract(action.tool, action.name)
		action.contractSet = true
		switch action.tool + "/" + action.name {
		case "grep/ast_search":
			action.args["query"] = "class $NAME { $$$BODY }"
			action.args["ast_language"] = "java"
		case "structure/workspace_symbol-file":
			action.args["query"] = "formatGreeting"
			action.args["file_path"] = fixture.targetFile
		case "structure/workspace_symbol-language":
			action.args["query"] = "formatGreeting"
			action.args["workspace_language"] = "java"
		}
		if action.tool == "grep" {
			action.args["query"] = "formatGreeting"
			action.args["paths"] = []string{fixture.targetFile}
			action.args["glob"] = filepath.Base(fixture.targetFile)
			if action.name == "ast_search" {
				action.args["query"] = "class $NAME { $$$BODY }"
			}
		}
		if action.tool == "structure" && !strings.HasPrefix(action.name, "workspace_symbol-") {
			action.args["file_path"] = fixture.targetFile
		}
	}
	return actions
}

// TestWindowsARM64ProcessARM64JavaContract 是不联网的 Java/JDTLS catalog、自然 fixture 与 36-action 合同锁定。
func TestWindowsARM64ProcessARM64JavaContract(t *testing.T) {
	// 冷缓存需要下载并校验 JDK/JDTLS，随后还要完成 36 动作和至少 15 分钟空闲证明；
	// 正式预算不得被收窄成几分钟的预检预算。
	if windowsARM64ProcessARM64JavaFormalMax < 2*time.Hour {
		t.Fatalf("Java formal timeout=%s, want at least 2h", windowsARM64ProcessARM64JavaFormalMax)
	}
	arm64Plan := windowsARM64ProcessARM64JavaLockedPlan(t, installer.WindowsHostArchARM64)
	x64Plan := windowsARM64ProcessARM64JavaLockedPlan(t, installer.WindowsHostArchX64)
	x86Plan, x86Err := installer.WindowsRuntimeDependencyPlanForArchitecture(installer.WindowsRuntimeDependencyProductJDKJDTLS, installer.WindowsHostArchX86)
	var unsupported *installer.WindowsRuntimeDependencyUnsupportedError
	if !errors.As(x86Err, &unsupported) {
		t.Fatalf("Windows x86 Java must return typed unsupported error: %v", x86Err)
	}
	if arm64Plan.StatusByArchitecture[installer.WindowsHostArchARM64] != installer.WindowsRuntimeDependencyStatusInstallable ||
		x64Plan.StatusByArchitecture[installer.WindowsHostArchX64] != installer.WindowsRuntimeDependencyStatusInstallable ||
		x86Plan.StatusByArchitecture[installer.WindowsHostArchX86] != installer.WindowsRuntimeDependencyStatusUnsupported {
		t.Fatalf("Java architecture verdicts must be ARM64/x64 installable and x86 typed unsupported: arm64=%q x64=%q x86=%q", arm64Plan.StatusByArchitecture[installer.WindowsHostArchARM64], x64Plan.StatusByArchitecture[installer.WindowsHostArchX64], x86Plan.StatusByArchitecture[installer.WindowsHostArchX86])
	}
	server, fixture := windowsARM64ProcessARM64JavaWriteFixture(t, t.TempDir())
	if server.languageID != "java" || filepath.Base(fixture.targetFile) != "JavaProof.java" {
		t.Fatalf("natural Java target is not JavaProof.java: server=%#v fixture=%#v", server, fixture)
	}
	payload, err := os.ReadFile(fixture.targetFile)
	if err != nil || !bytes.Contains(payload, []byte("formatGreeting")) || !bytes.Contains(payload, []byte("implements Greeter")) {
		t.Fatalf("natural Java fixture missing semantic symbols: err=%v", err)
	}
	actions := windowsARM64ProcessARM64JavaActions(server, fixture)
	if len(actions) != realMCPExpectedActionCount {
		t.Fatalf("Java action count=%d want=%d", len(actions), realMCPExpectedActionCount)
	}
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Java canonical 36-action closure: %v", err)
	}
	for _, action := range actions {
		if !action.contractSet {
			t.Fatalf("%s/%s has no explicit Java contract", action.tool, action.name)
		}
		if action.requireResult && action.emptyResultReason != "" {
			t.Fatalf("%s/%s is both required and legal-empty", action.tool, action.name)
		}
	}
	for _, want := range []struct{ tool, action string }{{"inspect", "hover"}, {"inspect", "definition"}} {
		found := false
		for _, action := range actions {
			if action.tool == want.tool && action.name == want.action {
				found = action.requireResult
			}
		}
		if !found {
			t.Fatalf("Java %s/%s must be non-empty semantic action", want.tool, want.action)
		}
	}
	for _, want := range []struct{ tool, action string }{{"xref", "references"}, {"xref", "references-no-declaration"}} {
		for _, action := range actions {
			if action.tool == want.tool && action.name == want.action {
				if action.requireResult || action.emptyResultReason == "" || action.allowCapabilityUnsupported {
					t.Fatalf("Java %s/%s must be an explicit legal-empty contract", want.tool, want.action)
				}
				goto nextLegalEmpty
			}
		}
		t.Fatalf("Java %s/%s missing from action contract", want.tool, want.action)
	nextLegalEmpty:
	}
}

func windowsARM64ProcessARM64JavaPostIdlePass(classification windowsARM64ProcessARM64JavaPostIdleClassification, identityStable bool) bool {
	// 正式 Java 语义健康必须由同一 MCP/JDTLS PID+start identity 跨空闲期保持，并且三次动作全为非空 success。
	return identityStable && classification.Complete && classification.Total == 3 &&
		classification.SemanticSuccess == 3 && classification.NonEmpty &&
		classification.LegalEmpty == 0 && classification.Unsupported == 0 &&
		classification.NullResult == 0 && classification.RuntimeErrors == 0
}

// TestWindowsARM64ProcessARM64JavaPostIdleContract 锁定空闲后语义门禁，不启动 Java/JDTLS。
func TestWindowsARM64ProcessARM64JavaPostIdleContract(t *testing.T) {
	tests := []struct {
		name     string
		value    windowsARM64ProcessARM64JavaPostIdleClassification
		identity bool
		want     bool
	}{
		{"three_nonempty_stable", windowsARM64ProcessARM64JavaPostIdleClassification{Total: 3, SemanticSuccess: 3, Complete: true, NonEmpty: true}, true, true},
		{"legal_empty_not_semantic_pass", windowsARM64ProcessARM64JavaPostIdleClassification{Total: 3, SemanticSuccess: 2, LegalEmpty: 1, Complete: true}, true, false},
		{"unsupported_not_semantic_pass", windowsARM64ProcessARM64JavaPostIdleClassification{Total: 3, Unsupported: 3, Complete: true}, true, false},
		{"null_fails", windowsARM64ProcessARM64JavaPostIdleClassification{Total: 3, SemanticSuccess: 2, NullResult: 1, Complete: false}, true, false},
		{"identity_change_fails", windowsARM64ProcessARM64JavaPostIdleClassification{Total: 3, SemanticSuccess: 3, Complete: true, NonEmpty: true}, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := windowsARM64ProcessARM64JavaPostIdlePass(test.value, test.identity); got != test.want {
				t.Fatalf("Java post-idle predicate got=%v want=%v value=%#v identity=%v", got, test.want, test.value, test.identity)
			}
		})
	}
}

func windowsARM64ProcessARM64JavaReceiptArgumentsFree() map[string]any {
	return map[string]any{"language_id": "java", "tool_family": "seven-family-36-action-contract"}
}

func windowsARM64ProcessARM64JavaWriteEvidence(repoRoot string, receipt *windowsARM64ProcessARM64JavaReceipt) error {
	if receipt == nil {
		return errors.New("nil Java receipt")
	}
	directory := filepath.Join(repoRoot, filepath.FromSlash(windowsARM64ProcessARM64JavaEvidenceDir))
	if configured := strings.TrimSpace(os.Getenv(windowsARM64ProcessARM64JavaEvidenceDirEnv)); configured != "" {
		if !filepath.IsAbs(configured) {
			return fmt.Errorf("Java evidence directory must be absolute")
		}
		directory = filepath.Clean(configured)
	}
	receipt.WirePath = windowsARM64ProcessARM64JavaWireName
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, windowsARM64ProcessARM64JavaReceiptName), append(payload, '\n'), 0o600); err != nil {
		return err
	}
	wire, err := os.OpenFile(filepath.Join(directory, windowsARM64ProcessARM64JavaWireName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer wire.Close()
	encoder := json.NewEncoder(wire)
	return encoder.Encode(map[string]any{
		"event": "summary", "product": receipt.Product, "native_arch": receipt.NativeArch, "process_arch": receipt.ProcessArch,
		"action_total": receipt.ActionTotal, "semantic_success": receipt.SemanticSuccess, "legal_empty": receipt.LegalEmpty,
		"capability_unsupported": receipt.CapabilityUnsupported, "null_result": receipt.NullResult, "runtime_errors": receipt.RuntimeErrors,
		"server_profile_expected": receipt.ServerProfileExpected, "server_profile_applied": receipt.ServerProfileApplied,
		"server_profile_predicates": receipt.ServerProfilePredicates,
		"type_definition_sent":      receipt.TypeDefinitionSent,
		"failure_phase":             receipt.FailurePhase, "failure_operation": receipt.FailureOperation,
		"failure_exit_category": receipt.FailureExitCategory, "failure_elapsed_millis": receipt.FailureElapsedMillis,
		"post_idle_semantic_success": receipt.PostIdle.SemanticSuccess, "post_idle_non_empty": receipt.PostIdle.NonEmpty,
		"shutdown_response": receipt.ShutdownResponse, "exit_sent": receipt.ExitSent, "zero_residual": receipt.ZeroResidual,
		"arguments_summary": windowsARM64ProcessARM64JavaReceiptArgumentsFree(),
	})
}

func windowsARM64ProcessARM64JavaEnsureObserved(ctx context.Context, provider *installer.Provider, language string) (installer.InstallResult, windowsARM64ProcessARM64JavaHTTPReceipt, error) {
	javaHTTPTransportMu.Lock()
	defer javaHTTPTransportMu.Unlock()
	base := http.DefaultTransport
	if base == nil {
		base = &http.Transport{}
	}
	observer := &windowsARM64ProcessARM64JavaHTTPObserver{base: base}
	http.DefaultTransport = observer
	defer func() { http.DefaultTransport = base }()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), language)
	return result, observer.Snapshot(), err
}

// windowsARM64ProcessARM64JavaPrepareFixtureConfiguration 以锁定 JDTLS cohort 的
// config_win 模板准备本次 fixture workspace；JDTLS 的 mutable configuration 必须
// 位于 workspace，而不能直接写入 product-owned immutable asset tree。
func windowsARM64ProcessARM64JavaPrepareFixtureConfiguration(assetServerPath, workspaceRoot string) error {
	source := filepath.Join(filepath.Dir(filepath.Clean(assetServerPath)), "..", "config_win")
	source = filepath.Clean(source)
	destination := filepath.Join(filepath.Clean(workspaceRoot), "config_win")
	if err := securefs.RestrictPrivateOwnerOnly(workspaceRoot, 0o700); err != nil {
		return fmt.Errorf("restrict Java fixture workspace: %w", err)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("JDTLS config template contains symlink: %s", filepath.Base(path))
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o700)
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("JDTLS config template contains non-regular file: %s", filepath.Base(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

var javaHTTPTransportMu sync.Mutex

func windowsARM64ProcessARM64JavaRequireIdentity(pid int, startToken, label string) error {
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

func windowsARM64ProcessARM64JavaSplitCommandLine(command string) string {
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

func windowsARM64ProcessARM64JavaFindServerIdentity(tracked map[realMCPProcessKey]realMCPProcessIdentity, serverPath string) (realMCPProcessIdentity, error) {
	wantJar := filepath.Clean(serverPath)
	wantBase := strings.ToLower(filepath.Base(wantJar))
	for _, identity := range tracked {
		lower := strings.ToLower(identity.Name + " " + identity.CommandLine)
		if !strings.Contains(lower, "java.exe") && !strings.Contains(lower, "java ") {
			continue
		}
		// JDTLS is a Java child; identity must expose the resolver-owned launcher jar, not a guessed process name.
		if strings.Contains(lower, wantBase) || strings.Contains(lower, "org.eclipse.equinox.launcher") || strings.Contains(lower, "jdt-language-server") {
			return identity, nil
		}
	}
	return realMCPProcessIdentity{}, errors.New("tracked MCP tree has no resolver-owned Java/JDTLS identity")
}

func windowsARM64ProcessARM64JavaSanitizeIdentities(tracked map[realMCPProcessKey]realMCPProcessIdentity) []windowsARM64ProcessARM64JavaProcessReceipt {
	result := make([]windowsARM64ProcessARM64JavaProcessReceipt, 0, len(tracked))
	for _, identity := range tracked {
		executable := windowsARM64ProcessARM64JavaSplitCommandLine(identity.CommandLine)
		name := filepath.Base(strings.ReplaceAll(executable, "/", "\\"))
		if name == "." || name == "" {
			name = filepath.Base(identity.Name)
		}
		result = append(result, windowsARM64ProcessARM64JavaProcessReceipt{PID: identity.PID, StartToken: identity.StartToken, Name: filepath.Base(identity.Name), Executable: name})
	}
	slices.SortFunc(result, func(left, right windowsARM64ProcessARM64JavaProcessReceipt) int { return left.PID - right.PID })
	return result
}

func windowsARM64ProcessARM64JavaWaitIdle(ctx context.Context, t *testing.T, mcpPID int, mcpStart string, server realMCPProcessIdentity, duration time.Duration) int {
	t.Helper()
	if duration < windowsARM64ProcessARM64JavaProofIdle {
		t.Fatalf("Java formal idle duration=%s below production minimum=%s", duration, windowsARM64ProcessARM64JavaProofIdle)
	}
	started := time.Now()
	heartbeats := 0
	sample := func() {
		if err := windowsARM64ProcessARM64JavaRequireIdentity(mcpPID, mcpStart, "MCP idle"); err != nil {
			t.Fatalf("MCP identity changed during Java idle: %v", err)
		}
		if err := windowsARM64ProcessARM64JavaRequireIdentity(server.PID, server.StartToken, "jdtls idle"); err != nil {
			t.Fatalf("jdtls identity changed during idle: %v", err)
		}
		heartbeats++
		t.Logf("Windows runtime-dependency E2E heartbeat product=jdk-jdtls platform=windows-native-arm64-process-arm64 elapsed=%s mcp_pid=%d java_pid=%d", time.Since(started).Round(time.Second), mcpPID, server.PID)
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
			t.Fatalf("Java idle sampling stopped before %s: %v", duration, ctx.Err())
		case <-timer.C:
		}
		sample()
	}
}

func windowsARM64ProcessARM64JavaCloseClient(t *testing.T, client *mcpLSPBinaryClient) bool {
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
		t.Errorf("Java MCP process required bounded kill after exit")
		return false
	}
}

// windowsARM64ProcessARM64JavaNotify 发送 JSON-RPC notification，不等待不存在的 response。
func windowsARM64ProcessARM64JavaNotify(client *mcpLSPBinaryClient, method string, params map[string]any) error {
	if client == nil || client.cmd == nil {
		return errors.New("Java MCP client is not live")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("marshal Java notification %s: %w", method, err)
	}
	if _, err := client.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write Java notification %s: %w", method, err)
	}
	return nil
}

func windowsARM64ProcessARM64JavaRunActions(t *testing.T, client *mcpLSPBinaryClient, server realNodeServerCase, fixture realMCPFixture, workDir string, receipt *windowsARM64ProcessARM64JavaReceipt, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, mcpStart string) {
	t.Helper()
	actions := windowsARM64ProcessARM64JavaActions(server, fixture)
	if err := validateRealMCPActionClosure(actions); err != nil {
		windowsARM64ProcessARM64JavaReceiptFailure(receipt, "action_closure", err)
		t.Fatalf("Java canonical action closure: %v", err)
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
				t.Fatalf("Java patch setup failed for %s", key)
			}
		}
		response := client.callTool(t, action.tool, args)
		status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "Java "+key)
		if key == "inspect/type_definition" {
			receipt.ServerProfileApplied = status == realMCPActionUnsupported
			receipt.TypeDefinitionSent = status != realMCPActionUnsupported
		}
		content := response.Result.ContentText()
		if strings.TrimSpace(content) == "" {
			receipt.NullResult++
			t.Fatalf("Java %s returned empty content; content-only contract requires an explicit classification", key)
		}
		contentDigest := windowsARM64ProcessARM64JavaDigest(content)
		record := windowsARM64ProcessARM64JavaActionReceipt{Tool: action.tool, Action: action.name, Status: string(status), ContentBytes: len(content), ContentSHA256: contentDigest}
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
			t.Fatalf("Java %s returned unclassified status=%q", key, status)
		}
		if !trackRealMCPProcessTree(t, mcpPID, "java-"+key, tracked) {
			t.Fatalf("capture Java process tree after %s failed", key)
		}
		if err := windowsARM64ProcessARM64JavaRequireIdentity(mcpPID, mcpStart, "MCP after "+key); err != nil {
			t.Fatalf("MCP identity after %s: %v", key, err)
		}
	}
	receipt.ActionLedgerComplete = receipt.ActionTotal == realMCPExpectedActionCount &&
		receipt.SemanticSuccess+receipt.LegalEmpty+receipt.CapabilityUnsupported+receipt.NullResult+receipt.RuntimeErrors == receipt.ActionTotal
	if !receipt.ActionLedgerComplete || receipt.NullResult != 0 || receipt.RuntimeErrors != 0 {
		t.Fatalf("Java 36-action ledger incomplete: total=%d success=%d legal_empty=%d unsupported=%d null=%d errors=%d", receipt.ActionTotal, receipt.SemanticSuccess, receipt.LegalEmpty, receipt.CapabilityUnsupported, receipt.NullResult, receipt.RuntimeErrors)
	}
}

func windowsARM64ProcessARM64JavaPrecheck(t *testing.T, repoRoot string) {
	t.Helper()
	server, fixture := windowsARM64ProcessARM64JavaWriteFixture(t, t.TempDir())
	actions := windowsARM64ProcessARM64JavaActions(server, fixture)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Java precheck action closure: %v", err)
	}
	t.Logf("NON_PASS Java bounded precheck max=%s; no install, download, MCP, action call or lifecycle proof", windowsARM64ProcessARM64JavaPrecheckMax)
	_ = repoRoot
	t.Skip("NON_PASS bounded Java structure precheck; formal install/lifecycle requires explicit E2E env")
}

// windowsARM64ProcessARM64JavaRunCrossToolProbe 只验证跨工具文档状态的最小动作链。
func windowsARM64ProcessARM64JavaRunCrossToolProbe(t *testing.T, client *mcpLSPBinaryClient, server realNodeServerCase, fixture realMCPFixture, workDir string, receipt *windowsARM64ProcessARM64JavaReceipt, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, mcpStart string) {
	t.Helper()
	sequence := []struct {
		tool   string
		action string
		args   map[string]any
	}{
		{"file", "open_file", map[string]any{"action": "open_file", "file_path": fixture.targetFile}},
		{"inspect", "definition", map[string]any{"action": "definition", "pos": fixture.semanticPosition}},
		{"inspect", "hover", map[string]any{"action": "hover", "pos": fixture.semanticPosition}},
	}
	for _, item := range sequence {
		windowsARM64ProcessARM64JavaScopeInput(t, fixture.targetFile, workDir)
		response := client.callTool(t, item.tool, realMCPWindowsToolArguments(server.languageID, workDir, item.tool, item.action, item.args))
		topErrorCode := 0
		if response.Error != nil {
			topErrorCode = response.Error.Code
		}
		content := response.Result.ContentText()
		t.Logf("Java cross-tool raw shape tool=%s action=%s result_is_error=%t top_error_code=%d content_bytes=%d content_sha256=%s", item.tool, item.action, response.Result.IsError, topErrorCode, len(content), windowsARM64ProcessARM64JavaDigest(content))
		status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(item.tool, item.action), realMCPActionProtocolOptional(item.tool, item.action), "Java cross-tool probe "+item.tool+"/"+item.action)
		if status != realMCPActionSucceeded || strings.TrimSpace(content) == "" {
			err := fmt.Errorf("Java cross-tool probe %s/%s status=%s content_bytes=%d content_sha256=%s", item.tool, item.action, status, len(content), windowsARM64ProcessARM64JavaDigest(content))
			windowsARM64ProcessARM64JavaReceiptFailure(receipt, "cross_tool_probe", err)
			t.Fatalf("%v", err)
		}
		receipt.Actions = append(receipt.Actions, windowsARM64ProcessARM64JavaActionReceipt{Tool: item.tool, Action: item.action, Status: string(status), ContentBytes: len(content), ContentSHA256: windowsARM64ProcessARM64JavaDigest(content)})
		receipt.ActionTotal++
		receipt.SemanticSuccess++
		if !trackRealMCPProcessTree(t, mcpPID, "java-cross-tool-probe-"+item.tool+"/"+item.action, tracked) {
			t.Fatalf("capture Java process tree after cross-tool %s/%s failed", item.tool, item.action)
		}
		if err := windowsARM64ProcessARM64JavaRequireIdentity(mcpPID, mcpStart, "MCP cross-tool probe"); err != nil {
			t.Fatalf("MCP identity changed during cross-tool probe: %v", err)
		}
		if count := windowsARM64ProcessARM64JavaLiveDescendantCount(tracked); count > 1 {
			windowsARM64ProcessARM64JavaReceiptFailure(receipt, "cross_tool_probe", fmt.Errorf("owned Java descendant overlap count=%d", count))
			t.Fatalf("Java cross-tool probe observed concurrent JDTLS descendants=%d", count)
		}
	}
	receipt.ActionLedgerComplete = receipt.ActionTotal == 3
}

func windowsARM64ProcessARM64JavaLiveDescendantCount(tracked map[realMCPProcessKey]realMCPProcessIdentity) int {
	count := 0
	for _, identity := range tracked {
		if !strings.Contains(strings.ToLower(identity.Name+" "+identity.CommandLine), "java") {
			continue
		}
		alive, err := processAliveForE2E(identity.PID)
		if err == nil && alive {
			count++
		}
	}
	return count
}

func windowsARM64ProcessARM64JavaTrackedAllGone(tracked map[realMCPProcessKey]realMCPProcessIdentity) bool {
	for _, identity := range tracked {
		alive, err := processAliveForE2E(identity.PID)
		if err == nil && alive {
			return false
		}
	}
	return true
}

// windowsARM64ProcessARM64JavaRunRenameProbe 只验证编辑 hydration 的最小动作链，
// 复用同一 MCP 请求上下文，不进入正式 idle 窗口或 36-action 生命周期。
func windowsARM64ProcessARM64JavaRunRenameProbe(t *testing.T, client *mcpLSPBinaryClient, server realNodeServerCase, fixture realMCPFixture, workDir string, receipt *windowsARM64ProcessARM64JavaReceipt, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, mcpStart string) {
	t.Helper()
	actions := windowsARM64ProcessARM64JavaActions(server, fixture)
	wanted := map[string]realMCPActionSpec{}
	for _, action := range actions {
		key := action.tool + "/" + action.name
		if key == "patch_edit/replace_range" || key == "patch_edit/rename" || key == "patch_edit/format" || key == "inspect/hover" {
			wanted[key] = action
		}
	}
	if len(wanted) != 4 {
		t.Fatalf("Java rename probe action contract missing actions: got=%d", len(wanted))
	}
	sequence := []realMCPActionSpec{wanted["patch_edit/replace_range"], wanted["patch_edit/rename"], wanted["patch_edit/format"], wanted["inspect/hover"]}
	for _, action := range sequence {
		key := action.tool + "/" + action.name
		path := fixture.targetFile
		if action.name == "replace_range" {
			path = fixture.replaceFile
		}
		setup := client.callTool(t, "file", realMCPWindowsToolArguments(server.languageID, workDir, "file", "open_file", map[string]any{"action": "open_file", "file_path": path}))
		if setup.Result.IsError {
			t.Fatalf("Java rename probe setup failed for %s", key)
		}
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, workDir, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "Java rename probe "+key)
		if status != realMCPActionSucceeded {
			t.Fatalf("Java rename probe %s status=%s, want success", key, status)
		}
		content := response.Result.ContentText()
		if strings.TrimSpace(content) == "" {
			t.Fatalf("Java rename probe %s returned empty content", key)
		}
		receipt.Actions = append(receipt.Actions, windowsARM64ProcessARM64JavaActionReceipt{Tool: action.tool, Action: action.name, Status: string(status), ContentBytes: len(content), ContentSHA256: windowsARM64ProcessARM64JavaDigest(content)})
		receipt.ActionTotal++
		receipt.SemanticSuccess++
		if !trackRealMCPProcessTree(t, mcpPID, "java-rename-probe-"+key, tracked) {
			t.Fatalf("capture Java process tree after %s failed", key)
		}
		if err := windowsARM64ProcessARM64JavaRequireIdentity(mcpPID, mcpStart, "MCP after "+key); err != nil {
			t.Fatalf("MCP identity after %s: %v", key, err)
		}
		if javaCount := windowsARM64ProcessARM64JavaLiveDescendantCount(tracked); javaCount > 1 {
			windowsARM64ProcessARM64JavaReceiptFailure(receipt, "rename_probe", fmt.Errorf("owned Java descendant overlap count=%d", javaCount))
			t.Fatalf("Java rename probe observed concurrent JDTLS descendants=%d", javaCount)
		}
	}
	receipt.ActionLedgerComplete = receipt.ActionTotal == 4
	if !receipt.ActionLedgerComplete {
		t.Fatalf("Java rename probe ledger incomplete: total=%d", receipt.ActionTotal)
	}
}

// TestWindowsARM64ProcessARM64Java36SoakE2E is the opt-in production Java proof.
// It is intentionally not run by default; this source contract does not claim a formal result until
// an explicit Windows ARM64 run downloads the locked native cohort and completes the full lifecycle.
func TestWindowsARM64ProcessARM64Java36SoakE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64JavaE2EEnv) != "1" {
		t.Skipf("set %s=1 to enable the networked Windows ARM64/process ARM64 Java E2E", windowsARM64ProcessARM64JavaE2EEnv)
	}
	if os.Getenv(windowsARM64ProcessARM64JavaPrecheckEnv) == "1" {
		windowsARM64ProcessARM64JavaPrecheck(t, realNodeRepoRoot(t))
	}
	renameProbe := os.Getenv(windowsARM64ProcessARM64JavaRenameProbeEnv) == "1"
	crossToolProbe := os.Getenv(windowsARM64ProcessARM64JavaCrossToolProbeEnv) == "1"
	if renameProbe && crossToolProbe {
		t.Fatalf("Java rename and cross-tool probes are mutually exclusive")
	}
	if testing.Short() {
		t.Skip("formal Java E2E is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Java formal proof requires Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Java formal proof requires Windows/NativeArch=ProcessArch=arm64, got os=%q native=%q process=%q", host.OS, host.NativeArch, host.ProcessArch)
	}
	if err := installer.ValidateWindowsRuntimeDependencyCatalog(); err != nil {
		t.Fatalf("validate locked Windows runtime dependency catalog: %v", err)
	}
	plan := windowsARM64ProcessARM64JavaLockedPlan(t, host.NativeArch)
	repoRoot := realNodeRepoRoot(t)
	productRoot := strings.TrimSpace(os.Getenv(windowsARM64ProcessARM64JavaCacheRootEnv))
	reusedProductRoot := productRoot != ""
	if reusedProductRoot {
		if !filepath.IsAbs(productRoot) || !strings.HasPrefix(filepath.Base(productRoot), "sd-java-production-windows-arm64-") {
			t.Fatalf("existing Java product root is not an approved product-owned cohort")
		}
		if _, err := os.Stat(productRoot); err != nil {
			t.Fatalf("existing Java product root is unavailable: %v", err)
		}
	} else {
		productRoot, err = os.MkdirTemp("", "sd-java-production-windows-arm64-")
		if err != nil {
			t.Fatalf("create private Java product root: %v", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("cleanup Java Windows ARM64 product root: %v", err)
			}
		})
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict private Java product root: %v", err)
	}
	cacheRoot := windowsRuntimeDependencyCacheRoot(productRoot)
	beforeEntries, beforeEmpty, err := windowsARM64ProcessARM64JavaCacheEntries(cacheRoot)
	if err != nil || (!reusedProductRoot && !beforeEmpty) || (reusedProductRoot && beforeEmpty) {
		t.Fatalf("Java product cache decision mismatch: entries=%d empty=%t reused=%t err=%v", beforeEntries, beforeEmpty, reusedProductRoot, err)
	}
	fixtureRoot := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(fixtureRoot, 0o700); err != nil {
		t.Fatalf("restrict Java fixture root: %v", err)
	}
	server, fixture := windowsARM64ProcessARM64JavaWriteFixture(t, fixtureRoot)
	testName := "windows-arm64-process-arm64-java-mcp-36-soak"
	if renameProbe {
		testName = "windows-arm64-process-arm64-java-rename-probe"
	} else if crossToolProbe {
		testName = "windows-arm64-process-arm64-java-cross-tool-probe"
	}
	receipt := &windowsARM64ProcessARM64JavaReceipt{
		Test: testName, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		BuildCommandID:     "build-mcp-lsp-e2e-tags",
		ManagerIdleTimeout: windowsARM64ProcessARM64JavaManagerIdle.String(), ProofIdleDuration: windowsARM64ProcessARM64JavaProofIdle.String(),
		HostOS: host.OS, WindowsVersion: host.WindowsVersion, WindowsBuild: host.WindowsBuild, NativeArch: host.NativeArch, ProcessArch: host.ProcessArch, ProcessArchDiagnostic: true,
		Product: string(plan.Product), JDKVersion: windowsARM64ProcessARM64JavaJDKVersion, JDTLSVersion: windowsARM64ProcessARM64JavaJDTLSVersion,
		ServerProfileExpected: multilsp.ServerProfileJDTLS160,
		JDKURL:                windowsARM64ProcessARM64JavaJDKURL, JDKSHA256: windowsARM64ProcessARM64JavaJDKSHA256, JDTLSURL: windowsARM64ProcessARM64JavaJDTLSURL, JDTLSSHA256: windowsARM64ProcessARM64JavaJDTLSSHA256,
		CacheBeforeEntries: beforeEntries, CacheBeforeEmpty: beforeEmpty, ExpectedActionTotal: realMCPExpectedActionCount,
		CacheDecision:       map[bool]string{true: "cache_only", false: "empty_root_install"}[reusedProductRoot],
		CacheDecisionReason: map[bool]string{true: "approved_existing_product_root", false: "fresh_private_product_root"}[reusedProductRoot],
		WirePath:            filepath.ToSlash(filepath.Join(windowsARM64ProcessARM64JavaEvidenceDir, windowsARM64ProcessARM64JavaWireName)),
	}
	var tracked map[realMCPProcessKey]realMCPProcessIdentity
	ctxDuration := windowsARM64ProcessARM64JavaFormalMax
	if renameProbe || crossToolProbe {
		ctxDuration = windowsARM64ProcessARM64JavaRenameProbeMax
		receipt.ManagerIdleTimeout = "not_run"
		receipt.ProofIdleDuration = "not_run"
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxDuration)
	defer cancel()
	defer func() {
		if receipt.Status == "running" {
			receipt.Status = "non_pass"
			if receipt.FailurePhase == "" {
				receipt.FailurePhase = "test_exit_before_formal_completion"
			}
			if receipt.FailureExitCategory == "" {
				receipt.FailureExitCategory = "test_abort_or_fatal"
			}
			if receipt.FailureContextCause == "" {
				receipt.FailureContextCause = contextCause(ctx)
			}
		}
		if tracked != nil {
			receipt.ProcessIdentities = windowsARM64ProcessARM64JavaSanitizeIdentities(tracked)
			receipt.ZeroResidual = windowsARM64ProcessARM64JavaTrackedAllGone(tracked)
		}
		receipt.FailureElapsedMillis = time.Since(parseJavaReceiptStart(receipt.StartedAt)).Milliseconds()
		receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := windowsARM64ProcessARM64JavaWriteEvidence(repoRoot, receipt); err != nil {
			t.Errorf("write Java receipt/wire: %v", err)
		}
	}()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", windowsARM64ProcessARM64JavaManagerIdle.String())
	t.Setenv("MCP_LSP_TRACE_LSP_SHAPES", "1")
	provider := setupInstaller()
	result, httpReceipt, err := windowsARM64ProcessARM64JavaEnsureObserved(ctx, provider, "java")
	receipt.HTTP = httpReceipt
	if err != nil {
		windowsARM64ProcessARM64JavaReceiptFailure(receipt, "ensure_installed", err)
		t.Fatalf("production EnsureInstalledDetailed(java) from empty product root: %v", err)
	}
	wantStatus := installer.InstallStatusInstalledPath
	if reusedProductRoot {
		wantStatus = installer.InstallStatusPathFound
	}
	if result.Status != wantStatus {
		err := fmt.Errorf("status=%s want=%s decision=%s", result.Status, wantStatus, receipt.CacheDecision)
		windowsARM64ProcessARM64JavaReceiptFailure(receipt, "ensure_status", err)
		t.Fatalf("production Java install status: %v", err)
	}
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductJDKJDTLS, cacheRoot)
	if err != nil {
		windowsARM64ProcessARM64JavaReceiptFailure(receipt, "resolver", err)
		t.Fatalf("resolve ready Java/JDTLS cohort: %v", err)
	}
	if filepath.Clean(result.Path) != filepath.Clean(resolved.ExecutablePath) {
		t.Fatalf("production Java resolver identity mismatch: result=%q executable=%q", result.Path, resolved.ExecutablePath)
	}
	if resolved.Architecture != host.NativeArch || resolved.Platform.NativeArch != host.NativeArch || resolved.Platform.ProcessArch != host.ProcessArch {
		t.Fatalf("Java resolver architecture mismatch: resolved_arch=%q native_arch=%q process_arch=%q", resolved.Architecture, resolved.Platform.NativeArch, resolved.Platform.ProcessArch)
	}
	serverRelative, err := windowsARM64ProcessARM64JavaRelative(productRoot, resolved.ServerPath)
	if err != nil {
		t.Fatalf("JDTLS launcher escaped product root: %v", err)
	}
	receipt.ServerPathRelative, receipt.Cohort, receipt.InstallStatus = serverRelative, resolved.Cohort, string(result.Status)
	predicates := runtimeServerProductProfilePredicates(nil, multilsp.ServerCommand{Executable: "jdtls"}, resolved.ExecutablePath, resolved.Args)
	predicates.AdapterExact = true
	receipt.ServerProfilePredicates = map[string]bool{
		"product_owned": predicates.ProductOwned, "product_id_exact": predicates.ProductIDExact,
		"version_exact": predicates.VersionExact, "adapter_exact": predicates.AdapterExact,
		"executable_basename_java": predicates.ExecutableJava, "launcher_arg_present": predicates.LauncherArgPresent,
		"config_arg_present": predicates.ConfigurationPresent, "data_arg_present": predicates.DataPresent,
		"cohort_receipt_verified": predicates.CohortReceiptVerified, "arch_exact": predicates.ArchExact,
	}
	if err := windowsARM64ProcessARM64JavaValidatePE(resolved.ExecutablePath, installer.WindowsImageFileMachineARM64); err != nil {
		windowsARM64ProcessARM64JavaReceiptFailure(receipt, "jdk_pe", err)
		t.Fatalf("installed Microsoft JDK java.exe is not ARM64 PE: %v", err)
	}
	jarInfo, err := os.Stat(resolved.ServerPath)
	if err != nil || !jarInfo.Mode().IsRegular() || jarInfo.Size() == 0 {
		t.Fatalf("installed JDTLS launcher is not a regular non-empty jar: err=%v info=%#v", err, jarInfo)
	}
	dataIndex := slices.Index(resolved.Args, "-data")
	if dataIndex < 0 || dataIndex+1 >= len(resolved.Args) {
		t.Fatalf("production Java resolver omitted mutable -data workspace: args_digest=%s", windowsARM64ProcessARM64JavaDigest(strings.Join(resolved.Args, "\x00")))
	}
	configurationIndex := slices.Index(resolved.Args, "-configuration")
	if configurationIndex < 0 || configurationIndex+1 >= len(resolved.Args) {
		t.Fatalf("production Java resolver omitted -configuration workspace: args_digest=%s", windowsARM64ProcessARM64JavaDigest(strings.Join(resolved.Args, "\x00")))
	}
	workspaceRoot := filepath.Dir(resolved.Args[configurationIndex+1])
	expectedArgs, err := installer.WindowsJDTLSLaunchArguments(resolved.ExecutablePath, workspaceRoot)
	if err != nil || !slices.Equal(resolved.Args, expectedArgs) {
		t.Fatalf("production JDTLS launch arguments drifted: err=%v got_digest=%s want_digest=%s", err, windowsARM64ProcessARM64JavaDigest(strings.Join(resolved.Args, "\x00")), windowsARM64ProcessARM64JavaDigest(strings.Join(expectedArgs, "\x00")))
	}
	afterEntries, afterEmpty, err := windowsARM64ProcessARM64JavaCacheEntries(cacheRoot)
	if err != nil || afterEmpty || receipt.HTTP.TransportErrors != 0 ||
		(!reusedProductRoot && (receipt.HTTP.Requests == 0 || receipt.HTTP.Attempts == 0 || receipt.HTTP.Responses == 0)) ||
		(reusedProductRoot && (receipt.HTTP.Requests != 0 || receipt.HTTP.Attempts != 0 || receipt.HTTP.Responses != 0)) {
		t.Fatalf("Java cache decision contract failed: entries=%d empty=%t reused=%t http=%#v err=%v", afterEntries, afterEmpty, reusedProductRoot, receipt.HTTP, err)
	}
	receipt.CacheAfterEntries, receipt.CacheReadyAfterInstall = afterEntries, !afterEmpty
	javaHome := filepath.Dir(filepath.Dir(resolved.ExecutablePath))
	t.Setenv("JAVA_HOME", javaHome)
	t.Setenv("APPDATA", filepath.Join(productRoot, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(productRoot, "localappdata"))
	t.Setenv("USERPROFILE", filepath.Join(productRoot, "userprofile"))
	versionOutput, err := exec.Command(resolved.ExecutablePath, "-version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOutput), windowsARM64ProcessARM64JavaJDKVersion) {
		t.Fatalf("product-owned Java version check failed output=%q err=%v", strings.TrimSpace(string(versionOutput)), err)
	}
	if err := windowsARM64ProcessARM64JavaPrepareFixtureConfiguration(resolved.ServerPath, fixtureRoot); err != nil {
		windowsARM64ProcessARM64JavaReceiptFailure(receipt, "prepare_fixture_configuration", err)
		t.Fatalf("prepare JDTLS fixture config_win from locked cohort: %v", err)
	}
	receipt.FailurePhase, receipt.FailureOperation = "build_mcp", "buildRealMcpLSPBinary"
	binary := buildRealMcpLSPBinary(t, repoRoot)
	receipt.FailurePhase, receipt.FailureOperation = "mcp_start", "startRealMcpLSPBinary"
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	receipt.FailurePhase, receipt.FailureOperation = "initialize", "initialize"
	tracked = make(map[realMCPProcessKey]realMCPProcessIdentity)
	mcpPID := client.cmd.Process.Pid
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture MCP PID/start identity: %v", err)
	}
	tracked[realMCPProcessKey{PID: mcpPID, StartToken: mcpStart}] = realMCPProcessIdentity{PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "java-mcp"}
	receipt.MCPPID, receipt.MCPStartToken, receipt.MCPIdentityStable = mcpPID, mcpStart, true
	defer func() {
		if client != nil && client.cmd != nil {
			_ = windowsARM64ProcessARM64JavaCloseClient(t, client)
		}
	}()
	initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "super-dolphin-java-windows-arm64", "version": "1"}})
	if initialize.Error != nil {
		t.Fatalf("Java MCP initialize returned error")
	}
	if err := windowsARM64ProcessARM64JavaNotify(client, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("Java initialized notification failed: %v", err)
	}
	receipt.FailurePhase, receipt.FailureOperation = "actions", "run_36_actions"
	tools := callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{})
	requireRealMCPToolFamilies(t, tools)
	if crossToolProbe {
		receipt.ExpectedActionTotal = 3
		receipt.FailurePhase, receipt.FailureOperation = "actions", "cross_tool_probe"
		t.Log("Java cross-tool probe manager_resolution_contract=single_entry_manager_reused registry_hit_expected=true")
		windowsARM64ProcessARM64JavaRunCrossToolProbe(t, client, server, fixture, fixtureRoot, receipt, tracked, mcpPID, mcpStart)
		if !trackRealMCPProcessTree(t, mcpPID, "java-cross-tool-probe-before-close", tracked) {
			t.Fatalf("capture Java cross-tool probe process tree before shutdown failed")
		}
		javaIdentity, err := windowsARM64ProcessARM64JavaFindServerIdentity(tracked, resolved.ServerPath)
		if err != nil {
			t.Fatalf("capture Java cross-tool probe JDTLS identity: %v", err)
		}
		receipt.JavaPID, receipt.JavaStartToken, receipt.JavaIdentityStable = javaIdentity.PID, javaIdentity.StartToken, true
		receipt.ProcessIdentities = windowsARM64ProcessARM64JavaSanitizeIdentities(tracked)
		receipt.FailurePhase, receipt.FailureOperation = "cleanup", "shutdown_exit_zero_residual"
		shutdown := client.call(t, "shutdown", map[string]any{})
		if shutdown.Error != nil {
			t.Fatalf("Java cross-tool probe shutdown returned JSON-RPC error")
		}
		receipt.ShutdownResponse = true
		receipt.ExitSent = windowsARM64ProcessARM64JavaCloseClient(t, client)
		if !receipt.ExitSent {
			t.Fatalf("Java cross-tool probe exit did not complete cleanly")
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		receipt.ZeroResidual = true
		receipt.Status = "pass"
		receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return
	}
	if renameProbe {
		receipt.ExpectedActionTotal = 4
		receipt.FailurePhase, receipt.FailureOperation = "actions", "rename_hydration_probe"
		t.Log("Java rename probe manager_resolution_contract=single_entry_manager_reused registry_hit_expected=true")
		windowsARM64ProcessARM64JavaRunRenameProbe(t, client, server, fixture, fixtureRoot, receipt, tracked, mcpPID, mcpStart)
		if !trackRealMCPProcessTree(t, mcpPID, "java-rename-probe-before-close", tracked) {
			t.Fatalf("capture Java rename probe process tree before shutdown failed")
		}
		javaIdentity, err := windowsARM64ProcessARM64JavaFindServerIdentity(tracked, resolved.ServerPath)
		if err != nil {
			t.Fatalf("capture Java rename probe JDTLS identity: %v", err)
		}
		receipt.JavaPID, receipt.JavaStartToken, receipt.JavaIdentityStable = javaIdentity.PID, javaIdentity.StartToken, true
		receipt.ProcessIdentities = windowsARM64ProcessARM64JavaSanitizeIdentities(tracked)
		receipt.FailurePhase, receipt.FailureOperation = "cleanup", "shutdown_exit_zero_residual"
		shutdown := client.call(t, "shutdown", map[string]any{})
		if shutdown.Error != nil {
			t.Fatalf("Java rename probe shutdown returned JSON-RPC error")
		}
		receipt.ShutdownResponse = true
		receipt.ExitSent = windowsARM64ProcessARM64JavaCloseClient(t, client)
		if !receipt.ExitSent {
			t.Fatalf("Java rename probe exit did not complete cleanly")
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		receipt.ZeroResidual = true
		receipt.Status = "pass"
		receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return
	}
	windowsARM64ProcessARM64JavaRunActions(t, client, server, fixture, fixtureRoot, receipt, tracked, mcpPID, mcpStart)
	trackRealMCPProcessTree(t, mcpPID, "java-final-before-idle", tracked)
	javaIdentity, err := windowsARM64ProcessARM64JavaFindServerIdentity(tracked, resolved.ServerPath)
	if err != nil {
		t.Fatalf("capture resolver-owned Java/JDTLS PID/start identity: %v", err)
	}
	receipt.JavaPID, receipt.JavaStartToken, receipt.JavaIdentityStable = javaIdentity.PID, javaIdentity.StartToken, true
	receipt.IdleHeartbeats = windowsARM64ProcessARM64JavaWaitIdle(ctx, t, mcpPID, mcpStart, javaIdentity, windowsARM64ProcessARM64JavaProofIdle)
	receipt.PostIdle.Total = 3
	for _, post := range []struct {
		tool   string
		action string
		args   map[string]any
	}{
		{"inspect", "hover", map[string]any{"action": "hover", "pos": fixture.semanticPosition}},
		{"inspect", "definition", map[string]any{"action": "definition", "pos": fixture.semanticPosition}},
		{"xref", "references", map[string]any{"action": "references", "pos": fixture.semanticPosition, "include_declaration": true, "max_results": 20}},
	} {
		response := client.callTool(t, post.tool, realMCPWindowsToolArguments("java", fixtureRoot, post.tool, post.action, post.args))
		status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(post.tool, post.action), realMCPActionProtocolOptional(post.tool, post.action), "Java post-idle "+post.tool+"/"+post.action)
		if status != realMCPActionSucceeded {
			t.Fatalf("Java post-idle %s/%s was not non-empty semantic success: status=%s", post.tool, post.action, status)
		}
		realMCPActionSemanticContentNonEmpty(t, response, "Java post-idle "+post.tool+"/"+post.action)
		receipt.PostIdle.SemanticSuccess++
		if err := windowsARM64ProcessARM64JavaRequireIdentity(mcpPID, mcpStart, "MCP post-idle"); err != nil {
			t.Fatalf("MCP identity after Java post-idle: %v", err)
		}
		if err := windowsARM64ProcessARM64JavaRequireIdentity(javaIdentity.PID, javaIdentity.StartToken, "Java/JDTLS post-idle"); err != nil {
			t.Fatalf("Java/JDTLS identity after post-idle: %v", err)
		}
	}
	receipt.PostIdle.Complete, receipt.PostIdle.NonEmpty = true, true
	if !windowsARM64ProcessARM64JavaPostIdlePass(receipt.PostIdle, receipt.JavaIdentityStable && receipt.MCPIdentityStable) {
		t.Fatalf("Java post-idle semantic health predicate failed: %#v", receipt.PostIdle)
	}
	if !trackRealMCPProcessTree(t, mcpPID, "java-final-before-close", tracked) {
		t.Fatalf("capture final Java process tree before shutdown failed")
	}
	receipt.ProcessIdentities = windowsARM64ProcessARM64JavaSanitizeIdentities(tracked)
	receipt.FailurePhase, receipt.FailureOperation = "cleanup", "shutdown_exit_zero_residual"
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.Error != nil {
		t.Fatalf("Java shutdown returned JSON-RPC error")
	}
	receipt.ShutdownResponse = true
	receipt.ExitSent = windowsARM64ProcessARM64JavaCloseClient(t, client)
	if !receipt.ExitSent {
		t.Fatalf("Java exit did not complete cleanly")
	}
	requireRealMCPProcessIdentitiesGone(t, tracked)
	receipt.ZeroResidual = true
	receipt.Status = "pass"
	receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
}
