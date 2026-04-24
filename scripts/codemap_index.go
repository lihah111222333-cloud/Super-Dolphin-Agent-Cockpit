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

	"github.com/anthropic-ai/super-agent-v3/internal/devtools/codemapindex"
)

// ---------- output types ----------

type Index struct {
	Version      string                `json:"version"`
	GeneratedAt  string                `json:"generated_at"`
	Description  string                `json:"description"`
	SectionIndex []string              `json:"section_index"`
	Codemaps     []Codemap             `json:"codemaps"`
	Files        map[string]*FileEntry `json:"files"`
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
	Refs []Ref `json:"refs"`
}
type Ref struct {
	CodemapID string `json:"c"`
	SectionID int    `json:"s"`
	StartLine int    `json:"l"`
	EndLine   int    `json:"e"`
}

// ---------- internal types ----------

type parsedMD struct {
	id, file, title string
	lines           []string
	sections        []Section
}

type rawRef struct {
	codemapID string
	section   string
	startLine int
	endLine   int
}

const maxRefsPerFile = 20

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	codemapDir := filepath.Join(root, "docs", "doc", "codemap")
	mds := loadCodemaps(codemapDir)

	// Build raw refs (still using section title strings).
	rawFilesIndex := buildRawFilesIndex(root, mds)

	// Collect unique section titles and build index.
	secSet := map[string]int{}
	var secIndex []string
	getSID := func(title string) int {
		if id, ok := secSet[title]; ok {
			return id
		}
		id := len(secIndex)
		secIndex = append(secIndex, title)
		secSet[title] = id
		return id
	}

	// Convert rawRefs to compact Refs with section IDs.
	filesIndex := make(map[string]*FileEntry, len(rawFilesIndex))
	for src, raws := range rawFilesIndex {
		refs := make([]Ref, len(raws))
		for i, r := range raws {
			refs[i] = Ref{
				CodemapID: r.codemapID,
				SectionID: getSID(r.section),
				StartLine: r.startLine,
				EndLine:   r.endLine,
			}
		}
		filesIndex[src] = &FileEntry{Refs: refs}
	}

	idx := Index{
		Version:      "1.0",
		GeneratedAt:  time.Now().Format("2006-01-02"),
		Description:  "代码地图索引：源码文件→md段落行范围（自动生成 make codemap-refresh）",
		SectionIndex: secIndex,
		Files:        filesIndex,
	}
	readmeCodemaps := make([]codemapindex.ReadmeCodemap, 0, len(mds))
	for _, md := range mds {
		// Only emit level 1-2 sections in output.
		var outSecs []Section
		for _, s := range md.sections {
			if s.Level <= 2 {
				outSecs = append(outSecs, s)
			}
		}
		cm := Codemap{ID: md.id, File: md.file, Title: md.title, TotalLines: len(md.lines), Sections: outSecs}
		idx.Codemaps = append(idx.Codemaps, cm)
		readmeCodemaps = append(readmeCodemaps, codemapindex.ReadmeCodemap{ID: cm.ID, File: cm.File, Title: cm.Title})
	}

	outPath := filepath.Join(codemapDir, "ai-index.json")
	data, _ := json.Marshal(idx)
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	if err := codemapindex.SyncREADME(filepath.Join(codemapDir, "README.md"), readmeCodemaps, idx.GeneratedAt); err != nil {
		fmt.Fprintf(os.Stderr, "sync readme: %v\n", err)
		os.Exit(1)
	}

	totalRefs := 0
	for _, f := range filesIndex {
		totalRefs += len(f.Refs)
	}
	fmt.Printf("ai-index.json: %d files, %d total refs, %d sections, %d codemaps\n",
		len(filesIndex), totalRefs, len(secIndex), len(idx.Codemaps))
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

func buildRawFilesIndex(root string, mds []parsedMD) map[string][]rawRef {
	srcFiles := codemapindex.ScanSourceFiles(root)
	sort.Strings(srcFiles)
	filesIndex := make(map[string][]rawRef, len(srcFiles))
	for _, src := range srcFiles {
		base := filepath.Base(src)
		shortPath := filepath.Join(filepath.Base(filepath.Dir(src)), base)
		seen := map[string]bool{}
		var terms []string
		for _, term := range []string{src, shortPath} {
			if seen[term] || len(term) <= 3 {
				continue
			}
			seen[term] = true
			terms = append(terms, term)
		}
		var refs []rawRef
		for _, md := range mds {
			refs = append(refs, findRefs(md.id, md.lines, md.sections, terms)...)
		}
		if len(refs) > maxRefsPerFile {
			refs = refs[:maxRefsPerFile]
		}
		if len(refs) == 0 {
			continue
		}
		filesIndex[src] = refs
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

func findRefs(cmID string, lines []string, secs []Section, terms []string) (refs []rawRef) {
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
		refs = append(refs, rawRef{cmID, sec, s, e})
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
