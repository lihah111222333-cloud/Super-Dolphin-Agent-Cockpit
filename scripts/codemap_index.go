// scripts/codemap_index.go
// 自动扫描 docs/doc/codemap/*.md 和项目源码，生成 ai-index.json 索引。
// 用法: go run scripts/codemap_index.go [project-root]
// 接入 CI: make codemap-refresh

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Index struct {
	Version     string                `json:"version"`
	GeneratedAt string                `json:"generated_at"`
	Description string                `json:"description"`
	Codemaps    []Codemap             `json:"codemaps"`
	Files       map[string]*FileEntry `json:"files"`
}
type Codemap struct {
	ID         string    `json:"id"`
	File       string    `json:"file"`
	Title      string    `json:"title"`
	TotalLines int       `json:"total_lines"`
	Sections   []Section `json:"sections"`
}
type Section struct {
	Title     string `json:"title"`
	Level     int    `json:"level"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}
type FileEntry struct {
	Package     string `json:"package"`
	Description string `json:"description"`
	Refs        []Ref  `json:"refs"`
}
type Ref struct {
	CodemapID string `json:"codemap"`
	Section   string `json:"section"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Context   string `json:"context"`
}

type parsedMD struct {
	id, file, title string
	lines           []string
	sections        []Section
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	codemapDir := filepath.Join(root, "docs", "doc", "codemap")
	mds := loadCodemaps(codemapDir)
	filesIndex := buildFilesIndex(root, mds)

	idx := Index{
		Version:     "1.0",
		GeneratedAt: time.Now().Format("2006-01-02"),
		Description: "代码地图索引：源码文件→md段落行范围（自动生成 make codemap-refresh）",
		Files:       filesIndex,
	}
	for _, md := range mds {
		idx.Codemaps = append(idx.Codemaps, Codemap{
			ID: md.id, File: md.file, Title: md.title,
			TotalLines: len(md.lines), Sections: md.sections,
		})
	}

	outPath := filepath.Join(codemapDir, "ai-index.json")
	data, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}

	totalRefs, withRefs := 0, 0
	for _, f := range filesIndex {
		totalRefs += len(f.Refs)
		if len(f.Refs) > 0 {
			withRefs++
		}
	}
	fmt.Printf("ai-index.json: %d files, %d with refs, %d total refs, %d codemaps\n",
		len(filesIndex), withRefs, totalRefs, len(idx.Codemaps))
}

func loadCodemaps(codemapDir string) []parsedMD {
	mdFiles := scanMDFiles(codemapDir)
	sort.Strings(mdFiles)
	numRe := regexp.MustCompile(`^\d{2}-`)
	var mds []parsedMD
	for _, path := range mdFiles {
		base := filepath.Base(path)
		if !numRe.MatchString(base) {
			continue
		}
		lines := readLines(path)
		if len(lines) == 0 {
			continue
		}
		mds = append(mds, parsedMD{
			id: base[:2], file: base, title: extractTitle(lines),
			lines: lines, sections: parseSections(lines),
		})
	}
	return mds
}

func buildFilesIndex(root string, mds []parsedMD) map[string]*FileEntry {
	srcFiles := scanSourceFiles(root)
	sort.Strings(srcFiles)
	filesIndex := make(map[string]*FileEntry, len(srcFiles))
	for _, src := range srcFiles {
		entry := &FileEntry{Package: detectPackage(filepath.Join(root, src)), Refs: []Ref{}}
		base := filepath.Base(src)
		shortPath := filepath.Join(filepath.Base(filepath.Dir(src)), base)
		seen := map[string]bool{}
		var terms []string
		for _, term := range []string{src, shortPath, base} {
			if seen[term] || len(term) <= 3 {
				continue
			}
			seen[term] = true
			terms = append(terms, term)
		}
		for _, md := range mds {
			entry.Refs = append(entry.Refs, findRefs(md.id, md.lines, md.sections, terms, src)...)
		}
		if len(entry.Refs) > 0 {
			entry.Description = entry.Refs[0].Context
		}
		if entry.Description == "" {
			entry.Description = entry.Package
		}
		filesIndex[src] = entry
	}
	return filesIndex
}

func scanMDFiles(dir string) (r []string) {
	filepath.Walk(dir, func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() && strings.HasSuffix(p, ".md") {
			r = append(r, p)
		}
		return nil
	})
	return
}

func scanSourceFiles(root string) (r []string) {
	exts := map[string]bool{".go": true, ".js": true, ".ts": true, ".vue": true, ".sql": true}
	skip := map[string]bool{"node_modules": true, ".git": true, ".vite-cache": true, "dist": true,
		".build-cache": true, ".workspace": true, "testdata": true, "playwright-report": true, "test-results": true}
	for _, d := range []string{"cmd", "internal", "pkg", "sql", "migrations"} {
		filepath.Walk(filepath.Join(root, d), func(p string, i os.FileInfo, e error) error {
			if e != nil {
				return nil
			}
			if i.IsDir() && skip[i.Name()] {
				return filepath.SkipDir
			}
			if !i.IsDir() && exts[filepath.Ext(p)] && !strings.HasSuffix(p, "_test.go") {
				rel, _ := filepath.Rel(root, p)
				r = append(r, rel)
			}
			return nil
		})
	}
	return
}

