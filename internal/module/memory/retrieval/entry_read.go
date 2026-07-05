package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
)

const manifestHeaderScanLimit = 32 * 1024

// readMemoryEntryHeader 只读取记忆文件 frontmatter 头部用于 manifest。
// 它不加载完整正文，降低大记忆目录扫描成本；头部读取仍记录文件 mtime 和 canonical name。
func readMemoryEntryHeader(path string) (MemoryEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	defer file.Close()

	header, err := parse.ScanFrontmatterHeader(file, manifestHeaderScanLimit)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return MemoryEntry{}, err
	}
	entry := parseMemoryHeader(path, header)
	entry.FilePath = path
	entry.UpdatedAt = info.ModTime()
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return normalizeLoadedEntry(entry), nil
}

// readMemoryEntryFile 读取完整记忆文件并清理正文。
// 该路径用于最终注入前的 hydrate，必须处理 BOM、frontmatter 和 HTML 注释。
func readMemoryEntryFile(path string) (MemoryEntry, error) {
	rawContent, err := os.ReadFile(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	content := parse.StripUTF8BOM(string(rawContent))
	entry := MemoryEntry{Content: content, FilePath: path, UpdatedAt: info.ModTime()}
	if frontmatter, body, ok := parse.SplitFrontmatter(content); ok {
		entry.Frontmatter = parseMemoryFrontmatter(frontmatter)
		entry.Content = body
	}
	entry.Content = parse.StripHTMLComments(entry.Content)
	entry = normalizeLoadedEntry(entry)
	if entry.Frontmatter.Name == "" {
		entry.Frontmatter.Name = fallbackEntryName(path)
	}
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
}

// parseMemoryHeader 从头部候选文本解析 manifest 条目。
// 没有完整 frontmatter 时仍返回带路径的空条目，后续排序和 hydrate 可继续处理。
func parseMemoryHeader(path, header string) MemoryEntry {
	frontmatter, _, ok := parse.SplitFrontmatter(header)
	if !ok {
		return MemoryEntry{FilePath: path}
	}
	return MemoryEntry{Frontmatter: parseMemoryFrontmatter(frontmatter), FilePath: path}
}

// parseMemoryFrontmatter 解析检索所需的 frontmatter 字段。
// 未知字段会忽略，避免旧文件扩展影响相关记忆扫描。
func parseMemoryFrontmatter(frontmatter string) MemoryFrontmatter {
	parsed := MemoryFrontmatter{}
	for line := range strings.SplitSeq(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "name":
			parsed.Name = parseScalar(value)
		case "description":
			parsed.Description = parseScalar(value)
		case "type":
			t := ParseMemoryType(parseScalar(value))
			parsed.Type = cloneMemoryType(t)
		case "lang":
			parsed.Lang = parseScalar(value)
		case "aliases":
			parsed.Aliases = parseStringList(value)
		case "search_keys":
			parsed.SearchKeys = parseStringList(value)
		case "title", "source":
			assignMemoryFrontmatterScalar(&parsed, key, value)
		}
	}
	return parsed
}

// assignMemoryFrontmatterScalar 写入检索支持的可选标量字段。
// title/source 与主 memory 包保持一致，方便 hydrate 后继续携带来源信息。
func assignMemoryFrontmatterScalar(parsed *MemoryFrontmatter, key, value string) {
	value = parseScalar(value)
	switch key {
	case "title":
		parsed.Title = value
	case "source":
		parsed.Source = value
	}
}

// normalizeLoadedEntry 规整从磁盘读取的检索条目。
// 字段清理和类型解析与父包一致，保证同一文件在 manifest 和 hydrate 阶段表现相同。
func normalizeLoadedEntry(entry MemoryEntry) MemoryEntry {
	entry.Frontmatter.Name = strings.Join(strings.Fields(entry.Frontmatter.Name), " ")
	entry.Frontmatter.Description = strings.Join(strings.Fields(entry.Frontmatter.Description), " ")
	entry.Frontmatter.Lang = strings.TrimSpace(entry.Frontmatter.Lang)
	entry.Frontmatter.Aliases = normalizeStringSlice(entry.Frontmatter.Aliases)
	entry.Frontmatter.SearchKeys = normalizeStringSlice(entry.Frontmatter.SearchKeys)
	if entry.Frontmatter.Type != nil {
		entry.Frontmatter.Type = cloneMemoryType(ParseMemoryType(string(*entry.Frontmatter.Type)))
	}
	entry.Content = strings.TrimSpace(entry.Content)
	return entry
}

// parseScalar 解析 frontmatter 标量值。
// 优先按 JSON 字符串解码，失败时兼容裸字符串和简单引号。
func parseScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.Trim(strings.TrimSpace(raw), "\"'")
}

// parseStringList 解析 aliases/search_keys 列表。
// 支持 JSON 数组和逗号分隔写法，最终统一去重和清洗。
func parseStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return normalizeStringSlice(decoded)
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := parseScalar(part); value != "" {
			values = append(values, value)
		}
	}
	return normalizeStringSlice(values)
}

// fallbackEntryName 从文件名生成缺省记忆名称。
// 旧文件缺少 name 时用它保持 manifest 标题非空。
func fallbackEntryName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.Join(strings.Fields(base), " ")
}
