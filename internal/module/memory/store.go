package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

// diskStore 以磁盘 Markdown 文件实现记忆存储。
// 所有写入通过 diskLockCoordinator 串行化，guard 负责团队记忆等额外写入校验。
type diskStore struct {
	*diskStoreIndexOps

	root  string
	guard memoryWriteGuard
	locks *diskLockCoordinator
}

type diskStoreIndexOps struct {
	store *diskStore
}

// newDiskStore 创建不带额外写入守卫的磁盘记忆存储。
func newDiskStore(root string, locks *diskLockCoordinator) (*diskStore, error) {
	return newDiskStoreWithGuard(root, nil, locks)
}

// newDiskStoreWithGuard 创建磁盘记忆存储，并在写文件前执行可选 guard。
func newDiskStoreWithGuard(root string, guard memoryWriteGuard, locks *diskLockCoordinator) (*diskStore, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	if locks == nil {
		locks = newDiskLockCoordinator()
	}
	store := &diskStore{root: normalizedRoot, guard: guard, locks: locks}
	store.diskStoreIndexOps = &diskStoreIndexOps{store: store}
	return store, nil
}

// Root 返回规范化后的记忆根目录。
func (s *diskStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// CreateStructured 将结构化写请求转换为记忆条目后创建新文件。
func (s *diskStore) CreateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.Create(buildMemoryEntryFromWriteRequest(req), opts...)
}

// Create 创建新记忆条目，已存在同名规范化条目时返回 ErrMemoryAlreadyExists。
func (s *diskStore) Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, false, resolveWriteOptions(opts))
}

// Read 按规范化名称读取记忆条目。
func (s *diskStore) Read(name string) (MemoryEntry, error) {
	root, err := diskStoreRootOrError(s)
	if err != nil {
		return MemoryEntry{}, err
	}
	canonicalName, err := canonicalLookupName(name)
	if err != nil {
		return MemoryEntry{}, err
	}
	entry, exists, err := findMemoryEntry(root, canonicalName)
	if err != nil {
		return MemoryEntry{}, err
	}
	if !exists {
		return MemoryEntry{}, ErrMemoryNotFound
	}
	return entry, nil
}

// Update 更新已存在的记忆条目，不存在时返回 ErrMemoryNotFound。
func (s *diskStore) Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, true, resolveWriteOptions(opts))
}

// UpdateStructured 将结构化写请求转换为记忆条目后更新已有文件。
func (s *diskStore) UpdateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.Update(buildMemoryEntryFromWriteRequest(req), opts...)
}

// UpdateStructuredPath 按指定文件路径更新结构化记忆。
// 路径、名称和类型都必须匹配现有条目，避免把一个文件改写成另一类记忆。
func (s *diskStore) UpdateStructuredPath(path string, req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.updatePath(path, buildMemoryEntryFromWriteRequest(req), resolveWriteOptions(opts))
}

// UpsertStructured 在单次磁盘锁内完成 prepare、写文件和索引更新。
// 它不检查 create/update 模式，目的是关闭先 Create 再 Update 带来的并发覆盖窗口。
func (s *diskStore) UpsertStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.upsertWrite(buildMemoryEntryFromWriteRequest(req), resolveWriteOptions(opts))
}

// upsertWrite 在单次磁盘锁内写入条目并刷新索引。
func (s *diskStore) upsertWrite(entry MemoryEntry, options WriteOptions) (MemoryEntry, error) {
	root, err := diskStoreRootOrError(s)
	if err != nil {
		return MemoryEntry{}, err
	}
	prepared, err := prepareWritableEntry(entry, false)
	if err != nil {
		return MemoryEntry{}, err
	}
	var written MemoryEntry
	err = s.locks.withDiskStoreLock(root, func() error {
		var werr error
		written, werr = writePreparedMemoryFile(root, prepared, s.guard)
		if werr != nil {
			return werr
		}
		return updateIndexAfterMutation(root, options)
	})
	return written, err
}

