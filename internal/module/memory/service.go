package memory

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

type Service interface {
	Config() Config
	RootDir() string
	EnsureRoot(ctx context.Context) error
	RunConsolidation(ctx context.Context) error
	GetDreamTaskStatus() DreamTaskSnapshot
	KillDreamTask() error
}

type service struct {
	cfg          *Config
	logger       *slog.Logger
	consolidator *AutoDreamConsolidator
	dreamHooks   *MemoryLifecycleHooks
}

type MemoryLifecycleHooks struct {
	cfg                 *Config
	team                *TeamMemoryManager
	enabled             bool
	extractOnStop       bool
	rootDir             string
	projectRoot         string
	autoMemPathOverride string
	consolidator        *AutoDreamConsolidator
	extractFn           ExtractFunc
	extractor           *MemoryExtractor
	manifestBuilder     *ManifestBuilder
	threads             historySource
	threadStore         threadMetadataStore
	sections            sectionInvalidator
	logger              *slog.Logger
	timeNow             func() time.Time
	feedbackTracker     *FeedbackTracker
	onFeedbackThreshold func(topicKey string, group []ExtractedMemory)

	// stateMu serialises all reads and writes of the six maps below.
	// The plain map choice (vs sync.Map) is intentional: this is a
	// coarse-grained mutex over the whole turn/extraction bookkeeping
	// surface, and ExtractionState values carry their own mu for
	// finer-grained field protection once a *ExtractionState reference
	// has been resolved under stateMu (the maps never delete entries
	// that other goroutines may still hold a reference to, so the
	// reference itself stays valid after stateMu is released).
	// New callers MUST hold stateMu while touching any of these maps.
	stateMu           sync.Mutex
	states            map[string]*ExtractionState    // guarded by stateMu
	activeTurns       map[string]string              // guarded by stateMu
	callTurns         map[string]toolCallScope       // guarded by stateMu
	turnWrites        map[string]map[string]struct{} // guarded by stateMu
	turnInputs        map[string]string              // guarded by stateMu
	handledTurnInputs map[string]struct{}            // guarded by stateMu
	extractWG         sync.WaitGroup

	// drainMu + drainClosed guard the extractWG against the classic
	// sync.WaitGroup race: once DrainPendingExtraction calls Wait(),
	// concurrent Add(1) from a new enqueueBackgroundExtraction can panic
	// with "WaitGroup is reused before previous Wait has returned".
	// drainClosed is set monotonically in Drain (close-path semantics);
	// new enqueues entering after Drain are dropped. Field is named
	// `drainClosed` (not `drainPending`) to avoid confusion with the
	// unrelated `drainPending()` method on nestedIngestWorker /
	// teamSyncCoordinator in the same package, which empty a queue
	// rather than flag a close.
	drainMu     sync.Mutex
	drainClosed bool

	dreamMu   sync.Mutex
	dreamTask *dreamTaskState

	// crossScopeWarned dedups cross-scope same-name warns emitted by
	// writeIntent (Phase 4.1a 子项 3.3): each name is logged at most
	// once per process to avoid log spam on repeated explicit writes
	// against the same entry. Key: entry.Name (string).
	crossScopeWarned sync.Map
}

var saveIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*remember\s+that\s+(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*remember\s*[:：\-—,，]\s*(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:save|store)\s+(.+?)\s+(?:to|in)\s+memory\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:save|store)\s+to\s+memory\s*(?:[:：\-—,，]\s*|\s+)(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:请)?记住(?:这个|这点)?\s*(?:[:：\-—,，]\s*|\s+)(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:把\s+)?(.+?)\s*(?:记到记忆里|保存到记忆里|保存到记忆中)\s*$`),
}

var forgetIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*forget\s+that\s+(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*forget\s*[:：\-—,，]\s*(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:remove|delete)\s+(.+?)\s+from\s+memory\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:请)?(?:忘记|忘掉)(?:这个|这点|这条)?\s*(?:[:：\-—,，]\s*|\s+)(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*把\s+(.+?)\s*(?:从记忆里删除|从记忆中删除|从记忆删除|从记忆里移除)\s*$`),
}

func NewService(cfg *Config, logger *slog.Logger, consolidator *AutoDreamConsolidator, hooks *MemoryLifecycleHooks) Service {
	return newServiceWithConsolidator(cfg, logger, consolidator, hooks)
}

