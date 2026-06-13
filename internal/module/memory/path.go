package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"golang.org/x/text/unicode/norm"
)

const (
	memoryIndexFileName    = "MEMORY.md"
	memoryProjectsDir      = "projects"
	memoryProjectDirName   = "memory"
	gitResolveTimeout      = 4 * time.Second
	consolidationStampFile = ".consolidation.stamp.json"
)

var (
	ErrInvalidMemoryRoot      = shared.ErrInvalidMemoryRoot
	ErrInvalidMemoryReadPath  = errors.New("invalid memory read path")
	ErrInvalidMemoryWritePath = errors.New("invalid memory write path")
)

type consolidationStamp struct {
	LastScanAt    string `json:"last_scan_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
}

// GetAutoMemPath 读取automem路径。
func GetAutoMemPath(baseRoot, projectRoot string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(baseRoot)
	if err != nil || validatedRoot == "" {
		return "", err
	}
	canonicalRoot, err := FindCanonicalGitRoot(context.Background(), projectRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(canonicalRoot) == "" {
		return "", fmt.Errorf("%w: empty project root", ErrInvalidMemoryRoot)
	}
	root := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	return filepath.Join(root, memoryProjectsDir, SanitizePath(canonicalRoot), memoryProjectDirName), nil
}

// GetAutoMemDailyLogPath 读取automemdaily日志路径。
func GetAutoMemDailyLogPath(baseRoot, projectRoot string, now time.Time) (string, error) {
	return getAutoMemDailyLogPath(baseRoot, projectRoot, now)
}

// FindCanonicalGitRoot 查找canonicalgit根目录。
func FindCanonicalGitRoot(ctx context.Context, projectRoot string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback, err := shared.CleanAbsolutePath(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	gitCtx, cancel := ctxutil.WithTimeout(ctx, gitResolveTimeout)
	defer cancel()

	cmd := exec.CommandContext(gitCtx, "git", "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	cmd.Dir = fallback
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git root for %q: %w", fallback, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return fallback, nil
	}
	gitRoot := strings.TrimSpace(lines[0])
	if gitRoot == "" {
		return fallback, nil
	}
	gitRoot = filepath.Clean(norm.NFC.String(gitRoot))
	if len(lines) < 2 {
		return gitRoot, nil
	}
	commonDir := strings.TrimSpace(lines[1])
	if filepath.Base(commonDir) == ".git" {
		parent := filepath.Dir(filepath.Clean(commonDir))
		if parent != "" {
			return parent, nil
		}
	}
	return gitRoot, nil
}

// SanitizePath 清理路径。
func SanitizePath(raw string) string {
	return pathutil.SanitizeMemoryProjectKey(raw)
}

// ValidateMemoryWritePath 校验记忆write路径。
func ValidateMemoryWritePath(root, file string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryWritePath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file, invalidMemoryWritePath)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveMemoryWritePath(rootDir)
	if err != nil {
		return "", err
	}
	candidateReal, err := resolveMemoryWritePath(candidate)
	if err != nil {
		return "", err
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryWritePath("path escapes root")
	}
	return candidate, nil
}

// ValidateMemoryReadPath 校验记忆read路径。
func ValidateMemoryReadPath(root, file string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryReadPath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file, invalidMemoryReadPath)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveExistingMemoryPath(rootDir)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	candidateReal, err := resolveExistingMemoryPath(candidate)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryReadPath("path escapes root")
	}
	if info, err := os.Stat(candidateReal); err != nil {
		return "", invalidMemoryReadPath(err.Error())
	} else if info.IsDir() {
		return "", invalidMemoryReadPath("path is a directory")
	}
	return candidateReal, nil
}

// prepareMemoryPath 准备记忆路径。
func prepareMemoryPath(validatedRoot, file string, wrap func(string) error) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", wrap("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", wrap("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := shared.CleanAbsolutePath(candidate)
	if err != nil {
		return "", "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(rootDir); err != nil {
		return "", "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(candidate); err != nil {
		return "", "", wrap(err.Error())
	}
	return rootDir, candidate, nil
}

func resolveMemoryWritePath(path string) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", invalidMemoryWritePath(err.Error())
	}
	if resolved == "" {
		return path, nil
	}
	return resolved, nil
}

func resolveExistingMemoryPath(path string) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil {
		return "", err
	}
	if resolved == "" {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

func invalidMemoryReadPath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMemoryReadPath, reason)
}

func invalidMemoryWritePath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMemoryWritePath, reason)
}

func memoryIndexPath(root string) string {
	return filepath.Join(root, memoryIndexFileName)
}

func memoryTypeDir(root string, memoryType MemoryType) string {
	return filepath.Join(root, string(ParseMemoryType(string(memoryType))))
}

// writeAtomicFile 写入atomic文件。
func writeAtomicFile(path string, data []byte, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o644
	}
	validatedPath, err := ValidateMemoryWritePath(filepath.Dir(path), path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(validatedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(validatedPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, validatedPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// loadConsolidationStamp 加载consolidationstamp。
func loadConsolidationStamp(root string) (consolidationStamp, error) {
	path, err := consolidationStampPath(root)
	if err != nil {
		return consolidationStamp{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return consolidationStamp{}, nil
	}
	if err != nil {
		return consolidationStamp{}, err
	}
	if len(raw) == 0 {
		return consolidationStamp{}, nil
	}
	var stamp consolidationStamp
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return consolidationStamp{}, err
	}
	return stamp, nil
}

func saveConsolidationStamp(root string, stamp consolidationStamp) error {
	path, err := consolidationStampPath(root)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(stamp, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(path, raw, 0o644)
}

func consolidationStampPath(root string) (string, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return "", err
	}
	return ValidateMemoryWritePath(normalizedRoot, filepath.Join(normalizedRoot, consolidationStampFile))
}

func recordConsolidation(root string, when time.Time) error {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return err
	}
	stamp.LastSuccessAt = stampTimeString(when)
	return saveConsolidationStamp(root, stamp)
}

func recordConsolidationScan(root string, when time.Time) error {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return err
	}
	stamp.LastScanAt = stampTimeString(when)
	return saveConsolidationStamp(root, stamp)
}

func stampTimeString(when time.Time) string {
	if when.IsZero() {
		when = time.Now()
	}
	return when.UTC().Format(time.RFC3339Nano)
}

func (s consolidationStamp) lastScanTime() time.Time {
	return parseStampTime(s.LastScanAt)
}

func (s consolidationStamp) lastSuccessTime() time.Time {
	return parseStampTime(s.LastSuccessAt)
}

func parseStampTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func consolidationCandidates(entries []MemoryEntry) []MemoryEntry {
	unique := uniqueEntriesByCanonicalName(entries)
	selected := make([]MemoryEntry, 0, len(unique))
	for _, entry := range unique {
		if hasMeaningfulMemoryContent(entry.Content) {
			selected = append(selected, entry)
		}
	}
	return selected
}

// staleMemoryPaths 处理stale记忆路径。
func staleMemoryPaths(entries []MemoryEntry) []string {
	selected := make(map[string]MemoryEntry, len(entries))
	stale := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !hasMeaningfulMemoryContent(entry.Content) {
			if entry.FilePath != "" {
				stale = append(stale, entry.FilePath)
			}
			continue
		}
		key := entry.CanonicalName
		if key == "" {
			key = CanonicalName(entry.Frontmatter.Name)
		}
		current, exists := selected[key]
		if !exists || preferMemoryEntry(entry, current) {
			if exists && current.FilePath != "" {
				stale = append(stale, current.FilePath)
			}
			selected[key] = entry
			continue
		}
		if entry.FilePath != "" {
			stale = append(stale, entry.FilePath)
		}
	}
	return uniqueNonEmptyStrings(stale)
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func removeMemoryFiles(root string, paths []string) error {
	for _, path := range paths {
		validatedPath, err := ValidateMemoryWritePath(root, path)
		if err != nil {
			return err
		}
		if err := os.Remove(validatedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeConsolidatedMemories(root string, items []ExtractedMemory) error {
	entries, err := prepareConsolidatedMemoryEntries(root, items)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := WriteMemoryFile(root, entry); err != nil {
			return err
		}
	}
	return nil
}

// prepareConsolidatedMemoryEntries 准备consolidated记忆条目。
func prepareConsolidatedMemoryEntries(root string, items []ExtractedMemory) ([]MemoryEntry, error) {
	if len(items) == 0 {
		return nil, nil
	}
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	entries := make([]MemoryEntry, 0, len(items))
	for _, item := range items {
		entry := buildConsolidatedMemoryEntry(item)
		prepared, err := prepareWritableEntry(entry, false)
		if err != nil {
			return nil, err
		}
		if _, err := resolveMemoryFilePath(normalizedRoot, prepared); err != nil {
			return nil, err
		}
		entries = append(entries, prepared)
	}
	return entries, nil
}

func buildConsolidatedMemoryEntry(item ExtractedMemory) MemoryEntry {
	item = normalizeExtractedMemory(item)
	description := truncateRunes(firstNonEmptyLine(item.Content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(item.Content, memoryHookMaxRunes)
	}
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        consolidationName(item, description),
			Description: description,
			Type:        cloneMemoryType(item.Type),
			SearchKeys:  normalizeStringSlice(item.Tags),
			Source:      "dream",
		},
		Content: item.Content,
	}
}

func consolidationName(item ExtractedMemory, description string) string {
	if description != "" {
		return description
	}
	if item.Type.IsKnown() {
		return fmt.Sprintf("%s dream note", item.Type)
	}
	return "Dream note"
}

type MemoryType = shared.MemoryType

const (
	MemoryTypeUnknown   = shared.MemoryTypeUnknown
	MemoryTypeUser      = shared.MemoryTypeUser
	MemoryTypeFeedback  = shared.MemoryTypeFeedback
	MemoryTypeProject   = shared.MemoryTypeProject
	MemoryTypeReference = shared.MemoryTypeReference
)

var diskMemoryTypes = []MemoryType{
	MemoryTypeUser,
	MemoryTypeFeedback,
	MemoryTypeProject,
	MemoryTypeReference,
}

// ParseMemoryType 解析记忆type。
func ParseMemoryType(raw string) MemoryType { return shared.ParseMemoryType(raw) }

// CanonicalName 处理canonical名称。
func CanonicalName(raw string) string { return shared.CanonicalName(raw) }

type MemoryScope string

const (
	MemoryScopeUser    MemoryScope = "user"
	MemoryScopeProject MemoryScope = "project"
	MemoryScopeLocal   MemoryScope = "local"
)

type MemoryFrontmatter = shared.MemoryFrontmatter
type MemoryEntry = shared.MemoryEntry
type ParsedMemory = shared.ParsedMemory

type SaveIntent struct {
	Detected bool
	Content  string
	Type     MemoryType
}

func cloneMemoryType(t MemoryType) *MemoryType {
	return shared.CloneMemoryType(t)
}

func normalizeStringSlice(values []string) []string {
	return shared.NormalizeStringSlice(values)
}
