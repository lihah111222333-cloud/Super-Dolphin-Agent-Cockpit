package claudecli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// resolveAbsCWD 将调用方提供的 cwd 转成绝对路径，但不会凭空发明缺失 cwd。
func resolveAbsCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return ""
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(cwd, "/") && !strings.HasPrefix(cwd, "//") {
		return cwd
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

// resolveBinaryPath 从环境变量或默认值解析 Claude CLI binary 路径。
func resolveBinaryPath() string {
	if bin := strings.TrimSpace(os.Getenv("CLAUDE_CLI_BIN")); bin != "" {
		return bin
	}
	return defaultClaudeCLIBin
}

// configFromMap 将 provider runtime config 解析为 Claude CLI 启动配置。
func configFromMap(cfg map[string]any) cliLaunchConfig {
	return cliLaunchConfig{
		ApprovalPolicy:              providershared.ConfigString(cfg, "approval_policy", "approvalPolicy", "approvals"),
		Sandbox:                     providershared.ConfigString(cfg, "sandbox"),
		Summary:                     providershared.ConfigString(cfg, "summary"),
		Effort:                      providershared.ConfigString(cfg, "effort"),
		Personality:                 providershared.ConfigString(cfg, "personality"),
		DeveloperInstructions:       providershared.ConfigString(cfg, "developer_instructions", "developerInstructions"),
		BuiltinTools:                builtinToolsFromMap(cfg),
		DisallowedTools:             disallowedBuiltinToolsFromMap(cfg),
		AdditionalDisallowedTools:   additionalDisallowedToolsFromMap(cfg),
		DisableProviderNativeSkills: providerNativeSkillsDisabledFromMap(cfg),
	}
}

// validateClaudeSecurityConfig 在 provider 启动前严格校验安全相关配置。
// 配置错误必须返回启动错误，不能被 NormalizeConfigStringSlice 隐式收敛为空列表。
func validateClaudeSecurityConfig(cfg map[string]any) error {
	if err := validateApprovalPolicyKeys(cfg, "approval_policy", "approvalPolicy", "approvals"); err != nil {
		return err
	}
	if err := validateSandboxConfigKey(cfg, "sandbox"); err != nil {
		return err
	}
	if err := validateConfigStringSliceKeys(cfg, "claude_builtin_tools", "claudeBuiltinTools", "builtin_tools", "builtinTools"); err != nil {
		return err
	}
	if err := validateConfigStringSliceKeys(cfg, "disallowed_tools", "disallowedTools", "disallowed_builtin_tools", "disallowedBuiltinTools"); err != nil {
		return err
	}
	if err := validateConfigStringSliceKeys(cfg, "additional_disallowed_tools", "additionalDisallowedTools", "extra_disallowed_tools", "extraDisallowedTools", "claude_additional_disallowed_tools", "claudeAdditionalDisallowedTools"); err != nil {
		return err
	}
	if err := validateConfigStringSliceKeys(cfg, "auto_approve", "autoApprove"); err != nil {
		return err
	}
	return validateConfigBoolKeys(cfg, "providerNativeSkills", "provider_native_skills", "disableProviderNativeSkills", "disable_provider_native_skills")
}

func validateApprovalPolicyKeys(cfg map[string]any, keys ...string) error {
	for _, key := range keys {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("invalid approval policy %s: must be string", key)
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "always", "never", "auto", "on-request", "on-failure", "untrusted":
			return nil
		default:
			return fmt.Errorf("invalid approval policy %s: %q", key, value)
		}
	}
	return nil
}

// validateSandboxConfigKey 校验 Claude 配置中的 sandbox 字段。
// 支持字符串、RawMessage 和对象输入，但任何未知类型或未知 sandbox 都会阻断启动。
func validateSandboxConfigKey(cfg map[string]any, key string) error {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case string:
		return validateClaudeSandboxType(typed)
	case json.RawMessage:
		return validateClaudeSandboxRaw(typed)
	case []byte:
		return validateClaudeSandboxRaw(typed)
	case map[string]any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Errorf("invalid sandbox %s: %w", key, err)
		}
		return validateClaudeSandboxRaw(encoded)
	default:
		return fmt.Errorf("invalid sandbox %s: expected string or object", key)
	}
}

