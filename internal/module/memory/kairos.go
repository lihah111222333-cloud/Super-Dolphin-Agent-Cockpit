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
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

const dateChangeAttachmentKind = "date_change"

var kairosOverviewLines = []string{
	"KAIROS mode writes durable remember-worthy facts to today's append-only daily log instead of editing topic files inline.",
	"The daily log lives under `logs/YYYY/MM/YYYY-MM-DD.md` inside the auto-memory root.",
	"Only root-thread auto-memory sessions write to that log; child agents and agent-memory scopes do not.",
}

var kairosWriteRules = []string{
	"When the user explicitly asks you to remember something, append exactly one new bullet to today's log using `- [HH:MM] content` in local time.",
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

func BuildDailyLogPrompt(skipIndex bool, extraGuidelines []string) string {
	sections := []string{
		renderSection("### 1. KAIROS daily log mode", kairosOverviewLines),
		renderSection("### 2. append-only write protocol", append(cloneStrings(kairosWriteRules), kairosSkipIndexRule(skipIndex))),
		renderSection("### 3. memory taxonomy to preserve", kairosTaxonomyHints()),
		renderSection("### 4. exclusions", standardExclusionRules),
		renderSection("### 5. trust, rollover, and consolidation", kairosConsolidationLines),
	}
	if extra := normalizeStringSlice(extraGuidelines); len(extra) > 0 {
		sections = append(sections, renderSection("### 6. extra guidelines", extra))
		sections = append(sections, renderSection("### 7. searching past context", standardSearchingPastContextRules))
		return strings.Join(nonEmpty(sections), "\n\n")
	}
	sections = append(sections, renderSection("### 6. searching past context", standardSearchingPastContextRules))
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

func (h *MemoryLifecycleHooks) writeDetectedIntent(ctx context.Context, evt turndto.TurnCompleted, intent SaveIntent) error {
	if written, err := h.tryAppendKairosDailyLog(ctx, evt, intent); written || err != nil {
		return err
	}
	return h.writeIntent(intent)
}

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
