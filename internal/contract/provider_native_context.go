package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// ProviderNativeContextSchemaVersion 标记 provider-native context 的持久化布局版本。
// 版本不匹配时调用方必须重新探测或拒绝恢复，不能复用旧能力结论。
const ProviderNativeContextSchemaVersion = 1

// ProviderNativeProvider* 是当前允许写入 provider-native context 的 provider 白名单。
// 这些 ID 独立于已退役的 canonical skill contract；新增 provider 必须先扩展本契约
// 并补齐独立能力证据，不能由任意输入字符串直接通过。
const (
	ProviderNativeProviderClaude = "claude"
	ProviderNativeProviderCodex  = "codex"
)

// ProviderNativeFeature 标识由 provider CLI/app-server 负责的上下文能力。
type ProviderNativeFeature string

const (
	// ProviderNativeFeatureMemory 标识 provider 原生 memory 能力。
	ProviderNativeFeatureMemory ProviderNativeFeature = "memory"
	// ProviderNativeFeatureSkills 标识 provider 原生 skills 能力。
	ProviderNativeFeatureSkills ProviderNativeFeature = "skills"
)

// Valid 判断 feature 是否属于当前契约声明的固定能力集合。
func (f ProviderNativeFeature) Valid() bool {
	switch f {
	case ProviderNativeFeatureMemory, ProviderNativeFeatureSkills:
		return true
	default:
		return false
	}
}

// ProviderNativeOwner 标识某项能力的实际责任方。
type ProviderNativeOwner string

const (
	// ProviderNativeOwnerProvider 表示能力由 provider CLI/app-server 自己管理。
	ProviderNativeOwnerProvider ProviderNativeOwner = "provider"
	// ProviderNativeOwnerLegacyV3 仅用于迁移期标记既有 V3 管理链，禁止隐式启用。
	ProviderNativeOwnerLegacyV3 ProviderNativeOwner = "v3_legacy"
	// ProviderNativeOwnerDisabled 表示用户或策略显式关闭该能力。
	ProviderNativeOwnerDisabled ProviderNativeOwner = "disabled"
)

// Valid 判断 owner 是否属于当前契约声明的责任方集合。
func (o ProviderNativeOwner) Valid() bool {
	switch o {
	case ProviderNativeOwnerProvider, ProviderNativeOwnerLegacyV3, ProviderNativeOwnerDisabled:
		return true
	default:
		return false
	}
}

// ProviderNativeStatus 标识能力探测或策略裁决的结果。
type ProviderNativeStatus string

const (
	// ProviderNativeStatusSupported 表示已探测并可由声明的 owner 提供能力。
	ProviderNativeStatusSupported ProviderNativeStatus = "supported"
	// ProviderNativeStatusUnsupported 表示已确认当前 provider 不提供该能力。
	ProviderNativeStatusUnsupported ProviderNativeStatus = "unsupported"
	// ProviderNativeStatusUnknown 表示尚未取得足够的 provider 证据。
	ProviderNativeStatusUnknown ProviderNativeStatus = "unknown"
	// ProviderNativeStatusDisabled 表示能力被显式关闭，不等同于 provider 不支持。
	ProviderNativeStatusDisabled ProviderNativeStatus = "disabled"
)

// Valid 判断 status 是否属于当前契约声明的状态集合。
func (s ProviderNativeStatus) Valid() bool {
	switch s {
	case ProviderNativeStatusSupported,
		ProviderNativeStatusUnsupported,
		ProviderNativeStatusUnknown,
		ProviderNativeStatusDisabled:
		return true
	default:
		return false
	}
}

