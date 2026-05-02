package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"golang.org/x/text/unicode/norm"
)

const (
	diskStoreLockFileName      = ".memory.lock"
	diskStoreLockRetryInterval = 25 * time.Millisecond
	diskStoreLockTimeout       = 5 * time.Second

	// memoryFileBaseMaxBytes mirrors the legacy memory project-key budget, but
	// filename truncation below must be UTF-8 aware because macOS rejects paths
	// that split a multibyte rune with "illegal byte sequence".
	memoryFileBaseMaxBytes = 96
)

var (
	ErrMemoryAlreadyExists     = errors.New("memory already exists")
	ErrMemoryLockTimeout       = errors.New("memory store lock timeout")
	ErrMemoryNotFound          = errors.New("memory not found")
	ErrInvalidMemoryEntry      = errors.New("invalid memory entry")
	ErrMemoryIndexUpdateFailed = errors.New("memory_index_update_failed")
)

type WriteOptions struct {
	SkipIndex bool
}

type MemoryWriteRequest struct {
	Name        string
	Description string
	Type        MemoryType
	Body        string
}

type memoryWriteGuard interface {
	ValidateWrite(path, content string) (string, error)
}

type memoryStructuredStore interface {
	Read(name string) (MemoryEntry, error)
	CreateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	UpdateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	// UpsertStructured writes the entry atomically inside a single disk
	// store lock acquisition (Phase 自有.1a). Replaces the legacy
	// Create-then-Update pattern in upsertStructuredMemory which had a
	// two-phase locking window where another writer could race in between
	// the failed Create and the follow-up Update, producing a lost update.
	UpsertStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error)
	Delete(name string, opts ...WriteOptions) error
}

type memoryWriteStore interface {
	memoryStructuredStore
	Root() string
	Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error)
	Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error)
}

type diskStore struct {
	root  string
	guard memoryWriteGuard
}

var diskStoreLocks sync.Map

func newDiskStore(root string) (*diskStore, error) {
	return newDiskStoreWithGuard(root, nil)
}

func newDiskStoreWithGuard(root string, guard memoryWriteGuard) (*diskStore, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	return &diskStore{root: normalizedRoot, guard: guard}, nil
}

func (s *diskStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *diskStore) CreateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.Create(buildMemoryEntryFromWriteRequest(req), opts...)
}

func (s *diskStore) Create(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, false, resolveWriteOptions(opts))
}

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

func (s *diskStore) Update(entry MemoryEntry, opts ...WriteOptions) (MemoryEntry, error) {
	return s.write(entry, true, resolveWriteOptions(opts))
}

func (s *diskStore) UpdateStructured(req MemoryWriteRequest, opts ...WriteOptions) (MemoryEntry, error) {
	return s.Update(buildMemoryEntryFromWriteRequest(req), opts...)
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
	err = withDiskStoreLock(root, func() error {
		var werr error
		written, werr = writePreparedMemoryFile(root, prepared, s.guard)
		if werr != nil {
			return werr
		}
		return updateIndexAfterMutation(root, options)
	})
	return written, err
}

func (s *diskStore) Delete(name string, opts ...WriteOptions) error {
	root, err := s.rootOrError()
	if err != nil {
		return err
	}
	options := resolveWriteOptions(opts)
	return withDiskStoreLock(root, func() error {
		if err := DeleteMemory(root, name); err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
}

func (s *diskStore) RebuildIndex() ([]MemoryIndexEntry, error) {
	root, err := s.rootOrError()
	if err != nil {
		return nil, err
	}
	return RebuildMemoryIndex(root)
}

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
	err = withDiskStoreLock(root, func() error {
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
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return strings.TrimSuffix(validatedRoot, string(os.PathSeparator)), nil
}

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
			return fmt.Errorf("%w: %s memory content must include Why: and How to apply:", ErrInvalidMemoryEntry, memoryType)
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

func memoryFileBase(name string) string {
	if !hasSlugRune(name) {
		return "mem-" + shared.ShortHash(CanonicalName(name))
	}
	return memoryFileSlug(name)
}

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

func acquireMemoryRootFileLock(root string, timeout time.Duration) (*os.File, error) {
	lockPath, err := ValidateMemoryWritePath(root, filepath.Join(root, diskStoreLockFileName))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := waitForMemoryRootFileLock(file, timeout); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func waitForMemoryRootFileLock(file *os.File, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = diskStoreLockTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		err := tryAcquireMemoryFileLock(file)
		if err == nil {
			return nil
		}
		if !isMemoryFileLockBusy(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s", ErrMemoryLockTimeout, file.Name())
		}
		time.Sleep(diskStoreLockRetryInterval)
	}
}

func closeMemoryRootFileLock(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := releaseMemoryFileLock(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func withDiskStoreLock(root string, fn func() error) (err error) {
	mutexValue, _ := diskStoreLocks.LoadOrStore(root, &sync.Mutex{})
	mutex := mutexValue.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	lockedFile, err := acquireMemoryRootFileLock(root, diskStoreLockTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeMemoryRootFileLock(lockedFile); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return fn()
}

func (s *diskStore) rootOrError() (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: nil store", ErrInvalidMemoryRoot)
	}
	return normalizeStoreRoot(s.root)
}
