package thread

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// mentionRe matches @mention prefixes like "@头脑风暴，" or "@brainstorm, "
var mentionRe = regexp.MustCompile(`^@[^，,、\s]+[，,、\s]+`)

// fencedCodeBlockRe strips triple-backtick fenced code blocks (multiline)
var fencedCodeBlockRe = regexp.MustCompile("(?s)```[^`]*```")

// backtickStripRe strips backtick-wrapped segments (inline code)
var backtickStripRe = regexp.MustCompile("`[^`]*`")

// sentenceSplitRe splits on Chinese/English sentence-ending punctuation
var sentenceSplitRe = regexp.MustCompile(`[。？！?!,，\n]`)

// fillerPrefixes are leading filler phrases to strip (longer first to avoid partial matches).
// Note: single-character words like "看" are not stripped to preserve meaningful verbs.
var fillerPrefixes = []string{
	"能不能帮我", "能不能", "帮我一下", "帮我", "请帮我", "请",
	"你能", "你帮我", "你", "看看", "我想", "我要",
	"给我",
}

// chineseFillerWords are particles/adverbs to drop from within the text.
// "一下" is included here so it's removed in-place rather than as a prefix.
var chineseFillerWords = []string{
	"一下", "的", "了", "吧", "啊", "呢", "嘛", "哦", "嗯", "吗",
}

// chinesePronouns — results consisting only of these are treated as too vague
var chinesePronouns = map[string]bool{
	"这":  true,
	"这个": true,
	"那":  true,
	"那个": true,
	"它":  true,
	"他":  true,
	"她":  true,
	"我":  true,
	"你":  true,
	"这些": true,
	"那些": true,
}

// englishFillerWords are articles/prepositions to drop from pure-English text
var englishFillerWords = map[string]bool{
	"the": true, "a": true, "an": true, "in": true, "at": true,
	"on": true, "of": true, "to": true, "for": true, "and": true,
	"or": true, "is": true, "are": true, "was": true, "were": true,
	"it": true, "this": true, "that": true, "with": true,
}

// ExtractTitle extracts a ≤8 display-unit title from the first user prompt.
// Returns "" when the result is too short or too vague (caller should use defaultThreadName).
func ExtractTitle(prompt string) string {
	if prompt == "" {
		return ""
	}

	// 1. Strip @mention prefix
	prompt = mentionRe.ReplaceAllString(strings.TrimSpace(prompt), "")
	prompt = strings.TrimSpace(prompt)

	// 2. Strip leading filler prefixes (before sentence split so "帮我，X" → "X")
	prompt = removeFiller(prompt)
	prompt = strings.TrimSpace(prompt)

	// 3. Remove fenced code blocks (triple-backtick) then inline code (single-backtick)
	prompt = fencedCodeBlockRe.ReplaceAllString(prompt, "")
	prompt = backtickStripRe.ReplaceAllString(prompt, "")
	prompt = strings.TrimSpace(prompt)

	// 4. Split on sentence boundaries; take first non-empty sentence
	parts := sentenceSplitRe.Split(prompt, -1)
	sentence := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			sentence = p
			break
		}
	}
	if sentence == "" {
		return ""
	}

	// (filler already stripped in step 2; no second pass needed)

	// 5. Remove Chinese filler particles/adverbs
	sentence = removeChineseParticles(sentence)

	// 6. For pure-English text, remove common stop words
	if !containsChinese(sentence) {
		sentence = removeEnglishFillers(sentence)
	}

	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return ""
	}

	// 7. Truncate to ≤8 display units
	sentence = truncateToUnits(sentence, 8)

	// 8. Fallback: too short or all pronouns
	if countDisplayUnits(sentence) <= 2 || isAllPronouns(sentence) {
		return ""
	}

	return sentence
}

// removeFiller strips known leading filler phrases from s (recursive to handle stacking).
func removeFiller(s string) string {
	for _, prefix := range fillerPrefixes {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			s = strings.TrimSpace(s)
			// recurse to catch stacked prefixes like "帮我看看"
			return removeFiller(s)
		}
	}
	return s
}

// removeChineseParticles removes Chinese filler particles from within s.
func removeChineseParticles(s string) string {
	for _, p := range chineseFillerWords {
		s = strings.ReplaceAll(s, p, "")
	}
	return s
}

