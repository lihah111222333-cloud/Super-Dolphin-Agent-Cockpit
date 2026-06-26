package builtinprompts

import (
	"fmt"
	"sort"
	"strings"
)

// allowedKinds 是内置 prompt 模板支持的类型白名单。
var allowedKinds = map[string]struct{}{
	"base":         {},
	"expert":       {},
	"recall":       {},
	"default_rule": {},
}

// allowedScopes 是内置 prompt 可声明的生效范围白名单。
var allowedScopes = map[string]struct{}{
	"global":      {},
	"projectless": {},
}

// allowedRegions 是 section 可注入区域白名单。
var allowedRegions = map[string]struct{}{
	"static":  {},
	"dynamic": {},
}

// allowedTriggerTypes 是 section 触发方式白名单。
var allowedTriggerTypes = map[string]struct{}{
	"always":  {},
	"keyword": {},
	"recall":  {},
}

// externalIdentityPatterns 捕获内置 prompt 中禁止出现的外部 provider 身份声明。
var externalIdentityPatterns = []identityPattern{
	{phrase: "you are claude code", compact: "youareclaudecode"},
	{phrase: "you are claude", compact: "youareclaude"},
	{phrase: "act as claude", compact: "actasclaude"},
	{phrase: "我是 claude", compact: "我是claude"},
	{phrase: "你是 claude", compact: "你是claude"},
	{phrase: "作为 claude", compact: "作为claude"},
}

// directIdentityNegationPatterns 允许明确否定外部身份声明的句式通过。
var directIdentityNegationPatterns = []identityPattern{
	{phrase: "never say you are claude code", compact: "neversayyouareclaudecode"},
	{phrase: "never say you are claude", compact: "neversayyouareclaude"},
	{phrase: "do not say you are claude code", compact: "donotsayyouareclaudecode"},
	{phrase: "do not say you are claude", compact: "donotsayyouareclaude"},
	{phrase: "don't say you are claude code", compact: "don'tsayyouareclaudecode"},
	{phrase: "don't say you are claude", compact: "don'tsayyouareclaude"},
	{phrase: "dont say you are claude code", compact: "dontsayyouareclaudecode"},
	{phrase: "dont say you are claude", compact: "dontsayyouareclaude"},
}

// identityPattern 保存普通文本和紧凑文本两种匹配形态。
type identityPattern struct {
	phrase  string
	compact string
}

// validateManifest 校验 manifest 版本、模板列表和路径唯一性。
func validateManifest(manifest manifestConfig) error {
	if manifest.Version != 1 {
		return fmt.Errorf("builtin prompts: manifest version must be 1")
	}
	if len(manifest.Templates) == 0 {
		return fmt.Errorf("builtin prompts: manifest templates is required")
	}
	seen := map[string]struct{}{}
	for _, path := range manifest.Templates {
		if path == "" {
			return fmt.Errorf("builtin prompts: manifest template path is required")
		}
		if _, ok := seen[path]; ok {
			return fmt.Errorf("builtin prompts: duplicate manifest template %q", path)
		}
		seen[path] = struct{}{}
	}
	return nil
}

// validateTemplateConfig 校验单个模板配置的必填项、枚举和 section。
func validateTemplateConfig(path string, cfg templateConfig) error {
	if err := validateTemplateRequired(path, cfg); err != nil {
		return err
	}
	if err := validateTemplateEnums(path, cfg); err != nil {
		return err
	}
	if err := validateTags(path, cfg.Tags); err != nil {
		return err
	}
	return validateSectionConfigs(path, cfg)
}

