// Package skillforge parses SKILL.md files and renders them into
// a slim entrypoint plus per-section reference files for cache
// projection. It is the pure transformation layer of the skill
// subsystem; library management lives in skilllibrary.
package skillforge

import (
	"errors"
	"strings"
)

// ParsedSkill is the intermediate representation of a parsed SKILL.md.
// Sections are split strictly on H2 headings; H3 headings stay inside their H2 Body.
type ParsedSkill struct {
	Name        string
	Description string
	Frontmatter map[string]string // raw frontmatter fields including name/description
	Sections    []Section
}

// Section represents one H2-delimited section.
type Section struct {
	Title string // H2 heading text (without the "## " prefix)
	Body  string // all content between this H2 and the next H2 (trimmed)
}

// ErrMissingFrontmatter is returned when the source does not begin with --- frontmatter ---.
var ErrMissingFrontmatter = errors.New("skillforge: SKILL.md must start with --- frontmatter ---")

// bom is the UTF-8 byte order mark that some editors prepend.
const bom = "\xef\xbb\xbf"

// Parse parses the full content of a SKILL.md file into a ParsedSkill.
// The frontmatter must start with "---\n" and end with "\n---\n" or "---\n" at the
// very beginning of rest (zero-length frontmatter).
// H2 sections are identified by lines beginning with "## ".
func Parse(src string) (*ParsedSkill, error) {
	src = strings.TrimPrefix(src, bom)
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return nil, ErrMissingFrontmatter
	}
	rest := strings.TrimPrefix(src, "---\n")

	var fmText, body string
	if strings.HasPrefix(rest, "---\n") {
		// zero-length frontmatter: opening --- immediately followed by closing ---
		fmText = ""
		body = strings.TrimPrefix(rest, "---\n")
	} else {
		end := strings.Index(rest, "\n---\n")
		if end < 0 {
			return nil, ErrMissingFrontmatter
		}
		fmText = rest[:end]
		body = rest[end+len("\n---\n"):]
	}

	fm := parseFrontmatter(fmText)
	ps := &ParsedSkill{
		Name:        fm["name"],
		Description: fm["description"],
		Frontmatter: fm,
	}
	ps.Sections = splitH2(body)
	return ps, nil
}

// parseFrontmatter parses minimal YAML (single-line key: value pairs only, no nesting).
// Design decision: no external yaml library; multi-line values are out of scope for Phase 1.
func parseFrontmatter(text string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if len(v) >= 2 {
			first, last := v[0], v[len(v)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		out[k] = v
	}
	return out
}

// splitH2 splits the body text into sections on H2 headings ("## ").
// Lines inside fenced code blocks (``` or ~~~) are never treated as headings,
// so example markdown inside SKILL.md does not split the section unexpectedly.
// Closing-suffix form "## Title ##" is normalized to just "Title".
func splitH2(body string) []Section {
	lines := strings.Split(body, "\n")
	var sections []Section
	var cur *Section
	fence := &fenceTracker{}
	for _, ln := range lines {
		cur = applyH2Line(ln, fence, cur, &sections)
	}
	if cur != nil {
		cur.Body = strings.TrimSpace(cur.Body)
		sections = append(sections, *cur)
	}
	return sections
}

// applyH2Line dispatches one input line through the fence/heading state machine
// and returns the (possibly new) current Section pointer. Pulled out of
// splitH2 to keep that function under the repository CC=10 limit.
func applyH2Line(ln string, fence *fenceTracker, cur *Section, sections *[]Section) *Section {
	if fence.update(ln) {
		if cur != nil {
			cur.Body += ln + "\n"
		}
		return cur
	}
	if !fence.inside() {
		if title, ok := parseH2Heading(ln); ok {
			if cur != nil {
				cur.Body = strings.TrimSpace(cur.Body)
				*sections = append(*sections, *cur)
			}
			return &Section{Title: title}
		}
	}
	if cur != nil {
		cur.Body += ln + "\n"
	}
	return cur
}

// fenceTracker tracks whether the parser is currently inside a fenced code
// block and which marker (``` or ~~~) opened it, so a tilde fence inside a
// backtick fence does not accidentally close the outer fence.
type fenceTracker struct {
	in     bool
	marker string
}

// update consumes one line. Returns true if the line was a fence open/close,
// false otherwise.
func (f *fenceTracker) update(line string) bool {
	marker, ok := fenceLine(line)
	if !ok {
		return false
	}
	if !f.in {
		f.in = true
		f.marker = marker
	} else if marker == f.marker {
		f.in = false
		f.marker = ""
	}
	return true
}

func (f *fenceTracker) inside() bool { return f.in }

// fenceLine reports whether ln opens or closes a fenced code block. Markers
// (``` and ~~~) are tracked separately so a tilde fence inside a backtick
// fence does not accidentally close the outer fence.
func fenceLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```", true
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~", true
	default:
		return "", false
	}
}

// parseH2Heading recognizes lines like "## Title" and "## Title ##" while
// rejecting H3+ lines such as "### Foo". Trailing closing "##" is stripped.
func parseH2Heading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "###") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
	title = strings.TrimSpace(strings.TrimSuffix(title, "##"))
	return title, title != ""
}