var (
	// ErrProviderNativeContextInvalid 表示 context 或 capability 违反结构化契约。
	ErrProviderNativeContextInvalid = errors.New("provider native context invalid")
	// ErrProviderNativeFeatureInvalid 表示请求了契约未声明的 memory/skills 能力。
	ErrProviderNativeFeatureInvalid = errors.New("provider native feature invalid")
	// ErrProviderNativeCapabilityUnsupported 表示能力已确认不可由 provider-native 路径提供。
	ErrProviderNativeCapabilityUnsupported = errors.New("provider native capability unsupported")
	// ErrProviderNativeCapabilityUnknown 表示尚未取得能力探测证据，禁止猜测为 supported。
	ErrProviderNativeCapabilityUnknown = errors.New("provider native capability unknown")
)

const (
	providerNativeReasonUnsupported      = "capability_unsupported"
	providerNativeReasonUnknown          = "capability_unknown"
	providerNativeReasonDisabled         = "capability_disabled"
	providerNativeReasonInvalid          = "capability_invalid"
	providerNativeReasonProbeRequired    = "capability_probe_required"
	providerNativeReasonProbeUnavailable = "probe_unavailable"
	providerNativeReasonMissingFeature   = "provider_missing_feature"
	providerNativeReasonDisabledByPolicy = "disabled_by_policy"
	providerNativeReasonSupported        = "capability_supported"
	providerNativeTokenAlphabet          = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.:/+"
	providerNativeLabelAlphabet          = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.+"
)

// ProviderNativeFeatureCapability 描述单项 provider-native 能力的 owner、状态和探测证据。
// Evidence/Reason 只接受有限字符的稳定 token；unsupported、unknown、disabled 必须携带 Reason。
type ProviderNativeFeatureCapability struct {
	Owner    ProviderNativeOwner  `json:"owner"`
	Status   ProviderNativeStatus `json:"status"`
	Version  string               `json:"version,omitempty"`
	Evidence string               `json:"evidence,omitempty"`
	Reason   string               `json:"reason,omitempty"`
}

// Validate 校验单项 capability 的枚举值和 owner/status 组合。
func (c ProviderNativeFeatureCapability) Validate(feature ProviderNativeFeature) error {
	return c.validate(feature, "")
}

// validate 校验 capability，并在有 provider 上下文时绑定 provider owner。
func (c ProviderNativeFeatureCapability) validate(feature ProviderNativeFeature, provider string) error {
	if err := validateProviderNativeCapabilityEnums(feature, c); err != nil {
		return err
	}
	if err := validateProviderNativeCapabilityMetadata(feature, c); err != nil {
		return err
	}
	if c.Status == ProviderNativeStatusSupported {
		return c.validateSupported(feature, provider)
	}
	return c.validateNonSupported(feature)
}

// validateProviderNativeCapabilityMetadata 校验所有状态都会持久化的版本和证据 token。
// unknown/unsupported 也不能借状态字段携带路径、账号或其他未受限文本。
func validateProviderNativeCapabilityMetadata(feature ProviderNativeFeature, c ProviderNativeFeatureCapability) error {
	if c.Version != "" {
		if err := validateProviderNativeToken("capability version", c.Version, 128); err != nil {
			return fmt.Errorf("%w: feature %s: %w", ErrProviderNativeContextInvalid, feature, err)
		}
	}
	if c.Evidence != "" {
		if err := validateProviderNativeToken("evidence", c.Evidence, 256); err != nil {
			return fmt.Errorf("%w: feature %s: %w", ErrProviderNativeContextInvalid, feature, err)
		}
	}
	return nil
}

