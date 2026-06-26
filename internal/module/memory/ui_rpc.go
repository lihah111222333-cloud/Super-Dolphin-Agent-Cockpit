package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/similarity"
)

const uiMemoryPreviewLimit = 320

// uiMemoryGetParams 是 ui/memory/get 的 JSON-RPC 入参；CWD 为空时使用 memory 配置中的项目根。
type uiMemoryGetParams struct {
	CWD string `json:"cwd,omitempty"`
}

// UIMemorySnapshot 是记忆中心首页的 wire 响应，聚合私有记忆、团队记忆和健康提示。
type UIMemorySnapshot struct {
	Overview UIMemoryOverview     `json:"overview"`
	Private  UIMemoryScopeSection `json:"private"`
	Team     UIMemoryScopeSection `json:"team"`
}

// UIMemoryOverview 暴露记忆功能开关和根目录状态；路径字段只用于本机 UI 展示，不参与写盘决策。
type UIMemoryOverview struct {
	Enabled             bool            `json:"enabled"`
	ToolsEnabled        bool            `json:"toolsEnabled"`
	AutoDreamEnabled    bool            `json:"autoDreamEnabled"`
	AutoDreamIntent     *bool           `json:"autoDreamIntent,omitempty"`
	RootDir             string          `json:"rootDir,omitempty"`
	ProjectRoot         string          `json:"projectRoot,omitempty"`
	PrivateRoot         string          `json:"privateRoot,omitempty"`
	AutoMemPathOverride string          `json:"autoMemPathOverride,omitempty"`
	TeamFeatureEnabled  bool            `json:"teamFeatureEnabled"`
	Health              *UIMemoryHealth `json:"health,omitempty"`
}

// UIMemoryScopeSection 表示一个记忆作用域的 UI 列表，Notice 承载脱敏后的扫描/解析失败原因。
type UIMemoryScopeSection struct {
	Label     string          `json:"label"`
	RootPath  string          `json:"rootPath,omitempty"`
	IndexPath string          `json:"indexPath,omitempty"`
	Notice    string          `json:"notice,omitempty"`
	Entries   []UIMemoryEntry `json:"entries"`
}

