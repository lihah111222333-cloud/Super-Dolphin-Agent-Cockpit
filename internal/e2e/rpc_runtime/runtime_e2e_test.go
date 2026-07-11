//go:build e2e

package rpc_runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"golang.org/x/sync/errgroup"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const (
	readyFileEnv     = "SUPER_DOLPHIN_RPC_READY_FILE"
	controlRPCAddr   = "GO_AGENT_CTL_RPC_ADDR"
	controlRPCToken  = "GO_AGENT_CTL_SESSION_TOKEN"
	testSessionToken = "sd-test-rpc-runtime-token"
)

func TestAgentRuntimeRPCBlackBox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := repoRoot(t)
	runtimeBin := buildAgentRuntime(t, ctx, root)
	home := t.TempDir()
	readyPath := filepath.Join(home, "rpc-ready.json")
	output := &lockedBuffer{}

	cmd := exec.CommandContext(ctx, runtimeBin)
	cmd.Dir = root
	cmd.Env = isolatedRuntimeEnv(home, readyPath, testSessionToken)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent-runtime: %v\noutput:\n%s", err, output.String())
	}
	waiter := waitForCommand(cmd)
	t.Cleanup(func() {
		cancel()
		select {
		case <-waiter.Done():
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-waiter.Done()
		}
	})

	ready, err := waitReadyFile(ctx, readyPath, waiter, output)
	if err != nil {
		t.Fatalf("wait agent-runtime ready: %v", err)
	}

	client, closeClient, err := dialRPC(ctx, ready.ControlRPCAddr())
	if err != nil {
		t.Fatalf("dial control rpc %q: %v\noutput:\n%s", ready.ControlRPCAddr(), err, output.String())
	}
	defer closeClient()

	registerRPCPeer(t, ctx, client, testSessionToken)
	status := callObservabilityStatus(t, ctx, client)
	if len(status) == 0 {
		t.Fatal("observability/status returned empty object")
	}
}

func TestRPCRuntimeE2EEnvIsIsolated(t *testing.T) {
	home := t.TempDir()
	readyPath := filepath.Join(home, "rpc-ready.json")
	env := isolatedRuntimeEnv(home, readyPath, testSessionToken)
	got := envMap(env)

	assertRequiredRuntimeEnv(t, got)
	assertExpectedRuntimeEnvValues(t, got, home, readyPath)
	assertForbiddenRuntimeEnvAbsent(t, got)
}

func assertRequiredRuntimeEnv(t *testing.T, got map[string]string) {
	t.Helper()
	for _, key := range []string{
		"SUPER_DOLPHIN_HOME",
		"SUPER_DOLPHIN_SQLITE_PATH",
		controlRPCAddr,
		controlRPCToken,
		readyFileEnv,
		"SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP",
		"LOG_LEVEL",
	} {
		if strings.TrimSpace(got[key]) == "" {
			t.Fatalf("isolated runtime env missing %s", key)
		}
	}
}

func assertExpectedRuntimeEnvValues(t *testing.T, got map[string]string, home string, readyPath string) {
	t.Helper()
	if got["SUPER_DOLPHIN_HOME"] != home {
		t.Fatalf("SUPER_DOLPHIN_HOME = %q, want %q", got["SUPER_DOLPHIN_HOME"], home)
	}
	if got["SUPER_DOLPHIN_SQLITE_PATH"] != filepath.Join(home, "super-dolphin.db") {
		t.Fatalf("SUPER_DOLPHIN_SQLITE_PATH = %q, want test-owned sqlite path", got["SUPER_DOLPHIN_SQLITE_PATH"])
	}
	if got[controlRPCAddr] != "127.0.0.1:0" {
		t.Fatalf("%s = %q, want ephemeral tcp listener", controlRPCAddr, got[controlRPCAddr])
	}
	if got[controlRPCToken] != testSessionToken {
		t.Fatalf("%s = %q, want test token", controlRPCToken, got[controlRPCToken])
	}
	if got[readyFileEnv] != readyPath {
		t.Fatalf("%s = %q, want %q", readyFileEnv, got[readyFileEnv], readyPath)
	}
	if got["SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP"] != "desktop_host" {
		t.Fatalf("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP = %q, want desktop_host", got["SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP"])
	}
}

func assertForbiddenRuntimeEnvAbsent(t *testing.T, got map[string]string) {
	t.Helper()
	for _, key := range []string{
		"SUPER_DOLPHIN_INTERNAL_SQLITE_PATH",
		"DATABASE_URL",
		"POSTGRES_CONNECTION_STRING",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("isolated runtime env inherited forbidden %s", key)
		}
	}
}