// validateProviderNativeCapabilityEnums 校验 capability 的 feature、owner 和 status 枚举闭合性。
func validateProviderNativeCapabilityEnums(feature ProviderNativeFeature, capability ProviderNativeFeatureCapability) error {
	if !feature.Valid() {
		return fmt.Errorf("%w: %q", ErrProviderNativeFeatureInvalid, feature)
	}
	if !capability.Owner.Valid() {
		return fmt.Errorf("%w: feature %s has unknown owner %q", ErrProviderNativeContextInvalid, feature, capability.Owner)
	}
	if !capability.Status.Valid() {
		return fmt.Errorf("%w: feature %s has unknown status %q", ErrProviderNativeContextInvalid, feature, capability.Status)
	}
	// A legacy V3 owner is never a valid provider-native capability state.
	// Reject it for every status, including unknown/unsupported, so callers
	// cannot reinterpret a legacy decision as an explicit provider result or
	// silently fall back to the retired V3 management chain.
	if capability.Owner == ProviderNativeOwnerLegacyV3 {
		return fmt.Errorf("%w: feature %s legacy owner is not allowed", ErrProviderNativeContextInvalid, feature)
	}
	if capability.Status == ProviderNativeStatusDisabled && capability.Owner != ProviderNativeOwnerDisabled {
		return fmt.Errorf("%w: feature %s disabled status requires disabled owner", ErrProviderNativeContextInvalid, feature)
	}
	if capability.Owner == ProviderNativeOwnerDisabled && capability.Status != ProviderNativeStatusDisabled {
		return fmt.Errorf("%w: feature %s disabled owner requires disabled status", ErrProviderNativeContextInvalid, feature)
	}
	return nil
}

// validateSupported 校验 provider-owned supported capability 必须具备安全 evidence。
func (c ProviderNativeFeatureCapability) validateSupported(feature ProviderNativeFeature, provider string) error {
	if c.Owner != ProviderNativeOwnerProvider {
		return fmt.Errorf("%w: feature %s supported status requires provider owner", ErrProviderNativeContextInvalid, feature)
	}
	if provider != "" && !isProviderNativeProvider(provider) {
		return fmt.Errorf("%w: provider %q is not allowed", ErrProviderNativeContextInvalid, provider)
	}
	if strings.TrimSpace(c.Evidence) == "" {
		return fmt.Errorf("%w: feature %s supported status requires evidence", ErrProviderNativeContextInvalid, feature)
	}
	return validateProviderNativeReason(feature, c.Status, c.Reason)
}

func (c ProviderNativeFeatureCapability) validateNonSupported(feature ProviderNativeFeature) error {
	if strings.TrimSpace(c.Reason) == "" {
		return fmt.Errorf("%w: feature %s status %s requires reason", ErrProviderNativeContextInvalid, feature, c.Status)
	}
	return validateProviderNativeReason(feature, c.Status, c.Reason)
}

func validateProviderNativeReason(feature ProviderNativeFeature, status ProviderNativeStatus, reason string) error {
	if reason == "" {
		return nil
	}
	if err := validateProviderNativeCode("reason", reason, 64); err != nil {
		return fmt.Errorf("%w: feature %s: %w", ErrProviderNativeContextInvalid, feature, err)
	}
	if !providerNativeReasonAllowed(status, reason) {
		return fmt.Errorf("%w: feature %s has invalid reason code", ErrProviderNativeContextInvalid, feature)
	}
	return nil
}

// providerNativeReasonAllowed 限定每种能力状态可使用的固定 reason code，避免任意文本进入契约。
func providerNativeReasonAllowed(status ProviderNativeStatus, reason string) bool {
	switch status {
	case ProviderNativeStatusSupported:
		return reason == providerNativeReasonSupported
	case ProviderNativeStatusUnknown:
		return reason == providerNativeReasonProbeRequired ||
			reason == providerNativeReasonProbeUnavailable ||
			reason == providerNativeReasonUnknown
	case ProviderNativeStatusUnsupported:
		return reason == providerNativeReasonMissingFeature || reason == providerNativeReasonUnsupported
	case ProviderNativeStatusDisabled:
		return reason == providerNativeReasonDisabledByPolicy || reason == providerNativeReasonDisabled
	default:
		return false
	}
}

// ProviderNativeCapabilities 固定承载 memory 与 skills 两项 provider-native 能力。
// 字段由反射枚举，后续新增字段时校验和能力查询不会依赖复制的手工字段列表。
type ProviderNativeCapabilities struct {
	Memory ProviderNativeFeatureCapability `json:"memory"`
	Skills ProviderNativeFeatureCapability `json:"skills"`
}

