package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// poolRoutingEnvVar 是进程级 ServerPool 路由开关。
// 未设置时默认启用 strict pool，携带 Codex identity 的会话不能退回共享 ServerManager。
const (
	poolRoutingEnvVar         = "CODEXAPP_USE_POOL"
	defaultCodexInstanceKey   = "default"
	defaultCodexModelProvider = defaultBootstrapModelProvider
	localCodexModelProvider   = "openai"
)

// prepareStartSessionRequest 解析 Codex provider home 并写入启动身份配置。
// provider-native skills 由 Codex CLI 根据 cwd/home 自己发现；该层不再生成或校验集中式 skill tree。
func (d *driver) prepareStartSessionRequest(_ context.Context, req dto.StartSessionRequest) (dto.StartSessionRequest, error) {
	if err := validateCodexProviderNativeSkillsConfig(req.Config); err != nil {
		return req, err
	}
	canonicalCWD, err := canonicalizeProviderNativeDiscoveryCWD(req.CWD)
	if err != nil {
		return req, err
	}
	req.CWD = canonicalCWD
	if err := validateStartCodexIdentityShape(req.Config); err != nil {
		return req, err
	}
	if err := validateCodexNativeToolPolicyConfig(req.Config); err != nil {
		return req, err
	}
	requestedHome := providershared.ConfigString(req.Config, contract.CodexHomeKey)
	providerHome, err := selectCodexProviderHome(requestedHome)
	if err != nil {
		return req, err
	}
	if providerHome.explicitAppManagedHome {
		if err := validateAppManagedRelayLaunchEnv(); err != nil {
			return req, err
		}
	}
	home, err := ensureResolvedCodexProviderHome(providerHome)
	if err != nil {
		return req, err
	}
	config, err := withDefaultCodexIdentity(req.Config, home, defaultCodexModelProviderForHome(providerHome))
	if err != nil {
		return req, err
	}
	req.Config = config
	return req, nil
}

// validateStartCodexIdentityShape 校验启动配置里的 Codex 身份字段类型。
// 只接受 string，避免 map/数组等值穿透到 provider home 选择和 pool identity。
func validateStartCodexIdentityShape(config map[string]any) error {
	for _, key := range []string{contract.CodexHomeKey, contract.CodexInstanceKeyKey, contract.CodexModelProviderKey} {
		if raw, ok := config[key]; ok && raw != nil {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("%w: %q must be string, got %T", providershared.ErrCodexIdentityInvalidType, key, raw)
			}
		}
	}
	return nil
}

// prepareResumeSessionRequest 恢复会话前重新解析 Codex home；provider-native skills 由 CLI 自己发现。
// Resume 依赖历史绑定身份，解析失败时必须阻断，不能退回默认 home。
func (d *driver) prepareResumeSessionRequest(ctx context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	if err := validateCodexProviderNativeSkillsConfig(req.Config); err != nil {
		return req, err
	}
	canonicalCWD, err := canonicalizeProviderNativeDiscoveryCWD(req.CWD)
	if err != nil {
		return req, err
	}
	req.CWD = canonicalCWD
	req, err = d.ResolveResumeSessionIdentity(ctx, req)
	if err != nil {
		return req, err
	}
	requestedHome := req.CodexHome
	providerHome, err := selectCodexProviderHome(requestedHome)
	if err != nil {
		return req, err
	}
	if providerHome.explicitAppManagedHome {
		if err := validateAppManagedRelayLaunchEnv(); err != nil {
			return req, err
		}
	}
	home, err := ensureResolvedCodexProviderHome(providerHome)
	if err != nil {
		return req, err
	}
	req.CodexHome = home
	return req, nil
}

