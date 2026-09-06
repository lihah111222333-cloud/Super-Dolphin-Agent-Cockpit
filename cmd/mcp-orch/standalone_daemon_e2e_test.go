//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const serveE2EToken = "standalone-daemon-e2e-token"

const serveE2EDefaultRequestTimeout = 30 * time.Second

type serveE2EFixture struct{ config, workspace, state, token string }
type serveE2EReady struct {
	Endpoint   string `json:"endpoint"`
	InstanceID string `json:"instance_id"`
}
type serveE2EProcess struct {
	cmd            *exec.Cmd
	done           chan struct{}
	waitErr        error
	wait           sync.WaitGroup
	stdout, stderr bytes.Buffer
	ready          serveE2EReady
}
type serveE2EReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
type serveE2EHTTPReply struct {
	status  int
	session string
	body    []byte
}
type serveE2ECommandResult struct {
	err            error
	timedOut       bool
	stdout, stderr string
}

func TestMCPOrchServeBlackBoxSingleInstance(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	process := startServeE2E(t, ctx, binary, fixture)
	a, b := &http.Client{Timeout: 5 * time.Second}, &http.Client{Timeout: 5 * time.Second}
	sessionA, nameA := initializeServeE2E(t, a, process.ready.Endpoint, fixture.token)
	sessionB, nameB := initializeServeE2E(t, b, process.ready.Endpoint, fixture.token)
	if sessionA == sessionB {
		t.Fatalf("two clients received same session id %q", sessionA)
	}
	if nameA != "mcp-orch" || nameB != nameA {
		t.Fatalf("server names=%q/%q, want mcp-orch", nameA, nameB)
	}
	// Request IDs are scoped to a client session. Reusing the same IDs across
	// both sessions must not cross-wire either response or session state.
	assertServeTools(t, a, process.ready.Endpoint, fixture.token, sessionA, 2)
	assertServeTools(t, b, process.ready.Endpoint, fixture.token, sessionB, 2)
	assertServeListAgents(t, a, process.ready.Endpoint, fixture.token, sessionA, 3)
	assertServeListAgents(t, b, process.ready.Endpoint, fixture.token, sessionB, 3)
	deleteServeE2ESession(t, a, process.ready.Endpoint, fixture.token, sessionA)
	removed := requestServeE2E(t, a, process.ready.Endpoint, fixture.token, sessionA, 4, "ping", map[string]any{})
	if removed.status != http.StatusNotFound {
		t.Fatalf("deleted session remained usable: status=%d body=%s", removed.status, serveE2EPayloadSummary(removed.body))
	}
	assertServeListAgents(t, b, process.ready.Endpoint, fixture.token, sessionB, 4)
	if err := stopServeProcessE2E(t, process, nil); err != nil {
		t.Fatalf("serve shutdown error = %v", err)
	}
	assertServeEndpointClosed(t, a, process.ready.Endpoint)
	if process.stdout.String() != "" {
		t.Fatalf("daemon wrote stdout: %s", serveE2ETextSummary(process.stdout.String()))
	}
}

// deleteServeE2ESession 通过真实 HTTP 关闭一个客户端会话，不停止共享 daemon。
func deleteServeE2ESession(t *testing.T, client *http.Client, endpoint, token, session string) {
	t.Helper()
	request, cancel, err := newServeE2ERequest(t, client, http.MethodDelete, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Mcp-Session-Id", session)
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("session DELETE status=%d", response.StatusCode)
	}
}