// ProviderNativeFeatures 返回 capabilities 结构声明的全部 feature 名称。
// 返回新切片，调用方修改结果不会影响后续校验。
func ProviderNativeFeatures() []ProviderNativeFeature {
	typ := reflect.TypeOf(ProviderNativeCapabilities{})
	features := make([]ProviderNativeFeature, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		features = append(features, ProviderNativeFeature(name))
	}
	sort.Slice(features, func(i, j int) bool { return features[i] < features[j] })
	return features
}

// Capability 返回指定 feature 的 capability；未知 feature 会返回 typed invalid 错误。
func (c ProviderNativeCapabilities) Capability(feature ProviderNativeFeature) (ProviderNativeFeatureCapability, error) {
	if !feature.Valid() {
		return ProviderNativeFeatureCapability{}, fmt.Errorf("%w: %q", ErrProviderNativeFeatureInvalid, feature)
	}
	typ := reflect.TypeOf(c)
	value := reflect.ValueOf(c)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != string(feature) {
			continue
		}
		capability, ok := value.Field(i).Interface().(ProviderNativeFeatureCapability)
		if !ok {
			return ProviderNativeFeatureCapability{}, fmt.Errorf("%w: field %s has incompatible type", ErrProviderNativeContextInvalid, name)
		}
		return capability, nil
	}
	return ProviderNativeFeatureCapability{}, fmt.Errorf("%w: %q", ErrProviderNativeFeatureInvalid, feature)
}

// Validate 校验 memory/skills 两项 capability，拒绝缺失的显式状态。
func (c ProviderNativeCapabilities) Validate() error {
	return c.validateForProvider("")
}

func (c ProviderNativeCapabilities) validateForProvider(provider string) error {
	features := ProviderNativeFeatures()
	if len(features) == 0 {
		return fmt.Errorf("%w: no features declared", ErrProviderNativeContextInvalid)
	}
	for _, feature := range features {
		capability, err := c.Capability(feature)
		if err != nil {
			return err
		}
		if err := capability.validate(feature, provider); err != nil {
			return err
		}
	}
	return nil
}

// ProviderNativeContext 是 start/resume 共享的 provider-native 运行上下文快照。
// V3 只传递 provider、cwd、home/profile 和探测结果，不在此结构中承载 memory/skill 正文。
type ProviderNativeContext struct {
	SchemaVersion   int                        `json:"schemaVersion"`
	Provider        string                     `json:"provider"`
	ProviderVersion string                     `json:"providerVersion,omitempty"`
	CWD             string                     `json:"cwd"`
	ProviderHome    string                     `json:"providerHome,omitempty"`
	Profile         string                     `json:"profile,omitempty"`
	Capabilities    ProviderNativeCapabilities `json:"capabilities"`
}

// Validate 校验 context 版本、白名单 provider、canonical cwd、元数据和 capabilities。
func (c ProviderNativeContext) Validate() error {
	if c.SchemaVersion != ProviderNativeContextSchemaVersion {
		return fmt.Errorf("%w: schema version %d is unsupported", ErrProviderNativeContextInvalid, c.SchemaVersion)
	}
	provider, err := CanonicalProviderNativeProvider(c.Provider)
	if err != nil {
		return err
	}
	canonicalCWD, err := CanonicalizeProviderNativeCWD(c.CWD)
	if err != nil {
		return fmt.Errorf("%w: cwd: %w", ErrProviderNativeContextInvalid, err)
	}
	if c.Provider != provider {
		return fmt.Errorf("%w: provider must use canonical name", ErrProviderNativeContextInvalid)
	}
	if c.CWD != canonicalCWD {
		return fmt.Errorf("%w: cwd must be an existing canonical absolute directory", ErrProviderNativeContextInvalid)
	}
	if err := validateProviderNativeContextMetadata(c); err != nil {
		return err
	}
	return c.Capabilities.validateForProvider(provider)
}

