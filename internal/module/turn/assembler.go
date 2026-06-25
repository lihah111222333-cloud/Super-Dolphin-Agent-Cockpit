package turn

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

const (
	maxTurnInputItems     = 256
	maxTurnInputTextBytes = 64 * 1024
	maxTurnInputPathBytes = 4096
)

var (
	allowedTurnInputFileExts = map[string]struct{}{
		"": {}, ".txt": {}, ".md": {}, ".markdown": {}, ".json": {}, ".yaml": {}, ".yml": {},
		".xml": {}, ".toml": {}, ".ini": {}, ".cfg": {}, ".conf": {}, ".log": {}, ".csv": {},
		".tsv": {}, ".sql": {}, ".sh": {}, ".bash": {}, ".zsh": {}, ".fish": {}, ".env": {},
		".go": {}, ".py": {}, ".js": {}, ".ts": {}, ".jsx": {}, ".tsx": {}, ".java": {}, ".kt": {},
		".rb": {}, ".rs": {}, ".c": {}, ".cc": {}, ".cpp": {}, ".h": {}, ".hpp": {}, ".m": {}, ".mm": {},
		".swift": {}, ".php": {}, ".html": {}, ".css": {}, ".scss": {}, ".less": {}, ".vue": {},
		".svelte": {}, ".proto": {}, ".graphql": {}, ".gql": {}, ".dockerfile": {},
	}
	allowedTurnInputImageExts = map[string]struct{}{
		".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".bmp": {}, ".svg": {},
	}
	allowedTurnInputDataImagePrefixes = []string{
		"data:image/png",
		"data:image/jpeg",
		"data:image/jpg",
		"data:image/gif",
		"data:image/webp",
		"data:image/bmp",
		"data:image/svg+xml",
	}
	deniedTurnInputExecutableExts = map[string]struct{}{
		".exe": {}, ".dylib": {}, ".so": {}, ".dll": {}, ".bin": {}, ".apk": {}, ".ipa": {},
		".msi": {}, ".pkg": {}, ".deb": {}, ".rpm": {},
	}
)

// inputAssembler 负责把 PrepareInput 中的 prompt、文件、图片等转换为规范化的 InputItem 列表。
type inputAssembler struct{}

