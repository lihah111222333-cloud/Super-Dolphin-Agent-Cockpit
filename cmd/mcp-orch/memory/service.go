package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func (s *service) prepareRead(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadRequest, string, string, error) {
	prepared := contract.MemoryReadRequest{Name: strings.TrimSpace(req.Name), Path: strings.TrimSpace(req.Path), Scope: sanitizeScope(req.Scope), Type: sanitizeType(req.Type)}
	if prepared.Name == "" && prepared.Path == "" {
		return prepared, "", "", fmt.Errorf("%w: name or path is required", contract.ErrMemoryInvalidParam)
	}
	root, denyReason, err := s.prepareRoot(ctx, prepared.Scope)
	return prepared, root, denyReason, err
}

func (s *service) prepareRoot(ctx context.Context, scope contract.MemoryScope) (string, string, error) {
	if !scope.Valid() {
		return "", "deny", fmt.Errorf("%w: unsupported scope", contract.ErrMemoryInvalidParam)
	}
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
	switch strings.ToLower(strings.TrimSpace(string(scope))) {
	case "":
		return contract.MemoryScopeProject
	case string(contract.MemoryScopeProject):
		return contract.MemoryScopeProject
	case string(contract.MemoryScopeUser):
		return contract.MemoryScopeUser
	case string(contract.MemoryScopeLocal):
		return contract.MemoryScopeLocal
	default:
		return contract.MemoryScope("")
	}
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
