//go:build windows && arm64 && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	windowsARM64ProcessARM64GoSQLS36ActionE2EEnv            = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_GOSQLS_36_E2E"
	windowsARM64ProcessARM64GoSQLS36ActionPrecheckEnv       = "SUPER_DOLPHIN_RUN_WINDOWS_ARM64_GOSQLS_36_PRECHECK"
	windowsARM64ProcessARM64GoSQLSProductRootEnv            = "SUPER_DOLPHIN_WINDOWS_ARM64_GOSQLS_PRODUCT_ROOT"
	windowsARM64ProcessARM64GoSQLS36ActionEvidenceDir       = ".build-cache/codex-go-sqls-windows-proof"
	windowsARM64ProcessARM64GoSQLS36ActionReceiptName       = "windows-arm64-process-arm64-go-sqls-36-action-receipt.json"
	windowsARM64ProcessARM64GoSQLS36ActionWireName          = "windows-arm64-process-arm64-go-sqls-36-action-wire.jsonl"
	windowsARM64ProcessARM64GoSQLS36ActionProofIdle         = 15 * time.Minute
	windowsARM64ProcessARM64GoSQLS36ActionManagerIdle       = 17 * time.Minute
	windowsARM64ProcessARM64GoSQLS36ActionPrecheckIdle      = 30 * time.Second
	windowsARM64ProcessARM64GoSQLS36ActionProductionMinIdle = 15 * time.Minute
)

type windowsARM64ProcessARM64GoSQLS36ActionRecord struct {
	Tool      string         `json:"tool"`
	Action    string         `json:"action"`
	Status    string         `json:"status"`
	Error     string         `json:"error,omitempty"`
	IsError   bool           `json:"is_error"`
	Arguments map[string]any `json:"arguments"`
	Content   string         `json:"content,omitempty"`
}

type windowsARM64ProcessARM64GoSQLS36ActionReceipt struct {
	Test                      string                                         `json:"test"`
	Phase                     string                                         `json:"phase"`
	Status                    string                                         `json:"status"`
	FailurePhase              string                                         `json:"failure_phase,omitempty"`
	FailureDigest             string                                         `json:"failure_digest,omitempty"`
	StartedAt                 string                                         `json:"started_at"`
	FinishedAt                string                                         `json:"finished_at"`
	Precheck                  bool                                           `json:"precheck"`
	ManagerIdle               string                                         `json:"manager_idle_timeout"`
	ProofIdle                 string                                         `json:"proof_idle_duration"`
	ProductionMinIdle         string                                         `json:"production_idle_minimum"`
	IdleDuration              string                                         `json:"idle_duration,omitempty"`
	IdleHeartbeats            int                                            `json:"idle_heartbeats"`
	PostIdleAction            string                                         `json:"post_idle_action,omitempty"`
	PostIdleStatus            string                                         `json:"post_idle_status,omitempty"`
	PostIdleNonEmpty          bool                                           `json:"post_idle_non_empty"`
	MCPIdentityStable         bool                                           `json:"mcp_identity_stable"`
	ServerIdentityStable      bool                                           `json:"server_identity_stable"`
	ShutdownResponse          bool                                           `json:"shutdown_response"`
	ExitProcessWait           bool                                           `json:"exit_process_wait"`
	ActionLedgerComplete      bool                                           `json:"action_ledger_complete"`
	ProcessArchDiagnosticOnly bool                                           `json:"process_arch_diagnostic_only"`
	AssetPolicy               string                                         `json:"asset_policy"`
	CachePolicy               string                                         `json:"cache_policy"`
	HTTPPolicy                string                                         `json:"http_policy"`
	ACLPolicy                 string                                         `json:"acl_policy"`
	WirePath                  string                                         `json:"wire_path"`
	HostOS                    string                                         `json:"host_os"`
	NativeArch                string                                         `json:"native_arch"`
	ProcessArch               string                                         `json:"process_arch"`
	WindowsVersion            string                                         `json:"windows_version"`
	WindowsBuild              uint32                                         `json:"windows_build"`
	Product                   string                                         `json:"product"`
	Version                   string                                         `json:"version"`
	SourceURL                 string                                         `json:"source_url"`
	SourceSHA256              string                                         `json:"source_sha256"`
	GoVersion                 string                                         `json:"go_version"`
	Cohort                    string                                         `json:"cohort"`
	ServerPath                string                                         `json:"server_path"`
	ServerPID                 int                                            `json:"server_pid"`
	ServerStartToken          string                                         `json:"server_start_token"`
	MCPPID                    int                                            `json:"mcp_pid"`
	MCPStartToken             string                                         `json:"mcp_start_token"`
	ActionTotal               int                                            `json:"action_total"`
	ExpectedActionTotal       int                                            `json:"expected_action_total"`
	Callable                  int                                            `json:"callable"`
	LegalEmpty                int                                            `json:"legal_empty"`
	Unsupported               int                                            `json:"capability_unsupported"`
	Null                      int                                            `json:"null_result"`
	Errors                    int                                            `json:"error"`
	ShutdownSent              bool                                           `json:"shutdown_sent"`
	ExitSent                  bool                                           `json:"exit_sent"`
	ZeroResidual              bool                                           `json:"zero_residual"`
	ProcessIdentities         []realMCPProcessIdentity                       `json:"process_identities,omitempty"`
	Actions                   []windowsARM64ProcessARM64GoSQLS36ActionRecord `json:"actions"`
}

// receiptActionArguments 只保留不含工作区路径、位置或补丁正文的动作字段；原始
// MCP 参数仍由本地 wire/log 证据保存，交付 receipt 不得泄露机器绝对路径。
func receiptActionArguments(args map[string]any) map[string]any {
	allowed := []string{
		"action", "language_id", "limit", "scope", "query", "regex", "case_sensitive",
		"max_results", "include_declaration", "direction", "language", "new_name", "only",
		"workspace_language", "file_path",
	}
	result := make(map[string]any, len(allowed))
	for _, key := range allowed {
		if value, ok := args[key]; ok {
			result[key] = value
		}
	}
	return result
}

// receiptTextSummary 以大小和摘要替代 MCP content 正文，避免错误信息或路径从 receipt 外泄。
func receiptTextSummary(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("bytes=%d sha256=%s", len(content), hex.EncodeToString(digest[:]))
}

// markWindowsARM64GoSQLSReceiptFailure 把首个失败阶段写入可交付收据；原始错误只留在
// 本地 wire/log，收据仅保留摘要，避免把工作区绝对路径带出证据边界。
func markWindowsARM64GoSQLSReceiptFailure(receipt *windowsARM64ProcessARM64GoSQLS36ActionReceipt, phase string, err error) {
	if receipt == nil {
		return
	}
	receipt.Phase = phase
	receipt.Status = "runtime_failure"
	receipt.FailurePhase = phase
	if err != nil {
		digest := sha256.Sum256([]byte(err.Error()))
		receipt.FailureDigest = hex.EncodeToString(digest[:])
	}
}