func TestMCPOrchServeRejectsSecondInstance(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	first := startServeE2E(t, ctx, binary, fixture)
	otherConfig := filepath.Join(t.TempDir(), "different-config.json")
	writeServeE2EFile(t, otherConfig, readServeE2EFile(t, fixture.config), 0o600)
	aliasConfig := writeServeE2EStateAliasConfig(t, fixture)
	for _, config := range []string{fixture.config, otherConfig, aliasConfig} {
		assertServeSecondOwnerRejected(t, ctx, binary, config)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	session, _ := initializeServeE2E(t, client, first.ready.Endpoint, fixture.token)
	assertServeListAgents(t, client, first.ready.Endpoint, fixture.token, session, 2)
	if first.ready.InstanceID == "" {
		t.Fatal("first instance_id is empty")
	}
}

func TestMCPOrchServeReclaimsAfterOwnerKilled(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	first := startServeE2E(t, ctx, binary, fixture)
	if first.cmd.Process == nil {
		t.Fatal("first serve process has no process handle")
	}
	if err := first.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill first serve owner: %v", err)
	}
	select {
	case <-first.done:
		first.wait.Wait()
		first.waitErr = nil
	case <-ctx.Done():
		t.Fatalf("killed serve owner did not exit: %v", ctx.Err())
	}
	second := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 5 * time.Second}
	session, _ := initializeServeE2E(t, client, second.ready.Endpoint, fixture.token)
	assertServeListAgents(t, client, second.ready.Endpoint, fixture.token, session, 2)
	if err := stopServeProcessE2E(t, second, nil); err != nil {
		t.Fatalf("reclaimed serve shutdown: %v", err)
	}
}

// assertServeSecondOwnerRejected 不同配置文件也必须按状态目录识别唯一 owner。
func assertServeSecondOwnerRejected(t *testing.T, ctx context.Context, binary, config string) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "second-ready.json")
	result := runServeE2E(t, ctx, binary, t.TempDir(), "serve", "--config", config, "--ready-file", ready)
	if result.timedOut || result.err == nil {
		t.Fatalf("second instance result invalid: timed_out=%t err_present=%t stdout=%s stderr=%s, want prompt rejection", result.timedOut, result.err != nil, serveE2ETextSummary(result.stdout), serveE2ETextSummary(result.stderr))
	}
	if result.stdout != "" {
		t.Fatalf("singleton rejection wrote stdout: %s", serveE2ETextSummary(result.stdout))
	}
	if !strings.Contains(strings.ToLower(result.stderr), "already running") {
		t.Fatalf("singleton stderr=%s", serveE2ETextSummary(result.stderr))
	}
	if _, err := os.Stat(ready); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second ready file stat=%v, want absent", err)
	}
}

func TestMCPOrchServeRejectsInvalidCLIAndConfig(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	cases := []serveE2EInvalidCase{
		{name: "unknown_flag", args: []string{"--unknown-flag"}, fragments: []string{"unknown", "flag"}},
		{name: "missing_config", fragments: []string{"config"}},
		{name: "relative_config", config: "config.json", ready: "relative-ready.json", fragments: []string{"absolute", "config"}},
		{name: "malformed_config", config: filepath.Join(t.TempDir(), "malformed.json"), ready: filepath.Join(t.TempDir(), "malformed-ready.json"), body: "{malformed\n", fragments: []string{"json", "parse", "malformed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runInvalidServeE2ECase(t, ctx, binary, fixture, tc)
		})
	}
}

type serveE2EInvalidCase struct {
	name, config, ready, body string
	args, fragments           []string
}

func runInvalidServeE2ECase(t *testing.T, ctx context.Context, binary string, fixture serveE2EFixture, tc serveE2EInvalidCase) {
	t.Helper()
	cwd, config := t.TempDir(), tc.config
	if tc.name == "relative_config" {
		config, tc.body = filepath.Join(cwd, config), string(readServeE2EFile(t, fixture.config))
	}
	if config != "" {
		writeServeE2EFile(t, config, []byte(tc.body), 0o600)
	}
	args := append([]string{"serve"}, tc.args...)
	if tc.config != "" {
		args = append(args, "--config", tc.config)
	}
	if tc.ready != "" {
		ready := tc.ready
		if !filepath.IsAbs(ready) {
			ready = filepath.Join(cwd, ready)
		}
		args = append(args, "--ready-file", ready)
	}
	result := runServeE2E(t, ctx, binary, cwd, args...)
	assertInvalidServeE2EResult(t, result, tc.fragments)
}

func assertInvalidServeE2EResult(t *testing.T, result serveE2ECommandResult, fragments []string) {
	t.Helper()
	if result.timedOut || result.err == nil || result.stdout != "" {
		t.Fatalf("invalid invocation result: timed_out=%t err_present=%t stdout=%s stderr=%s", result.timedOut, result.err != nil, serveE2ETextSummary(result.stdout), serveE2ETextSummary(result.stderr))
	}
	lower := strings.ToLower(result.stderr)
	if strings.Contains(lower, "parent launch contract") || strings.Contains(lower, "runtime_resources") {
		t.Fatalf("reached legacy V3 contract: stderr=%s", serveE2ETextSummary(result.stderr))
	}
	if !containsServeE2EFragment(lower, fragments) {
		t.Fatalf("stderr=%s, want one of %v", serveE2ETextSummary(result.stderr), fragments)
	}
}