// Delete 按名称删除记忆条目，并在同一把磁盘锁内刷新索引。
func (s *diskStore) Delete(name string, opts ...WriteOptions) error {
	root, err := diskStoreRootOrError(s)
	if err != nil {
		return err
	}
	options := resolveWriteOptions(opts)
	return s.locks.withDiskStoreLock(root, func() error {
		if err := DeleteMemory(root, name); err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
}

// DeletePath 按文件路径删除记忆条目，并在同一把磁盘锁内刷新索引。
func (s *diskStore) DeletePath(path string, opts ...WriteOptions) error {
	root, err := diskStoreRootOrError(s)
	if err != nil {
		return err
	}
	options := resolveWriteOptions(opts)
	return s.locks.withDiskStoreLock(root, func() error {
		if err := DeleteMemoryPath(root, path); err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
}

// RebuildIndex 重建记忆索引文件。
func (s *diskStoreIndexOps) RebuildIndex() ([]MemoryIndexEntry, error) {
	var store *diskStore
	if s != nil {
		store = s.store
	}
	root, err := diskStoreRootOrError(store)
	if err != nil {
		return nil, err
	}
	return RebuildMemoryIndex(root)
}

// write 在磁盘锁内执行 create/update 写入。
// requireExisting 决定是否必须已有条目，写入成功后立即更新索引，避免索引和文件内容分离。
func (s *diskStore) write(entry MemoryEntry, requireExisting bool, options WriteOptions) (MemoryEntry, error) {
	root, err := diskStoreRootOrError(s)
	if err != nil {
		return MemoryEntry{}, err
	}
	prepared, err := prepareWritableEntry(entry, false)
	if err != nil {
		return MemoryEntry{}, err
	}
	var written MemoryEntry
	err = s.locks.withDiskStoreLock(root, func() error {
		_, exists, err := findMemoryEntry(root, prepared.CanonicalName)
		if err != nil {
			return err
		}
		if err := validateMutationMode(exists, requireExisting); err != nil {
			return err
		}
		written, err = writePreparedMemoryFile(root, prepared, s.guard)
		if err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
	return written, err
}

// updatePath 在磁盘锁内按路径更新条目。
// 除路径安全外，还会校验现有文件的规范化名称和类型，防止跨条目覆盖。
func (s *diskStore) updatePath(path string, entry MemoryEntry, options WriteOptions) (MemoryEntry, error) {
	root, err := diskStoreRootOrError(s)
	if err != nil {
		return MemoryEntry{}, err
	}
	prepared, err := prepareWritableEntry(entry, false)
	if err != nil {
		return MemoryEntry{}, err
	}
	var written MemoryEntry
	err = s.locks.withDiskStoreLock(root, func() error {
		validatedPath, err := ValidateMemoryWritePath(root, path)
		if err != nil {
			return err
		}
		existing, err := readMemoryEntryFile(validatedPath)
		if errors.Is(err, os.ErrNotExist) {
			return ErrMemoryNotFound
		}
		if err != nil {
			return err
		}
		if existing.CanonicalName != prepared.CanonicalName {
			return fmt.Errorf("%w: name mismatch", ErrInvalidMemoryEntry)
		}
		if existing.Type() != prepared.Type() {
			return fmt.Errorf("%w: type mismatch", ErrInvalidMemoryEntry)
		}
		written, err = writePreparedMemoryFilePath(root, validatedPath, prepared, s.guard)
		if err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
	return written, err
}

// WriteMemoryFile 按 root 和 entry 写入单个记忆文件。
// 这是低层 helper，不刷新索引；调用方需要自己处理索引一致性。
func WriteMemoryFile(root string, entry MemoryEntry) (MemoryEntry, error) {
	prepared, err := prepareWritableEntry(entry, false)
	if err != nil {
		return MemoryEntry{}, err
	}
	return writePreparedMemoryFile(root, prepared, nil)
}

// writePreparedMemoryFile 为已校验条目分配安全路径并写入磁盘。
func writePreparedMemoryFile(root string, prepared MemoryEntry, guard memoryWriteGuard) (MemoryEntry, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return MemoryEntry{}, err
	}
	targetPath, err := resolveMemoryFilePath(normalizedRoot, prepared)
	if err != nil {
		return MemoryEntry{}, err
	}
	return writePreparedMemoryFilePath(normalizedRoot, targetPath, prepared, guard)
}

// writePreparedMemoryFilePath 在指定安全路径写入已准备好的记忆条目。
// 写入前先执行可选 guard 和内容校验，成功后重新读取文件作为返回值。
func writePreparedMemoryFilePath(root, targetPath string, prepared MemoryEntry, guard memoryWriteGuard) (MemoryEntry, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return MemoryEntry{}, err
	}
	targetPath, err = ValidateMemoryWritePath(normalizedRoot, targetPath)
	if err != nil {
		return MemoryEntry{}, err
	}
	raw := formatMemoryEntry(prepared)
	if guard != nil {
		targetPath, err = guard.ValidateWrite(targetPath, raw)
		if err != nil {
			return MemoryEntry{}, err
		}
	}
	if err := ValidateMemoryEntryContent(prepared); err != nil {
		return MemoryEntry{}, err
	}
	prepared.FilePath = targetPath
	if err := writeAtomicFile(targetPath, []byte(raw), 0o644); err != nil {
		return MemoryEntry{}, err
	}
	return readMemoryEntryFile(targetPath)
}

// DeleteMemory 按名称删除记忆文件，名称可走精确或模糊匹配。
func DeleteMemory(root, name string) error {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return err
	}
	entry, exists, err := findMemoryEntryForDelete(normalizedRoot, name)
	if err != nil {
		return err
	}
	if !exists {
		return ErrMemoryNotFound
	}
	return removeMemoryFile(normalizedRoot, entry.FilePath)
}

// DeleteMemoryPath 按路径删除记忆文件。
func DeleteMemoryPath(root, path string) error {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return err
	}
	return removeMemoryFile(normalizedRoot, path)
}

// removeMemoryFile 校验删除目标并拒绝删除 MEMORY.md 入口文件。
func removeMemoryFile(root, path string) error {
	if filepath.Base(filepath.ToSlash(strings.TrimSpace(path))) == memoryIndexFileName {
		return invalidMemoryWritePath("cannot remove memory entrypoint")
	}
	validatedPath, err := ValidateMemoryWritePath(root, path)
	if err != nil {
		return err
	}

	if err := os.Remove(validatedPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrMemoryNotFound
		}
		return err
	}
	return nil
}

// normalizeStoreRoot 校验并标准化磁盘存储根目录。
func normalizeStoreRoot(root string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return strings.TrimSuffix(validatedRoot, string(os.PathSeparator)), nil
}

// prepareWritableEntry 标准化待写入条目并校验 frontmatter 与结构化内容。
// validateContent 为 true 时会执行正文长度/截断规则校验。
func prepareWritableEntry(entry MemoryEntry, validateContent bool) (MemoryEntry, error) {
	entry = normalizeLoadedEntry(entry)
	if strings.TrimSpace(entry.Content) == "" {
		return MemoryEntry{}, fmt.Errorf("%w: content is required", ErrInvalidMemoryEntry)
	}
	if err := validateRequiredMemoryFrontmatter(entry.Frontmatter); err != nil {
		return MemoryEntry{}, err
	}
	memoryType := entry.Type()
	if validateContent {
		if err := ValidateMemoryEntryContent(entry); err != nil {
			return MemoryEntry{}, err
		}
	}
	if err := validateStructuredMemoryContent(memoryType, entry.Content); err != nil {
		return MemoryEntry{}, err
	}
	entry.Frontmatter.Type = cloneMemoryType(memoryType)
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
}

// validateRequiredMemoryFrontmatter 校验写入所需的基础 frontmatter 字段。
func validateRequiredMemoryFrontmatter(frontmatter MemoryFrontmatter) error {
	if strings.TrimSpace(frontmatter.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidMemoryEntry)
	}
	if strings.TrimSpace(frontmatter.Description) == "" {
		return fmt.Errorf("%w: description is required", ErrInvalidMemoryEntry)
	}
	if frontmatter.Type == nil {
		return fmt.Errorf("%w: type is required", ErrInvalidMemoryEntry)
	}
	if !ParseMemoryType(string(*frontmatter.Type)).IsKnown() {
		return fmt.Errorf("%w: unknown type", ErrInvalidMemoryEntry)
	}
	return nil
}

// validateStructuredMemoryContent 校验结构化类型的正文必须包含关键段落。
func validateStructuredMemoryContent(memoryType MemoryType, content string) error {
	switch memoryType {
	case MemoryTypeFeedback, MemoryTypeProject:
		if !hasAnyStructuredMemorySection(content, "why", "原因") || !hasAnyStructuredMemorySection(content, "how to apply", "如何应用") {
			return fmt.Errorf("%w: %s memory content must include Why: and How to apply", ErrInvalidMemoryEntry, memoryType)
		}
	}
	return nil
}

// hasAnyStructuredMemorySection 判断正文是否包含任一指定结构化段落标题。
func hasAnyStructuredMemorySection(content string, labels ...string) bool {
	for _, label := range labels {
		if hasStructuredMemorySection(content, label) {
			return true
		}
	}
	return false
}

// hasStructuredMemorySection 判断正文行是否包含指定段落标题。
func hasStructuredMemorySection(content, label string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		normalized := strings.ToLower(strings.TrimSpace(line))
		normalized = strings.TrimPrefix(normalized, "- ")
		normalized = strings.ReplaceAll(normalized, "**", "")
		if strings.HasPrefix(normalized, label+":") || strings.HasPrefix(normalized, label+"：") {
			return true
		}
	}
	return false
}

// buildMemoryEntryFromWriteRequest 将 RPC 写请求转换为内部 MemoryEntry。
func buildMemoryEntryFromWriteRequest(req MemoryWriteRequest) MemoryEntry {
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        strings.TrimSpace(req.Name),
			Description: strings.TrimSpace(req.Description),
			Type:        cloneMemoryType(req.Type),
			Title:       strings.TrimSpace(req.Title),
			Source:      strings.TrimSpace(req.Source),
		},

		Content: strings.TrimSpace(req.Body),
	}
}