// callWindowsARM64GoSQLSProtocol 与 MCP stdio 端点交换一条响应，并把 JSON-RPC
// error 作为普通 error 返回，调用方才能在 t.Fatalf 前把失败阶段落入 receipt。
func callWindowsARM64GoSQLSProtocol(client *mcpLSPBinaryClient, method string, params map[string]any) (json.RawMessage, error) {
	if client == nil || client.cmd == nil {
		return nil, fmt.Errorf("mcp-lsp client is not live")
	}
	request := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal %s request: %w", method, err)
	}
	if _, err := client.stdin.Write(append(payload, '\n')); err != nil {
		return nil, fmt.Errorf("write %s request: %w", method, err)
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", method, err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s returned JSON-RPC error %d: %s", method, response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}

// notifyWindowsARM64GoSQLSProtocol 发送无需响应的 MCP 通知；写失败仍由调用方
// 作为生命周期 runtime_failure 记录，不能把通知丢失当成成功。
func notifyWindowsARM64GoSQLSProtocol(client *mcpLSPBinaryClient, method string, params map[string]any) error {
	if client == nil || client.cmd == nil {
		return fmt.Errorf("mcp-lsp client is not live")
	}
	request := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal %s notification: %w", method, err)
	}
	if _, err := client.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write %s notification: %w", method, err)
	}
	return nil
}

func windowsARM64GoSQLSActionIdle(precheck bool) time.Duration {
	if precheck {
		return windowsARM64ProcessARM64GoSQLS36ActionPrecheckIdle
	}
	return windowsARM64ProcessARM64GoSQLS36ActionProofIdle
}

type windowsARM64GoSQLSPostIdleAction struct {
	Tool string
	Name string
	Args map[string]any
}

// windowsARM64GoSQLSPostIdleSpec 固定使用已经由本轮真实 receipt 证明经过 SQL
// LSP 的 format；不得用 file/grep 或合法空 signature_help 伪造生命周期语义。
func windowsARM64GoSQLSPostIdleSpec(fixture realMCPFixture) windowsARM64GoSQLSPostIdleAction {
	return windowsARM64GoSQLSPostIdleAction{
		Tool: "patch_edit",
		Name: "format",
		Args: map[string]any{"action": "format", "file_path": fixture.formatFile},
	}
}

func windowsARM64GoSQLSPostIdleContract(tool, action string) bool {
	return tool == "patch_edit" && action == "format"
}

func TestWindowsARM64ProcessARM64GoSQLSPostIdleContract(t *testing.T) {
	positive := windowsARM64GoSQLSPostIdleContract("patch_edit", "format")
	if !positive {
		t.Fatal("SQL format must remain the post-idle semantic positive contract")
	}
	for _, negative := range [][2]string{{"inspect", "signature_help"}, {"file", "open_file"}, {"grep", "text_search"}} {
		if windowsARM64GoSQLSPostIdleContract(negative[0], negative[1]) {
			t.Fatalf("non-semantic or legal-empty action accepted as post-idle probe: %s/%s", negative[0], negative[1])
		}
	}
}

// windowsARM64ProcessARM64GoSQLSServerCase 锁定 bin/LSP/test/sql 的真实文件、标识符和查询。
// 36-action 矩阵必须从该快照复制到隔离 workspace，不能重新拼接一个 synthetic SQL。
func windowsARM64ProcessARM64GoSQLSServerCase() realNodeServerCase {
	return realNodeServerCase{
		name:                 "sql",
		languageID:           "sql",
		fileName:             "001_schema.sql",
		sourceDir:            "sql",
		sourceFile:           "fixtures/001_schema.sql",
		sourceSecondaryFile:  "fixtures/003_queries.sql",
		sourceIdentifier:     "users",
		sourceWorkspaceQuery: "users",
		sourceLine:           1,
		sourceCharacter:      13,
	}
}

func windowsARM64ProcessARM64GoSQLSFixtureReproJSON(repoRoot, fixtureRoot string, server realNodeServerCase) string {
	payload := map[string]any{
		"test":                   "windows-arm64-process-arm64-go-sqls-36-action-fixture",
		"repo_root":              filepath.ToSlash(repoRoot),
		"fixture_root":           filepath.ToSlash(fixtureRoot),
		"source_root":            filepath.ToSlash(filepath.Join(repoRoot, "bin", "LSP", "test")),
		"source_dir":             server.sourceDir,
		"source_file":            server.sourceFile,
		"source_secondary_file":  server.sourceSecondaryFile,
		"source_identifier":      server.sourceIdentifier,
		"source_workspace_query": server.sourceWorkspaceQuery,
		"source_line":            server.sourceLine,
		"source_character":       server.sourceCharacter,
		"isolated_workspace":     filepath.ToSlash(filepath.Join(fixtureRoot, server.name)),
		"patch_edit_root":        filepath.ToSlash(filepath.Join(fixtureRoot, server.name, ".mcp-actions")),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"json_marshal_error":"` + strings.ReplaceAll(err.Error(), `"`, `'`) + `"}`
	}
	return string(encoded)
}

