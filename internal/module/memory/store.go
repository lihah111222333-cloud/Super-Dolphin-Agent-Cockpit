package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

var (
	ErrMemoryAlreadyExists      = errors.New("memory already exists")
	ErrMemoryNotFound           = errors.New("memory not found")
	ErrInvalidMemoryEntry       = errors.New("invalid memory entry")
	ErrMemoryIndexUpdateFailed  = errors.New("memory_index_update_failed")
)

type WriteOptions struct {
	SkipIndex bool
}

type DiskStore struct {
	root string
}

var diskStoreLocks sync.Map

func NewDiskStore(root string) (*DiskStore, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	return &DiskStore{root: normalizedRoot}, nil
}

func (s *DiskStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *DiskStore) Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, false, resolveWriteOptions(opts))
}

func (s *DiskStore) Read(name string) (MemoryEntry, error) {
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

func (s *DiskStore) Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, true, resolveWriteOptions(opts))
}

func (s *DiskStore) Delete(name string, opts ...WriteOptions) error {
	root, err := s.rootOrError()
	if err != nil {
		return err
	}
	canonicalName, err := canonicalLookupName(name)
	if err != nil {
		return err
	}
	options := resolveWriteOptions(opts)
	return withDiskStoreLock(root, func() error {
		if err := DeleteMemory(root, canonicalName); err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
}

func (s *DiskStore) RebuildIndex() ([]MemoryIndexEntry, error) {
	root, err := s.rootOrError()
	if err != nil {
		return nil, err
	}
	return RebuildMemoryIndex(root)
}

func (s *DiskStore) write(entry MemoryEntry, requireExisting bool, options WriteOptions) (MemoryEntry, error) {
	root, err := s.rootOrError()
	if err != nil {
		return MemoryEntry{}, err
	}
	prepared, err := normalizeWritableEntry(entry)
	if err != nil {
		return MemoryEntry{}, err
	}
	var written MemoryEntry
	err = withDiskStoreLock(root, func() error {
		_, exists, err := findMemoryEntry(root, prepared.CanonicalName)
		if err != nil {
			return err
		}
		if err := validateMutationMode(exists, requireExisting); err != nil {
			return err
		}
		written, err = WriteMemoryFile(root, prepared)
		if err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
	return written, err
}

func WriteMemoryFile(root string, entry MemoryEntry) (MemoryEntry, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return MemoryEntry{}, err
	}
	prepared, err := normalizeWritableEntry(entry)
	if err != nil {
		return MemoryEntry{}, err
	}
	targetPath, err := resolveMemoryFilePath(normalizedRoot, prepared)
	if err != nil {
		return MemoryEntry{}, err
	}
	prepared.FilePath = targetPath
	if err := writeAtomicFile(targetPath, []byte(formatMemoryEntry(prepared)), 0o644); err != nil {
		return MemoryEntry{}, err
	}
	return readMemoryEntryFile(targetPath)
}

func DeleteMemory(root, name string) error {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return err
	}
	canonicalName, err := canonicalLookupName(name)
	if err != nil {
		return err
	}
	entry, exists, err := findMemoryEntry(normalizedRoot, canonicalName)
	if err != nil {
		return err
	}
	if !exists {
		return ErrMemoryNotFound
	}
	validatedPath, err := ValidateMemoryWritePath(normalizedRoot, entry.FilePath)
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
	validatedRoot, err := ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return strings.TrimSuffix(validatedRoot, string(os.PathSeparator)), nil
}

func normalizeWritableEntry(entry MemoryEntry) (MemoryEntry, error) {
	entry = normalizeLoadedEntry(entry)
	if strings.TrimSpace(entry.Frontmatter.Description) == "" {
		entry.Frontmatter.Description = firstNonEmptyLine(entry.Content)
	}
	if strings.TrimSpace(entry.Frontmatter.Name) == "" {
		return MemoryEntry{}, fmt.Errorf("%w: name is required", ErrInvalidMemoryEntry)
	}
	if strings.TrimSpace(entry.Frontmatter.Description) == "" {
		return MemoryEntry{}, fmt.Errorf("%w: description is required", ErrInvalidMemoryEntry)
	}
	if strings.TrimSpace(entry.Content) == "" {
		return MemoryEntry{}, fmt.Errorf("%w: content is required", ErrInvalidMemoryEntry)
	}
	if !entry.Type().IsKnown() {
		return MemoryEntry{}, fmt.Errorf("%w: unknown type", ErrInvalidMemoryEntry)
	}
	entry.Frontmatter.Type = cloneMemoryType(entry.Type())
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
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

func reserveMemoryFilePath(root, dir, base, canonicalName string) (string, error) {
	candidates := []string{filepath.Join(dir, base+".md")}
	hash := shortHash(canonicalName)
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

func memoryFileBase(name string) string {
	if !hasSlugRune(name) {
		return "mem-" + shortHash(CanonicalName(name))
	}
	return SanitizePath(name)
}

func hasSlugRune(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
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
		return fmt.Errorf("%w: %v", ErrMemoryIndexUpdateFailed, err)
	}
	return nil
}

func resolveWriteOptions(opts []WriteOptions) WriteOptions {
	if len(opts) == 0 {
		return WriteOptions{}
	}
	return opts[0]
}

func withDiskStoreLock(root string, fn func() error) error {
	mutexValue, _ := diskStoreLocks.LoadOrStore(root, &sync.Mutex{})
	mutex := mutexValue.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	return fn()
}

func (s *DiskStore) rootOrError() (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: nil store", ErrInvalidMemoryRoot)
	}
	return normalizeStoreRoot(s.root)
}
