package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
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

type uiAgentMemoryDeleteParams struct {
	CWD       string `json:"cwd,omitempty"`
	Scope     string `json:"scope,omitempty"`
	AgentType string `json:"agentType"`
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
	// WriteCommittedReadbackFailed is a tri-state flag for write-then-read:
	//   nil   = readback did not occur (read-only paths; field omitted in JSON)
	//   true  = readback ran but failed (containment, broken symlink, IO race);
	//           Content/Path/UpdatedAt are echoed from the request, downstream
	//           consumers (KAIROS, consolidation) MUST re-read from disk before
	//           trusting the snapshot.
	//   false = readback ran and succeeded; snapshot is authoritative.
	// Phase 2.1.AB.6 P1 #3: bool→*bool so save success path can explicitly
	// distinguish itself from read-only paths.
	WriteCommittedReadbackFailed *bool `json:"writeCommittedReadbackFailed,omitempty"`
}

// boolPtr is a shorthand for taking the address of a bool literal,
// used by saveUIAgentMemory to fill the tri-state
// WriteCommittedReadbackFailed field.
func boolPtr(b bool) *bool { return &b }

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
		"ui/memory/agent/get": rpc.StrictHandler(func(ctx context.Context, req uiAgentMemoryGetParams) (UIAgentMemoryDetail, error) {
			return getUIAgentMemory(ctx, p, req)
		}),
		"ui/memory/agent/save": rpc.StrictHandler(func(ctx context.Context, req uiAgentMemorySaveParams) (UIAgentMemoryDetail, error) {
			return saveUIAgentMemory(ctx, p, req)
		}),
		"ui/memory/agent/delete": rpc.StrictHandler(func(ctx context.Context, req uiAgentMemoryDeleteParams) (map[string]any, error) {
			if err := deleteUIAgentMemory(ctx, p, req); err != nil {
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

func getUIAgentMemory(ctx context.Context, deps memoryHandlerDeps, req uiAgentMemoryGetParams) (UIAgentMemoryDetail, error) {
	manager, scope, err := resolveUIAgentManager(deps.Service, req.CWD, req.Scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	detail, err := readUIAgentMemory(deps.Logger, manager, scope, req.AgentType)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	return detail, nil
}

func deleteUIAgentMemory(ctx context.Context, deps memoryHandlerDeps, req uiAgentMemoryDeleteParams) error {
	manager, scope, err := resolveUIAgentManager(deps.Service, req.CWD, req.Scope)
	if err != nil {
		return err
	}
	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" {
		return publicValidationErr("agentType is required")
	}
	entrypoint, err := manager.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return err
	}
	if err := os.Remove(entrypoint); err != nil && !errors.Is(err, os.ErrNotExist) {
		// os.Remove returns *os.PathError whose text embeds the
		// absolute entrypoint path; redact before returning to RPC.
		return redactRPCError(deps.Logger, "agent_memory_delete",
			errAgentMemoryDeleteFailed, err,
			"scope", string(scope), "agent_type", agentType)
	}
	// Best-effort: drop the now-empty agent-type directory so the scope scan
	// no longer surfaces the agent as an existing (empty) entry. Ignore errors
	// here — the MEMORY.md file is already gone, which is the contract.
	if agentDir, err := manager.GetAgentMemoryDir(agentType, scope); err == nil {
		_ = os.Remove(agentDir)
	}
	invalidateAgentMemorySections(deps.Sections)
	return nil
}

func saveUIAgentMemory(ctx context.Context, deps memoryHandlerDeps, req uiAgentMemorySaveParams) (UIAgentMemoryDetail, error) {
	manager, scope, err := resolveUIAgentManager(deps.Service, req.CWD, req.Scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" {
		return UIAgentMemoryDetail{}, publicValidationErr("agentType is required")
	}
	if err := manager.EnsureAgentMemoryDir(agentType, scope); err != nil {
		// EnsureAgentMemoryDir wraps os.MkdirAll which produces
		// *os.PathError with the agent-memory subtree path; redact.
		return UIAgentMemoryDetail{}, redactRPCError(deps.Logger, "agent_memory_ensure_dir",
			errAgentMemorySaveFailed, err,
			"scope", string(scope), "agent_type", agentType)
	}
	entrypoint, err := manager.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	if err := os.WriteFile(entrypoint, []byte(strings.ReplaceAll(req.Content, "\r\n", "\n")), 0o644); err != nil {
		// os.WriteFile returns *os.PathError; redact entrypoint path.
		return UIAgentMemoryDetail{}, redactRPCError(deps.Logger, "agent_memory_save",
			errAgentMemorySaveFailed, err,
			"scope", string(scope), "agent_type", agentType)
	}
	invalidateAgentMemorySections(deps.Sections)
	// Phase 2.1.AB.4: the bytes have been written. If the read-back step
	// fails (containment, broken symlink, IO race), the SAVE itself still
	// succeeded — returning errAgentMemoryReadFailed here would mislead
	// the UI into thinking the write didn't land. Synthesise a minimal
	// detail from the request so the UI reflects what was just saved, and
	// set WriteCommittedReadbackFailed so downstream consumers can tell
	// the difference between "fresh from disk" and "echoed from request".
	// The redacted readback failure is logged at Warn for operator visibility.
	detail, err := readUIAgentMemory(deps.Logger, manager, scope, agentType)
	if err != nil {
		redactRPCError(deps.Logger, "agent_memory_save_readback",
			errAgentMemoryReadFailed, err,
			"scope", string(scope), "agent_type", agentType)
		return UIAgentMemoryDetail{
			Scope:                        string(scope),
			AgentType:                    agentType,
			Path:                         filepath.ToSlash(filepath.Base(filepath.Dir(entrypoint)) + "/" + filepath.Base(entrypoint)),
			Content:                      strings.ReplaceAll(req.Content, "\r\n", "\n"),
			UpdatedAt:                    time.Now().UTC(),
			WriteCommittedReadbackFailed: boolPtr(true),
		}, nil
	}
	// Phase 2.1.AB.6 P1 #3: explicit false on save success path so the
	// tri-state stays distinguishable from nil (read-only paths).
	detail.WriteCommittedReadbackFailed = boolPtr(false)
	return detail, nil
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

func readUIAgentMemory(logger *slog.Logger, manager *AgentMemoryManager, scope MemoryScope, agentType string) (UIAgentMemoryDetail, error) {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return UIAgentMemoryDetail{}, publicValidationErr("agentType is required")
	}
	entrypoint, err := manager.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	scopeRoot, err := manager.GetAgentMemoryScopeRoot(scope)
	if err != nil {
		return UIAgentMemoryDetail{}, err
	}
	// SafeReadEntrypoint enforces symlink-resolved containment under
	// scopeRoot before reading. Phase 2.1.AB.2: route through the
	// shared isUserVisibleNotFound predicate so the boundary rule lives
	// in one place (ui_rpc.go; AB.2 originally placed it in ui_error_policy.go,
	// AB.5 #7 split + folded the predicate back into ui_rpc.go), and redact non-NotFound errors
	// before they cross the JSON-RPC boundary so OS error strings (which
	// embed absolute paths under macOS / Linux) cannot leak the
	// agent-memory layout into the UI's error.message.
	raw, info, err := memshared.SafeReadEntrypoint(scopeRoot, entrypoint)
	var updatedAt time.Time
	switch {
	case err == nil:
		updatedAt = info.ModTime()
	case isUserVisibleNotFound(err):
		raw = nil
	case isContainmentRejection(err):
		// Containment failure is surfaced to the user as "empty" (same
		// shape as NotFound so the editor view stays usable), but it is
		// an attack signal — a symlink resolved outside the agent-memory
		// scope. Log at Warn with the redacted cause so operators see it
		// in observability before the public response swallows it.
		redactRPCError(logger, "agent_memory_read_containment",
			errAgentMemoryReadFailed, err,
			"scope", string(scope), "agent_type", agentType)
		raw = nil
	default:
		return UIAgentMemoryDetail{}, redactRPCError(logger, "agent_memory_read",
			errAgentMemoryReadFailed, err,
			"scope", string(scope), "agent_type", agentType)
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
		contract.DynamicSectionMemoryEntrypoint,
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