// canonicalLookupName 规范化查询名称，并拒绝空名称。
func canonicalLookupName(name string) (string, error) {
	canonicalName := CanonicalName(name)
	if canonicalName == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalidMemoryEntry)
	}
	return canonicalName, nil
}

// findMemoryEntry 按规范化名称查找去重后的记忆条目。
func findMemoryEntry(root, canonicalName string) (MemoryEntry, bool, error) {
	entries, err := scanMemoryEntries(root)
	if err != nil {
		return MemoryEntry{}, false, err
	}
	for _, entry := range uniqueEntriesByCanonicalName(entries) {
		if entry.CanonicalName == canonicalName {
			return entry, true, nil
		}
	}
	return MemoryEntry{}, false, nil
}

// findMemoryEntryForDelete 先按规范化名称精确查找，失败后再走模糊匹配。
func findMemoryEntryForDelete(root, name string) (MemoryEntry, bool, error) {
	if _, err := canonicalLookupName(name); err != nil {
		return MemoryEntry{}, false, err
	}
	entry, exists, err := findMemoryEntry(root, CanonicalName(name))
	if err != nil || exists {
		return entry, exists, err
	}
	return findMatchingMemoryEntry(root, name)
}

// findMatchingMemoryEntry 对删除请求执行模糊匹配。
// 多个候选分数相同时使用 preferMemoryEntry 稳定选择，避免删除结果不确定。
func findMatchingMemoryEntry(root, query string) (MemoryEntry, bool, error) {
	entries, err := scanMemoryEntries(root)
	if err != nil {
		return MemoryEntry{}, false, err
	}
	query = canonicalMemoryMatchText(query)
	if query == "" {
		return MemoryEntry{}, false, nil
	}
	var best MemoryEntry
	bestScore := 0
	found := false
	for _, entry := range uniqueEntriesByCanonicalName(entries) {
		score := memoryDeleteMatchScore(query, entry)
		if score == 0 {
			continue
		}
		if !found || score > bestScore || (score == bestScore && preferMemoryEntry(entry, best)) {
			best = entry
			bestScore = score
			found = true
		}
	}
	return best, found, nil
}