func buildAgentRuntime(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	runtimeBin := filepath.Join(t.TempDir(), "agent-runtime")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", runtimeBin, "./cmd/agent-runtime")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/agent-runtime: %v\n%s", err, string(out))
	}
	return runtimeBin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func isolatedRuntimeEnv(home string, readyPath string, token string) []string {
	env := make([]string, 0, 12)
	for _, key := range []string{"PATH", "TMPDIR", "TEMP", "TMP"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env = upsertEnv(env, key, value)
		}
	}
	sqlitePath := filepath.Join(home, "super-dolphin.db")
	env = upsertEnv(env, "HOME", home)
	env = upsertEnv(env, "SUPER_DOLPHIN_HOME", home)
	env = upsertEnv(env, "SUPER_DOLPHIN_SQLITE_PATH", sqlitePath)
	env = upsertEnv(env, controlRPCAddr, "127.0.0.1:0")
	env = upsertEnv(env, controlRPCToken, token)
	env = upsertEnv(env, readyFileEnv, readyPath)
	env = upsertEnv(env, "SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "desktop_host")
	env = upsertEnv(env, "LOG_LEVEL", "debug")
	return env
}

func upsertEnv(env []string, key string, value string) []string {
	prefix := key + "="
	entry := prefix + value
	for i, current := range env {
		if strings.HasPrefix(current, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func envMap(env []string) map[string]string {
	got := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			got[key] = value
		}
	}
	return got
}

type rpcReadyFile struct {
	RPCAddr    string `json:"rpc_addr"`
	Addr       string `json:"addr"`
	ListenAddr string `json:"listen_addr"`
}

func (r rpcReadyFile) ControlRPCAddr() string {
	for _, candidate := range []string{r.RPCAddr, r.Addr, r.ListenAddr} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func waitReadyFile(ctx context.Context, path string, waiter *processWaiter, output fmt.Stringer) (rpcReadyFile, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return rpcReadyFile{}, fmt.Errorf("context done before ready file %s: %w\noutput:\n%s", path, ctx.Err(), output.String())
		case <-waiter.Done():
			return rpcReadyFile{}, fmt.Errorf("agent-runtime exited before ready file %s: %v\noutput:\n%s", path, waiter.Err(), output.String())
		case <-ticker.C:
			ready, err := readReadyFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return rpcReadyFile{}, fmt.Errorf("read ready file %s: %w\noutput:\n%s", path, err, output.String())
			}
			return ready, nil
		}
	}
}

func readReadyFile(path string) (rpcReadyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return rpcReadyFile{}, err
	}
	var ready rpcReadyFile
	if err := json.Unmarshal(data, &ready); err != nil {
		return rpcReadyFile{}, err
	}
	if ready.ControlRPCAddr() == "" {
		return rpcReadyFile{}, fmt.Errorf("ready file missing rpc_addr")
	}
	return ready, nil
}

func dialRPC(ctx context.Context, addr string) (*jrpc2.Client, func(), error) {
	var dialer net.Dialer
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	client := jrpc2.NewClient(channel.Line(raw, raw), nil)
	return client, func() {
		client.Close()
		_ = raw.Close()
	}, nil
}

func registerRPCPeer(t *testing.T, ctx context.Context, client *jrpc2.Client, token string) {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req := mcpdto.RegisterRequest{
		InstanceID:   "rpc-runtime-e2e",
		BinaryName:   "rpc-runtime-e2e",
		PID:          os.Getpid(),
		SessionToken: token,
		ClientKind:   mcpdto.ClientKindCustom,
		PeerKind:     mcpdto.PeerKindTool,
	}
	var resp mcpdto.RegisterResponse
	if err := client.CallResult(callCtx, mcpdto.MethodRegister, req, &resp); err != nil {
		t.Fatalf("ctl/register error = %v", err)
	}
	if resp.InstanceID != req.InstanceID {
		t.Fatalf("ctl/register instance_id = %q, want %q", resp.InstanceID, req.InstanceID)
	}
}

func callObservabilityStatus(t *testing.T, ctx context.Context, client *jrpc2.Client) map[string]any {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var status map[string]any
	if err := client.CallResult(callCtx, "observability/status", map[string]any{}, &status); err != nil {
		t.Fatalf("observability/status error = %v", err)
	}
	return status
}

type processWaiter struct {
	done chan struct{}
	g    errgroup.Group
	mu   sync.Mutex
	err  error
}

func waitForCommand(cmd *exec.Cmd) *processWaiter {
	waiter := &processWaiter{done: make(chan struct{})}
	waiter.g.Go(func() error {
		err := cmd.Wait()
		waiter.mu.Lock()
		waiter.err = err
		waiter.mu.Unlock()
		close(waiter.done)
		return nil
	})
	return waiter
}

func (w *processWaiter) Done() <-chan struct{} {
	return w.done
}

func (w *processWaiter) Err() error {
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