func newServiceWithConsolidator(cfg *Config, logger *slog.Logger, consolidator *AutoDreamConsolidator, hooks *MemoryLifecycleHooks) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	if consolidator == nil {
		consolidator = NewAutoDreamConsolidator(nil)
	}
	consolidator.cfg = memoryConfig(cfg)
	return &service{cfg: cfg, logger: logger, consolidator: consolidator, dreamHooks: hooks}
}

func (s *service) Config() Config {
	if s == nil || s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

func (s *service) RootDir() string {
	return strings.TrimSpace(s.Config().RootDir)
}

func (s *service) EnsureRoot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	cfg := s.Config()
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return err
	}
	if root == "" {
		return errors.New("memory root dir is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if teamMemoryConfigured(cfg) {
		teamRoot, err := configuredTeamMemRoot(&cfg)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(teamRoot, 0o755); err != nil {
			return err
		}
	}
	if s.logger != nil {
		s.logger.Debug("memory root ready", "root_dir", filepath.Clean(root))
	}
	return nil
}

func (s *service) RunConsolidation(ctx context.Context) error {
	if s == nil || s.consolidator == nil {
		return ErrConsolidationExtractFuncRequired
	}
	if err := s.EnsureRoot(ctx); err != nil {
		return err
	}
	cfg := s.Config()
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return err
	}
	if err := s.consolidator.consolidateWithOptions(ctx, root, nil, s.consolidationRunOptions(ctx, root)); err != nil {
		return err
	}
	if s.dreamHooks != nil {
		s.dreamHooks.invalidateMemorySections()
	}
	return nil
}

func (s *service) GetDreamTaskStatus() DreamTaskSnapshot {
	if s == nil || s.dreamHooks == nil {
		return DreamTaskSnapshot{}
	}
	return s.dreamHooks.GetDreamTaskStatus()
}

func (s *service) KillDreamTask() error {
	if s == nil || s.dreamHooks == nil {
		return ErrDreamTaskNotRunning
	}
	return s.dreamHooks.KillDreamTask()
}

func (s *service) consolidationRunOptions(ctx context.Context, root string) consolidationRunOptions {
	return consolidationRunOptions{
		cfg:            s.cfg,
		runtimeContext: s.manualConsolidationRuntimeContext(ctx, root),
	}
}

func (s *service) manualConsolidationRuntimeContext(ctx context.Context, root string) string {
	if s == nil || s.dreamHooks == nil {
		return ""
	}
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return ""
	}
	sessions, err := s.dreamHooks.autoDreamSessionCount(ctx, "", stamp.lastSuccessTime())
	if err != nil {
		sessions = 0
	}
	return buildConsolidationRuntimeContext("manual consolidation request", sessions, stamp.lastSuccessTime(), "")
}

func (h *MemoryLifecycleHooks) onThreadStart(_ context.Context, evt threaddto.Started) {
	if h == nil || !h.enabled || h.logger == nil {
		return
	}
	h.logger.Debug("memory thread hook ready", "thread_id", strings.TrimSpace(evt.ThreadID))
}

func (h *MemoryLifecycleHooks) onTurnEnd(ctx context.Context, evt turndto.TurnCompleted) {
	if h.shouldSkipTurnEnd(ctx, evt) {
		return
	}
	if h.handleTrackedTurnIntent(ctx, evt) {
		return
	}
	h.writeDetectedTurnIntent(ctx, evt)
}

func (h *MemoryLifecycleHooks) shouldSkipTurnEnd(ctx context.Context, evt turndto.TurnCompleted) bool {
	if h == nil || !h.enabled || !evt.Success {
		return true
	}
	return contextErr(ctx) != nil
}

func (h *MemoryLifecycleHooks) handleTrackedTurnIntent(ctx context.Context, evt turndto.TurnCompleted) bool {
	key := turnTrackingKey(evt.ThreadID, evt.TurnID)
	if h.consumeHandledTurnInput(key) {
		h.clearTurnInput(key)
		return true
	}
	text, ok := h.consumeTurnInput(key)
	if !ok {
		return false
	}
	handled, err := h.handleExplicitUserMemoryIntent(ctx, evt, text)
	h.handleExplicitIntentError(evt.ThreadID, handled, err)
	return handled || err != nil
}

func (h *MemoryLifecycleHooks) writeDetectedTurnIntent(ctx context.Context, evt turndto.TurnCompleted) {
	intent := h.detectTurnIntent(ctx, evt)
	if !intent.Detected || strings.TrimSpace(intent.Content) == "" {
		return
	}
	if err := h.writeDetectedIntent(ctx, evt, intent); err != nil && h.logger != nil {
		h.logger.Warn("memory explicit save failed", "thread_id", strings.TrimSpace(evt.ThreadID), "error", err)
	}
}

