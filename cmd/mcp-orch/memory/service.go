package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type service struct {
	cfg *Config
}

func NewService(cfg *Config) contract.MemoryService {
	return &service{cfg: cfg}
}

func (s *service) Read(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	prepared, root, denyReason, err := s.prepareRead(ctx, req)
	if err != nil || denyReason != "" {
		return contract.MemoryReadResult{DenyReason: denyReason}, err
	}
	indexEntries, degraded, source, err := loadIndex(root)
	if err != nil {
		return contract.MemoryReadResult{}, classifyError(err)
	}
	entry, indexHit, err := lookupEntry(root, prepared.Name, prepared.Path, indexEntries)
	if err != nil {
		return contract.MemoryReadResult{}, classifyError(err)
	}
	if entry.filePath == "" || (prepared.Type.IsKnown() && entry.entry.Type != prepared.Type) {
		return applyReadMetadata(contract.MemoryReadResult{IndexHit: indexHit}, degraded, source), nil
	}
	dto := entryToContract(entry, root)
	return applyReadMetadata(contract.MemoryReadResult{Entry: &dto, SourcePath: dto.SourcePath, IndexHit: indexHit}, degraded, source), nil
}

func (s *service) Write(ctx context.Context, req contract.MemoryWriteRequest) (contract.MemoryWriteResult, error) {
	name, content, err := s.validateWriteInput(req)
	if err != nil {
		return contract.MemoryWriteResult{}, err
	}

	root, err := s.resolveWriteRoot(ctx, req.Scope)
	if err != nil {
		return contract.MemoryWriteResult{}, err
	}

	memType := req.Type
	if !memType.IsKnown() {
		memType = contract.MemoryTypeFeedback
	}
	content = ensureStructuredSections(content, memType)

	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = firstNonEmptyLine(content)
	}

	targetPath, targetDir := s.resolveWriteTarget(root, name, memType)

	if _, err := validateMemoryWritePath(root, targetPath); err != nil {
		return contract.MemoryWriteResult{}, fmt.Errorf("%w: %v", contract.ErrMemoryPersist, err)
	}

	fileContent := buildFrontmatter(name, description, memType) + content
	if err := atomicWrite(targetDir, targetPath, fileContent); err != nil {
		return contract.MemoryWriteResult{}, fmt.Errorf("%w: %v", contract.ErrMemoryPersist, err)
	}

	if err := rebuildIndex(root); err != nil {
		return contract.MemoryWriteResult{}, fmt.Errorf("%w: %v", contract.ErrMemoryPersist, err)
	}

	relPath := relativePath(root, targetPath)
	return contract.MemoryWriteResult{Path: relPath}, nil

}

func (s *service) validateWriteInput(req contract.MemoryWriteRequest) (string, string, error) {
	if err := s.ensureEnabled(); err != nil {
		return "", "", err
	}
	name := strings.TrimSpace(req.Name)
	content := strings.TrimSpace(req.Content)
	if name == "" || content == "" {
		return "", "", fmt.Errorf("%w: name and content are required", contract.ErrMemoryInvalidParam)
	}
	return name, content, nil
}

func (s *service) resolveWriteRoot(ctx context.Context, rawScope contract.MemoryScope) (string, error) {
	scope := sanitizeScope(rawScope)
	root, denyReason, err := resolveScopeRoot(ctx, s.cfg, scope)
	if err != nil {
		return "", err
	}
	if denyReason != "" {
		return "", fmt.Errorf("%w: %s", contract.ErrMemoryInvalidParam, denyReason)
	}
	return root, nil
}

func (s *service) resolveWriteTarget(root, name string, memType contract.MemoryType) (targetPath, targetDir string) {
	typeDir := string(memType)
	targetDir = filepath.Join(root, typeDir)
	if existingPath := s.findExistingByName(root, name); existingPath != "" {
		targetPath = existingPath
		targetDir = filepath.Dir(targetPath)
		return targetPath, targetDir
	}
	targetPath = s.reserveWriteTarget(root, targetDir, name)
	return targetPath, targetDir
}

func (s *service) reserveWriteTarget(root, targetDir, name string) string {
	slug := slugify(name)
	candidates := []string{filepath.Join(targetDir, slug+".md")}
	hash := shortNameHash(canonicalName(name))
	for attempt := 0; attempt < 8; attempt++ {
		candidateSlug := slug + "-" + hash
		if attempt > 0 {
			candidateSlug = fmt.Sprintf("%s-%s-%d", slug, hash, attempt+1)
		}
		candidates = append(candidates, filepath.Join(targetDir, candidateSlug+".md"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return filepath.Join(targetDir, slug+"-"+hash+".md")
}

func (s *service) findExistingByName(root, name string) string {
	entries, err := scanEntries(root)
	if err != nil {
		return ""
	}
	key := canonicalName(name)
	for _, entry := range entries {
		if entry.canonicalName == key {
			return entry.filePath
		}
	}
	return ""
}

func ensureStructuredSections(content string, memType contract.MemoryType) string {
	if memType != contract.MemoryTypeFeedback && memType != contract.MemoryTypeProject {
		return content
	}
	if !containsAnySection(content, "why", "原因") {
		content += "\nWhy: agent auto-detected this as important context."
	}
	if !containsAnySection(content, "how to apply", "如何应用") {
		content += "\nHow to apply: follow this guidance in future work."
	}

	return content
}

func containsAnySection(content string, sections ...string) bool {
	for _, section := range sections {
		if containsSection(content, section) {
			return true
		}
	}
	return false
}

func containsSection(content, section string) bool {
	lower := strings.ToLower(content)
	label := strings.ToLower(section)
	return strings.Contains(lower, label+":") || strings.Contains(lower, label+"：")
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = slugRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = slug[:60]
		slug = strings.TrimRight(slug, "-")
	}
	if slug == "" {
		slug = "memory-entry-" + shortNameHash(canonicalName(name))
	}
	return slug
}

func shortNameHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:4])
}