// canonicalizeProviderNativeDiscoveryCWD 保留 provider-native discovery 所需的
// cwd/path 安全边界，并返回 provider CLI/app-server 实际应使用的 canonical 路径。
// 该校验只确认 CLI 的工作目录可用，不读取或写入 Super-Dolphin skill tree。
func canonicalizeProviderNativeDiscoveryCWD(cwd string) (string, error) {
	trimmed := strings.TrimSpace(cwd)
	if trimmed == "" {
		return "", errors.New("codexapp: provider-native skills cwd is required")
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("codexapp: provider-native skills cwd must be absolute: %s", cwd)
	}
	canonical, err := contract.CanonicalizeProviderNativeCWD(trimmed)
	if err != nil {
		return "", fmt.Errorf("codexapp: provider-native skills cwd: %w", err)
	}
	return canonical, nil
}

// validateProviderNativeDiscoveryCWD 保留旧内部调用名；新调用方应使用
// canonicalizeProviderNativeDiscoveryCWD 并把返回路径写回请求。
func validateProviderNativeDiscoveryCWD(cwd string) error {
	_, err := canonicalizeProviderNativeDiscoveryCWD(cwd)
	return err
}

// buildCodexProviderNativeContext constructs the identity shared by start,
// resume and recovery.  Capability status remains explicit unknown until a
// provider-owned probe supplies evidence; launching app-server alone is not a
// supported-capability claim.
func buildCodexProviderNativeContext(req dto.StartSessionRequest) (contract.ProviderNativeContext, error) {
	return providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     "codex",
		CWD:          req.CWD,
		ProviderHome: providershared.ConfigString(req.Config, contract.CodexHomeKey),
		Profile:      providershared.ConfigString(req.Config, contract.CodexInstanceKeyKey),
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
}

func buildCodexResumeNativeContext(req dto.ResumeSessionRequest) (contract.ProviderNativeContext, error) {
	return providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     "codex",
		CWD:          req.CWD,
		ProviderHome: req.CodexHome,
		Profile:      req.CodexInstanceKey,
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
}

// prepareCodexStartBoundary 收敛 start 的请求、CWD 和 native context 校验。
// 返回值只包含已经 canonicalize/Validate 的对象，后续启动阶段不得重新读取原始路径。
func (d *driver) prepareCodexStartBoundary(ctx context.Context, req dto.StartSessionRequest) (dto.StartSessionRequest, contract.ProviderNativeContext, error) {
	if !codexPromptSnapshotBlank(req.StartAssembly.Snapshot) {
		if err := contract.ValidateResumePromptSnapshotForProvider(req.StartAssembly.Snapshot, "codex"); err != nil {
			return dto.StartSessionRequest{}, contract.ProviderNativeContext{}, fmt.Errorf("codexapp: start prompt snapshot: %w", err)
		}
	}
	prepared, err := d.prepareStartSessionRequest(ctx, req)
	if err != nil {
		return dto.StartSessionRequest{}, contract.ProviderNativeContext{}, err
	}
	nativeContext, err := buildCodexProviderNativeContext(prepared)
	if err != nil {
		return dto.StartSessionRequest{}, contract.ProviderNativeContext{}, fmt.Errorf("codexapp: start native context: %w", err)
	}
	if err := providershared.ValidateProviderNativeContextForProvider(nativeContext, "codex"); err != nil {
		return dto.StartSessionRequest{}, contract.ProviderNativeContext{}, fmt.Errorf("codexapp: start native context: %w", err)
	}
	return prepared, nativeContext, nil
}