// validateTemplateRequired 校验模板必填字段，内置模板 ID 必须保持负数。
func validateTemplateRequired(path string, cfg templateConfig) error {
	switch {
	case cfg.ID != nil && *cfg.ID >= 0:
		return fmt.Errorf("builtin prompts: %s id must be negative", path)
	case cfg.PromptKey == "":
		return fmt.Errorf("builtin prompts: %s prompt_key is required", path)
	case cfg.Kind == "":
		return fmt.Errorf("builtin prompts: %s kind is required", path)
	case cfg.Title == "":
		return fmt.Errorf("builtin prompts: %s title is required", path)
	case cfg.AgentKey == "":
		return fmt.Errorf("builtin prompts: %s agent_key is required", path)
	case cfg.Enabled == nil:
		return fmt.Errorf("builtin prompts: %s enabled is required", path)
	case cfg.Scope == "":
		return fmt.Errorf("builtin prompts: %s scope is required", path)
	case len(cfg.Sections) == 0:
		return fmt.Errorf("builtin prompts: %s sections is required", path)
	default:
		return nil
	}
}

// validateTemplateEnums 校验模板 kind、scope 以及 default_rule 的 agent_key 约束。
func validateTemplateEnums(path string, cfg templateConfig) error {
	if _, ok := allowedKinds[cfg.Kind]; !ok {
		return fmt.Errorf("builtin prompts: %s invalid kind %q", path, cfg.Kind)
	}
	if _, ok := allowedScopes[cfg.Scope]; !ok {
		return fmt.Errorf("builtin prompts: %s invalid scope %q", path, cfg.Scope)
	}
	if cfg.Kind == "default_rule" && cfg.AgentKey != "default_rule" {
		return fmt.Errorf("builtin prompts: %s default_rule agent_key must be default_rule", path)
	}
	return nil
}

// validateTags 要求内置模板带 builtin:system，且不能携带项目私有 scope 标签。
func validateTags(path string, tags []string) error {
	hasBuiltin := false
	for _, tag := range tags {
		if tag == "builtin:system" {
			hasBuiltin = true
		}
		if strings.HasPrefix(tag, "scope.cwd:") {
			return fmt.Errorf("builtin prompts: %s builtin tags must not include scope.cwd:*", path)
		}
	}
	if !hasBuiltin {
		return fmt.Errorf("builtin prompts: %s tags must include builtin:system", path)
	}
	return nil
}

// validateSectionConfigs 校验所有 section 并阻止 section_key 重复。
func validateSectionConfigs(path string, cfg templateConfig) error {
	seen := map[string]struct{}{}
	for _, section := range cfg.Sections {
		if err := validateSectionConfig(path, cfg.Kind, section); err != nil {
			return err
		}
		if _, ok := seen[section.SectionKey]; ok {
			return fmt.Errorf("builtin prompts: %s duplicate section_key %q", path, section.SectionKey)
		}
		seen[section.SectionKey] = struct{}{}
	}
	return nil
}

// validateSectionConfig 校验单个 section 的必填项、区域和触发方式。
func validateSectionConfig(path, kind string, section sectionConfig) error {
	if err := validateSectionRequired(path, section); err != nil {
		return err
	}
	if _, ok := allowedRegions[section.Region]; !ok {
		return fmt.Errorf("builtin prompts: %s invalid section region %q", path, section.Region)
	}
	if _, ok := allowedTriggerTypes[section.TriggerType]; !ok {
		return fmt.Errorf("builtin prompts: %s invalid section trigger_type %q", path, section.TriggerType)
	}
	if section.TriggerType == "recall" && section.RecallTopic == "" {
		return fmt.Errorf("builtin prompts: %s recall section %q requires recall_topic", path, section.SectionKey)
	}
	if kind == "recall" && section.TriggerType != "recall" {
		return fmt.Errorf("builtin prompts: %s recall template section %q must use recall trigger_type", path, section.SectionKey)
	}
	return nil
}

// validateSectionRequired 校验 section 基础字段，正文文件必须显式声明。
func validateSectionRequired(path string, section sectionConfig) error {
	switch {
	case section.SectionKey == "":
		return fmt.Errorf("builtin prompts: %s section_key is required", path)
	case section.Region == "":
		return fmt.Errorf("builtin prompts: %s section %q region is required", path, section.SectionKey)
	case section.TriggerType == "":
		return fmt.Errorf("builtin prompts: %s section %q trigger_type is required", path, section.SectionKey)
	case section.BodyFile == "":
		return fmt.Errorf("builtin prompts: %s section %q body_file is required", path, section.SectionKey)
	default:
		return nil
	}
}

