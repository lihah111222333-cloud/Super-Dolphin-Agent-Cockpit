package memory

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sort"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

const (
	maxRenderedMemoryRunes     = 720
	maxRenderedTranscriptRunes = 480
	defaultTranscriptLimit     = 3
)

type transcriptSnippet struct {
	Role      string
	Content   string
	Timestamp time.Time
}

type scoredTranscriptSnippet struct {
	snippet transcriptSnippet
	score   int
}

func freezeRelevantMemoryAttachments(entries []MemoryEntry, now time.Time) []dto.AttachmentEnvelope {
	attachments := make([]dto.AttachmentEnvelope, 0, len(entries))
	for _, entry := range entries {
		attachment, ok := relevantMemoryAttachment(entry, now)
		if !ok {
			continue
		}
		attachments = append(attachments, attachment)
	}
	if len(attachments) == 0 {
		return nil
	}
	return attachments
}

func freezeTranscriptInputs(snippets []transcriptSnippet) []shareddto.InputItem {
	items := make([]shareddto.InputItem, 0, len(snippets))
	for idx, snippet := range snippets {
		content := renderTranscriptBlock(snippet)
		if content == "" {
			continue
		}
		items = append(items, shareddto.InputItem{
			Type:    "filecontent",
			Name:    transcriptLabel(snippet, idx),
			Content: content,
		})
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func relevantMemoryAttachment(entry MemoryEntry, now time.Time) (dto.AttachmentEnvelope, bool) {
	body, truncated := truncateRenderedTextWithFlag(memoryRenderBody(entry), maxRenderedMemoryRunes)
	if body == "" {
		return dto.AttachmentEnvelope{}, false
	}
	attachment := dto.NewRelevantMemoryAttachment(
		memoryDisplayPath(entry),
		memoryHeader(now, entry),
		body,
		entry.UpdatedAt,
		maxRenderedMemoryRunes,
		truncated,
	).Envelope()
	return attachment, attachment.IsValid()
}

func memoryAgeDays(now, updatedAt time.Time) int {
	if updatedAt.IsZero() {
		return -1
	}
	loc := now.Location()
	if loc == nil {
		loc = time.UTC
	}
	nowDay := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	savedDay := time.Date(updatedAt.In(loc).Year(), updatedAt.In(loc).Month(), updatedAt.In(loc).Day(), 0, 0, 0, 0, loc)
	if savedDay.After(nowDay) {
		return 0
	}
	return int(nowDay.Sub(savedDay).Hours() / 24)
}

func memoryAge(now, updatedAt time.Time) string {
	switch days := memoryAgeDays(now, updatedAt); {
	case days < 0:
		return ""
	case days == 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days == 2:
		return "2 days ago"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

func memoryFreshnessText(now, updatedAt time.Time) string {
	if memoryAgeDays(now, updatedAt) <= 1 {
		return ""
	}
	age := memoryAge(now, updatedAt)
	if age == "" {
		age = "some time ago"
	}
	return "This memory was saved " + age + ", so it may not reflect live state. File or line references may be outdated; verify the current code before relying on it."
}

func memoryHeader(now time.Time, entry MemoryEntry) string {
	path := memoryDisplayPath(entry)
	switch memoryAgeDays(now, entry.UpdatedAt) {
	case 0:
		return "Memory (saved today): " + path + ":"
	case 1:
		return "Memory (saved yesterday): " + path + ":"
	}
	warning := memoryFreshnessText(now, entry.UpdatedAt)
	if warning == "" {
		return "Memory: " + path + ":"
	}
	return warning + "\n\nMemory: " + path + ":"
}

func memoryDisplayPath(entry MemoryEntry) string {
	path := strings.TrimSpace(filepath.ToSlash(entry.FilePath))
	if path == "" {
		name := strings.TrimSpace(entry.Frontmatter.Name)
		if name == "" {
			base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(entry.FilePath), filepath.Ext(entry.FilePath)))
			if base == "" {
				return "memory note"
			}
			return base
		}
		return name
	}
	return path
}

func renderRelevantMemoryBlock(entry MemoryEntry, now time.Time) string {
	attachment, ok := relevantMemoryAttachment(entry, now)
	if !ok {
		return ""
	}
	return attachment.Header + "\n" + attachment.Content
}

func renderTranscriptBlock(snippet transcriptSnippet) string {
	body := truncateRenderedText(strings.TrimSpace(snippet.Content), maxRenderedTranscriptRunes)
	if body == "" {
		return ""
	}
	return transcriptHeader(snippet) + "\n" + body
}

func memoryRenderBody(entry MemoryEntry) string {
	frontmatter := relevantMemoryFrontmatter(entry)
	body := strings.TrimSpace(entry.Content)
	switch {
	case frontmatter == "":
		return body
	case body == "":
		return frontmatter
	default:
		return frontmatter + "\n\n" + body
	}
}

func relevantMemoryFrontmatter(entry MemoryEntry) string {
	lines := make([]string, 0, 5)
	if name := strings.TrimSpace(entry.Frontmatter.Name); name != "" {
		lines = append(lines, "name: "+strconv.Quote(name))
	}
	if description := strings.TrimSpace(entry.Frontmatter.Description); description != "" {
		lines = append(lines, "description: "+strconv.Quote(description))
	}
	if entry.Frontmatter.Type != nil {
		if raw := strings.TrimSpace(string(*entry.Frontmatter.Type)); raw != "" {
			lines = append(lines, "type: "+strconv.Quote(raw))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	lines = append([]string{"---"}, lines...)
	lines = append(lines, "---")
	return strings.Join(lines, "\n")
}

func transcriptHeader(snippet transcriptSnippet) string {
	header := "Past context transcript"
	if role := strings.TrimSpace(snippet.Role); role != "" {
		header += " (" + role + ")"
	}
	if !snippet.Timestamp.IsZero() {
		header += " — " + snippet.Timestamp.Format(time.RFC3339)
	}
	return header + ":"
}

func transcriptLabel(snippet transcriptSnippet, idx int) string {
	role := strings.ToLower(strings.TrimSpace(snippet.Role))
	if role == "" {
		role = "snippet"
	}
	return role + "-past-context-" + string(rune('a'+idx)) + ".txt"
}

func shouldSearchPastContextQuery(query string) bool {
	normalized, _ := searchTerms(query)
	return len([]rune(normalized)) >= 4
}

func memoryRetrievalLowConfidence(query string, entries []MemoryEntry) bool {
	if len(entries) == 0 {
		return true
	}
	normalized, terms := searchTerms(query)
	if normalized == "" {
		return false
	}
	best := 0
	for _, entry := range entries {
		if score := scoreMemoryEntry(normalized, terms, entry); score > best {
			best = score
		}
	}
	return best < 18
}

func searchTranscriptSnippets(query string, messages []dto.Message, budget int) []transcriptSnippet {
	normalized, terms := searchTerms(query)
	if normalized == "" || len(messages) == 0 {
		return nil
	}
	ranked := rankTranscriptSnippets(normalized, terms, messages)
	if len(ranked) == 0 {
		return nil
	}
	return selectTranscriptSnippets(ranked, budget)
}

func rankTranscriptSnippets(normalized string, terms []string, messages []dto.Message) []scoredTranscriptSnippet {
	ranked := make([]scoredTranscriptSnippet, 0, len(messages))
	for _, message := range messages {
		score := scoreTranscriptMessage(normalized, terms, message)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, scoredTranscriptSnippet{
			snippet: transcriptSnippet{
				Role:      strings.TrimSpace(message.Role),
				Content:   strings.TrimSpace(message.Content),
				Timestamp: message.Timestamp,
			},
			score: score,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].snippet.Timestamp.After(ranked[j].snippet.Timestamp)
	})
	return ranked
}

func selectTranscriptSnippets(ranked []scoredTranscriptSnippet, budget int) []transcriptSnippet {
	if budget <= 0 {
		budget = defaultRelevantMemoryBudgetBytes / 2
	}
	remaining := budget
	seen := make(map[string]struct{}, len(ranked))
	selected := make([]transcriptSnippet, 0, minInt(len(ranked), defaultTranscriptLimit))
	for _, item := range ranked {
		if len(selected) >= defaultTranscriptLimit || remaining <= 0 {
			break
		}
		key := CanonicalName(item.snippet.Role + "\n" + item.snippet.Content)
		if _, ok := seen[key]; ok {
			continue
		}
		size := len([]byte(strings.TrimSpace(item.snippet.Content)))
		if size > remaining {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, item.snippet)
		remaining -= size
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func scoreTranscriptMessage(normalized string, terms []string, message dto.Message) int {
	content := CanonicalName(message.Content)
	if content == "" {
		return 0
	}
	fields := []string{content}
	score := matchWeight(fields, normalized, 16)
	for _, term := range terms {
		score += matchWeight(fields, term, 6)
	}
	if score > 0 {
		score += transcriptMatchedTerms(fields, terms) * 2
	}
	return score
}

func transcriptMatchedTerms(fields []string, terms []string) int {
	matched := 0
	for _, term := range terms {
		if matchWeight(fields, term, 1) > 0 {
			matched++
		}
	}
	return matched
}

func truncateRenderedText(text string, limit int) string {
	truncated, _ := truncateRenderedTextWithFlag(text, limit)
	return truncated
}

func truncateRenderedTextWithFlag(text string, limit int) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	return strings.TrimSpace(string(runes[:limit])) + "…", true
}
