//go:build windows && e2e

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// TestRealSvelteYAMLRawNodeExistingCohortE2E isolates the package/server
// boundary from MCP startup. It uses the existing product Node runtime and
// cohort only, then exercises initialize plus semantic probes (including
// documentSymbol/diagnostics) for both checked-in fixtures.
func TestRealSvelteYAMLRawNodeExistingCohortE2E(t *testing.T) {
	if os.Getenv(realNodeSharedCohortWindowsE2EEnv) != "1" || runtime.GOOS != "windows" {
		t.Skip("set shared cohort Windows E2E env on Windows")
	}
	productRoot := filepath.Clean(strings.TrimSpace(os.Getenv(realNodeSharedCohortRootEnv)))
	if productRoot == "." || productRoot == "" {
		t.Fatalf("%s must name existing product root", realNodeSharedCohortRootEnv)
	}
	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("create existing Node runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	paths, err := nodeRuntime.Ensure(ctx)
	if err != nil {
		t.Fatalf("reuse existing Node runtime: %v", err)
	}
	installDir := paths.Prefix
	nodeDist := filepath.Dir(paths.NodePath)
	for _, languageID := range []string{"svelte", "yaml"} {
		cases := realNodeServerCasesForLanguage(languageID)
		if len(cases) != 1 {
			t.Fatalf("expected one server case for %s, got %d", languageID, len(cases))
		}
		runRealNodeServer(t, realNodeRepoRoot(t), nodeDist, installDir, cases[0])
	}
}

// TestRealYAMLProtocolInitializeRedE2E is the focused protocol red test. It
// intentionally stops at initialize so stderr/process evidence identifies a
// launcher/runtime incompatibility before MCP adds another layer.
func TestRealYAMLProtocolInitializeRedE2E(t *testing.T) {
	if os.Getenv(realNodeSharedCohortWindowsE2EEnv) != "1" || runtime.GOOS != "windows" {
		t.Skip("set shared cohort Windows E2E env on Windows")
	}
	productRoot := filepath.Clean(strings.TrimSpace(os.Getenv(realNodeSharedCohortRootEnv)))
	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	paths, err := nodeRuntime.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cases := realNodeServerCasesForLanguage("yaml")
	if len(cases) != 1 {
		t.Fatalf("yaml server cases=%d", len(cases))
	}
	// runRealNodeServer performs the exact Content-Length initialize exchange,
	// captures stderr, and reports the server PID before the timeout.
	runRealNodeServer(t, realNodeRepoRoot(t), filepath.Dir(paths.NodePath), paths.Prefix, cases[0])
}

// TestRealYAMLKnownPathProtocolE2E bypasses WindowsAssetCache verification and
// launches the already-materialized Node/server paths directly. The request
// bytes and argv are logged so cache contention cannot mask protocol evidence.
func TestRealYAMLKnownPathProtocolE2E(t *testing.T) {
	if os.Getenv(realNodeSharedCohortWindowsE2EEnv) != "1" || runtime.GOOS != "windows" {
		t.Skip("set shared cohort Windows E2E env on Windows")
	}
	root := filepath.Clean(strings.TrimSpace(os.Getenv(realNodeSharedCohortRootEnv)))
	cohort := filepath.Join(root, "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a")
	nodeDist := filepath.Join(root, "cache", "lsp-assets", "node-runtime", "22.22.0", "arm64", "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a", "ready", "node-v22.22.0-win-arm64")
	server := filepath.Join(cohort, "node_modules", "yaml-language-server", "bin", "yaml-language-server")
	if _, err := os.Stat(server); err != nil {
		t.Fatalf("known YAML server path missing: %v", err)
	}
	fixture := writeRealMCPBinSourceFixture(t, t.TempDir(), realNodeServerCasesForLanguage("yaml")[0])
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(nodeDist, "node.exe"), server, "--stdio")
	cmd.Dir = fixture.workDir
	cmd.Env = realNodeEnvironment(os.Environ(), nodeDist, cohort)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	client := &realLSPClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &realNodeBuffer{}}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(client.stderr, stderr) }()
	params := realInitializeParams(realFileURI(fixture.workDir))
	rawParams, _ := json.Marshal(params)
	t.Logf("yaml direct argv=%q node=%s package=%s initialize_content_length=%d initialize_json=%s", cmd.Args, filepath.Join(nodeDist, "node.exe"), server, len(rawParams), rawParams)
	_, requestErr := client.request(ctx, "initialize", params)
	if requestErr != nil {
		t.Logf("yaml direct initialize RED stderr=%q process_alive=%t err=%v", client.stderr.String(), cmd.ProcessState == nil, requestErr)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("yaml direct known-path initialize: %v", requestErr)
	}
	t.Logf("yaml direct initialize GREEN stderr=%q", client.stderr.String())
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

