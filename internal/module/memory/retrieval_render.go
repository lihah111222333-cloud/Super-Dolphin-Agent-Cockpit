package memory

import (
	"path/filepath"
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

func freezeRelevantMemoryInputs(entries []MemoryEntry, now time.Time) []shareddto.InputItem {
	items := make([]shareddto.InputItem, 0, len(entries))
	for _, entry := range entries {
		content := renderRelevantMemoryBlock(entry, now)
		if content == "" {
			continue
		}
		items = append(items, shareddto.InputItem{
			Type:    "filecontent",
			Name:    filepath.Base(memoryDisplayPath(entry)),
			Content: content,
		})
	}
	if len(items) == 0 {
		return nil
	}
	return items
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

func renderRelevantMemoryBlock(entry MemoryEntry, now time.Time) string {
	body := truncateRenderedText(memoryRenderBody(entry), maxRenderedMemoryRunes)
	if body == "" {
		return ""
	}
	return memoryHeader(now, entry) + "\n" + body
}

func renderTranscriptBlock(snippet transcriptSnippet) string {
	body := truncateRenderedText(strings.TrimSpace(snippet.Content), maxRenderedTranscriptRunes)
	if body == "" {
		return ""
	}
	return transcriptHeader(snippet) + "\n" + body
}

func memoryRenderBody(entry MemoryEntry) string {
	if text := strings.TrimSpace(entry.Content); text != "" {
		return text
	}
	return strings.TrimSpace(entry.Frontmatter.Description)
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
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
