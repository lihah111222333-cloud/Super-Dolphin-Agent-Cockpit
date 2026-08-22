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
	"golang.org/x/sys/windows"
)

const (
	windowsARM64ProcessARM64Gopls4x36E2EEnv        = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_GOPLS_4X36_E2E"
	windowsARM64ProcessARM64Gopls4x36PrecheckEnv   = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_GOPLS_4X36_PRECHECK"
	windowsARM64ProcessARM64GoplsEvidenceDir       = ".build-cache/codex-gopls-windows-proof"
	windowsARM64ProcessARM64GoplsReceiptPrefix     = "windows-arm64-process-arm64-gopls-4x36-soak"
	windowsARM64ProcessARM64GoplsManagerIdle       = 17 * time.Minute
	windowsARM64ProcessARM64GoplsProofIdle         = 15 * time.Minute
	windowsARM64ProcessARM64GoplsProductionMinIdle = 15 * time.Minute
	windowsARM64ProcessARM64GoplsPrecheckMax       = 30 * time.Second
	windowsARM64ProcessARM64GoplsVersion           = "0.23.0"
	windowsARM64ProcessARM64GoplsGoVersion         = "1.26.5"
	windowsARM64ProcessARM64GoplsGoURL             = "https://go.dev/dl/go1.26.5.windows-arm64.zip"
	windowsARM64ProcessARM64GoplsGoSHA256          = "f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017"
	windowsARM64ProcessARM64GoplsSourceURL         = "https://proxy.golang.org/golang.org/x/tools/gopls/@v/v0.23.0.zip"
	windowsARM64ProcessARM64GoplsSourceSHA256      = "b3bb593ef163f614e358cdb14a9feede3cad2bfc9087b8e4dca73b2fff858b74"
)

var windowsARM64ProcessARM64GoplsLanguageIDs = []string{"go", "gomod", "gosum", "gowork"}

