package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/similarity"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

const uiMemoryPreviewLimit = 320

type uiMemoryGetParams struct {
	CWD string `json:"cwd,omitempty"`
}

type UIMemorySnapshot struct {
	Overview UIMemoryOverview     `json:"overview"`
	Private  UIMemoryScopeSection `json:"private"`
	Team     UIMemoryScopeSection `json:"team"`
}

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

type UIMemoryScopeSection struct {
	Label     string          `json:"label"`
	RootPath  string          `json:"rootPath,omitempty"`
	IndexPath string          `json:"indexPath,omitempty"`
	Notice    string          `json:"notice,omitempty"`
	Entries   []UIMemoryEntry `json:"entries"`
}

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

type UIMemoryHealth struct {
	PreferenceCount int              `json:"preferenceCount"`
	ProjectCount    int              `json:"projectCount"`
	MaxPerCategory  int              `json:"maxPerCategory"`
	SimilarGroups   []UISimilarGroup `json:"similarGroups,omitempty"`
}

type UISimilarGroup struct {
	NameA   string  `json:"nameA"`
	NameB   string  `json:"nameB"`
	PathA   string  `json:"pathA"`
	PathB   string  `json:"pathB"`
	TargetA string  `json:"targetA"`
	TargetB string  `json:"targetB"`
	Score   float64 `json:"score"`
}