func (h *MemoryLifecycleHooks) detectTurnIntent(ctx context.Context, evt turndto.TurnCompleted) SaveIntent {
	meta := h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(evt.ThreadID))
	gate := ResolveMemoryGate(meta.buildCtx(), h.cfg)
	if gate.KairosActive {
		if intent := DetectKairosWriteIntent(evt); intent.Detected {
			return intent
		}
	}
	return SaveIntent{}
}

func (h *MemoryLifecycleHooks) writeIntent(ctx context.Context, threadID string, intent SaveIntent) error {
	entry := buildExplicitMemoryWrite(intent)
	options := h.writeOptions(ctx, threadID)
	primary, secondary, err := h.intentDiskStores(ctx, threadID, entry.Type)
	if err != nil {
		return err
	}
	store, err := selectExplicitWriteStore(entry.Name, primary, secondary)
	if err != nil {
		return err
	}
	primaryScope, secondaryScope := scopeNamesForIntentStores(entry.Type, secondary != nil)
	h.warnCrossScopeSameName(entry.Name, store, primary, secondary, primaryScope, secondaryScope)
	if writeErr := upsertStructuredMemory(store, entry, options); writeErr != nil {
		return writeErr
	}
	trackFeedbackIfApplicable(h, intent)
	return nil
}

// warnCrossScopeSameName logs a single warn (dedup'd by name) when the
// same-name entry exists in BOTH the selected store and the other store
// of the (primary, secondary) pair. Combined mode invariant: explicit
// writes pick one scope, but if the entry already exists in the other
// scope under the same name, future retrieval may surface either copy
// depending on ranking. The warn signals this divergence to operators
// without blocking the write. Phase 4.1a 子项 3.3.
func (h *MemoryLifecycleHooks) warnCrossScopeSameName(name string, selected, primary, secondary memoryStructuredStore, primaryScope, secondaryScope string) {
	if h == nil || h.logger == nil {
		return
	}
	var (
		other              memoryStructuredStore
		selScope, oppScope string
	)
	switch selected {
	case primary:
		other, selScope, oppScope = secondary, primaryScope, secondaryScope
	case secondary:
		other, selScope, oppScope = primary, secondaryScope, primaryScope
	}
	if other == nil {
		return
	}
	if _, err := other.Read(name); err != nil {
		return
	}
	if _, loaded := h.crossScopeWarned.LoadOrStore(name, struct{}{}); loaded {
		return
	}
	h.logger.Warn("memory cross-scope same-name entry detected",
		"name", name,
		"selected_scope", selScope,
		"other_scope", oppScope,
		"note", "explicit write went to selected store; same-name entry exists in the other scope",
	)
}

// scopeNamesForIntentStores returns the scope tag corresponding to the
// (primary, secondary) pair returned by intentDiskStores. Mirrors the
// routing logic at intentDiskStores: when no team store is available,
// primary is always private; otherwise primary depends on
// defaultTeamMemoryType. Phase 4.1a 子项 3.3.
func scopeNamesForIntentStores(memoryType MemoryType, hasSecondary bool) (primaryScope, secondaryScope string) {
	if !hasSecondary {
		return "private", ""
	}
	if defaultTeamMemoryType(memoryType) {
		return "team", "private"
	}
	return "private", "team"
}

func (h *MemoryLifecycleHooks) deleteIntent(ctx context.Context, threadID string, intent ForgetIntent) error {
	options := h.writeOptions(ctx, threadID)
	primary, secondary, err := h.intentDiskStores(ctx, threadID, MemoryTypeUnknown)
	if err != nil {
		return err
	}
	if err := deleteMemoryAcrossStores(intent.Query, options, primary, secondary); err != nil {
		return err
	}
	h.invalidateMemorySections()
	return nil
}

func (h *MemoryLifecycleHooks) diskStore() (memoryWriteStore, error) {
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return nil, err
	}
	return newDiskStore(root)
}

type ForgetIntent struct {
	Detected bool
	Query    string
}

func DetectSaveIntent(userText string) SaveIntent {
	response := normalizeIntentText(userText)
	if response == "" {
		return SaveIntent{}
	}
	for _, pattern := range saveIntentPatterns {
		matches := pattern.FindStringSubmatch(response)
		if len(matches) == 0 {
			continue
		}
		content := normalizeHookContent(matches[len(matches)-1])
		if !hasMeaningfulMemoryContent(content) {
			continue
		}
		return SaveIntent{Detected: true, Content: content, Type: inferMemoryType(content)}
	}
	return SaveIntent{}
}

