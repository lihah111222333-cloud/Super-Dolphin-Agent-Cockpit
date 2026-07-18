package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	skillidentity "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/identity"
	"gopkg.in/yaml.v3"
)

type skillRecord struct {
	info SkillInfo
	path string
	rel  string
}

var internalSkillMarkerSummaryPattern = regexp.MustCompile(`^</?[A-Z][A-Z0-9_-]*>$`)

func scanSkillEntryDepth(rootPath, path string) (int, error) {
	rel, err := filepath.Rel(rootPath, path)
	if err != nil {
		return 0, err
	}
	if rel == "." {
		return 0, nil
	}
	return strings.Count(rel, string(filepath.Separator)) + 1, nil
}

func visitSkillDir(rootPath, path, name string, _ int) error {
	if path != rootPath && strings.HasPrefix(name, ".") && name != ".system" {
		return filepath.SkipDir
	}
	if name == ".git" {
		return filepath.SkipDir
	}
	return nil
}

// parseSkillRecord 读取并解析单个 SKILL.md。
// 扫描期必须先拒绝 symlink、非普通文件和超大文件，避免 canonical 刷新时越界读或耗尽内存。
func parseSkillRecord(root, path string, defaultTrust TrustScope) (skillRecord, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return skillRecord{}, err
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		return skillRecord{}, fmt.Errorf("skill file is symlink: %s", path)
	}
	if !stat.Mode().IsRegular() {
		return skillRecord{}, fmt.Errorf("skill file is not a regular file: %s", path)
	}
	if stat.Size() > maxSkillFileBytes {
		return skillRecord{}, fmt.Errorf("skill file too large: %s is %d bytes, limit %d", path, stat.Size(), maxSkillFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return skillRecord{}, err
	}
	dir := filepath.Dir(path)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return skillRecord{}, err
	}
	info, err := parseSkillInfo(rel, dir, string(data), defaultTrust)
	if err != nil {
		return skillRecord{}, err
	}
	return skillRecord{info: info, path: path, rel: filepath.ToSlash(rel)}, nil
}

// parseSkillInfo 解析技能info。
func parseSkillInfo(rel, dir, content string, defaultTrust TrustScope) (SkillInfo, error) {
	info := SkillInfo{Name: fallbackSkillName(rel), Dir: dir}
	frontmatter, body, ok := splitFrontmatter(content)
	if ok {
		lines := strings.Split(frontmatter, "\n")
		for i := 0; i < len(lines); i++ {
			key, value, ok := parseMetaLine(lines[i])
			if !ok {
				continue
			}
			i += applyMetaLine(&info, key, value, lines[i+1:])
		}
		if err := applyYAMLFrontmatter(&info, frontmatter); err != nil {
			return SkillInfo{}, err
		}
	} else {
		body = content
	}
	if info.Name == "" {
		info.Name = fallbackSkillName(rel)
	}
	if info.Summary == "" {
		info.Summary = summarizeSkillBody(body, info.Description)
	}
	info.Description = truncateRunes(info.Description, 120)
	info.Summary = truncateRunes(info.Summary, 220)
	info.TriggerWords = uniqStrings(append(info.TriggerWords, "@"+info.Name, "[skill:"+info.Name+"]"))
	info.ForceWords = uniqStrings(info.ForceWords)
	info.ReplacesNative = normalizeReplacesNative(info.ReplacesNative)
	// 信任域和安全字段在解析后统一收口，确保 frontmatter 缺失时仍有明确默认值。
	info.AllowedTools = uniqStrings(info.AllowedTools)
	if info.Trust == TrustUnknown {
		if defaultTrust.Valid() {
			info.Trust = defaultTrust
		} else {
			info.Trust = TrustProject // 安全兜底：未知源按不受信任处理。
		}
	}
	info.Trust = capSkillTrustByRoot(info.Trust, defaultTrust)
	// ContentHash 覆盖完整 SKILL.md 内容，用于审批缓存的 TOCTOU 防护。
	// frontmatter 或正文任一变化都会改变 hash 并触发重新审批。
	sum := sha256.Sum256([]byte(content))
	info.ContentHash = hex.EncodeToString(sum[:])
	return info, nil
}

// capSkillTrustByRoot 按 skill 所在根目录限制 frontmatter 声明的信任域。
// 项目根下的 skill 不能通过 metadata 自行升级成 user/signed 信任。
func capSkillTrustByRoot(trust, defaultTrust TrustScope) TrustScope {
	if !trust.Valid() {
		return TrustProject
	}
	switch defaultTrust {
	case TrustUser:
		if trust == TrustSigned {
			return TrustUser
		}
		return trust
	case TrustSigned:
		return trust
	default:
		if trust.Trusted() {
			return TrustProject
		}
		return trust
	}
}