// prepareCodexResumeBoundary 收敛 resume 的身份、CWD 和 native context 校验。
// provider thread、prompt snapshot 与 context 任一边界失败都会在 transport 之前返回。
func (d *driver) prepareCodexResumeBoundary(ctx context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, contract.ProviderNativeContext, error) {
	providerThreadID, err := requireProviderResumeThreadID("codexapp", req.ProviderThreadID)
	if err != nil {
		return dto.ResumeSessionRequest{}, contract.ProviderNativeContext{}, err
	}
	if err := contract.ValidateResumePromptSnapshotForProvider(req.PromptSnapshot, d.Name()); err != nil {
		return dto.ResumeSessionRequest{}, contract.ProviderNativeContext{}, fmt.Errorf("codexapp: %w", err)
	}
	req.ProviderThreadID = providerThreadID
	prepared, err := d.prepareResumeSessionRequest(ctx, req)
	if err != nil {
		return dto.ResumeSessionRequest{}, contract.ProviderNativeContext{}, err
	}
	nativeContext, err := buildCodexResumeNativeContext(prepared)
	if err != nil {
		return dto.ResumeSessionRequest{}, contract.ProviderNativeContext{}, fmt.Errorf("codexapp: resume native context: %w", err)
	}
	if err := providershared.ValidateProviderNativeContextForProvider(nativeContext, "codex"); err != nil {
		return dto.ResumeSessionRequest{}, contract.ProviderNativeContext{}, fmt.Errorf("codexapp: resume native context: %w", err)
	}
	return prepared, nativeContext, nil
}

func matchesAppManagedCodexHome(requested string) bool {
	home, err := providershared.AppManagedProviderHome(providershared.ProviderCodex)
	if err != nil {
		return false
	}
	comparable, err := comparableCodexHomePath(home)
	if err != nil {
		return false
	}
	return filepath.Clean(requested) == filepath.Clean(comparable)
}

func legacyAppManagedCodexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Clean(filepath.Join(home, ".super-dolphin", "providers", "codex")), nil
}

func matchesDefaultCodexCLIHome(requested string) bool {
	home, err := defaultCodexCLIHome()
	if err != nil {
		return false
	}
	return filepath.Clean(requested) == filepath.Clean(home)
}

func defaultCodexCLIHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	path := filepath.Join(home, ".codex")
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(real), nil
	}
	if os.IsNotExist(err) {
		return filepath.Clean(path), nil
	}
	return "", fmt.Errorf("resolve default codex home realpath: %w", err)
}

// comparableCodexHomePath 将用户输入的 codexHome 标准化成可比较的绝对路径。
// 支持当前用户的 ~ 展开，但拒绝 ~user 形式，避免跨用户路径被静默误解析。
func comparableCodexHomePath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", providershared.ErrCodexHomeRequired
	}
	if strings.HasPrefix(path, "~") {
		switch {
		case path == "~":
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("codexHome ~ expand: %w", err)
			}
			path = home
		case strings.HasPrefix(path, "~/"):
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("codexHome ~ expand: %w", err)
			}
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		default:
			return "", fmt.Errorf("%w: ~user/... form not supported, got %q", providershared.ErrCodexIdentityInvalidType, raw)
		}
	}
	path = filepath.Clean(os.ExpandEnv(path))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: codexHome must be absolute after expansion, got %q", providershared.ErrCodexIdentityInvalidType, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(real), nil
	}
	if os.IsNotExist(err) {
		return path, nil
	}
	return "", fmt.Errorf("codexHome canonicalize: %w", err)
}

// withDefaultCodexIdentity 克隆启动配置并补齐 Codex identity 三元组。
// 原 map 不会被原地修改，调用方可以安全保留请求原始配置用于日志或重试。
func withDefaultCodexIdentity(config map[string]any, home, fallbackModelProvider string) (map[string]any, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return config, providershared.ErrCodexHomeRequired
	}
	out := maps.Clone(config)
	if out == nil {
		out = make(map[string]any, 3)
	}
	if err := putCodexString(out, contract.CodexHomeKey, home); err != nil {
		return config, err
	}
	if err := putDefaultCodexString(out, contract.CodexInstanceKeyKey, defaultCodexInstanceKey); err != nil {
		return config, err
	}
	modelProvider, err := supportutil.ResolveCodexModelProvider(out, home, fallbackModelProvider, localCodexModelProvider, providershared.ProviderClaude, providershared.ProviderCodex)
	if err != nil {
		return config, err
	}
	if err := putDefaultCodexString(out, contract.CodexModelProviderKey, modelProvider); err != nil {
		return config, err
	}
	return out, nil
}
func defaultCodexModelProviderForHome(providerHome codexProviderHomeSelection) string {
	if providerHome.useAppManagedHome {
		return defaultCodexModelProvider
	}
	return localCodexModelProvider
}
func putDefaultCodexString(config map[string]any, key, value string) error {
	raw, ok := config[key]
	if !ok || raw == nil {
		config[key] = value
		return nil
	}
	text, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: %q must be string, got %T", providershared.ErrCodexIdentityInvalidType, key, raw)
	}
	if strings.TrimSpace(text) == "" {
		config[key] = value
	}
	return nil
}

