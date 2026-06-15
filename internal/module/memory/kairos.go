package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

const dateChangeAttachmentKind = "date_change"

var kairosOverviewLines = []string{
	"KAIROS mode continuously records durable remember-worthy facts to today's append-only daily log instead of editing topic files inline.",
	"The daily log lives under `logs/YYYY/MM/YYYY-MM-DD.md` inside the auto-memory root.",
	"`MEMORY.md` remains a read-only orientation summary in KAIROS: read it for context, but never edit it inline during the live session.",
	"Only root-thread auto-memory sessions write to that log; child agents do not.",
}

var kairosWriteRules = []string{
	"Whenever durable remember-worthy information becomes clear, append exactly one new bullet to today's log using `- [HH:MM] content` in local time.",
	"Explicit `remember` requests are a strong signal, but KAIROS should keep recording other durable facts, preferences, project notes, workflow rules, and reference pointers as they emerge.",
	"Create the parent directories and today's file on first write if needed.",
	"Never rewrite, reorder, deduplicate, or retroactively edit older bullets in the daily log; it is append-only.",
	"Keep each bullet self-contained, convert relative dates to absolute dates, and make the durable fact understandable when read later in isolation.",
}

var kairosConsolidationLines = []string{
	"When the date changes, switch to a new daily log file for the new day.",
	"Runtime may surface a `date_change` attachment instead of rebuilding cached base instructions; use the new date silently.",
	"Later `/dream` or manual consolidation distills daily logs into typed topic files and refreshes `MEMORY.md`.",
	"Treat daily logs as recent signal, not guaranteed current truth; verify current state before acting on time-sensitive details.",
}

var kairosAckPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*(?:i(?:'|’)ll remember|i will remember|i(?:'|’)ve noted|noted|saved to memory|saved in memory|i(?:'|’)ll save this to memory)\s*(?:that\s+)?(?:[:：\-—,，]\s*|\s+)?(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:记住了|已记住|我会记住|已经记住|已保存到记忆|保存到记忆了|帮你记住了)\s*(?:这个|这点|了)?\s*(?:[:：\-—,，]\s*|\s+)?(.+?)\s*$`),
}

// BuildDailyLogPrompt 构建daily日志prompt。
func BuildDailyLogPrompt(skipIndex, searchPastContextEnabled bool, extraGuidelines []string) string {
	sections := []string{
		renderSection("### 1. KAIROS daily log mode", kairosOverviewLines),
		renderSection("### 2. append-only write protocol", append(cloneStrings(kairosWriteRules), kairosSkipIndexRule(skipIndex))),
		renderSection("### 3. memory taxonomy to preserve", kairosTaxonomyHints()),
		renderSection("### 4. exclusions", standardExclusionRules),
		renderSection("### 5. trust, rollover, and consolidation", kairosConsolidationLines),
	}
	if extra := normalizeStringSlice(extraGuidelines); len(extra) > 0 {
		sections = append(sections, renderSection("### 6. extra guidelines", extra))
		if section := searchingPastContextSection("### 7. searching past context", searchPastContextEnabled); section != "" {
			sections = append(sections, section)
		}
		return strings.Join(nonEmpty(sections), "\n\n")
	}
	if section := searchingPastContextSection("### 6. searching past context", searchPastContextEnabled); section != "" {
		sections = append(sections, section)
	}
	return strings.Join(nonEmpty(sections), "\n\n")
}

func kairosTaxonomyHints() []string {
	lines := append([]string(nil), resolvedRuleEngine(nil).taxonomyLines()...)
	lines = append(lines, "Write the durable fact in natural language now; later consolidation will normalize it into typed memory files.")
	return lines
}

func kairosSkipIndexRule(skipIndex bool) string {
	if skipIndex {
		return "`skipIndex` does not change KAIROS logging: the daily log stays append-only and any `MEMORY.md` rebuild remains deferred to consolidation."
	}
	return "KAIROS logging never updates `MEMORY.md` inline; consolidation later refreshes topic files and `MEMORY.md` from the append-only log."
}

func getAutoMemDailyLogPath(baseRoot, projectRoot string, now time.Time) (string, error) {
	root, err := GetAutoMemPath(baseRoot, projectRoot)
	if err != nil {
		return "", err
	}
	return autoMemDailyLogPath(root, now), nil
}

func autoMemDailyLogPath(root string, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return filepath.Join(strings.TrimSpace(root), "logs", now.Format("2006"), now.Format("01"), now.Format("2006-01-02")+".md")
}

func autoMemDailyLogRelativePath(now time.Time) string {
	return filepath.ToSlash(filepath.Join("logs", now.Format("2006"), now.Format("01"), now.Format("2006-01-02")+".md"))
}

// DetectKairosWriteIntent 处理detectkairoswriteintent。
func DetectKairosWriteIntent(evt turndto.TurnCompleted) SaveIntent {
	for _, text := range kairosIntentTexts(evt) {
		if intent := detectKairosAcknowledgement(text); intent.Detected {
			return intent
		}
	}
	for _, text := range kairosIntentTexts(evt) {
		if intent := DetectSaveIntent(text); intent.Detected {
			return intent
		}
	}
	for _, text := range kairosIntentTexts(evt) {
		item, ok := internalMemoryFromMessage(dto.Message{Role: "assistant", Content: text})
		if !ok {
			continue
		}
		content := normalizeHookContent(item.Content)
		if hasMeaningfulMemoryContent(content) {
			return SaveIntent{Detected: true, Content: content, Type: item.Type}
		}
	}
	return SaveIntent{}
}

func detectKairosAcknowledgement(text string) SaveIntent {
	text = strings.TrimSpace(text)
	if text == "" {
		return SaveIntent{}
	}
	for _, pattern := range kairosAckPatterns {
		matches := pattern.FindStringSubmatch(text)
		if len(matches) == 0 {
			continue
		}
		content := normalizeHookContent(matches[len(matches)-1])
		if hasMeaningfulMemoryContent(content) {
			return SaveIntent{Detected: true, Content: content, Type: inferMemoryType(content)}
		}
	}
	return SaveIntent{}
}

func kairosIntentTexts(evt turndto.TurnCompleted) []string {
	return uniqueNonEmptyStrings([]string{evt.Message, evt.Summary, evt.Result})
}

func (h *MemoryLifecycleHooks) writeDetectedIntent(ctx context.Context, evt turndto.TurnCompleted, intent SaveIntent) error {
	written, err := h.tryAppendKairosDailyLog(ctx, evt, intent)
	if err != nil {
		return err
	}
	if written {
		h.invalidateMemorySections()
		return nil
	}
	if err := h.writeIntent(ctx, evt.ThreadID, intent); err != nil {
		return err
	}
	h.invalidateMemorySections()
	return nil
}

// tryAppendKairosDailyLog 处理tryappendkairosdaily日志。
func (h *MemoryLifecycleHooks) tryAppendKairosDailyLog(ctx context.Context, evt turndto.TurnCompleted, intent SaveIntent) (bool, error) {
	threadID := strings.TrimSpace(evt.ThreadID)
	if h == nil || threadID == "" {
		return false, nil
	}
	meta := h.resolveThreadRuntimeMetadata(ctx, threadID)
	gate := ResolveMemoryGate(meta.buildCtx(), h.cfg)
	if !gate.KairosActive || !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
		return false, nil
	}
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return false, err
	}
	now := h.now()
	if err := appendDailyLogEntry(root, autoMemDailyLogPath(root, now), now, intent.Content); err != nil {
		return false, err
	}
	return true, nil
}

func (m threadRuntimeMetadata) buildCtx() contract.BuildCtx {
	return contract.BuildCtx{SessionFlags: cloneBoolMap(m.sessionFlags)}
}

// isAutoMemoryRootThread 判断auto记忆根目录线程是否可用。
func (m threadRuntimeMetadata) isAutoMemoryRootThread() bool {
	if !m.resolved || m.bareMode || strings.TrimSpace(m.parentAgentID) != "" || strings.TrimSpace(m.ownerThreadID) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(m.threadKind)) {
	case "", "main", "root":
		return true
	default:
		return false
	}
}