// validateWindowsARM64ProcessARM64GoSQLSFixtureSpec 在复制前验证源/目标边界和真实语义锚点。
// 所有失败由调用方附带固定 JSON 复现参数，避免路径越界或缺失被复制过程吞掉。
func validateWindowsARM64ProcessARM64GoSQLSFixtureSpec(repoRoot, fixtureRoot string, server realNodeServerCase) error {
	repoRoot = filepath.Clean(repoRoot)
	fixtureRoot = filepath.Clean(fixtureRoot)
	if !filepath.IsAbs(repoRoot) || !filepath.IsAbs(fixtureRoot) {
		return fmt.Errorf("repo/fixture roots must be absolute: repo=%q fixture=%q", repoRoot, fixtureRoot)
	}
	sourceRoot := filepath.Join(repoRoot, "bin", "LSP", "test")
	sourceDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(server.sourceDir)))
	if sourceDir == "." || filepath.IsAbs(sourceDir) {
		return fmt.Errorf("source directory must be relative and non-empty: %q", server.sourceDir)
	}
	sourceProjectRoot := filepath.Join(sourceRoot, sourceDir)
	workspaceRoot := filepath.Join(fixtureRoot, filepath.FromSlash(strings.TrimSpace(server.name)))
	if !realMCPPathWithinRoot(sourceRoot, sourceProjectRoot) {
		return fmt.Errorf("source project escaped bin/LSP/test: %q", sourceProjectRoot)
	}
	if !realMCPPathWithinRoot(fixtureRoot, workspaceRoot) {
		return fmt.Errorf("isolated workspace escaped fixture root: %q", workspaceRoot)
	}
	sourceInfo, err := os.Lstat(sourceProjectRoot)
	if err != nil {
		return fmt.Errorf("stat source project %q: %w", sourceProjectRoot, err)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source project is not a regular directory: %q", sourceProjectRoot)
	}
	if err := filepath.WalkDir(sourceProjectRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source snapshot contains unsupported symlink %q", path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk source project %q: %w", sourceProjectRoot, err)
	}

	checkRelativeFile := func(label, relative string) (string, error) {
		relative = strings.TrimSpace(relative)
		if relative == "" || filepath.IsAbs(filepath.FromSlash(relative)) {
			return "", fmt.Errorf("%s must be a relative non-empty path: %q", label, relative)
		}
		path := filepath.Join(sourceProjectRoot, filepath.FromSlash(relative))
		if !realMCPPathWithinRoot(sourceProjectRoot, path) {
			return "", fmt.Errorf("%s escaped source project: %q", label, path)
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return "", fmt.Errorf("%s is missing: %q: %w", label, path, statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is not a regular file: %q", label, path)
		}
		return path, nil
	}
	sourcePath, err := checkRelativeFile("source file", server.sourceFile)
	if err != nil {
		return err
	}
	secondaryPath, err := checkRelativeFile("secondary source file", server.sourceSecondaryFile)
	if err != nil {
		return err
	}
	if sourcePath == secondaryPath {
		return fmt.Errorf("source and secondary files must be distinct: %q", sourcePath)
	}
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read source file %q: %w", sourcePath, err)
	}
	if len(sourceBytes) == 0 {
		return fmt.Errorf("source file is empty: %q", sourcePath)
	}
	identifier := strings.TrimSpace(server.sourceIdentifier)
	workspaceQuery := strings.TrimSpace(server.sourceWorkspaceQuery)
	if identifier == "" || workspaceQuery == "" || server.sourceLine <= 0 || server.sourceCharacter < 0 {
		return fmt.Errorf("source semantic mapping is incomplete: identifier=%q query=%q line=%d character=%d", identifier, workspaceQuery, server.sourceLine, server.sourceCharacter)
	}
	sourceLines := strings.Split(strings.ReplaceAll(string(sourceBytes), "\r\n", "\n"), "\n")
	if server.sourceLine > len(sourceLines) {
		return fmt.Errorf("source semantic line=%d exceeds %d lines", server.sourceLine, len(sourceLines))
	}
	semanticLine := sourceLines[server.sourceLine-1]
	semanticEnd := server.sourceCharacter + len(identifier)
	if semanticEnd > len(semanticLine) || semanticLine[server.sourceCharacter:semanticEnd] != identifier {
		return fmt.Errorf("source semantic anchor does not target %q at %d:%d: %q", identifier, server.sourceLine, server.sourceCharacter, semanticLine)
	}
	if !strings.Contains(string(sourceBytes), workspaceQuery) {
		return fmt.Errorf("workspace query %q is absent from source file %q", workspaceQuery, sourcePath)
	}
	if _, err := os.ReadFile(secondaryPath); err != nil {
		return fmt.Errorf("read secondary source file %q: %w", secondaryPath, err)
	}
	return nil
}

func writeWindowsARM64ProcessARM64GoSQLSFixture(t *testing.T, fixtureRoot string) realMCPFixture {
	t.Helper()
	server := windowsARM64ProcessARM64GoSQLSServerCase()
	repoRoot := realNodeRepoRoot(t)
	if err := validateWindowsARM64ProcessARM64GoSQLSFixtureSpec(repoRoot, fixtureRoot, server); err != nil {
		t.Fatalf("GoSQLS SQL fixture preflight failed: %v; repro_json=%s", err, windowsARM64ProcessARM64GoSQLSFixtureReproJSON(repoRoot, fixtureRoot, server))
	}
	return writeRealMCPBinSourceFixture(t, fixtureRoot, server)
}

func TestWindowsARM64ProcessARM64GoSQLSFixtureContract(t *testing.T) {
	fixtureRoot := t.TempDir()
	server := windowsARM64ProcessARM64GoSQLSServerCase()
	repoRoot := realNodeRepoRoot(t)
	fixture := writeWindowsARM64ProcessARM64GoSQLSFixture(t, fixtureRoot)
	repro := windowsARM64ProcessARM64GoSQLSFixtureReproJSON(repoRoot, fixtureRoot, server)
	sourceSQLRoot := filepath.Join(repoRoot, "bin", "LSP", "test", "sql")
	if !realMCPPathWithinRoot(fixtureRoot, fixture.workDir) || filepath.Clean(fixture.workDir) != filepath.Join(fixtureRoot, server.name) {
		t.Fatalf("GoSQLS fixture workspace is not isolated: work_dir=%q; repro_json=%s", fixture.workDir, repro)
	}
	expectedFiles := []string{
		"LICENSE.md", "README.md",
		"fixtures/001_schema.sql", "fixtures/002_seed.sql", "fixtures/003_queries.sql", "fixtures/004_views.sql",
		"fixtures/005_trigger.sql", "fixtures/006_indexes.sql", "fixtures/007_transaction.sql", "fixtures/008_report.sql",
	}
	for _, relative := range expectedFiles {
		sourcePath := filepath.Join(sourceSQLRoot, filepath.FromSlash(relative))
		targetPath := filepath.Join(fixture.workDir, filepath.FromSlash(relative))
		if !realMCPPathWithinRoot(sourceSQLRoot, sourcePath) || !realMCPPathWithinRoot(fixture.workDir, targetPath) {
			t.Fatalf("GoSQLS fixture path escaped root: source=%q target=%q; repro_json=%s", sourcePath, targetPath, repro)
		}
		sourceBytes, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read GoSQLS source fixture %q: %v; repro_json=%s", sourcePath, err, repro)
		}
		targetBytes, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("read isolated GoSQLS fixture %q: %v; repro_json=%s", targetPath, err, repro)
		}
		if !bytes.Equal(sourceBytes, targetBytes) {
			t.Fatalf("isolated GoSQLS fixture differs from source snapshot: relative=%q; repro_json=%s", relative, repro)
		}
	}
	for label, path := range map[string]string{
		"replace": fixture.replaceFile, "rename": fixture.renameFile, "code_action": fixture.codeActionFile,
		"format": fixture.formatFile, "completion": fixture.completionFile,
	} {
		if !realMCPPathWithinRoot(fixture.workDir, path) || !strings.Contains(filepath.ToSlash(path), "/.mcp-actions/") {
			t.Fatalf("GoSQLS patch_edit %s target is not an isolated copy: %q; repro_json=%s", label, path, repro)
		}
		if filepath.Clean(path) == filepath.Clean(fixture.targetFile) {
			t.Fatalf("GoSQLS patch_edit %s target aliases primary source: %q; repro_json=%s", label, path, repro)
		}
	}
	if fixture.workspaceQuery != server.sourceWorkspaceQuery || fixture.searchNeedle == "" {
		t.Fatalf("GoSQLS fixture semantic mapping drifted: workspace_query=%q search_needle=%q; repro_json=%s", fixture.workspaceQuery, fixture.searchNeedle, repro)
	}
}