func buildServeE2EBinary(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	root := serveE2ERepoRoot(t)
	name := "mcp-orch"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	command := exec.CommandContext(ctx, serveE2EGo(t), "build", "-buildvcs=false", "-o", output, "./cmd/mcp-orch")
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build ./cmd/mcp-orch: %v\n%s", err, out)
	}
	return output
}

func serveE2ERepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve E2E caller")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}

func serveE2EGo(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("REAL_GO_BIN")); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatalf("REAL_GO_BIN must be absolute: %q", configured)
		}
		return configured
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(runtime.GOROOT(), "bin", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("resolve real Go %s: %v", path, err)
	}
	return path
}

func writeServeE2EFixture(t *testing.T, binary string) serveE2EFixture {
	t.Helper()
	workspace, state, home, credDir, configDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{workspace, state, home, credDir, configDir} {
		if err := securefs.RestrictPrivateOwnerOnly(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(credDir, "token")
	writeServeE2EFile(t, credential, []byte(serveE2EToken+"\n"), 0o600)
	configPath := filepath.Join(configDir, "serve.json")
	config := map[string]any{"schema_version": 1, "workspace_root": workspace, "state_dir": state, "listen": "127.0.0.1:0", "credential_file": credential, "principal_ref": "operator/e2e", "scope_ref": "workspace/e2e", "providers": []any{map[string]any{"name": "codex", "executable": binary, "home": home, "models": []string{"fixture-model"}, "sandbox": "read-only"}}}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeServeE2EFile(t, configPath, raw, 0o600)
	return serveE2EFixture{config: configPath, workspace: workspace, state: state, token: serveE2EToken}
}

func writeServeE2EStateAliasConfig(t *testing.T, fixture serveE2EFixture) string {
	t.Helper()
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(fixture.state, alias); err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(readServeE2EFile(t, fixture.config), &config); err != nil {
		t.Fatal(err)
	}
	config["state_dir"] = alias
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state-alias-config.json")
	writeServeE2EFile(t, path, raw, 0o600)
	return path
}

func startServeE2E(t *testing.T, parent context.Context, binary string, fixture serveE2EFixture) *serveE2EProcess {
	t.Helper()
	readyDir := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(readyDir, "ready.json")
	process := startServeProcessE2E(t, parent, binary, fixture.workspace, "serve", "--config", fixture.config, "--ready-file", readyPath)
	process.ready = waitServeE2EReady(t, process, readyPath)
	parsed, err := url.Parse(process.ready.Endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/mcp" {
		t.Fatalf("ready endpoint=%q, want http://127.0.0.1:<port>/mcp", process.ready.Endpoint)
	}
	return process
}

func startServeProcessE2E(t *testing.T, parent context.Context, binary, cwd string, args ...string) *serveE2EProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = cwd, serveE2EEnvForOrchConfig(t, args)
	process := &serveE2EProcess{cmd: command, done: make(chan struct{})}
	command.Stdout, command.Stderr = &process.stdout, &process.stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start mcp-orch serve: %v\nstderr=%s", err, serveE2ETextSummary(process.stderr.String()))
	}
	t.Cleanup(func() {
		if err := stopServeProcessE2E(t, process, cancel); err != nil {
			t.Errorf("serve cleanup: %v", err)
		}
	})
	process.wait.Go(func() { process.waitErr = command.Wait(); close(process.done) })
	return process
}