func fallbackSkillName(rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return "skill"
	}
	parts := strings.Split(rel, "/")
	return strings.TrimSpace(parts[len(parts)-1])
}

func splitFrontmatter(content string) (string, string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	frontmatter, tail, ok := strings.Cut(content[4:], "\n---")
	if !ok {
		return "", content, false
	}
	return frontmatter, strings.TrimPrefix(tail, "\n"), true
}

func parseMetaLine(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

var metaScalarAppliers = map[string]func(*SkillInfo, string){
	"name": func(info *SkillInfo, value string) {
		info.Name = parseScalar(value)
	},
	"display_name": func(info *SkillInfo, value string) {
		info.DisplayName = parseScalar(value)
	},
	"display-name": func(info *SkillInfo, value string) {
		info.DisplayName = parseScalar(value)
	},
	"displayname": func(info *SkillInfo, value string) {
		info.DisplayName = parseScalar(value)
	},
	"title": func(info *SkillInfo, value string) {
		info.DisplayName = parseScalar(value)
	},
	"description": func(info *SkillInfo, value string) {
		info.Description = parseScalar(value)
	},
	"summary": func(info *SkillInfo, value string) {
		info.Summary = parseScalar(value)
	},
	"digest": func(info *SkillInfo, value string) {
		info.Summary = parseScalar(value)
	},
}

var triggerWordMetaKeys = map[string]struct{}{"trigger_words": {}, "triggerwords": {}, "triggers": {}, "aliases": {}, "tags": {}, "keywords": {}}

var forceWordMetaKeys = map[string]struct{}{"force_words": {}, "forcewords": {}, "mandatory_words": {}, "must_words": {}}

var allowedToolsMetaKeys = map[string]struct{}{"allowed-tools": {}, "allowed_tools": {}, "allowedtools": {}, "tools": {}}

var trustMetaKeys = map[string]struct{}{"trust": {}, "trust_scope": {}, "trustscope": {}}

var disableModelInvocationMetaKeys = map[string]struct{}{"disable-model-invocation": {}, "disable_model_invocation": {}, "disablemodelinvocation": {}}

var replacesNativeMetaKeys = []string{
	"replaces_native",
	"replaces-native",
	"replacesnative",
}

func applyMetaLine(info *SkillInfo, key, value string, tail []string) int {
	if handler, ok := metaScalarAppliers[key]; ok {
		handler(info, value)
		return 0
	}
	if used, ok := applyMetaWordList(info, key, value, tail); ok {
		return used
	}
	return 0
}

func applyMetaWordList(info *SkillInfo, key, value string, tail []string) (int, bool) {
	words, used := parseWordList(value, tail)
	switch {
	case metaKeyMatch(key, triggerWordMetaKeys):
		info.TriggerWords = append(info.TriggerWords, words...)
	case metaKeyMatch(key, forceWordMetaKeys):
		info.ForceWords = append(info.ForceWords, words...)
	case metaKeyMatch(key, allowedToolsMetaKeys):
		info.AllowedTools = append(info.AllowedTools, words...)
	default:
		return 0, false
	}
	return used, true
}

type yamlSecurityMetadata struct {
	key   *yaml.Node
	value *yaml.Node
}

type skillFrontmatterYAML struct {
	root     *yaml.Node
	security map[string]yamlSecurityMetadata
}

// parseSkillFrontmatterYAML 解析并校验唯一的 YAML 映射根节点，保留节点语义供 parser 与 mirror 共用。
func parseSkillFrontmatterYAML(frontmatter string) (skillFrontmatterYAML, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &document); err != nil {
		return skillFrontmatterYAML{}, fmt.Errorf("parse skill frontmatter YAML: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return skillFrontmatterYAML{}, fmt.Errorf("parse skill frontmatter YAML: root must be a mapping")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return skillFrontmatterYAML{}, fmt.Errorf("parse skill frontmatter YAML: root must be a mapping")
	}
	if yamlNodeHasMerge(root) {
		return skillFrontmatterYAML{}, fmt.Errorf("parse skill frontmatter YAML: merge keys are not allowed")
	}
	if yamlNodeUsesAlias(root) {
		return skillFrontmatterYAML{}, fmt.Errorf("parse skill frontmatter YAML: aliases are not allowed")
	}
	security, err := indexYAMLSecurityMetadata(root)
	if err != nil {
		return skillFrontmatterYAML{}, fmt.Errorf("parse skill frontmatter YAML: %w", err)
	}
	return skillFrontmatterYAML{root: root, security: security}, nil
}

// yamlNodeHasMerge 检查 YAML 树中是否存在 merge key，阻止字段通过合并语义注入根节点。
func yamlNodeHasMerge(node *yaml.Node) bool {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Tag == "!!merge" || key.Value == "<<" {
				return true
			}
		}
	}
	return slices.ContainsFunc(node.Content, yamlNodeHasMerge)
}