func TestWindowsARM64GoSQLSBuildCompilerUsesResolvedProductGoAsset(t *testing.T) {
	root := t.TempDir()
	goExecutable := filepath.Join(root, "go", "bin", "go.exe")
	if err := os.MkdirAll(filepath.Dir(goExecutable), 0o700); err != nil {
		t.Fatalf("create product-owned Go directory: %v", err)
	}
	if err := os.WriteFile(goExecutable, []byte("locked-go"), 0o700); err != nil {
		t.Fatalf("write product-owned Go fixture: %v", err)
	}
	resolved := installer.WindowsRuntimeDependencyProvisionResult{
		Product:      installer.WindowsRuntimeDependencyProductGoSQLS,
		Architecture: installer.WindowsHostArchARM64,
		RootPath:     root,
	}

	got, err := windowsARM64GoSQLSBuildCompiler(resolved)
	if err != nil {
		t.Fatalf("resolve product-owned Go compiler: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(goExecutable) {
		t.Fatalf("resolved Go compiler = %q, want %q", got, goExecutable)
	}
}

// windowsARM64GoSQLSBuildCompiler 从已解析的 product-owned cohort 目录读取锁定的原生 Go 编译器，不触发下载。
func windowsARM64GoSQLSBuildCompiler(resolved installer.WindowsRuntimeDependencyProvisionResult) (string, error) {
	if resolved.Product != installer.WindowsRuntimeDependencyProductGoSQLS {
		return "", fmt.Errorf("resolved compiler product=%q, want %q", resolved.Product, installer.WindowsRuntimeDependencyProductGoSQLS)
	}
	if resolved.Architecture != installer.WindowsHostArchARM64 {
		return "", fmt.Errorf("resolved compiler architecture=%q, want %q", resolved.Architecture, installer.WindowsHostArchARM64)
	}
	root := filepath.Clean(strings.TrimSpace(resolved.RootPath))
	if root == "." {
		return "", fmt.Errorf("resolved product compiler root is empty")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("resolved product compiler root must be absolute: %q", root)
	}
	assets, err := installer.WindowsRuntimeDependencyAssetsForArchitecture(resolved.Product, resolved.Architecture)
	if err != nil {
		return "", fmt.Errorf("read locked GoSQLS assets: %w", err)
	}
	var goAsset installer.WindowsRuntimeDependencyAsset
	for _, asset := range assets {
		if asset.Component != "go" {
			continue
		}
		if goAsset.Component != "" {
			return "", fmt.Errorf("locked GoSQLS catalog contains multiple Go assets")
		}
		goAsset = asset
	}
	if goAsset.Component == "" || goAsset.BinaryPath == "" {
		return "", fmt.Errorf("locked GoSQLS catalog has no Go compiler asset")
	}
	if !goAsset.Native || goAsset.Version != "1.26.5" || !strings.EqualFold(filepath.ToSlash(goAsset.BinaryPath), "go/bin/go.exe") {
		return "", fmt.Errorf("locked GoSQLS Go asset is not the native 1.26.5 compiler: %#v", goAsset)
	}
	path := filepath.Join(root, filepath.FromSlash(goAsset.BinaryPath))
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved compiler escaped product root: relative=%q err=%v", relative, err)
	}
	if !strings.EqualFold(filepath.ToSlash(relative), "go/bin/go.exe") {
		return "", fmt.Errorf("resolved compiler path=%q, want product go/bin/go.exe", filepath.ToSlash(relative))
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect resolved product Go compiler: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("resolved product Go compiler is not a regular file")
	}
	return path, nil
}

// requireWindowsARM64GoSQLSIdentity 在每个阶段用 PID+启动 token 复核同一
// MCP/GoSQLS 进程，避免 PID 复用或 server 重启被误计为生命周期稳定。
func requireWindowsARM64GoSQLSIdentity(pid int, startToken, label string) error {
	if pid <= 0 || strings.TrimSpace(startToken) == "" {
		return fmt.Errorf("%s identity is incomplete: pid=%d start_token_empty=%t", label, pid, strings.TrimSpace(startToken) == "")
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
		return fmt.Errorf("%s PID %d start identity changed: got=%s want=%s", label, pid, current, startToken)
	}
	return nil
}

// waitWindowsARM64GoSQLSIdle 只使用进程 API 采样，不发送 MCP 请求；heartbeat
// 每分钟记录一次，正式证明必须跨过生产最小 15 分钟 idle 窗口。
func waitWindowsARM64GoSQLSIdle(ctx context.Context, t *testing.T, mcpPID int, mcpStartToken string, serverPID int, serverStartToken string, duration time.Duration) int {
	t.Helper()
	if duration <= 0 {
		t.Fatalf("GoSQLS idle duration must be positive: %s", duration)
	}
	started := time.Now()
	heartbeats := 0
	sample := func() {
		if err := requireWindowsARM64GoSQLSIdentity(mcpPID, mcpStartToken, "MCP idle"); err != nil {
			t.Fatalf("GoSQLS MCP identity changed during idle after %s: %v", time.Since(started).Round(time.Second), err)
		}
		if err := requireWindowsARM64GoSQLSIdentity(serverPID, serverStartToken, "GoSQLS idle"); err != nil {
			t.Fatalf("GoSQLS server identity changed during idle after %s: %v", time.Since(started).Round(time.Second), err)
		}
		heartbeats++
		t.Logf("GoSQLS Windows ARM64/process ARM64 idle heartbeat elapsed=%s mcp_pid=%d mcp_start=%s server_pid=%d server_start=%s", time.Since(started).Round(time.Second), mcpPID, mcpStartToken, serverPID, serverStartToken)
	}
	sample()
	deadline := started.Add(duration)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
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
			t.Fatalf("GoSQLS idle sampling stopped before %s: %v", duration, ctx.Err())
		case <-timer.C:
			sample()
		}
	}
	return heartbeats
}

// closeWindowsARM64GoSQLSClient 明确发送 exit 并等待 MCP owner 进程自然退出；
// 超时强杀只返回 false，禁止把强杀误记成协议生命周期成功。
func closeWindowsARM64GoSQLSClient(t *testing.T, client *mcpLSPBinaryClient) bool {
	t.Helper()
	if client == nil || client.cmd == nil {
		return false
	}
	cmd := client.cmd
	client.cmd = nil
	closeHook := client.closeHook
	client.closeHook = nil
	exitSent := false
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	if err == nil {
		if _, err := client.stdin.Write(append(payload, '\n')); err == nil {
			exitSent = true
		}
	}
	if err := client.stdin.Close(); err != nil {
		exitSent = false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := true
	select {
	case waitErr := <-done:
		if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
			waited = false
			t.Logf("GoSQLS mcp-lsp owner exit error=%v stderr=%s", waitErr, client.stderrString())
		}
	case <-time.After(30 * time.Second):
		waited = false
		_ = cmd.Process.Kill()
		<-done
		t.Errorf("GoSQLS mcp-lsp owner required forced kill after exit timeout; stderr=%s", client.stderrString())
	}
	if closeHook != nil {
		if err := closeHook(); err != nil {
			waited = false
			t.Errorf("close GoSQLS mcp-lsp process owner: %v", err)
		}
	}
	return exitSent && waited
}

