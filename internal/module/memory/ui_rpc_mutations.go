package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	sharedfilecleanup "github.com/anthropic-ai/super-agent-v3/internal/module/memory/sharedfilecleanup"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/similarity"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
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

// registerUIMemoryMutationHandlers 注册UI记忆mutation处理器。
func registerUIMemoryMutationHandlers(p memoryHandlerDeps) handler.Map {
	out := handler.Map{
		"ui/memory/entry/get": platformrpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryGetParams) (UIMemoryEntryDetail, error) {
			return getUIMemoryEntry(ctx, p, req)
		}),
		"ui/memory/entry/upsert": platformrpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryUpsertParams) (UIMemoryEntryDetail, error) {
			return upsertUIMemoryEntry(ctx, p, req)
		}),
		"ui/memory/entry/delete": platformrpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryDeleteParams) (map[string]any, error) {
			if err := deleteUIMemoryEntry(ctx, p, req); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true}, nil
		}),
		"ui/memory/shared-file/get": platformrpc.StrictHandler(func(ctx context.Context, req uiSharedFileGetParams) (UISharedFileDetail, error) {
			return getUISharedFile(ctx, p, req)
		}),
		"ui/memory/shared-file/delete": platformrpc.StrictHandler(func(ctx context.Context, req uiSharedFileDeleteParams) (map[string]any, error) {
			deleted, err := deleteUISharedFile(ctx, p, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{"deleted": deleted}, nil
		}),
		"ui/memory/shared-file/cleanup-preview": platformrpc.StrictHandler(func(ctx context.Context, req sharedfilecleanup.Params) (sharedfilecleanup.Result, error) {
			return sharedfilecleanup.Preview(ctx, sharedFileCleanupDeps(p), req)
		}),
		"ui/memory/shared-file/cleanup-apply": platformrpc.StrictHandler(func(ctx context.Context, req sharedfilecleanup.Params) (sharedfilecleanup.Result, error) {
			return sharedfilecleanup.Apply(ctx, sharedFileCleanupDeps(p), req)
		}),
		"ui/memory/auto-dream/set-intent": platformrpc.StrictHandler(func(ctx context.Context, req uiAutoDreamIntentParams) (map[string]any, error) {
			return setAutoDreamIntent(ctx, p, req)
		}),
		"ui/memory/entry/merge": platformrpc.StrictHandler(func(ctx context.Context, req uiMemoryEntryMergeParams) (UIMemoryEntryDetail, error) {
			return mergeUIMemoryEntries(ctx, p, req)
		}),
		"ui/memory/similarity/ignore": platformrpc.StrictHandler(func(ctx context.Context, req uiSimilarityIgnoreParams) (map[string]any, error) {
			return ignoreSimilarityPairHandler(ctx, p, req)
		}),
		"ui/memory/similarity/consolidate-all": platformrpc.StrictHandler(func(ctx context.Context, req uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllResult, error) {
			return consolidateAllHandler(ctx, p, req)
		}),
		"ui/memory/similarity/consolidate-all/start": platformrpc.StrictHandler(func(ctx context.Context, req uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllStartResult, error) {
			return startConsolidateAllHandler(ctx, p, req)
		}),
		"ui/memory/similarity/consolidate-all/status": platformrpc.StrictHandler(func(ctx context.Context, req uiSimilarityConsolidateAllStatusParams) (uiSimilarityConsolidateAllStatusResult, error) {
			return statusConsolidateAllHandler(ctx, p, req)
		}),
	}
	return out
}

func sharedFileCleanupDeps(p memoryHandlerDeps) sharedfilecleanup.Deps {
	return sharedfilecleanup.Deps{
		Reader:     p.SharedFiles,
		Deleter:    p.SharedFilesDeleter,
		DAGRuntime: sharedFileDeleteGuardRuntime(p),
	}
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
	publishUIMemoryChanged(p, "auto-dream-intent")
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

// upsertUIMemoryEntry 处理upsertUI记忆条目。
func upsertUIMemoryEntry(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryUpsertParams) (UIMemoryEntryDetail, error) {
	root, target, err := resolveUIMemoryTargetRoot(ctx, deps.Service, req.CWD, req.Target)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "durable_memory_resolve_root",
			errDurableMemorySaveFailed, err, "target", req.Target)
	}
	store, err := newUIMemoryMutationStore(deps.Service, root, target)
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
	publishUIMemoryChanged(deps, "upsert")
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

	store, err := newUIMemoryMutationStore(deps.Service, root, target)
	if err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_open_store",
			errDurableMemoryDeleteFailed, err, "target", target)
	}

	if err := store.DeletePath(req.Path, WriteOptions{}); err != nil {
		return redactIfPathBearing(deps.Logger, "durable_memory_delete",
			errDurableMemoryDeleteFailed, err, "target", target, "path", req.Path)
	}

	invalidateDurableMemorySections(deps.Sections)
	publishUIMemoryChanged(deps, "delete")
	return nil
}