func DetectForgetIntent(userText string) ForgetIntent {
	response := normalizeIntentText(userText)
	if response == "" {
		return ForgetIntent{}
	}
	for _, pattern := range forgetIntentPatterns {
		matches := pattern.FindStringSubmatch(response)
		if len(matches) == 0 {
			continue
		}
		query := normalizeHookContent(matches[len(matches)-1])
		if !hasMeaningfulMemoryContent(query) || isGenericForgetTarget(query) {
			continue
		}
		return ForgetIntent{Detected: true, Query: query}
	}
	return ForgetIntent{}
}

func (h *MemoryLifecycleHooks) handleExplicitUserMemoryIntent(
	ctx context.Context,
	evt turndto.TurnCompleted,
	text string,
) (bool, error) {
	if forget := DetectForgetIntent(text); forget.Detected {
		return true, h.deleteIntent(ctx, evt.ThreadID, forget)
	}
	intent := DetectSaveIntent(text)
	if !intent.Detected {
		return false, nil
	}
	return true, h.writeDetectedIntent(ctx, evt, intent)
}

func (h *MemoryLifecycleHooks) handleExplicitIntentError(threadID string, handled bool, err error) {
	if !handled || err == nil || h == nil || h.logger == nil {
		return
	}
	h.logger.Warn("memory explicit intent failed", "thread_id", strings.TrimSpace(threadID), "error", err)
}

func resolvedStoreRoot(baseRoot, projectRoot, autoMemPathOverride string) (string, error) {
	if override := strings.TrimSpace(autoMemPathOverride); override != "" {
		validatedOverride, err := shared.ValidateMemoryRoot(override)
		if err != nil {
			return "", err
		}
		if validatedOverride == "" {
			return "", errors.New("memory path override is empty")
		}
		return strings.TrimSuffix(validatedOverride, string(os.PathSeparator)), nil
	}
	baseRoot = strings.TrimSpace(baseRoot)
	if baseRoot == "" {
		return "", errors.New("memory root dir is empty")
	}
	if projectRoot = strings.TrimSpace(projectRoot); projectRoot != "" {
		if root, err := GetAutoMemPath(baseRoot, projectRoot); err == nil && strings.TrimSpace(root) != "" {
			return root, nil
		}
	}
	return baseRoot, nil
}

func buildExplicitMemoryWrite(intent SaveIntent) MemoryWriteRequest {
	content := normalizeHookContent(intent.Content)
	memoryType := intent.Type
	if !memoryType.IsKnown() {
		memoryType = inferMemoryType(content)
	}
	description := buildExplicitMemoryDescription(content)
	return MemoryWriteRequest{
		Name:        buildExplicitMemoryName(memoryType, description),
		Description: description,
		Type:        memoryType,
		Body:        buildExplicitMemoryBody(memoryType, content),
	}
}

func buildExplicitMemoryDescription(content string) string {
	description := truncateRunes(firstNonEmptyLine(content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(content, memoryHookMaxRunes)
	}
	return description
}

func buildExplicitMemoryBody(memoryType MemoryType, content string) string {
	content = strings.TrimSpace(content)
	switch memoryType {
	case MemoryTypeFeedback:
		if hasStructuredMemorySection(content, "why") && hasStructuredMemorySection(content, "how to apply") {
			return content
		}
		return strings.Join([]string{
			content,
			"Why: user explicitly asked to remember this working guidance.",
			"How to apply: follow this guidance when future work touches the same area.",
		}, "\n")
	case MemoryTypeProject:
		if hasStructuredMemorySection(content, "why") && hasStructuredMemorySection(content, "how to apply") {
			return content
		}
		return strings.Join([]string{
			content,
			"Why: user explicitly asked to preserve this project context.",
			"How to apply: use this context when making project recommendations or planning follow-up work.",
		}, "\n")
	default:
		return content
	}
}

func buildExplicitMemoryName(memoryType MemoryType, description string) string {
	prefix := "Saved memory"
	switch memoryType {
	case MemoryTypeUser:
		prefix = "User note"
	case MemoryTypeFeedback:
		prefix = "Feedback rule"
	case MemoryTypeProject:
		prefix = "Project note"
	case MemoryTypeReference:
		prefix = "Reference note"
	}
	if description == "" {
		return prefix
	}
	return truncateRunes(prefix+": "+description, 96)
}

func normalizeHookContent(text string) string {
	lines := make([]string, 0, 4)
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• "))
		line = strings.TrimSpace(strings.TrimLeft(line, ":：-—,，。.!！?？;；"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "that "))
		line = strings.TrimSpace(strings.TrimPrefix(line, "关于 "))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func hasMeaningfulMemoryContent(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}) >= 0
}

