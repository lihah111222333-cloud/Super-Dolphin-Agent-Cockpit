package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
)

type diskStore struct {
	root  string
	guard memoryWriteGuard
	locks *diskLockCoordinator
}

func newDiskStore(root string, locks *diskLockCoordinator) (*diskStore, error) {
	return newDiskStoreWithGuard(root, nil, locks)
}

func newDiskStoreWithGuard(root string, guard memoryWriteGuard, locks *diskLockCoordinator) (*diskStore, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	if locks == nil {
		locks = newDiskLockCoordinator()
	}
	return &diskStore{root: normalizedRoot, guard: guard, locks: locks}, nil
}

// Root 处理根目录。
func (s *diskStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// CreateStructured 创建structured。
func (s *diskStore) CreateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.Create(buildMemoryEntryFromWriteRequest(req), opts...)
}

// Create 创建记忆。
func (s *diskStore) Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, false, resolveWriteOptions(opts))
}

// Read 读取记忆。
func (s *diskStore) Read(name string) (MemoryEntry, error) {
	root, err := s.rootOrError()
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

// Update 更新记忆。
func (s *diskStore) Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, true, resolveWriteOptions(opts))
}

// UpdateStructured 更新structured。
func (s *diskStore) UpdateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.Update(buildMemoryEntryFromWriteRequest(req), opts...)
}

// UpdateStructuredPath 更新structured路径。
func (s *diskStore) UpdateStructuredPath(path string, req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.updatePath(path, buildMemoryEntryFromWriteRequest(req), resolveWriteOptions(opts))
}

// UpsertStructured writes the entry atomically inside a single
// withDiskStoreLock acquisition: prepare → acquire lock →
// writePreparedMemoryFile → updateIndexAfterMutation → release lock.
// Skips validateMutationMode entirely — upsert always writes regardless
// of pre-existence. Phase 自有.1a: closes the Create-then-Update RMW
// window in upsertStructuredMemory where two independent lock
// acquisitions allowed a racing writer to flip Create-failed-with-
// AlreadyExists into an Update that silently overwrote a concurrently-
// written entry from another goroutine / process.
func (s *diskStore) UpsertStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.upsertWrite(buildMemoryEntryFromWriteRequest(req), resolveWriteOptions(opts))
}

func (s *diskStore) upsertWrite(entry MemoryEntry, options WriteOptions) (MemoryEntry, error) {
	root, err := s.rootOrError()
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

// Delete 删除记忆。
func (s *diskStore) Delete(name string, opts ...WriteOptions) error {
	root, err := s.rootOrError()
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

// DeletePath 删除路径。
func (s *diskStore) DeletePath(path string, opts ...WriteOptions) error {
	root, err := s.rootOrError()
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

// RebuildIndex 处理rebuild索引。
func (s *diskStore) RebuildIndex() ([]MemoryIndexEntry, error) {
	root, err := s.rootOrError()
	if err != nil {
		return nil, err
	}
	return RebuildMemoryIndex(root)
}

// write 写入记忆。
func (s *diskStore) write(entry MemoryEntry, requireExisting bool, options WriteOptions) (MemoryEntry, error) {
	root, err := s.rootOrError()
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

// updatePath 更新路径。
func (s *diskStore) updatePath(path string, entry MemoryEntry, options WriteOptions) (MemoryEntry, error) {
	root, err := s.rootOrError()
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

// WriteMemoryFile 写入记忆文件。
func WriteMemoryFile(root string, entry MemoryEntry) (MemoryEntry, error) {
	prepared, err := prepareWritableEntry(entry, false)
	if err != nil {
		return MemoryEntry{}, err
	}
	return writePreparedMemoryFile(root, prepared, nil)
}

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

// writePreparedMemoryFilePath 写入prepared记忆文件路径。
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

// DeleteMemory 删除记忆。
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

// DeleteMemoryPath 删除记忆路径。
func DeleteMemoryPath(root, path string) error {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return err
	}
	return removeMemoryFile(normalizedRoot, path)
}

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

// prepareWritableEntry 准备writable条目。
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

func validateStructuredMemoryContent(memoryType MemoryType, content string) error {
	switch memoryType {
	case MemoryTypeFeedback, MemoryTypeProject:
		if !hasAnyStructuredMemorySection(content, "why", "原因") || !hasAnyStructuredMemorySection(content, "how to apply", "如何应用") {
			return fmt.Errorf("%w: %s memory content must include Why: and How to apply", ErrInvalidMemoryEntry, memoryType)
		}
	}
	return nil
}

func hasAnyStructuredMemorySection(content string, labels ...string) bool {
	for _, label := range labels {
		if hasStructuredMemorySection(content, label) {
			return true
		}
	}
	return false
}

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

func canonicalLookupName(name string) (string, error) {
	canonicalName := CanonicalName(name)
	if canonicalName == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalidMemoryEntry)
	}
	return canonicalName, nil
}

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

// findMatchingMemoryEntry 查找matching记忆条目。
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

// memoryDeleteMatchScore 处理记忆deletematchscore。
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

func canonicalMemoryMatchText(text string) string {
	return CanonicalName(strings.ReplaceAll(strings.TrimSpace(text), "\n", " "))
}

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

// reserveMemoryFilePath 处理reserve记忆文件路径。
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

func memoryPathAvailable(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func validateMutationMode(exists, requireExisting bool) error {
	if requireExisting && !exists {
		return ErrMemoryNotFound
	}
	if !requireExisting && exists {
		return ErrMemoryAlreadyExists
	}
	return nil
}

func updateIndexAfterMutation(root string, options WriteOptions) error {
	if options.SkipIndex {
		return nil
	}
	if _, err := UpdateMemoryIndex(root); err != nil {
		return fmt.Errorf("%w: %w", ErrMemoryIndexUpdateFailed, err)
	}
	return nil
}

func resolveWriteOptions(opts []WriteOptions) WriteOptions {
	if len(opts) == 0 {
		return WriteOptions{}
	}
	return opts[0]
}

func (s *diskStore) rootOrError() (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: nil store", ErrInvalidMemoryRoot)
	}
	return normalizeStoreRoot(s.root)
}
