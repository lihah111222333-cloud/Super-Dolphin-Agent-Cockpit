package skill

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	skillBlockHeaderV1 = regexp.MustCompile(`^\[skill:([a-z0-9][a-z0-9-]{0,63})::([a-z]+)@v(\d+)\]\s*$`)
	skillBlockFooterV1 = regexp.MustCompile(`^\[/skill:([a-z0-9][a-z0-9-]{0,63})(?:::([a-z]+)@v(\d+))?\]\s*$`)
)

const (
	legacySkillLookahead   = 8
	skillBlockVersionSuffix = "@v1"
	skillExpandBodyToolName = "skill_expand_body"
)

type SkillBlockFormat int
const (
	SkillBlockFormatNone SkillBlockFormat = iota
	SkillBlockFormatLegacy
	SkillBlockFormatV1
)
type SkillBlockHeader struct {
	Format  SkillBlockFormat
	Name    string
	Mode    string
	Version int
}
func ParseSkillBlockHeader(line string) SkillBlockHeader {
	trimmed := strings.TrimSpace(line)
	if m := skillBlockHeaderV1.FindStringSubmatch(trimmed); m != nil {
		return SkillBlockHeader{
			Format:  SkillBlockFormatV1,
			Name:    m[1],
			Mode:    m[2],
			Version: parseVersion(m[3]),
		}
	}
	if strings.HasPrefix(trimmed, "[skill:") && strings.Contains(trimmed, "]") {
		return SkillBlockHeader{Format: SkillBlockFormatLegacy}
	}
	return SkillBlockHeader{Format: SkillBlockFormatNone}
}
func ParseSkillBlockFooter(line string) (SkillBlockHeader, bool) {
	m := skillBlockFooterV1.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return SkillBlockHeader{}, false
	}
	return SkillBlockHeader{
		Format:  SkillBlockFormatV1,
		Name:    m[1],
		Mode:    m[2],
		Version: parseVersion(m[3]),
	}, true
}
func TrimInjectedSkillBlocks(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	trimmed := false
	for i := 0; i < len(lines); i++ {
		header := ParseSkillBlockHeader(lines[i])
		switch header.Format {
		case SkillBlockFormatV1:
			if footer := findMatchingSkillBlockFooter(lines, i+1, header); footer >= 0 {
				trimmed = true
				i = footer
				continue
			}
		case SkillBlockFormatLegacy:
			if looksLikeLegacyInjectedBlock(lines, i) {
				return strings.TrimRight(strings.Join(kept, "\n"), "\n")
			}
		}
		kept = append(kept, lines[i])
	}
	if !trimmed {
		return text
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

func RenderSkillBlock(name, body, summary, mode string) (string, bool) {
	name, err := validateSkillName(name)
	if err != nil {
		return "", false
	}
	switch effectiveRenderMode(mode) {
	case "full":
		body = strings.TrimSpace(body)
		if body == "" || containsSkillBlockMarker(body) {
			return "", false
		}
		return wrapSkillBlock(name, "full", body), true
	case "summary":
		summary = strings.TrimSpace(summary)
		if summary == "" || containsSkillBlockMarker(summary) {
			return "", false
		}
		inner := summary + "\n→ Call " + skillExpandBodyToolName + "(\"" + name + "\") for full body"
		return wrapSkillBlock(name, "summary", inner), true
	default:
		return "", false
	}
}

func containsSkillBlockMarker(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[skill:") || strings.HasPrefix(line, "[/skill:") {
			return true
		}
	}
	return false
}

func effectiveRenderMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "":
		return "full"
	case "full", "summary", "none":
		return strings.TrimSpace(strings.ToLower(mode))
	default:
		return "none"
	}
}

func wrapSkillBlock(name, mode, inner string) string {
	header := "[skill:" + name + "::" + mode + skillBlockVersionSuffix + "]"
	footer := "[/skill:" + name + "::" + mode + skillBlockVersionSuffix + "]"
	return header + "\n" + inner + "\n" + footer
}

func findMatchingSkillBlockFooter(lines []string, start int, header SkillBlockHeader) int {
	for i := start; i < len(lines); i++ {
		footer, ok := ParseSkillBlockFooter(lines[i])
		if ok && footerMatches(header, footer) {
			return i
		}
	}
	return -1
}

func footerMatches(header, footer SkillBlockHeader) bool {
	if header.Name != footer.Name {
		return false
	}
	return footer.Mode == "" || (header.Mode == footer.Mode && header.Version == footer.Version)
}

func looksLikeLegacyInjectedBlock(lines []string, start int) bool {
	if !isValidLegacyStart(lines, start) {
		return false
	}
	hasSummary, hasUsage := markLegacySkillMarkers(lines[start])
	for _, line := range legacyLookaheadWindow(lines, start) {
		stop, summary, usage := scanLegacyLookaheadLine(line)
		if stop {
			break
		}
		hasSummary, hasUsage = mergeLegacyMarkerState(hasSummary, hasUsage, summary, usage)
		if legacyMarkersComplete(hasSummary, hasUsage) {
			return true
		}
	}
	return legacyMarkersComplete(hasSummary, hasUsage)
}

func isValidLegacyStart(lines []string, start int) bool {
	return start >= 0 && start < len(lines)
}

func legacyLookaheadWindow(lines []string, start int) []string {
	end := start + legacySkillLookahead + 1
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start+1 : end]
}

func scanLegacyLookaheadLine(line string) (bool, bool, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false, false, false
	}
	if strings.HasPrefix(trimmed, "[skill:") {
		return true, false, false
	}
	summary, usage := markLegacySkillMarkers(trimmed)
	return false, summary, usage
}

func mergeLegacyMarkerState(summary, usage, nextSummary, nextUsage bool) (bool, bool) {
	return summary || nextSummary, usage || nextUsage
}

func legacyMarkersComplete(summary, usage bool) bool {
	return summary && usage
}

func markLegacySkillMarkers(line string) (bool, bool) {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "摘要:"), strings.HasPrefix(trimmed, "使用方式: ")
}

func parseVersion(raw string) int {
	if raw == "" {
		return 0
	}
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		return 0
	}
	return version
}
