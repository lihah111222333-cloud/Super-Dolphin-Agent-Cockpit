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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// runPoolSpawn 为池管理的 codexHome 启动 codex app-server，并把进程封装成新的 transport。
// 它必须复用池专属的命令构造和环境白名单，避免父进程残留的 CODEX_HOME 或数据库变量泄漏。
// 启动失败时返回带 stderr 尾部的错误，调用方可直接写入池的退避状态。
func runPoolSpawn(ctx context.Context, home, modelProvider string, registry *pidregistry.Registry, logger *slog.Logger) (*transport, error) {
	logger = defaultCodexAppLogger(logger)
	if err := ensureCodexCLIAvailable(ctx); err != nil {
		return nil, err
	}
	startupCtx, cancel := withTimeout(ctx, transportReadyTimeout)
	defer cancel()
	cmd, err := BuildPoolSpawnCmd(startupCtx, PoolSpawnArgs{Home: home, ExtraArgs: append(poolSpawnNativeLSPConfigOverrideArgs([]string{"model_provider=" + tomlString(modelProvider)}), poolSpawnAppServerArgs(startupCtx)...)})
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
	t.startCollectProcessStderr(proc, stderr)
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
	t.startWatchLocalProcess(proc)
	if registry != nil {
		if pid := t.localPID(); pid > 0 {
			if err := registry.RegisterChecked(pid, "codex-app-server-pool", map[string]string{"codex_home": home, "work_dir": workDir}); err != nil {
				_ = t.shutdownTransport(false)
				return nil, fmt.Errorf("codexapp: pool pidregistry register: %w", err)
			}
		}
	}
	// 建立池持有的控制 WebSocket 和 initialize 握手，避免 app-server 启动后因零客户端超时退出。
	// 上层 session 仍会创建自己的连接；这里的连接只负责维持池中进程可用性。
	if err := t.establish(startupCtx); err != nil {
		_ = t.shutdownTransport(false)
		return nil, fmt.Errorf("codexapp: pool establish: %w", err)
	}
	fields := []any{"server_url", serverURL}
	fields = append(fields, platformshared.SafePathLogFields("codex_home", home)...)
	fields = append(fields, platformshared.SafePathLogFields("work_dir", workDir)...)
	logger.Info("codexapp: pool spawned app-server", fields...)
	return t, nil
}

type poolSpawnWorkDirContextKey struct{}
type poolSpawnAppServerArgsContextKey struct{}
type poolSpawnPolicySignatureContextKey struct{}
type poolSpawnWorkspaceRootsContextKey struct{}
type poolSpawnMCPBinaryDirContextKey struct{}

func withPoolSpawnWorkDir(ctx context.Context, raw string) context.Context {
	ctx = nonNilContext(ctx)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ctx
	}
	return context.WithValue(ctx, poolSpawnWorkDirContextKey{}, raw)
}

func poolSpawnWorkDir(ctx context.Context) string {
	return strings.TrimSpace(poolSpawnContextValue[string](ctx, poolSpawnWorkDirContextKey{}))
}

func withPoolSpawnNativeToolPolicy(ctx context.Context, policy codexNativeToolPolicy) context.Context {
	ctx = nonNilContext(ctx)
	args := policy.AppServerArgs()
	if len(args) == 0 {
		return ctx
	}
	ctx = context.WithValue(ctx, poolSpawnAppServerArgsContextKey{}, args)
	return context.WithValue(ctx, poolSpawnPolicySignatureContextKey{}, policy.ProcessSignature())
}

func withPoolSpawnLSPConfig(ctx context.Context, roots []string, binaryDir string) context.Context {
	ctx = nonNilContext(ctx)
	if normalized := normalizePoolSpawnWorkspaceRoots(roots); len(normalized) > 0 {
		ctx = context.WithValue(ctx, poolSpawnWorkspaceRootsContextKey{}, normalized)
	}
	if binaryDir = strings.TrimSpace(binaryDir); binaryDir != "" {
		ctx = context.WithValue(ctx, poolSpawnMCPBinaryDirContextKey{}, binaryDir)
	}
	return ctx
}