// memoryDeleteMatchScore 计算删除查询与条目的匹配分数。
// 名称权重最高，其次描述、hook 和正文，模糊包含关系使用较低分数。
func memoryDeleteMatchScore(query string, entry MemoryEntry) int {
	fields := []struct {
		text  string
		exact int
		fuzzy int
	}{
		{text: entry.Frontmatter.Name, exact: 100, fuzzy: 80},
		{text: entry.Frontmatter.Description, exact: 95, fuzzy: 75},
		{text: hookFromEntry(entry), exact: 90, fuzzy: 70},
		{text: entry.Content, exact: 85, fuzzy: 65},
	}
	best := 0
	for _, field := range fields {
		target := canonicalMemoryMatchText(field.text)
		if target == "" {
			continue
		}
		if target == query {
			return field.exact
		}
		if strings.Contains(target, query) || strings.Contains(query, target) {
			if field.fuzzy > best {
				best = field.fuzzy
			}
		}
	}
	return best
}

// canonicalMemoryMatchText 规范化用于模糊匹配的文本。
func canonicalMemoryMatchText(text string) string {
	return CanonicalName(strings.ReplaceAll(strings.TrimSpace(text), "\n", " "))
}

// resolveMemoryFilePath 复用现有条目路径；新条目则按类型目录预留不冲突路径。
func resolveMemoryFilePath(root string, entry MemoryEntry) (string, error) {
	existing, exists, err := findMemoryEntry(root, entry.CanonicalName)
	if err != nil {
		return "", err
	}
	if exists {
		return ValidateMemoryWritePath(root, existing.FilePath)
	}
	dir := memoryTypeDir(root, entry.Type())
	base := memoryFileBase(entry.Frontmatter.Name)
	return reserveMemoryFilePath(root, dir, base, entry.CanonicalName)
}