// UIMemoryEntry 是列表页使用的条目摘要，Preview 只截取正文片段，完整内容需走详情 RPC。
type UIMemoryEntry struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Type        string    `json:"type,omitempty"`
	Path        string    `json:"path,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	Preview     string    `json:"preview,omitempty"`
	Title       string    `json:"title,omitempty"`
	// Source 透传记忆条目的来源标记（如 "dream"），UI 据此渲染徽章。
	Source string `json:"source,omitempty"`
}

// UIMemoryHealth 汇总可执行的治理信号，如类型计数和相似组，用于 UI banner 而非写盘校验。
type UIMemoryHealth struct {
	PreferenceCount int              `json:"preferenceCount"`
	ProjectCount    int              `json:"projectCount"`
	MaxPerCategory  int              `json:"maxPerCategory"`
	SimilarGroups   []UISimilarGroup `json:"similarGroups,omitempty"`
}

// UISimilarGroup 是前端相似记忆提示的 wire DTO，target/path 会回传给 ignore/merge RPC。
type UISimilarGroup struct {
	NameA   string  `json:"nameA"`
	NameB   string  `json:"nameB"`
	PathA   string  `json:"pathA"`
	PathB   string  `json:"pathB"`
	TargetA string  `json:"targetA"`
	TargetB string  `json:"targetB"`
	Score   float64 `json:"score"`
}

// buildUIMemorySnapshot 构建记忆中心快照，分别解析私有/团队根目录并对路径类错误做脱敏。
func buildUIMemorySnapshot(ctx context.Context, svc Service, logger *slog.Logger, cwd string) (UIMemorySnapshot, error) {
	if svc == nil {
		return UIMemorySnapshot{}, errors.New("memory service is not configured")
	}
	cfg := svc.Config()
	projectRoot := strings.TrimSpace(cwd)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	buildCtx := contract.BuildCtx{CWD: projectRoot}
	if gitRoot, err := FindCanonicalGitRoot(ctx, projectRoot); err == nil && strings.TrimSpace(gitRoot) != "" {
		buildCtx.GitRoot = strings.TrimSpace(gitRoot)
	}
	gate := ResolveMemoryGate(buildCtx, &cfg)
	intent, _ := ReadAutoDreamIntent(cfg.RootDir)

	privateRoot, privateErr := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
	privateSection := loadUIMemoryScope(logger, "Private durable memory", privateRoot, privateErr, true)

	teamSection := UIMemoryScopeSection{
		Label:   "Team durable memory",
		Notice:  "当前未启用 Team memory。",
		Entries: []UIMemoryEntry{},
	}
	if teamMemoryConfigured(cfg) {
		teamRoot, err := configuredTeamMemRoot(&cfg, buildCtx)
		teamSection = loadUIMemoryScope(logger, "Team durable memory", teamRoot, err, false)
	}

	health := computeUIMemoryHealth(privateSection.Entries, teamSection.Entries)
	populateUIMemoryHealthSimilarGroups(health, privateRoot, privateSection.Entries, teamSection.RootPath, teamSection.Entries)

	return UIMemorySnapshot{
		Overview: UIMemoryOverview{
			Enabled:             cfg.Enabled,
			ToolsEnabled:        cfg.EnableTools,
			AutoDreamEnabled:    cfg.Enabled && cfg.ExtractOnStop && gate.AutoEnabled,
			AutoDreamIntent:     intent,
			RootDir:             strings.TrimSpace(cfg.RootDir),
			ProjectRoot:         projectRoot,
			PrivateRoot:         strings.TrimSpace(privateRoot),
			AutoMemPathOverride: strings.TrimSpace(cfg.AutoMemPathOverride),
			TeamFeatureEnabled:  cfg.Features.TeamMemory,
			Health:              health,
		},
		Private: privateSection,
		Team:    teamSection,
	}, nil
}

// computeUIMemoryHealth 统计私有和团队记忆的类型计数，作为 UI 的轻量健康提示。
func computeUIMemoryHealth(privateEntries, teamEntries []UIMemoryEntry) *UIMemoryHealth {
	var prefCount, projCount int
	for _, e := range privateEntries {
		countByCategory(e.Type, &prefCount, &projCount)
	}
	for _, e := range teamEntries {
		countByCategory(e.Type, &prefCount, &projCount)
	}
	return &UIMemoryHealth{
		PreferenceCount: prefCount,
		ProjectCount:    projCount,
		MaxPerCategory:  dedup.MaxEntriesPerType,
	}
}

// populateUIMemoryHealthSimilarGroups 计算相似记忆组，并过滤用户已经忽略的 pair。
func populateUIMemoryHealthSimilarGroups(health *UIMemoryHealth, privateRoot string, privateEntries []UIMemoryEntry, teamRoot string, teamEntries []UIMemoryEntry) {
	if health == nil {
		return
	}
	pairs := dedup.FindSimilarPairs(buildUIMemoryHealthSnapshots(privateRoot, privateEntries, "private", teamRoot, teamEntries, "team"))
	if len(pairs) == 0 {
		return
	}
	// ignored set 持久化在 private root 下；加载失败按空 set 处理，
	// 避免 ignored 文件损坏阻塞 banner 渲染。
	ignored, _ := similarity.LoadIgnored(privateRoot)
	groups := make([]UISimilarGroup, 0, len(pairs))
	for _, p := range pairs {
		if _, hit := ignored[similarity.IgnoreKey(p.ScopeA, p.PathA, p.ScopeB, p.PathB)]; hit {
			continue
		}
		groups = append(groups, UISimilarGroup{
			NameA: p.NameA, NameB: p.NameB,
			PathA: p.PathA, PathB: p.PathB,
			TargetA: p.ScopeA, TargetB: p.ScopeB,
			Score: p.Score,
		})
	}
	health.SimilarGroups = groups
}

// buildUIMemoryHealthSnapshots 把私有和团队列表转换为 dedup 子包需要的最小快照。
func buildUIMemoryHealthSnapshots(privateRoot string, privateEntries []UIMemoryEntry, privateScope string, teamRoot string, teamEntries []UIMemoryEntry, teamScope string) []dedup.EntrySnapshot {
	snapshots := make([]dedup.EntrySnapshot, 0, len(privateEntries)+len(teamEntries))
	snapshots = append(snapshots, readUIMemoryHealthSnapshots(privateRoot, privateEntries, privateScope)...)
	snapshots = append(snapshots, readUIMemoryHealthSnapshots(teamRoot, teamEntries, teamScope)...)
	return snapshots
}

// readUIMemoryHealthSnapshots 逐条读取 UI 列表背后的文件；单条读取失败只跳过该条，避免 banner 阻断首页。
func readUIMemoryHealthSnapshots(root string, entries []UIMemoryEntry, scope string) []dedup.EntrySnapshot {
	if strings.TrimSpace(root) == "" || len(entries) == 0 {
		return nil
	}
	snapshots := make([]dedup.EntrySnapshot, 0, len(entries))
	for _, entry := range entries {
		detail, _, err := readUIMemoryEntryByPath(root, scope, entry.Path)
		if err != nil {
			continue
		}
		snapshots = append(snapshots, dedup.EntrySnapshot{
			Name: strings.TrimSpace(detail.Frontmatter.Name), Type: strings.TrimSpace(string(detail.Type())), Content: detail.Content,
			Path: entry.Path, Scope: scope,
		})
	}
	return snapshots
}

// countByCategory 将 durable memory 类型归入偏好类或项目类计数，未知类型不参与健康上限判断。
func countByCategory(entryType string, pref, proj *int) {
	switch entryType {
	case "user", "feedback":
		*pref++
	case "project", "reference":
		*proj++
	}
}

// loadUIMemoryScope 加载 UI 记忆作用域并把路径类错误脱敏后放入 Notice。
func loadUIMemoryScope(logger *slog.Logger, label, root string, rootErr error, filterPrivateTeam bool) UIMemoryScopeSection {
	section := UIMemoryScopeSection{
		Label:   label,
		Entries: []UIMemoryEntry{},
	}
	if rootErr != nil {
		// rootErr 可能包含磁盘根目录模板，进入 UI Notice 前必须脱敏。
		section.Notice = redactIfPathBearing(logger, "durable_memory_resolve_root",
			errDurableMemoryScanFailed, rootErr, "label", label).Error()
		return section
	}
	root = strings.TrimSpace(root)
	if root == "" {
		section.Notice = "未解析到目录。"
		return section
	}
	section.RootPath = root
	section.IndexPath = memoryIndexPath(root)
	entries, err := scanMemoryEntries(root)
	if err != nil {
		// scanMemoryEntries 可能透出 filepath.Walk 的路径错误，返回 UI 前统一脱敏。
		section.Notice = redactIfPathBearing(logger, "durable_memory_scope_scan",
			errDurableMemoryScanFailed, err, "label", label).Error()
		return section
	}
	for _, entry := range entries {
		rel := memoryEntryDisplayPath(root, entry.FilePath)
		if filterPrivateTeam && strings.HasPrefix(rel, "team/") {
			continue
		}
		section.Entries = append(section.Entries, UIMemoryEntry{
			Name:        strings.TrimSpace(entry.Frontmatter.Name),
			Description: strings.TrimSpace(entry.Frontmatter.Description),
			Type:        strings.TrimSpace(string(entry.Type())),
			Path:        rel,
			UpdatedAt:   entry.UpdatedAt,
			Preview:     uiPreviewText(entry.Content),
			Title:       strings.TrimSpace(entry.Frontmatter.Title),
			Source:      strings.TrimSpace(entry.Frontmatter.Source),
		})
	}
	if len(section.Entries) == 0 {
		section.Notice = firstNonEmptyUI(section.Notice, "当前目录下还没有可读的记忆条目。")
	}
	return section
}

// memoryEntryDisplayPath 将磁盘路径转成相对 root 的 UI 展示路径；解析失败时退回 slash 形式但不参与写入。
func memoryEntryDisplayPath(root, path string) string {
	root = strings.TrimSpace(root)
	path = strings.TrimSpace(path)
	if resolvedRoot, err := resolveExistingMemoryPath(root); err == nil {
		root = resolvedRoot
	}
	if resolvedPath, err := resolveExistingMemoryPath(path); err == nil {
		path = resolvedPath
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// uiPreviewText 生成列表预览文本，限制行数和 rune 长度，避免大正文撑爆 JSON-RPC 响应。
func uiPreviewText(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	runes := []rune(text)
	if len(runes) > uiMemoryPreviewLimit {
		return strings.TrimSpace(string(runes[:uiMemoryPreviewLimit])) + "…"
	}
	return text
}

// firstNonEmptyUI 返回首个非空白 UI 文案，用于 Notice 等字段的优先级合并。
func firstNonEmptyUI(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// registerUIMemoryHandlers 注册只读记忆 RPC，写入/删除入口在 mutation 文件中集中处理。
func registerUIMemoryHandlers(p memoryHandlerDeps) handler.Map {
	return handler.Map{
		"ui/memory/get": platformrpc.StrictHandler(func(ctx context.Context, req uiMemoryGetParams) (UIMemorySnapshot, error) {
			return buildUIMemorySnapshot(ctx, p.Service, p.Logger, req.CWD)
		}),
	}
}

// UI RPC 边界使用的公开错误哨兵。
// 文案不含文件系统路径；底层真实错误只进入 warn 日志，返回给前端的错误只说明失败操作。
var (
	errDurableMemoryReadFailed   = errors.New("durable memory entry read failed")
	errDurableMemoryScanFailed   = errors.New("durable memory scope scan failed")
	errDurableMemorySaveFailed   = errors.New("durable memory entry save failed")
	errDurableMemoryDeleteFailed = errors.New("durable memory entry delete failed")
)

// publicValidationError 标记可原样穿过 UI RPC 边界的校验错误。
// 只有确认不含路径和运维细节的消息才能使用该类型；其他错误默认按可能含路径处理并脱敏。
type publicValidationError struct {
	msg string
}

// Error 返回已经审计为 UI 安全的错误文本。
func (e *publicValidationError) Error() string { return e.msg }

// publicValidationErr 把已审计的消息包装成 UI 安全校验错误。
func publicValidationErr(msg string) error {
	return &publicValidationError{msg: msg}
}

// errorIsPublicValidation 判断错误是否显式标记为 UI 安全。
// allowlist 采用 opt-in：未包装的错误都按可能含路径处理，尤其不能自动放行 fs.PathError。
func errorIsPublicValidation(err error) bool {
	if err == nil {
		return false
	}
	var v *publicValidationError
	return errors.As(err, &v)
}

// redactIfPathBearing 默认脱敏错误，只允许 publicValidationErr 包装的消息原样返回。
// 这样未来新增的服务层错误不会因为忘记加入黑名单而把文件路径泄漏到 JSON-RPC 响应。
func redactIfPathBearing(logger *slog.Logger, op string, public, err error, attrs ...any) error {
	if err == nil {
		return nil
	}
	if errorIsPublicValidation(err) {
		return err
	}
	return redactRPCError(logger, op, public, err, attrs...)
}

// redactRPCError 记录原始错误并返回公开哨兵错误。
// logger 为空时使用 slog.Default，确保运维侧仍能看到真实 cause，前端只拿到脱敏消息。
func redactRPCError(logger *slog.Logger, op string, public, cause error, attrs ...any) error {
	if logger == nil {
		logger = slog.Default()
	}
	fields := append([]any{"op", op, "err", cause.Error()}, attrs...)
	logger.Warn("memory rpc operation failed", fields...)
	return public
}

// ---------------------------------------------------------------------------
// Shared-file RPC helpers (was ui_rpc_sharedfile.go)
// ---------------------------------------------------------------------------

type uiSharedFileGetParams struct {
	Path string `json:"path"`
}

// uiSharedFileDeleteParams 是 shared file 删除 RPC 的入参，Path 会先经过 DAG final_output 保护检查。
type uiSharedFileDeleteParams struct {
	Path string `json:"path"`
}

// UISharedFileDetail 是 shared file 详情响应；Content 仅来自 shared file store，不读取任意本地路径。
type UISharedFileDetail struct {
	Path      string    `json:"path"`
	Content   string    `json:"content,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// getUISharedFile 读取单个 shared file，空 path 返回公开校验错误，store 未装配则 fail-fast。