func deleteAbsorbedEntry(svc Service, root, target, path string) error {
	store, err := newUIMemoryMutationStore(svc, root, target)
	if err != nil {
		return err
	}
	return store.DeletePath(path)
}

func newUIMemoryMutationStore(svc Service, root, target string) (*diskStore, error) {
	var cfg Config
	var locks *diskLockCoordinator
	if svc != nil {
		cfg = svc.Config()
		locks = svc.MemoryCoordinator()
	}
	if target == "team" {
		return newDiskStoreWithGuard(root, NewTeamMemoryGuard(NewTeamMemoryManager(&cfg)), locks)
	}
	return newDiskStore(root, locks)
}

func rollbackMergedEntry(svc Service, root, target, path string, entry MemoryEntry) error {
	store, err := newUIMemoryMutationStore(svc, root, target)
	if err != nil {
		return err
	}

	_, err = store.updatePath(path, entry, WriteOptions{})
	return err
}

// resolveUIMemoryTargetRoot 解析UI记忆target根目录。
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

// readUIMemoryEntryByPath 按路径读取UI记忆条目。
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
	// MergedDescription/MergedContent: 可选 LLM 整合输出。两者同时非空时覆盖默认的
	// dedup.MergeContent 字面融合行为，写入 keep 侧 entry。任一为空走旧路径。
	MergedDescription string `json:"mergedDescription,omitempty"`
	MergedContent     string `json:"mergedContent,omitempty"`
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

// mergeUIMemoryEntries 合并UI记忆条目。
func mergeUIMemoryEntries(ctx context.Context, deps memoryHandlerDeps, req uiMemoryEntryMergeParams) (UIMemoryEntryDetail, error) {
	resolved, err := resolveUIMemoryMergeEntries(ctx, deps, req)
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	if err := validateUIMemoryMergePair(resolved.entryA, resolved.entryB); err != nil {
		return UIMemoryEntryDetail{}, err
	}

	writeReq := buildUIMemoryMergeWriteRequest(resolved.entryA, resolved.entryB, req.MergedDescription, req.MergedContent)
	storeA, err := newUIMemoryMutationStore(deps.Service, resolved.rootA, resolved.targetA)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_open_store_a",
			errDurableMemorySaveFailed, err, "target", resolved.targetA)
	}

	if _, err := storeA.UpdateStructuredPath(req.PathA, writeReq); err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_write_a",
			errDurableMemorySaveFailed, err, "target", resolved.targetA, "path", req.PathA)
	}

	if err := deleteAbsorbedEntry(deps.Service, resolved.rootB, resolved.targetB, req.PathB); err != nil {
		_ = rollbackMergedEntry(deps.Service, resolved.rootA, resolved.targetA, req.PathA, resolved.entryA)
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_delete_b",
			errDurableMemoryDeleteFailed, err, "target", resolved.targetB, "path", req.PathB)
	}

	invalidateDurableMemorySections(deps.Sections)

	merged, mergedPath, err := readUIMemoryEntryByPath(resolved.rootA, resolved.targetA, req.PathA)
	if err != nil {
		return UIMemoryEntryDetail{}, redactIfPathBearing(deps.Logger, "merge_read_back",
			errDurableMemoryReadFailed, err, "target", resolved.targetA, "path", req.PathA)
	}
	publishUIMemoryChanged(deps, "merge")
	return toUIMemoryEntryDetail(resolved.targetA, resolved.rootA, mergedPath, merged), nil
}

type uiSimilarityIgnoreParams struct {
	CWD     string `json:"cwd,omitempty"`
	TargetA string `json:"targetA"`
	PathA   string `json:"pathA"`
	TargetB string `json:"targetB"`
	PathB   string `json:"pathB"`
}

