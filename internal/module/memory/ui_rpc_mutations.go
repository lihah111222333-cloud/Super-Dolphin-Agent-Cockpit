package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
)

type uiMemoryEntryGetParams struct {
	CWD    string `json:"cwd,omitempty"`
	Target string `json:"target,omitempty"`
	Path   string `json:"path"`
}

type uiMemoryEntryUpsertParams struct {
	CWD          string `json:"cwd,omitempty"`
	Target       string `json:"target,omitempty"`
	ExistingPath string `json:"existingPath,omitempty"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	Content      string `json:"content"`
	Title        string `json:"title,omitempty"`
}

type uiMemoryEntryDeleteParams struct {
	CWD    string `json:"cwd,omitempty"`
	Target string `json:"target,omitempty"`
	Path   string `json:"path"`
}

type UIMemoryEntryDetail struct {
	Target      string    `json:"target,omitempty"`
	Path        string    `json:"path,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Type        string    `json:"type,omitempty"`
	Content     string    `json:"content,omitempty"`
	Title       string    `json:"title,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

func registerUIMemoryMutationHandlers(p memoryHandlerDeps) handler.Map {
	out := handler.Map{
		"ui/memory/entry/get": rpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryGetParams) (UIMemoryEntryDetail, error) {
			return getUIMemoryEntry(ctx, p, req)
		}),
		"ui/memory/entry/upsert": rpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryUpsertParams) (UIMemoryEntryDetail, error) {
			return upsertUIMemoryEntry(ctx, p, req)
		}),
		"ui/memory/entry/delete": rpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryDeleteParams) (map[string]any, error) {
			if err := deleteUIMemoryEntry(ctx, p, req); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true}, nil
		}),
		"ui/memory/shared-file/get": rpc.StrictHandler(func(ctx context.Context, req uiSharedFileGetParams) (UISharedFileDetail, error) {
			return getUISharedFile(ctx, p, req)
		}),
		"ui/memory/shared-file/promote": rpc.StrictHandler(func(ctx context.Context, req uiSharedFilePromoteParams) (UIMemoryEntryDetail, error) {
			return promoteSharedFileToMemory(ctx, p, req)
		}),
		"ui/memory/shared-file/delete": rpc.StrictHandler(func(ctx context.Context, req uiSharedFileDeleteParams) (map[string]any, error) {
			deleted, err := deleteUISharedFile(ctx, p, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{"deleted": deleted}, nil
		}),
		"ui/memory/auto-dream/set-intent": rpc.StrictHandler(func(ctx context.Context, req uiAutoDreamIntentParams) (map[string]any, error) {
			return setAutoDreamIntent(ctx, p, req)
		}),
		"ui/memory/entry/merge": rpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryMergeParams) (UIMemoryEntryDetail, error) {
			return mergeUIMemoryEntries(ctx, p, req)
		}),
	}
	for name, item := range registerAutoContinueStateHandlers(p) {
		out[name] = item
	}
	return out
}

type uiAutoDreamIntentParams struct {
	Enabled bool `json:"enabled"`
}

func setAutoDreamIntent(_ context.Context, p memoryHandlerDeps, req uiAutoDreamIntentParams) (map[string]any, error) {
	if p.Service == nil {
		return nil, errors.New("memory service is not configured")
	}
	rootDir := strings.TrimSpace(p.Service.Config().RootDir)
	if rootDir == "" {
		return nil, errors.New("memory root dir is empty")
	}
	if err := WriteAutoDreamIntent(rootDir, req.Enabled); err != nil {
		return nil, fmt.Errorf("persist auto-dream intent: %w", err)
	}
	return map[string]any{"ok": true, "enabled": req.Enabled}, nil
}

func getUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryGetParams) (UIMemoryEntryDetail, error) {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_resolve_root",
			errDurableMemoryReadFailed, err, "target", req.Target)
	}
	entry, relPath, err := readUIMemoryEntryByPath(root, target, req.Path)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_read",
			errDurableMemoryReadFailed, err, "target", target, "path", req.Path)
	}
	return toUIMemoryEntryDetail(target, root, relPath, entry), nil
}

func upsertUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryUpsertParams) (UIMemoryEntryDetail, error) {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_resolve_root",
			errDurableMemorySaveFailed, err, "target", req.Target)
	}
	store, err := newDiskStore(root)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_open_store",
			errDurableMemorySaveFailed, err, "target", target)
	}
	writeReq, err := buildUIWriteRequest(req.Name, req.Description, req.Type, req.Content, req.Title)
	if err != nil {
		return UIMemoryEntryDetail{}, err // pure validation, no path
	}
	if err := applyUIMemoryUpsert(store, root, target, req.ExistingPath, writeReq); err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_upsert",
			errDurableMemorySaveFailed, err, "target", target, "name", writeReq.Name)
	}
	invalidateDurableMemorySections(deps.Sections)
	entry, relPath, err := readUIMemoryEntryByName(root, writeReq.Name)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_read_back",
			errDurableMemoryReadFailed, err, "target", target, "name", writeReq.Name)
	}
	return toUIMemoryEntryDetail(target, root, relPath, entry), nil
}

func applyUIMemoryUpsert(store *diskStore, root, target, existingPath string, writeReq MemoryWriteRequest) error {
	if strings.TrimSpace(existingPath) == "" {
		_, err := store.CreateStructured(writeReq, WriteOptions{})
		return err
	}
	existing, _, err := readUIMemoryEntryByPath(root, target, existingPath)
	if err != nil {
		return err
	}
	if existing.CanonicalName != CanonicalName(writeReq.Name) {
		return publicValidationErr("现有 durable memory 暂不支持改名；如需改名请删除后重建")
	}
	if ParseMemoryType(string(existing.Type())) != writeReq.Type {
		return publicValidationErr("现有 durable memory 暂不支持改类型；如需改类型请删除后重建")
	}
	_, err = store.UpdateStructuredPath(existingPath, writeReq, WriteOptions{})
	return err
}

func deleteUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryDeleteParams) error {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_resolve_root",
			errDurableMemoryDeleteFailed, err, "target", req.Target)
	}
	if _, _, err := readUIMemoryEntryByPath(root, target, req.Path); err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_read",
			errDurableMemoryDeleteFailed, err, "target", target, "path", req.Path)
	}

	store, err := newDiskStore(root)
	if err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_open_store",
			errDurableMemoryDeleteFailed, err, "target", target)
	}
	if err := store.DeletePath(req.Path, WriteOptions{}); err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_delete",
			errDurableMemoryDeleteFailed, err, "target", target, "path", req.Path)
	}

	invalidateDurableMemorySections(deps.Sections)
	return nil
}

func deleteAbsorbedEntry(root, path string) error {
	store, err := newDiskStore(root)
	if err != nil {
		return err
	}
	return store.DeletePath(path)
}

func newUIMemoryMutationStore(cfg *Config, root, target string) (*diskStore, error) {
	if target == "team" {
		return newDiskStoreWithGuard(root, NewTeamMemoryGuard(NewTeamMemoryManager(cfg)))
	}
	return newDiskStore(root)
}

func rollbackMergedEntry(cfg *Config, root, target, path string, entry MemoryEntry) error {
	store, err := newUIMemoryMutationStore(cfg, root, target)
	if err != nil {
		return err
	}
	_, err = store.updatePath(path, entry, WriteOptions{})
	return err
}

func resolveUIMemoryTargetRoot(ctx context.Context, svc Service, cwd, rawTarget string) (string, string, error) {
	if svc == nil {
		return "", "", publicValidationErr("memory service is not configured")
	}
	cfg := svc.Config()
	projectRoot := strings.TrimSpace(cwd)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	target := normalizeUIMemoryTarget(rawTarget)
	switch target {
	case "private":
		root, err := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
		return root, target, err
	case "team":
		if !teamMemoryConfigured(cfg) {
			return "", "", publicValidationErr("team memory is not enabled")
		}
		buildCtx := contract.BuildCtx{CWD: projectRoot}

		if gitRoot, err := FindCanonicalGitRoot(ctx, projectRoot); err == nil && strings.TrimSpace(gitRoot) != "" {
			buildCtx.GitRoot = strings.TrimSpace(gitRoot)
		}
		root, err := configuredTeamMemRoot(&cfg, buildCtx)
		return root, target, err
	default:
		return "", "", publicValidationErr(fmt.Sprintf("unknown memory target %q", rawTarget))
	}
}

func normalizeUIMemoryTarget(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "team":
		return "team"
	default:
		return "private"
	}
}

func buildUIWriteRequest(name, description, rawType, content, title string) (MemoryWriteRequest, error) {
	memoryType := ParseMemoryType(rawType)
	if !memoryType.IsKnown() {
		return MemoryWriteRequest{}, publicValidationErr("type must be one of user|feedback|project|reference")
	}
	req := MemoryWriteRequest{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Type:        memoryType,
		Body:        strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n")),
		Title:       strings.TrimSpace(title),
	}
	if strings.TrimSpace(req.Name) == "" {
		return MemoryWriteRequest{}, publicValidationErr("name is required")
	}
	if strings.TrimSpace(req.Description) == "" {
		return MemoryWriteRequest{}, publicValidationErr("description is required")
	}
	if strings.TrimSpace(req.Body) == "" {
		return MemoryWriteRequest{}, publicValidationErr("content is required")
	}
	return req, nil
}

func readUIMemoryEntryByPath(root, target, relPath string) (MemoryEntry, string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return MemoryEntry{}, "", publicValidationErr("path is required")
	}
	slashPath := filepath.ToSlash(relPath)
	if filepath.Base(slashPath) == memoryIndexFileName {
		return MemoryEntry{}, "", ErrInvalidMemoryReadPath
	}
	if target == "private" && strings.HasPrefix(slashPath, "team/") {

		return MemoryEntry{}, "", publicValidationErr("private durable memory cannot access team/ paths")
	}
	validated, err := ValidateMemoryReadPath(root, relPath)
	if err != nil {
		return MemoryEntry{}, "", err
	}
	entry, err := readMemoryEntryFile(validated)
	if err != nil {
		return MemoryEntry{}, "", err
	}
	display := memoryEntryDisplayPath(root, validated)
	return entry, display, nil
}

func readUIMemoryEntryByName(root, name string) (MemoryEntry, string, error) {
	entry, exists, err := findMemoryEntry(root, CanonicalName(name))
	if err != nil {
		return MemoryEntry{}, "", err
	}
	if !exists {
		return MemoryEntry{}, "", ErrMemoryNotFound
	}
	return entry, memoryEntryDisplayPath(root, entry.FilePath), nil
}

func toUIMemoryEntryDetail(target, root, path string, entry MemoryEntry) UIMemoryEntryDetail {
	return UIMemoryEntryDetail{
		Target:      target,
		Path:        firstNonEmptyUI(path, memoryEntryDisplayPath(root, entry.FilePath)),
		Name:        strings.TrimSpace(entry.Frontmatter.Name),
		Description: strings.TrimSpace(entry.Frontmatter.Description),
		Type:        strings.TrimSpace(string(entry.Type())),
		Content:     strings.TrimSpace(entry.Content),
		Title:       strings.TrimSpace(entry.Frontmatter.Title),
		UpdatedAt:   entry.UpdatedAt,
	}
}

func invalidateDurableMemorySections(invalidator contract.SectionInvalidator) {
	if invalidator == nil {
		return
	}
	invalidator.InvalidateSections(
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)
}

func validateUIMemoryMergePair(entryA, entryB MemoryEntry) error {
	if ParseMemoryType(string(entryA.Type())) != ParseMemoryType(string(entryB.Type())) {
		return publicValidationErr("只能整合同类型记忆")
	}
	score := dedup.Containment(
		dedup.Bigrams(dedup.Normalize(entryA.Content)),
		dedup.Bigrams(dedup.Normalize(entryB.Content)),
	)
	if score < dedup.MinMergePairContainment {
		return publicValidationErr("两条记忆相似度不足，无法整合")
	}
	return nil
}

type uiMemoryEntryMergeParams struct {
	CWD     string `json:"cwd,omitempty"`
	TargetA string `json:"targetA"` // "private" or "team"
	PathA   string `json:"pathA"`   // kept entry
	TargetB string `json:"targetB"`
	PathB   string `json:"pathB"` // absorbed entry (deleted after merge)
}

type uiMemoryMergeResolved struct {
	rootA   string
	rootB   string
	targetA string
	targetB string
	entryA  MemoryEntry
	entryB  MemoryEntry
}

func resolveUIMemoryMergeEntries(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryMergeParams) (uiMemoryMergeResolved, error) {
	rootA, targetA, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.TargetA)
	if err != nil {
		return uiMemoryMergeResolved{}, redactIfPathBearing(deps.Logger, "merge_resolve_root_a",
			errDurableMemorySaveFailed, err, "target", req.TargetA)
	}
	rootB, targetB, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.TargetB)
	if err != nil {
		return uiMemoryMergeResolved{}, redactIfPathBearing(deps.Logger, "merge_resolve_root_b",
			errDurableMemorySaveFailed, err, "target", req.TargetB)
	}
	entryA, _, err := readUIMemoryEntryByPath(rootA, targetA, req.PathA)
	if err != nil {
		return uiMemoryMergeResolved{}, redactIfPathBearing(deps.Logger, "merge_read_a",
			errDurableMemoryReadFailed, err, "target", targetA, "path", req.PathA)
	}
	entryB, _, err := readUIMemoryEntryByPath(rootB, targetB, req.PathB)
	if err != nil {
		return uiMemoryMergeResolved{}, redactIfPathBearing(deps.Logger, "merge_read_b",
			errDurableMemoryReadFailed, err, "target", targetB, "path", req.PathB)
	}
	return uiMemoryMergeResolved{rootA: rootA, rootB: rootB, targetA: targetA, targetB: targetB, entryA: entryA, entryB: entryB}, nil
}

func mergeUIMemoryEntries(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryMergeParams) (UIMemoryEntryDetail, error) {
	resolved, err := resolveUIMemoryMergeEntries(ctx, deps, req)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	if err := validateUIMemoryMergePair(resolved.entryA, resolved.entryB); err != nil {
		return UIMemoryEntryDetail{}, err
	}

	writeReq := buildUIMemoryMergeWriteRequest(resolved.entryA, resolved.entryB)
	cfg := deps.Service.Config()
	storeA, err := newUIMemoryMutationStore(&cfg, resolved.rootA, resolved.targetA)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_open_store_a",
			errDurableMemorySaveFailed, err, "target", resolved.targetA)
	}
	if _, err := storeA.UpdateStructuredPath(req.PathA, writeReq); err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_write_a",
			errDurableMemorySaveFailed, err, "target", resolved.targetA, "path", req.PathA)
	}

	if err := deleteAbsorbedEntry(resolved.rootB, req.PathB); err != nil {
		_ = rollbackMergedEntry(&cfg, resolved.rootA, resolved.targetA, req.PathA, resolved.entryA)
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_delete_b",
			errDurableMemoryDeleteFailed, err, "target", resolved.targetB, "path", req.PathB)
	}

	invalidateDurableMemorySections(deps.Sections)

	merged, mergedPath, err := readUIMemoryEntryByPath(resolved.rootA, resolved.targetA, req.PathA)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_read_back",
			errDurableMemoryReadFailed, err, "target", resolved.targetA, "path", req.PathA)
	}
	return toUIMemoryEntryDetail(resolved.targetA, resolved.rootA, mergedPath, merged), nil
}

func buildUIMemoryMergeWriteRequest(entryA, entryB MemoryEntry) MemoryWriteRequest {
	mergedDesc := entryA.Frontmatter.Description
	if len(entryB.Frontmatter.Description) > len(mergedDesc) {
		mergedDesc = entryB.Frontmatter.Description
	}
	return MemoryWriteRequest{
		Name:        entryA.Frontmatter.Name,
		Description: mergedDesc,
		Type:        ParseMemoryType(string(entryA.Type())),
		Body:        dedup.MergeContent(string(entryA.Type()), entryA.Content, entryB.Content),
	}
}