func getUISharedFile(ctx context.Context, deps memoryHandlerDeps, req uiSharedFileGetParams) (UISharedFileDetail, error) {
	if deps.SharedFiles == nil {
		return UISharedFileDetail{}, errors.New("shared file store is not configured")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return UISharedFileDetail{}, publicValidationErr("path is required")
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

// deleteUISharedFile 删除 shared file 前先确认没有 DAG final_output 引用，避免 UI 误删交付产物。
func deleteUISharedFile(ctx context.Context, deps memoryHandlerDeps, req uiSharedFileDeleteParams) (bool, error) {
	if deps.SharedFilesDeleter == nil {
		return false, errors.New("shared file store is not configured for deletion")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return false, publicValidationErr("path is required")
	}
	if err := ensureSharedFileDeleteAllowed(ctx, sharedFileDeleteGuardRuntime(deps), path); err != nil {
		return false, err
	}
	count, err := deps.SharedFilesDeleter.Delete(ctx, path)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

const (
	sharedFileDeleteGuardDAGLimit int   = 500
	sharedFileDeleteGuardRunLimit int32 = 100
)

// sharedFileDeleteGuardRuntime 选择可用的 DAG runtime，新接口优先，旧 orchestration 作为兼容入口。
func sharedFileDeleteGuardRuntime(deps memoryHandlerDeps) contract.DAGRuntime {
	if deps.DAGRuntime != nil {
		return deps.DAGRuntime
	}
	return deps.Orchestration
}

// ensureSharedFileDeleteAllowed 执行 shared file 删除保护，runtime 缺失时阻断而不是放行删除。
func ensureSharedFileDeleteAllowed(ctx context.Context, dagRuntime contract.DAGRuntime, path string) error {
	if dagRuntime == nil {
		return publicValidationErr("shared file final_output delete guard is unavailable; retry after DAG orchestration is connected")
	}
	protected, err := sharedFileReferencedByFinalOutput(ctx, dagRuntime, path)
	if err != nil {
		return fmt.Errorf("check shared file final_output references: %w", err)
	}
	if protected {
		return publicValidationErr("shared file is referenced as a DAG final_output; export or detach it before deleting")
	}
	return nil
}

// sharedFileReferencedByFinalOutput 检查 shared file 是否被任一 DAG final_output 引用。
// 发现引用或扫描触及上限时会阻断删除，避免 UI 误删仍被运行记录依赖的产物。
func sharedFileReferencedByFinalOutput(ctx context.Context, dagRuntime contract.DAGRuntime, path string) (bool, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		return false, nil
	}
	dags, err := listDAGsForDeleteGuard(ctx, dagRuntime)
	if err != nil {
		return false, err
	}
	for _, dag := range dags {
		protected, err := dagFinalOutputReferencesPath(ctx, dagRuntime, dag.DagKey, target)
		if err != nil || protected {
			return protected, err
		}
	}
	return false, nil
}

// listDAGsForDeleteGuard 读取有限数量 DAG；触及上限视为保护信息不完整并阻断删除。
func listDAGsForDeleteGuard(ctx context.Context, dagRuntime contract.DAGRuntime) ([]contract.DAGSummary, error) {
	dags, err := dagRuntime.ListDAGs(ctx, contract.ListDAGsFilter{Limit: sharedFileDeleteGuardDAGLimit})
	if err != nil {
		return nil, err
	}
	if len(dags) >= sharedFileDeleteGuardDAGLimit {
		return nil, fmt.Errorf("DAG scan reached safety limit %d", sharedFileDeleteGuardDAGLimit)
	}
	return dags, nil
}

// dagFinalOutputReferencesPath 扫描单个 DAG 的运行记录，判断 final_output 是否指向目标路径。
func dagFinalOutputReferencesPath(ctx context.Context, dagRuntime contract.DAGRuntime, dagKey, target string) (bool, error) {
	dagKey = strings.TrimSpace(dagKey)
	if dagKey == "" {
		return false, nil
	}
	runs, err := dagRuntime.ListRuns(ctx, contract.ListRunsRequest{
		DagKey: dagKey,
		Limit:  sharedFileDeleteGuardRunLimit,
	})
	if err != nil {
		return false, err
	}
	if len(runs.Runs) >= int(sharedFileDeleteGuardRunLimit) {
		return false, fmt.Errorf("run scan reached safety limit %d for DAG %q", sharedFileDeleteGuardRunLimit, dagKey)
	}
	for _, run := range runs.Runs {
		ref, ok := contract.FinalOutputFileFromRunMetadata(run.Metadata)
		if ok && strings.TrimSpace(ref.Path) == target {
			return true, nil
		}
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// similarity.Deps adapter
// 把 similarity 子包需要的读写、日志和 dream 能力桥接到 UI RPC 私有实现。
// ---------------------------------------------------------------------------

type similarityAdapter struct {
	deps         memoryHandlerDeps
	dreamOptions contract.DreamOptions
}

// newSimilarityAdapter 把 UI RPC 依赖收窄为 similarity 子包接口，dreamOptions 只影响本次批量整合。
func newSimilarityAdapter(d memoryHandlerDeps, options ...contract.DreamOptions) similarityAdapter {
	adapter := similarityAdapter{deps: d}
	if len(options) > 0 {
		adapter.dreamOptions = options[0]
	}
	return adapter
}

// Logger 返回 similarity 子包记录问题时使用的 logger。
func (s similarityAdapter) Logger() *slog.Logger { return s.deps.Logger }

// PrivateRoot 解析当前 cwd 对应的私有记忆根目录。
func (s similarityAdapter) PrivateRoot(_ context.Context, cwd string) (string, error) {
	if s.deps.Service == nil {
		return "", errors.New("memory service is not configured")
	}
	cfg := s.deps.Service.Config()
	projectRoot := strings.TrimSpace(cwd)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	return resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
}

// SimilarPairs 从 UI snapshot 中提取相似记忆配对，供 similarity 子包批量处理。
func (s similarityAdapter) SimilarPairs(ctx context.Context, cwd string) ([]similarity.SimilarPair, error) {
	snap, err := buildUIMemorySnapshot(ctx, s.deps.Service, s.deps.Logger, cwd)
	if err != nil {
		return nil, err
	}
	if snap.Overview.Health == nil {
		return nil, nil
	}
	groups := snap.Overview.Health.SimilarGroups
	out := make([]similarity.SimilarPair, 0, len(groups))
	for _, g := range groups {
		out = append(out, similarity.SimilarPair{
			NameA: g.NameA, NameB: g.NameB,
			PathA: g.PathA, PathB: g.PathB,
			TargetA: g.TargetA, TargetB: g.TargetB,
			Score: g.Score,
		})
	}
	return out, nil
}

// ReadEntry 按 target/path 读取单条记忆，并转换为 similarity 子包的最小快照。
func (s similarityAdapter) ReadEntry(ctx context.Context, cwd, target, path string) (similarity.EntrySnapshot, error) {
	root, _, err := resolveUIMemoryTargetRoot(ctx, s.deps.Service, cwd, target)
	if err != nil {
		return similarity.EntrySnapshot{}, err
	}
	entry, _, err := readUIMemoryEntryByPath(root, target, path)
	if err != nil {
		return similarity.EntrySnapshot{}, err
	}
	return similarity.EntrySnapshot{
		Name:        entry.Frontmatter.Name,
		Description: entry.Frontmatter.Description,
		Content:     entry.Content,
		Type:        string(entry.Type()),
	}, nil
}

// Merge 复用 UI RPC 的合并实现写回记忆，保持路径校验和错误脱敏策略一致。
func (s similarityAdapter) Merge(ctx context.Context, req similarity.MergeRequest) error {
	_, err := mergeUIMemoryEntries(ctx, s.deps, uiMemoryEntryMergeParams{
		CWD:               req.CWD,
		TargetA:           req.TargetA,
		PathA:             req.PathA,
		TargetB:           req.TargetB,
		PathB:             req.PathB,
		MergedDescription: req.MergedDescription,
		MergedContent:     req.MergedContent,
	})
	return err
}

// DreamExecute 调用 dream executor 生成相似记忆合并决策。
// 支持带 options 的 executor；未配置时返回 contract.ErrDreamExecutorNotConfigured。
func (s similarityAdapter) DreamExecute(ctx context.Context, prompt string) (string, error) {
	if s.deps.DreamExecutor == nil {
		return "", contract.ErrDreamExecutorNotConfigured
	}
	if withOptions, ok := s.deps.DreamExecutor.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, s.dreamOptions)
	}
	return s.deps.DreamExecutor.ExecuteDream(ctx, prompt)
}