// validateProviderNativeContextMetadata 校验 provider context 中的版本、profile 与 home 元数据。
func validateProviderNativeContextMetadata(ctx ProviderNativeContext) error {
	if ctx.ProviderVersion != "" {
		if err := validateProviderNativeCode("provider version", ctx.ProviderVersion, 128); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderNativeContextInvalid, err)
		}
	}
	if ctx.Profile != "" {
		if err := validateProviderNativeCode("profile", ctx.Profile, 64); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderNativeContextInvalid, err)
		}
	}
	if ctx.ProviderHome != "" {
		if err := validateProviderNativeAbsolutePath("provider home", ctx.ProviderHome); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderNativeContextInvalid, err)
		}
		if err := validateProviderNativeHomeRealpath(ctx.ProviderHome); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderNativeContextInvalid, err)
		}
	}
	return nil
}

// validateProviderNativeAbsolutePath 拒绝相对、非 clean 或带隐式空白的路径。
func validateProviderNativeAbsolutePath(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", name)
	}
	if trimmed != value {
		return fmt.Errorf("%s must not contain surrounding whitespace", name)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be absolute", name)
	}
	if filepath.Clean(value) != value {
		return fmt.Errorf("%s must be clean", name)
	}
	return nil
}

// validateProviderNativeHomeRealpath 拒绝已存在且解析到其他位置的 home symlink。
func validateProviderNativeHomeRealpath(value string) error {
	real, err := filepath.EvalSymlinks(value)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("provider home realpath resolution failed: %w", err)
	}
	if filepath.Clean(real) != value {
		return errors.New("provider home must not resolve through a symlink")
	}
	return nil
}

// CanonicalizeProviderNativeCWD 将 cwd 解析为存在的绝对真实目录。
// 调用方必须持久化返回值；Validate 不会替调用方静默改写原始路径。
func CanonicalizeProviderNativeCWD(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("cwd is required")
	}
	if !filepath.IsAbs(trimmed) {
		return "", errors.New("cwd must be absolute")
	}
	cleaned := filepath.Clean(trimmed)
	real, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve cwd realpath: %w", err)
	}
	real = filepath.Clean(real)
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("stat cwd: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("cwd is not a directory")
	}
	return real, nil
}

// CanonicalizeProviderNativeContext 规范化 provider 和 cwd 后再执行完整契约校验。
// 该显式转换入口避免 Validate 通过后仍携带未规范化路径。
func CanonicalizeProviderNativeContext(ctx ProviderNativeContext) (ProviderNativeContext, error) {
	provider, err := CanonicalProviderNativeProvider(ctx.Provider)
	if err != nil {
		return ProviderNativeContext{}, err
	}
	cwd, err := CanonicalizeProviderNativeCWD(ctx.CWD)
	if err != nil {
		return ProviderNativeContext{}, fmt.Errorf("%w: cwd: %w", ErrProviderNativeContextInvalid, err)
	}
	ctx.Provider = provider
	ctx.CWD = cwd
	if err := ctx.Validate(); err != nil {
		return ProviderNativeContext{}, err
	}
	return ctx, nil
}

// CanonicalProviderNativeProvider 将 provider 名称收敛到白名单内的小写 canonical 值。
// 该函数可用于构造 context；Validate 仍会拒绝未 canonical 化的原始字段。
func CanonicalProviderNativeProvider(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: provider is required", ErrProviderNativeContextInvalid)
	}
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case ProviderNativeProviderClaude, ProviderNativeProviderCodex:
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: provider %q is not supported", ErrProviderNativeContextInvalid, normalized)
	}
}

// IsProviderNativeProvider 判断名称是否属于 provider-native context 白名单。
func IsProviderNativeProvider(provider string) bool {
	return isProviderNativeProvider(provider)
}

func isProviderNativeProvider(provider string) bool {
	switch provider {
	case ProviderNativeProviderClaude, ProviderNativeProviderCodex:
		return true
	default:
		return false
	}
}