// validateClaudeSandboxRaw 校验原始 sandbox JSON。
// 这里只接受字符串或带 type 的对象，防止未知 alias 被映射到提权模式。
func validateClaudeSandboxRaw(raw []byte) error {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}
	if raw[0] != '{' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid sandbox: expected string or object with type")
		}
		return validateClaudeSandboxType(value)
	}
	var payload struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("invalid sandbox object: %w", err)
	}
	if payload.Type == nil {
		return fmt.Errorf("invalid sandbox object: type is required")
	}
	return validateClaudeSandboxType(*payload.Type)
}

func validateClaudeSandboxType(value string) error {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	switch normalized {
	case "", "readonly", "workspacewrite", "dangerfullaccess":
		return nil
	default:
		return fmt.Errorf("invalid sandbox type %q", value)
	}
}

func validateConfigStringSliceKeys(cfg map[string]any, keys ...string) error {
	for _, key := range keys {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		if err := validateConfigStringSliceValue(key, raw); err != nil {
			return err
		}
	}
	return nil
}

// validateConfigStringSliceValue 校验字符串列表配置的原始类型。
// []any 内任何非字符串元素都视为配置错误，避免安全列表被悄悄截断。
func validateConfigStringSliceValue(key string, raw any) error {
	switch typed := raw.(type) {
	case string, []string:
		return nil
	case []any:
		for i, value := range typed {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s[%d] must be string", key, i)
			}
		}
		return nil
	default:
		return fmt.Errorf("%s must be string or string array", key)
	}
}

// validateConfigBoolKeys 校验布尔安全开关必须显式为 bool。
// 字符串或对象不能被当成 false 处理，否则会隐藏配置写错的问题。
func validateConfigBoolKeys(cfg map[string]any, keys ...string) error {
	for _, key := range keys {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("%s must be boolean", key)
		}
	}
	return nil
}

// builtinToolsFromMap 读取显式 builtin tools 覆盖；nil 表示使用默认策略。
func builtinToolsFromMap(cfg map[string]any) []string {
	for _, key := range []string{"claude_builtin_tools", "claudeBuiltinTools", "builtin_tools", "builtinTools"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		ids := providershared.NormalizeConfigStringSlice(raw)
		if ids == nil {
			return []string{}
		}
		return ids
	}
	return nil
}

// disallowedBuiltinToolsFromMap 读取禁用的上游内置工具列表。
// 未配置时返回 nil 让调用方使用默认禁用列表；显式空数组返回非 nil 空切片，
// 表示不禁用任何上游内置工具。
func disallowedBuiltinToolsFromMap(cfg map[string]any) []string {
	for _, key := range []string{"disallowed_tools", "disallowedTools", "disallowed_builtin_tools", "disallowedBuiltinTools"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		ids := providershared.NormalizeConfigStringSlice(raw)
		if ids == nil {
			return []string{}
		}
		return ids
	}
	return nil
}

// additionalDisallowedToolsFromMap 读取额外禁用工具列表；显式空数组表示不追加。
func additionalDisallowedToolsFromMap(cfg map[string]any) []string {
	for _, key := range []string{"additional_disallowed_tools", "additionalDisallowedTools", "extra_disallowed_tools", "extraDisallowedTools", "claude_additional_disallowed_tools", "claudeAdditionalDisallowedTools"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		ids := providershared.NormalizeConfigStringSlice(raw)
		if ids == nil {
			return []string{}
		}
		return ids
	}
	return nil
}

// providerNativeSkillsDisabledFromMap 解析是否禁用 provider 原生 skills。
func providerNativeSkillsDisabledFromMap(cfg map[string]any) bool {
	for _, key := range []string{"providerNativeSkills", "provider_native_skills"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		enabled, ok := raw.(bool)
		return ok && !enabled
	}
	for _, key := range []string{"disableProviderNativeSkills", "disable_provider_native_skills"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		disabled, ok := raw.(bool)
		return ok && disabled
	}
	return false
}

