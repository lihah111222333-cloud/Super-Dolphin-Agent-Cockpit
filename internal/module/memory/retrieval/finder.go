package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
)

const (
	DefaultRelevantMemoryBudgetBytes = 60 * 1024
	DefaultRelevantMemoryLimit       = 5
	DefaultRelevantMemoryCandidates  = 20
)

// RelevantMemoryFinder 在 manifest 中选择可注入本轮 prompt 的相关记忆。
// 它先按轻量 header 排序，再按预算读取正文，避免把整个 memory 根一次性塞给 provider。
type RelevantMemoryFinder struct {
	BudgetBytes    int
	MaxResults     int
	CandidateLimit int
	readEntry      func(string) (MemoryEntry, error)
}

// NewRelevantMemoryFinder 创建相关记忆查找器。
// 默认预算、结果数和候选数共同限制 prompt 注入规模，readEntry 可在测试中替换。
func NewRelevantMemoryFinder() *RelevantMemoryFinder {
	return &RelevantMemoryFinder{
		BudgetBytes:    DefaultRelevantMemoryBudgetBytes,
		MaxResults:     DefaultRelevantMemoryLimit,
		CandidateLimit: DefaultRelevantMemoryCandidates,
		readEntry:      readMemoryEntryFile,
	}
}

// FindRelevantMemories 在 manifest 中查找与查询相关的记忆。
// 该入口不带 already surfaced 集合，适用于单次检索或后台预取首次运行。
func (f *RelevantMemoryFinder) FindRelevantMemories(ctx context.Context, query string, manifest []MemoryEntry) ([]MemoryEntry, error) {
	return f.FindRelevantMemoriesWithAlreadySurfaced(ctx, query, manifest, nil)
}

// FindRelevantMemoriesWithAlreadySurfaced 查找尚未展示过的相关记忆。
// 查询会先排序候选，再读取完整正文，最后按预算和 surfaced 集合去重选择。
func (f *RelevantMemoryFinder) FindRelevantMemoriesWithAlreadySurfaced(
	ctx context.Context,
	query string,
	manifest []MemoryEntry,
	alreadySurfaced map[string]struct{},
) ([]MemoryEntry, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	ranked := f.rankEntries(query, filterAlreadySurfacedEntries(manifest, alreadySurfaced))
	if len(ranked) == 0 {
		return nil, nil
	}
	hydrated, err := f.hydrateEntries(ctx, ranked)
	if err != nil {
		return nil, err
	}
	return f.SelectRelevantMemoriesWithAlreadySurfaced(hydrated, f.budget(), alreadySurfaced), nil
}

// SelectRelevantMemories 在预算内选择相关记忆。
// 该入口不考虑历史 surfaced 集合，主要用于直接调用和测试。
func (f *RelevantMemoryFinder) SelectRelevantMemories(entries []MemoryEntry, budget int) []MemoryEntry {
	return f.SelectRelevantMemoriesWithAlreadySurfaced(entries, budget, nil)
}

// SelectRelevantMemoriesWithAlreadySurfaced 在预算内选择未展示过的记忆。
// 选择过程同时按路径和内容 hash 去重，避免同一记忆重复占用 prompt。
func (f *RelevantMemoryFinder) SelectRelevantMemoriesWithAlreadySurfaced(
	entries []MemoryEntry,
	budget int,
	alreadySurfaced map[string]struct{},
) []MemoryEntry {
	entries = filterAlreadySurfacedEntries(entries, alreadySurfaced)
	remaining := resolveRelevantBudget(budget)
	limit := f.maxResults()
	if len(entries) == 0 || remaining <= 0 || limit <= 0 {
		return nil
	}

	seenPaths := make(map[string]struct{}, len(entries))
	seenHashes := make(map[string]struct{}, len(entries))
	selected := make([]MemoryEntry, 0, minInt(len(entries), limit))
	for _, entry := range entries {
		if len(selected) >= limit || remaining <= 0 {
			break
		}
		pathKey, hashKey := memoryDedupKeys(entry)
		if isSeen(seenPaths, pathKey) || isSeen(seenHashes, hashKey) {
			continue
		}
		size := memoryBudgetBytes(entry)
		if size > remaining {
			continue
		}
		rememberKey(seenPaths, pathKey)
		rememberKey(seenHashes, hashKey)
		selected = append(selected, cloneMemoryEntry(entry))
		remaining -= size
	}
	return selected
}

// rankEntries 根据查询文本对 manifest 条目打分并截取候选集。
// 这里只使用 header/路径等轻量字段，完整正文会在 hydrateEntries 阶段读取。
func (f *RelevantMemoryFinder) rankEntries(query string, manifest []MemoryEntry) []MemoryEntry {
	normalizedQuery, terms := searchTerms(query)
	if normalizedQuery == "" || len(terms) == 0 || len(manifest) == 0 {
		return nil
	}

	ranked := make([]scoredMemoryEntry, 0, len(manifest))
	for _, entry := range manifest {
		score := scoreMemoryEntry(normalizedQuery, terms, entry)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, scoredMemoryEntry{entry: cloneMemoryEntry(entry), score: score})
	}
	sortScoredMemories(ranked)
	if limit := f.candidateLimit(); len(ranked) > limit {
		ranked = ranked[:limit]
	}
	entries := make([]MemoryEntry, 0, len(ranked))
	for _, item := range ranked {
		entries = append(entries, item.entry)
	}
	return entries
}

