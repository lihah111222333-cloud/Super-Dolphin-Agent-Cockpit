package skill

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type skillRecord struct {
	info SkillInfo
	path string
	rel  string
}

func (s *service) scanSkills() ([]skillRecord, error) {
	roots := s.skillRoots()
	if len(roots) == 0 {
		return nil, nil
	}
	records := make([]skillRecord, 0, 16)
	for _, root := range roots {
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			return s.visitSkillEntry(root, path, entry, walkErr, &records)
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].info.Name) < strings.ToLower(records[j].info.Name)
	})
	return records, nil
}

func (s *service) visitSkillEntry(root, path string, entry os.DirEntry, walkErr error, records *[]skillRecord) error {
	if walkErr != nil || entry == nil {
		return walkErr
	}
	if entry.IsDir() {
		name := entry.Name()
		if path != root && strings.HasPrefix(name, ".") && name != ".system" {
			return filepath.SkipDir
		}
		if name == ".git" {
			return filepath.SkipDir
		}
		return nil
	}
	if !strings.EqualFold(entry.Name(), skillMainFile) {
		return nil
	}
	record, err := parseSkillRecord(root, path)
	if err != nil {
		return nil
	}
	*records = append(*records, record)
	return nil
}

func parseSkillRecord(root, path string) (skillRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skillRecord{}, err
	}
	dir := filepath.Dir(path)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return skillRecord{}, err
	}
	info := parseSkillInfo(rel, dir, string(data))
	return skillRecord{info: info, path: path, rel: filepath.ToSlash(rel)}, nil
}

func parseSkillInfo(rel, dir, content string) SkillInfo {
	info := SkillInfo{Name: fallbackSkillName(rel), Dir: dir}
	frontmatter, body, ok := splitFrontmatter(content)
	if ok {
		lines := strings.Split(frontmatter, "\n")
		for i := 0; i < len(lines); i++ {
			key, value, ok := parseMetaLine(lines[i])
			if !ok {
				continue
			}
			i += applyMetaLine(&info, key, value, lines[i+1:])
		}
	} else {
		body = content
	}
	if info.Name == "" {
		info.Name = fallbackSkillName(rel)
	}
	if info.Summary == "" {
		info.Summary = summarizeSkillBody(body, info.Description)
	}
	info.Description = truncateRunes(info.Description, 120)
	info.Summary = truncateRunes(info.Summary, 220)
	info.TriggerWords = uniqStrings(append(info.TriggerWords, "@"+info.Name, "[skill:"+info.Name+"]"))
	info.ForceWords = uniqStrings(info.ForceWords)
	return info
}

func fallbackSkillName(rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return "skill"
	}
	parts := strings.Split(rel, "/")
	return strings.TrimSpace(parts[len(parts)-1])
}

func splitFrontmatter(content string) (string, string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	frontmatter, tail, ok := strings.Cut(content[4:], "\n---")
	if !ok {
		return "", content, false
	}
	return frontmatter, strings.TrimPrefix(tail, "\n"), true
}

func parseMetaLine(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

func applyMetaLine(info *SkillInfo, key, value string, tail []string) int {
	switch key {
	case "name":
		info.Name = parseScalar(value)
	case "description":
		info.Description = parseScalar(value)
	case "summary", "digest":
		info.Summary = parseScalar(value)
	case "trigger_words", "triggerwords", "triggers", "aliases", "tags", "keywords":
		words, used := parseWordList(value, tail)
		info.TriggerWords = append(info.TriggerWords, words...)
		return used
	case "force_words", "forcewords", "mandatory_words", "must_words":
		words, used := parseWordList(value, tail)
		info.ForceWords = append(info.ForceWords, words...)
		return used
	}
	return 0
}

func parseWordList(value string, tail []string) ([]string, int) {
	if value = strings.TrimSpace(value); value != "" {
		return splitWords(value), 0
	}
	words := make([]string, 0, 4)
	used := 0
	for _, line := range tail {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		words = append(words, parseScalar(strings.TrimPrefix(trimmed, "- ")))
		used++
	}
	return uniqStrings(words), used
}

func splitWords(value string) []string {
	value = strings.Trim(value, "[]")
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if word := parseScalar(part); word != "" {
			words = append(words, word)
		}
	}
	return uniqStrings(words)
}

func parseScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

func summarizeSkillBody(body, description string) string {
	if description = strings.TrimSpace(description); description != "" {
		return description
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, "```"):
			continue
		default:
			return line
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func uniqStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (s *service) resolveSkill(name string) (skillRecord, error) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return skillRecord{}, errors.New("skill name is required")
	}
	records, err := s.scanSkills()
	if err != nil {
		return skillRecord{}, err
	}
	for _, record := range records {
		for _, candidate := range []string{record.info.Name, filepath.Base(record.info.Dir), record.rel} {
			if strings.EqualFold(strings.TrimSpace(candidate), needle) {
				return record, nil
			}
		}
	}
	return skillRecord{}, os.ErrNotExist
}

func (s *service) writeSkill(name, content string) (string, error) {
	root := strings.TrimSpace(s.root)
	if root == "" {
		return "", errors.New("skills root is not configured")
	}
	dir := filepath.Join(root, skillSlug(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, skillMainFile)
	return path, os.WriteFile(path, []byte(content), 0o644)
}

func (s *service) updateSkillSummary(name, summary string) (string, string, error) {
	record, err := s.resolveSkill(name)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(record.path)
	if err != nil {
		return "", "", err
	}
	updated := upsertSkillSummary(string(data), summary)
	return record.path, record.info.Name, os.WriteFile(record.path, []byte(updated), 0o644)
}

func upsertSkillSummary(content, summary string) string {
	summary = strings.TrimSpace(summary)
	frontmatter, body, ok := splitFrontmatter(content)
	if !ok {
		if summary == "" {
			return content
		}
		return strings.Join([]string{"---", `summary: "` + strings.ReplaceAll(summary, `"`, `\"`) + `"`, "---", "", strings.TrimSpace(content)}, "\n")
	}
	lines := strings.Split(frontmatter, "\n")
	next := make([]string, 0, len(lines)+1)
	wrote := false
	for _, line := range lines {
		key, _, ok := parseMetaLine(line)
		if ok && (key == "summary" || key == "digest") {
			if summary != "" {
				next = append(next, `summary: "`+strings.ReplaceAll(summary, `"`, `\"`)+`"`)
			}
			wrote = true
			continue
		}
		next = append(next, line)
	}
	if !wrote && summary != "" {
		next = append(next, `summary: "`+strings.ReplaceAll(summary, `"`, `\"`)+`"`)
	}
	return strings.Join([]string{"---", strings.Join(next, "\n"), "---", body}, "\n")
}

func skillSlug(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "skill"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case lastDash:
		default:
			b.WriteRune('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "skill"
	}
	return slug
}