// yamlNodeUsesAlias 检查 YAML 树中的 anchor 与 alias，避免安全字段通过间接节点表达。
func yamlNodeUsesAlias(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return true
	}
	return slices.ContainsFunc(node.Content, yamlNodeUsesAlias)
}

// indexYAMLSecurityMetadata 建立安全字段索引，并拒绝重复键、别名重复和非标量安全值。
func indexYAMLSecurityMetadata(root *yaml.Node) (map[string]yamlSecurityMetadata, error) {
	if len(root.Content)%2 != 0 {
		return nil, fmt.Errorf("mapping contains an unmatched key")
	}
	seenKeys := make(map[string]struct{}, len(root.Content)/2)
	security := make(map[string]yamlSecurityMetadata, 2)
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("metadata key must be a string scalar")
		}
		if _, exists := seenKeys[key.Value]; exists {
			return nil, fmt.Errorf("duplicate metadata key %q", key.Value)
		}
		seenKeys[key.Value] = struct{}{}
		field := yamlSecurityFieldName(key.Value)
		if field == "" {
			continue
		}
		if previous, exists := security[field]; exists {
			return nil, fmt.Errorf(
				"%s declared through multiple aliases: %q and %q",
				field,
				previous.key.Value,
				key.Value,
			)
		}
		if value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
			return nil, invalidYAMLSecurityScalarError(field)
		}
		security[field] = yamlSecurityMetadata{key: key, value: value}
	}
	return security, nil
}

func invalidYAMLSecurityScalarError(field string) error {
	if field == "trust" {
		return fmt.Errorf("trust must be user, project, or signed")
	}
	return fmt.Errorf("disable_model_invocation must be a boolean")
}

func yamlSecurityFieldName(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch {
	case metaKeyMatch(normalized, trustMetaKeys):
		return "trust"
	case metaKeyMatch(normalized, disableModelInvocationMetaKeys):
		return "disable_model_invocation"
	default:
		return ""
	}
}

// applyYAMLFrontmatter 将严格 YAML 节点中的安全字段和普通结构化字段写入技能信息。
func applyYAMLFrontmatter(info *SkillInfo, frontmatter string) error {
	doc, err := parseSkillFrontmatterYAML(frontmatter)
	if err != nil {
		return err
	}
	if err := applyYAMLSecurityMetadata(info, doc); err != nil {
		return err
	}
	var decoded map[string]any
	if err := doc.root.Decode(&decoded); err != nil {
		return fmt.Errorf("decode skill frontmatter YAML: %w", err)
	}
	for _, key := range replacesNativeMetaKeys {
		if raw, ok := decoded[key]; ok {
			info.ReplacesNative = parseReplacesNativeYAML(raw)
			break
		}
	}
	return nil
}

func applyYAMLSecurityMetadata(info *SkillInfo, doc skillFrontmatterYAML) error {
	if err := applyYAMLTrust(info, doc.security["trust"]); err != nil {
		return err
	}
	return applyYAMLDisableModelInvocation(info, doc.security["disable_model_invocation"])
}

func applyYAMLTrust(info *SkillInfo, metadata yamlSecurityMetadata) error {
	if metadata.value == nil {
		return nil
	}
	if metadata.value.Tag != "!!str" {
		return fmt.Errorf("trust must be user, project, or signed: %s", metadata.value.Value)
	}
	value := strings.TrimSpace(metadata.value.Value)
	scope := parseTrustScope(value)
	if !scope.Valid() {
		return fmt.Errorf("trust must be user, project, or signed: %q", value)
	}
	info.Trust = scope
	return nil
}

func applyYAMLDisableModelInvocation(info *SkillInfo, metadata yamlSecurityMetadata) error {
	if metadata.value == nil {
		return nil
	}
	var raw any
	if err := metadata.value.Decode(&raw); err != nil {
		return fmt.Errorf("decode disable_model_invocation: %w", err)
	}
	disabled, err := parseYAMLBoolScalar(raw)
	if err != nil {
		return err
	}
	info.DisableModelInvocation = disabled
	return nil
}