// reserveMemoryFilePath 为新记忆分配可用文件名。
// 先尝试可读名称，再追加短 hash 和序号，所有候选都必须通过写路径校验。
func reserveMemoryFilePath(root, dir, base, canonicalName string) (string, error) {
	candidates := []string{filepath.Join(dir, base+".md")}
	hash := shared.ShortHash(canonicalName)
	for attempt := range 8 {
		name := fmt.Sprintf("%s-%s", base, hash)
		if attempt > 0 {
			name = fmt.Sprintf("%s-%s-%d", base, hash, attempt+1)
		}
		candidates = append(candidates, filepath.Join(dir, name+".md"))
	}
	for _, candidate := range candidates {
		validatedPath, err := ValidateMemoryWritePath(root, candidate)
		if err != nil {
			return "", err
		}
		available, err := memoryPathAvailable(validatedPath)
		if err != nil {
			return "", err
		}
		if available {
			return validatedPath, nil
		}
	}
	return "", fmt.Errorf("%w: unable to allocate file path", ErrInvalidMemoryEntry)
}

// memoryPathAvailable 判断路径是否尚未存在。
func memoryPathAvailable(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// validateMutationMode 校验 create/update 模式与现有条目状态是否匹配。
func validateMutationMode(exists, requireExisting bool) error {
	if requireExisting && !exists {
		return ErrMemoryNotFound
	}
	if !requireExisting && exists {
		return ErrMemoryAlreadyExists
	}
	return nil
}

// updateIndexAfterMutation 在写操作完成后按需重建索引。
// 批量写入可用 SkipIndex 延后索引维护；普通写入失败必须返回，避免磁盘条目和索引静默分叉。
func updateIndexAfterMutation(root string, options WriteOptions) error {
	if options.SkipIndex {
		return nil
	}
	if _, err := UpdateMemoryIndex(root); err != nil {
		return fmt.Errorf("%w: %v", ErrMemoryIndexUpdateFailed, err)
	}
	return nil
}

// resolveWriteOptions 解析可变参数形式的写入选项。
// 当前写路径只接受首个选项，保持旧调用兼容并避免多个选项叠加出隐式优先级。
func resolveWriteOptions(opts []WriteOptions) WriteOptions {
	if len(opts) == 0 {
		return WriteOptions{}
	}
	return opts[0]
}

// diskStoreRootOrError 返回 diskStore 的规范化根目录，nil store 或空根目录直接报错以阻断后续路径校验。
func diskStoreRootOrError(s *diskStore) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: nil store", ErrInvalidMemoryRoot)
	}
	return normalizeStoreRoot(s.root)
}