// serveE2EEnvForOrchConfig gives the explicitly selected host_default Claude
// mode access to the logged-in host keychain without weakening isolated tests.
func serveE2EEnvForOrchConfig(t *testing.T, args []string) []string {
	t.Helper()
	env := serveE2EEnv(t.TempDir())
	for index, arg := range args {
		if arg != "--config" || index+1 >= len(args) {
			continue
		}
		raw := string(readServeE2EFile(t, args[index+1]))
		if !strings.Contains(raw, "host_default") {
			return env
		}
		home := strings.TrimSpace(os.Getenv("HOME"))
		if !filepath.IsAbs(home) {
			t.Fatalf("HOME must be absolute for host_default Claude E2E")
		}
		// Host keychain lookup needs the normal login-session environment; retain
		// it only for explicit host_default, while removing provider overrides.
		env = make([]string, 0, len(os.Environ())+1)
		for _, value := range os.Environ() {
			key, _, _ := strings.Cut(value, "=")
			if strings.HasPrefix(key, "ANTHROPIC_") || key == "CLAUDE_CONFIG_DIR" || strings.HasPrefix(key, "SUPER_DOLPHIN_") {
				continue
			}
			env = append(env, value)
		}
		return append(env, "HOME="+home)
	}
	return env
}

func stopServeProcessE2E(t *testing.T, process *serveE2EProcess, cancel context.CancelFunc) error {
	t.Helper()
	if process == nil {
		return nil
	}
	select {
	case <-process.done:
		process.wait.Wait()
		if cancel != nil {
			cancel()
		}
		return process.waitErr
	default:
	}
	if process.cmd.Process != nil {
		_ = process.cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-process.done:
		process.wait.Wait()
		return process.waitErr
	case <-time.After(5 * time.Second):
		return forceStopServeE2E(process, cancel)
	}
}

func forceStopServeE2E(process *serveE2EProcess, cancel context.CancelFunc) error {
	if cancel != nil {
		cancel()
	}
	if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-process.done:
		process.wait.Wait()
		return errors.Join(errors.New("serve did not stop gracefully"), process.waitErr)
	case <-time.After(2 * time.Second):
		return errors.New("serve process did not stop")
	}
}

func waitServeE2EReady(t *testing.T, process *serveE2EProcess, path string) serveE2EReady {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if raw, err := os.ReadFile(path); err == nil {
			var ready serveE2EReady
			if err := json.Unmarshal(raw, &ready); err != nil {
				t.Fatalf("decode ready: %v", err)
			}
			if ready.Endpoint == "" || ready.InstanceID == "" {
				t.Fatalf("ready=%#v, want endpoint and instance_id", ready)
			}
			return ready
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read ready: %v", err)
		}
		select {
		case <-process.done:
			t.Fatalf("serve exited before ready: %v; stderr=%s", process.waitErr, serveE2ETextSummary(process.stderr.String()))
		case <-deadline.C:
			t.Fatalf("serve ready timeout")
		case <-ticker.C:
		}
	}
}

func initializeServeE2E(t *testing.T, client *http.Client, endpoint, token string) (string, string) {
	t.Helper()
	reply := requestServeE2E(t, client, endpoint, token, "", 1, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "standalone-e2e", "version": "1"}})
	if reply.status != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", reply.status, serveE2EPayloadSummary(reply.body))
	}
	decoded := decodeServeE2E(t, reply)
	assertServeReply(t, decoded, 1)
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatal(err)
	}
	if reply.session == "" {
		t.Fatal("initialize omitted Mcp-Session-Id")
	}
	notice := requestServeE2E(t, client, endpoint, token, reply.session, -1, "notifications/initialized", map[string]any{})
	if notice.status != http.StatusAccepted && notice.status != http.StatusOK {
		t.Fatalf("initialized status=%d", notice.status)
	}
	return reply.session, result.ServerInfo.Name
}

func assertServeTools(t *testing.T, client *http.Client, endpoint, token, session string, id int) {
	t.Helper()
	reply := callServeE2E(t, client, endpoint, token, session, id, "tools/list", map[string]any{})
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "list_agents" {
			return
		}
	}
	t.Fatalf("tools/list omitted list_agents: result=%s", serveE2EPayloadSummary(reply.Result))
}