// Assemble 把 PrepareInput 中各类输入项收集、规范化并去重，返回最终提交给 provider 的 InputItem 列表。
func (a *inputAssembler) Assemble(input PrepareInput) []shareddto.InputItem {
	raw := a.collect(input)
	if len(raw) > maxTurnInputItems {
		raw = raw[:maxTurnInputItems]
	}
	items := make([]shareddto.InputItem, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		normalized, ok := a.normalize(item)
		if !ok {
			continue
		}
		key := inputKey(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, normalized)
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

// PromptText 拼接 PrepareInput 中所有文本类输入为单一字符串，用于专家匹配和 assembly 上下文。
func (a *inputAssembler) PromptText(input PrepareInput) string {
	parts := make([]string, 0, len(input.Inputs)+1)
	if prompt := strings.TrimSpace(input.Prompt); prompt != "" {
		parts = append(parts, prompt)
	}
	for _, item := range input.Inputs {
		switch normalizeInputType(item.Type) {
		case "text", "filecontent":
			if text := clampString(strings.TrimSpace(item.Content), maxTurnInputTextBytes); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func (a *inputAssembler) collect(input PrepareInput) []shareddto.InputItem {
	items := make([]shareddto.InputItem, 0, len(input.Inputs)+1+len(input.Images)+len(input.Files))
	if prompt := strings.TrimSpace(input.Prompt); prompt != "" {
		items = append(items, shareddto.InputItem{Type: "text", Content: prompt})
	}
	items = append(items, input.Inputs...)
	for _, raw := range input.Images {
		items = append(items, shareddto.InputItem{Type: "image", Path: raw})
	}
	for _, raw := range input.Files {
		items = append(items, shareddto.InputItem{Type: "mention", Path: raw})
	}
	return items
}

// normalize 按 type 路由到对应规范化函数，返回 (规范化后的 item, 是否有效)。
func (a *inputAssembler) normalize(item shareddto.InputItem) (shareddto.InputItem, bool) {
	switch normalizeInputType(item.Type) {
	case "text":
		return normalizeTextItem(item)
	case "filecontent":
		return normalizeFileContentItem(item)
	case "image":
		return normalizeImageItem(item)
	case "local_image":
		return normalizeLocalImageItem(item)
	case "mention":
		return normalizeMentionItem(item)
	default:
		return normalizeFallbackItem(item)
	}
}

func normalizeTextItem(item shareddto.InputItem) (shareddto.InputItem, bool) {
	content := clampString(strings.TrimSpace(item.Content), maxTurnInputTextBytes)
	if content == "" {
		return shareddto.InputItem{}, false
	}
	return shareddto.InputItem{Type: "text", Content: content}, true
}

func normalizeFileContentItem(item shareddto.InputItem) (shareddto.InputItem, bool) {
	content := clampString(strings.TrimSpace(item.Content), maxTurnInputTextBytes)
	if content == "" {
		return shareddto.InputItem{}, false
	}
	out := shareddto.InputItem{Type: "filecontent", Content: content}
	if name := normalizeInputName(item.Name, item.Path); name != "" {
		out.Name = name
	}
	return out, true
}

// normalizeImageItem 规范化图片输入项，支持 data URI、远程 URL 和本地路径，扩展名不在白名单时丢弃。
func normalizeImageItem(item shareddto.InputItem) (shareddto.InputItem, bool) {
	target := normalizeInputTarget(item.URL, item.Path, item.Content)
	if target == "" {
		return shareddto.InputItem{}, false
	}
	out := shareddto.InputItem{Type: "image"}
	if name := normalizeInputName(item.Name, target); name != "" {
		out.Name = name
	}
	if isDataImage(target) || util.IsRemoteTurnInput(target) {
		out.URL = target
		return out, true
	}
	if !isAllowedExtension(target, allowedTurnInputImageExts) {
		return shareddto.InputItem{}, false
	}
	out.Path = target
	return out, true
}

func normalizeLocalImageItem(item shareddto.InputItem) (shareddto.InputItem, bool) {
	target := normalizeInputTarget(item.Path, item.Content)
	if target == "" || !isAllowedExtension(target, allowedTurnInputImageExts) {
		return shareddto.InputItem{}, false
	}
	out := shareddto.InputItem{Type: "local_image", Path: target}
	if name := normalizeInputName(item.Name, target); name != "" {
		out.Name = name
	}
	return out, true
}

func normalizeMentionItem(item shareddto.InputItem) (shareddto.InputItem, bool) {
	target := normalizeInputTarget(item.Path, item.URL, item.Content)
	if target == "" || !isAllowedFileTarget(target) {
		return shareddto.InputItem{}, false
	}
	out := shareddto.InputItem{Type: "mention", Path: target}
	if name := normalizeInputName(item.Name, target); name != "" {
		out.Name = name
	}
	return out, true
}

func normalizeFallbackItem(item shareddto.InputItem) (shareddto.InputItem, bool) {
	if strings.TrimSpace(item.Content) != "" {
		return normalizeFileContentItem(item)
	}
	return normalizeMentionItem(item)
}

// normalizeInputType 将各种别名统一为标准类型字符串，未知类型保持小写原值。
func normalizeInputType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "text":
		return "text"
	case "image":
		return "image"
	case "localimage", "local_image":
		return "local_image"
	case "file", "mention":
		return "mention"
	case "filecontent":
		return "filecontent"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeInputTarget(values ...string) string {
	for _, value := range values {
		if target := clampString(strings.TrimSpace(value), maxTurnInputPathBytes); target != "" {
			return target
		}
	}
	return ""
}

func normalizeInputName(name string, fallback string) string {
	if name = clampString(strings.TrimSpace(name), maxTurnInputPathBytes); name != "" {
		return name
	}
	base := filepath.Base(strings.TrimSpace(pathWithoutQuery(fallback)))
	switch base {
	case "", ".", "/":
		return ""
	}
	return clampString(base, maxTurnInputPathBytes)
}

func isAllowedFileTarget(target string) bool {
	if isDataImage(target) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(pathWithoutQuery(target)))
	if _, denied := deniedTurnInputExecutableExts[ext]; denied {
		return false
	}
	if util.IsRemoteTurnInput(target) {
		_, ok := allowedTurnInputFileExts[ext]
		return ok
	}
	return isAllowedExtension(target, allowedTurnInputFileExts)
}

func isAllowedExtension(target string, allowed map[string]struct{}) bool {
	ext := strings.ToLower(filepath.Ext(pathWithoutQuery(target)))
	_, ok := allowed[ext]
	return ok
}

func inputKey(item shareddto.InputItem) string {
	return strings.Join([]string{
		normalizeInputType(item.Type),
		strings.TrimSpace(item.Content),
		strings.TrimSpace(item.Path),
		strings.TrimSpace(item.URL),
	}, "|")
}

func clampString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func isDataImage(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range allowedTurnInputDataImagePrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func pathWithoutQuery(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.IndexByte(value, '?'); idx >= 0 {
		return value[:idx]
	}
	return value
}
