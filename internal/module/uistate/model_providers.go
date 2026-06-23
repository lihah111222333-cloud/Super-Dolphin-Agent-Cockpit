package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const preferenceModelProviderRegistry = "settings.modelProviders.registry"

var modelProviderEnvKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type modelProvidersParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type modelProvidersSaveParams struct {
	Cwd      string                `json:"cwd,omitempty"`
	Registry modelProviderRegistry `json:"registry"`
}

type modelProvidersApplyParams struct {
	Cwd      string `json:"cwd,omitempty"`
	VendorID string `json:"vendorId"`
}

type modelProviderRegistry struct {
	Vendors        []modelProviderVendor `json:"vendors"`
	ActiveVendorID string                `json:"activeVendorId,omitempty"`
}

type modelProviderVendor struct {
	ID                 string                 `json:"id"`
	Label              string                 `json:"label"`
	Enabled            bool                   `json:"enabled"`
	BaseURL            string                 `json:"baseURL"`
	EnvKey             string                 `json:"envKey"`
	CodexModelProvider string                 `json:"codexModelProvider"`
	DefaultModel       string                 `json:"defaultModel"`
	CodexHome          string                 `json:"codexHome,omitempty"`
	CodexInstanceKey   string                 `json:"codexInstanceKey,omitempty"`
	Budget             modelProviderBudget    `json:"budget,omitempty"`
	TokenPool          modelProviderTokenPool `json:"tokenPool,omitempty"`
	Configured         bool                   `json:"configured,omitempty"`
	MaskedEnv          string                 `json:"maskedEnv,omitempty"`
	EnvStatus          string                 `json:"envStatus,omitempty"`
}

type modelProviderBudget struct {
	DailyUSD   float64 `json:"dailyUsd,omitempty"`
	MonthlyUSD float64 `json:"monthlyUsd,omitempty"`
}

type modelProviderTokenPool struct {
	Priority         float64 `json:"priority,omitempty"`
	FallbackVendorID string  `json:"fallbackVendorId,omitempty"`
}

// defaultModelProviderRegistry 返回内置厂商模板；这些模板只描述配置入口，不包含任何密钥。
func defaultModelProviderRegistry() modelProviderRegistry {
	return modelProviderRegistry{Vendors: []modelProviderVendor{
		{
			ID:                 "openrouter",
			Label:              "OpenRouter",
			Enabled:            true,
			BaseURL:            "https://openrouter.ai/api/v1",
			EnvKey:             "OPENROUTER_API_KEY",
			CodexModelProvider: "openrouter",
			DefaultModel:       "openai/gpt-4.1",
			TokenPool:          modelProviderTokenPool{Priority: 10, FallbackVendorID: "deepseek"},
		},
		{
			ID:                 "deepseek",
			Label:              "DeepSeek",
			Enabled:            false,
			BaseURL:            "https://api.deepseek.com/v1",
			EnvKey:             "DEEPSEEK_API_KEY",
			CodexModelProvider: "deepseek",
			DefaultModel:       "deepseek-chat",
			TokenPool:          modelProviderTokenPool{Priority: 20, FallbackVendorID: "qwen"},
		},
		{
			ID:                 "qwen",
			Label:              "Qwen",
			Enabled:            false,
			BaseURL:            "https://dashscope.aliyuncs.com/compatible-mode/v1",
			EnvKey:             "QWEN_API_KEY",
			CodexModelProvider: "qwen",
			DefaultModel:       "qwen-plus",
			TokenPool:          modelProviderTokenPool{Priority: 30},
		},
	}}
}

// listModelProviders 读取当前作用域的厂商注册表，并只附加由环境变量实时计算的安全状态。
func listModelProviders(ctx context.Context, svc Service, cwd string) (modelProviderRegistry, error) {
	registry, err := loadModelProviderRegistry(withPreferenceScope(ctx, cwd), svc)
	if err != nil {
		return modelProviderRegistry{}, err
	}
	return withModelProviderEnvStatus(registry), nil
}

