package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"golang.org/x/text/unicode/norm"
)

const (
	memoryHookMaxRunes        = 150
	manifestHeaderScanLimit   = 32 * 1024
	uiMemoryScanMaxEntries    = 200
	uiMemoryScanMaxEntryBytes = 256 * 1024
	uiMemoryScanMaxTotalBytes = 2 * 1024 * 1024
)

const (
	uiMemoryScanReasonTruncated = "memory_scan_truncated"
	uiMemoryScanReasonCanceled  = "memory_scan_canceled"
)

var errUIMemoryScanStopped = errors.New("ui memory scan stopped")

type MemoryIndexEntry struct {
	Title         string
	Path          string
	Hook          string
	CanonicalName string
}

// ParseMemoryIndex 解析 MEMORY.md 指针索引。
// 输入会先清理 BOM 和 CRLF；任一非空行格式不符合索引语法时立即返回错误，
// 避免损坏索引被部分接受。
func ParseMemoryIndex(content string) ([]MemoryIndexEntry, error) {
	content = parse.StripUTF8BOM(content)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	entries := make([]MemoryIndexEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry, err := parseIndexLine(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ReadMemoryIndex 从磁盘读取并解析 MEMORY.md。
// 读取路径必须先通过根目录包含校验，防止调用方传入越界路径读取任意文件。
func ReadMemoryIndex(path string) ([]MemoryIndexEntry, error) {
	validatedPath, err := ValidateMemoryReadPath(filepath.Dir(path), path)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(validatedPath)
	if err != nil {
		return nil, err
	}
	return ParseMemoryIndex(string(content))
}

// WriteMemoryIndex 根据记忆条目重写根目录下的 MEMORY.md。
// 写入使用原子替换，索引内容只保存指针和短 hook，不内联完整记忆正文。
func WriteMemoryIndex(root string, entries []MemoryEntry) error {
	indexEntries, err := buildMemoryIndex(root, entries)
	if err != nil {
		return err
	}
	return writeAtomicFile(memoryIndexPath(root), []byte(formatMemoryIndex(indexEntries)), 0o644)
}

// UpdateMemoryIndex 重新扫描根目录并刷新 MEMORY.md。
// 扫描失败或索引写入失败都会返回错误，调用方不能继续假定索引已更新。
func UpdateMemoryIndex(root string) ([]MemoryIndexEntry, error) {
	entries, err := scanMemoryEntries(root)
	if err != nil {
		return nil, err
	}
	if err := WriteMemoryIndex(root, entries); err != nil {
		return nil, err
	}
	return buildMemoryIndex(root, entries)
}

// RebuildMemoryIndex 是对 UpdateMemoryIndex 的兼容入口。
// 保留该名称供外部恢复流程调用，实际重建语义统一由 UpdateMemoryIndex 维护。
func RebuildMemoryIndex(root string) ([]MemoryIndexEntry, error) {
	return UpdateMemoryIndex(root)
}

// scanMemoryEntries 扫描根目录下可进入索引的记忆条目。
// 不存在的根目录返回空列表；遍历过程中遇到非法路径或解析错误会立即失败。
func scanMemoryEntries(root string) ([]MemoryEntry, error) {
	return scanMemoryEntriesWithBudget(context.Background(), root, nil)
}

// scanMemoryEntriesWithBudget 为 UI 首页扫描提供可取消、可截断的扫描入口。
// 预算触顶不是存储错误，调用方通过 budget.metadata() 向前端展示降级状态。
func scanMemoryEntriesWithBudget(ctx context.Context, root string, budget *uiMemoryScanBudget) ([]MemoryEntry, error) {
	exists, err := memoryEntriesRootExists(root)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	entries := make([]MemoryEntry, 0, 16)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		entry, skipDir, ok, err := scannedMemoryEntryWithBudget(ctx, budget, root, path, d, walkErr)
		if err != nil {
			return err
		}
		if skipDir {
			return filepath.SkipDir
		}
		if ok {
			entries = append(entries, entry)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errUIMemoryScanStopped) {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

// memoryEntriesRootExists 判断记忆根目录是否存在。
// 不存在不是错误，调用方可据此生成空索引；其它 stat 错误需要上抛。
func memoryEntriesRootExists(root string) (bool, error) {
	if _, err := os.Stat(root); errorsIsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// scannedMemoryEntry 处理 WalkDir 扫描到的单个路径。
// 它会跳过索引文件和整理日志目录，并对候选 markdown 再做读路径校验。
func scannedMemoryEntryWithBudget(ctx context.Context, budget *uiMemoryScanBudget, root, path string, d fs.DirEntry, walkErr error) (MemoryEntry, bool, bool, error) {
	if err := uiMemoryScanStopped(ctx, budget); err != nil {
		return MemoryEntry{}, false, false, err
	}
	if walkErr != nil {
		return MemoryEntry{}, false, false, walkErr
	}
	if d.IsDir() {
		return MemoryEntry{}, isConsolidationLogPath(root, path), false, nil
	}
	if shouldSkipScannedMemoryPath(root, path) {
		return MemoryEntry{}, false, false, nil
	}
	if _, err := ValidateMemoryReadPath(root, path); err != nil {
		return MemoryEntry{}, false, false, err
	}
	if !reserveUIMemoryScanFile(budget, d) {
		return MemoryEntry{}, false, false, errUIMemoryScanStopped
	}
	entry, err := readMemoryEntryFile(path)
	if err != nil {
		return MemoryEntry{}, false, false, err
	}
	if budget != nil {
		budget.recordEntry()
	}
	return entry, false, true, nil
}

type uiMemoryScanBudget struct {
	ctx         context.Context
	entryLimit  int
	singleLimit int64
	totalLimit  int64
	entries     int
	filesRead   int
	bytesRead   int64
	truncated   bool
	canceled    bool
	reason      string
}

func newUIMemoryScanBudget(ctx context.Context) *uiMemoryScanBudget {
	if ctx == nil {
		ctx = context.Background()
	}
	return &uiMemoryScanBudget{
		ctx:         ctx,
		entryLimit:  uiMemoryScanMaxEntries,
		singleLimit: uiMemoryScanMaxEntryBytes,
		totalLimit:  uiMemoryScanMaxTotalBytes,
	}
}

func newConsolidationMemoryScanBudget(ctx context.Context, cfg *Config) *uiMemoryScanBudget {
	if ctx == nil {
		ctx = context.Background()
	}
	return &uiMemoryScanBudget{
		ctx:         ctx,
		entryLimit:  maxConsolidationFiles(cfg),
		singleLimit: maxConsolidationFileBytes(cfg),
		totalLimit:  maxConsolidationTotalBytes(cfg),
	}
}

func uiMemoryScanStopped(ctx context.Context, budget *uiMemoryScanBudget) error {
	if budget == nil {
		return nil
	}
	if ctx == nil {
		ctx = budget.ctx
	}
	if err := ctx.Err(); err != nil {
		budget.stop(uiMemoryScanReasonCanceled)
		return errUIMemoryScanStopped
	}
	if budget.isStopped() {
		return errUIMemoryScanStopped
	}
	return nil
}

func reserveUIMemoryScanFile(budget *uiMemoryScanBudget, d fs.DirEntry) bool {
	if budget == nil {
		return true
	}
	info, err := d.Info()
	if err != nil {
		budget.stop(uiMemoryScanReasonTruncated)
		return false
	}
	return budget.reserveFile(info.Size())
}

func (b *uiMemoryScanBudget) reserveFile(size int64) bool {
	if b == nil {
		return true
	}
	if b.entries >= b.entryLimit || size > b.singleLimit || b.bytesRead+size > b.totalLimit {
		b.stop(uiMemoryScanReasonTruncated)
		return false
	}
	b.filesRead++
	b.bytesRead += size
	return true
}

func (b *uiMemoryScanBudget) recordEntry() {
	if b != nil {
		b.entries++
	}
}

func (b *uiMemoryScanBudget) stop(reason string) {
	if b == nil || b.reason != "" {
		return
	}
	b.reason = strings.TrimSpace(reason)
	b.truncated = b.reason == uiMemoryScanReasonTruncated
	b.canceled = b.reason == uiMemoryScanReasonCanceled
}

func (b *uiMemoryScanBudget) isStopped() bool {
	return b != nil && b.reason != ""
}

func (b *uiMemoryScanBudget) metadata() UIMemoryScanMetadata {
	if b == nil {
		return UIMemoryScanMetadata{}
	}
	return UIMemoryScanMetadata{
		Truncated:            b.truncated,
		Canceled:             b.canceled,
		Reason:               b.reason,
		Entries:              b.entries,
		FilesRead:            b.filesRead,
		BytesRead:            b.bytesRead,
		EntryLimit:           b.entryLimit,
		SingleFileBytesLimit: b.singleLimit,
		TotalBytesLimit:      b.totalLimit,
	}
}

func (b *uiMemoryScanBudget) notice() string {
	if b == nil {
		return ""
	}
	switch b.reason {
	case uiMemoryScanReasonTruncated:
		return "记忆扫描已达到安全上限，列表仅显示部分条目。"
	case uiMemoryScanReasonCanceled:
		return "记忆扫描已取消。"
	default:
		return ""
	}
}

// shouldSkipScannedMemoryPath 判断扫描路径是否不应进入记忆索引。
// MEMORY.md 和 consolidation 日志不是用户记忆正文，必须从索引重建中排除。
func shouldSkipScannedMemoryPath(root, path string) bool {
	return filepath.Ext(path) != ".md" ||
		filepath.Base(path) == memoryIndexFileName ||
		isConsolidationLogPath(root, path)
}

// readMemoryEntryFile 读取单个记忆文件并补齐运行时索引字段。
// frontmatter 缺 name 时使用文件名兜底，但 canonical name 始终按最终名称生成。
func readMemoryEntryFile(path string) (MemoryEntry, error) {
	parsed, err := ParseMemoryFile(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	entry := MemoryEntry{Content: parsed.Content, FilePath: path, UpdatedAt: info.ModTime()}
	entry.Frontmatter = parsed.Frontmatter
	entry = normalizeLoadedEntry(entry)
	if entry.Frontmatter.Name == "" {
		entry.Frontmatter.Name = fallbackEntryName(path)
	}
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
}

// buildMemoryIndex 将去重后的记忆条目转换为 MEMORY.md 指针行。
// 同名条目只保留最新版本，避免索引中出现多个 canonical name 相同的入口。
func buildMemoryIndex(root string, entries []MemoryEntry) ([]MemoryIndexEntry, error) {
	uniqueEntries := uniqueEntriesByCanonicalName(entries)
	indexEntries := make([]MemoryIndexEntry, 0, len(uniqueEntries))
	for _, entry := range uniqueEntries {
		rel, err := filepath.Rel(root, entry.FilePath)
		if err != nil {
			return nil, err
		}
		indexEntries = append(indexEntries, MemoryIndexEntry{
			Title:         strings.TrimSpace(entry.Frontmatter.Name),
			Path:          filepath.ToSlash(rel),
			Hook:          hookFromEntry(entry),
			CanonicalName: entry.CanonicalName,
		})
	}
	return indexEntries, nil
}

// formatMemoryIndex 把索引条目渲染为 MEMORY.md 文本。
// 空索引返回空字符串；非空索引以换行结尾，保持文件写入稳定。
func formatMemoryIndex(entries []MemoryIndexEntry) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		line := fmt.Sprintf("- [%s](%s)", entry.Title, entry.Path)
		if hook := strings.TrimSpace(entry.Hook); hook != "" {
			line += " — " + hook
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// formatMemoryEntry 将结构化记忆条目序列化为 markdown 文件。
// frontmatter 字段使用 JSON 字符串编码，避免标题、描述或别名中的特殊字符破坏解析。
func formatMemoryEntry(entry MemoryEntry) string {
	frontmatter := entry.Frontmatter
	lines := []string{
		"---",
		"name: " + strconv.Quote(frontmatter.Name),
		"description: " + strconv.Quote(frontmatter.Description),
		"type: " + strconv.Quote(string(entry.Type())),
	}
	if frontmatter.Lang != "" {
		lines = append(lines, "lang: "+strconv.Quote(frontmatter.Lang))
	}
	if len(frontmatter.Aliases) > 0 {
		lines = append(lines, "aliases: "+formatStringList(frontmatter.Aliases))
	}
	if len(frontmatter.SearchKeys) > 0 {
		lines = append(lines, "search_keys: "+formatStringList(frontmatter.SearchKeys))
	}
	if frontmatter.Title != "" {
		lines = append(lines, "title: "+strconv.Quote(frontmatter.Title))
	}
	if frontmatter.Source != "" {
		lines = append(lines, "source: "+strconv.Quote(frontmatter.Source))
	}
	lines = append(lines, "---", "", strings.TrimSpace(entry.Content), "")
	return strings.Join(lines, "\n")
}

// parseIndexLine 解析 MEMORY.md 中的一条指针行。
// 格式必须是 markdown link，可选 hook 只在右括号后用破折号承载。
func parseIndexLine(line string) (MemoryIndexEntry, error) {
	if !strings.HasPrefix(line, "- [") {
		return MemoryIndexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	rest := strings.TrimPrefix(line, "- [")
	title, tail, ok := strings.Cut(rest, "](")
	if !ok {
		return MemoryIndexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	path, hook, ok := strings.Cut(tail, ")")
	if !ok {
		return MemoryIndexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	entry := MemoryIndexEntry{Title: strings.TrimSpace(title), Path: strings.TrimSpace(path)}
	hook = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(hook), "—"))
	entry.Hook = hook
	entry.CanonicalName = CanonicalName(entry.Title)
	return entry, nil
}

// parseMemoryFrontmatter 解析记忆文件 frontmatter。
// 仅识别当前 schema 支持的字段；未知字段会被忽略，避免旧数据扩展阻断读取。
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
			memoryType := ParseMemoryType(parseScalar(value))
			parsed.Type = cloneMemoryType(memoryType)
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

// assignMemoryFrontmatterScalar 写入 title/source 这类标量扩展字段。
// 该函数集中处理可选字段，防止主解析 switch 继续膨胀。
func assignMemoryFrontmatterScalar(parsed *MemoryFrontmatter, key, value string) {
	value = parseScalar(value)
	switch key {
	case "title":
		parsed.Title = value
	case "source":
		parsed.Source = value
	}
}

// normalizeLoadedEntry 规整从磁盘加载的记忆条目。
// 它会裁剪正文和字段空白，并把类型重新解析到已知枚举，避免脏 frontmatter 泄漏到索引。
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
// 优先按 JSON 字符串解码，失败时兼容裸字符串和简单引号包裹写法。
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

// parseStringList 解析 frontmatter 中的字符串列表。
// 兼容 JSON 数组和逗号分隔写法，最终通过 normalizeStringSlice 去重和清洗。
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

// hookFromEntry 生成 MEMORY.md 索引中的短 hook。
// 优先使用 description，缺失时取正文首个非空行，并按固定 rune 预算截断。
func hookFromEntry(entry MemoryEntry) string {
	hook := strings.TrimSpace(entry.Frontmatter.Description)
	if hook == "" {
		hook = firstNonEmptyLine(entry.Content)
	}
	hook = strings.Join(strings.Fields(hook), " ")
	return truncateRunes(hook, memoryHookMaxRunes)
}

// formatStringList 将字符串列表编码为 frontmatter 可读的 JSON 数组。
// json.Marshal 对 []string 不应失败，失败时沿用空字符串语义。
func formatStringList(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// truncateRunes 按 rune 数截断文本。
// 该函数用于 prompt hook 等展示字段，不按字节截断以避免切碎多字节字符。
func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}

// firstNonEmptyLine 返回文本的第一条非空行。
// 自动整理生成 description 时用它提取最短可读摘要。
func firstNonEmptyLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// fallbackEntryName 从文件名生成缺省记忆名称。
// 仅在旧文件缺少 name frontmatter 时使用，避免索引中出现空标题。
func fallbackEntryName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.Join(strings.Fields(base), " ")
}

// uniqueEntriesByCanonicalName 按 canonical name 去重记忆条目。
// 同名冲突时保留更新时间最新的文件，时间相同则用路径排序保证结果稳定。
func uniqueEntriesByCanonicalName(entries []MemoryEntry) []MemoryEntry {
	if len(entries) == 0 {
		return nil
	}
	selected := make(map[string]MemoryEntry, len(entries))
	for _, entry := range entries {
		key := entry.CanonicalName
		if key == "" {
			key = CanonicalName(entry.Frontmatter.Name)
		}
		current, exists := selected[key]
		if !exists || preferMemoryEntry(entry, current) {
			selected[key] = entry
		}
	}
	uniqueEntries := make([]MemoryEntry, 0, len(selected))
	for _, entry := range selected {
		uniqueEntries = append(uniqueEntries, entry)
	}
	sortEntries(uniqueEntries)
	return uniqueEntries
}

// preferMemoryEntry 判断候选条目是否应替换当前同名条目。
// 更新时间优先，路径作为稳定 tie-breaker，保证重建索引可重复。
func preferMemoryEntry(candidate, current MemoryEntry) bool {
	if candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.FilePath < current.FilePath
	}
	return candidate.UpdatedAt.After(current.UpdatedAt)
}

// sortEntries 按 canonical name 和文件路径稳定排序记忆条目。
// 索引写入依赖该顺序，减少无意义 diff。
func sortEntries(entries []MemoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i].CanonicalName
		right := entries[j].CanonicalName
		if left == right {
			return entries[i].FilePath < entries[j].FilePath
		}
		return left < right
	})
}

// errorsIsNotExist 判断错误是否为 os.IsNotExist。
// nil 错误返回 false，便于 stat 分支直接调用。
func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

const (
	// memoryFileBaseMaxBytes 是记忆文件名主体的字节预算。
	// 截断必须保持 UTF-8 完整；macOS 会拒绝切断多字节字符的路径。
	memoryFileBaseMaxBytes = 96
)

// memoryFileBase 为记忆名称生成稳定文件名主体。
// 名称没有可 slug 化字符时退回短 hash，避免生成空文件名。
func memoryFileBase(name string) string {
	if !hasSlugRune(name) {
		return "mem-" + shared.ShortHash(CanonicalName(name))
	}
	return memoryFileSlug(name)
}

// memoryFileSlug 将记忆名称转换为可读、稳定且有字节上限的 slug。
// 超限时保留前缀并追加短 hash，避免不同长名称截断后碰撞。
func memoryFileSlug(raw string) string {
	normalized := norm.NFC.String(strings.TrimSpace(raw))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case lastDash:
		default:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "mem-" + shared.ShortHash(CanonicalName(raw))
	}
	if len(slug) <= memoryFileBaseMaxBytes {
		return slug
	}
	prefix := strings.Trim(truncateUTF8Bytes(slug, memoryFileBaseMaxBytes-9), "-")
	if prefix == "" {
		prefix = "mem"
	}
	return prefix + "-" + shared.ShortHash(normalized)
}

// truncateUTF8Bytes 按字节预算截断字符串且不切断 UTF-8 rune。
// 文件名预算以字节计，但路径必须保持有效 UTF-8。
func truncateUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 || strings.TrimSpace(text) == "" {
		return ""
	}
	if len(text) <= maxBytes && utf8.ValidString(text) {
		return text
	}
	var builder strings.Builder
	for _, r := range text {
		runeLen := utf8.RuneLen(r)
		if runeLen < 0 {
			runeLen = len(string(r))
		}
		if builder.Len()+runeLen > maxBytes {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// hasSlugRune 判断文本中是否存在可用于 slug 的字母或数字。
// 没有可用字符时文件名必须走 hash 兜底。
func hasSlugRune(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