// validateProviderNativeToken 拒绝空白、超长或包含控制字符的自由文本 token。
func validateProviderNativeToken(name, value string, maxLen int) error {
	return validateProviderNativeValue(name, value, maxLen, providerNativeTokenAlphabet)
}

func validateProviderNativeCode(name, value string, maxLen int) error {
	return validateProviderNativeValue(name, value, maxLen, providerNativeLabelAlphabet)
}

// validateProviderNativeValue 按指定字符集验证有限长度的 provider-native 字段。
func validateProviderNativeValue(name, value string, maxLen int, alphabet string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", name)
	}
	if trimmed != value {
		return fmt.Errorf("%s must not contain surrounding whitespace", name)
	}
	if len(value) > maxLen {
		return fmt.Errorf("%s exceeds %d bytes", name, maxLen)
	}
	for _, r := range value {
		if !strings.ContainsRune(alphabet, r) {
			return fmt.Errorf("%s must be a stable token", name)
		}
	}
	return nil
}

// UnmarshalJSON 对 provider-native context 使用 additionalProperties=false 的严格解码。
// 解析成功后立即 Validate，避免不完整快照进入恢复链。
func (c *ProviderNativeContext) UnmarshalJSON(data []byte) error {
	if c == nil {
		return ErrProviderNativeContextInvalid
	}
	if err := rejectDuplicateProviderNativeJSONKeys(data); err != nil {
		return err
	}
	type wire ProviderNativeContext
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value wire
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrProviderNativeContextInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrProviderNativeContextInvalid)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrProviderNativeContextInvalid, err)
	}
	ctx := ProviderNativeContext(value)
	if err := ctx.Validate(); err != nil {
		return err
	}
	*c = ctx
	return nil
}

func rejectDuplicateProviderNativeJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := consumeProviderNativeJSONValue(decoder, providerNativeContextJSONKeys()); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrProviderNativeContextInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: multiple JSON values", ErrProviderNativeContextInvalid)
	}
	return nil
}

// providerNativeContextJSONKeys 返回顶层严格 JSON allow-list 的新副本。
// 这些表故意不作为 package global 保存，避免任意调用方通过 map alias 改写解码契约。
func providerNativeContextJSONKeys() map[string]struct{} {
	return map[string]struct{}{
		"schemaVersion": {}, "provider": {}, "providerVersion": {}, "cwd": {},
		"providerHome": {}, "profile": {}, "capabilities": {},
	}
}

func providerNativeCapabilitiesJSONKeys() map[string]struct{} {
	return map[string]struct{}{
		"memory": {}, "skills": {},
	}
}

func providerNativeCapabilityJSONKeys() map[string]struct{} {
	return map[string]struct{}{
		"owner": {}, "status": {}, "version": {}, "evidence": {}, "reason": {},
	}
}

func consumeProviderNativeJSONValue(decoder *json.Decoder, allowedKeys map[string]struct{}) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return consumeProviderNativeJSONObject(decoder, allowedKeys)
	case '[':
		return consumeProviderNativeJSONArray(decoder)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

// consumeProviderNativeJSONObject 扫描对象并拒绝重复字段名。
func consumeProviderNativeJSONObject(decoder *json.Decoder, allowedKeys map[string]struct{}) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("object key is not a string")
		}
		if allowedKeys != nil {
			if _, allowed := allowedKeys[name]; !allowed {
				return fmt.Errorf("unknown JSON field %q", name)
			}
		}
		canonicalName := strings.ToLower(name)
		if _, exists := seen[canonicalName]; exists {
			return fmt.Errorf("duplicate JSON field %q", name)
		}
		seen[canonicalName] = struct{}{}
		if err := consumeProviderNativeJSONValue(decoder, providerNativeJSONChildKeys(name)); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func consumeProviderNativeJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := consumeProviderNativeJSONValue(decoder, nil); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func providerNativeJSONChildKeys(key string) map[string]struct{} {
	// The field names are unique within this wire shape, so key-based routing is
	// sufficient after each parent-level allow-list check.
	switch key {
	case "capabilities":
		return providerNativeCapabilitiesJSONKeys()
	case "memory", "skills":
		return providerNativeCapabilityJSONKeys()
	default:
		return nil
	}
}

