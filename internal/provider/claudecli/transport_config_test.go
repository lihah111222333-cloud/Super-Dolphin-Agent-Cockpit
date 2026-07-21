package claudecli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/manifestbuilder"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func mustBuildCLIArgs(t testing.TB, model, instructions, mcpConfigPath string, cfg cliLaunchConfig) []string {
	t.Helper()
	args, err := buildCLIArgs(model, instructions, mcpConfigPath, cfg)
	if err != nil {
		t.Fatalf("buildCLIArgs() error = %v", err)
	}
	return args
}

// TestLogManifestLaunchRedactsArgsAndURLSecrets 锁住 MCP launch 日志的命令行/URL 隐私边界。
// 日志可以保留 server 摘要，但不能输出 DSN、token query 或任何原始参数 secret。
func TestLogManifestLaunchRedactsArgsAndURLSecrets(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	logManifestLaunch("claude", "/work/project", "sonnet", "/tmp/mcp.json", dto.MCPManifest{Binaries: []dto.MCPBinary{
		{
			Name:    "playwright",
			Command: []string{"npx", "@playwright/mcp@latest", "--token=secret-pass"},
		},
		{
			Name: "proxy",
			Type: "http",
			URL:  "http://127.0.0.1:39003/mcp?token=sk-l05-url-secret",
		},
	}})

	output := buf.String()
	for _, forbidden := range []string{"secret-pass", "sk-l05-url-secret", "token="} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("launch manifest log leaked %q: %s", forbidden, output)
		}
	}
	for _, required := range []string{"playwright", "proxy", "server_count"} {
		if !strings.Contains(output, required) {
			t.Fatalf("launch manifest log missing safe summary %q: %s", required, output)
		}
	}
}

// TestLogManifestLaunchKeepsEnvKeysOnly 确认 launch 日志只保留 env key 和布尔摘要。
// env value、RPC 地址和命令行 secret 都不能写进结构化日志。
func TestLogManifestLaunchKeepsEnvKeysOnly(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	logManifestLaunch("claude", "/work/project", "sonnet", "/tmp/mcp.json", dto.MCPManifest{Binaries: []dto.MCPBinary{{
		Name: "orch",
		Command: []string{
			"/tmp/bin/mcp-orch",
			"--token",
			"sk-l05-arg-secret",
		},
		Env: map[string]string{
			"GO_AGENT_CTL_RPC_ADDR":       "127.0.0.1:44000",
			"GO_AGENT_CTL_BOOTSTRAP_JSON": `{"token":"sk-l05-env-secret"}`,
			"SAFE_KEY":                    "sk-l05-env-value",
		},
	}}})

	output := buf.String()
	for _, forbidden := range []string{"sk-l05-arg-secret", "sk-l05-env-secret", "sk-l05-env-value", "127.0.0.1:44000"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("launch manifest log leaked %q: %s", forbidden, output)
		}
	}
	for _, required := range []string{"GO_AGENT_CTL_RPC_ADDR", "GO_AGENT_CTL_BOOTSTRAP_JSON", "SAFE_KEY"} {
		if !strings.Contains(output, required) {
			t.Fatalf("launch manifest log missing env key %q: %s", required, output)
		}
	}
}