func (f *RelevantMemoryFinder) hydrateEntries(ctx context.Context, entries []MemoryEntry) ([]MemoryEntry, error) {
	readEntry := f.entryReader()
	seenPaths := make(map[string]struct{}, len(entries))
	hydrated := make([]MemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		pathKey, _ := memoryDedupKeys(entry)
		if isSeen(seenPaths, pathKey) {
			continue
		}
		rememberKey(seenPaths, pathKey)
		loaded, ok := hydrateMemoryEntrySafe(entry, readEntry)
		if ok {
			hydrated = append(hydrated, loaded)
		}
	}
	return hydrated, nil
}

func hydrateMemoryEntrySafe(entry MemoryEntry, readEntry func(string) (MemoryEntry, error)) (MemoryEntry, bool) {
	if strings.TrimSpace(entry.Content) != "" || strings.TrimSpace(entry.FilePath) == "" {
		return cloneMemoryEntry(entry), true
	}
	loaded, err := readEntry(entry.FilePath)
	if err != nil {
		return MemoryEntry{}, false
	}
	return loaded, true
}

func resolveRelevantBudget(budget int) int {
	if budget > 0 {
		return budget
	}
	return DefaultRelevantMemoryBudgetBytes
}

func memoryBudgetBytes(entry MemoryEntry) int {
	content := strings.TrimSpace(entry.Content)
	if content != "" {
		return len([]byte(content))
	}
	fallback := entry.Frontmatter.Description + entry.Frontmatter.Name + entry.FilePath
	return len([]byte(strings.TrimSpace(fallback)))
}

func memoryDedupKeys(entry MemoryEntry) (string, string) {
	pathKey := CanonicalName(filepath.ToSlash(filepath.Clean(entry.FilePath)))
	content := strings.TrimSpace(entry.Content)
	if content == "" {
		content = strings.TrimSpace(entry.Frontmatter.Name + "\n" + entry.Frontmatter.Description)
	}
	sum := sha256.Sum256([]byte(content))
	return pathKey, hex.EncodeToString(sum[:])
}

func rememberKey(seen map[string]struct{}, key string) {
	if key != "" {
		seen[key] = struct{}{}
	}
}

func isSeen(seen map[string]struct{}, key string) bool {
	if key == "" {
		return false
	}
	_, ok := seen[key]
	return ok
}

func (f *RelevantMemoryFinder) budget() int {
	if f == nil || f.BudgetBytes <= 0 {
		return DefaultRelevantMemoryBudgetBytes
	}
	return f.BudgetBytes
}

func (f *RelevantMemoryFinder) maxResults() int {
	if f == nil || f.MaxResults <= 0 {
		return DefaultRelevantMemoryLimit
	}
	return f.MaxResults
}

func (f *RelevantMemoryFinder) candidateLimit() int {
	if f == nil || f.CandidateLimit <= 0 {
		return DefaultRelevantMemoryCandidates
	}
	return f.CandidateLimit
}

func (f *RelevantMemoryFinder) entryReader() func(string) (MemoryEntry, error) {
	if f != nil && f.readEntry != nil {
		return f.readEntry
	}
	return readMemoryEntryFile
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func cloneEntries(entries []MemoryEntry) []MemoryEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]MemoryEntry, 0, len(entries))
	for _, entry := range entries {
		cloned = append(cloned, cloneMemoryEntry(entry))
	}
	return cloned
}

func cloneMemoryEntry(entry MemoryEntry) MemoryEntry {
	entry.Frontmatter.Aliases = slices.Clone(entry.Frontmatter.Aliases)
	entry.Frontmatter.SearchKeys = slices.Clone(entry.Frontmatter.SearchKeys)
	if entry.Frontmatter.Type != nil {
		entry.Frontmatter.Type = cloneMemoryType(*entry.Frontmatter.Type)
	}
	return entry
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func filterAlreadySurfacedEntries(entries []MemoryEntry, alreadySurfaced map[string]struct{}) []MemoryEntry {
	if len(entries) == 0 || len(alreadySurfaced) == 0 {
		return entries
	}
	filtered := make([]MemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if surfacedEntrySeen(alreadySurfaced, entry) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func rememberSurfacedEntries(alreadySurfaced map[string]struct{}, entries []MemoryEntry) {
	for _, entry := range entries {
		for _, key := range surfacedEntryKeys(entry) {
			alreadySurfaced[key] = struct{}{}
		}
	}
}

func surfacedEntrySeen(alreadySurfaced map[string]struct{}, entry MemoryEntry) bool {
	for _, key := range surfacedEntryKeys(entry) {
		if _, ok := alreadySurfaced[key]; ok {
			return true
		}
	}
	return false
}

func surfacedEntryKeys(entry MemoryEntry) []string {
	pathKey, hashKey := memoryDedupKeys(entry)
	keys := make([]string, 0, 2)
	if pathKey != "" {
		keys = append(keys, "path:"+pathKey)
	}
	if hashKey != "" {
		keys = append(keys, "hash:"+hashKey)
	}
	return keys
}

func cloneSurfacedSet(entries map[string]struct{}) map[string]struct{} {
	if len(entries) == 0 {
		return nil
	}
	cloned := make(map[string]struct{}, len(entries))
	for key := range entries {
		cloned[key] = struct{}{}
	}
	return cloned
}
