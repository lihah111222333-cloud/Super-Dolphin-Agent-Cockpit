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
func splitH2(body string) []Section {
	lines := strings.Split(body, "\n")
	var sections []Section
	var cur *Section
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			if cur != nil {
				cur.Body = strings.TrimSpace(cur.Body)
				sections = append(sections, *cur)
			}
			cur = &Section{Title: strings.TrimSpace(strings.TrimPrefix(ln, "## "))}
			continue
		}
		if cur != nil {
			cur.Body += ln + "\n"
		}
	}
	if cur != nil {
		cur.Body = strings.TrimSpace(cur.Body)
		sections = append(sections, *cur)
	}
	return sections
}