// uiSimilarityConsolidateAllParams 是 ui/memory/similarity/consolidate-all RPC 入参。
type uiSimilarityConsolidateAllParams struct {
	CWD           string `json:"cwd,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
}

func (p uiSimilarityConsolidateAllParams) dreamOptions() contract.DreamOptions {
	return contract.DreamOptions{
		Provider:      strings.TrimSpace(p.Provider),
		Model:         strings.TrimSpace(p.Model),
		ModelProvider: strings.TrimSpace(p.ModelProvider),
	}
}

// uiSimilarityConsolidateAllResult 是 RPC 出参（字段语义与 similarity.ConsolidateResult 对齐）。
type uiSimilarityConsolidateAllResult struct {
	Merged  int      `json:"merged"`
	Ignored int      `json:"ignored"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

// ignoreSimilarityPairHandler 是 ui/memory/similarity/ignore 的 RPC 入口。
// 实际持久化逻辑在 similarity 子包；本函数负责参数校验 + 错误 redact。
func ignoreSimilarityPairHandler(ctx context.Context, p memoryHandlerDeps, req uiSimilarityIgnoreParams) (map[string]any, error) {
	if p.Service == nil {
		return nil, errors.New("memory service is not configured")
	}
	if strings.TrimSpace(req.PathA) == "" || strings.TrimSpace(req.PathB) == "" {
		return nil, publicValidationErr("pathA and pathB are required")
	}
	// M3: 规范化 target 值，防外部脚本通过非标 (e.g. "Private") 写入永不命中的 ignored key。
	targetA := normalizeUIMemoryTarget(req.TargetA)
	targetB := normalizeUIMemoryTarget(req.TargetB)
	adapter := newSimilarityAdapter(p)
	if err := similarity.IgnorePair(ctx, adapter, req.CWD, targetA, req.PathA, targetB, req.PathB); err != nil {
		return nil, redactIfPathBearing(p.Logger, "similarity_ignore",
			errDurableMemorySaveFailed, err)
	}
	key := similarity.IgnoreKey(targetA, req.PathA, targetB, req.PathB)
	publishUIMemoryChanged(p, "ignore-similarity")
	return map[string]any{"ignored": true, "key": key}, nil
}

// consolidateAllHandler 是 ui/memory/similarity/consolidate-all 的 RPC 入口。
// 主流程在 similarity 子包；本函数负责 ErrDreamExecutorNotConfigured 哨兵透传 + 路径 redact。
func consolidateAllHandler(ctx context.Context, p memoryHandlerDeps, req uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllResult, error) {
	result, err := runConsolidateAll(ctx, p, req)
	if err != nil {
		return uiSimilarityConsolidateAllResult{}, err
	}
	publishUIMemoryChanged(p, "consolidate-similarities")
	return result, nil
}

func runConsolidateAll(ctx context.Context, p memoryHandlerDeps, req uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllResult, error) {
	if p.Service == nil {
		return uiSimilarityConsolidateAllResult{}, errors.New("memory service is not configured")
	}
	adapter := newSimilarityAdapter(p, req.dreamOptions())
	res, err := similarity.ConsolidateAll(ctx, adapter, req.CWD)
	if err != nil {
		if publicErr := publicConsolidateAllError(err); publicErr != nil {
			return uiSimilarityConsolidateAllResult{}, publicErr
		}
		return uiSimilarityConsolidateAllResult{}, redactIfPathBearing(p.Logger, "consolidate_all",
			errDurableMemorySaveFailed, err)
	}
	return uiSimilarityConsolidateAllResult{
		Merged: res.Merged, Ignored: res.Ignored,
		Failed: res.Failed, Skipped: res.Skipped,
		Errors: res.Errors,
	}, nil
}

func publicConsolidateAllError(err error) error {
	if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		return err
	}
	if errors.Is(err, ErrTeamMemSecretDetected) {
		return ErrTeamMemSecretDetected
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return publicValidationErr("智能整合耗时过长，请稍后重试")
	}
	if errors.Is(err, similarity.ErrLLMConsolidate) {
		return publicValidationErr("智能整合调用模型失败，请检查当前模型配置后重试")
	}
	return nil
}

func publishUIMemoryChanged(deps memoryHandlerDeps, action string) {
	if deps.Dispatcher == nil {
		return
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return
	}
	contract.NewEmitter[uidto.UIMemoryChanged](deps.Dispatcher)(uidto.UIMemoryChanged{
		EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
		Action:      action,
	})
}

func buildUIMemoryMergeWriteRequest(entryA, entryB MemoryEntry, overrideDescription, overrideContent string) MemoryWriteRequest {
	// LLM 整合 override 优先：两个字段同时非空才采用，避免半空 LLM 输出污染 entry。
	overrideDescription = strings.TrimSpace(overrideDescription)
	overrideContent = strings.TrimSpace(overrideContent)
	if overrideDescription != "" && overrideContent != "" {
		return MemoryWriteRequest{
			Name:        entryA.Frontmatter.Name,
			Description: overrideDescription,
			Type:        ParseMemoryType(string(entryA.Type())),
			Body:        overrideContent,
		}
	}
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