func (m threadRuntimeMetadata) hasAgentMemoryScope() bool {
	return strings.TrimSpace(m.agentMemoryScope) != ""
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]bool, len(src))
	for key, value := range src {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (h *MemoryLifecycleHooks) now() time.Time {
	if h == nil || h.timeNow == nil {
		return time.Now()
	}
	return h.timeNow()
}

// appendDailyLogEntry 追加daily日志条目。
func appendDailyLogEntry(root, path string, now time.Time, content string) error {
	entry := formatDailyLogEntry(now, content)
	if entry == "" {
		return nil
	}
	validatedPath, err := ValidateMemoryWritePath(root, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(validatedPath), 0o755); err != nil {
		return err
	}
	prefix, err := dailyLogSeparator(validatedPath)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(validatedPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(prefix + entry + "\n")
	return err
}

func formatDailyLogEntry(now time.Time, content string) string {
	content = normalizeDailyLogContent(content)
	if content == "" {
		return ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("- [%s] %s", now.Format("15:04"), content)
}

func normalizeDailyLogContent(text string) string {
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\n", " ")
	return strings.Join(strings.Fields(replacer.Replace(strings.TrimSpace(text))), " ")
}

// dailyLogSeparator 生成 daily log 条目分隔线。
func dailyLogSeparator(path string) (string, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	case info.Size() == 0:
		return "", nil
	}
	last, err := readLastByte(path)
	if err != nil {
		return "", err
	}
	if last == '\n' {
		return "", nil
	}
	return "\n", nil
}

func readLastByte(path string) (byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if _, err := file.Seek(-1, 2); err != nil {
		return 0, err
	}
	buf := []byte{0}
	if _, err := file.Read(buf); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func (p *MemoryContextProvider) appendKairosDateChangeAttachment(
	threadID string,
	gate MemoryGateSnapshot,
	attachments []dto.AttachmentEnvelope,
) []dto.AttachmentEnvelope {
	if !gate.KairosActive {
		return attachments
	}
	attachment, ok := p.nextKairosDateChangeAttachment(threadID, p.now())
	if !ok {
		return attachments
	}
	return append(attachments, attachment)
}

func (p *MemoryContextProvider) nextKairosDateChangeAttachment(threadID string, now time.Time) (dto.AttachmentEnvelope, bool) {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return dto.AttachmentEnvelope{}, false
	}
	current := now.Format("2006-01-02")
	p.mu.Lock()
	state := p.turnStateLocked(threadID)
	previous := strings.TrimSpace(state.lastDate)
	state.lastDate = current
	p.mu.Unlock()
	if previous == "" || previous == current {
		return dto.AttachmentEnvelope{}, false
	}
	return buildDateChangeAttachment(p.memoryRoot, now), true
}

func buildDateChangeAttachment(memoryRoot string, now time.Time) dto.AttachmentEnvelope {
	path := autoMemDailyLogRelativePath(now)
	if root := strings.TrimSpace(memoryRoot); root != "" {
		path = relativeMemoryPath(root, autoMemDailyLogPath(root, now))
	}
	day := now.Format("2006-01-02")
	return dto.AttachmentEnvelope{
		Kind:      dateChangeAttachmentKind,
		Path:      path,
		Header:    "Date change",
		Content:   fmt.Sprintf("The date has changed. Today's date is now %s. Do not mention this to the user explicitly; just use the new date for time-sensitive reasoning and KAIROS daily-log writes.", day),
		MtimeMs:   now.UnixMilli(),
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
}

// FeedbackTracker accumulates feedback memories by topic key.
// When count reaches threshold, the caller can trigger a skill proposal.
type FeedbackTracker struct {
	mu        sync.Mutex
	threshold int
	groups    map[string][]ExtractedMemory
}

// NewFeedbackTracker 创建feedbacktracker。
func NewFeedbackTracker(threshold int) *FeedbackTracker {
	if threshold < 2 {
		threshold = 2
	}
	return &FeedbackTracker{
		threshold: threshold,
		groups:    make(map[string][]ExtractedMemory),
	}
}

// Record 记录记忆。
func (ft *FeedbackTracker) Record(topicKey string, mem ExtractedMemory) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.groups[topicKey] = append(ft.groups[topicKey], mem)
}

// Count 统计记忆。
func (ft *FeedbackTracker) Count(topicKey string) int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.groups[topicKey])
}

// ThresholdReached reports whether the topic has accumulated enough
// feedback to trigger a skill proposal.
// ThresholdReached 处理thresholdreached。
func (ft *FeedbackTracker) ThresholdReached(topicKey string) bool {
	return ft.Count(topicKey) >= ft.threshold
}