func marshalSkillFrontmatterYAML(doc skillFrontmatterYAML) (string, error) {
	encoded, err := yaml.Marshal(doc.root)
	if err != nil {
		return "", fmt.Errorf("marshal skill frontmatter YAML: %w", err)
	}
	return strings.TrimSuffix(string(encoded), "\n"), nil
}

func rewriteYAMLScalar(node *yaml.Node, value string) {
	node.Value = value
	node.Tag = "!!str"
	node.Style = 0
	node.Anchor = ""
	node.HeadComment = ""
	node.LineComment = ""
	node.FootComment = ""
}

// parseYAMLBoolScalar 解析兼容的布尔标量；未知或空值必须失败，禁止静默降级。
func parseYAMLBoolScalar(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case int:
		switch typed {
		case 1:
			return true, nil
		case 0:
			return false, nil
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "yes", "y", "on", "1":
			return true, nil
		case "false", "no", "n", "off", "0":
			return false, nil
		}
	}
	return false, fmt.Errorf("disable_model_invocation must be a boolean: %v", value)
}

// parseReplacesNativeYAML 解析 replaces_native YAML 字段为 provider 到工具名列表的映射。
func parseReplacesNativeYAML(raw any) map[string][]string {
	switch value := raw.(type) {
	case map[string]any:
		out := make(map[string][]string, len(value))
		for provider, tools := range value {
			if names := parseYAMLStringList(tools); len(names) > 0 {
				out[normalizeReplacesNativeProvider(provider)] = names
			}
		}
		return out
	case []any, []string, string:
		if names := parseYAMLStringList(value); len(names) > 0 {
			return map[string][]string{"*": names}
		}
	}
	return nil
}

func parseYAMLStringList(raw any) []string {
	switch value := raw.(type) {
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, parseScalar(fmt.Sprint(item)))
		}
		return uniqStrings(out)
	case []string:
		return uniqStrings(value)
	case string:
		return splitWords(value)
	default:
		return nil
	}
}

func normalizeReplacesNativeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "*"
	}
	return provider
}

func normalizeReplacesNative(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for provider, tools := range in {
		if names := uniqStrings(tools); len(names) > 0 {
			out[normalizeReplacesNativeProvider(provider)] = names
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func metaKeyMatch(key string, aliases map[string]struct{}) bool {
	_, ok := aliases[key]
	return ok
}

// parseBoolScalar 识别常见的布尔表示法：true/yes/on/1 为 true，其余（包括空）为 false。不区分大小写。
func parseBoolScalar(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "y", "on", "1":
		return true
	}
	return false
}

func parseWordList(value string, tail []string) ([]string, int) {
	if value = strings.TrimSpace(value); value != "" {
		return splitWords(value), 0
	}
	words := make([]string, 0, 4)
	used := 0
	for _, line := range tail {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		words = append(words, parseScalar(strings.TrimPrefix(trimmed, "- ")))
		used++
	}
	return uniqStrings(words), used
}

func splitWords(value string) []string {
	value = strings.Trim(value, "[]")
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if word := parseScalar(part); word != "" {
			words = append(words, word)
		}
	}
	return uniqStrings(words)
}

func parseScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

// summarizeSkillBody 从正文中提取可展示摘要。
// 内部 marker、标题和代码围栏会被跳过，避免把系统标记暴露到 UI。
func summarizeSkillBody(body, description string) string {
	if description = strings.TrimSpace(description); description != "" {
		return description
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	skipInternalBlock := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if skipInternalBlock != "" {
			if line == "</"+skipInternalBlock+">" {
				skipInternalBlock = ""
			}
			continue
		}
		if name, ok := internalSkillMarkerOpenName(line); ok {
			skipInternalBlock = name
			continue
		}
		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, "```"), isInternalSkillMarkerSummary(line):
			continue
		default:
			return line
		}
	}
	return ""
}

func isInternalSkillMarkerSummary(value string) bool {
	return internalSkillMarkerSummaryPattern.MatchString(strings.TrimSpace(value))
}

// internalSkillMarkerOpenName 识别正文中的内部 marker 起始标签。
// 只接受无空白和路径分隔符的标签名，避免把普通 HTML 或路径片段当成内部块。
func internalSkillMarkerOpenName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") || strings.HasPrefix(value, "</") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">")
	if name == "" || strings.ContainsAny(name, " \t/") {
		return "", false
	}
	if !isInternalSkillMarkerSummary(value) {
		return "", false
	}
	return name, true
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func uniqStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func skillSlug(name string) string { return skillidentity.Slug(name) }