func writeWindowsARM64ProcessARM64GoSQLSManifest(t *testing.T, productRoot, executable, version string) string {
	t.Helper()
	root, err := filepath.Abs(productRoot)
	if err != nil {
		t.Fatalf("resolve GoSQLS product root for E2E manifest: %v", err)
	}
	resolvedExecutable, err := filepath.Abs(executable)
	if err != nil {
		t.Fatalf("resolve GoSQLS executable for E2E manifest: %v", err)
	}
	relative, err := filepath.Rel(root, resolvedExecutable)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("resolver-owned GoSQLS executable escaped product-root manifest: root=%q executable=%q relative=%q err=%v", root, resolvedExecutable, relative, err)
	}
	digest, err := sha256File(resolvedExecutable)
	if err != nil {
		t.Fatalf("hash resolver-owned GoSQLS executable: %v", err)
	}
	// 清单放在 product root 而不是不可变 ready cohort 目录，避免改写 resolver cache 的 manifest/tree。
	manifestPath := filepath.Join(root, "lsp-manifest-go-sqls-e2e.json")
	manifest := map[string]any{
		"servers": map[string]any{
			string(installer.WindowsRuntimeDependencyProductGoSQLS): map[string]any{
				"path":      filepath.ToSlash(relative),
				"version":   version,
				"sha256":    digest,
				"languages": []string{"sql"},
			},
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal GoSQLS E2E packaged manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write GoSQLS E2E packaged manifest: %v", err)
	}
	t.Logf("GoSQLS child bundle root=product-root executable_relative=%s sha256=%s", filepath.ToSlash(relative), digest)
	return manifestPath
}

func splitWindowsProcessCommandLine(command string) (string, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", ""
	}
	if command[0] == '"' {
		end := strings.IndexByte(command[1:], '"')
		if end < 0 {
			return "", command
		}
		end++
		return command[1:end], strings.TrimSpace(command[end+1:])
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", ""
	}
	return fields[0], strings.TrimSpace(command[len(fields[0]):])
}

// requireWindowsARM64GoSQLSChildIdentity 证明实际 child 仍是 resolver 返回的同一
// product-owned sqls.exe，且命令行没有遗留 sqruff/lsp 或其他参数；PATH 同名程序不算证明。
func requireWindowsARM64GoSQLSChildIdentity(t *testing.T, tracked map[realMCPProcessKey]realMCPProcessIdentity, serverPath, shortServerPath string) realMCPProcessIdentity {
	t.Helper()
	for _, identity := range tracked {
		if !strings.Contains(strings.ToLower(identity.Name+" "+identity.CommandLine), "sqls") {
			continue
		}
		executable, arguments := splitWindowsProcessCommandLine(identity.CommandLine)
		normalize := func(value string) string { return filepath.Clean(strings.ReplaceAll(value, "/", "\\")) }
		if !strings.EqualFold(normalize(executable), normalize(serverPath)) && !strings.EqualFold(normalize(executable), normalize(shortServerPath)) {
			t.Fatalf("GoSQLS child PID %d start=%s escaped resolver path: executable=%q want=%q or short=%q", identity.PID, identity.StartToken, executable, serverPath, shortServerPath)
		}
		if strings.TrimSpace(arguments) != "" {
			t.Fatalf("GoSQLS child PID %d start=%s has non-empty arguments=%q; product SQLS requires empty args", identity.PID, identity.StartToken, arguments)
		}
		t.Logf("GoSQLS child identity pid=%d start=%s executable_basename=%s arguments_empty=true", identity.PID, identity.StartToken, filepath.Base(executable))
		return identity
	}
	t.Fatalf("real MCP process tree did not capture resolver-owned GoSQLS child; tracked=%d", len(tracked))
	return realMCPProcessIdentity{}
}

