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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/supportutil"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
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
// 技能镜像必须在选定 home 后同步，避免 app-server 看到旧 provider mirror。
func (d *driver) prepareStartSessionRequest(ctx context.Context, req dto.StartSessionRequest) (dto.StartSessionRequest, error) {
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
	home, mirrorHome, err := ensureResolvedCodexProviderHome(providerHome)
	if err != nil {
		return req, err
	}
	if err := d.reconcileProviderMirrors(ctx, req.CWD, mirrorHome); err != nil {
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

// prepareResumeSessionRequest 恢复会话前重新解析 Codex home 并刷新技能镜像。
// Resume 依赖历史绑定身份，解析失败时必须阻断，不能退回默认 home。
func (d *driver) prepareResumeSessionRequest(ctx context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	var err error
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
	home, mirrorHome, err := ensureResolvedCodexProviderHome(providerHome)
	if err != nil {
		return req, err
	}
	if err := d.reconcileProviderMirrors(ctx, req.CWD, mirrorHome); err != nil {
		return req, err
	}
	req.CodexHome = home
	return req, nil
}

// reconcileProviderMirrors 将 canonical skill roots 同步到 Codex 可发现的 provider mirror。
// mirror 组件缺失直接报错，因为继续启动会造成 runtime 技能面与项目配置不一致。
func (d *driver) reconcileProviderMirrors(ctx context.Context, cwd, home string) error {
	if d == nil || d.mirror == nil {
		return errors.New("codex skill mirror reconciler is required")
	}
	targets, err := providershared.ProviderMirrorTargets(providershared.ProviderCodex, cwd, home)
	if err != nil {
		return err
	}
	report, err := d.mirror.ReconcileProviderMirrors(ctx, cwd, targets)
	if err != nil {
		return err
	}
	if err := providershared.EnsureNoSkillMirrorConflicts(report); err != nil {
		return err
	}
	return nil
}

func normalizedExplicitProviderHome(rawHome, normalizedHome string) string {
	if strings.TrimSpace(rawHome) == "" {
		return ""
	}
	return normalizedHome
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
		out["sandboxPolicy"] = codexReadOnlySandboxPolicyValue()
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
	roots := trustedWorkspaceRoots(workDir, providershared.ConfigStringSlice(cfg, contract.RuntimeConfigAdditionalWorkingDirectories.Keys()...))
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

const codexDisabledNativeToolsConfigKey = "codexDisabledNativeTools"

type codexNativeToolPolicy struct{ contract.CodexNativeToolPolicy }

func codexNativeToolPolicyFromConfig(cfg map[string]any) (codexNativeToolPolicy, error) {
	values, err := rawStringList(cfg[codexDisabledNativeToolsConfigKey])
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

// rawStringList 严格解析 native tool 禁用列表，遇到非字符串元素会直接阻断启动。
func rawStringList(value any) ([]string, error) {
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
				return nil, fmt.Errorf("%s must be []string or []any of strings, got element %T", codexDisabledNativeToolsConfigKey, value)
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be []string or []any of strings, got %T", codexDisabledNativeToolsConfigKey, value)
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
			return fmt.Errorf("%s contains unknown Codex native tool %q", codexDisabledNativeToolsConfigKey, id)
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
	params.SandboxPolicy = codexReadOnlySandboxPolicy()
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
	s.setRuntimeConfigValue("sandboxPolicy", codexReadOnlySandboxPolicyValue())
	return nil
}

func codexReadOnlySandbox(raw json.RawMessage) json.RawMessage {
	if codexSandboxIsReadOnly(raw) {
		return mustJSON("read-only")
	}
	return mustJSON("read-only")
}

func codexReadOnlySandboxPolicy() json.RawMessage {
	return mustJSON(codexReadOnlySandboxPolicyValue())
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