func buildFrontmatter(name, description string, memType contract.MemoryType) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %q\n", name))
	b.WriteString(fmt.Sprintf("description: %q\n", description))
	b.WriteString(fmt.Sprintf("type: %q\n", string(memType)))
	b.WriteString("source: \"explicit\"\n")
	b.WriteString("---\n\n")
	return b.String()
}

func atomicWrite(dir, path, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func rebuildIndex(root string) error {
	entries, err := scanEntries(root)
	if err != nil {
		return err
	}
	unique := uniqueEntries(entries)
	var b strings.Builder
	for _, entry := range unique {
		rel := relativePath(root, entry.filePath)
		hook := hookFromEntry(entry.entry)
		if hook != "" {
			b.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", entry.entry.Name, rel, hook))
		} else {
			b.WriteString(fmt.Sprintf("- [%s](%s)\n", entry.entry.Name, rel))
		}
	}
	indexPath := filepath.Join(root, memoryIndexFileName)
	return os.WriteFile(indexPath, []byte(b.String()), 0o644)
}

func (s *service) prepareRead(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadRequest, string, string, error) {
	prepared := contract.MemoryReadRequest{Name: strings.TrimSpace(req.Name), Path: strings.TrimSpace(req.Path), Scope: sanitizeScope(req.Scope), Type: sanitizeType(req.Type)}
	if prepared.Name == "" && prepared.Path == "" {
		return prepared, "", "", fmt.Errorf("%w: name or path is required", contract.ErrMemoryInvalidParam)
	}
	root, denyReason, err := s.prepareRoot(ctx, prepared.Scope)
	return prepared, root, denyReason, err
}

func (s *service) prepareRoot(ctx context.Context, scope contract.MemoryScope) (string, string, error) {
	if err := s.ensureEnabled(); err != nil {
		return "", "", err
	}
	root, denyReason, err := resolveScopeRoot(ctx, s.cfg, scope)
	if err != nil || denyReason != "" {
		return "", denyReason, err
	}
	return root, authorizeRoot(scope, root), nil
}

func (s *service) ensureEnabled() error {
	if s == nil || s.cfg == nil {
		return contract.ErrFeatureDisabled
	}
	if !s.cfg.Enabled || !s.cfg.EnableTools {
		return contract.ErrFeatureDisabled
	}
	return nil
}

func authorizeRoot(scope contract.MemoryScope, root string) string {
	if root == "" {
		return "deny"
	}
	if scope == contract.MemoryScopeLocal {
		if _, err := os.Stat(filepath.Dir(filepath.Dir(root))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "local_unavailable"
		}
	}
	return ""
}

func sanitizeScope(scope contract.MemoryScope) contract.MemoryScope {
	parsed := contract.ParseMemoryScope(string(scope))
	if parsed.Valid() {
		return parsed
	}
	return contract.MemoryScopeProject
}

func sanitizeType(memoryType contract.MemoryType) contract.MemoryType {
	parsed := contract.ParseMemoryType(string(memoryType))
	if parsed == contract.MemoryTypeUnknown {
		return contract.MemoryTypeUnknown
	}
	return parsed
}

func lookupEntry(root, name, filePath string, indexEntries []indexEntry) (diskEntry, bool, error) {
	if filePath != "" {
		validated, err := validateMemoryWritePath(root, filePath)
		if err != nil {
			return diskEntry{}, false, fmt.Errorf("%w: invalid path", contract.ErrMemoryInvalidParam)
		}
		entry, err := readEntryFile(validated)
		if errors.Is(err, os.ErrNotExist) {
			return diskEntry{}, false, nil
		}
		return entry, indexContains(indexEntries, entry.canonicalName, validated, root), err
	}
	entry, exists, err := findEntryByCanonical(root, canonicalName(name))
	if err != nil || !exists {
		return diskEntry{}, false, err
	}
	return entry, indexContains(indexEntries, entry.canonicalName, entry.filePath, root), nil
}

func indexContains(entries []indexEntry, key, filePath, root string) bool {
	rel := relativePath(root, filePath)
	for _, entry := range entries {
		if entry.canonicalName == key || entry.path == rel {
			return true
		}
	}
	return false
}

func findEntryByCanonical(root, key string) (diskEntry, bool, error) {
	entries, err := scanEntries(root)
	if err != nil {
		return diskEntry{}, false, err
	}
	for _, entry := range uniqueEntries(entries) {
		if entry.canonicalName == key {
			return entry, true, nil
		}
	}
	return diskEntry{}, false, nil
}

func entryToContract(entry diskEntry, root string) contract.MemoryEntry {
	result := entry.entry
	result.SourcePath = relativePath(root, entry.filePath)
	return result
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func applyReadMetadata(result contract.MemoryReadResult, degraded bool, source string) contract.MemoryReadResult {
	if degraded {
		result.Degraded = true
		result.Source = source
	}
	return result
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, contract.ErrFeatureDisabled) ||
		errors.Is(err, contract.ErrMemoryInvalidParam) ||
		errors.Is(err, contract.ErrMemoryPersist) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", contract.ErrMemoryTimedOut, err)
	}
	return err
}