func putCodexString(config map[string]any, key, value string) error {
	if raw, ok := config[key]; ok && raw != nil {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("%w: %q must be string, got %T", providershared.ErrCodexIdentityInvalidType, key, raw)
		}
	}
	config[key] = value
	return nil
}

// resolveSessionOptions 为 StartSession 选择共享 app-server 或独立 ServerPool。
// 携带 Codex identity 时必须走 pool；pool 缺失、禁用或 identity 非法都会 fail-closed，避免误用 ambient home。
// 成功 acquire 的 server 会把 URL 和 release 绑定进 session，启动背压错误原样返回给调用方。
func (d *driver) resolveSessionOptions(ctx context.Context, req dto.StartSessionRequest) ([]sessionOption, error) {
	if err := validateCodexProviderNativeSkillsConfig(req.Config); err != nil {
		return nil, err
	}
	if err := validateCodexNativeToolPolicyConfig(req.Config); err != nil {
		return nil, err
	}
	policy, err := codexNativeToolPolicyFromConfig(req.Config)
	if err != nil {
		return nil, err
	}
	if d == nil || d.pool == nil {
		if _, err := providershared.ResolveCodexIdentity(req.Config); err == nil {
			return nil, errors.New("codexapp: codex identity requires pool-backed app-server")
		}
		return legacySessionOptionsForNativeToolPolicy(policy)
	}
	identity, enabled, err := d.resolveStartPoolIdentity(req)
	if err != nil {
		return nil, err
	}
	if !enabled {
		if !hasStartCodexIdentityConfig(req.Config) {
			return legacySessionOptionsForNativeToolPolicy(policy)
		}
		return d.disabledPoolSessionOptions(req, policy, err)
	}
	return d.acquirePoolSessionOptions(ctx, req, policy, identity)
}

// resolveStartPoolIdentity 解析 StartSession 的 pool identity 和开关状态。
// 一旦请求包含身份字段，解析失败就返回错误，避免退回共享 app-server。
func (d *driver) resolveStartPoolIdentity(req dto.StartSessionRequest) (providershared.CodexIdentity, bool, error) {
	enabled, _, err := poolRoutingDecision()
	if err != nil {
		return providershared.CodexIdentity{}, false, err
	}
	if !enabled && !hasStartCodexIdentityConfig(req.Config) {
		return providershared.CodexIdentity{}, false, nil
	}
	identity, identityErr := providershared.ResolveCodexIdentity(req.Config)
	if identityErr != nil {
		return providershared.CodexIdentity{}, false, fmt.Errorf("codex identity required: %w", identityErr)
	}
	if !enabled {
		return providershared.CodexIdentity{}, false, nil
	}
	return identity, true, nil
}

func hasStartCodexIdentityConfig(config map[string]any) bool {
	if len(config) == 0 {
		return false
	}
	for _, key := range []string{contract.CodexHomeKey, contract.CodexInstanceKeyKey, contract.CodexModelProviderKey} {
		if _, ok := config[key]; ok {
			return true
		}
	}
	return false
}