// TestWindowsARM64ProcessARM64GoSQLS36ActionE2E 通过生产 installer、真实 mcp-lsp
// stdio 和 product-owned GoSQLS cohort 运行七个 MCP 工具族的完整 36-action 闭环。
// 该网络测试默认关闭；unsupported、合法空、null 与未分类错误分别记账，不能互相冒充语义 PASS。
func TestWindowsARM64ProcessARM64GoSQLS36ActionE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64GoSQLS36ActionE2EEnv) != "1" {
		t.Skipf("set %s=1 to enable the Windows ARM64 GoSQLS 36-action E2E", windowsARM64ProcessARM64GoSQLS36ActionE2EEnv)
	}
	precheck := os.Getenv(windowsARM64ProcessARM64GoSQLS36ActionPrecheckEnv) == "1"
	if testing.Short() && !precheck {
		t.Skip("formal GoSQLS 15-minute lifecycle proof is disabled by -short; set the explicit PRECHECK env for a bounded precheck")
	}
	if windowsARM64ProcessARM64GoSQLS36ActionProofIdle < windowsARM64ProcessARM64GoSQLS36ActionProductionMinIdle || windowsARM64ProcessARM64GoSQLS36ActionManagerIdle <= windowsARM64ProcessARM64GoSQLS36ActionProofIdle {
		t.Fatalf("invalid GoSQLS lifecycle windows: production_min=%s proof=%s manager=%s", windowsARM64ProcessARM64GoSQLS36ActionProductionMinIdle, windowsARM64ProcessARM64GoSQLS36ActionProofIdle, windowsARM64ProcessARM64GoSQLS36ActionManagerIdle)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("GoSQLS native proof requires windows/arm64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("GoSQLS action proof requires native/process ARM64, got native=%q process=%q", host.NativeArch, host.ProcessArch)
	}

	startedAt := time.Now().UTC()
	idleDuration := windowsARM64GoSQLSActionIdle(precheck)
	repoRoot := realNodeRepoRoot(t)
	productRoot := strings.TrimSpace(os.Getenv(windowsARM64ProcessARM64GoSQLSProductRootEnv))
	if productRoot == "" {
		// removeRealWindowsProductRoot 只接受显式 Windows 产品前缀的临时根；保持
		// 同一安全边界，避免失败收尾留下不可验证的临时树。
		productRoot, err = os.MkdirTemp("", "sd-node-production-windows-gosqls-")
		if err != nil {
			t.Fatalf("create isolated GoSQLS product root: %v", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("remove isolated GoSQLS product root: %v", err)
			}
		})
		if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
			t.Fatalf("restrict isolated GoSQLS product root: %v", err)
		}
	} else {
		info, statErr := os.Stat(productRoot)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("caller-provided GoSQLS product root is not a directory: %q err=%v", productRoot, statErr)
		}
		t.Logf("reusing caller-provided GoSQLS product root for post-provision MCP rerun: %s", productRoot)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	// 让缺失 APPDATA 的旧 SQLS 行为在子进程中可复现；生产 runtime 必须为 SQLS
	// 注入 cohort/config，而不是把系统用户目录当作隐式兜底。
	t.Setenv("APPDATA", "")

	contextTimeout := 40 * time.Minute
	if precheck {
		contextTimeout = 6 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", windowsARM64ProcessARM64GoSQLS36ActionManagerIdle.String())
	provider := setupInstaller()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "sql")
	if err != nil {
		t.Fatalf("production EnsureInstalledDetailed(sql) failed: %v", err)
	}
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductGoSQLS, windowsRuntimeDependencyCacheRoot(productRoot))
	if err != nil {
		t.Fatalf("resolve product-owned GoSQLS cohort after install: %v", err)
	}
	if filepath.Clean(result.Path) != filepath.Clean(resolved.ServerPath) {
		t.Fatalf("EnsureInstalled(sql) path=%q, resolver server=%q", result.Path, resolved.ServerPath)
	}
	goBin, err := windowsARM64GoSQLSBuildCompiler(resolved)
	if err != nil {
		t.Fatalf("resolve product-owned locked Go 1.26.5 compiler: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_GO_BIN", goBin)
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, result.Path); err != nil {
		t.Fatalf("GoSQLS server escaped product root: %v", err)
	}
	if err := installer.ValidateWindowsGoSQLSExecutable(result.Path, host.NativeArch); err != nil {
		t.Fatalf("validate native ARM64 GoSQLS PE: %v", err)
	}
	if got := runtimeServerWindowsEnvironmentValue(resolved.Env, runtimeServerWindowsAppDataEnv); filepath.Clean(got) != filepath.Join(resolved.RootPath, "config") {
		t.Fatalf("resolved GoSQLS APPDATA=%q, want cohort config", got)
	}
	assets, err := installer.WindowsRuntimeDependencyAssetsForArchitecture(installer.WindowsRuntimeDependencyProductGoSQLS, host.NativeArch)
	if err != nil {
		t.Fatalf("read locked GoSQLS asset facts: %v", err)
	}
	var sourceAsset installer.WindowsRuntimeDependencyAsset
	for _, asset := range assets {
		if asset.Component == "sqls-source" {
			sourceAsset = asset
			break
		}
	}
	if sourceAsset.URL != installer.WindowsGoSQLSModuleZipURL || !strings.EqualFold(sourceAsset.Checksum, installer.WindowsGoSQLSModuleZipSHA256) {
		t.Fatalf("locked GoSQLS source asset changed: %#v", sourceAsset)
	}
	shortServerPath, err := installer.WindowsShortProcessPathWithinRoot(productRoot, result.Path)
	if err != nil {
		t.Fatalf("resolve 8.3 GoSQLS process path: %v", err)
	}
	manifestPath := writeWindowsARM64ProcessARM64GoSQLSManifest(t, productRoot, result.Path, sourceAsset.Version)
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", productRoot)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", manifestPath)

	fixtureRoot := filepath.Join(t.TempDir(), "windows-arm64-process-arm64-go-sqls-36-action-fixture")
	if err := os.MkdirAll(fixtureRoot, 0o700); err != nil {
		t.Fatalf("create GoSQLS fixture root: %v", err)
	}
	server := windowsARM64ProcessARM64GoSQLSServerCase()
	fixture := writeWindowsARM64ProcessARM64GoSQLSFixture(t, fixtureRoot)
	astFile := filepath.Join(fixtureRoot, "ast_fixture.js")
	writeRealFixture(t, astFile, "function goSQLSAstFixture(name) { return name; }\ngoSQLSAstFixture(\"world\");\n")

	binary := buildRealMcpLSPBinary(t, repoRoot)
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	mcpStartToken, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		client.close(t)
		t.Fatalf("capture mcp-lsp PID %d start identity: %v", mcpPID, err)
	}
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: mcpPID, StartToken: mcpStartToken}: {PID: mcpPID, StartToken: mcpStartToken, Name: "mcp-lsp", Language: "sql"}}
	receipt := windowsARM64ProcessARM64GoSQLS36ActionReceipt{
		Test: windowsARM64ProcessARM64GoSQLS36ActionReceiptName, Phase: "stdio_started", Status: "running", StartedAt: startedAt.Format(time.RFC3339Nano),
		Precheck: precheck, ManagerIdle: windowsARM64ProcessARM64GoSQLS36ActionManagerIdle.String(), ProofIdle: idleDuration.String(), ProductionMinIdle: windowsARM64ProcessARM64GoSQLS36ActionProductionMinIdle.String(),
		ProcessArchDiagnosticOnly: true,
		AssetPolicy:               "locked GoSQLS URL+SHA256 plus native ARM64 PE validation",
		CachePolicy:               "isolated product-root cache with resolver readiness revalidation",
		HTTPPolicy:                "production installer only; no network fallback in resolver check-only path",
		ACLPolicy:                 "owner-only product root; Win32 5/1314 remains typed authorization_required",
		WirePath:                  filepath.ToSlash(filepath.Join(windowsARM64ProcessARM64GoSQLS36ActionEvidenceDir, windowsARM64ProcessARM64GoSQLS36ActionWireName)),
		HostOS:                    host.OS, NativeArch: host.NativeArch, ProcessArch: host.ProcessArch, WindowsVersion: host.WindowsVersion,
		WindowsBuild: host.WindowsBuild, Product: string(installer.WindowsRuntimeDependencyProductGoSQLS), Version: installer.WindowsGoSQLSVersion,
		SourceURL: sourceAsset.URL, SourceSHA256: sourceAsset.Checksum, GoVersion: "go1.26.5", Cohort: resolved.Cohort,
		ServerPath: "runtime-dependencies/go-sqls/arm64/.../bin/sqls.exe", ServerPID: 0, ServerStartToken: "",
		MCPPID: mcpPID, MCPStartToken: mcpStartToken, ExpectedActionTotal: realMCPExpectedActionCount, Actions: make([]windowsARM64ProcessARM64GoSQLS36ActionRecord, 0, realMCPExpectedActionCount),
	}
	shutdownSent := false
	exitSent := false
	defer func() {
		receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		receipt.ActionTotal = len(receipt.Actions)
		receipt.ShutdownSent = shutdownSent
		receipt.ExitSent = exitSent
		if client != nil && client.cmd != nil {
			if !shutdownSent {
				if _, err := callWindowsARM64GoSQLSProtocol(client, "shutdown", map[string]any{}); err != nil {
					markWindowsARM64GoSQLSReceiptFailure(&receipt, "shutdown_recovery", err)
				} else {
					shutdownSent = true
					receipt.ShutdownResponse = true
				}
			}
			trackRealMCPProcessTree(t, mcpPID, "final-before-close", tracked)
			exitSent = closeWindowsARM64GoSQLSClient(t, client)
			receipt.ShutdownSent = shutdownSent
			receipt.ExitSent = exitSent
			receipt.ExitProcessWait = exitSent
		}
		if len(tracked) > 0 {
			receipt.ProcessIdentities = sanitizeWindowsARM64GoSQLSReceiptIdentities(trackedValues(tracked))
			if shutdownSent && exitSent {
				requireRealMCPProcessIdentitiesGone(t, tracked)
				receipt.ZeroResidual = true
			}
		}
		receipt.ServerPID = findTrackedGoSQLSServerPID(tracked)
		if receipt.ServerPID != 0 {
			for _, identity := range tracked {
				if identity.PID == receipt.ServerPID {
					receipt.ServerStartToken = identity.StartToken
					break
				}
			}
		}
		if receipt.Status == "running" {
			receipt.Status = "runtime_failure"
			if receipt.FailurePhase == "" {
				receipt.FailurePhase = receipt.Phase
			}
		}
		if err := writeWindowsARM64ProcessARM64GoSQLSWire(repoRoot, receipt); err != nil {
			t.Errorf("write GoSQLS wire evidence: %v", err)
		}
		if err := writeWindowsARM64ProcessARM64GoSQLS36ActionReceipt(repoRoot, receipt); err != nil {
			t.Errorf("write GoSQLS 36-action receipt: %v", err)
		}
	}()

	// mcp-lsp 的 MCP initialize schema 只接受协议版本与 capabilities；LSP
	// initializationOptions 属于下游 LSP，不得误塞进 MCP 顶层请求。
	actions := realMCPActionSpecs(server, fixture, astFile)
	// 远程纯文本契约一般要求 workspace_symbol-file 使用 file_path；SQL 是
	// 明确例外，生产 resolver 必须用 file_path 校验 SQLite sqlc owner，不能
	// 携带 workspace_language 或旧 language selector。
	for index := range actions {
		if actions[index].tool == "structure" && actions[index].name == "workspace_symbol-language" {
			delete(actions[index].args, "language")
			delete(actions[index].args, "workspace_language")
			actions[index].args["file_path"] = fixture.targetFile
		}
	}
	if err := validateRealMCPActionClosure(actions); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "action_closure", err)
		t.Fatalf("GoSQLS action closure: %v", err)
	}
	receipt.ExpectedActionTotal = len(actions)
	receipt.Phase = "initialize"
	if _, err := callWindowsARM64GoSQLSProtocol(client, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "super-dolphin-go-sqls-windows-arm64-e2e", "version": "1"},
	}); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "initialize", err)
		t.Fatalf("GoSQLS initialize failed: %v; action_total=0 means protocol failed before action dispatch", err)
	}
	receipt.Phase = "initialized"
	if err := notifyWindowsARM64GoSQLSProtocol(client, "notifications/initialized", map[string]any{}); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "initialized_notification", err)
		t.Fatalf("GoSQLS initialized notification failed: %v", err)
	}
	receipt.Phase = "tools_list"
	tools, err := callWindowsARM64GoSQLSProtocol(client, "tools/list", map[string]any{})
	if err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "tools_list", err)
		t.Fatalf("GoSQLS tools/list failed: %v", err)
	}
	requireRealMCPToolFamilies(t, tools)
	receipt.Phase = "actions"
	t.Logf("GoSQLS action dispatch begins expected_action_total=%d; initialize/tools-list succeeded", len(actions))
	for _, action := range actions {
		action := action
		t.Run(action.tool+"/"+action.name, func(t *testing.T) {
			key := action.tool + "/" + action.name
			args := realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args)
			record := windowsARM64ProcessARM64GoSQLS36ActionRecord{Tool: action.tool, Action: action.name, Status: "runtime_failure", Arguments: receiptActionArguments(args)}
			recorded := false
			responseObserved := false
			nullResult := false
			defer func() {
				if recorded {
					return
				}
				receipt.Errors++
				if responseObserved && nullResult {
					receipt.Null++
				}
				record.Error = "runtime_failure"
				receipt.Actions = append(receipt.Actions, record)
				markWindowsARM64GoSQLSReceiptFailure(&receipt, "action/"+key, fmt.Errorf("action runtime failure"))
			}()
			if err := requireWindowsARM64GoSQLSIdentity(mcpPID, mcpStartToken, "MCP before "+key); err != nil {
				markWindowsARM64GoSQLSReceiptFailure(&receipt, "action/"+key+"/mcp_identity", err)
				t.Fatalf("MCP identity before %s: %v", key, err)
			}
			response := client.callTool(t, action.tool, args)
			responseObserved = true
			if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
				markWindowsARM64GoSQLSReceiptFailure(&receipt, "action/"+key+"/structured_content", fmt.Errorf("deprecated structuredContent returned"))
				t.Fatalf("GoSQLS %s returned deprecated structuredContent; content-only contract requires empty structuredContent", key)
			}
			contentText := response.Result.ContentText()
			nullResult = strings.TrimSpace(contentText) == ""
			record.IsError = response.Result.IsError
			record.Content = receiptTextSummary(contentText)
			status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "sql "+key)
			record.Status = string(status)
			receipt.Actions = append(receipt.Actions, record)
			recorded = true
			switch status {
			case realMCPActionSucceeded:
				receipt.Callable++
			case realMCPActionLegalEmpty:
				receipt.Callable++
				receipt.LegalEmpty++
			case realMCPActionUnsupported:
				receipt.Unsupported++
			}
			if !trackRealMCPProcessTree(t, mcpPID, "sql-action-"+key, tracked) {
				markWindowsARM64GoSQLSReceiptFailure(&receipt, "action/"+key+"/process_tree", fmt.Errorf("process tree capture failed"))
				t.Fatalf("capture GoSQLS process tree after %s failed", key)
			}
			if err := requireWindowsARM64GoSQLSIdentity(mcpPID, mcpStartToken, "MCP after "+key); err != nil {
				markWindowsARM64GoSQLSReceiptFailure(&receipt, "action/"+key+"/mcp_identity_after", err)
				t.Fatalf("MCP identity after %s: %v", key, err)
			}
		})
	}
	receipt.ActionTotal = len(receipt.Actions)
	receipt.ActionLedgerComplete = receipt.ActionTotal == receipt.ExpectedActionTotal && receipt.Callable+receipt.Unsupported+receipt.Errors == receipt.ActionTotal
	if !receipt.ActionLedgerComplete {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "action_ledger", fmt.Errorf("total=%d expected=%d callable=%d unsupported=%d errors=%d null=%d", receipt.ActionTotal, receipt.ExpectedActionTotal, receipt.Callable, receipt.Unsupported, receipt.Errors, receipt.Null))
		t.Fatalf("GoSQLS 36-action ledger has unaccounted results: total=%d callable=%d unsupported=%d null=%d errors=%d", receipt.ActionTotal, receipt.Callable, receipt.Unsupported, receipt.Null, receipt.Errors)
	}
	receipt.Phase = "actions_complete"
	if receipt.Errors != 0 {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "actions_complete", fmt.Errorf("runtime_failure=%d", receipt.Errors))
		t.Fatalf("GoSQLS 36-action matrix has runtime_failure=%d; runtime failures are not PASS", receipt.Errors)
	}
	if !trackRealMCPProcessTree(t, mcpPID, "after-actions", tracked) {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "after_actions_process_tree", fmt.Errorf("process tree capture failed"))
		t.Fatalf("capture GoSQLS process tree after action matrix failed")
	}
	serverIdentity := requireWindowsARM64GoSQLSChildIdentity(t, tracked, result.Path, shortServerPath)
	receipt.ServerPID = serverIdentity.PID
	receipt.ServerStartToken = serverIdentity.StartToken
	receipt.ServerIdentityStable = true
	if err := requireWindowsARM64GoSQLSIdentity(serverIdentity.PID, serverIdentity.StartToken, "GoSQLS after actions"); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "after_actions_server_identity", err)
		t.Fatalf("GoSQLS identity after action matrix: %v", err)
	}
	receipt.MCPIdentityStable = true
	heartbeats := waitWindowsARM64GoSQLSIdle(ctx, t, mcpPID, mcpStartToken, serverIdentity.PID, serverIdentity.StartToken, idleDuration)
	receipt.IdleDuration = idleDuration.String()
	receipt.IdleHeartbeats = heartbeats
	receipt.Phase = "post_idle"
	postIdleSpec := windowsARM64GoSQLSPostIdleSpec(fixture)
	postIdleArgs := realMCPWindowsToolArguments(server.languageID, fixtureRoot, postIdleSpec.Tool, postIdleSpec.Name, postIdleSpec.Args)
	postIdle := client.callTool(t, postIdleSpec.Tool, postIdleArgs)
	postIdleStatus := requireRealMCPActionResult(t, postIdle, true, "", false, realMCPActionCapabilityKey(postIdleSpec.Tool, postIdleSpec.Name), false, "sql post-idle "+postIdleSpec.Tool+"/"+postIdleSpec.Name)
	receipt.PostIdleAction = postIdleSpec.Tool + "/" + postIdleSpec.Name
	receipt.PostIdleStatus = string(postIdleStatus)
	receipt.PostIdleNonEmpty = realMCPActionSemanticContentNonEmpty(t, postIdle, "sql post-idle inspect/signature_help")
	if postIdleStatus != realMCPActionSucceeded || !receipt.PostIdleNonEmpty {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "post_idle", fmt.Errorf("status=%s non_empty=%t", postIdleStatus, receipt.PostIdleNonEmpty))
		t.Fatalf("GoSQLS post-idle semantic action status=%s non_empty=%t; require real non-empty success", postIdleStatus, receipt.PostIdleNonEmpty)
	}
	if err := requireWindowsARM64GoSQLSIdentity(mcpPID, mcpStartToken, "MCP after post-idle"); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "post_idle_mcp_identity", err)
		t.Fatalf("MCP identity after post-idle action: %v", err)
	}
	if err := requireWindowsARM64GoSQLSIdentity(serverIdentity.PID, serverIdentity.StartToken, "GoSQLS after post-idle"); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "post_idle_server_identity", err)
		t.Fatalf("GoSQLS identity after post-idle action: %v", err)
	}
	trackRealMCPProcessTree(t, mcpPID, "before-shutdown", tracked)
	receipt.Phase = "shutdown"
	if _, err := callWindowsARM64GoSQLSProtocol(client, "shutdown", map[string]any{}); err != nil {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "shutdown", err)
		t.Fatalf("GoSQLS shutdown failed: %v", err)
	}
	shutdownSent = true
	receipt.ShutdownResponse = true
	receipt.Phase = "exit"
	exitSent = closeWindowsARM64GoSQLSClient(t, client)
	receipt.ExitProcessWait = exitSent
	if !exitSent {
		markWindowsARM64GoSQLSReceiptFailure(&receipt, "exit", fmt.Errorf("exit notification or process wait failed"))
		t.Fatalf("GoSQLS exit did not complete cleanly")
	}
	receipt.ExitSent = true
	requireRealMCPProcessIdentitiesGone(t, tracked)
	receipt.ZeroResidual = true
	receipt.Status = map[bool]string{true: "NON_PASS_precheck_complete_not_formal_15m", false: "full_matrix_and_15m_soak"}[precheck]
	receipt.Phase = "complete"
	receipt.ActionLedgerComplete = true
	t.Logf("GoSQLS Windows ARM64/process ARM64 36-action total=%d callable=%d legal_empty=%d capability_unsupported=%d null=%d error=%d mcp_pid=%d start=%s", receipt.ActionTotal, receipt.Callable, receipt.LegalEmpty, receipt.Unsupported, receipt.Null, receipt.Errors, mcpPID, mcpStartToken)
}