// saveModelProviders 校验并保存厂商注册表；密钥只通过 envKey 引用，不写入偏好存储。
func saveModelProviders(ctx context.Context, svc Service, p modelProvidersSaveParams) (map[string]any, error) {
	registry := normalizeModelProviderRegistry(p.Registry)
	if err := validateModelProviderRegistry(registry); err != nil {
		return nil, err
	}
	if err := svc.SetPreference(withPreferenceScope(ctx, p.Cwd), preferenceModelProviderRegistry, registry); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// applyModelProvider 将启用且已配置环境变量的厂商写入现有 Codex 偏好，并标记为当前 active 厂商。
func applyModelProvider(ctx context.Context, svc Service, p modelProvidersApplyParams) (modelProviderRegistry, error) {
	scopeCtx := withPreferenceScope(ctx, p.Cwd)
	registry, err := loadModelProviderRegistry(scopeCtx, svc)
	if err != nil {
		return modelProviderRegistry{}, err
	}
	vendor, err := applicableModelProviderVendor(registry, p.VendorID)
	if err != nil {
		return modelProviderRegistry{}, err
	}
	if err := setCodexModelProviderPreferences(scopeCtx, svc, vendor); err != nil {
		return modelProviderRegistry{}, err
	}

	registry.ActiveVendorID = vendor.ID
	if err := svc.SetPreference(scopeCtx, preferenceModelProviderRegistry, registry); err != nil {
		return modelProviderRegistry{}, err
	}
	return withModelProviderEnvStatus(registry), nil
}

// loadModelProviderRegistry 从当前作用域偏好加载注册表，并在返回前完成结构校验。
func loadModelProviderRegistry(ctx context.Context, svc Service) (modelProviderRegistry, error) {
	prefs, err := svc.GetPreferences(ctx)
	if err != nil {
		return modelProviderRegistry{}, err
	}
	registry, err := modelProviderRegistryFromPreference(preferenceValue(*prefs, preferenceModelProviderRegistry))
	if err != nil {
		return modelProviderRegistry{}, err
	}
	if err := validateModelProviderRegistry(registry); err != nil {
		return modelProviderRegistry{}, err
	}
	return registry, nil
}

// applicableModelProviderVendor 校验目标厂商存在、已启用且环境变量可用，失败时立即返回明确错误。
func applicableModelProviderVendor(registry modelProviderRegistry, vendorID string) (modelProviderVendor, error) {
	vendorID = strings.TrimSpace(vendorID)
	if vendorID == "" {
		return modelProviderVendor{}, errors.New("model provider vendorId is required")
	}
	vendor, ok := findModelProviderVendor(registry, vendorID)
	if !ok {
		return modelProviderVendor{}, fmt.Errorf("model provider %q not found", vendorID)
	}
	if !vendor.Enabled {
		return modelProviderVendor{}, fmt.Errorf("model provider %q is disabled", vendorID)
	}
	if strings.TrimSpace(os.Getenv(vendor.EnvKey)) == "" {
		return modelProviderVendor{}, fmt.Errorf("environment variable %s is not configured", vendor.EnvKey)
	}
	return vendor, nil
}

// setCodexModelProviderPreferences 只写现有 Codex 启动偏好，不触碰其它 provider 设置。
func setCodexModelProviderPreferences(ctx context.Context, svc Service, vendor modelProviderVendor) error {
	if err := svc.SetPreference(ctx, "settings.provider.codex.codexModelProvider", vendor.CodexModelProvider); err != nil {
		return err
	}
	if vendor.CodexHome != "" {
		if err := svc.SetPreference(ctx, "settings.provider.codex.codexHome", vendor.CodexHome); err != nil {
			return err
		}
	}
	if vendor.CodexInstanceKey != "" {
		if err := svc.SetPreference(ctx, "settings.provider.codex.codexInstanceKey", vendor.CodexInstanceKey); err != nil {
			return err
		}
	}
	return nil
}

// modelProviderRegistryFromPreference 将偏好中的任意 JSON 形态还原为注册表；缺省时返回内置模板。
func modelProviderRegistryFromPreference(value any) (modelProviderRegistry, error) {
	if value == nil {
		return defaultModelProviderRegistry(), nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return modelProviderRegistry{}, fmt.Errorf("marshal model provider registry preference: %w", err)
	}
	var registry modelProviderRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return modelProviderRegistry{}, fmt.Errorf("decode model provider registry preference: %w", err)
	}
	return normalizeModelProviderRegistry(registry), nil
}

// validateModelProviderRegistry 对写入和应用前的注册表做 fail-fast 校验，避免保存无效厂商配置。
func validateModelProviderRegistry(registry modelProviderRegistry) error {
	if len(registry.Vendors) == 0 {
		return errors.New("model provider registry must include at least one vendor")
	}

	ids := make(map[string]struct{}, len(registry.Vendors))
	for i, vendor := range registry.Vendors {
		if err := validateModelProviderVendor(i, vendor); err != nil {
			return err
		}
		if _, ok := ids[vendor.ID]; ok {
			return fmt.Errorf("model provider vendor id %q is duplicated", vendor.ID)
		}
		ids[vendor.ID] = struct{}{}
	}
	if registry.ActiveVendorID != "" {
		if _, ok := ids[registry.ActiveVendorID]; !ok {
			return fmt.Errorf("active model provider %q not found", registry.ActiveVendorID)
		}
	}
	for _, vendor := range registry.Vendors {
		fallbackID := vendor.TokenPool.FallbackVendorID
		if fallbackID == "" {
			continue
		}
		if _, ok := ids[fallbackID]; !ok {
			return fmt.Errorf("model provider %q fallbackVendorId %q not found", vendor.ID, fallbackID)
		}
	}
	return nil
}

// maskModelProviderEnv 返回可展示的掩码值，确保 RPC 响应不会携带 API key 明文。
func maskModelProviderEnv(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}