// TestLogManifestLaunchOmitsRawPathsAndSummarizesURL 锁住 MCP launch 日志的路径边界。
// cwd、临时 mcp config、绝对 command 目录和 URL 非固定 path/query/fragment 都不能写入日志。
func TestLogManifestLaunchOmitsRawPathsAndSummarizesURL(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	cwd := "/Users/l05/private/repo"
	mcpPath := "/var/folders/l05/private/mcp-config.json"
	commandPath := "/Users/l05/private/bin/mcp-sensitive-server"
	logManifestLaunch("claude", cwd, "sonnet", mcpPath, dto.MCPManifest{Binaries: []dto.MCPBinary{
		{
			Name: "fixed",
			Type: "http",
			URL:  "https://mcp.example.test:9443/mcp?token=sk-l05-query#sk-l05-fragment",
		},
		{
			Name: "custom",
			Type: "http",
			Command: []string{
				commandPath,
				"--dsn",
				"postgres://user:secret-pass@127.0.0.1/app",
			},
			URL: "https://mcp.example.test:9443/private/team/project?token=sk-l05-path-token#sk-l05-fragment",
		},
	}})

	output := buf.String()
	for _, forbidden := range []string{
		cwd,
		mcpPath,
		filepath.Dir(commandPath),
		"/private/team/project",
		"token=",
		"sk-l05-query",
		"sk-l05-path-token",
		"sk-l05-fragment",
		"secret-pass",
		"postgres://user",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("launch manifest log leaked %q: %s", forbidden, output)
		}
	}
	for _, required := range []string{
		"cwd_present",
		"mcp_config_present",
		"mcp-sensitive-server",
		"arg_sha256",
		"url_scheme",
		"https",
		"url_host",
		"mcp.example.test",
		"url_port",
		"9443",
		"url_path",
		"/mcp",
		"url_path_segments",
		"url_path_sha256",
	} {
		if !strings.Contains(output, required) {
			t.Fatalf("launch manifest log missing safe field %q: %s", required, output)
		}
	}
}

func TestWriteManifestConfigIncludesEnvAndAutoApprove(t *testing.T) {
	t.Parallel()

	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/bin",
		Env:       map[string]string{"CLAUDE_TEST_ENV": "1"},
	})
	manifest.Binaries[0].AutoApprove = []string{"tool.alpha", "tool.beta"}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, _ := servers[manifest.Binaries[0].Name].(map[string]any)
	env, _ := server["env"].(map[string]any)
	if got := env["CLAUDE_TEST_ENV"]; got != "1" {
		t.Fatalf("server.env = %#v, want CLAUDE_TEST_ENV=1", env)
	}
	autoApprove, _ := server["autoApprove"].([]any)
	if len(autoApprove) != 2 || autoApprove[0] != "tool.alpha" || autoApprove[1] != "tool.beta" {
		t.Fatalf("server.autoApprove = %#v, want ordered tool list", server["autoApprove"])
	}
	if got := server["cwd"]; got != "/tmp/work" {
		t.Fatalf("server.cwd = %#v, want /tmp/work", got)
	}
}

func TestWriteManifestConfigIncludesHTTPHeaders(t *testing.T) {
	t.Parallel()

	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{
		Name:    "orch",
		Type:    "http",
		URL:     "http://127.0.0.1:39003/mcp",
		Headers: map[string]string{"Authorization": "Bearer secret"},
	}}}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, _ := servers["orch"].(map[string]any)
	headers, _ := server["headers"].(map[string]any)
	if got := headers["Authorization"]; got != "Bearer secret" {
		t.Fatalf("server.headers = %#v, want Authorization bearer token", server["headers"])
	}
}

func TestWriteManifestConfigRejectsRemovedPostgresStdioServer(t *testing.T) {
	t.Parallel()

	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{
		Name: "postgres",
		Command: []string{
			"mcp-server-postgres",
			"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
		},
	}}}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if cleanup != nil {
		defer cleanup()
	}
	if path != "" || err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("writeManifestConfig() = (%q, %v), want removed postgres command rejection", path, err)
	}
}

func TestWriteManifestConfigIncludesAllowedNPXSQLiteStdioServer(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), ".super-dolphin", "super-dolphin.db")
	dsn := "sqlite:///" + filepath.ToSlash(dbPath)
	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{
		Name: "sqlite",
		Command: []string{
			"npx",
			"-y",
			"@bytebase/dbhub@0.23.0",
			"--dsn=" + dsn,
		},
	}}}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, _ := servers["sqlite"].(map[string]any)
	if server["command"] != "npx" {
		t.Fatalf("sqlite server = %#v, want npx command", server)
	}
	args, _ := server["args"].([]any)
	if len(args) != 3 || args[1] != "@bytebase/dbhub@0.23.0" || args[2] != "--dsn="+dsn {
		t.Fatalf("sqlite args = %#v, want dbhub sqlite npx package", args)
	}
}