// removeEnglishFillers removes common English stop words from the token list.
func removeEnglishFillers(s string) string {
	tokens := strings.Fields(s)
	out := tokens[:0]
	for _, t := range tokens {
		lower := strings.ToLower(t)
		if !englishFillerWords[lower] {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

// containsChinese reports whether s contains any CJK character.
func containsChinese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			return true
		}
	}
	return false
}

// countDisplayUnits counts display units:
//   - 1 CJK character = 1 unit
//   - 1 English word (whitespace-delimited, including filenames/paths) = 1 unit
func countDisplayUnits(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	units := 0
	inEnglishWord := false

	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			if inEnglishWord {
				units++
				inEnglishWord = false
			}
			units++
		} else if unicode.IsSpace(r) {
			if inEnglishWord {
				units++
				inEnglishWord = false
			}
		} else {
			inEnglishWord = true
		}
	}
	if inEnglishWord {
		units++
	}
	return units
}

// runeKind classifies a rune for display-unit counting.
type runeKind int

const (
	runeKindCJK   runeKind = iota // CJK ideograph: 1 unit per character
	runeKindSpace                 // whitespace: word boundary
	runeKindOther                 // Latin/digit/symbol: part of an English word
)

func classifyRune(r rune) runeKind {
	if unicode.In(r, unicode.Han) {
		return runeKindCJK
	}
	if unicode.IsSpace(r) {
		return runeKindSpace
	}
	return runeKindOther
}

// truncateToUnits returns s truncated to at most maxUnits display units.
func truncateToUnits(s string, maxUnits int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	units := 0
	inWord := false
	i := 0

	for _, r := range s {
		runeLen := len(string(r))
		kind := classifyRune(r)

		// Close a pending English word on any non-other rune.
		if inWord && kind != runeKindOther {
			units++
			inWord = false
			if units >= maxUnits {
				return strings.TrimSpace(s[:i])
			}
		}

		switch kind {
		case runeKindCJK:
			units++
			if units >= maxUnits {
				return strings.TrimSpace(s[:i+runeLen])
			}
		case runeKindOther:
			inWord = true
			// runeKindSpace: no action beyond the word-close above.
		}
		i += runeLen
	}
	if inWord {
		units++
	}
	return strings.TrimSpace(s)
}

// isAllPronouns reports whether every whitespace-separated token in s is a Chinese pronoun.
func isAllPronouns(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if !chinesePronouns[t] {
			return false
		}
	}
	return true
}

// resolveDisplayName consolidates the auto-naming logic shared by
// completeStart and startPendingThread:
//  1. If the thread was manually renamed, preserve the existing name.
//  2. Otherwise extract a title from the user prompt.
//  3. Fall back to the default thread name.
func resolveDisplayName(ctx context.Context, store threadstore.Store, agentID, prompt, currentName string) string {
	name := strings.TrimSpace(currentName)
	if name == "" && store != nil {
		existing, err := store.GetByThreadID(ctx, agentID)
		if err == nil && existing.ManuallyRenamed {
			name = existing.Name
		}
	}
	if name == "" {
		if p := strings.TrimSpace(prompt); p != "" {
			name = ExtractTitle(p)
		}
	}
	if name == "" {
		name = defaultThreadName()
	}
	return name
}

// defaultThreadName returns the fallback name for a new thread.
func defaultThreadName() string {
	return "新对话"
}

// continuationName derives a continuation thread name from parentName:
//   - "Title" → "Title (续)"
//   - "Title (续)" → "Title (续 2)"
//   - "Title (续 N)" → "Title (续 N+1)"
var contRe = regexp.MustCompile(`^(.+) \(续(?: (\d+))?\)$`)

func continuationName(parentName string) string {
	if m := contRe.FindStringSubmatch(parentName); m != nil {
		base := m[1]
		if m[2] == "" {
			return fmt.Sprintf("%s (续 2)", base)
		}
		n := 0
		fmt.Sscanf(m[2], "%d", &n)
		return fmt.Sprintf("%s (续 %d)", base, n+1)
	}
	return fmt.Sprintf("%s (续)", parentName)
}