// acquirePoolSessionOptions 从 ServerPool 取得独立 app-server 并绑定 release 回调。
// pool 返回空 URL 时必须立即 release，避免泄漏已占用的 server slot。
func (d *driver) acquirePoolSessionOptions(ctx context.Context, req dto.StartSessionRequest, policy codexNativeToolPolicy, identity providershared.CodexIdentity) ([]sessionOption, error) {
	owner := strings.TrimSpace(req.AgentID)
	workDir := strings.TrimSpace(req.CWD)
	spawnCtx := withPoolSpawnSessionConfig(ctx, workDir, req.Config, policy)
	server, release, acquireErr := d.pool.Acquire(spawnCtx, identity, owner)
	if acquireErr != nil {
		return nil, acquireErr
	}
	url := strings.TrimSpace(server.ServerURL())
	if url == "" {
		release()
		return nil, errors.New("codexapp: pool returned server with empty URL")
	}
	if d.logger != nil {
		fields := []any{
			"agent_id", strings.TrimSpace(req.AgentID),
			"instance_key", identity.InstanceKey,
			"owner", owner,
			"server_url", url,
		}
		fields = append(fields, platformshared.SafePathLogFields("codex_home", identity.Home)...)
		fields = append(fields, platformshared.SafePathLogFields("work_dir", workDir)...)
		d.logger.Info("codexapp: start session via pool", fields...)
	}
	return []sessionOption{withPoolServer(url, release)}, nil
}

func (d *driver) disabledPoolSessionOptions(req dto.StartSessionRequest, policy codexNativeToolPolicy, identityErr error) ([]sessionOption, error) {
	if identityErr == nil {
		return nil, errors.New("codexapp: codex identity requires pool-backed app-server")
	}
	if d != nil {
		d.warnLegacyIdentityFallback(req.AgentID, identityErr)
	}
	return legacySessionOptionsForNativeToolPolicy(policy)
}

func legacySessionOptionsForNativeToolPolicy(policy codexNativeToolPolicy) ([]sessionOption, error) {
	if policy.HasProcessFlags() {
		return nil, errors.New("codexapp: native tool policy requires pool-backed app-server")
	}
	return []sessionOption(nil), nil
}

// canonicalStartRuntimeConfig 把启动 config 规整成会话运行时可复用的形态。
// Codex 身份字段和 sandboxPolicy 会在后续 turn/start 继续使用，native tool 限制会强制收敛到只读策略。
func canonicalStartRuntimeConfig(config map[string]any) map[string]any {
	if len(config) == 0 {
		return nil
	}
	out := make(map[string]any, len(config))
	maps.Copy(out, config)
	if policyRaw := codexSandboxPolicyWireJSON(supportutil.ConfigJSON(config, "sandbox")); len(policyRaw) > 0 {
		var policyValue any
		if err := json.Unmarshal(policyRaw, &policyValue); err == nil {
			out["sandboxPolicy"] = policyValue
		}
	}
	if policy, err := codexNativeToolPolicyFromConfig(config); err == nil && policy.RequiresReadOnlySandbox() {
		out["sandbox"] = "read-only"
		out["sandboxPolicy"] = codexReadOnlySandboxPolicyValueFromAny(out["sandboxPolicy"])
	}
	identity, err := providershared.ResolveCodexIdentity(config)
	if err != nil {
		return out
	}
	out["codexHome"] = identity.Home
	out["codexInstanceKey"] = identity.InstanceKey
	out["codexModelProvider"] = identity.ModelProvider
	return out
}

// canonicalRuntimeConfigWithCWD 在会话 runtime 启动前一次性水合 canonical config 和已校验的 cwd。
// cwd 由 start/resume boundary 提前 canonicalize；这里不重新解析或放宽路径校验。已存在的 cwd 保留给 recovery 做 canonical/mismatch 校验，确保不会被静默覆盖。
func canonicalRuntimeConfigWithCWD(config map[string]any, cwd string) map[string]any {
	out := canonicalStartRuntimeConfig(config)
	if out == nil {
		out = make(map[string]any, 1)
	}
	if _, exists := out[contract.RuntimeConfigCWD().Canonical]; !exists {
		out[contract.RuntimeConfigCWD().Canonical] = cwd
	}
	return out
}