// validateLoadedTemplates 校验加载后的模板 prompt_key、解析 ID 和 section 正文。
func validateLoadedTemplates(templates []loadedTemplate) error {
	seenKeys := map[string]struct{}{}
	for _, template := range templates {
		if _, ok := seenKeys[template.Config.PromptKey]; ok {
			return fmt.Errorf("builtin prompts: duplicate prompt_key %q", template.Config.PromptKey)
		}
		seenKeys[template.Config.PromptKey] = struct{}{}
		if err := validateLoadedSections(template); err != nil {
			return err
		}
	}
	ordered := append([]loadedTemplate(nil), templates...)
	sort.SliceStable(ordered, loadedTemplateLess(ordered))
	seenIDs := map[int64]string{}
	for i, template := range ordered {
		id := builtinTemplateID(template.Config, i)
		if existing, ok := seenIDs[id]; ok {
			return fmt.Errorf("builtin prompts: duplicate resolved template id %d for %q and %q", id, existing, template.Config.PromptKey)
		}
		seenIDs[id] = template.Config.PromptKey
	}
	return nil
}

// validateLoadedSections 校验 section 正文非空且不包含外部 provider 身份声明。
func validateLoadedSections(template loadedTemplate) error {
	for _, section := range template.Sections {
		if strings.TrimSpace(section.Body) == "" {
			return fmt.Errorf("builtin prompts: %s section %q body_file is empty", template.Path, section.Config.SectionKey)
		}
		if containsExternalProviderIdentity(section.Body) {
			return fmt.Errorf("builtin prompts: %s section %q contains external provider identity", template.Path, section.Config.SectionKey)
		}
	}
	return nil
}

// containsExternalProviderIdentity 判断正文是否声明了 Claude 等外部 provider 身份。
func containsExternalProviderIdentity(body string) bool {
	normalized := normalizeIdentityText(body)
	compact := strings.ReplaceAll(normalized, " ", "")
	for _, pattern := range externalIdentityPatterns {
		if phraseMatchesIdentityPattern(normalized, pattern.phrase, false) {
			return true
		}
		if phraseMatchesIdentityPattern(compact, pattern.compact, true) {
			return true
		}
	}
	return false
}

// phraseMatchesIdentityPattern 查找身份短语，并允许直接否定句式豁免。
func phraseMatchesIdentityPattern(text, pattern string, compact bool) bool {
	for start := 0; start < len(text); {
		idx := strings.Index(text[start:], pattern)
		if idx < 0 {
			return false
		}
		absolute := start + idx
		if !hasDirectIdentityNegation(text, absolute, pattern, compact) {
			return true
		}
		start = absolute + len(pattern)
	}
	return false
}

// hasDirectIdentityNegation 判断命中的身份短语是否被紧邻的禁止声明覆盖。
func hasDirectIdentityNegation(text string, idx int, pattern string, compact bool) bool {
	for _, negation := range directIdentityNegationPatterns {
		negationText := negation.phrase
		if compact {
			negationText = negation.compact
		}
		patternOffset := strings.Index(negationText, pattern)
		if patternOffset < 0 {
			continue
		}
		start := idx - patternOffset
		end := start + len(negationText)
		if start >= 0 && end <= len(text) && text[start:end] == negationText {
			return true
		}
	}
	return false
}

// normalizeIdentityText 把正文统一成小写空格分隔文本，降低标点和换行差异。
func normalizeIdentityText(body string) string {
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		".", " ",
		",", " ",
		":", " ",
		";", " ",
		"。", " ",
		"，", " ",
		"：", " ",
		"；", " ",
		"、", " ",
		"`", " ",
		"\"", " ",
		"“", " ",
		"”", " ",
		"「", " ",
		"」", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(strings.ToLower(body))), " ")
}
