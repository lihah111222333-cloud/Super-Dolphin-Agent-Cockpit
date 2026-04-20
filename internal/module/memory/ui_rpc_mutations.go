package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
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

type uiAgentMemoryGetParams struct {
	CWD       string `json:"cwd,omitempty"`
	Scope     string `json:"scope,omitempty"`
	AgentType string `json:"agentType"`
}

type uiAgentMemorySaveParams struct {
	CWD       string `json:"cwd,omitempty"`
	Scope     string `json:"scope,omitempty"`
	AgentType string `json:"agentType"`
	Content   string `json:"content"`
}

type uiSharedFileGetParams struct {
	Path string `json:"path"`
}

type uiSharedFileDeleteParams struct {
	Path string `json:"path"`
}

type uiSharedFilePromoteParams struct {
	CWD         string `json:"cwd,omitempty"`
	SharedPath  string `json:"sharedPath"`
	Target      string `json:"target,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Content     string `json:"content,omitempty"`
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

type UIAgentMemoryDetail struct {
	Scope     string    `json:"scope"`
	AgentType string    `json:"agentType"`
	Path      string    `json:"path,omitempty"`
	Content   string    `json:"content,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

type UISharedFileDetail struct {
	Path      string    `json:"path"`
	Content   string    `json:"content,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

func registerUIMemoryMutationHandlers(p memoryHandlerDeps) handler.Map {
	return handler.Map{
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
		"ui/memory/agent/get": rpc.StrictHandler(func(ctx context.Context, req uiAgentMemoryGetParams) (UIAgentMemoryDetail, error) {
			return getUIAgentMemory(ctx, p, req)
		}),
		"ui/memory/agent/save": rpc.StrictHandler(func(ctx context.Context, req uiAgentMemorySaveParams) (UIAgentMemoryDetail, error) {
			return saveUIAgentMemory(ctx, p, req)
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
	}
}

func getUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryGetParams) (UIMemoryEntryDetail, error) {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	entry, relPath, err := readUIMemoryEntryByPath(root, target, req.Path)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	return toUIMemoryEntryDetail(target, root, relPath, entry), nil
}

func upsertUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryUpsertParams) (UIMemoryEntryDetail, error) {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	store, err := newDiskStore(root)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	writeReq, err := buildUIWriteRequest(req.Name, req.Description, req.Type, req.Content)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	if err := applyUIMemoryUpsert(store, root, target, req.ExistingPath, writeReq); err != nil {
		return UIMemoryEntryDetail{}, err
	}
	invalidateDurableMemorySections(deps.Sections)
	entry, relPath, err := readUIMemoryEntryByName(root, writeReq.Name)
	if err != nil {
		return UIMemoryEntryDetail{}, err
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
		return errors.New("现有 durable memory 暂不支持改名；如需改名请删除后重建")
	}
	if ParseMemoryType(string(existing.Type())) != writeReq.Type {
		return errors.New("现有 durable memory 暂不支持改类型；如需改类型请删除后重建")
	}
	_, err = store.UpdateStructured(writeReq, WriteOptions{})
	return err
}

func deleteUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryDeleteParams) error {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return err
	}
	entry, _, err := readUIMemoryEntryByPath(root, target, req.Path)
	if err != nil {
		return err
	}
	store, err := newDiskStore(root)
	if err != nil {
		return err
	}
	if err := store.Delete(entry.Frontmatter.Name, WriteOptions{}); err != nil {
		return err
	}
	invalidateDurableMemorySections(deps.Sections)
	return nil
}

func getUIAgentMemory(ctx context.Context, deps memoryHandlerDeps, req uiAgentMemoryGetParams) (UIAgentMemoryDetail, error) {
	manager, scope, err := resolveUIAgentManager(deps.Service, req.CWD, req.Scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	detail, err := readUIAgentMemory(manager, scope, req.AgentType)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	return detail, nil
}

func saveUIAgentMemory(ctx context.Context, deps memoryHandlerDeps, req uiAgentMemorySaveParams) (UIAgentMemoryDetail, error) {
	manager, scope, err := resolveUIAgentManager(deps.Service, req.CWD, req.Scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" {
		return UIAgentMemoryDetail{}, errors.New("agentType is required")
	}
	if err := manager.EnsureAgentMemoryDir(agentType, scope); err != nil {
		return UIAgentMemoryDetail{}, err
	}
	entrypoint, err := manager.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	if err := os.WriteFile(entrypoint, []byte(strings.ReplaceAll(req.Content, "\r\n", "\n")), 0o644); err != nil {
		return UIAgentMemoryDetail{}, err
	}
	invalidateAgentMemorySections(deps.Sections)
	return readUIAgentMemory(manager, scope, agentType)
}

func getUISharedFile(ctx context.Context, deps memoryHandlerDeps, req uiSharedFileGetParams) (UISharedFileDetail, error) {
	if deps.SharedFiles == nil {
		return UISharedFileDetail{}, errors.New("shared file store is not configured")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return UISharedFileDetail{}, errors.New("path is required")
	}
	item, err := deps.SharedFiles.Get(ctx, path)
	if err != nil {
		return UISharedFileDetail{}, err
	}
	return UISharedFileDetail{
		Path:      item.Path,
		Content:   item.Content,
		UpdatedBy: item.UpdatedBy,
		UpdatedAt: item.UpdatedAt,
	}, nil
}

func deleteUISharedFile(ctx context.Context, deps memoryHandlerDeps, req uiSharedFileDeleteParams) (bool, error) {
	if deps.SharedFilesDeleter == nil {
		return false, errors.New("shared file store is not configured for deletion")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return false, errors.New("path is required")
	}
	count, err := deps.SharedFilesDeleter.Delete(ctx, path)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func promoteSharedFileToMemory(ctx context.Context, deps memoryHandlerDeps, req uiSharedFilePromoteParams) (UIMemoryEntryDetail, error) {
	if deps.SharedFiles == nil {
		return UIMemoryEntryDetail{}, errors.New("shared file store is not configured")
	}
	file, err := getUISharedFile(ctx, deps, uiSharedFileGetParams{Path: req.SharedPath})
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = strings.TrimSpace(file.Content)
	}
	return upsertUIMemoryEntry(ctx, deps, uiMemoryEntryUpsertParams{
		CWD:         req.CWD,
		Target:      req.Target,
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		Content:     content,
	})
}

func resolveUIMemoryTargetRoot(ctx context.Context, svc Service, cwd, rawTarget string) (string, string, error) {
	if svc == nil {
		return "", "", errors.New("memory service is not configured")
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
		return "", "", fmt.Errorf("unknown memory target %q", rawTarget)
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
		return MemoryWriteRequest{}, errors.New("type must be one of user|feedback|project|reference")
	}
	req := MemoryWriteRequest{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Type:        memoryType,
		Body:        strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n")),
	}
	if strings.TrimSpace(req.Name) == "" {
		return MemoryWriteRequest{}, errors.New("name is required")
	}
	if strings.TrimSpace(req.Description) == "" {
		return MemoryWriteRequest{}, errors.New("description is required")
	}
	if strings.TrimSpace(req.Body) == "" {
		return MemoryWriteRequest{}, errors.New("content is required")
	}
	return req, nil
}

func readUIMemoryEntryByPath(root, target, relPath string) (MemoryEntry, string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return MemoryEntry{}, "", errors.New("path is required")
	}
	if target == "private" && strings.HasPrefix(filepath.ToSlash(relPath), "team/") {
		return MemoryEntry{}, "", errors.New("private durable memory cannot access team/ paths")
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

func resolveUIAgentManager(svc Service, cwd, rawScope string) (*AgentMemoryManager, MemoryScope, error) {
	if svc == nil {
		return nil, "", errors.New("memory service is not configured")
	}
	cfg := svc.Config()
	if strings.TrimSpace(cwd) != "" {
		cfg.ProjectRoot = strings.TrimSpace(cwd)
	}
	scope := normalizeUIAgentScope(rawScope)
	return NewAgentMemoryManager(&cfg), scope, nil
}

func normalizeUIAgentScope(raw string) MemoryScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(MemoryScopeUser):
		return MemoryScopeUser
	case string(MemoryScopeLocal):
		return MemoryScopeLocal
	default:
		return MemoryScopeProject
	}
}

func readUIAgentMemory(manager *AgentMemoryManager, scope MemoryScope, agentType string) (UIAgentMemoryDetail, error) {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return UIAgentMemoryDetail{}, errors.New("agentType is required")
	}
	entrypoint, err := manager.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	raw, err := os.ReadFile(entrypoint)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return UIAgentMemoryDetail{}, err
		}
		raw = nil
	}
	var updatedAt time.Time
	if info, statErr := os.Stat(entrypoint); statErr == nil {
		updatedAt = info.ModTime()
	}
	return UIAgentMemoryDetail{
		Scope:     string(scope),
		AgentType: agentType,
		Path:      filepath.ToSlash(filepath.Base(filepath.Dir(entrypoint)) + "/" + filepath.Base(entrypoint)),
		Content:   strings.ReplaceAll(string(raw), "\r\n", "\n"),
		UpdatedAt: updatedAt,
	}, nil
}

func invalidateDurableMemorySections(invalidator contract.SectionInvalidator) {
	if invalidator == nil {
		return
	}
	invalidator.InvalidateSections(
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
	)
}

func invalidateAgentMemorySections(invalidator contract.SectionInvalidator) {
	if invalidator == nil {
		return
	}
	invalidator.InvalidateSections(
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionAgentMemory,
	)
}