func TestWriteManifestConfigIncludesAllowedNPXPlaywrightStdioServer(t *testing.T) {
	t.Parallel()

	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{
		Name: "playwright",
		Command: []string{
			"npx",
			"@playwright/mcp@latest",
		},
	}}}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, _ := servers["playwright"].(map[string]any)
	if server["command"] != "npx" {
		t.Fatalf("playwright server = %#v, want npx command", server)
	}
	args, _ := server["args"].([]any)
	if len(args) != 1 || args[0] != "@playwright/mcp@latest" {
		t.Fatalf("playwright args = %#v, want playwright npx package", args)
	}
}

func TestWriteManifestConfigFailsFastForRejectedStdioServer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "unknown command", command: "run-anything"},
		{name: "mcp prefix", command: filepath.Join(t.TempDir(), "mcp-evil")},
		{name: "mcp prefix exe", command: filepath.Join(t.TempDir(), "mcp-evil.exe")},
		{name: "mcp prefix cmd", command: filepath.Join(t.TempDir(), "mcp-evil.cmd")},
		{name: "managed basename with untrusted server name", command: filepath.Join(t.TempDir(), "mcp-lsp")},
		{name: "path-qualified postgres", command: filepath.Join(t.TempDir(), "mcp-server-postgres")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{
				Name:    "unsafe",
				Command: []string{tc.command},
			}}}

			path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
			if err == nil {
				if cleanup != nil {
					cleanup()
				}
				t.Fatalf("writeManifestConfig() = (%q, cleanup, nil), want rejected server error", path)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "rejected") {
				t.Fatalf("writeManifestConfig() error = %v, want rejected server context", err)
			}
		})
	}
}

func TestResolvePermissionModeAcceptsLegacyAndNewApprovalPolicies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		policy string
		want   string
	}{
		{name: "empty", policy: "", want: "bypassPermissions"},
		{name: "never", policy: "never", want: "bypassPermissions"},
		{name: "on-request", policy: "on-request", want: "bypassPermissions"},
		{name: "always", policy: "always", want: "bypassPermissions"},
		{name: "auto", policy: "auto", want: "bypassPermissions"},
		{name: "on-failure", policy: "on-failure", want: "default"},
		{name: "untrusted", policy: "untrusted", want: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePermissionMode(tc.policy, "")
			if err != nil {
				t.Fatalf("resolvePermissionMode(%q, \"\") error = %v", tc.policy, err)
			}
			if got != tc.want {
				t.Fatalf("resolvePermissionMode(%q, \"\") = %q, want %q", tc.policy, got, tc.want)
			}
		})
	}
}

func TestBuildCLIArgsRejectsUnknownPermissionInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		cfg     cliLaunchConfig
		wantErr string
	}{
		{name: "unknown approval", cfg: cliLaunchConfig{ApprovalPolicy: "danger"}, wantErr: "invalid approval policy"},
		{name: "unknown sandbox", cfg: cliLaunchConfig{Sandbox: "god-mode"}, wantErr: "invalid sandbox type"},
		{name: "sandbox object missing type", cfg: cliLaunchConfig{Sandbox: `{"mode":"workspace-write"}`}, wantErr: "type is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", tc.cfg); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("buildCLIArgs() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestComposeLaunchSystemPromptUsesPromptAssemblySnapshot(t *testing.T) {
	t.Parallel()

	got := composeLaunchSystemPrompt("", cliLaunchConfig{
		DeveloperInstructions: "legacy developer",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled developer",
		},
	})
	if got != "assembled base\n\nassembled developer" {
		t.Fatalf("composeLaunchSystemPrompt() = %q", got)
	}
}