// buildUIMemorySnapshot 构建UI记忆快照。
func buildUIMemorySnapshot(ctx context.Context, svc Service, logger *pkglogger.Logger, cwd string) (UIMemorySnapshot, error) {
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

func buildUIMemoryHealthSnapshots(privateRoot string, privateEntries []UIMemoryEntry, privateScope string, teamRoot string, teamEntries []UIMemoryEntry, teamScope string) []dedup.EntrySnapshot {
	snapshots := make([]dedup.EntrySnapshot, 0, len(privateEntries)+len(teamEntries))
	snapshots = append(snapshots, readUIMemoryHealthSnapshots(privateRoot, privateEntries, privateScope)...)
	snapshots = append(snapshots, readUIMemoryHealthSnapshots(teamRoot, teamEntries, teamScope)...)
	return snapshots
}

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

func countByCategory(entryType string, pref, proj *int) {
	switch entryType {
	case "user", "feedback":
		*pref++
	case "project", "reference":
		*proj++
	}
}

// loadUIMemoryScope 加载UI记忆作用域。
func loadUIMemoryScope(logger *pkglogger.Logger, label, root string, rootErr error, filterPrivateTeam bool) UIMemoryScopeSection {
	section := UIMemoryScopeSection{
		Label:   label,
		Entries: []UIMemoryEntry{},
	}
	if rootErr != nil {
		// Phase 2.1.AB.4: rootErr from resolvedStoreRoot /
		// configuredTeamMemRoot can be a *fs.PathError with the
		// on-disk root template; redact before placing in Notice.
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
		// scanMemoryEntries surfaces *fs.PathError from filepath.Walk
		// and friends; redact entry-content scan failures.
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

func firstNonEmptyUI(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func registerUIMemoryHandlers(p memoryHandlerDeps) handler.Map {
	return handler.Map{
		"ui/memory/get": platformrpc.StrictHandler(func(ctx context.Context, req uiMemoryGetParams) (UIMemorySnapshot, error) {
			return buildUIMemorySnapshot(ctx, p.Service, p.Logger, req.CWD)
		}),
	}
}

// Per-operation redacted public sentinels for the UI RPC boundary.
// Each one carries a fixed human-readable message with NO filesystem
// paths, so a *fs.PathError surfaced from the underlying syscalls
// cannot leak the on-disk memory layout through error.message in the
// JSON-RPC reply. The original error (with path) is logged at Warn
// via redactRPCError; the public form returned to the client only
// names the failed operation.
//
// Sentinels are grouped per user-visible action family (read / scan /
// save / delete) rather than per internal RPC method, so the list
// stays short as more handlers join.
var (
	errDurableMemoryReadFailed   = errors.New("durable memory entry read failed")
	errDurableMemoryScanFailed   = errors.New("durable memory scope scan failed")
	errDurableMemorySaveFailed   = errors.New("durable memory entry save failed")
	errDurableMemoryDeleteFailed = errors.New("durable memory entry delete failed")
)

// publicValidationError marks an error as safe to surface verbatim across
// the RPC boundary. Wrap a message with publicValidationErr only when the
// message contains NO filesystem path and NO operator-only detail (e.g.
// "name is required", "private durable memory cannot access team/ paths").
// The redact policy in redactIfPathBearing treats anything not wrapped this
// way as potentially path-bearing and replaces it with a sanitised public
// sentinel. Defaulting to redact is safer than the previous black-list
// approach: a future fmt.Errorf("path %s ...", path) added in a service
// helper will be redacted automatically instead of leaking through.
type publicValidationError struct {
	msg string
}

// Error 返回错误文本。
func (e *publicValidationError) Error() string { return e.msg }

// publicValidationErr wraps msg as a UI-safe validation error. Always
// returns a non-nil *publicValidationError; callers are responsible for
// auditing that msg contains no leakable detail.
func publicValidationErr(msg string) error {
	return &publicValidationError{msg: msg}
}

// errorIsPublicValidation reports whether err has been explicitly marked
// as UI-safe via publicValidationErr. The allowlist is opt-in: anything
// not wrapped this way is treated as path-bearing and redacted by
// redactIfPathBearing. We deliberately do NOT auto-classify *fs.PathError
// or the memory-module path sentinels as "safe" — those almost always
// embed paths in their Error() string.
func errorIsPublicValidation(err error) bool {
	if err == nil {
		return false
	}
	var v *publicValidationError
	return errors.As(err, &v)
}

// redactIfPathBearing redacts err by default, letting only errors that
// were explicitly opted-in via publicValidationErr through unchanged. This
// inverts the previous black-list (which let any non-listed error pass) so
// new validation messages added in service code do not silently leak
// filesystem paths through the JSON-RPC reply.
func redactIfPathBearing(logger *pkglogger.Logger, op string, public, err error, attrs ...any) error {
	if err == nil {
		return nil
	}
	if errorIsPublicValidation(err) {
		return err
	}
	return redactRPCError(logger, op, public, err, attrs...)
}

// redactRPCError logs the original (path-bearing) cause to the
// supplied slog logger at Warn level, then returns the public sentinel
// the RPC handler should surface to the client. Pass nil logger to
// fall back to pkglogger.Get() so callers without a threaded logger do
// not silently lose the operator-side signal. Caller-provided attrs
// are appended to the standard {op, err} fields.
func redactRPCError(logger *pkglogger.Logger, op string, public, cause error, attrs ...any) error {
	if logger == nil {
		logger = pkglogger.Get()
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

type uiSharedFileDeleteParams struct {
	Path string `json:"path"`
}

type UISharedFileDetail struct {
	Path      string    `json:"path"`
	Content   string    `json:"content,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

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

func sharedFileDeleteGuardRuntime(deps memoryHandlerDeps) contract.DAGRuntime {
	if deps.DAGRuntime != nil {
		return deps.DAGRuntime
	}
	return deps.Orchestration
}

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

// sharedFileReferencedByFinalOutput 按finaloutput处理shared文件referenced。
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

// dagFinalOutputReferencesPath 处理DAGfinaloutput引用路径。
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
// 把子包 internal/module/memory/similarity 反向依赖到主包私有 API 上。
// ---------------------------------------------------------------------------

type similarityAdapter struct {
	deps         memoryHandlerDeps
	dreamOptions contract.DreamOptions
}

func newSimilarityAdapter(d memoryHandlerDeps, options ...contract.DreamOptions) similarityAdapter {
	adapter := similarityAdapter{deps: d}
	if len(options) > 0 {
		adapter.dreamOptions = options[0]
	}
	return adapter
}

// Logger 处理日志器。
func (s similarityAdapter) Logger() *pkglogger.Logger { return s.deps.Logger }

// PrivateRoot 处理private根目录。
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

// SimilarPairs 返回相似记忆条目配对。
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

// ReadEntry 读取条目。
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

// Merge 合并记忆。
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

// DreamExecute 处理dreamexecute。
func (s similarityAdapter) DreamExecute(ctx context.Context, prompt string) (string, error) {
	if s.deps.DreamExecutor == nil {
		return "", contract.ErrDreamExecutorNotConfigured
	}
	if withOptions, ok := s.deps.DreamExecutor.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, s.dreamOptions)
	}
	return s.deps.DreamExecutor.ExecuteDream(ctx, prompt)
}
