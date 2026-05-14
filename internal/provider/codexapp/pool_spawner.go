package codexapp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// runPoolSpawn spawns a codex app-server for a pool-managed codexHome
// and wires the running process into a fresh *transport.
//
// The recipe mirrors (*transport).spawnLocal but differs in two
// pool-specific ways:
//
//   - The command is produced by BuildPoolSpawnCmd, which installs the
//     env allowlist (stripping everything outside the closed set) and
//     injects CODEX_HOME=home so a stale parent value cannot leak in.
//   - On success the PID is registered with the shared pidregistry so
//     a crash of the parent process still lets the sweeper reap the
//     app-server instead of leaving it behind.
//
// On failure the caller gets an error already enriched with the stderr
// tail, which the pool caches in the identity+owner backoff slot.
func runPoolSpawn(ctx context.Context, home string, registry *pidregistry.Registry, logger *slog.Logger) (*transport, error) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	startupCtx, cancel := withTimeout(ctx, transportReadyTimeout)
	defer cancel()
	cmd, err := BuildPoolSpawnCmd(startupCtx, PoolSpawnArgs{Home: home})
	if err != nil {
		return nil, err
	}
	workDir := cmd.Dir
	cmd.Stdout = io.Discard
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codexapp: pool stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codexapp: pool start: %w", err)
	}
	t := &transport{}
	proc := newLocalProcess(cmd, stderr)
	proc.guard = attachProcessGuard(cmd)
	proc.waitAsync()
	go t.collectProcessStderr(proc, stderr)
	serverURL, err := proc.waitForListenURL(startupCtx)
	if err != nil {
		_ = proc.signal(sigForceKill)
		proc.waitForExit(transportKillWaitTimeout)
		proc.waitForStderr(time.Second)
		proc.guard.close()
		return nil, enrichSpawnError(err, proc)
	}
	t.stateMu.Lock()
	t.local = true
	t.serverURL = serverURL
	t.process = proc
	t.processErr = nil
	t.stateMu.Unlock()
	go t.watchLocalProcess(proc)
	if registry != nil {
		if pid := t.localPID(); pid > 0 {
			registry.Register(pid, "codex-app-server-pool", map[string]string{"codex_home": home, "work_dir": workDir})
		}
	}
	// Establish a control WebSocket + JSON-RPC initialize handshake so
	// codex stays awake. Mirrors ServerManager.start: without this
	// call, codex app-server sees zero clients after boot, times
	// itself out, and Alive() starts reporting false within hundreds
	// of ms. Sessions layered on top will still create their own WS
	// connections; this one is the pool-owned keep-alive.
	if err := t.establish(startupCtx); err != nil {
		_ = t.shutdownTransport(false)
		return nil, fmt.Errorf("codexapp: pool establish: %w", err)
	}
	logger.Info("codexapp: pool spawned app-server",
		slog.String("codex_home", home),
		slog.String("work_dir", workDir),
		slog.String("server_url", serverURL),
	)
	return t, nil
}

type poolSpawnWorkDirContextKey struct{}

func withPoolSpawnWorkDir(ctx context.Context, raw string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ctx
	}
	return context.WithValue(ctx, poolSpawnWorkDirContextKey{}, raw)
}

func poolSpawnWorkDir(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(poolSpawnWorkDirContextKey{}).(string)
	return strings.TrimSpace(value)
}

func poolSpawnNormalizedWorkDir(ctx context.Context) (string, error) {
	if err := poolSpawnContextErr(ctx); err != nil {
		return "", err
	}
	return normalizePoolSpawnWorkDir(poolSpawnWorkDir(ctx))
}

func poolSpawnContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func normalizePoolSpawnWorkDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	clean := filepath.Clean(raw)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("codexapp: pool work dir must be absolute: %q", raw)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("codexapp: pool work dir stat %q: %w", clean, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("codexapp: pool work dir is not a directory: %q", clean)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("codexapp: pool work dir realpath %q: %w", clean, err)
	}
	return real, nil
}

func buildPoolSpawnEnv(parent []string, home, workDir string) []string {
	overrides := map[string]string{"CODEX_HOME": strings.TrimSpace(home)}
	if workDir = strings.TrimSpace(workDir); workDir != "" {
		overrides["PWD"] = workDir
	}
	return buildAllowlistedSpawnEnv(parent, overrides)
}

// NewTransportSpawner returns a Spawner suitable for ServerPool. Each
// invocation launches a fresh codex app-server bound to the requested
// codexHome and wraps it in a SpawnedServer view via wrapTransport.
//
// The returned Spawner does not itself enforce concurrency or deduplication;
// the pool owns those decisions per identity+owner entry.
func NewTransportSpawner(registry *pidregistry.Registry, logger *slog.Logger) Spawner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return func(ctx context.Context, home string) (SpawnedServer, error) {
		t, err := runPoolSpawn(ctx, home, registry, logger)
		if err != nil {
			return nil, err
		}
		return wrapTransport(t), nil
	}
}
