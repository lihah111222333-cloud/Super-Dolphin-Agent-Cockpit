// scripts/codemap_index.go
// 自动扫描 docs/doc/codemap/*.md 和项目源码，生成 ai-index.json 索引。
// 用法: go run scripts/codemap_index.go [project-root]
// 接入 CI: make codemap-refresh

package main

import (
	"bytes"
	"encoding/json"
	"flag"
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
	Generator    string                `json:"generator"`
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
	check := flag.Bool("check", false, "verify docs/doc/codemap generated files without modifying the worktree")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	codemapDir := filepath.Join(root, "docs", "doc", "codemap")
	outPath := filepath.Join(codemapDir, "ai-index.json")
	readmePath := filepath.Join(codemapDir, "README.md")

	generatedAt := time.Now().Format("2006-01-02")
	if *check {
		if existing, ok := existingGeneratedAt(outPath); ok {
			generatedAt = existing
		}
	}

	idx, readmeCodemaps, err := buildIndex(root, codemapDir, generatedAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build index: %v\n", err)
		os.Exit(1)
	}
	data, err := json.Marshal(idx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}

	if *check {
		checkGeneratedFiles(outPath, data, readmePath, readmeCodemaps, idx.GeneratedAt)
		fmt.Printf("ai-index.json: %d files, %d total refs, %d sections, %d codemaps (up to date)\n",
			len(idx.Files), countRefs(idx.Files), len(idx.SectionIndex), len(idx.Codemaps))
		return
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	if err := codemapindex.SyncREADME(readmePath, readmeCodemaps, idx.GeneratedAt); err != nil {
		fmt.Fprintf(os.Stderr, "sync readme: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ai-index.json: %d files, %d total refs, %d sections, %d codemaps\n",
		len(idx.Files), countRefs(idx.Files), len(idx.SectionIndex), len(idx.Codemaps))
}

func buildIndex(root, codemapDir, generatedAt string) (Index, []codemapindex.ReadmeCodemap, error) {
	mds, err := loadCodemaps(codemapDir)
	if err != nil {
		return Index{}, nil, err
	}

	// Build raw refs (still using section title strings).
	rawFilesIndex, err := buildRawFilesIndex(root, mds)
	if err != nil {
		return Index{}, nil, err
	}

	// Convert rawRefs to compact Refs with section IDs.
	filesIndex, secIndex := buildCompactFilesIndex(rawFilesIndex)

	codemaps, readmeCodemaps := buildOutputCodemaps(mds)

	idx := Index{
		Version:      "1.0",
		Generator:    codemapindex.GeneratorAnchor,
		GeneratedAt:  generatedAt,
		Description:  "代码地图索引：源码文件→md段落行范围（自动生成 make codemap-refresh）",
		SectionIndex: secIndex,
		Codemaps:     codemaps,
		Files:        filesIndex,
	}
	return idx, readmeCodemaps, nil
}

func checkGeneratedFiles(indexPath string, indexData []byte, readmePath string, readmeCodemaps []codemapindex.ReadmeCodemap, generatedAt string) {
	stale := !sameFileContent(indexPath, indexData)
	expectedREADME, err := renderSyncedREADME(readmePath, readmeCodemaps, generatedAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codemap-check: render README: %v\n", err)
		os.Exit(1)
	}
	if !sameFileContent(readmePath, expectedREADME) {
		stale = true
	}
	if stale {
		fmt.Fprintln(os.Stderr, "codemap-check: generated files are stale; run `make codemap-refresh`")
		os.Exit(1)
	}
}

func sameFileContent(path string, want []byte) bool {
	got, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codemap-check: read %s: %v\n", path, err)
		return false
	}
	if bytes.Equal(got, want) {
		return true
	}
	fmt.Fprintf(os.Stderr, "codemap-check: %s differs from generated output\n", path)
	return false
}

func renderSyncedREADME(readmePath string, codemaps []codemapindex.ReadmeCodemap, generatedAt string) ([]byte, error) {
	current, err := os.ReadFile(readmePath)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "codemap-readme-check-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(tmpPath, current, 0644); err != nil {
		return nil, err
	}
	if err := codemapindex.SyncREADME(tmpPath, codemaps, generatedAt); err != nil {
		return nil, err
	}
	return os.ReadFile(tmpPath)
}

func existingGeneratedAt(indexPath string) (string, bool) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return "", false
	}
	var idx struct {
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(data, &idx); err != nil || strings.TrimSpace(idx.GeneratedAt) == "" {
		return "", false
	}
	return strings.TrimSpace(idx.GeneratedAt), true
}

func countRefs(files map[string]*FileEntry) int {
	totalRefs := 0
	for _, f := range files {
		totalRefs += len(f.Refs)
	}
	return totalRefs
}

func loadCodemaps(codemapDir string) ([]parsedMD, error) {
	mdFiles, err := scanMDFiles(codemapDir)
	if err != nil {
		return nil, fmt.Errorf("scan codemap markdown: %w", err)
	}
	sort.Strings(mdFiles)
	numRe := regexp.MustCompile(`^\d{2}-`)
	var mds []parsedMD
	for _, path := range mdFiles {
		base := filepath.Base(path)
		if !numRe.MatchString(base) {
			continue
		}
		lines, err := readLines(path)
		if err != nil {
			return nil, fmt.Errorf("read codemap markdown %s: %w", path, err)
		}
		if len(lines) == 0 {
			continue
		}
		mds = append(mds, parsedMD{
			id: base[:2], file: base, title: extractTitle(lines),
			lines: lines, sections: parseSections(lines),
		})
	}
	return mds, nil
}

func buildRawFilesIndex(root string, mds []parsedMD) (map[string][]rawRef, error) {
	srcFiles, err := codemapindex.ScanSourceFiles(root)
	if err != nil {
		return nil, fmt.Errorf("scan source files: %w", err)
	}
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
	return filesIndex, nil
}

func buildCompactFilesIndex(rawFilesIndex map[string][]rawRef) (map[string]*FileEntry, []string) {
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

	filesIndex := make(map[string]*FileEntry, len(rawFilesIndex))
	sources := make([]string, 0, len(rawFilesIndex))
	for src := range rawFilesIndex {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	for _, src := range sources {
		raws := rawFilesIndex[src]
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
	return filesIndex, secIndex
}

func buildOutputCodemaps(mds []parsedMD) ([]Codemap, []codemapindex.ReadmeCodemap) {
	var codemaps []Codemap
	readmeCodemaps := make([]codemapindex.ReadmeCodemap, 0, len(mds))
	for _, md := range mds {
		var outSecs []Section
		for _, s := range md.sections {
			if s.Level <= 2 {
				outSecs = append(outSecs, s)
			}
		}
		cm := Codemap{ID: md.id, File: md.file, Title: md.title, TotalLines: len(md.lines), Sections: outSecs}
		codemaps = append(codemaps, cm)
		readmeCodemaps = append(readmeCodemaps, codemapindex.ReadmeCodemap{ID: cm.ID, File: cm.File, Title: cm.Title})
	}
	return codemaps, readmeCodemaps
}

func scanMDFiles(dir string) ([]string, error) {
	var r []string
	if err := filepath.Walk(dir, func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if !i.IsDir() && strings.HasSuffix(p, ".md") {
			r = append(r, p)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return r, nil
}

func readLines(p string) ([]string, error) {
	d, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(d), "\n"), nil
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
	return refs
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