// RequireProviderFeature 要求指定 feature 已由 provider-native 路径明确提供。
// legacy、disabled、unsupported 和 unknown 都会返回 typed error，绝不隐式回退到 V3 管理链。
func (c ProviderNativeContext) RequireProviderFeature(feature ProviderNativeFeature) error {
	if err := c.Validate(); err != nil {
		return err
	}
	capability, err := c.Capabilities.Capability(feature)
	if err != nil {
		return err
	}
	if capability.Status == ProviderNativeStatusUnknown {
		return newProviderNativeCapabilityError(
			ErrProviderNativeCapabilityUnknown,
			c.Provider,
			feature,
			capability,
		)
	}
	if capability.Status != ProviderNativeStatusSupported || capability.Owner != ProviderNativeOwnerProvider {
		return newProviderNativeCapabilityError(
			ErrProviderNativeCapabilityUnsupported,
			c.Provider,
			feature,
			capability,
		)
	}
	return nil
}

// ProviderNativeCapabilityError 携带 provider、feature、owner、status 和拒绝原因。
// 调用方可用 errors.Is/As 保留稳定的 unsupported/unknown 分类。
type ProviderNativeCapabilityError struct {
	Err      error
	Provider string
	Feature  ProviderNativeFeature
	Owner    ProviderNativeOwner
	Status   ProviderNativeStatus
	Reason   string
}

// Error 返回可诊断的 provider-native capability 错误文本。
func (e ProviderNativeCapabilityError) Error() string {
	provider := sanitizeProviderNativeLabel(e.Provider, "provider")
	feature := sanitizeProviderNativeLabel(string(e.Feature), "feature")
	owner := sanitizeProviderNativeLabel(string(e.Owner), "owner")
	status := sanitizeProviderNativeLabel(string(e.Status), "status")
	reason := providerNativeReasonCode(e.Status)
	return fmt.Sprintf(
		"%s: provider=%s feature=%s owner=%s status=%s reason_code=%s",
		providerNativeCapabilityErrorText(e.Err),
		provider,
		feature,
		owner,
		status,
		reason,
	)
}

// Unwrap 暴露稳定哨兵，支持 errors.Is 分类。
func (e ProviderNativeCapabilityError) Unwrap() error { return e.Err }

func newProviderNativeCapabilityError(
	err error,
	provider string,
	feature ProviderNativeFeature,
	capability ProviderNativeFeatureCapability,
) error {
	return ProviderNativeCapabilityError{
		Err:      err,
		Provider: strings.TrimSpace(provider),
		Feature:  feature,
		Owner:    capability.Owner,
		Status:   capability.Status,
		Reason:   providerNativeReasonCode(capability.Status),
	}
}

func providerNativeReasonCode(status ProviderNativeStatus) string {
	switch status {
	case ProviderNativeStatusUnsupported:
		return providerNativeReasonUnsupported
	case ProviderNativeStatusUnknown:
		return providerNativeReasonUnknown
	case ProviderNativeStatusDisabled:
		return providerNativeReasonDisabled
	default:
		return providerNativeReasonInvalid
	}
}

func sanitizeProviderNativeLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return fallback
	}
	for _, r := range value {
		if !strings.ContainsRune(providerNativeLabelAlphabet, r) {
			return fallback
		}
	}
	return value
}

func providerNativeCapabilityErrorText(err error) string {
	switch {
	case errors.Is(err, ErrProviderNativeCapabilityUnknown):
		return ErrProviderNativeCapabilityUnknown.Error()
	case errors.Is(err, ErrProviderNativeCapabilityUnsupported):
		return ErrProviderNativeCapabilityUnsupported.Error()
	case errors.Is(err, ErrProviderNativeContextInvalid):
		return ErrProviderNativeContextInvalid.Error()
	default:
		return "provider native capability error"
	}
}