// windowsARM64ProcessARM64GoplsActionReceipt 只保存可交付的脱敏摘要；原始 MCP
// 参数和 payload 只应存在于本地调试 wire，不得把工作区绝对路径写入 receipt。
type windowsARM64ProcessARM64GoplsActionReceipt struct {
	Tool          string         `json:"tool"`
	Action        string         `json:"action"`
	Status        string         `json:"status"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	ContentBytes  int            `json:"content_bytes,omitempty"`
	ContentSHA256 string         `json:"content_sha256,omitempty"`
}

type windowsARM64ProcessARM64GoplsProcessReceipt struct {
	PID        int    `json:"pid"`
	StartToken string `json:"start_token"`
	Name       string `json:"name,omitempty"`
	Executable string `json:"executable,omitempty"`
}

type windowsARM64ProcessARM64GoplsHTTPReceipt struct {
	Requests          int `json:"requests"`
	Attempts          int `json:"attempts"`
	Responses         int `json:"responses"`
	RedirectResponses int `json:"redirect_responses"`
	TransportErrors   int `json:"transport_errors"`
}

type windowsARM64ProcessARM64GoplsReceipt struct {
	Test                          string                                        `json:"test"`
	LanguageID                    string                                        `json:"language_id"`
	Phase                         string                                        `json:"phase,omitempty"`
	Status                        string                                        `json:"status"`
	FailurePhase                  string                                        `json:"failure_phase,omitempty"`
	FailureDigest                 string                                        `json:"failure_digest,omitempty"`
	StartedAt                     string                                        `json:"started_at"`
	FinishedAt                    string                                        `json:"finished_at"`
	Precheck                      bool                                          `json:"precheck"`
	ManagerIdleTimeout            string                                        `json:"manager_idle_timeout"`
	ProofIdleDuration             string                                        `json:"proof_idle_duration"`
	ProductionMinimumIdle         string                                        `json:"production_minimum_idle"`
	IdleDuration                  string                                        `json:"idle_duration,omitempty"`
	IdleHeartbeats                int                                           `json:"idle_heartbeats"`
	HostOS                        string                                        `json:"host_os"`
	WindowsVersion                string                                        `json:"windows_version"`
	WindowsBuild                  uint32                                        `json:"windows_build"`
	NativeArch                    string                                        `json:"native_arch"`
	ProcessArch                   string                                        `json:"process_arch"`
	ProcessArchDiagnosticOnly     bool                                          `json:"process_arch_diagnostic_only"`
	Product                       string                                        `json:"product"`
	GoVersion                     string                                        `json:"go_version"`
	GoplsVersion                  string                                        `json:"gopls_version"`
	GoSourceURL                   string                                        `json:"go_source_url"`
	GoSourceSHA256                string                                        `json:"go_source_sha256"`
	GoplsSourceURL                string                                        `json:"gopls_source_url"`
	GoplsSourceSHA256             string                                        `json:"gopls_source_sha256"`
	GoExecutableRelative          string                                        `json:"go_executable_relative,omitempty"`
	Cohort                        string                                        `json:"cohort,omitempty"`
	InstallStatus                 string                                        `json:"install_status,omitempty"`
	CacheBeforeEntries            int                                           `json:"cache_before_entries"`
	CacheAfterEntries             int                                           `json:"cache_after_entries"`
	CacheBeforeEmpty              bool                                          `json:"cache_before_empty"`
	CacheReadyAfterInstall        bool                                          `json:"cache_ready_after_install"`
	HTTP                          windowsARM64ProcessARM64GoplsHTTPReceipt      `json:"http"`
	ServerPathRelative            string                                        `json:"server_path_relative,omitempty"`
	MCPPID                        int                                           `json:"mcp_pid,omitempty"`
	MCPStartToken                 string                                        `json:"mcp_start_token,omitempty"`
	GoplsPID                      int                                           `json:"gopls_pid,omitempty"`
	GoplsStartToken               string                                        `json:"gopls_start_token,omitempty"`
	MCPIdentityStable             bool                                          `json:"mcp_identity_stable"`
	GoplsIdentityStable           bool                                          `json:"gopls_identity_stable"`
	PostIdleNonEmpty              bool                                          `json:"post_idle_non_empty"`
	PostIdleActionComplete        bool                                          `json:"post_idle_action_complete"`
	PostIdleTotal                 int                                           `json:"post_idle_total"`
	PostIdleSemanticSuccess       int                                           `json:"post_idle_semantic_success"`
	PostIdleLegalEmpty            int                                           `json:"post_idle_legal_empty"`
	PostIdleCapabilityUnsupported int                                           `json:"post_idle_capability_unsupported"`
	PostIdleNullResult            int                                           `json:"post_idle_null_result"`
	PostIdleRuntimeErrors         int                                           `json:"post_idle_runtime_errors"`
	ShutdownResponse              bool                                          `json:"shutdown_response"`
	ExitSent                      bool                                          `json:"exit_sent"`
	ZeroResidual                  bool                                          `json:"zero_residual"`
	ActionLedgerComplete          bool                                          `json:"action_ledger_complete"`
	ActionTotal                   int                                           `json:"action_total"`
	ExpectedActionTotal           int                                           `json:"expected_action_total"`
	SemanticSuccess               int                                           `json:"semantic_success"`
	LegalEmpty                    int                                           `json:"legal_empty"`
	CapabilityUnsupported         int                                           `json:"capability_unsupported"`
	NullResult                    int                                           `json:"null_result"`
	RuntimeErrors                 int                                           `json:"runtime_errors"`
	WirePath                      string                                        `json:"wire_path"`
	ProcessIdentities             []windowsARM64ProcessARM64GoplsProcessReceipt `json:"process_identities,omitempty"`
	Actions                       []windowsARM64ProcessARM64GoplsActionReceipt  `json:"actions"`
}

// windowsARM64ProcessARM64GoplsPostIdleClassification 是三次 post-idle 语义动作的闭包分类。
// go/main.go 必须三次都得到非空语义结果；gomod/gosum/gowork 的自然文件可以合法为空或协议声明
// unsupported，但仍必须三次非 null、无 runtime error、完整分类且对应 gopls 身份稳定。
// 该判定只描述语言合同，不读取 runtime.GOOS/GOARCH，也不把合法空或 unsupported 冒充语义成功。
type windowsARM64ProcessARM64GoplsPostIdleClassification struct {
	Total                 int
	SemanticSuccess       int
	LegalEmpty            int
	CapabilityUnsupported int
	NullResult            int
	RuntimeErrors         int
	ActionComplete        bool
	NonEmpty              bool
}

// windowsARM64ProcessARM64GoplsPostIdlePass 只判定 post-idle 闭包，不改变 36-action 账本语义。
func windowsARM64ProcessARM64GoplsPostIdlePass(languageID string, classification windowsARM64ProcessARM64GoplsPostIdleClassification, identityStable bool) bool {
	if !identityStable || !classification.ActionComplete || classification.Total != 3 || classification.NullResult != 0 || classification.RuntimeErrors != 0 {
		return false
	}
	if classification.SemanticSuccess+classification.LegalEmpty+classification.CapabilityUnsupported != classification.Total {
		return false
	}
	switch languageID {
	case "go":
		return classification.SemanticSuccess == 3 && classification.NonEmpty
	case "gomod", "gosum", "gowork":
		return true
	default:
		return false
	}
}

var windowsARM64ProcessARM64GoplsHTTPTransportMu sync.Mutex

type windowsARM64ProcessARM64GoplsHTTPObserver struct {
	base              http.RoundTripper
	mu                sync.Mutex
	requests          int
	responses         int
	redirectResponses int
	transportErrors   int
}

func (o *windowsARM64ProcessARM64GoplsHTTPObserver) RoundTrip(request *http.Request) (*http.Response, error) {
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
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		o.redirectResponses++
	}
	return response, nil
}

func (o *windowsARM64ProcessARM64GoplsHTTPObserver) snapshot() windowsARM64ProcessARM64GoplsHTTPReceipt {
	o.mu.Lock()
	defer o.mu.Unlock()
	return windowsARM64ProcessARM64GoplsHTTPReceipt{
		Requests: o.requests, Attempts: o.requests, Responses: o.responses,
		RedirectResponses: o.redirectResponses, TransportErrors: o.transportErrors,
	}
}

// windowsARM64ProcessARM64GoplsEnsureObserved 在 production EnsureInstalledDetailed
// 周围临时包裹 DefaultTransport，只记录计数，不记录 URL、header、token 或路径。
func windowsARM64ProcessARM64GoplsEnsureObserved(ctx context.Context, provider *installer.Provider, language string) (installer.InstallResult, windowsARM64ProcessARM64GoplsHTTPReceipt, error) {
	windowsARM64ProcessARM64GoplsHTTPTransportMu.Lock()
	defer windowsARM64ProcessARM64GoplsHTTPTransportMu.Unlock()
	base := http.DefaultTransport
	if base == nil {
		base = &http.Transport{}
	}
	observer := &windowsARM64ProcessARM64GoplsHTTPObserver{base: base}
	http.DefaultTransport = observer
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), language)
	http.DefaultTransport = base
	return result, observer.snapshot(), err
}

func windowsARM64ProcessARM64GoplsReceiptFailure(receipt *windowsARM64ProcessARM64GoplsReceipt, phase string, err error) {
	if receipt == nil {
		return
	}
	receipt.Status = "non_pass"
	receipt.FailurePhase = phase
	if err != nil {
		digest := sha256.Sum256([]byte(err.Error()))
		receipt.FailureDigest = hex.EncodeToString(digest[:])
	}
}

func windowsARM64ProcessARM64GoplsReceiptArgs(args map[string]any) map[string]any {
	allowed := []string{"action", "language_id", "limit", "scope", "query", "regex", "case_sensitive", "max_results", "include_declaration", "direction", "language", "new_name", "only"}
	result := make(map[string]any, len(allowed))
	for _, key := range allowed {
		if value, ok := args[key]; ok {
			result[key] = value
		}
	}
	return result
}

func windowsARM64ProcessARM64GoplsPayloadSummary(payload []byte) (int, string) {
	digest := sha256.Sum256(payload)
	return len(payload), hex.EncodeToString(digest[:])
}

func windowsARM64ProcessARM64GoplsContentSummary(content string) (int, string) {
	return windowsARM64ProcessARM64GoplsPayloadSummary([]byte(content))
}

func windowsARM64ProcessARM64GoplsNullResult(response mcpLSPBinaryResponse) bool {
	return strings.TrimSpace(response.Result.ContentText()) == ""
}

func windowsARM64ProcessARM64GoplsCacheEntries(root string) (int, bool, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	return len(entries), len(entries) == 0, nil
}

func windowsARM64ProcessARM64GoplsLockedPlan(t *testing.T, architecture string) installer.WindowsRuntimeDependencyCatalogEntry {
	t.Helper()
	plan, err := installer.WindowsRuntimeDependencyPlanForArchitecture(installer.WindowsRuntimeDependencyProductGoGopls, architecture)
	if err != nil {
		t.Fatalf("resolve locked go-gopls plan for %s: %v", architecture, err)
	}
	if plan.Product != installer.WindowsRuntimeDependencyProductGoGopls || plan.Install.Command != "go install" || !slices.Equal(plan.Install.Args, []string{"install", "golang.org/x/tools/gopls@v0.23.0"}) || plan.Install.ServerPath != "bin/gopls.exe" {
		t.Fatalf("go-gopls install contract changed: product=%q command=%q args=%v server=%q", plan.Product, plan.Install.Command, plan.Install.Args, plan.Install.ServerPath)
	}
	assets := plan.AssetsByArchitecture[architecture]
	var goAsset, goplsAsset installer.WindowsRuntimeDependencyAsset
	for _, asset := range assets {
		switch asset.Component {
		case "go":
			goAsset = asset
		case "gopls":
			goplsAsset = asset
		}
	}
	if goAsset.Version != windowsARM64ProcessARM64GoplsGoVersion || goAsset.URL != windowsARM64ProcessARM64GoplsGoURL || !strings.EqualFold(goAsset.Checksum, windowsARM64ProcessARM64GoplsGoSHA256) || goAsset.Architecture != architecture {
		t.Fatalf("locked Go asset changed: %#v", goAsset)
	}
	if goplsAsset.Version != windowsARM64ProcessARM64GoplsVersion || goplsAsset.URL != windowsARM64ProcessARM64GoplsSourceURL || !strings.EqualFold(goplsAsset.Checksum, windowsARM64ProcessARM64GoplsSourceSHA256) || goplsAsset.Architecture != architecture {
		t.Fatalf("locked gopls source asset changed: %#v", goplsAsset)
	}
	return plan
}

func windowsARM64ProcessARM64GoplsValidatePE(path string, wantMachine uint16) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open PE: %w", err)
	}
	defer file.Close()
	if uint16(file.FileHeader.Machine) != wantMachine {
		return fmt.Errorf("PE machine=0x%04x want=0x%04x", file.FileHeader.Machine, wantMachine)
	}
	return nil
}

func windowsARM64ProcessARM64GoplsLockedGo(t *testing.T, repoRoot string) string {
	t.Helper()
	configured := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_GO_BIN"))
	if configured == "" {
		t.Fatalf("formal Go gopls E2E requires SUPER_DOLPHIN_GO_BIN pointing to locked Go 1.26.5")
	}
	path := filepath.Clean(configured)
	if err := windowsARM64ProcessARM64GoplsValidatePE(path, installer.WindowsImageFileMachineARM64); err != nil {
		t.Fatalf("locked Go compiler is not ARM64 PE: %v", err)
	}
	output, err := exec.Command(path, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "go1.26.5") || !strings.Contains(string(output), "windows/arm64") {
		t.Fatalf("locked Go compiler version=%q err=%v; want go1.26.5 windows/arm64", strings.TrimSpace(string(output)), err)
	}
	if !filepath.IsAbs(path) || !strings.HasPrefix(strings.ToLower(path), strings.ToLower(filepath.Join(repoRoot, ".build-cache"))) {
		t.Fatalf("locked Go compiler must be product-owned under .build-cache, got %q", path)
	}
	return path
}

func windowsARM64ProcessARM64GoplsValidateProductGo(t *testing.T, productRoot, path string) string {
	t.Helper()
	path = filepath.Clean(path)
	if err := windowsARM64ProcessARM64GoplsValidatePE(path, installer.WindowsImageFileMachineARM64); err != nil {
		t.Fatalf("production Go executable is not ARM64 PE: %v", err)
	}
	output, err := exec.Command(path, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "go1.26.5") || !strings.Contains(string(output), "windows/arm64") {
		t.Fatalf("production Go executable version=%q err=%v; want go1.26.5 windows/arm64", strings.TrimSpace(string(output)), err)
	}
	relative, err := filepath.Rel(productRoot, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("production Go executable escaped product root: relative=%q err=%v", relative, err)
	}
	return filepath.ToSlash(relative)
}

// windowsARM64ProcessARM64GoplsInstallPackagedBundle 把生产安装器已校验的 gopls
// 收敛到 mcp-lsp 同一产品根的固定 lsp/bin 布局，并生成生产信任链实际读取的严格清单。
// 该步骤不伪造服务器：bundle 内容逐字节来自本轮自动安装结果，SHA256 由落盘文件重算。
func windowsARM64ProcessARM64GoplsInstallPackagedBundle(t *testing.T, productRoot, installedGopls string) (string, string, string) {
	t.Helper()
	bundleRoot := filepath.Join(productRoot, "lsp")
	bundledGopls := filepath.Join(bundleRoot, "bin", "gopls.exe")
	if err := os.MkdirAll(filepath.Dir(bundledGopls), 0o700); err != nil {
		t.Fatalf("create packaged Windows gopls directory: %v", err)
	}
	payload, err := os.ReadFile(installedGopls)
	if err != nil {
		t.Fatalf("read auto-installed Windows gopls: %v", err)
	}
	if err := os.WriteFile(bundledGopls, payload, 0o700); err != nil {
		t.Fatalf("write packaged Windows gopls: %v", err)
	}
	if err := windowsARM64ProcessARM64GoplsValidatePE(bundledGopls, installer.WindowsImageFileMachineARM64); err != nil {
		t.Fatalf("packaged Windows gopls is not ARM64 PE: %v", err)
	}
	digest := sha256.Sum256(payload)
	manifest := runtimeServerWindowsLSPManifest{
		SchemaVersion: 1,
		BundlePath:    "lsp",
		Profile:       "standard",
		Servers: map[string]runtimeServerWindowsLSPServer{
			"gopls": {
				Path:      "bin/gopls.exe",
				Version:   windowsARM64ProcessARM64GoplsVersion,
				SHA256:    hex.EncodeToString(digest[:]),
				Languages: slices.Clone(windowsARM64ProcessARM64GoplsLanguageIDs),
			},
		},
	}
	manifestPayload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode packaged Windows gopls manifest: %v", err)
	}
	manifestPath := filepath.Join(bundleRoot, "lsp-manifest.json")
	if err := os.WriteFile(manifestPath, append(manifestPayload, '\n'), 0o600); err != nil {
		t.Fatalf("write packaged Windows gopls manifest: %v", err)
	}
	return bundleRoot, manifestPath, bundledGopls
}

const windowsARM64ProcessARM64GoplsFixtureContent = `package main

type Greeter interface {
	RootGreeter
}

type BaseGreeter struct{}

func (BaseGreeter) Greet(name string) string { return formatGreeting(name) }

type DerivedGreeter struct{ BaseGreeter }

func formatGreeting(name string) string { return "Hello, " + name }

func main() { var g Greeter = DerivedGreeter{}; _ = g.Greet("world") }

type RootGreeter interface {
	Greet(name string) string
}

type ExtendedGreeter interface {
	Greeter
	Extra() string
}
`

func windowsARM64ProcessARM64GoplsPosition(t *testing.T, path, content string, line int, needle string, occurrence int) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	if line <= 0 || line > len(lines) {
		t.Fatalf("Go fixture line %d is outside %d lines", line, len(lines))
	}
	text := lines[line-1]
	for index := 0; index < occurrence; index++ {
		start := strings.Index(text, needle)
		if start < 0 {
			t.Fatalf("Go fixture line %d lacks occurrence %d of %q", line, occurrence, needle)
		}
		text = text[start+len(needle):]
		if index == occurrence-1 {
			start = strings.Index(lines[line-1], needle)
			for step := 0; step < index; step++ {
				start = strings.Index(lines[line-1][start+len(needle):], needle) + start + len(needle)
			}
			return realMCPPositionFromLSP(path, line, start)
		}
	}
	return ""
}

func windowsARM64ProcessARM64GoplsWriteFixture(t *testing.T, root, languageID string) (realNodeServerCase, realMCPFixture) {
	t.Helper()
	semanticFile := filepath.Join(root, "main.go")
	semanticContent := windowsARM64ProcessARM64GoplsFixtureContent
	writeRealFixture(t, semanticFile, semanticContent)
	targetName, targetContent := "main.go", semanticContent
	switch languageID {
	case "gomod":
		targetName, targetContent = "go.mod", "module example.test/goplsproof\n\ngo 1.25.0\n"
	case "gosum":
		targetName, targetContent = "go.sum", "example.test/goplsproof v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"
	case "gowork":
		targetName, targetContent = "go.work", "go 1.25.0\n\nuse .\n"
	}
	target := filepath.Join(root, targetName)
	if target != semanticFile {
		writeRealFixture(t, target, targetContent)
	}
	secondary := filepath.Join(root, "secondary.go")
	replace := filepath.Join(root, "replace.go")
	rename := filepath.Join(root, "rename.go")
	codeAction := filepath.Join(root, "code_action.go")
	format := filepath.Join(root, "format.go")
	completion := filepath.Join(root, "completion.go")
	completionLine := "func completionProbe() { var g Greeter; g.G }"
	completionContent := "package main\n" + completionLine + "\n"
	writeRealFixture(t, secondary, semanticContent+"\nvar realMCPNeedle_"+languageID+" = \"gopls\"\n")
	writeRealFixture(t, replace, "package main\n\n// REAL_GOPLS_REPLACE_ME\n")
	writeRealFixture(t, rename, semanticContent)
	writeRealFixture(t, codeAction, "package main\n\nfunc broken() { _ = missingGoplsIdentifier }\n")
	writeRealFixture(t, format, "package main\nfunc formatProbe(){println(\"format\")}\n")
	writeRealFixture(t, completion, completionContent)
	writeRealFixture(t, filepath.Join(root, "go.mod"), "module example.test/goplsproof\n\ngo 1.25.0\n")
	writeRealFixture(t, filepath.Join(root, "go.sum"), "example.test/goplsproof v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")
	writeRealFixture(t, filepath.Join(root, "go.work"), "go 1.25.0\n\nuse .\n")

	// 共享 helper 只提供 36 个 canonical key/参数骨架；每个 language ID 的自然目标文件是主证据。
	server := realNodeServerCase{name: "go", languageID: languageID, fileName: filepath.Base(target), content: targetContent, line: 1, character: 1}
	semantic := realMCPPosition(target, 1, 1)
	definition, implementation, typeDefinition, callHierarchy, typeHierarchy, signature := semantic, semantic, semantic, semantic, semantic, semantic
	if languageID == "go" {
		semantic = windowsARM64ProcessARM64GoplsPosition(t, semanticFile, semanticContent, 9, "formatGreeting", 1)
		definition = windowsARM64ProcessARM64GoplsPosition(t, semanticFile, semanticContent, 9, "formatGreeting", 1)
		implementation = windowsARM64ProcessARM64GoplsPosition(t, semanticFile, semanticContent, 15, "Greeter", 1)
		typeDefinition = windowsARM64ProcessARM64GoplsPosition(t, semanticFile, semanticContent, 15, "g", 1)
		// Greet 方法既被 main 调用、又调用 formatGreeting，可同时证明 incoming/outgoing/both。
		callHierarchy = windowsARM64ProcessARM64GoplsPosition(t, semanticFile, semanticContent, 9, "Greet", 2)
		typeHierarchy = implementation
		// 使用已经显式打开的 main.go 中的直接函数调用，排除接口分派和跨文档 bootstrap 差异。
		signature = windowsARM64ProcessARM64GoplsPosition(t, semanticFile, semanticContent, 9, "name", 2)
	}
	completionPosition := realMCPPosition(target, 1, 1)
	if languageID == "go" {
		completionPosition = realMCPPositionFromLSP(completion, 2, strings.Index(completionLine, "g.G")+len("g.G"))
	}
	codeActionContent := "package main\n\nfunc broken() { _ = missingGoplsIdentifier }\n"
	codeActionPosition := realMCPPositionFromLSP(codeAction, 3, strings.Index(codeActionContent, "missingGoplsIdentifier"))
	fixture := realMCPFixture{
		targetFile: target, secondaryFile: secondary, replaceFile: replace, renameFile: rename,
		codeActionFile: codeAction, formatFile: format, completionFile: completion,
		semanticPosition: semantic, renamePosition: definition, implementationPosition: implementation,
		typeDefinitionPosition: typeDefinition, callHierarchyPosition: callHierarchy,
		typeHierarchyPosition: typeHierarchy, signaturePosition: signature,
		completionPosition: completionPosition, codeActionPosition: codeActionPosition,
		replacePatch: "@@\n-// REAL_GOPLS_REPLACE_ME\n+// REAL_GOPLS_REPLACED\n",
	}
	return server, fixture
}

// windowsARM64ProcessARM64GoplsActions 基于共享 canonical 36-action 合同，仅替换
// JavaScript AST 查询和 Go workspace selector；七个工具族及动作键不得自行缩减。
func windowsARM64ProcessARM64GoplsActions(server realNodeServerCase, fixture realMCPFixture) []realMCPActionSpec {
	semanticFile := realMCPPositionPath(fixture.semanticPosition)
	actions := realMCPActionSpecs(server, fixture, semanticFile)
	for index := range actions {
		action := &actions[index]
		// Go 的 action 合同在本文件显式定义；不得把 Python fixture 的预期结果带入 Go 证明。
		action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported = windowsARM64ProcessARM64GoplsActionContract(server.languageID, action.tool, action.name)
		action.contractSet = true
		switch action.tool + "/" + action.name {
		case "grep/ast_search":
			action.args["query"] = "func $NAME($$$ARGS) { $$$BODY }"
			action.args["ast_language"] = "go"
		case "structure/workspace_symbol-file", "structure/workspace_symbol-language":
			action.args["query"] = windowsARM64ProcessARM64GoplsWorkspaceQuery(server.languageID)
			if action.name == "workspace_symbol-language" {
				action.args["workspace_language"] = server.languageID
			}
		case "structure/document_symbol", "structure/folding_range", "structure/semantic_tokens":
			action.args["file_path"] = semanticFile
		}
		if action.tool == "structure" && action.name == "workspace_symbol-file" {
			action.args["file_path"] = semanticFile
		}
		if action.tool == "grep" {
			query := windowsARM64ProcessARM64GoplsWorkspaceQuery(server.languageID)
			action.args["query"] = query
			action.args["paths"] = []string{semanticFile}
			action.args["glob"] = filepath.Base(semanticFile)
		}
	}
	return actions
}

// windowsARM64ProcessARM64GoplsActionContract 是 Go/gomod/gosum/gowork 的显式结果合同。
// 共享 Node 合同只复用 36 个 action key；自然文件不支持的语义动作必须记为合法空或协议 unsupported。
func windowsARM64ProcessARM64GoplsActionContract(languageID, tool, action string) (requireResult bool, emptyReason string, allowCapabilityUnsupported bool) {
	naturalSemantic := languageID != "go"
	switch {
	case tool == "file" && (action == "diagnostics" || action == "diagnostics-batch"):
		return false, "diagnostics 对无诊断自然文件允许合法空结果", false
	case tool == "patch_edit" && action == "replace_range":
		return false, "replace_range 的 changed=false/空 edit 是合法成功结果", false
	case tool == "patch_edit" && action == "format":
		return false, "format 对已格式化文件允许合法空 edit", false
	case tool == "patch_edit" && action == "code_action":
		return false, "code_action 对无 quickfix 诊断允许合法空列表", false
	case naturalSemantic && tool == "grep" && action == "ast_search":
		return false, "go.mod/go.sum/go.work 不是 Go AST，ast_search 合法空结果", false
	case naturalSemantic && tool == "file" && action == "read_file-function":
		return false, "go.mod/go.sum/go.work 没有 Go function scope，read_file-function 合法空结果", false
	case naturalSemantic && tool == "inspect":
		return false, "该自然文件类型没有稳定的 gopls inspect 语义，合法空结果单列", true
	case naturalSemantic && tool == "xref":
		return false, "该自然文件类型没有稳定的 gopls xref 语义，合法空结果单列", true
	case naturalSemantic && tool == "structure":
		return false, "该自然文件类型没有稳定的 gopls structure 语义，合法空结果单列", true
	case naturalSemantic && tool == "patch_edit" && action == "rename":
		return false, "自然文件无可重命名 Go 符号，合法空结果单列", true
	case naturalSemantic && tool == "completion":
		return false, "自然文件无稳定 completion 候选，合法空结果单列", true
	default:
		if tool == "xref" && strings.HasPrefix(action, "type_hierarchy-") {
			return true, "", true
		}
		return true, "", false
	}
}

func windowsARM64ProcessARM64GoplsWorkspaceQuery(languageID string) string {
	if languageID == "go" {
		return "formatGreeting"
	}
	return "goplsproof"
}

// TestWindowsARM64ProcessARM64GoplsNaturalFixtureContract 锁定自然 basename/语法与合同，完全不启动网络或 MCP。
func TestWindowsARM64ProcessARM64GoplsNaturalFixtureContract(t *testing.T) {
	if !realMCPGoplsNaturalFileCapabilityError("gopls gomod xref/type_hierarchy-supertypes", "ERROR code=tool_error MESSAGE unsupported file type: C:\\private\\go.mod") {
		t.Fatal("gopls natural go.mod unsupported-file-type error must be capability-accountable")
	}
	if !realMCPGoplsNaturalFileCapabilityError("gopls gomod patch_edit/rename", "ERROR code=tool_error MESSAGE cannot rename in file of type go.mod") {
		t.Fatal("gopls natural go.mod rename capability error must be capability-accountable")
	}
	if !realMCPGoplsNaturalFileCapabilityError("gopls gomod post-idle inspect/definition", "ERROR code=tool_error MESSAGE can't find definitions for file type go.mod") {
		t.Fatal("post-idle gopls label must preserve the natural language classifier prefix")
	}
	if realMCPGoplsNaturalFileCapabilityError("gopls gosum patch_edit/rename", "ERROR code=tool_error MESSAGE cannot rename in file of type go.mod") {
		t.Fatal("natural-file capability classifier must require the label/file-type pair")
	}
	if realMCPGoplsNaturalFileCapabilityError("gopls go xref/type_hierarchy-supertypes", "ERROR code=tool_error MESSAGE unsupported file type: main.go") {
		t.Fatal("ordinary Go tool_error must not be downgraded to capability unsupported")
	}
	wantTargets := map[string]string{"go": "main.go", "gomod": "go.mod", "gosum": "go.sum", "gowork": "go.work"}
	wantText := map[string]string{"go": "formatGreeting", "gomod": "module example.test/goplsproof", "gosum": "example.test/goplsproof", "gowork": "use ."}
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		languageID := languageID
		t.Run(languageID, func(t *testing.T) {
			server, fixture := windowsARM64ProcessARM64GoplsWriteFixture(t, t.TempDir(), languageID)
			if got := filepath.Base(fixture.targetFile); got != wantTargets[languageID] {
				t.Fatalf("natural target=%q, want %q", got, wantTargets[languageID])
			}
			if server.languageID != languageID || server.fileName != wantTargets[languageID] {
				t.Fatalf("server natural identity=%#v", server)
			}
			payload, err := os.ReadFile(fixture.targetFile)
			if err != nil || !strings.Contains(string(payload), wantText[languageID]) {
				t.Fatalf("natural target %s syntax/content missing marker %q: err=%v", fixture.targetFile, wantText[languageID], err)
			}
			actions := windowsARM64ProcessARM64GoplsActions(server, fixture)
			if err := validateRealMCPActionClosure(actions); err != nil {
				t.Fatalf("canonical Go action closure: %v", err)
			}
			for _, action := range actions {
				if action.tool == "xref" && strings.HasPrefix(action.name, "type_hierarchy-") && !action.allowCapabilityUnsupported {
					t.Fatalf("%s/%s must preserve protocol-optional unsupported accounting", action.tool, action.name)
				}
				gotActionLanguage := windowsARM64ProcessARM64GoplsActionLanguageID(languageID, action)
				wantActionLanguage := languageID
				if languageID != "go" && action.tool == "patch_edit" && (action.name == "replace_range" || action.name == "code_action" || action.name == "format") {
					wantActionLanguage = "go"
				}
				if gotActionLanguage != wantActionLanguage {
					t.Fatalf("%s/%s action language=%q, want %q", action.tool, action.name, gotActionLanguage, wantActionLanguage)
				}
				if languageID == "go" && action.tool == "inspect" && action.name == "hover" && !action.requireResult {
					t.Fatalf("Go main.go hover must require non-empty result")
				}
			}
			if languageID == "go" {
				if !strings.Contains(string(payload), "RootGreeter") || !strings.Contains(string(payload), "ExtendedGreeter") {
					t.Fatal("Go type hierarchy fixture must keep Greeter between RootGreeter and ExtendedGreeter")
				}
				wantCallHierarchy := windowsARM64ProcessARM64GoplsPosition(t, fixture.targetFile, string(payload), 9, "Greet", 2)
				if fixture.callHierarchyPosition != wantCallHierarchy {
					t.Fatalf("Go call hierarchy cursor=%q, want method identifier cursor %q", fixture.callHierarchyPosition, wantCallHierarchy)
				}
				wantSignature := windowsARM64ProcessARM64GoplsPosition(t, fixture.targetFile, string(payload), 9, "name", 2)
				if fixture.signaturePosition != wantSignature {
					t.Fatalf("Go signature_help cursor=%q, want call-argument cursor %q", fixture.signaturePosition, wantSignature)
				}
				completionLine := "func completionProbe() { var g Greeter; g.G }"
				wantCompletion := realMCPPositionFromLSP(fixture.completionFile, 2, strings.Index(completionLine, "g.G")+len("g.G"))
				if fixture.completionPosition != wantCompletion {
					t.Fatalf("Go completion cursor=%q, want line-relative cursor %q", fixture.completionPosition, wantCompletion)
				}
				for _, action := range actions {
					legalEmpty := action.tool == "file" && (action.name == "diagnostics" || action.name == "diagnostics-batch") || action.tool == "patch_edit" && (action.name == "replace_range" || action.name == "code_action" || action.name == "format")
					if legalEmpty != !action.requireResult {
						t.Fatalf("Go action %s/%s legal-empty contract=%v require=%v", action.tool, action.name, legalEmpty, action.requireResult)
					}
				}
				return
			}
			for _, action := range actions {
				semantic := action.tool == "inspect" || action.tool == "xref" || action.tool == "structure" || action.tool == "completion"
				naturalEmpty := semantic || (action.tool == "grep" && action.name == "ast_search") || (action.tool == "file" && action.name == "read_file-function") || (action.tool == "patch_edit" && action.name == "rename")
				if naturalEmpty && (action.requireResult || strings.TrimSpace(action.emptyResultReason) == "") {
					t.Fatalf("%s/%s natural %s contract must be explicit legal-empty or unsupported", action.tool, action.name, languageID)
				}
			}
		})
	}
}

// TestWindowsARM64ProcessARM64GoplsPostIdlePredicateContract 锁定四个自然文件的 post-idle 结论，完全不联网。
func TestWindowsARM64ProcessARM64GoplsPostIdlePredicateContract(t *testing.T) {
	tests := []struct {
		name       string
		languageID string
		value      windowsARM64ProcessARM64GoplsPostIdleClassification
		identity   bool
		want       bool
	}{
		{name: "go_requires_three_nonempty_semantic_results", languageID: "go", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, SemanticSuccess: 3, ActionComplete: true, NonEmpty: true}, identity: true, want: true},
		{name: "go_legal_empty_is_not_semantic_pass", languageID: "go", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, SemanticSuccess: 2, LegalEmpty: 1, ActionComplete: true}, identity: true, want: false},
		{name: "gomod_three_legal_empty_actions_are_complete", languageID: "gomod", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, LegalEmpty: 3, ActionComplete: true}, identity: true, want: true},
		{name: "gosum_three_protocol_unsupported_actions_are_complete", languageID: "gosum", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, CapabilityUnsupported: 3, ActionComplete: true}, identity: true, want: true},
		{name: "gowork_mixed_classification_is_complete", languageID: "gowork", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, SemanticSuccess: 1, LegalEmpty: 1, CapabilityUnsupported: 1, ActionComplete: true}, identity: true, want: true},
		{name: "natural_null_is_not_complete", languageID: "gomod", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, LegalEmpty: 2, NullResult: 1, ActionComplete: false}, identity: true, want: false},
		{name: "natural_runtime_error_is_not_complete", languageID: "gosum", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, LegalEmpty: 2, RuntimeErrors: 1, ActionComplete: false}, identity: true, want: false},
		{name: "natural_incomplete_classification_is_not_complete", languageID: "gowork", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 2, LegalEmpty: 2, ActionComplete: true}, identity: true, want: false},
		{name: "go_identity_change_is_not_complete", languageID: "go", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, SemanticSuccess: 3, ActionComplete: true, NonEmpty: true}, identity: false, want: false},
		{name: "unknown_language_is_not_accepted", languageID: "unknown", value: windowsARM64ProcessARM64GoplsPostIdleClassification{Total: 3, LegalEmpty: 3, ActionComplete: true}, identity: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := windowsARM64ProcessARM64GoplsPostIdlePass(test.languageID, test.value, test.identity); got != test.want {
				t.Fatalf("post-idle predicate language=%s got=%v want=%v classification=%#v identity=%v", test.languageID, got, test.want, test.value, test.identity)
			}
		})
	}
}

// TestWindowsARM64ProcessARM64GoplsSharedIdentityContract 证明同一 gopls PID/start 可归属四个语言，且 go 不会误匹配 gomod。
func TestWindowsARM64ProcessARM64GoplsSharedIdentityContract(t *testing.T) {
	serverPath := `C:\product\go-gopls\bin\gopls.exe`
	shared := realMCPProcessIdentity{PID: 7108, StartToken: "start-shared", Name: "gopls.exe", CommandLine: `"C:\product\go-gopls\bin\gopls.exe" -mode=stdio`, Language: "gopls-go-hover,gopls-gomod-hover,gopls-gosum-hover,gopls-gowork-hover"}
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: shared.PID, StartToken: shared.StartToken}: shared}
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		identity, err := windowsARM64ProcessARM64GoplsFindServerIdentityForLanguage(tracked, serverPath, languageID)
		if err != nil {
			t.Fatalf("shared gopls identity for %s: %v", languageID, err)
		}
		if identity.PID != shared.PID || identity.StartToken != shared.StartToken {
			t.Fatalf("shared gopls identity for %s changed: got pid=%d start=%s", languageID, identity.PID, identity.StartToken)
		}
	}
	nearMiss := shared
	nearMiss.Language = "gopls-gomod-hover"
	nearMissTracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: nearMiss.PID, StartToken: nearMiss.StartToken}: nearMiss}
	if _, err := windowsARM64ProcessARM64GoplsFindServerIdentityForLanguage(nearMissTracked, serverPath, "go"); err == nil {
		t.Fatal("go identity lookup must not match the gomod label by substring")
	}
}

func windowsARM64ProcessARM64GoplsProtocol(client *mcpLSPBinaryClient, method string, params map[string]any) (json.RawMessage, error) {
	if client == nil || client.cmd == nil {
		return nil, fmt.Errorf("gopls MCP client is not live")
	}
	request := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", method, err)
	}
	if _, err := client.stdin.Write(append(payload, '\n')); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", method, err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("decode %s: %w", method, err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s JSON-RPC error %d: %s", method, response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}

func windowsARM64ProcessARM64GoplsNotify(client *mcpLSPBinaryClient, method string, params map[string]any) error {
	if client == nil || client.cmd == nil {
		return fmt.Errorf("gopls MCP client is not live")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("marshal notification %s: %w", method, err)
	}
	_, err = client.stdin.Write(append(payload, '\n'))
	return err
}

func windowsARM64ProcessARM64GoplsCloseClient(t *testing.T, client *mcpLSPBinaryClient) bool {
	t.Helper()
	if client == nil || client.cmd == nil {
		return false
	}
	cmd := client.cmd
	client.cmd = nil
	closeHook := client.closeHook
	client.closeHook = nil
	exitWritten := false
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	if err == nil {
		_, err = client.stdin.Write(append(payload, '\n'))
		exitWritten = err == nil
	}
	if err := client.stdin.Close(); err != nil {
		exitWritten = false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	select {
	case waitErr := <-done:
		waited = waitErr == nil || errors.Is(waitErr, os.ErrProcessDone)
		if !waited {
			t.Logf("gopls MCP owner exit error=%v stderr=%s", waitErr, client.stderrString())
		}
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		<-done
		t.Errorf("gopls MCP owner exceeded bounded exit wait; forced kill is not a lifecycle PASS")
	}
	if closeHook != nil {
		if err := closeHook(); err != nil {
			waited = false
			t.Errorf("close gopls MCP process owner: %v", err)
		}
	}
	return exitWritten && waited
}

func windowsARM64ProcessARM64GoplsTrackedGone(tracked map[realMCPProcessKey]realMCPProcessIdentity) bool {
	for _, identity := range tracked {
		alive, err := processAliveForE2E(identity.PID)
		if err != nil {
			return false
		}
		if !alive {
			continue
		}
		current, identityErr := windowsGoplsProcessStartIdentity(identity.PID)
		if identityErr == nil && current != identity.StartToken {
			// PID 已被复用；启动身份不同，原 cohort 进程已消失。
			continue
		}
		return false
	}
	return true
}

func windowsARM64ProcessARM64GoplsWaitTrackedGone(tracked map[realMCPProcessKey]realMCPProcessIdentity, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if windowsARM64ProcessARM64GoplsTrackedGone(tracked) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// windowsARM64ProcessARM64GoplsCleanupTestOwnedDurableProcesses 只终止本次正式 E2E
// 私有 productRoot 内、且 PID 启动身份仍与取证一致的进程。生产侧 17 分钟 durable
// 生命周期保持不变；这里仅在测试已经完成生命周期证明或失败收尾时闭合零残留证据。
func windowsARM64ProcessARM64GoplsCleanupTestOwnedDurableProcesses(productRoot string, tracked map[realMCPProcessKey]realMCPProcessIdentity) error {
	root, err := filepath.Abs(productRoot)
	if err != nil {
		return fmt.Errorf("resolve gopls E2E product root: %w", err)
	}
	candidates := make([]realMCPProcessIdentity, 0, len(tracked))
	for _, identity := range tracked {
		executable := windowsARM64ProcessARM64GoplsSplitCommandLine(identity.CommandLine)
		if executable == "" {
			continue
		}
		executable, err = filepath.Abs(executable)
		if err != nil {
			return fmt.Errorf("resolve tracked PID %d executable: %w", identity.PID, err)
		}
		relative, relErr := filepath.Rel(root, executable)
		if relErr != nil {
			return fmt.Errorf("compare tracked PID %d executable with product root: %w", identity.PID, relErr)
		}
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		candidates = append(candidates, identity)
	}
	// 先停语言服务器子进程，最后停 broker；避免父进程先退出后丢失子进程的精确归属链。
	slices.SortStableFunc(candidates, func(left, right realMCPProcessIdentity) int {
		leftBroker := strings.EqualFold(filepath.Base(windowsARM64ProcessARM64GoplsSplitCommandLine(left.CommandLine)), "mcp-lsp.exe")
		rightBroker := strings.EqualFold(filepath.Base(windowsARM64ProcessARM64GoplsSplitCommandLine(right.CommandLine)), "mcp-lsp.exe")
		if leftBroker == rightBroker {
			return 0
		}
		if leftBroker {
			return 1
		}
		return -1
	})
	cleanupErrors := make([]error, 0, len(candidates))
	for _, identity := range candidates {
		alive, aliveErr := processAliveForE2E(identity.PID)
		if aliveErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("query test-owned PID %d before cleanup: %w", identity.PID, aliveErr))
			continue
		}
		if !alive {
			continue
		}
		handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(identity.PID))
		if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if openErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("open exact test-owned PID %d: %w", identity.PID, openErr))
			continue
		}
		terminateErr := terminateWindowsGoplsExactHandle(handle, identity.PID, identity.StartToken)
		closeErr := windows.CloseHandle(handle)
		if terminateErr != nil || closeErr != nil {
			cleanupErrors = append(cleanupErrors, errors.Join(terminateErr, closeErr))
		}
	}
	return errors.Join(cleanupErrors...)
}

func windowsARM64ProcessARM64GoplsWaitIdle(ctx context.Context, t *testing.T, mcpPID int, mcpStart string, goplsPID int, goplsStart string, duration time.Duration) int {
	t.Helper()
	if duration < windowsARM64ProcessARM64GoplsProductionMinIdle {
		t.Fatalf("formal Go gopls idle duration=%s is below production minimum=%s", duration, windowsARM64ProcessARM64GoplsProductionMinIdle)
	}
	started := time.Now()
	heartbeats := 0
	sample := func() {
		if err := windowsARM64ProcessARM64GoplsRequireIdentity(mcpPID, mcpStart, "MCP idle"); err != nil {
			t.Fatalf("MCP identity changed during gopls idle: %v", err)
		}
		if err := windowsARM64ProcessARM64GoplsRequireIdentity(goplsPID, goplsStart, "gopls idle"); err != nil {
			t.Fatalf("gopls identity changed during idle: %v", err)
		}
		heartbeats++
		t.Logf("Windows ARM64/process ARM64 gopls idle heartbeat elapsed=%s mcp_pid=%d gopls_pid=%d", time.Since(started).Round(time.Second), mcpPID, goplsPID)
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
			t.Fatalf("gopls idle sampling stopped before %s: %v", duration, ctx.Err())
		case <-timer.C:
			sample()
		}
	}
}

func windowsARM64ProcessARM64GoplsRequireIdentity(pid int, startToken, label string) error {
	if pid <= 0 || strings.TrimSpace(startToken) == "" {
		return fmt.Errorf("%s identity incomplete: pid=%d", label, pid)
	}
	alive, err := processAliveForE2E(pid)
	if err != nil {
		return fmt.Errorf("%s PID %d liveness: %w", label, pid, err)
	}
	if !alive {
		return fmt.Errorf("%s PID %d is not alive", label, pid)
	}
	current, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		return fmt.Errorf("%s PID %d start identity: %w", label, pid, err)
	}
	if current != startToken {
		return fmt.Errorf("%s PID %d start identity changed", label, pid)
	}
	return nil
}

func windowsARM64ProcessARM64GoplsSplitCommandLine(command string) string {
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

func windowsARM64ProcessARM64GoplsFindServerIdentity(tracked map[realMCPProcessKey]realMCPProcessIdentity, serverPath string) (realMCPProcessIdentity, error) {
	want := filepath.Clean(serverPath)
	for _, identity := range tracked {
		if !strings.Contains(strings.ToLower(identity.Name+" "+identity.CommandLine), "gopls") {
			continue
		}
		executable := windowsARM64ProcessARM64GoplsSplitCommandLine(identity.CommandLine)
		if executable != "" && strings.EqualFold(filepath.Clean(executable), want) {
			return identity, nil
		}
	}
	return realMCPProcessIdentity{}, fmt.Errorf("tracked process tree has no resolver-owned gopls executable")
}

func windowsARM64ProcessARM64GoplsSanitizeIdentities(tracked map[realMCPProcessKey]realMCPProcessIdentity) []windowsARM64ProcessARM64GoplsProcessReceipt {
	result := make([]windowsARM64ProcessARM64GoplsProcessReceipt, 0, len(tracked))
	for _, identity := range tracked {
		executable := windowsARM64ProcessARM64GoplsSplitCommandLine(identity.CommandLine)
		name := filepath.Base(strings.ReplaceAll(executable, "/", "\\"))
		if name == "." || name == "" {
			name = filepath.Base(identity.Name)
		}
		result = append(result, windowsARM64ProcessARM64GoplsProcessReceipt{PID: identity.PID, StartToken: identity.StartToken, Name: filepath.Base(identity.Name), Executable: name})
	}
	slices.SortFunc(result, func(left, right windowsARM64ProcessARM64GoplsProcessReceipt) int {
		return left.PID - right.PID
	})
	return result
}

func windowsARM64ProcessARM64GoplsWriteEvidence(repoRoot string, receipt windowsARM64ProcessARM64GoplsReceipt) error {
	directory := filepath.Join(repoRoot, filepath.FromSlash(windowsARM64ProcessARM64GoplsEvidenceDir))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-receipt.json", windowsARM64ProcessARM64GoplsReceiptPrefix, receipt.LanguageID)
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, name), append(payload, '\n'), 0o600); err != nil {
		return err
	}
	wireName := fmt.Sprintf("%s-%s-wire.jsonl", windowsARM64ProcessARM64GoplsReceiptPrefix, receipt.LanguageID)
	file, err := os.OpenFile(filepath.Join(directory, wireName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(map[string]any{"event": "summary", "language_id": receipt.LanguageID, "status": receipt.Status, "action_total": receipt.ActionTotal, "semantic_success": receipt.SemanticSuccess, "legal_empty": receipt.LegalEmpty, "capability_unsupported": receipt.CapabilityUnsupported, "null_result": receipt.NullResult, "runtime_errors": receipt.RuntimeErrors, "post_idle_total": receipt.PostIdleTotal, "post_idle_action_complete": receipt.PostIdleActionComplete, "post_idle_semantic_success": receipt.PostIdleSemanticSuccess, "post_idle_legal_empty": receipt.PostIdleLegalEmpty, "post_idle_capability_unsupported": receipt.PostIdleCapabilityUnsupported, "post_idle_null_result": receipt.PostIdleNullResult, "post_idle_runtime_errors": receipt.PostIdleRuntimeErrors, "post_idle_non_empty": receipt.PostIdleNonEmpty, "shutdown_response": receipt.ShutdownResponse, "exit_sent": receipt.ExitSent, "zero_residual": receipt.ZeroResidual}); err != nil {
		return err
	}
	for _, action := range receipt.Actions {
		if err := encoder.Encode(map[string]any{"event": "action", "tool": action.Tool, "action": action.Action, "status": action.Status, "arguments": action.Arguments, "content_bytes": action.ContentBytes, "content_sha256": action.ContentSHA256}); err != nil {
			return err
		}
	}
	return nil
}

func windowsARM64ProcessARM64GoplsPrecheck(t *testing.T, repoRoot string) {
	t.Helper()
	server := realNodeServerCase{name: "go", languageID: "go", fileName: "main.go", line: 8, character: 1}
	fixtureRoot := t.TempDir()
	_, fixture := windowsARM64ProcessARM64GoplsWriteFixture(t, fixtureRoot, server.languageID)
	actions := windowsARM64ProcessARM64GoplsActions(server, fixture)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("gopls precheck canonical 36-action closure: %v", err)
	}
	receipt := windowsARM64ProcessARM64GoplsReceipt{Test: windowsARM64ProcessARM64GoplsReceiptPrefix, LanguageID: "precheck", Status: "NON_PASS_precheck", Precheck: true, ManagerIdleTimeout: windowsARM64ProcessARM64GoplsManagerIdle.String(), ProofIdleDuration: windowsARM64ProcessARM64GoplsProofIdle.String(), ProductionMinimumIdle: windowsARM64ProcessARM64GoplsProductionMinIdle.String(), ExpectedActionTotal: realMCPExpectedActionCount, ActionTotal: 0, WirePath: filepath.ToSlash(filepath.Join(windowsARM64ProcessARM64GoplsEvidenceDir, windowsARM64ProcessARM64GoplsReceiptPrefix+"-precheck-wire.jsonl"))}
	if err := windowsARM64ProcessARM64GoplsWriteEvidence(repoRoot, receipt); err != nil {
		t.Fatalf("write gopls bounded precheck receipt: %v", err)
	}
	t.Skipf("NON_PASS bounded structure precheck (max=%s); it does not install, start, call actions, or prove lifecycle", windowsARM64ProcessARM64GoplsPrecheckMax)
}

// TestWindowsARM64ProcessARM64GoplsCleanupFailureFailsTestContract 锁定正式长测的
// 文件系统清理失败必须同时降级 receipt、清空 zero-residual 并让 go test 失败；
// 禁止恢复为只写 NON_PASS receipt 却返回测试成功的旧行为。
func TestWindowsARM64ProcessARM64GoplsCleanupFailureFailsTestContract(t *testing.T) {
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve gopls Windows ARM64 E2E source path")
	}
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read gopls Windows ARM64 E2E source: %v", err)
	}
	formalStart := bytes.Index(payload, []byte("func TestWindowsARM64ProcessARM64Gopls4x36SoakE2E"))
	if formalStart < 0 {
		t.Fatal("formal gopls Windows ARM64 E2E function is missing")
	}
	formalSource := string(payload[formalStart:])
	for _, required := range []string{
		"cleanupErrors := make([]error, 0, 2)",
		"installer.RemoveWindowsInstallerTreeChecked(os.TempDir(), fixtureRoot)",
		"zeroResidual = false",
		"t.Errorf(\"gopls Windows ARM64 lifecycle cleanup failed: %v\", cleanupErr)",
	} {
		if !strings.Contains(formalSource, required) {
			t.Fatalf("formal gopls Windows ARM64 E2E cleanup contract missing %q", required)
		}
	}
}

// TestWindowsARM64ProcessARM64Gopls4x36SoakE2E 走 production installer/resolver 和真实
// mcp-lsp stdio，逐一证明 go/gomod/gosum/gowork 的 36-action 闭包与 15 分钟生命周期。
func TestWindowsARM64ProcessARM64Gopls4x36SoakE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64Gopls4x36E2EEnv) != "1" {
		t.Skipf("set %s=1 to enable the Windows ARM64/process ARM64 real gopls E2E", windowsARM64ProcessARM64Gopls4x36E2EEnv)
	}
	if value, exists := os.LookupEnv("GOWORK"); exists {
		t.Fatalf("gopls formal proof requires GOWORK to be unset, got %q", value)
	}
	if os.Getenv(windowsARM64ProcessARM64Gopls4x36PrecheckEnv) == "1" {
		windowsARM64ProcessARM64GoplsPrecheck(t, realNodeRepoRoot(t))
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("gopls formal proof requires windows/arm64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("gopls formal proof requires NativeArch=ProcessArch=arm64, got native=%q process=%q", host.NativeArch, host.ProcessArch)
	}
	if err := installer.ValidateWindowsRuntimeDependencyCatalog(); err != nil {
		t.Fatalf("validate locked Windows runtime dependency catalog: %v", err)
	}
	repoRoot := realNodeRepoRoot(t)
	goBin := windowsARM64ProcessARM64GoplsLockedGo(t, repoRoot)
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", windowsARM64ProcessARM64GoplsManagerIdle.String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	windowsARM64ProcessARM64GoplsRunCohort(t, ctx, repoRoot, host, goBin)
}

func windowsARM64ProcessARM64GoplsAddHTTP(left, right windowsARM64ProcessARM64GoplsHTTPReceipt) windowsARM64ProcessARM64GoplsHTTPReceipt {
	return windowsARM64ProcessARM64GoplsHTTPReceipt{
		Requests: left.Requests + right.Requests, Attempts: left.Attempts + right.Attempts,
		Responses: left.Responses + right.Responses, RedirectResponses: left.RedirectResponses + right.RedirectResponses,
		TransportErrors: left.TransportErrors + right.TransportErrors,
	}
}

func windowsARM64ProcessARM64GoplsFindServerIdentityForLanguage(tracked map[realMCPProcessKey]realMCPProcessIdentity, serverPath, languageID string) (realMCPProcessIdentity, error) {
	want := filepath.Clean(serverPath)
	languagePrefix := "gopls-" + strings.ToLower(strings.TrimSpace(languageID)) + "-"
	if strings.Trim(languagePrefix, "-") == "gopls" {
		return realMCPProcessIdentity{}, fmt.Errorf("gopls language identity is empty")
	}
	for _, identity := range tracked {
		if !strings.Contains(strings.ToLower(identity.Name+" "+identity.CommandLine), "gopls") {
			continue
		}
		languageMatched := false
		for _, label := range strings.Split(identity.Language, ",") {
			label = strings.ToLower(strings.TrimSpace(label))
			if label == strings.TrimSuffix(languagePrefix, "-") || strings.HasPrefix(label, languagePrefix) {
				languageMatched = true
				break
			}
		}
		if !languageMatched {
			continue
		}
		executable := windowsARM64ProcessARM64GoplsSplitCommandLine(identity.CommandLine)
		if executable != "" && strings.EqualFold(filepath.Clean(executable), want) {
			return identity, nil
		}
	}
	return realMCPProcessIdentity{}, fmt.Errorf("tracked process tree has no resolver-owned gopls identity for language %q", languageID)
}

func windowsARM64ProcessARM64GoplsWaitCohortIdle(ctx context.Context, t *testing.T, mcpPID int, mcpStart string, gopls []realMCPProcessIdentity, duration time.Duration) int {
	t.Helper()
	if duration < windowsARM64ProcessARM64GoplsProductionMinIdle {
		t.Fatalf("formal Go gopls cohort idle duration=%s is below production minimum=%s", duration, windowsARM64ProcessARM64GoplsProductionMinIdle)
	}
	unique := make(map[realMCPProcessKey]realMCPProcessIdentity, len(gopls))
	for _, identity := range gopls {
		unique[realMCPProcessKey{PID: identity.PID, StartToken: identity.StartToken}] = identity
	}
	if len(unique) == 0 {
		t.Fatalf("gopls cohort has no tracked server identity before idle proof")
	}
	started := time.Now()
	heartbeats := 0
	sample := func() {
		if err := windowsARM64ProcessARM64GoplsRequireIdentity(mcpPID, mcpStart, "MCP cohort idle"); err != nil {
			t.Fatalf("MCP identity changed during gopls cohort idle: %v", err)
		}
		for _, identity := range unique {
			if err := windowsARM64ProcessARM64GoplsRequireIdentity(identity.PID, identity.StartToken, "gopls cohort idle"); err != nil {
				t.Fatalf("gopls identity changed during cohort idle: %v", err)
			}
		}
		heartbeats++
		t.Logf("Windows ARM64/process ARM64 gopls cohort idle heartbeat elapsed=%s mcp_pid=%d gopls_identities=%d", time.Since(started).Round(time.Second), mcpPID, len(unique))
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
			t.Fatalf("gopls cohort idle sampling stopped before %s: %v", duration, ctx.Err())
		case <-timer.C:
		}
		sample()
	}
}

func windowsARM64ProcessARM64GoplsRunActionMatrix(t *testing.T, client *mcpLSPBinaryClient, languageID, workDir string, server realNodeServerCase, fixture realMCPFixture, receipt *windowsARM64ProcessARM64GoplsReceipt, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, mcpStart string) {
	t.Helper()
	actions := windowsARM64ProcessARM64GoplsActions(server, fixture)
	if err := validateRealMCPActionClosure(actions); err != nil {
		windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "action_closure", err)
		t.Fatalf("gopls %s canonical 36-action closure: %v", languageID, err)
	}
	receipt.Phase = "actions"
	for _, action := range actions {
		action := action
		key := action.tool + "/" + action.name
		actionLanguageID := windowsARM64ProcessARM64GoplsActionLanguageID(languageID, action)
		args := realMCPWindowsToolArguments(actionLanguageID, workDir, action.tool, action.name, action.args)
		record := windowsARM64ProcessARM64GoplsActionReceipt{Tool: action.tool, Action: action.name, Status: "runtime_error", Arguments: windowsARM64ProcessARM64GoplsReceiptArgs(args)}
		var response mcpLSPBinaryResponse
		var status realMCPActionStatus
		nullResult := false
		ok := t.Run(languageID+"/action/"+key, func(t *testing.T) {
			t.Cleanup(func() {
				if !t.Failed() {
					return
				}
				stderr := client.stderrString()
				digest := sha256.Sum256([]byte(stderr))
				const maxTailBytes = 64 * 1024
				tail := stderr
				if len(tail) > maxTailBytes {
					tail = tail[len(tail)-maxTailBytes:]
				}
				t.Logf("gopls %s %s failure stderr_bytes=%d stderr_sha256=%s stderr_tail=%q", languageID, key, len(stderr), hex.EncodeToString(digest[:]), tail)
			})
			response = client.callTool(t, action.tool, args)
			record.ContentBytes, record.ContentSHA256 = windowsARM64ProcessARM64GoplsContentSummary(response.Result.ContentText())
			status = requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "gopls "+languageID+" "+key)
			if windowsARM64ProcessARM64GoplsNullResult(response) {
				nullResult = true
				record.Status = "null_result"
				t.Fatalf("%s returned JSON null/empty payload; it is not legal_empty", key)
			}
			record.Status = string(status)
		})
		if !ok {
			if nullResult {
				receipt.NullResult++
			} else {
				receipt.RuntimeErrors++
			}
			receipt.Actions = append(receipt.Actions, record)
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "action/"+key, fmt.Errorf("action subtest failed"))
			t.Fatalf("gopls %s 36-action matrix failed at %s; no idle proof follows", languageID, key)
		}
		receipt.Actions = append(receipt.Actions, record)
		switch status {
		case realMCPActionSucceeded:
			receipt.SemanticSuccess++
		case realMCPActionLegalEmpty:
			receipt.LegalEmpty++
		case realMCPActionUnsupported:
			receipt.CapabilityUnsupported++
		default:
			receipt.RuntimeErrors++
		}
		if !trackRealMCPProcessTree(t, mcpPID, "gopls-"+languageID+"-"+key, tracked) {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "process_tree/"+key, fmt.Errorf("process tree capture failed"))
			t.Fatalf("capture gopls process tree after %s/%s failed", languageID, key)
		}
		if err := windowsARM64ProcessARM64GoplsRequireIdentity(mcpPID, mcpStart, "MCP after "+languageID+"/"+key); err != nil {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "mcp_identity/"+key, err)
			t.Fatalf("MCP identity after %s/%s: %v", languageID, key, err)
		}
	}
	receipt.ActionTotal = len(receipt.Actions)
	receipt.ActionLedgerComplete = receipt.ActionTotal == realMCPExpectedActionCount && receipt.SemanticSuccess+receipt.LegalEmpty+receipt.CapabilityUnsupported+receipt.NullResult+receipt.RuntimeErrors == receipt.ActionTotal
	if !receipt.ActionLedgerComplete || receipt.NullResult != 0 || receipt.RuntimeErrors != 0 {
		err := fmt.Errorf("total=%d success=%d legal_empty=%d unsupported=%d null=%d errors=%d", receipt.ActionTotal, receipt.SemanticSuccess, receipt.LegalEmpty, receipt.CapabilityUnsupported, receipt.NullResult, receipt.RuntimeErrors)
		windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "action_ledger", err)
		t.Fatalf("gopls %s action ledger is not a complete 36-action classification: %v", languageID, err)
	}
}

// windowsARM64ProcessARM64GoplsActionLanguageID 保持自然文件动作使用 gomod/gosum/gowork，
// 但 canonical patch_edit 中三个独立的 .go 辅助夹具必须交给 go manager。若把这些
// 文件伪装成自然文件 ID，open_file 与实际编辑同步会跨 client generation，产生陈旧文档回滚错误。
func windowsARM64ProcessARM64GoplsActionLanguageID(matrixLanguageID string, action realMCPActionSpec) string {
	if matrixLanguageID == "go" || action.tool != "patch_edit" {
		return matrixLanguageID
	}
	switch action.name {
	case "replace_range", "code_action", "format":
		return "go"
	default:
		return matrixLanguageID
	}
}

// windowsARM64ProcessARM64GoplsPatchSetupPath 返回与真实 patch_edit 参数相同的文档，
// 避免先打开自然目标文件、随后却编辑另一个辅助文件而制造无关的 manager 轮换。
func windowsARM64ProcessARM64GoplsPatchSetupPath(actionName string, fixture realMCPFixture) string {
	switch actionName {
	case "replace_range":
		return fixture.replaceFile
	case "code_action":
		return fixture.codeActionFile
	case "format":
		return fixture.formatFile
	default:
		return fixture.targetFile
	}
}

func windowsARM64ProcessARM64GoplsRunCohort(t *testing.T, ctx context.Context, repoRoot string, host installer.WindowsHostPlatform, compilerPath string) {
	t.Helper()
	startedAt := time.Now().UTC()
	receipts := make(map[string]*windowsARM64ProcessARM64GoplsReceipt, len(windowsARM64ProcessARM64GoplsLanguageIDs))
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		receipts[languageID] = &windowsARM64ProcessARM64GoplsReceipt{
			Test: windowsARM64ProcessARM64GoplsReceiptPrefix, LanguageID: languageID, Status: "running", StartedAt: startedAt.Format(time.RFC3339Nano),
			ManagerIdleTimeout: windowsARM64ProcessARM64GoplsManagerIdle.String(), ProofIdleDuration: windowsARM64ProcessARM64GoplsProofIdle.String(), ProductionMinimumIdle: windowsARM64ProcessARM64GoplsProductionMinIdle.String(),
			HostOS: host.OS, WindowsVersion: host.WindowsVersion, WindowsBuild: host.WindowsBuild, NativeArch: host.NativeArch, ProcessArch: host.ProcessArch, ProcessArchDiagnosticOnly: true,
			Product: string(installer.WindowsRuntimeDependencyProductGoGopls), GoVersion: windowsARM64ProcessARM64GoplsGoVersion, GoplsVersion: windowsARM64ProcessARM64GoplsVersion,
			GoSourceURL: windowsARM64ProcessARM64GoplsGoURL, GoSourceSHA256: windowsARM64ProcessARM64GoplsGoSHA256, GoplsSourceURL: windowsARM64ProcessARM64GoplsSourceURL, GoplsSourceSHA256: windowsARM64ProcessARM64GoplsSourceSHA256,
			ExpectedActionTotal: realMCPExpectedActionCount, WirePath: filepath.ToSlash(filepath.Join(windowsARM64ProcessARM64GoplsEvidenceDir, fmt.Sprintf("%s-%s-wire.jsonl", windowsARM64ProcessARM64GoplsReceiptPrefix, languageID))),
		}
	}
	productRoot, err := os.MkdirTemp("", "sd-gopls-production-windows-arm64-")
	if err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "product_root", err)
		}
		t.Fatalf("create empty gopls product root: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "product_root_acl", err)
		}
		cleanupErr := windowsARM64ProcessARM64GoplsCleanupSetupRoots(productRoot, "")
		if cleanupErr != nil {
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "product_root_cleanup", cleanupErr)
			}
		}
		t.Fatalf("restrict empty gopls product root: %v", errors.Join(err, cleanupErr))
	}
	fixtureRoot, err := os.MkdirTemp("", "sd-gopls-fixture-windows-arm64-")
	if err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "fixture_root", err)
		}
		cleanupErr := windowsARM64ProcessARM64GoplsCleanupSetupRoots(productRoot, "")
		if cleanupErr != nil {
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "product_root_cleanup", cleanupErr)
			}
		}
		t.Fatalf("create gopls cohort fixture root: %v", errors.Join(err, cleanupErr))
	}
	if err := securefs.RestrictPrivateOwnerOnly(fixtureRoot, 0o700); err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "fixture_root_acl", err)
		}
		cleanupErr := windowsARM64ProcessARM64GoplsCleanupSetupRoots(productRoot, fixtureRoot)
		if cleanupErr != nil {
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "setup_root_cleanup", cleanupErr)
			}
		}
		t.Fatalf("restrict gopls cohort fixture root: %v", errors.Join(err, cleanupErr))
	}
	tracked := make(map[realMCPProcessKey]realMCPProcessIdentity)
	var client *mcpLSPBinaryClient
	var mcpPID int
	var mcpStartToken string
	shutdownResponse, exitSent := false, false
	defer func() {
		if client != nil && client.cmd != nil {
			if !shutdownResponse {
				if _, shutdownErr := windowsARM64ProcessARM64GoplsProtocol(client, "shutdown", map[string]any{}); shutdownErr == nil {
					shutdownResponse = true
				} else {
					for _, receipt := range receipts {
						windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "shutdown_recovery", shutdownErr)
					}
				}
			}
			exitSent = windowsARM64ProcessARM64GoplsCloseClient(t, client)
		}
		cleanupErrors := make([]error, 0, 2)
		if err := windowsARM64ProcessARM64GoplsCleanupTestOwnedDurableProcesses(productRoot, tracked); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("terminate exact test-owned durable process: %w", err))
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "durable_process_cleanup", err)
			}
		}
		zeroResidual := exitSent && len(cleanupErrors) == 0 && windowsARM64ProcessARM64GoplsWaitTrackedGone(tracked, 30*time.Second)
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove product root: %w", err))
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "product_root_cleanup", err)
			}
		}
		if err := installer.RemoveWindowsInstallerTreeChecked(os.TempDir(), fixtureRoot); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove fixture root: %w", err))
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "fixture_root_cleanup", err)
			}
		} else if _, statErr := os.Stat(fixtureRoot); !errors.Is(statErr, os.ErrNotExist) {
			if statErr == nil {
				statErr = errors.New("fixture root still exists after cleanup")
			}
			cleanupErrors = append(cleanupErrors, fmt.Errorf("verify fixture root removal: %w", statErr))
			for _, receipt := range receipts {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "fixture_root_residual", statErr)
			}
		}
		if len(cleanupErrors) > 0 {
			zeroResidual = false
		}
		for languageID, receipt := range receipts {
			receipt.MCPPID, receipt.MCPStartToken = mcpPID, mcpStartToken
			receipt.MCPIdentityStable = receipt.MCPIdentityStable && mcpPID > 0 && mcpStartToken != ""
			receipt.ProcessIdentities = windowsARM64ProcessARM64GoplsSanitizeIdentities(tracked)
			receipt.ShutdownResponse, receipt.ExitSent, receipt.ZeroResidual = shutdownResponse, exitSent, zeroResidual
			if !zeroResidual && len(cleanupErrors) == 0 {
				windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "zero_residual", fmt.Errorf("tracked process identity remains"))
			}
			receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if receipt.Status == "running" {
				postIdlePass := windowsARM64ProcessARM64GoplsPostIdlePass(languageID, windowsARM64ProcessARM64GoplsPostIdleClassification{
					Total: receipt.PostIdleTotal, SemanticSuccess: receipt.PostIdleSemanticSuccess, LegalEmpty: receipt.PostIdleLegalEmpty,
					CapabilityUnsupported: receipt.PostIdleCapabilityUnsupported, NullResult: receipt.PostIdleNullResult,
					RuntimeErrors: receipt.PostIdleRuntimeErrors, ActionComplete: receipt.PostIdleActionComplete, NonEmpty: receipt.PostIdleNonEmpty,
				}, receipt.GoplsIdentityStable)
				if receipt.ActionLedgerComplete && receipt.NullResult == 0 && receipt.RuntimeErrors == 0 && receipt.MCPIdentityStable && postIdlePass && receipt.ShutdownResponse && receipt.ExitSent && receipt.ZeroResidual {
					receipt.Status = "pass"
				} else {
					receipt.Status = "non_pass"
				}
			}
			if err := windowsARM64ProcessARM64GoplsWriteEvidence(repoRoot, *receipt); err != nil {
				t.Errorf("write gopls %s receipt/wire: %v", languageID, err)
			}
		}
		if cleanupErr := errors.Join(cleanupErrors...); cleanupErr != nil {
			t.Errorf("gopls Windows ARM64 lifecycle cleanup failed: %v", cleanupErr)
		}
	}()

	cacheRoot := windowsRuntimeDependencyCacheRoot(productRoot)
	beforeEntries, beforeEmpty, err := windowsARM64ProcessARM64GoplsCacheEntries(cacheRoot)
	if err != nil || !beforeEmpty {
		if err == nil {
			err = fmt.Errorf("product root cache is not empty: entries=%d", beforeEntries)
		}
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "cache_before", err)
		}
		t.Fatalf("gopls formal proof requires empty private product cache: %v", err)
	}
	for _, receipt := range receipts {
		receipt.CacheBeforeEntries, receipt.CacheBeforeEmpty = beforeEntries, beforeEmpty
	}
	plan := windowsARM64ProcessARM64GoplsLockedPlan(t, host.NativeArch)
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	provider := setupInstaller()
	var httpTotal windowsARM64ProcessARM64GoplsHTTPReceipt
	var serverPath string
	var productGoPath string
	for index, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		receipt := receipts[languageID]
		result, httpReceipt, ensureErr := windowsARM64ProcessARM64GoplsEnsureObserved(ctx, provider, languageID)
		httpTotal = windowsARM64ProcessARM64GoplsAddHTTP(httpTotal, httpReceipt)
		receipt.HTTP = httpTotal
		if ensureErr != nil {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "ensure_installed", ensureErr)
			t.Fatalf("production EnsureInstalledDetailed(%s) failed: %v", languageID, ensureErr)
		}
		expectedStatus := installer.InstallStatusPathFound
		if index == 0 {
			expectedStatus = installer.InstallStatusInstalledPath
		}
		if result.Status != expectedStatus {
			err = fmt.Errorf("status=%s want=%s", result.Status, expectedStatus)
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "ensure_installed_status", err)
			t.Fatalf("production gopls install used unexpected status=%s want=%s", result.Status, expectedStatus)
		}
		resolved, resolveErr := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductGoGopls, cacheRoot)
		if resolveErr != nil {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "resolver", resolveErr)
			t.Fatalf("resolve ready go-gopls cohort: %v", resolveErr)
		}
		if serverPath == "" {
			serverPath = result.Path
		}
		if productGoPath == "" {
			productGoPath = resolved.ExecutablePath
		}
		if filepath.Clean(resolved.ExecutablePath) != filepath.Clean(productGoPath) {
			err = fmt.Errorf("resolved product Go executable changed across language IDs")
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "go_resolver_identity", err)
			t.Fatalf("production Go resolver identity changed for %s", languageID)
		}
		if filepath.Clean(result.Path) != filepath.Clean(resolved.ServerPath) || filepath.Clean(result.Path) != filepath.Clean(serverPath) {
			err = fmt.Errorf("EnsureInstalled path/resolver identity differs")
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "resolver_identity", err)
			t.Fatalf("production resolver identity mismatch for %s", languageID)
		}
		if err := windowsARM64ProcessARM64GoplsValidatePE(result.Path, installer.WindowsImageFileMachineARM64); err != nil {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "gopls_pe", err)
			t.Fatalf("installed gopls is not ARM64 PE: %v", err)
		}
		relative, relErr := filepath.Rel(productRoot, result.Path)
		if relErr != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			err = fmt.Errorf("server escaped product root")
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "resolver_root", err)
			t.Fatalf("gopls server escaped product root")
		}
		receipt.ServerPathRelative, receipt.Cohort, receipt.InstallStatus = filepath.ToSlash(relative), resolved.Cohort, string(result.Status)
	}
	afterEntries, afterEmpty, err := windowsARM64ProcessARM64GoplsCacheEntries(cacheRoot)
	if err != nil || afterEmpty {
		if err == nil {
			err = fmt.Errorf("ready cache remains empty")
		}
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "cache_after", err)
		}
		t.Fatalf("inspect ready go-gopls cache: %v", err)
	}
	for _, receipt := range receipts {
		receipt.CacheAfterEntries, receipt.CacheReadyAfterInstall, receipt.HTTP = afterEntries, !afterEmpty, httpTotal
	}
	productGoRelative := windowsARM64ProcessARM64GoplsValidateProductGo(t, productRoot, productGoPath)
	for _, receipt := range receipts {
		receipt.GoExecutableRelative = productGoRelative
	}
	if len(plan.AssetsByArchitecture[host.NativeArch]) == 0 || httpTotal.Attempts == 0 {
		t.Fatalf("empty-cache install did not record real download attempt: assets=%d http_attempts=%d", len(plan.AssetsByArchitecture[host.NativeArch]), httpTotal.Attempts)
	}

	servers := make(map[string]realNodeServerCase, len(receipts))
	fixtures := make(map[string]realMCPFixture, len(receipts))
	workDirs := make(map[string]string, len(receipts))
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		workDir := filepath.Join(fixtureRoot, languageID)
		workDirs[languageID] = workDir
		server, fixture := windowsARM64ProcessARM64GoplsWriteFixture(t, workDir, languageID)
		servers[languageID], fixtures[languageID] = server, fixture
	}
	oldCompilerEnv, hadCompilerEnv := os.LookupEnv("SUPER_DOLPHIN_GO_BIN")
	if err := os.Setenv("SUPER_DOLPHIN_GO_BIN", compilerPath); err != nil {
		t.Fatalf("set locked compiler environment for mcp-lsp build: %v", err)
	}
	t.Cleanup(func() {
		if hadCompilerEnv {
			_ = os.Setenv("SUPER_DOLPHIN_GO_BIN", oldCompilerEnv)
		} else {
			_ = os.Unsetenv("SUPER_DOLPHIN_GO_BIN")
		}
	})
	builtBinary := buildRealMcpLSPBinary(t, repoRoot)
	// Windows gopls 信任链以真实 mcp-lsp self 路径绑定同一产品根下的 bin 与 lsp；
	// 正式 E2E 必须复刻交付布局，不能从测试临时目录启动后绕过生产校验。
	packageBinDir := filepath.Join(productRoot, "bin")
	if err := os.MkdirAll(packageBinDir, 0o700); err != nil {
		t.Fatalf("create packaged mcp-lsp bin directory: %v", err)
	}
	binary := filepath.Join(packageBinDir, "mcp-lsp.exe")
	binaryPayload, err := os.ReadFile(builtBinary)
	if err != nil {
		t.Fatalf("read built mcp-lsp binary: %v", err)
	}
	if err := os.WriteFile(binary, binaryPayload, 0o700); err != nil {
		t.Fatalf("install mcp-lsp into packaged Windows bin directory: %v", err)
	}
	bundleRoot, manifestPath, bundledGopls := windowsARM64ProcessARM64GoplsInstallPackagedBundle(t, productRoot, serverPath)
	serverPath = bundledGopls
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", bundleRoot)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", manifestPath)
	if err := os.Unsetenv("SUPER_DOLPHIN_GO_BIN"); err != nil {
		t.Fatalf("remove compiler-only environment before production MCP start: %v", err)
	}
	productGoBin := filepath.Dir(productGoPath)
	productGoRoot := filepath.Dir(productGoBin)
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		t.Fatalf("production child Go environment requires inherited PATH for non-Go tools")
	}
	t.Setenv("PATH", productGoBin+string(os.PathListSeparator)+pathValue)
	t.Setenv("GOROOT", productGoRoot)
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOMODCACHE", filepath.Join(productRoot, ".gomodcache"))
	t.Setenv("GOCACHE", filepath.Join(productRoot, ".gocache"))
	client = startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID = client.cmd.Process.Pid
	mcpStartToken, err = windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "mcp_identity", err)
		}
		t.Fatalf("capture mcp-lsp PID/start identity: %v", err)
	}
	tracked[realMCPProcessKey{PID: mcpPID, StartToken: mcpStartToken}] = realMCPProcessIdentity{PID: mcpPID, StartToken: mcpStartToken, Name: "mcp-lsp", Language: "gopls-cohort"}
	for _, receipt := range receipts {
		receipt.MCPPID, receipt.MCPStartToken = mcpPID, mcpStartToken
		receipt.MCPIdentityStable = true
	}
	if _, err := windowsARM64ProcessARM64GoplsProtocol(client, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "super-dolphin-gopls-windows-arm64-e2e", "version": "1"}}); err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "initialize", err)
		}
		t.Fatalf("gopls MCP initialize failed: %v", err)
	}
	if err := windowsARM64ProcessARM64GoplsNotify(client, "notifications/initialized", map[string]any{}); err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "initialized", err)
		}
		t.Fatalf("gopls MCP initialized notification failed: %v", err)
	}
	toolsPayload, err := windowsARM64ProcessARM64GoplsProtocol(client, "tools/list", map[string]any{})
	if err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "tools_list", err)
		}
		t.Fatalf("gopls MCP tools/list failed: %v", err)
	}
	requireRealMCPToolFamilies(t, toolsPayload)
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		if !t.Run("actions/"+languageID, func(t *testing.T) {
			windowsARM64ProcessARM64GoplsRunActionMatrix(t, client, languageID, workDirs[languageID], servers[languageID], fixtures[languageID], receipts[languageID], tracked, mcpPID, mcpStartToken)
		}) {
			t.Fatalf("gopls action matrix failed for %s", languageID)
		}
	}
	serverIdentities := make(map[string]realMCPProcessIdentity, len(receipts))
	uniqueIdentities := make(map[realMCPProcessKey]realMCPProcessIdentity)
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		identity, findErr := windowsARM64ProcessARM64GoplsFindServerIdentityForLanguage(tracked, serverPath, languageID)
		if findErr != nil {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipts[languageID], "gopls_identity", findErr)
			t.Fatalf("capture resolver-owned gopls identity for %s: %v", languageID, findErr)
		}
		serverIdentities[languageID] = identity
		uniqueIdentities[realMCPProcessKey{PID: identity.PID, StartToken: identity.StartToken}] = identity
		receipts[languageID].GoplsPID, receipts[languageID].GoplsStartToken, receipts[languageID].GoplsIdentityStable = identity.PID, identity.StartToken, true
	}
	identities := make([]realMCPProcessIdentity, 0, len(uniqueIdentities))
	for _, identity := range uniqueIdentities {
		identities = append(identities, identity)
	}
	for _, receipt := range receipts {
		receipt.Phase = "idle"
	}
	heartbeats := windowsARM64ProcessARM64GoplsWaitCohortIdle(ctx, t, mcpPID, mcpStartToken, identities, windowsARM64ProcessARM64GoplsProofIdle)
	for _, receipt := range receipts {
		receipt.IdleDuration, receipt.IdleHeartbeats = windowsARM64ProcessARM64GoplsProofIdle.String(), heartbeats
		receipt.Phase = "post_idle"
	}
	for _, languageID := range windowsARM64ProcessARM64GoplsLanguageIDs {
		receipt := receipts[languageID]
		receipt.PostIdleActionComplete = true
		receipt.PostIdleNonEmpty = true
		for _, post := range []struct {
			tool, action string
			args         map[string]any
		}{
			{tool: "inspect", action: "hover", args: map[string]any{"action": "hover", "pos": fixtures[languageID].semanticPosition}},
			{tool: "inspect", action: "definition", args: map[string]any{"action": "definition", "pos": fixtures[languageID].semanticPosition}},
			{tool: "xref", action: "references", args: map[string]any{"action": "references", "pos": fixtures[languageID].semanticPosition, "include_declaration": true, "max_results": 20}},
		} {
			receipt.PostIdleTotal++
			// 保持与 36-action 阶段相同的“gopls <language>”前缀；自然文件能力分类器
			// 依赖该精确语言标签，post-idle 只能追加阶段说明，不能改变合同身份。
			postLabel := "gopls " + languageID + " post-idle " + post.tool + "/" + post.action
			requireResult, emptyReason, allowUnsupported := windowsARM64ProcessARM64GoplsActionContract(languageID, post.tool, post.action)
			response := client.callTool(t, post.tool, realMCPWindowsToolArguments(languageID, workDirs[languageID], post.tool, post.action, post.args))
			status := requireRealMCPActionResult(t, response, requireResult, emptyReason, allowUnsupported, realMCPActionCapabilityKey(post.tool, post.action), realMCPActionProtocolOptional(post.tool, post.action), postLabel)
			if windowsARM64ProcessARM64GoplsNullResult(response) {
				receipt.PostIdleNullResult++
				receipt.NullResult++
				receipt.PostIdleActionComplete = false
			}
			switch status {
			case realMCPActionSucceeded:
				if realMCPActionSemanticContentNonEmpty(t, response, postLabel) {
					receipt.PostIdleSemanticSuccess++
				} else {
					receipt.PostIdleActionComplete = false
				}
			case realMCPActionLegalEmpty:
				receipt.PostIdleLegalEmpty++
			case realMCPActionUnsupported:
				receipt.PostIdleCapabilityUnsupported++
			default:
				receipt.PostIdleRuntimeErrors++
				receipt.RuntimeErrors++
				receipt.PostIdleActionComplete = false
			}
			if status != realMCPActionSucceeded || !realMCPActionSemanticContentNonEmpty(t, response, postLabel) {
				receipt.PostIdleNonEmpty = false
				t.Logf("gopls post-idle %s/%s for %s classified status=%s; it is not semantic non-empty success", post.tool, post.action, languageID, status)
			}
		}
		identity := serverIdentities[languageID]
		if err := windowsARM64ProcessARM64GoplsRequireIdentity(mcpPID, mcpStartToken, "MCP after post-idle "+languageID); err != nil {
			receipt.MCPIdentityStable = false
			receipt.PostIdleActionComplete = false
			t.Fatalf("MCP identity after post-idle %s: %v", languageID, err)
		}
		if err := windowsARM64ProcessARM64GoplsRequireIdentity(identity.PID, identity.StartToken, "gopls after post-idle "+languageID); err != nil {
			receipt.GoplsIdentityStable = false
			receipt.PostIdleActionComplete = false
			t.Fatalf("gopls identity after post-idle %s: %v", languageID, err)
		}
	}
	for _, receipt := range receipts {
		receipt.Phase = "shutdown"
	}
	if _, err := windowsARM64ProcessARM64GoplsProtocol(client, "shutdown", map[string]any{}); err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "shutdown", err)
		}
		t.Fatalf("gopls shutdown response failed: %v", err)
	}
	shutdownResponse = true
	for _, receipt := range receipts {
		receipt.ShutdownResponse = true
		receipt.Phase = "exit"
	}
	exitSent = windowsARM64ProcessARM64GoplsCloseClient(t, client)
	for _, receipt := range receipts {
		receipt.ExitSent = exitSent
	}
	if !exitSent {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "exit", fmt.Errorf("exit notification/process wait failed"))
		}
		t.Fatalf("gopls exit did not complete cleanly")
	}
	if err := windowsARM64ProcessARM64GoplsCleanupTestOwnedDurableProcesses(productRoot, tracked); err != nil {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "durable_process_cleanup", err)
		}
		t.Fatalf("cleanup exact test-owned gopls durable process: %v", err)
	}
	if !windowsARM64ProcessARM64GoplsWaitTrackedGone(tracked, 30*time.Second) {
		for _, receipt := range receipts {
			windowsARM64ProcessARM64GoplsReceiptFailure(receipt, "zero_residual", fmt.Errorf("MCP or gopls PID/start identity remains"))
		}
		t.Fatalf("gopls cohort process tree has residual PID/start identity")
	}
	for _, receipt := range receipts {
		receipt.ZeroResidual = true
		receipt.Phase = "complete"
	}
}

// windowsARM64ProcessARM64GoplsCleanupSetupRoots 统一处理正式长测进入主生命周期前的失败：
// 产品根和 fixture 根都必须走受控 Windows 删除器，且清理失败必须并入原始错误，不能吞掉。
func windowsARM64ProcessARM64GoplsCleanupSetupRoots(productRoot, fixtureRoot string) error {
	cleanupErrors := make([]error, 0, 2)
	if strings.TrimSpace(productRoot) != "" {
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove gopls setup product root: %w", err))
		}
	}
	if strings.TrimSpace(fixtureRoot) != "" {
		if err := installer.RemoveWindowsInstallerTreeChecked(os.TempDir(), fixtureRoot); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove gopls setup fixture root: %w", err))
		}
	}
	return errors.Join(cleanupErrors...)
}