func assertServeListAgents(t *testing.T, client *http.Client, endpoint, token, session string, id int) {
	t.Helper()
	reply := callServeE2E(t, client, endpoint, token, session, id, "tools/call", map[string]any{"name": "list_agents", "arguments": map[string]any{}})
	var result struct {
		Content []struct{ Type, Text string } `json:"content"`
		IsError bool                          `json:"isError"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) == 0 || result.Content[0].Type != "text" {
		t.Fatalf("list_agents result invalid: is_error=%t content_items=%d payload=%s", result.IsError, len(result.Content), serveE2EPayloadSummary(reply.Result))
	}
}

func assertServeEndpointClosed(t *testing.T, client *http.Client, endpoint string) {
	t.Helper()
	request, cancel, err := newServeE2ERequest(t, client, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	response, err := client.Do(request)
	if err == nil {
		_ = response.Body.Close()
		t.Fatalf("endpoint %s remained reachable after daemon shutdown", endpoint)
	}
}

func callServeE2E(t *testing.T, client *http.Client, endpoint, token, session string, id int, method string, params any) serveE2EReply {
	t.Helper()
	reply := requestServeE2E(t, client, endpoint, token, session, id, method, params)
	if reply.status != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", method, reply.status, serveE2EPayloadSummary(reply.body))
	}
	decoded := decodeServeE2E(t, reply)
	assertServeReply(t, decoded, id)
	return decoded
}

func requestServeE2E(t *testing.T, client *http.Client, endpoint, token, session string, id int, method string, params any) serveE2EHTTPReply {
	t.Helper()
	payload := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id >= 0 {
		payload["id"] = id
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel, err := newServeE2ERequest(t, client, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return serveE2EHTTPReply{status: response.StatusCode, session: response.Header.Get("Mcp-Session-Id"), body: body}
}

func decodeServeE2E(t *testing.T, reply serveE2EHTTPReply) serveE2EReply {
	t.Helper()
	var decoded serveE2EReply
	if err := json.Unmarshal(reply.body, &decoded); err != nil {
		t.Fatalf("decode MCP response: %v; payload=%s", err, serveE2EPayloadSummary(reply.body))
	}
	return decoded
}
func assertServeReply(t *testing.T, reply serveE2EReply, id int) {
	t.Helper()
	if reply.JSONRPC != "2.0" {
		t.Fatalf("invalid JSON-RPC response version %q", reply.JSONRPC)
	}
	if reply.Error != nil {
		t.Fatalf("MCP error %d: message=%s", reply.Error.Code, serveE2ETextSummary(reply.Error.Message))
	}
	var got int
	if err := json.Unmarshal(reply.ID, &got); err != nil || got != id {
		t.Fatalf("response id invalid: payload=%s, want %d", serveE2EPayloadSummary(reply.ID), id)
	}
}

func runServeE2E(t *testing.T, parent context.Context, binary, cwd string, args ...string) serveE2ECommandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = cwd, serveE2EEnv(t.TempDir())
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return serveE2ECommandResult{err: err, timedOut: ctx.Err() != nil, stdout: stdout.String(), stderr: stderr.String()}
}

func serveE2EEnv(home string) []string {
	// Keep real-provider E2E output deterministic and avoid opt-in telemetry or
	// error-reporting side effects while still requiring explicit provider auth.
	// Reuse the private temporary home as the process temp root so provider
	// caches and transient files do not land in a shared host temp directory.
	env := []string{"HOME=" + home, "TMPDIR=" + home, "TMP=" + home, "TEMP=" + home,
		"NO_COLOR=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1"}
	for _, key := range []string{"PATH", "LANG", "LC_ALL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// newServeE2ERequest gives every HTTP probe an explicit context deadline. The
// client timeout remains the source of truth for long-running wait_report
// calls; clients without one still receive a bounded default.
func newServeE2ERequest(t *testing.T, client *http.Client, method, endpoint string, body io.Reader) (*http.Request, context.CancelFunc, error) {
	t.Helper()
	if client == nil {
		return nil, nil, errors.New("HTTP client is required")
	}
	timeout := serveE2EDefaultRequestTimeout
	if client.Timeout > 0 {
		timeout = client.Timeout
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return request, cancel, nil
}

// serveE2EPayloadSummary prevents provider/report JSON from being copied into
// test failure output while retaining a bounded size diagnostic.
func serveE2EPayloadSummary(payload []byte) string {
	if len(payload) == 0 {
		return "<empty payload>"
	}
	return fmt.Sprintf("<redacted payload; bytes=%d>", len(payload))
}

func serveE2ETextSummary(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty text>"
	}
	return fmt.Sprintf("<redacted text; bytes=%d>", len(value))
}

func writeServeE2EFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(path, mode); err != nil {
		t.Fatal(err)
	}
}
func readServeE2EFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func containsServeE2EFragment(value string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