func readLines(p string) []string {
	d, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(string(d), "\n")
}

func extractTitle(lines []string) string {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "# ") {
			return strings.TrimPrefix(strings.TrimSpace(l), "# ")
		}
	}
	return ""
}

var headingRe = regexp.MustCompile(`^(#{1,4})\s+(.+)`)

func parseSections(lines []string) []Section {
	type raw struct {
		title        string
		level, start int
	}
	var raws []raw
	for i, l := range lines {
		if m := headingRe.FindStringSubmatch(l); m != nil {
			raws = append(raws, raw{strings.TrimSpace(m[2]), len(m[1]), i + 1})
		}
	}
	type frame struct {
		level int
		title string
	}
	var stack []frame
	var out []Section
	for idx, r := range raws {
		for len(stack) > 0 && stack[len(stack)-1].level >= r.level {
			stack = stack[:len(stack)-1]
		}
		full := r.title
		if len(stack) > 0 && r.level > 2 {
			full = stack[len(stack)-1].title + " > " + r.title
		}
		end := len(lines)
		for j := idx + 1; j < len(raws); j++ {
			if raws[j].level <= r.level {
				end = raws[j].start - 1
				break
			}
		}
		out = append(out, Section{Title: full, Level: r.level, StartLine: r.start, EndLine: end})
		stack = append(stack, frame{r.level, full})
	}
	return out
}

func detectPackage(p string) string {
	switch filepath.Ext(p) {
	case ".js", ".ts", ".vue":
		return "frontend"
	case ".sql":
		return "sql"
	}
	d, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	for _, l := range strings.SplitN(string(d), "\n", 20) {
		if strings.HasPrefix(strings.TrimSpace(l), "package ") {
			return strings.Fields(strings.TrimSpace(l))[1]
		}
	}
	return ""
}

func findRefs(cmID string, lines []string, secs []Section, terms []string, fullPath string) (refs []Ref) {
	matched := map[string]bool{}
	for i, line := range lines {
		ln := i + 1
		hit := false
		for _, t := range terms {
			if strings.Contains(line, t) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		sec, s, e := blockRange(lines, ln, secs)
		key := fmt.Sprintf("%s:%d-%d", sec, s, e)
		if matched[key] {
			continue
		}
		matched[key] = true
		refs = append(refs, Ref{cmID, sec, s, e, extractCtx(line, fullPath)})
	}
	return
}

func blockRange(lines []string, ln int, secs []Section) (string, int, int) {
	sec := ""
	for _, s := range secs {
		if ln >= s.StartLine && ln <= s.EndLine {
			sec = s.Title
		}
	}
	idx := ln - 1
	if s, e, ok := tableRange(lines, idx); ok {
		return sec, s, e
	}
	if s, e, ok := codeBlockRange(lines, idx); ok {
		return sec, s, e
	}
	if s, e, ok := listItemRange(lines, idx); ok {
		return sec, s, e
	}
	s, e := paragraphRange(lines, idx)
	return sec, s, e
}

func tableRange(lines []string, idx int) (int, int, bool) {
	if idx < 0 || idx >= len(lines) {
		return 0, 0, false
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[idx]), "|") {
		return 0, 0, false
	}
	start, end := idx+1, idx+1
	for i := idx - 1; i >= 0; i-- {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			break
		}
		start = i + 1
	}
	for i := idx + 1; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			break
		}
		end = i + 1
	}
	return start, end, true
}

func codeBlockRange(lines []string, idx int) (int, int, bool) {
	inCode, start := false, 0
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "```") {
			continue
		}
		if !inCode {
			inCode, start = true, i
			continue
		}
		if start <= idx && idx <= i {
			return start + 1, i + 1, true
		}
		inCode = false
	}
	return 0, 0, false
}

func listItemRange(lines []string, idx int) (int, int, bool) {
	if idx < 0 || idx >= len(lines) {
		return 0, 0, false
	}
	line := strings.TrimSpace(lines[idx])
	if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
		return 0, 0, false
	}
	start, end := idx+1, idx+1
	for i := idx + 1; i < len(lines); i++ {
		if len(lines[i]) == 0 {
			break
		}
		if lines[i][0] != ' ' && lines[i][0] != '\t' {
			break
		}
		end = i + 1
	}
	return start, end, true
}

func paragraphRange(lines []string, idx int) (int, int) {
	start, end := idx+1, idx+1
	for i := idx - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		start = i + 1
	}
	for i := idx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		end = i + 1
	}
	return start, end
}

func extractCtx(line, fullPath string) string {
	s := strings.TrimLeft(strings.TrimSpace(line), "|- *#>")
	s = strings.TrimSpace(s)
	if strings.Contains(s, "|") {
		parts := strings.Split(s, "|")
		base := filepath.Base(fullPath)
		for i, p := range parts {
			if strings.Contains(strings.TrimSpace(p), base) && i+1 < len(parts) {
				s = strings.TrimSpace(parts[i+1])
				break
			}
		}
	}
	s = strings.ReplaceAll(s, "`"+fullPath+"`", "")
	s = strings.ReplaceAll(s, "`"+filepath.Base(fullPath)+"`", "")
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