// resolveResumeOptions 为 ResumeSession 选择 pool 或 legacy 路径。
// 带 Codex 身份的恢复必须走 pool；非 strict legacy 仅用于老线程兼容并会记录告警。
func (d *driver) resolveResumeOptions(ctx context.Context, req dto.ResumeSessionRequest) ([]sessionOption, error) {
	policy, err := codexNativeToolPolicyFromDisabled(req.CodexDisabledNativeTools)
	if err != nil {
		return nil, err
	}
	return d.resolveResumeOptionsWithPolicy(ctx, req, policy)
}

// resolveResumeOptionsWithPolicy 在 native 工具策略已校验后执行 resume 的 pool/legacy 路由。
func (d *driver) resolveResumeOptionsWithPolicy(ctx context.Context, req dto.ResumeSessionRequest, policy codexNativeToolPolicy) ([]sessionOption, error) {
	identity, hasIdentity := resumeIdentity(req)
	enabled, strict, err := poolRoutingDecision()
	if err != nil {
		return nil, err
	}
	if d == nil || d.pool == nil || !enabled {
		if hasIdentity {
			return nil, errors.New("codexapp: codex identity requires pool-backed app-server")
		}
		return legacySessionOptionsForNativeToolPolicy(policy)
	}
	if !hasIdentity {
		return d.missingResumeIdentityOptions(req, strict)
	}
	owner := strings.TrimSpace(req.AgentID)
	workDir := strings.TrimSpace(req.CWD)
	spawnCtx := withPoolSpawnSessionConfig(ctx, workDir, req.Config, policy)
	server, release, err := d.pool.Acquire(spawnCtx, identity, owner)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSpace(server.ServerURL())
	if url == "" {
		release()
		return nil, errors.New("codexapp: pool returned server with empty URL")
	}
	if d.logger != nil {
		fields := []any{
			"agent_id", strings.TrimSpace(req.AgentID),
			"instance_key", identity.InstanceKey,
			"owner", owner,
			"server_url", url,
		}
		fields = append(fields, platformshared.SafePathLogFields("codex_home", identity.Home)...)
		fields = append(fields, platformshared.SafePathLogFields("work_dir", workDir)...)
		d.logger.Info("codexapp: resume session via pool", fields...)
	}
	return []sessionOption{withPoolServer(url, release)}, nil
}

func (d *driver) missingResumeIdentityOptions(req dto.ResumeSessionRequest, strict bool) ([]sessionOption, error) {
	err := errors.New("codex identity required for resume")
	if !strict {
		d.warnLegacyIdentityFallback(req.AgentID, err)
		return []sessionOption(nil), nil
	}
	return nil, err
}

// withPoolSpawnSessionConfig 把工作区、LSP roots 和 native tool policy 注入 pool spawn context。
// 这些值只用于新 app-server 启动，不写回线程运行时配置。
func withPoolSpawnSessionConfig(ctx context.Context, workDir string, cfg map[string]any, policy codexNativeToolPolicy) context.Context {
	roots := trustedWorkspaceRoots(workDir, providershared.ConfigStringSlice(cfg, contract.RuntimeConfigAdditionalWorkingDirectories().Keys()...))
	ctx = withPoolSpawnWorkDir(ctx, workDir)
	ctx = withPoolSpawnLSPConfig(ctx, roots, providershared.ResolveBinaryDir(workDir, cfg))
	return withPoolSpawnNativeToolPolicy(ctx, policy)
}

func resumeIdentity(req dto.ResumeSessionRequest) (providershared.CodexIdentity, bool) {
	identity := providershared.CodexIdentity{Home: strings.TrimSpace(req.CodexHome), InstanceKey: strings.TrimSpace(req.CodexInstanceKey), ModelProvider: strings.TrimSpace(req.CodexModelProvider)}
	if identity.Home == "" || identity.InstanceKey == "" || identity.ModelProvider == "" {
		return providershared.CodexIdentity{}, false
	}
	return identity, true
}