func trackedValues(tracked map[realMCPProcessKey]realMCPProcessIdentity) []realMCPProcessIdentity {
	values := make([]realMCPProcessIdentity, 0, len(tracked))
	for _, identity := range tracked {
		values = append(values, identity)
	}
	return values
}

func sanitizeWindowsARM64GoSQLSReceiptIdentities(identities []realMCPProcessIdentity) []realMCPProcessIdentity {
	// receipt 只保留 PID、启动令牌和脱敏后的 basename；原始命令行留在本地调试日志，不进入交付收据。
	result := make([]realMCPProcessIdentity, len(identities))
	for index, identity := range identities {
		result[index] = identity
		executable, arguments := splitWindowsProcessCommandLine(identity.CommandLine)
		result[index].CommandLine = filepath.Base(strings.ReplaceAll(executable, "/", "\\"))
		if strings.TrimSpace(arguments) == "" {
			result[index].CommandLine += " [arguments_empty]"
		} else {
			result[index].CommandLine += " [arguments_redacted]"
		}
	}
	return result
}

func findTrackedGoSQLSServerPID(tracked map[realMCPProcessKey]realMCPProcessIdentity) int {
	for _, identity := range tracked {
		if strings.Contains(strings.ToLower(identity.Name+" "+identity.CommandLine), "sqls") {
			return identity.PID
		}
	}
	return 0
}