// GetGroup returns a snapshot of the feedback entries for a topic.
// GetGroup 读取group。
func (ft *FeedbackTracker) GetGroup(topicKey string) []ExtractedMemory {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	src := ft.groups[topicKey]
	cp := make([]ExtractedMemory, len(src))
	copy(cp, src)
	return cp
}

// MarkProposed clears the group for a topic after a proposal has been
// generated, preventing duplicate proposals.
// MarkProposed 标记proposed。
func (ft *FeedbackTracker) MarkProposed(topicKey string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	delete(ft.groups, topicKey)
}

// FeedbackTopicSlug normalises a feedback memory name into a short,
// stable topic key used for grouping related feedback.
// FeedbackTopicSlug 处理feedbacktopicslug。
func FeedbackTopicSlug(name string) string {
	lower := strings.ToLower(name)
	var parts []string
	var current []rune
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
		} else if len(current) > 0 {
			parts = append(parts, string(current))
			current = current[:0]
		}
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}
	filtered := filterFeedbackStopWords(parts)
	if len(filtered) > 4 {
		filtered = filtered[:4]
	}
	return strings.Join(filtered, "-")
}

var feedbackStopWords = map[string]bool{
	"的": true, "是": true, "在": true, "了": true, "和": true,
	"与": true, "或": true, "等": true, "时": true, "要": true,
	"a": true, "an": true, "the": true, "to": true, "of": true,
	"and": true, "or": true, "for": true, "with": true, "this": true,
	"that": true, "from": true, "when": true, "must": true,
}

func filterFeedbackStopWords(words []string) []string {
	var out []string
	for _, w := range words {
		if !feedbackStopWords[w] && len(w) > 0 {
			out = append(out, w)
		}
	}
	return out
}

// LoadFromDir 从目录加载记忆。
func (ft *FeedbackTracker) LoadFromDir(rootDir string) int {
	if rootDir == "" {
		return 0
	}
	loaded := 0
	for _, sub := range []string{"", "team", "feedback", filepath.Join("team", "feedback")} {
		dir := rootDir
		if sub != "" {
			dir = filepath.Join(rootDir, sub)
		}
		loaded += ft.scanDirForFeedback(dir)
	}
	return loaded
}

// scanDirForFeedback 为feedback扫描目录。
func (ft *FeedbackTracker) scanDirForFeedback(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	loaded := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "MEMORY.md" {
			continue
		}
		if mem, ok := parseFeedbackFile(filepath.Join(dir, e.Name())); ok {
			ft.Record(FeedbackTopicSlug(mem.Content), mem)
			loaded++
		}
	}
	return loaded
}

// parseFeedbackFile 解析feedback文件。
func parseFeedbackFile(path string) (ExtractedMemory, bool) {
	parsed, err := ParseMemoryFile(path)
	if err != nil || parsed == nil {
		return ExtractedMemory{}, false
	}
	if parsed.Frontmatter.Type == nil || *parsed.Frontmatter.Type != MemoryTypeFeedback {
		return ExtractedMemory{}, false
	}
	slug := FeedbackTopicSlug(parsed.Content)
	if slug == "" {
		return ExtractedMemory{}, false
	}
	return ExtractedMemory{Type: MemoryTypeFeedback, Content: parsed.Content}, true
}

// trackFeedbackIfApplicable is called after a successful memory write.
// It records feedback-type intents and fires the threshold callback.
// trackFeedbackIfApplicable 跟踪feedbackifapplicable。
func trackFeedbackIfApplicable(h *MemoryLifecycleHooks, intent SaveIntent) {
	if h == nil || h.feedbackTracker == nil || intent.Type != MemoryTypeFeedback {
		return
	}
	slug := FeedbackTopicSlug(intent.Content)
	if slug == "" {
		return
	}
	h.feedbackTracker.Record(slug, ExtractedMemory{Type: intent.Type, Content: intent.Content})
	if h.feedbackTracker.ThresholdReached(slug) && h.onFeedbackThreshold != nil {
		group := h.feedbackTracker.GetGroup(slug)
		h.feedbackTracker.MarkProposed(slug)
		go h.onFeedbackThreshold(slug, group)
	}
}