func (d *driver) warnLegacyIdentityFallback(agentID string, err error) {
	if d == nil || d.logger == nil || err == nil {
		return
	}
	d.logger.Warn("codexapp: legacy shared app-server fallback after identity error",
		slog.String("agent_id", strings.TrimSpace(agentID)),
		slog.String("reason", err.Error()),
	)
}

func poolRoutingDecision() (enabled bool, strict bool, err error) {
	raw := strings.TrimSpace(os.Getenv(poolRoutingEnvVar))
	if raw == "" {
		return true, true, nil
	}
	parsed, parseErr := strconv.ParseBool(raw)
	if parseErr != nil {
		return false, false, fmt.Errorf("%s must be a boolean: %w", poolRoutingEnvVar, parseErr)
	}
	return parsed, parsed, nil
}

type codexNativeToolPolicy struct{ contract.CodexNativeToolPolicy }

func codexNativeToolPolicyFromConfig(cfg map[string]any) (codexNativeToolPolicy, error) {
	key := contract.RuntimeConfigCodexDisabledNativeTools().Canonical
	values, err := rawStringList(key, cfg[key])
	if err != nil {
		return codexNativeToolPolicy{}, err
	}
	return codexNativeToolPolicyFromDisabled(values)
}

func codexNativeToolPolicyFromDisabled(ids []string) (codexNativeToolPolicy, error) {
	cleaned := cleanCodexNativeToolIDs(ids)
	if err := validateCodexNativeToolIDs(cleaned); err != nil {
		return codexNativeToolPolicy{}, err
	}
	return codexNativeToolPolicy{CodexNativeToolPolicy: contract.NewCodexNativeToolPolicy(cleaned)}, nil
}

func validateCodexNativeToolPolicyConfig(cfg map[string]any) error {
	_, err := codexNativeToolPolicyFromConfig(cfg)
	return err
}

// validateCodexProviderNativeSkillsConfig 明确解析 common provider-native skills runtime flags。
// Codex 按自身 provider home/cwd 发现技能且没有运行时 enable/disable 开关，因此关闭请求必须
// 返回显式 unsupported，不能静默忽略；开启请求只表示调用方意图，不等同于能力探测已支持。
func validateCodexProviderNativeSkillsConfig(cfg map[string]any) error {
	enabled, hasEnabled, err := readCodexProviderNativeSkillsBool(cfg, contract.RuntimeConfigProviderNativeSkills())
	if err != nil {
		return err
	}
	disabled, hasDisabled, err := readCodexProviderNativeSkillsBool(cfg, contract.RuntimeConfigDisableProviderNativeSkills())
	if err != nil {
		return err
	}
	if hasEnabled && hasDisabled && enabled == disabled {
		return fmt.Errorf("%w: codex provider native skills flags conflict", contract.ErrProviderNativeContextInvalid)
	}
	if (hasEnabled && !enabled) || (hasDisabled && disabled) {
		return fmt.Errorf("%w: codex provider native skills disable flag is unsupported", contract.ErrProviderNativeCapabilityUnsupported)
	}
	return nil
}

// readCodexProviderNativeSkillsBool 严格解析 runtime 字段及其别名。
// 同组别名只有在值一致时才接受；值不一致的请求含义不明确，必须在 provider 副作用前失败。
func readCodexProviderNativeSkillsBool(cfg map[string]any, field contract.RuntimeConfigField) (bool, bool, error) {
	var (
		value bool
		found bool
	)
	for _, key := range field.Keys() {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		parsed, ok := raw.(bool)
		if !ok {
			return false, false, fmt.Errorf("%w: codex runtime config %s must be boolean", contract.ErrProviderNativeContextInvalid, key)
		}
		if found && value != parsed {
			return false, false, fmt.Errorf("%w: codex runtime config %s aliases conflict", contract.ErrProviderNativeContextInvalid, field.Canonical)
		}
		value, found = parsed, true
	}
	return value, found, nil
}