func TestBuildCLIArgsDoesNotFallbackSystemPromptWhenPromptEmpty(t *testing.T) {
	t.Parallel()

	args := mustBuildCLIArgs(t, "claude-sonnet", "", "", cliLaunchConfig{})
	if got := flagValues(args, "--system-prompt"); len(got) != 0 {
		t.Fatalf("flagValues(--system-prompt) = %#v, want no fallback prompt", got)
	}
}

func TestBuildCLIArgsSplitsBoundaryBlocksIntoRepeatedSystemPrompts(t *testing.T) {
	t.Parallel()

	args := mustBuildCLIArgs(t, "claude-sonnet", "", "", cliLaunchConfig{
		DeveloperInstructions: "legacy developer",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			BaseInstructions: "assembled base",
			Boundary: &dto.PromptAssemblyBoundary{
				CachedPrefix: "cached prefix",
				UncachedTail: "uncached tail",
			},
			DeveloperInstructions: "assembled developer",
		},
	})
	if got := flagValues(args, "--system-prompt"); len(got) != 3 ||
		got[0] != "cached prefix" ||
		got[1] != "uncached tail" ||
		got[2] != "assembled developer" {
		t.Fatalf("flagValues(--system-prompt) = %#v, want cached/uncached/developer blocks", got)
	}
}

func TestBuildCLIArgsCanonicalizesLatestClaudeLongSlugs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		model string
		want  string
	}{
		{name: "opus latest", model: " claude-opus-4-7 ", want: "opus"},
		{name: "opus latest 1m", model: "claude-opus-4-7[1m]", want: "opus[1m]"},
		{name: "sonnet latest", model: "claude-sonnet-4-7", want: "sonnet"},
		{name: "sonnet latest 1m", model: "claude-sonnet-4-7[1m]", want: "sonnet[1m]"},
		{name: "haiku latest", model: "claude-haiku-4-5", want: "haiku"},
		{name: "pinned version unchanged", model: "claude-opus-4-6[1m]", want: "claude-opus-4-6[1m]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := mustBuildCLIArgs(t, tc.model, "system", "", cliLaunchConfig{})
			values := flagValues(args, "--model")
			if len(values) != 1 || values[0] != tc.want {
				t.Fatalf("--model values = %#v, want [%q]", values, tc.want)
			}
		})
	}
}

func TestWriteManifestConfigAcceptsShortFamilyName(t *testing.T) {
	t.Parallel()

	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	// BuildManifest now emits short family names ("lsp", "orch").
	if _, ok := servers["lsp"]; !ok {
		t.Fatalf("mcpServers = %#v, want short family name key \"lsp\"", servers)
	}
	if _, ok := servers["orch"]; !ok {
		t.Fatalf("mcpServers = %#v, want short family name key \"orch\"", servers)
	}
}

func TestClaude_MCP_SmokeTest(t *testing.T) {
	t.Parallel()

	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	args := mustBuildCLIArgs(t, "claude-sonnet", "system", path, cliLaunchConfig{})
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--mcp-config" {
			continue
		}
		if args[i+1] != path {
			t.Fatalf("--mcp-config path = %q, want %q", args[i+1], path)
		}
		if _, err := os.Stat(args[i+1]); err != nil {
			t.Fatalf("Stat(%q) error = %v", args[i+1], err)
		}
		return
	}
	t.Fatalf("buildCLIArgs() args = %#v, want --mcp-config %q", args, path)
}