// withModelProviderEnvStatus 使用 os.Getenv 计算厂商状态；返回值只包含 configured/missing 和掩码。
func withModelProviderEnvStatus(registry modelProviderRegistry) modelProviderRegistry {
	registry = normalizeModelProviderRegistry(registry)
	for i := range registry.Vendors {
		envValue := strings.TrimSpace(os.Getenv(registry.Vendors[i].EnvKey))
		if envValue == "" {
			registry.Vendors[i].Configured = false
			registry.Vendors[i].MaskedEnv = ""
			registry.Vendors[i].EnvStatus = "missing"
			continue
		}
		registry.Vendors[i].Configured = true
		registry.Vendors[i].MaskedEnv = maskModelProviderEnv(envValue)
		registry.Vendors[i].EnvStatus = "configured"
	}
	return registry
}

// normalizeModelProviderRegistry 只保留可持久化配置字段，避免保存由本机环境实时派生的状态。
func normalizeModelProviderRegistry(registry modelProviderRegistry) modelProviderRegistry {
	out := modelProviderRegistry{ActiveVendorID: strings.TrimSpace(registry.ActiveVendorID)}
	if len(registry.Vendors) == 0 {
		return out
	}
	out.Vendors = make([]modelProviderVendor, 0, len(registry.Vendors))
	for _, vendor := range registry.Vendors {
		vendor.ID = strings.TrimSpace(vendor.ID)
		vendor.Label = strings.TrimSpace(vendor.Label)
		vendor.BaseURL = strings.TrimSpace(vendor.BaseURL)
		vendor.EnvKey = strings.TrimSpace(vendor.EnvKey)
		vendor.CodexModelProvider = strings.TrimSpace(vendor.CodexModelProvider)
		vendor.DefaultModel = strings.TrimSpace(vendor.DefaultModel)
		vendor.CodexHome = strings.TrimSpace(vendor.CodexHome)
		vendor.CodexInstanceKey = strings.TrimSpace(vendor.CodexInstanceKey)
		vendor.TokenPool.FallbackVendorID = strings.TrimSpace(vendor.TokenPool.FallbackVendorID)
		vendor.Configured = false
		vendor.MaskedEnv = ""
		vendor.EnvStatus = ""
		out.Vendors = append(out.Vendors, vendor)
	}
	return out
}

func findModelProviderVendor(registry modelProviderRegistry, vendorID string) (modelProviderVendor, bool) {
	vendorID = strings.TrimSpace(vendorID)
	for _, vendor := range registry.Vendors {
		if vendor.ID == vendorID {
			return vendor, true
		}
	}
	return modelProviderVendor{}, false
}

// validateModelProviderVendor 校验单个厂商的必填项、URL、env key 和数值字段。
func validateModelProviderVendor(index int, vendor modelProviderVendor) error {
	if err := validateModelProviderRequiredFields(index, vendor); err != nil {
		return err
	}
	if err := validateModelProviderBaseURL(vendor.BaseURL); err != nil {
		return fmt.Errorf("model provider %q baseURL: %w", vendor.ID, err)
	}
	if !modelProviderEnvKeyPattern.MatchString(vendor.EnvKey) {
		return fmt.Errorf("model provider %q envKey must match %s", vendor.ID, modelProviderEnvKeyPattern.String())
	}
	return validateModelProviderNumbers(vendor)
}

// validateModelProviderRequiredFields 保证注册表不会保存缺少关键启动信息的厂商。
func validateModelProviderRequiredFields(index int, vendor modelProviderVendor) error {
	if vendor.ID == "" {
		return fmt.Errorf("model provider vendors[%d].id is required", index)
	}
	if vendor.Label == "" {
		return fmt.Errorf("model provider %q label is required", vendor.ID)
	}
	if vendor.BaseURL == "" {
		return fmt.Errorf("model provider %q baseURL is required", vendor.ID)
	}
	if vendor.EnvKey == "" {
		return fmt.Errorf("model provider %q envKey is required", vendor.ID)
	}
	if vendor.CodexModelProvider == "" {
		return fmt.Errorf("model provider %q codexModelProvider is required", vendor.ID)
	}
	if vendor.DefaultModel == "" {
		return fmt.Errorf("model provider %q defaultModel is required", vendor.ID)
	}
	return nil
}

// validateModelProviderNumbers 拒绝负数、NaN 和 Inf，避免预算或路由优先级保存成不可用状态。
func validateModelProviderNumbers(vendor modelProviderVendor) error {
	if !validModelProviderNumber(vendor.Budget.DailyUSD) {
		return fmt.Errorf("model provider %q budget.dailyUsd must be non-negative", vendor.ID)
	}
	if !validModelProviderNumber(vendor.Budget.MonthlyUSD) {
		return fmt.Errorf("model provider %q budget.monthlyUsd must be non-negative", vendor.ID)
	}
	if !validModelProviderNumber(vendor.TokenPool.Priority) {
		return fmt.Errorf("model provider %q tokenPool.priority must be non-negative", vendor.ID)
	}
	return nil
}

// validateModelProviderBaseURL 确认厂商地址是带 host 的 HTTP(S) 绝对 URL。
func validateModelProviderBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	switch parsed.Scheme {
	case "http", "https":
		return nil
	default:
		return errors.New("must use http or https")
	}
}

// validModelProviderNumber 只接受可序列化且非负的数值。
func validModelProviderNumber(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