const (
	realNodeSharedCohortWindowsE2EEnv = "MCP_LSP_REAL_SVELTE_YAML_SHARED_COHORT_WINDOWS_E2E"
	realNodeSharedCohortRootEnv       = "MCP_LSP_REAL_SVELTE_YAML_SHARED_COHORT_ROOT"
)

type realSvelteYAMLSharedCohortProbe struct {
	Language  string `json:"language"`
	Tool      string `json:"tool"`
	Action    string `json:"action"`
	File      string `json:"file"`
	RPCError  string `json:"rpc_error,omitempty"`
	IsError   bool   `json:"is_error"`
	Content   string `json:"content,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	CallError string `json:"call_error,omitempty"`
}

type realSvelteYAMLSharedCohortReport struct {
	OS              string                            `json:"os"`
	GOARCH          string                            `json:"goarch"`
	ProductRoot     string                            `json:"product_root"`
	CachePolicy     string                            `json:"cache_policy"`
	ExpectedPackage map[string]string                 `json:"expected_package"`
	Initialization  []realSvelteYAMLSharedCohortProbe `json:"initialization,omitempty"`
	Concurrent      []realSvelteYAMLSharedCohortProbe `json:"concurrent"`
	FollowUp        []realSvelteYAMLSharedCohortProbe `json:"follow_up,omitempty"`
}

// TestRealSvelteYAMLSharedCohortConcurrentWindowsE2E 锁定真实 bin/LSP/test
// fixture、共享产品 npm cohort 和跨 sidecar 并发；测试只复用显式提供的产品根，
// 不删除缓存，也不把每次运行变成强制下载。
func TestRealSvelteYAMLSharedCohortConcurrentWindowsE2E(t *testing.T) {
	if os.Getenv(realNodeSharedCohortWindowsE2EEnv) != "1" {
		t.Skip("set MCP_LSP_REAL_SVELTE_YAML_SHARED_COHORT_WINDOWS_E2E=1 to run the shared npm cohort E2E")
	}
	if runtime.GOOS != "windows" {
		t.Skip("shared npm cohort E2E is Windows-only")
	}
	productRoot := filepath.Clean(strings.TrimSpace(os.Getenv(realNodeSharedCohortRootEnv)))
	if productRoot == "." || productRoot == "" {
		t.Fatalf("%s must name an existing product root; refusing to create a download-only test root", realNodeSharedCohortRootEnv)
	}
	info, err := os.Stat(productRoot)
	if err != nil {
		t.Fatalf("stat shared npm cohort product root %q: %v", productRoot, err)
	}
	if !info.IsDir() {
		t.Fatalf("shared npm cohort product root %q is not a directory", productRoot)
	}

	// The child sidecars must resolve the bundled Node runtime, not a host PATH
	// node/npm that would bypass the production cohort under test.
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))
	binary := buildMcpLSPBinaryForTest(t)
	fixtureRoot := t.TempDir()
	servers := map[string]realNodeServerCase{}
	for _, languageID := range []string{"svelte", "yaml"} {
		cases := realNodeServerCasesForLanguage(languageID)
		if len(cases) != 1 {
			t.Fatalf("expected one real Node server case for %s, got %d", languageID, len(cases))
		}
		servers[languageID] = cases[0]
	}
	svelteFixture := writeRealMCPBinSourceFixture(t, fixtureRoot, servers["svelte"])
	yamlFixture := writeRealMCPBinSourceFixture(t, fixtureRoot, servers["yaml"])

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	report := realSvelteYAMLSharedCohortReport{
		OS:          runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		ProductRoot: productRoot,
		CachePolicy: "reuse-existing-product-cohort; no cache deletion or forced download",
		ExpectedPackage: map[string]string{
			"svelte-language-server": svelteLanguageServerInstallVersion,
			"yaml-language-server":   yamlLanguageServerInstallVersion,
		},
	}
	clients := map[string]*mcpLSPBinaryClient{}
	for _, languageID := range []string{"svelte", "yaml"} {
		client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, realNodeRepoRoot(t), "", "", productRoot)
		clients[languageID] = client
		t.Cleanup(func() {
			if client.cmd != nil {
				client.close(t)
			}
		})
	}

	initialization := make(chan realSvelteYAMLSharedCohortProbe, len(clients))
	var initGroup sync.WaitGroup
	for languageID, client := range clients {
		languageID, client := languageID, client
		initGroup.Add(1)
		go func() {
			defer initGroup.Done()
			response, callErr := callRealMCPInitializeNoFatal(client, 45*time.Second)
			probe := realSvelteYAMLSharedCohortProbe{
				Language: languageID, Tool: "json-rpc", Action: "initialize",
				IsError: response.Result.IsError, Content: response.Result.ContentText(),
				Stderr: client.stderrString(),
			}
			if response.Error != nil {
				probe.RPCError = response.Error.Message
			}
			if callErr != nil {
				probe.CallError = callErr.Error()
			}
			initialization <- probe
		}()
	}
	initGroup.Wait()
	close(initialization)
	for probe := range initialization {
		report.Initialization = append(report.Initialization, probe)
	}
	for _, probe := range report.Initialization {
		if probe.CallError != "" || probe.RPCError != "" || probe.IsError {
			reportJSON, marshalErr := json.Marshal(report)
			if marshalErr != nil {
				t.Fatalf("marshal shared npm cohort initialization JSON: %v", marshalErr)
			}
			t.Logf("real_svelte_yaml_shared_cohort_reproduction_json=%s", reportJSON)
			for _, client := range clients {
				if client.cmd != nil && client.cmd.Process != nil {
					_ = client.cmd.Process.Kill()
				}
			}
			t.Fatalf("real Svelte/YAML shared npm cohort initialization failed: %s", reportJSON)
		}
	}
	type request struct {
		language string
		tool     string
		action   string
		file     string
		workDir  string
		client   *mcpLSPBinaryClient
		args     map[string]any
	}
	requests := []request{
		{
			language: "svelte", tool: "file", action: "diagnostics", file: svelteFixture.targetFile,
			workDir: svelteFixture.workDir,
			client:  clients["svelte"],
			args: realMCPWindowsToolArguments("svelte", svelteFixture.workDir, "file", "diagnostics", map[string]any{
				"action": "diagnostics", "file_path": svelteFixture.targetFile,
			}),
		},
		{
			language: "yaml", tool: "structure", action: "document_symbol", file: yamlFixture.targetFile,
			workDir: yamlFixture.workDir,
			client:  clients["yaml"],
			args: realMCPWindowsToolArguments("yaml", yamlFixture.workDir, "structure", "document_symbol", map[string]any{
				"action": "document_symbol", "file_path": yamlFixture.targetFile, "max_results": 20,
			}),
		},
	}
	results := make(chan realSvelteYAMLSharedCohortProbe, len(requests))
	var group sync.WaitGroup
	for _, req := range requests {
		req := req
		group.Add(1)
		go func() {
			defer group.Done()
			response, callErr := callMCPToolForScopedE2E(req.client, req.tool, req.args, req.workDir, []string{req.workDir})
			probe := realSvelteYAMLSharedCohortProbe{
				Language: req.language, Tool: req.tool, Action: req.action, File: req.file,
				IsError: response.Result.IsError, Content: response.Result.ContentText(),
				Stderr: req.client.stderrString(),
			}
			if response.Error != nil {
				probe.RPCError = response.Error.Message
			}
			if callErr != nil {
				probe.CallError = callErr.Error()
			}
			results <- probe
		}()
	}
	group.Wait()
	close(results)
	for probe := range results {
		report.Concurrent = append(report.Concurrent, probe)
	}

	// Once the two installers have completed, exercise the opposite operation
	// on each sidecar too. This proves both package shims are usable, not merely
	// that one installation happened to win the race.
	followUps := []request{
		{
			language: "svelte", tool: "structure", action: "document_symbol", file: svelteFixture.targetFile,
			workDir: svelteFixture.workDir,
			client:  clients["svelte"],
			args: realMCPWindowsToolArguments("svelte", svelteFixture.workDir, "structure", "document_symbol", map[string]any{
				"action": "document_symbol", "file_path": svelteFixture.targetFile, "max_results": 20,
			}),
		},
		{
			language: "yaml", tool: "file", action: "diagnostics", file: yamlFixture.targetFile,
			workDir: yamlFixture.workDir,
			client:  clients["yaml"],
			args: realMCPWindowsToolArguments("yaml", yamlFixture.workDir, "file", "diagnostics", map[string]any{
				"action": "diagnostics", "file_path": yamlFixture.targetFile,
			}),
		},
	}
	for _, req := range followUps {
		response, callErr := callMCPToolForScopedE2E(req.client, req.tool, req.args, req.workDir, []string{req.workDir})
		probe := realSvelteYAMLSharedCohortProbe{
			Language: req.language, Tool: req.tool, Action: req.action, File: req.file,
			IsError: response.Result.IsError, Content: response.Result.ContentText(),
			Stderr: req.client.stderrString(),
		}
		if response.Error != nil {
			probe.RPCError = response.Error.Message
		}
		if callErr != nil {
			probe.CallError = callErr.Error()
		}
		report.FollowUp = append(report.FollowUp, probe)
	}

	reportJSON, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatalf("marshal shared npm cohort reproduction JSON: %v", marshalErr)
	}
	t.Logf("real_svelte_yaml_shared_cohort_reproduction_json=%s", reportJSON)
	for _, probe := range append(report.Concurrent, report.FollowUp...) {
		if probe.CallError != "" || probe.RPCError != "" || probe.IsError {
			t.Fatalf("real Svelte/YAML shared npm cohort request failed: %s", reportJSON)
		}
	}
}

func callRealMCPInitializeNoFatal(client *mcpLSPBinaryClient, timeout time.Duration) (mcpLSPBinaryResponse, error) {
	if client == nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("nil MCP client")
	}
	result := make(chan struct {
		response mcpLSPBinaryResponse
		err      error
	}, 1)
	go func() {
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      time.Now().UnixNano(),
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "shared-npm-cohort-e2e", "version": "1"},
			},
		}
		raw, err := json.Marshal(req)
		if err != nil {
			result <- struct {
				response mcpLSPBinaryResponse
				err      error
			}{err: err}
			return
		}
		if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
			result <- struct {
				response mcpLSPBinaryResponse
				err      error
			}{err: err}
			return
		}
		line, err := client.stdout.ReadBytes('\n')
		if err != nil {
			result <- struct {
				response mcpLSPBinaryResponse
				err      error
			}{err: err}
			return
		}
		var response mcpLSPBinaryResponse
		if err := json.Unmarshal(line, &response); err != nil {
			result <- struct {
				response mcpLSPBinaryResponse
				err      error
			}{err: err}
			return
		}
		if response.Error != nil {
			result <- struct {
				response mcpLSPBinaryResponse
				err      error
			}{response: response, err: fmt.Errorf("initialize JSON-RPC error %d: %s", response.Error.Code, response.Error.Message)}
			return
		}
		result <- struct {
			response mcpLSPBinaryResponse
			err      error
		}{response: response}
	}()
	select {
	case value := <-result:
		return value.response, value.err
	case <-time.After(timeout):
		return mcpLSPBinaryResponse{}, fmt.Errorf("initialize response timeout after %s", timeout)
	}
}
