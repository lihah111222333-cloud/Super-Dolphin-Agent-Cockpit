package supportutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrModelResolutionRequired 标记默认模型必须解析但 model/list 不可用或为空。
var ErrModelResolutionRequired = errors.New("codexapp: model resolution required")

// ModelResolutionRequiredError 表示默认模型必须经 model/list 解析但解析失败。
type ModelResolutionRequiredError struct {
	Requested string
	Err       error
}

// NewModelResolutionRequiredError 保留 model/list 根因，同时支持 errors.Is 哨兵匹配。
func NewModelResolutionRequiredError(requested string, err error) error {
	if err == nil {
		err = errors.New("model/list returned no supported models")
	}
	return &ModelResolutionRequiredError{
		Requested: strings.TrimSpace(requested),
		Err:       err,
	}
}

// Error 生成用户可见的模型解析错误，并带上请求模型与 model/list 根因。
func (e *ModelResolutionRequiredError) Error() string {
	if e == nil {
		return ErrModelResolutionRequired.Error()
	}
	if e.Requested != "" {
		return fmt.Sprintf("%s for %q: %v", ErrModelResolutionRequired, e.Requested, e.Err)
	}
	return fmt.Sprintf("%s: %v", ErrModelResolutionRequired, e.Err)
}

// Unwrap 暴露 provider 原始错误，便于调用方保留诊断根因。
func (e *ModelResolutionRequiredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is 让 errors.Is 可以稳定匹配 ErrModelResolutionRequired 哨兵。
func (e *ModelResolutionRequiredError) Is(target error) bool {
	return target == ErrModelResolutionRequired
}

// DecodeAllowedModels 从 Codex model/list 响应中提取模型 ID。
// 兼容 `models`、`data` 和顶层数组三种 wire 形态；都不匹配时返回显式错误。
func DecodeAllowedModels(raw []byte) ([]string, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err == nil {
		if models := modelIDs(top["models"]); len(models) > 0 {
			return models, nil
		}
		if models := modelIDs(top["data"]); len(models) > 0 {
			return models, nil
		}
	}
	var list []any
	if err := json.Unmarshal(raw, &list); err == nil {
		if models := modelIDs(list); len(models) > 0 {
			return models, nil
		}
	}
	return nil, errors.New("codexapp: invalid model/list response")
}

// PreferredCodexModel 从模型列表中选择默认 Codex 模型。
// 固定优先级命中后保留 provider 返回的原始大小写，否则返回第一个非空模型。
func PreferredCodexModel(models []string) string {
	for _, preferred := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5", "codex-auto-review"} {
		for _, model := range models {
			if strings.EqualFold(strings.TrimSpace(model), preferred) {
				return strings.TrimSpace(model)
			}
		}
	}
	for _, model := range models {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// CodexModelListContains 判断 Codex 模型列表是否包含目标模型。
func CodexModelListContains(models []string, requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), requested) {
			return true
		}
	}
	return false
}

// CodexModelResolutionSource 标记模型值来自本次显式覆盖还是默认/继承配置。
type CodexModelResolutionSource string

const (
	// CodexModelResolutionSourceDefault 表示模型来自默认、继承或配置占位值。
	CodexModelResolutionSourceDefault CodexModelResolutionSource = "default"
	// CodexModelResolutionSourceExplicit 表示模型来自本次用户显式覆盖。
	CodexModelResolutionSourceExplicit CodexModelResolutionSource = "explicit"
)

// CodexModelNeedsListResolution 判断默认/继承模型是否需要查询 model/list。
// 空模型或泛化 GPT 默认值需要解析；显式覆盖请使用 CodexModelNeedsListResolutionForSource。
func CodexModelNeedsListResolution(model string) bool {
	return CodexModelNeedsListResolutionForSource(model, CodexModelResolutionSourceDefault)
}

// CodexModelNeedsListResolutionForSource 用模型来源决定是否必须先解析 model/list。
func CodexModelNeedsListResolutionForSource(model string, source CodexModelResolutionSource) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	if source == CodexModelResolutionSourceExplicit {
		return false
	}
	return CodexModelIsGenericGPT(model)
}