// TestRouterInjectedPromptReachesSystemPromptFlag proves that a PromptTemplate
// body pushed into StartSessionRequest by the thread-module router survives
// through resolveStartAssembly + buildCLIArgs and ends up as a --system-prompt
// argument on the Claude CLI invocation. If this test fails, the injection is
// actually broken. If it passes but the live agent still answers "I am
// Claude", the template content is just too generic to override identity.
func TestRouterInjectedPromptReachesSystemPromptFlag(t *testing.T) {
	t.Parallel()

	const routerBody = "ROUTER_INJECTED_PROMPT_BODY_通用助手"

	req := dto.StartSessionRequest{
		Provider:     "claude",
		Instructions: routerBody,
		StartAssembly: dto.StartAssembly{
			BaseInstructions: routerBody,
			Snapshot: dto.PromptAssemblySnapshot{
				BaseInstructions: routerBody,
			},
		},
	}
	assembly := resolveStartAssembly(req, cliLaunchConfig{}, "claude")
	if assembly.BaseInstructions != routerBody {
		t.Fatalf("assembly.BaseInstructions = %q, want router body", assembly.BaseInstructions)
	}
	if assembly.Snapshot.BaseInstructions != routerBody {
		t.Fatalf("assembly.Snapshot.BaseInstructions = %q, want router body", assembly.Snapshot.BaseInstructions)
	}

	cfg := cliLaunchConfig{
		PromptSnapshot: contract.PromptAssemblySnapshot{
			BaseInstructions: assembly.Snapshot.BaseInstructions,
		},
	}
	args := mustBuildCLIArgs(t, "claude-sonnet", assembly.BaseInstructions, "", cfg)

	if !slices.Contains(flagValues(args, "--system-prompt"), routerBody) {
		t.Fatalf("--system-prompt flag values = %#v, want one equal to %q", flagValues(args, "--system-prompt"), routerBody)
	}
}

func TestStartRuntimeContextReachesSystemPromptFlag(t *testing.T) {
	t.Parallel()

	req := dto.StartSessionRequest{
		Provider: "claude",
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "assembled base",
			UserContext: map[string]string{
				"runtimeExtras": "可用专家: main/expert/prompt",
			},
			SystemContext: dto.SystemContext{"gitStatus": "## main\n M prompt.go"},
		},
	}
	assembly := resolveStartAssembly(req, cliLaunchConfig{}, "claude")
	args := mustBuildCLIArgs(t, "claude-sonnet", assembly.BaseInstructions, "", cliLaunchConfig{
		PromptSnapshot: contract.PromptAssemblySnapshot{BaseInstructions: assembly.Snapshot.BaseInstructions},
	})

	joined := strings.Join(flagValues(args, "--system-prompt"), "\n")
	for _, want := range []string{"assembled base", "可用专家: main/expert/prompt", "# System Context"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("--system-prompt = %q, want substring %q", joined, want)
		}
	}
}

func TestStartRuntimeContextReachesBoundarySystemPromptFlag(t *testing.T) {
	t.Parallel()

	req := dto.StartSessionRequest{
		Provider: "claude",
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "assembled base",
			Boundary: &dto.PromptAssemblyBoundary{
				CachedPrefix: "cached base",
				UncachedTail: "existing uncached tail\n\n可用专家: main/expert/prompt",
			},
			UserContext: map[string]string{
				"runtimeExtras": "可用专家: main/expert/prompt",
			},
			SystemContext: dto.SystemContext{"gitStatus": "## main\n M prompt.go"},
		},
	}
	assembly := resolveStartAssembly(req, cliLaunchConfig{}, "claude")
	args := mustBuildCLIArgs(t, "claude-sonnet", assembly.BaseInstructions, "", cliLaunchConfig{
		PromptSnapshot: assembly.Snapshot,
	})

	joined := strings.Join(flagValues(args, "--system-prompt"), "\n")
	for _, want := range []string{"cached base", "existing uncached tail", "可用专家: main/expert/prompt", "# System Context"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("--system-prompt = %q, want substring %q", joined, want)
		}
	}
	if strings.Count(joined, "可用专家: main/expert/prompt") != 1 {
		t.Fatalf("--system-prompt = %q, want available experts exactly once", joined)
	}
}

func flagValues(args []string, flag string) []string {
	values := make([]string, 0, len(args)/2)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			values = append(values, args[i+1])
		}
	}
	return values
}
