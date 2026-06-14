package codexapp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
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
// runPoolSpawn 运行poolspawn。
func runPoolSpawn(ctx context.Context, home, modelProvider string, registry *pidregistry.Registry, logger *slog.Logger) (*transport, error) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if err := ensureCodexCLIAvailable(ctx); err != nil {
		return nil, err
	}
	startupCtx, cancel := withTimeout(ctx, transportReadyTimeout)
	defer cancel()
	cmd, err := BuildPoolSpawnCmd(startupCtx, PoolSpawnArgs{
		Home:      home,
		ExtraArgs: append(poolSpawnNativeLSPConfigOverrideArgs([]string{"model_provider=" + tomlString(modelProvider)}), poolSpawnAppServerArgs(startupCtx)...),
	})
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
type poolSpawnAppServerArgsContextKey struct{}
type poolSpawnPolicySignatureContextKey struct{}
type poolSpawnWorkspaceRootsContextKey struct{}
type poolSpawnMCPBinaryDirContextKey struct{}

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

func withPoolSpawnNativeToolPolicy(ctx context.Context, policy codexNativeToolPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	args := policy.AppServerArgs()
	if len(args) == 0 {
		return ctx
	}
	ctx = context.WithValue(ctx, poolSpawnAppServerArgsContextKey{}, args)
	return context.WithValue(ctx, poolSpawnPolicySignatureContextKey{}, policy.ProcessSignature())
}

func withPoolSpawnLSPConfig(ctx context.Context, roots []string, binaryDir string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if normalized := normalizePoolSpawnWorkspaceRoots(roots); len(normalized) > 0 {
		ctx = context.WithValue(ctx, poolSpawnWorkspaceRootsContextKey{}, normalized)
	}
	if binaryDir = strings.TrimSpace(binaryDir); binaryDir != "" {
		ctx = context.WithValue(ctx, poolSpawnMCPBinaryDirContextKey{}, binaryDir)
	}
	return ctx
}

func poolSpawnAppServerArgs(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(poolSpawnAppServerArgsContextKey{}).([]string)
	return append([]string(nil), value...)
}

func poolSpawnPolicySignature(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(poolSpawnPolicySignatureContextKey{}).(string)
	return joinPoolSpawnSignatureParts(strings.TrimSpace(value), poolSpawnLSPConfigSignature(ctx))
}

func poolSpawnWorkspaceRoots(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(poolSpawnWorkspaceRootsContextKey{}).([]string)
	return append([]string(nil), value...)
}

func poolSpawnMCPBinaryDir(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(poolSpawnMCPBinaryDirContextKey{}).(string)
	return strings.TrimSpace(value)
}

func poolSpawnLSPConfigSignature(ctx context.Context) string {
	roots := poolSpawnWorkspaceRoots(ctx)
	binaryDir := poolSpawnMCPBinaryDir(ctx)
	if len(roots) == 0 && binaryDir == "" {
		return ""
	}
	return "lsp_roots=" + strings.Join(roots, "\x1f") + "\nlsp_binary_dir=" + binaryDir
}

func joinPoolSpawnSignatureParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n")
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

// normalizePoolSpawnWorkDir 规范化poolspawnwork目录。
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

func normalizePoolSpawnWorkspaceRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := map[string]struct{}{}
	base := ""
	for i, root := range roots {
		root = normalizePoolSpawnWorkspaceRoot(base, root)
		if root == "" {
			continue
		}
		if i == 0 {
			base = root
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out
}

// normalizePoolSpawnWorkspaceRoot 规范化poolspawn工作区根目录。
func normalizePoolSpawnWorkspaceRoot(base, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if strings.TrimSpace(base) != "" && !filepath.IsAbs(root) {
		root = filepath.Join(base, root)
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	if strings.TrimSpace(base) == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return ""
}

func buildPoolSpawnEnv(parent []string, home, workDir string) []string {
	overrides := map[string]string{"CODEX_HOME": strings.TrimSpace(home)}
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		overrides["OPENAI_API_KEY"] = key
	}
	if workDir = strings.TrimSpace(workDir); workDir != "" {
		overrides["PWD"] = workDir
	}
	return buildAllowlistedSpawnEnv(parent, overrides)
}

// codexSpawnEnvAllowlist is the set of parent-process environment variables
// that are propagated into a spawned codex app-server child. Everything else
// (including rogue CODEX_* / OPENAI_* pollutants left over from another
// instance) is dropped so CODEX_HOME is the sole identity authority.
var codexSpawnEnvAllowlist = []string{
	"PATH",
	"HOME",
	"USER",
	"LOGNAME",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LC_MESSAGES",
	"TZ",
	"TMPDIR",
	"TEMP",
	"TMP",
	"SHELL",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	sidecarRuntimeModeEnv, sidecarRuntimeResourcesEnv, providershared.SuperDolphinHomeEnv,
	codexRelayBootstrapTokenEnv,
}

// buildAllowlistedSpawnEnv 构建allowlistedspawnenv。
func buildAllowlistedSpawnEnv(parent []string, overrides map[string]string) []string {
	allowed := make(map[string]struct{}, len(codexSpawnEnvAllowlist))
	for _, key := range codexSpawnEnvAllowlist {
		allowed[strings.ToUpper(key)] = struct{}{}
	}
	merged := make(map[string]string, len(allowed)+len(overrides))
	for _, kv := range parent {
		key, val, ok := splitEnv(kv)
		if !ok {
			continue
		}
		if _, permitted := allowed[strings.ToUpper(key)]; !permitted {
			continue
		}
		merged[key] = val
	}
	for key, val := range overrides {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		merged[key] = val
	}
	out := make([]string, 0, len(merged))
	for key, val := range merged {
		out = append(out, key+"="+val)
	}
	sort.Strings(out)
	return providershared.EnsureLoopbackNoProxy(out)
}

func splitEnv(kv string) (string, string, bool) {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(kv[:idx]), kv[idx+1:], true
}

// NewTransportSpawner returns a Spawner suitable for ServerPool. Each
// invocation launches a fresh codex app-server bound to the requested
// codexHome and wraps it in a SpawnedServer view via wrapTransport.
//
// The returned Spawner does not itself enforce concurrency or deduplication;
// the pool owns those decisions per identity+owner entry.
// NewTransportSpawner 创建传输spawner。
func NewTransportSpawner(registry *pidregistry.Registry, logger *slog.Logger) Spawner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return func(ctx context.Context, home, modelProvider string) (SpawnedServer, error) {
		t, err := runPoolSpawn(ctx, home, modelProvider, registry, logger)
		if err != nil {
			return nil, err
		}
		return wrapTransport(t), nil
	}
}
