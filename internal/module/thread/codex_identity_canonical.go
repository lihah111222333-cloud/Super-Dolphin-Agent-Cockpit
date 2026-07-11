package thread

import (
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type resumeCodexIdentityValues struct {
	home             any
	hasHome          bool
	instanceKey      any
	hasInstanceKey   bool
	modelProvider    any
	hasModelProvider bool
}

// validateExplicitResumeCodexIdentity 在读取历史状态前检查调用方显式传入的 Codex 身份。
// 只要请求里出现任一身份字段，就要求三元组是完整字符串；realpath 收敛只在 canonicalize 阶段做一次。
func validateExplicitResumeCodexIdentity(req ResumeRequest) error {
	if !isCodexResumeProvider(req.Provider) {
		return nil
	}
	values := collectResumeCodexIdentityValues(req, req.Config)
	if !values.hasAny() {
		return nil
	}
	return values.validateCompleteStrings()
}

// canonicalizeResumeCodexIdentity 在 resume 进入 provider 前把可解析的 Codex 身份收敛为 realpath。
// 历史 binding/runtime 里可能有旧的 partial 或假路径；这些存量值不能在热路径变成新的致命错误。
func canonicalizeResumeCodexIdentity(req ResumeRequest) (ResumeRequest, error) {
	if !isCodexResumeProvider(req.Provider) {
		return req, nil
	}
	values := collectResumeCodexIdentityValues(req, req.Config)
	home, instanceKey, modelProvider, ok := values.completeStrings()
	if !values.hasAny() || !ok {
		return req, nil
	}
	identity, _, err := canonicalizeCodexIdentityFields(req.Provider, home, instanceKey, modelProvider)
	if err != nil {
		return ResumeRequest{}, err
	}
	req.CodexHome = identity.Home
	req.CodexInstanceKey = identity.InstanceKey
	req.CodexModelProvider = identity.ModelProvider
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	req.Config[contract.CodexHomeKey] = identity.Home
	req.Config[contract.CodexInstanceKeyKey] = identity.InstanceKey
	req.Config[contract.CodexModelProviderKey] = identity.ModelProvider
	return req, nil
}

// canonicalizeHydratedResumeCodexIdentity 在直接 resume 入口执行默认补齐和 realpath 收敛。
// Resume 主流程传下来的请求已经收敛过，enabled=false 时必须原样透传，避免二次 EvalSymlinks。
func (s *service) canonicalizeHydratedResumeCodexIdentity(req ResumeRequest, enabled bool) (ResumeRequest, error) {
	if !enabled {
		return req, nil
	}
	req, err := s.injectDefaultCodexIdentityForResume(req)
	if err != nil {
		return ResumeRequest{}, err
	}
	return canonicalizeResumeCodexIdentity(req)
}

// canonicalizeCodexIdentityFields 用字段形式收敛完整 Codex identity。
// bool 表示输入包含完整三元组；partial 历史值由调用方决定是否兼容跳过。
func canonicalizeCodexIdentityFields(provider, home, instanceKey, modelProvider string) (contract.CodexIdentity, bool, error) {
	if !isCodexResumeProvider(provider) {
		return contract.CodexIdentity{}, false, nil
	}
	home = strings.TrimSpace(home)
	instanceKey = strings.TrimSpace(instanceKey)
	modelProvider = strings.TrimSpace(modelProvider)
	if home == "" || instanceKey == "" || modelProvider == "" {
		return contract.CodexIdentity{}, false, nil
	}
	identity, err := contract.ResolveCodexIdentity(map[string]any{
		contract.CodexHomeKey:          home,
		contract.CodexInstanceKeyKey:   instanceKey,
		contract.CodexModelProviderKey: modelProvider,
	})
	if err != nil {
		return contract.CodexIdentity{}, true, err
	}
	return identity, true, nil
}

func isCodexResumeProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "codex")
}

func collectResumeCodexIdentityValues(req ResumeRequest, config map[string]any) resumeCodexIdentityValues {
	values := resumeCodexIdentityValues{}
	values.setConfig(config)
	values.setString(contract.CodexHomeKey, req.CodexHome)
	values.setString(contract.CodexInstanceKeyKey, req.CodexInstanceKey)
	values.setString(contract.CodexModelProviderKey, req.CodexModelProvider)
	return values
}

func (v *resumeCodexIdentityValues) setConfig(config map[string]any) {
	if raw, ok := config[contract.CodexHomeKey]; ok {
		v.home = raw
		v.hasHome = true
	}
	if raw, ok := config[contract.CodexInstanceKeyKey]; ok {
		v.instanceKey = raw
		v.hasInstanceKey = true
	}
	if raw, ok := config[contract.CodexModelProviderKey]; ok {
		v.modelProvider = raw
		v.hasModelProvider = true
	}
}

func (v *resumeCodexIdentityValues) setString(key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch key {
	case contract.CodexHomeKey:
		v.home = value
		v.hasHome = true
	case contract.CodexInstanceKeyKey:
		v.instanceKey = value
		v.hasInstanceKey = true
	case contract.CodexModelProviderKey:
		v.modelProvider = value
		v.hasModelProvider = true
	}
}

func (v resumeCodexIdentityValues) hasAny() bool {
	return v.hasHome || v.hasInstanceKey || v.hasModelProvider
}

func (v resumeCodexIdentityValues) completeStrings() (string, string, string, bool) {
	home, ok := codexIdentityString(v.home)
	if !ok {
		return "", "", "", false
	}
	instanceKey, ok := codexIdentityString(v.instanceKey)
	if !ok {
		return "", "", "", false
	}
	modelProvider, ok := codexIdentityString(v.modelProvider)
	if !ok {
		return "", "", "", false
	}
	return home, instanceKey, modelProvider, true
}

// validateCompleteStrings 保留显式 partial 的 fail-fast，但不触碰文件系统。
// 完整三元组是否合法交给 canonicalizeResumeCodexIdentity 唯一一次 Resolve。
func (v resumeCodexIdentityValues) validateCompleteStrings() error {
	if _, err := requireResumeCodexIdentityString(v.home, v.hasHome, contract.CodexHomeKey, contract.ErrCodexHomeRequired); err != nil {
		return err
	}
	if _, err := requireResumeCodexIdentityString(v.instanceKey, v.hasInstanceKey, contract.CodexInstanceKeyKey, contract.ErrCodexInstanceKeyRequired); err != nil {
		return err
	}
	if _, err := requireResumeCodexIdentityString(v.modelProvider, v.hasModelProvider, contract.CodexModelProviderKey, contract.ErrCodexModelProviderRequired); err != nil {
		return err
	}
	return nil
}

func requireResumeCodexIdentityString(value any, present bool, key string, missingErr error) (string, error) {
	if !present || value == nil {
		return "", missingErr
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: %q must be string, got %T", contract.ErrCodexIdentityInvalidType, key, value)
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", missingErr
	}
	return text, nil
}

func codexIdentityString(value any) (string, bool) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}
