package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	writeReq, err := buildUIWriteRequest(req.Name, req.Description, req.Type, req.Content)
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
	_, err = store.UpdateStructured(writeReq, WriteOptions{})
	return err
}

func deleteUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryDeleteParams) error {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_resolve_root",
			errDurableMemoryDeleteFailed, err, "target", req.Target)
	}
	entry, _, err := readUIMemoryEntryByPath(root, target, req.Path)
	if err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_read",
			errDurableMemoryDeleteFailed, err, "target", target, "path", req.Path)
	}
	store, err := newDiskStore(root)
	if err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_open_store",
			errDurableMemoryDeleteFailed, err, "target", target)
	}
	if err := store.Delete(entry.Frontmatter.Name, WriteOptions{}); err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_delete",
			errDurableMemoryDeleteFailed, err, "target", target, "name", entry.Frontmatter.Name)
	}
	invalidateDurableMemorySections(deps.Sections)
	return nil
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

func buildUIWriteRequest(name, description, rawType, content string) (MemoryWriteRequest, error) {
	memoryType := ParseMemoryType(rawType)
	if !memoryType.IsKnown() {
		return MemoryWriteRequest{}, publicValidationErr("type must be one of user|feedback|project|reference")
	}
	req := MemoryWriteRequest{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Type:        memoryType,
		Body:        strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n")),
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
	if target == "private" && strings.HasPrefix(filepath.ToSlash(relPath), "team/") {
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