// rawStringList 严格解析 native tool 禁用列表，遇到非字符串元素会直接阻断启动。
func rawStringList(key string, value any) ([]string, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, value := range v {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be []string or []any of strings, got element %T", key, value)
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be []string or []any of strings, got %T", key, value)
	}
}

func cleanCodexNativeToolIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if id := strings.TrimSpace(value); id != "" {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func validateCodexNativeToolIDs(ids []string) error {
	for _, id := range ids {
		if !contract.IsKnownCodexNativeTool(id) {
			return fmt.Errorf("%s contains unknown Codex native tool %q", contract.RuntimeConfigCodexDisabledNativeTools().Canonical, id)
		}
	}
	return nil
}

// ApplyThreadStartParams 在禁用 native tool 时强制 StartThread 使用 read-only sandbox。
// approvalPolicy 同步改为 never，避免 app-server 再向前端发起本地执行审批。
func (p codexNativeToolPolicy) ApplyThreadStartParams(params *threadStartParams) {
	if params == nil || !p.RequiresReadOnlySandbox() {
		return
	}
	params.Sandbox = codexReadOnlySandbox(params.Sandbox)
	params.SandboxPolicy = codexReadOnlySandboxPolicy(params.SandboxPolicy)
	params.ApprovalPolicy = "never"
}

// ApplyThreadResumeParams 在 ResumeThread 参数上恢复 native tool 限制。
// Resume 使用字符串 sandbox 形态，因此这里与 Start 参数的 JSON 形态分开处理。
func (p codexNativeToolPolicy) ApplyThreadResumeParams(params *threadResumeParams) {
	if params == nil || !p.RequiresReadOnlySandbox() {
		return
	}
	params.Sandbox = "read-only"
	params.ApprovalPolicy = "never"
}

func applyResumeNativeToolRuntimePolicy(s *session, disabled []string) error {
	if s == nil {
		return nil
	}
	policy, err := codexNativeToolPolicyFromDisabled(disabled)
	if err != nil {
		return err
	}
	if !policy.RequiresReadOnlySandbox() {
		return nil
	}
	s.setApprovalPolicy("never")
	s.setRuntimeConfigValue("approvalPolicy", "never")
	s.setRuntimeConfigValue("sandbox", "read-only")
	runtimeSandboxPolicy := s.RuntimeConfigSnapshot()["sandboxPolicy"]
	s.setRuntimeConfigValue("sandboxPolicy", codexReadOnlySandboxPolicyValueFromAny(runtimeSandboxPolicy))
	return nil
}

func codexReadOnlySandbox(raw json.RawMessage) json.RawMessage {
	if codexSandboxIsReadOnly(raw) {
		return mustJSON("read-only")
	}
	return mustJSON("read-only")
}

func codexReadOnlySandboxPolicy(raw json.RawMessage) json.RawMessage {
	return mustJSON(codexReadOnlySandboxPolicyValueFromRaw(raw))
}

func codexReadOnlySandboxPolicyValue() map[string]any {
	return map[string]any{"type": "readOnly"}
}

// codexSandboxIsReadOnly 兼容 Codex sandbox 的字符串和对象两种 wire 形态。
// 只识别 read-only/readOnly，其他 malformed JSON 一律按非只读处理。
func codexSandboxIsReadOnly(raw json.RawMessage) bool {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.EqualFold(strings.TrimSpace(text), "read-only") ||
			strings.EqualFold(strings.TrimSpace(text), "readOnly")
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	if _, ok := obj["read-only"]; ok {
		return true
	}
	if _, ok := obj["readOnly"]; ok {
		return true
	}
	for _, key := range []string{"mode", "type"} {
		value, _ := obj[key].(string)
		if strings.EqualFold(strings.TrimSpace(value), "read-only") ||
			strings.EqualFold(strings.TrimSpace(value), "readOnly") {
			return true
		}
	}
	return false
}