func inferMemoryType(text string) MemoryType {
	normalized := CanonicalName(strings.ToLower(strings.ReplaceAll(text, "\n", " ")))
	if normalized == "" {
		return MemoryTypeUser
	}
	if strings.Contains(normalized, "http://") || strings.Contains(normalized, "https://") {
		return MemoryTypeReference
	}
	typeScores := map[MemoryType]int{
		MemoryTypeUser:      keywordScore(normalized, "prefer", "preference", "style", "tone", "habit", "偏好", "风格", "习惯", "喜欢", "回答尽量", "回复尽量"),
		MemoryTypeFeedback:  keywordScore(normalized, "rule", "workflow", "always", "never", "avoid", "规范", "规则", "流程", "约定", "务必", "不要", "how to apply"),
		MemoryTypeProject:   keywordScore(normalized, "project", "phase", "milestone", "deadline", "owner", "incident", "项目", "阶段", "里程碑", "截止", "负责人", "事故", "发布日期"),
		MemoryTypeReference: keywordScore(normalized, "docs", "doc", "wiki", "runbook", "notion", "slack", "grafana", "dashboard", "文档", "链接", "地址", "手册"),
	}
	bestType := MemoryTypeUser
	bestScore := -1
	for _, candidate := range []MemoryType{MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference} {
		if score := typeScores[candidate]; score > bestScore {
			bestType = candidate
			bestScore = score
		}
	}
	return bestType
}

func keywordScore(text string, keywords ...string) int {
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(text, CanonicalName(keyword)) {
			score++
		}
	}
	return score
}

func (h *MemoryLifecycleHooks) intentDiskStores(ctx context.Context, threadID string, memoryType MemoryType) (memoryStructuredStore, memoryStructuredStore, error) {
	privateStore, err := h.diskStore()
	if err != nil {
		return nil, nil, err
	}
	teamStore, err := h.teamDiskStore(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if teamStore == nil {
		return privateStore, nil, nil
	}
	if defaultTeamMemoryType(memoryType) {
		return teamStore, privateStore, nil
	}
	return privateStore, teamStore, nil
}

func (h *MemoryLifecycleHooks) teamDiskStore(ctx context.Context, threadID string) (memoryStructuredStore, error) {
	if h == nil || h.team == nil {
		return nil, nil
	}
	buildCtx := h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(threadID)).buildCtx()
	if !h.team.IsTeamMemoryEnabled(buildCtx) {
		return nil, nil
	}
	root, err := configuredTeamMemPath(h.team, buildCtx)
	if err != nil {
		return nil, err
	}
	return newDiskStoreWithGuard(root, NewTeamMemoryGuard(h.team))
}

func selectExplicitWriteStore(name string, primary, secondary memoryStructuredStore) (memoryStructuredStore, error) {
	for _, store := range []memoryStructuredStore{primary, secondary} {
		if store == nil {
			continue
		}
		if _, err := store.Read(name); err == nil {
			return store, nil
		} else if !errors.Is(err, ErrMemoryNotFound) {
			return nil, err
		}
	}
	if primary != nil {
		return primary, nil
	}

	return nil, errors.New("memory store is nil")
}

// upsertStructuredMemory writes the entry atomically via the store's
// UpsertStructured implementation, which acquires the disk store lock
// once for the full check-and-write sequence. Phase 自有.1a replaced the
// previous Create-then-Update pattern, where two independent lock
// acquisitions left a window for a racing writer to convert a
// Create-failed-with-AlreadyExists into an Update that overwrote
// concurrently-written content.
func upsertStructuredMemory(store memoryStructuredStore, entry MemoryWriteRequest, options WriteOptions) error {
	if store == nil {
		return errors.New("memory store is nil")
	}
	_, err := store.UpsertStructured(entry, options)
	return err
}

func deleteMemoryAcrossStores(name string, options WriteOptions, stores ...memoryStructuredStore) error {
	deleted := false
	for _, store := range stores {
		if store == nil {
			continue
		}
		if err := store.Delete(name, options); err == nil {
			deleted = true
			continue
		} else if !errors.Is(err, ErrMemoryNotFound) {
			return err
		}
	}
	if deleted {
		return nil
	}
	return ErrMemoryNotFound
}

func defaultTeamMemoryType(memoryType MemoryType) bool {
	switch ParseMemoryType(string(memoryType)) {
	case MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}