func poolSpawnAppServerArgs(ctx context.Context) []string {
	return append([]string(nil), poolSpawnContextValue[[]string](ctx, poolSpawnAppServerArgsContextKey{})...)
}

func poolSpawnPolicySignature(ctx context.Context) string {
	value := strings.TrimSpace(poolSpawnContextValue[string](ctx, poolSpawnPolicySignatureContextKey{}))
	lsp := poolSpawnLSPConfigSignature(ctx)
	if value == "" {
		return lsp
	}
	if lsp == "" {
		return value
	}
	return value + "\n" + lsp
}

func poolSpawnWorkspaceRoots(ctx context.Context) []string {
	return append([]string(nil), poolSpawnContextValue[[]string](ctx, poolSpawnWorkspaceRootsContextKey{})...)
}

func poolSpawnMCPBinaryDir(ctx context.Context) string {
	return strings.TrimSpace(poolSpawnContextValue[string](ctx, poolSpawnMCPBinaryDirContextKey{}))
}

func poolSpawnContextValue[T any](ctx context.Context, key any) (value T) {
	if ctx != nil {
		value, _ = ctx.Value(key).(T)
	}
	return value
}

func poolSpawnLSPConfigSignature(ctx context.Context) string {
	roots := poolSpawnWorkspaceRoots(ctx)
	binaryDir := poolSpawnMCPBinaryDir(ctx)
	if len(roots) == 0 && binaryDir == "" {
		return ""
	}
	return "lsp_roots=" + strings.Join(roots, "\x1f") + "\nlsp_binary_dir=" + binaryDir
}

func poolSpawnNormalizedWorkDir(ctx context.Context) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	return normalizePoolSpawnWorkDir(poolSpawnWorkDir(ctx))
}

// normalizePoolSpawnWorkDir 校验池启动工作目录并解析真实路径。
// 这里只接受已存在的绝对目录，避免子进程在未知相对路径下继承错误的项目上下文。
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

// normalizePoolSpawnWorkspaceRoot 按首个 workspace root 解析后续相对根路径。
// 空值或无法定位到绝对路径的条目会被丢弃，确保传给 mcp-lsp 的根目录稳定可复现。
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

// codexSpawnEnvAllowlist 列出允许传给子 app-server 的父进程环境变量。
// 其他 CODEX_*、OPENAI_* 污染项会被剥离，确保 CODEX_HOME 是唯一身份来源。
var codexSpawnEnvAllowlist = []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "TZ", "TMPDIR", "TEMP", "TMP", "SHELL", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", sidecarRuntimeModeEnv, sidecarRuntimeResourcesEnv, providershared.SuperDolphinHomeEnv, codexRelayBootstrapTokenEnv}

// buildAllowlistedSpawnEnv 从父环境中只保留允许传递给 codex app-server 的变量。
// overrides 会在数据库环境拦截后覆盖同名值，最终再补齐 loopback no_proxy 以保证本地握手可达。
func buildAllowlistedSpawnEnv(parent []string, overrides map[string]string) []string {
	allowed := make(map[string]struct{}, len(codexSpawnEnvAllowlist))
	for _, key := range codexSpawnEnvAllowlist {
		allowed[strings.ToUpper(key)] = struct{}{}
	}
	merged := make(map[string]string, len(allowed)+len(overrides))
	for _, kv := range contract.ScrubDatabaseEnv(parent) {
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
		if key == "" || contract.IsForbiddenDatabaseEnvKey(key) {
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

// NewTransportSpawner 构造供 ServerPool 使用的 app-server 启动器。
// 每次调用都会为指定 codexHome 启动独立进程；并发、去重和释放由池按 identity+owner 负责。
func NewTransportSpawner(registry *pidregistry.Registry, logger *slog.Logger) Spawner {
	logger = defaultCodexAppLogger(logger)
	return func(ctx context.Context, home, modelProvider string) (SpawnedServer, error) {
		t, err := runPoolSpawn(ctx, home, modelProvider, registry, logger)
		if err != nil {
			return nil, err
		}
		return wrapTransport(t), nil
	}
}

func defaultCodexAppLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return pkglogger.Get()
}