// resolveStartAssembly 合并启动请求中的 prompt assembly、runtime context 和 provider 默认指令。
func resolveStartAssembly(req dto.StartSessionRequest, cfg cliLaunchConfig, provider string) contract.StartAssembly {
	assembly := req.StartAssembly
	baseInstructions := promptSnapshotBaseInstructions(assembly.Snapshot, req.Instructions)
	if value := strings.TrimSpace(assembly.BaseInstructions); value != "" {
		baseInstructions = value
	}
	runtimeContext := contract.RenderStartRuntimeContext(assembly)
	baseInstructions = contract.AppendStartRuntimeContext(baseInstructions, assembly)
	developerInstructions := promptDeveloperInstructions(cliLaunchConfig{
		DeveloperInstructions: cfg.DeveloperInstructions,
		PromptSnapshot:        assembly.Snapshot,
	})
	if value := strings.TrimSpace(assembly.DeveloperInstructions); value != "" {
		developerInstructions = value
	}
	assembly.BaseInstructions = baseInstructions
	assembly.DeveloperInstructions = developerInstructions
	if assembly.Snapshot.Boundary == nil && assembly.Boundary != nil {
		boundary := *assembly.Boundary
		assembly.Snapshot.Boundary = &boundary
	}
	assembly.Snapshot = normalizePromptSnapshot(assembly.Snapshot, baseInstructions, developerInstructions, provider)
	assembly.Snapshot = appendRuntimeContextToSnapshotBoundary(assembly.Snapshot, runtimeContext)
	return assembly
}

// appendRuntimeContextToSnapshotBoundary 把 runtime context 放入 prompt snapshot 的 uncached tail。
func appendRuntimeContextToSnapshotBoundary(
	snapshot contract.PromptAssemblySnapshot,
	runtimeContext string,
) contract.PromptAssemblySnapshot {
	runtimeContext = strings.TrimSpace(runtimeContext)
	if runtimeContext == "" || snapshot.Boundary == nil {
		return snapshot
	}
	boundary := *snapshot.Boundary
	boundary.UncachedTail = appendPromptBlock(boundary.UncachedTail, runtimeContext)
	snapshot.Boundary = &boundary
	return snapshot
}

// appendPromptBlock 向已有 prompt 追加去重后的块内容。
func appendPromptBlock(base, block string) string {
	base = strings.TrimSpace(base)
	block = strings.TrimSpace(block)
	if block == "" {
		return base
	}
	if base == "" {
		return block
	}
	if strings.Contains(base, block) {
		return base
	}
	return base + "\n\n" + block
}

// normalizePromptSnapshot 填充 snapshot 缺省字段，保证恢复会话有完整上下文。
func normalizePromptSnapshot(
	snapshot contract.PromptAssemblySnapshot,
	baseInstructions string,
	developerInstructions string,
	provider string,
) contract.PromptAssemblySnapshot {
	if strings.TrimSpace(snapshot.BaseInstructions) == "" {
		snapshot.BaseInstructions = strings.TrimSpace(baseInstructions)
	}
	if strings.TrimSpace(snapshot.DeveloperInstructions) == "" {
		snapshot.DeveloperInstructions = strings.TrimSpace(developerInstructions)
	}
	if strings.TrimSpace(snapshot.Provider) == "" {
		snapshot.Provider = strings.TrimSpace(provider)
	}
	if snapshot.Version == 0 {
		snapshot.Version = contract.PromptAssemblySnapshotVersion
	}
	return snapshot
}

// copyCapabilities 复制能力集合，避免 session 修改共享 map。
func copyCapabilities(in dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(in))
	maps.Copy(out, in)
	return out
}

// cloneConfigMap 深拷贝 runtime config，避免 session 内部修改调用方 map。
func cloneConfigMap(cfg map[string]any) map[string]any {
	if len(cfg) == 0 {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		out := make(map[string]any, len(cfg))
		maps.Copy(out, cfg)
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = make(map[string]any, len(cfg))
		maps.Copy(out, cfg)
	}
	return out
}

// fallbackThreadID 返回恢复请求中已有的 threadID；新会话必须等待 provider 回报真实 id。
func fallbackThreadID(threadID string) string {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		return threadID
	}
	return ""
}
