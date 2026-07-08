package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	sharedfilecleanup "github.com/anthropic-ai/super-agent-v3/internal/module/memory/sharedfilecleanup"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/similarity"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
)

// uiMemoryEntryGetParams 是记忆详情读取 RPC 的入参，Target 为空时按私有记忆处理。
type uiMemoryEntryGetParams struct {
	CWD    string `json:"cwd,omitempty"`
	Target string `json:"target,omitempty"`
	Path   string `json:"path"`
}

// uiMemoryEntryUpsertParams 是记忆创建/更新 RPC 的 wire 入参；ExistingPath 非空表示更新原条目。
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

// uiMemoryEntryDeleteParams 是记忆删除 RPC 的入参，Path 会按 target 根目录重新校验。
type uiMemoryEntryDeleteParams struct {
	CWD    string `json:"cwd,omitempty"`
	Target string `json:"target,omitempty"`
	Path   string `json:"path"`
}

// UIMemoryEntryDetail 是编辑器详情页使用的响应结构，字段同时兼容读取、创建、合并后的回包。
type UIMemoryEntryDetail struct {
	Target      string    `json:"target,omitempty"`
	Path        string    `json:"path,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Type        string    `json:"type,omitempty"`
	Content     string    `json:"content,omitempty"`
	Title       string    `json:"title,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitzero"`
}

// registerUIMemoryMutationHandlers 注册记忆中心所有写入类 RPC，并把 handler 统一挂到 StrictHandler。
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

// sharedFileCleanupDeps 收窄 shared file GC 所需依赖，删除保护复用详情删除的 DAG runtime 选择逻辑。
func sharedFileCleanupDeps(p memoryHandlerDeps) sharedfilecleanup.Deps {
	return sharedfilecleanup.Deps{
		Reader:     p.SharedFiles,
		Deleter:    p.SharedFilesDeleter,
		DAGRuntime: p.DAGRuntime,
	}
}

// uiAutoDreamIntentParams 是自动 dream 开关的持久化入参。
// CWD 必须来自当前项目，用于解析对应项目的记忆根，避免跨项目写错 intent 文件。
type uiAutoDreamIntentParams struct {
	CWD     string `json:"cwd"`
	Enabled bool   `json:"enabled"`
}

// setAutoDreamIntent 保存当前项目的 auto-dream 意图；memory service 或 root 缺失时 fail-fast。
func setAutoDreamIntent(ctx context.Context, p memoryHandlerDeps, req uiAutoDreamIntentParams) (map[string]any, error) {
	if p.Service == nil {
		return nil, errors.New("memory service is not configured")
	}
	projectRoot := strings.TrimSpace(req.CWD)
	if projectRoot == "" {
		return nil, publicValidationErr("cwd is required")
	}
	rootDir, _, err := resolveUIMemoryTargetRoot(ctx, p.Service, projectRoot, "private")
	if err != nil {
		return nil, err
	}
	if rootDir == "" {
		return nil, errors.New("memory root dir is empty")
	}
	if err := WriteAutoDreamIntent(rootDir, req.Enabled); err != nil {
		return nil, fmt.Errorf("persist auto-dream intent: %w", err)
	}
	publishUIMemoryChanged(p, "auto-dream-intent")
	return map[string]any{"ok": true, "enabled": req.Enabled}, nil
}

// getUIMemoryEntry 读取单条记忆详情，并在根目录解析或读文件失败时按 UI RPC 规则脱敏错误。
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

// upsertUIMemoryEntry 创建或更新 UI durable memory 条目，写入后失效 prompt 记忆 section 并发布变更事件。
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

// applyUIMemoryUpsert 执行实际写盘；更新时禁止改名和改类型，避免旧路径与索引语义漂移。
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

// deleteUIMemoryEntry 先确认目标可读再删除，确保路径错误按读取/删除阶段分别脱敏记录。
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

// deleteAbsorbedEntry 删除合并后被吸收的一侧记忆，复用 target 对应的 guard 和锁。
func deleteAbsorbedEntry(svc Service, root, target, path string) error {
	store, err := newUIMemoryMutationStore(svc, root, target)
	if err != nil {
		return err
	}
	return store.DeletePath(path)
}

// newUIMemoryMutationStore 为 UI 写入创建 diskStore；team target 会挂团队记忆 guard，private target 只用普通 store。
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

// rollbackMergedEntry 在合并第二步删除失败时恢复 keep 侧原内容，减少跨 target 合并的半成功窗口。
func rollbackMergedEntry(svc Service, root, target, path string, entry MemoryEntry) error {
	store, err := newUIMemoryMutationStore(svc, root, target)
	if err != nil {
		return err
	}

	_, err = store.updatePath(path, entry, WriteOptions{})
	return err
}

// resolveUIMemoryTargetRoot 将 UI target 解析为可写根目录；team target 需要功能启用并按当前项目根重新计算。
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

// normalizeUIMemoryTarget 规范化 UI target，历史空值和未知值默认按 private 处理以保持旧前端兼容。
func normalizeUIMemoryTarget(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "team":
		return "team"
	default:
		return "private"
	}
}

// buildUIWriteRequest 校验 UI 表单并构造写请求；类型、名称、描述和正文缺失都会返回公开校验错误。
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

// readUIMemoryEntryByPath 按 target 根目录安全读取条目，拒绝 private 访问 team/ 和 MEMORY.md 索引文件。
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

// readUIMemoryEntryByName 通过 canonical name 查找写后条目，用于 create/update 回读最新路径。
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

// toUIMemoryEntryDetail 把磁盘条目转换为 UI wire 结构，Path 优先使用已校验的展示路径。
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

// invalidateDurableMemorySections 在记忆写入、删除、合并后失效 prompt 中的 durable memory 动态段。
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

// validateUIMemoryMergePair 确认两条记忆类型一致且内容足够相似，避免 UI 手动合并不相关条目。
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

// uiMemoryEntryMergeParams 是记忆合并 RPC 的入参，A 为保留侧，B 为合并后删除侧。
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

// uiMemoryMergeResolved 保存合并前解析出的根目录和条目快照，后续回滚依赖 entryA 原值。
type uiMemoryMergeResolved struct {
	rootA   string
	rootB   string
	targetA string
	targetB string
	entryA  MemoryEntry
	entryB  MemoryEntry
}

// resolveUIMemoryMergeEntries 分别解析 A/B 两侧 target 和路径，所有路径类错误在这里完成脱敏。
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

// mergeUIMemoryEntries 把 B 的内容合并进 A 后删除 B；删除失败会尝试回滚 A，最后统一失效 prompt 缓存。
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

// uiSimilarityIgnoreParams 是相似记忆忽略 RPC 的入参，两侧 target/path 会组成稳定 ignored key。
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

// dreamOptions 将 UI 选择的模型参数收窄为 DreamOptions，只影响本次相似记忆批量整合。
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
	// target 值先规范化，避免外部脚本用非标大小写写入永远无法命中的 ignored key。
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

// runConsolidateAll 调用 similarity 子包完成批量整合，并把模型/路径错误转换为 UI 可处理的公开错误。
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

// publicConsolidateAllError 只放行已审计的整合失败原因，其余错误交由 redactIfPathBearing 脱敏。
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

// publishUIMemoryChanged 向 UI 事件总线广播记忆变更；dispatcher 缺失时静默跳过以兼容无 UI 运行模式。
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

// buildUIMemoryMergeWriteRequest 构造合并写请求；LLM override 必须描述和正文同时存在才会覆盖字面合并。
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