func writeWindowsARM64ProcessARM64GoSQLS36ActionReceipt(repoRoot string, receipt windowsARM64ProcessARM64GoSQLS36ActionReceipt) error {
	evidenceDir := filepath.Join(repoRoot, filepath.FromSlash(windowsARM64ProcessARM64GoSQLS36ActionEvidenceDir))
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(evidenceDir, windowsARM64ProcessARM64GoSQLS36ActionReceiptName), append(payload, '\n'), 0o600)
}

// writeWindowsARM64ProcessARM64GoSQLSWire 将阶段和 action 摘要写成 JSONL；原始
// MCP payload 可能包含 fixture URI，只保留摘要和受限参数，避免 wire 证据泄漏绝对路径。
func writeWindowsARM64ProcessARM64GoSQLSWire(repoRoot string, receipt windowsARM64ProcessARM64GoSQLS36ActionReceipt) error {
	evidenceDir := filepath.Join(repoRoot, filepath.FromSlash(windowsARM64ProcessARM64GoSQLS36ActionEvidenceDir))
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(evidenceDir, windowsARM64ProcessARM64GoSQLS36ActionWireName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(map[string]any{
		"event": "summary", "phase": receipt.Phase, "status": receipt.Status,
		"failure_phase": receipt.FailurePhase, "failure_digest": receipt.FailureDigest,
		"expected_action_total": receipt.ExpectedActionTotal, "action_total": receipt.ActionTotal,
		"callable": receipt.Callable, "legal_empty": receipt.LegalEmpty,
		"capability_unsupported": receipt.Unsupported, "runtime_failure": receipt.Errors,
		"shutdown_response": receipt.ShutdownResponse, "exit_process_wait": receipt.ExitProcessWait,
		"zero_residual": receipt.ZeroResidual,
	}); err != nil {
		return err
	}
	for _, action := range receipt.Actions {
		if err := encoder.Encode(map[string]any{
			"event": "action", "tool": action.Tool, "action": action.Action, "status": action.Status,
			"is_error": action.IsError, "arguments": action.Arguments,
			"content": action.Content,
		}); err != nil {
			return err
		}
	}
	return nil
}