// CodexModelIsGenericGPT 判断模型名是否是非 Codex 家族的 GPT 默认值。
// 该判断用于识别旧配置中的默认占位值，并改用当前账号实际支持的 Codex 模型。
func CodexModelIsGenericGPT(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-") && !CodexModelIsCodexFamily(model)
}

// CodexModelIsCodexFamily 判断模型名是否显式属于 Codex 家族。
func CodexModelIsCodexFamily(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "codex")
}

// WrapCodexModelUnsupportedError 为 Codex 模型不支持错误补充用户可读提示。
// 非模型不支持错误原样返回，避免改变其他调用失败的分类。
func WrapCodexModelUnsupportedError(err error, model string) error {
	if err == nil {
		return nil
	}
	notice := CodexModelUnsupportedNotice(err, model)
	if notice == "" {
		return err
	}
	return fmt.Errorf("%s: %w", notice, err)
}

// CodexModelUnsupportedNotice 从 provider 错误中提取模型不可用提示。
// 只有同时指向 Codex/ChatGPT/model/not supported 的错误才会生成设置引导文案。
func CodexModelUnsupportedNotice(err error, model string) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "model") ||
		!strings.Contains(lower, "not supported") ||
		!strings.Contains(lower, "codex") ||
		!strings.Contains(lower, "chatgpt") {
		return ""
	}
	selected := strings.TrimSpace(model)
	if selected == "" {
		selected = quotedModelFromUnsupportedText(text)
	}
	if selected != "" {
		return fmt.Sprintf("Codex model %q is not supported by the current ChatGPT account. Choose a supported Codex model in Settings or clear the model override, then retry", selected)
	}
	return "The selected Codex model is not supported by the current ChatGPT account. Choose a supported Codex model in Settings or clear the model override, then retry"
}

// ConfigString 从运行时配置中读取第一个有效字符串。
// 会过滤前端序列化残留的 undefined/null/[object Object]，避免脏值进入 provider RPC。
func ConfigString(cfg map[string]any, keys ...string) string {
	if cfg == nil {
		return ""
	}
	for _, key := range keys {
		value, _ := cfg[key].(string)
		if value = SanitizeConfigStringArtifact(value); value != "" {
			return value
		}
	}
	return ""
}

// FirstConfigString 是 ConfigString 的别名入口，保留给需要显式表达优先级的调用方。
func FirstConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := ConfigString(cfg, key); value != "" {
			return value
		}
	}
	return ""
}

// SanitizeConfigStringArtifact 清理配置字符串中的前端占位产物。
// 空值和常见 JS 占位都会归一为空字符串，让调用方按缺失处理。
func SanitizeConfigStringArtifact(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		return value
	}
}

// ResolveApprovalPolicy 解析审批策略。
func ResolveApprovalPolicy(cfg map[string]any) string {
	for _, key := range []string{"approvalPolicy", "approval_policy"} {
		if value := ConfigString(cfg, key); value != "" {
			return value
		}
	}
	return "never"
}

// ConfigJSON 将配置项编码为 JSON 原文。
// nil、编码失败或 JSON null 都返回 nil，避免向 provider 发送无意义 patch。
func ConfigJSON(cfg map[string]any, key string) json.RawMessage {
	if cfg == nil || cfg[key] == nil {
		return nil
	}
	raw, err := json.Marshal(cfg[key])
	if err != nil || string(raw) == "null" {
		return nil
	}
	return raw
}

// SortedConfigKeys 返回配置键的稳定排序副本。
// 主要用于诊断和错误输出，空配置返回 nil。
func SortedConfigKeys(cfg map[string]any) []string {
	if len(cfg) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg))
	for key := range cfg {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}

func quotedModelFromUnsupportedText(text string) string {
	const prefix = "The '"
	_, rest, ok := strings.Cut(text, prefix)
	if !ok {
		return ""
	}
	model, _, ok := strings.Cut(rest, "'")
	if !ok || strings.TrimSpace(model) == "" {
		return ""
	}
	return strings.TrimSpace(model)
}

func modelIDs(raw any) []string {
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
