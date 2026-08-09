// 自动扫描 docs/doc/codemap/*.md 和项目源码，生成 ai-index.json 索引。

package codemapindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ----- 输出结构 -----

// Index 是 ai-index.json 的顶层 wire 结构，字段名受生成文件消费者约束。
type Index struct {
	Version      string                `json:"version"`       // 索引格式版本。
	Generator    string                `json:"generator"`     // 生成器锚点，用于 README 同步识别。
	GeneratedAt  string                `json:"generated_at"`  // 生成日期；check 模式会复用旧值避免漂移。
	Description  string                `json:"description"`   // 给 AI/人类读取的索引说明。
	Counts       []CodemapCount        `json:"counts"`        // 编号卷声明的实时源码计数，禁止在 Markdown 中手写快照。
	SectionIndex []string              `json:"section_index"` // section ID 到标题的全局映射表。
	Codemaps     []Codemap             `json:"codemaps"`      // 每份 codemap 的章节摘要。
	Files        map[string]*FileEntry `json:"files"`         // 源码文件到 codemap 引用的索引。
}

// CodemapCount 是 codemap-count 声明对应的自动生成计数。
type CodemapCount struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Value int    `json:"value"`
}

// Codemap 描述单个 codemap markdown 文件及其可引用章节。
type Codemap struct {
	ID         string    `json:"id"`          // 两位文件编号。
	File       string    `json:"file"`        // codemap markdown 文件名。
	Title      string    `json:"title"`       // 文档一级标题。
	TotalLines int       `json:"total_lines"` // markdown 总行数。
	Sections   []Section `json:"sections"`    // 暴露到 README/索引的章节。
}

// Section 描述 markdown 章节在原文件中的行范围。
type Section struct {
	Title     string `json:"title"`
	Level     int    `json:"level"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// FileEntry 保存源码文件命中的 codemap 引用列表。
type FileEntry struct {
	Refs []Ref `json:"refs"`
}

// Ref 是文件到 codemap section 的紧凑引用，字段名为压缩 JSON 体积而保持短名。
type Ref struct {
	CodemapID string `json:"c"`
	SectionID int    `json:"s"`
	StartLine int    `json:"l"`
	EndLine   int    `json:"e"`
}

// ----- 内部结构 -----

// parsedMD 是解析后的 codemap markdown，保留原始行以便后续定位引用范围。
type parsedMD struct {
	id, file, title string
	lines           []string
	sections        []Section
}

// rawRef 在 section 标题压缩成 ID 前保存源码文件的原始引用。
type rawRef struct {
	codemapID string
	section   string
	startLine int
	endLine   int
}

type generatedCodemapArtifacts struct {
	index          Index
	indexData      []byte
	readmeCodemaps []ReadmeCodemap
	anchorManifest AnchorManifest
	anchorData     []byte
}

// maxRefsPerFile 限制单个源码文件的 codemap 引用数量，避免索引体积失控。
const maxRefsPerFile = 20

// Generate 构建或检查指定仓库根目录下的代码地图生成物。
func Generate(root string, check bool) error {
	codemapDir := filepath.Join(root, "docs", "doc", "codemap")
	outPath := filepath.Join(codemapDir, "ai-index.json")
	readmePath := filepath.Join(codemapDir, "README.md")
	anchorManifestPath := filepath.Join(codemapDir, "anchor-identities.json")

	artifacts, err := buildGeneratedCodemapArtifacts(root, codemapDir, generatedAtForMode(check, outPath, time.Now))
	if err != nil {
		return err
	}

	if check {
		if err := checkGeneratedFiles(
			outPath,
			artifacts.indexData,
			readmePath,
			artifacts.readmeCodemaps,
			artifacts.index.GeneratedAt,
			anchorManifestPath,
			artifacts.anchorData,
			artifacts.anchorManifest,
		); err != nil {
			return err
		}
		fmt.Printf("ai-index.json: %d files, %d total refs, %d counts, %d sections, %d codemaps (up to date)\n",
			len(artifacts.index.Files), countRefs(artifacts.index.Files), len(artifacts.index.Counts), len(artifacts.index.SectionIndex), len(artifacts.index.Codemaps))
		return nil
	}

	if err := refreshGeneratedCodemapFiles(outPath, readmePath, anchorManifestPath, artifacts); err != nil {
		return err
	}
	fmt.Printf("ai-index.json: %d files, %d total refs, %d counts, %d sections, %d codemaps\n",
		len(artifacts.index.Files), countRefs(artifacts.index.Files), len(artifacts.index.Counts), len(artifacts.index.SectionIndex), len(artifacts.index.Codemaps))
	return nil
}

// generatedAtForMode 在检查模式复用既有日期，刷新模式使用 UTC 当天日期。
func generatedAtForMode(check bool, outPath string, nowFunc func() time.Time) string {
	if check {
		if existing, ok := existingGeneratedAt(outPath); ok {
			return existing
		}
	}
	return nowFunc().UTC().Format("2006-01-02")
}

// buildGeneratedCodemapArtifacts 一次性构建索引、README 输入和锚点身份生成物。
func buildGeneratedCodemapArtifacts(root, codemapDir, generatedAt string) (generatedCodemapArtifacts, error) {
	idx, readmeCodemaps, err := buildIndex(root, codemapDir, generatedAt)
	if err != nil {
		return generatedCodemapArtifacts{}, fmt.Errorf("build index: %w", err)
	}
	indexData, err := json.Marshal(idx)
	if err != nil {
		return generatedCodemapArtifacts{}, fmt.Errorf("marshal index: %w", err)
	}
	anchorManifest, err := buildAnchorManifest(root, codemapDir)
	if err != nil {
		return generatedCodemapArtifacts{}, fmt.Errorf("build anchor manifest: %w", err)
	}
	anchorData, err := MarshalAnchorManifest(anchorManifest)
	if err != nil {
		return generatedCodemapArtifacts{}, fmt.Errorf("marshal anchor manifest: %w", err)
	}
	return generatedCodemapArtifacts{
		index:          idx,
		indexData:      indexData,
		readmeCodemaps: readmeCodemaps,
		anchorManifest: anchorManifest,
		anchorData:     anchorData,
	}, nil
}

// refreshGeneratedCodemapFiles 写入索引、README 同步区和锚点身份清单。
func refreshGeneratedCodemapFiles(
	outPath, readmePath, anchorManifestPath string,
	artifacts generatedCodemapArtifacts,
) error {
	if err := os.WriteFile(outPath, artifacts.indexData, 0o644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	if err := SyncREADME(readmePath, artifacts.readmeCodemaps, artifacts.index.GeneratedAt); err != nil {
		return fmt.Errorf("sync README: %w", err)
	}
	if err := WriteAnchorManifest(anchorManifestPath, artifacts.anchorManifest); err != nil {
		return fmt.Errorf("write anchor manifest: %w", err)
	}
	return nil
}

// buildIndex 扫描 codemap 文档和源码文件，构建索引及 README 同步输入。
func buildIndex(root, codemapDir, generatedAt string) (Index, []ReadmeCodemap, error) {
	mds, err := loadCodemaps(codemapDir)
	if err != nil {
		return Index{}, nil, err
	}
	if err := validateCodemapSemantics(root, mds); err != nil {
		return Index{}, nil, err
	}
	counts, err := collectCodemapCounts(root, mds)
	if err != nil {
		return Index{}, nil, err
	}

	// raw refs 先保留 section 标题，后续统一压缩成 section ID。
	rawFilesIndex, err := buildRawFilesIndex(root, mds)
	if err != nil {
		return Index{}, nil, err
	}

	// compact refs 复用全局 section table，降低 ai-index.json 体积。
	filesIndex, secIndex := buildCompactFilesIndex(rawFilesIndex)

	codemaps, readmeCodemaps := buildOutputCodemaps(mds)

	idx := Index{
		Version:      "1.1",
		Generator:    GeneratorAnchor,
		GeneratedAt:  generatedAt,
		Description:  "代码地图索引：源码实时计数与文件→md段落行范围（自动生成 make codemap-refresh）",
		Counts:       counts,
		SectionIndex: secIndex,
		Codemaps:     codemaps,
		Files:        filesIndex,
	}
	return idx, readmeCodemaps, nil
}

// checkGeneratedFiles 对比索引和 README 的生成内容，并返回稳定的漂移错误。
func checkGeneratedFiles(
	indexPath string,
	indexData []byte,
	readmePath string,
	readmeCodemaps []ReadmeCodemap,
	generatedAt string,
	anchorManifestPath string,
	anchorData []byte,
	expectedAnchorManifest AnchorManifest,
) error {
	stalePaths := make([]string, 0, 3)
	if !sameFileContent(indexPath, indexData) {
		stalePaths = append(stalePaths, indexPath)
	}
	expectedREADME, err := renderSyncedREADME(readmePath, readmeCodemaps, generatedAt)
	if err != nil {
		return fmt.Errorf("codemap-check: render README: %w", err)
	}
	if !sameFileContent(readmePath, expectedREADME) {
		stalePaths = append(stalePaths, readmePath)
	}
	currentAnchorData, err := os.ReadFile(anchorManifestPath)
	if err != nil {
		stalePaths = append(stalePaths, anchorManifestPath)
	} else {
		if validateErr := ValidateAnchorManifest(currentAnchorData, expectedAnchorManifest); validateErr != nil {
			stalePaths = append(stalePaths, anchorManifestPath)
		} else if !bytes.Equal(currentAnchorData, anchorData) {
			stalePaths = append(stalePaths, anchorManifestPath)
		}
	}
	if len(stalePaths) > 0 {
		return fmt.Errorf(
			"codemap-check: generated files are stale (%s); run `make codemap-refresh`",
			strings.Join(stalePaths, ", "),
		)
	}
	return nil
}

// buildAnchorManifest 复用编号卷输入生成 path:line 内容身份清单。
func buildAnchorManifest(root, codemapDir string) (AnchorManifest, error) {
	mds, err := loadCodemaps(codemapDir)
	if err != nil {
		return AnchorManifest{}, err
	}
	return BuildAnchorManifest(root, semanticMarkdownDocs(mds))
}

// sameFileContent 判断磁盘文件是否与期望内容完全一致。
func sameFileContent(path string, want []byte) bool {
	got, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if bytes.Equal(got, want) {
		return true
	}
	return false
}

// renderSyncedREADME 在临时目录中渲染 README，避免 check 模式修改工作区。
func renderSyncedREADME(readmePath string, codemaps []ReadmeCodemap, generatedAt string) ([]byte, error) {
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
	if err := SyncREADME(tmpPath, codemaps, generatedAt); err != nil {
		return nil, err
	}
	return os.ReadFile(tmpPath)
}

// existingGeneratedAt 读取已有索引日期；读取失败或字段为空时返回 ok=false。
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

// countRefs 统计索引中全部文件引用数。
func countRefs(files map[string]*FileEntry) int {
	totalRefs := 0
	for _, f := range files {
		totalRefs += len(f.Refs)
	}
	return totalRefs
}

// loadCodemaps 读取编号 codemap markdown，并解析标题和章节范围。
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
		lines, err := readCodemapLines(path)
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

// buildRawFilesIndex 为每个源码文件查找命中的 codemap 段落范围。
func buildRawFilesIndex(root string, mds []parsedMD) (map[string][]rawRef, error) {
	srcFiles, err := ScanSourceFiles(root)
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

// buildCompactFilesIndex 将原始引用压缩为 section ID 引用，并按文件路径稳定排序。
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

// buildOutputCodemaps 生成对外 codemap 摘要和 README 同步数据。
func buildOutputCodemaps(mds []parsedMD) ([]Codemap, []ReadmeCodemap) {
	var codemaps []Codemap
	readmeCodemaps := make([]ReadmeCodemap, 0, len(mds))
	for _, md := range mds {
		var outSecs []Section
		for _, s := range md.sections {
			if s.Level <= 2 {
				outSecs = append(outSecs, s)
			}
		}
		cm := Codemap{ID: md.id, File: md.file, Title: md.title, TotalLines: len(md.lines), Sections: outSecs}
		codemaps = append(codemaps, cm)
		readmeCodemaps = append(readmeCodemaps, ReadmeCodemap{ID: cm.ID, File: cm.File, Title: cm.Title})
	}
	return codemaps, readmeCodemaps
}

// scanMDFiles 递归收集 codemap 目录下的 markdown 文件。
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

// readLines 按行读取文件内容，保留空行以维持行号稳定。
func readCodemapLines(p string) ([]string, error) {
	d, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(d), "\n"), nil
}

// extractTitle 返回 markdown 的第一个一级标题。
func extractTitle(lines []string) string {
	for _, l := range lines {
		if title, ok := strings.CutPrefix(strings.TrimSpace(l), "# "); ok {
			return title
		}
	}
	return ""
}

// headingRe 匹配一到四级 markdown 标题。
var headingRe = regexp.MustCompile(`^(#{1,4})\s+(.+)`)

// parseSections 解析 markdown 标题层级，并计算每个章节的行范围。
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

// findRefs 在 codemap 行中查找源码路径命中，并去重同一章节范围。
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

// blockRange 返回命中行所在的表格、代码块、列表项或段落范围。
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

// tableRange 返回命中行所在 markdown 表格的连续行范围。
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

// codeBlockRange 返回命中行所在 fenced code block 的行范围。
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

// listItemRange 返回命中行所在列表项及其缩进行的范围。
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

// paragraphRange 返回命中行所在普通段落的行范围。
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

// validateCodemapSemantics 将脚本解析结果投影到可复用的语义校验包。
func validateCodemapSemantics(root string, mds []parsedMD) error {
	return ValidateSemantics(root, semanticMarkdownDocs(mds))
}

// semanticMarkdownDocs 把生成器解析结果转换为 validator 与锚点清单的共享输入。
func semanticMarkdownDocs(mds []parsedMD) []SemanticMarkdown {
	docs := make([]SemanticMarkdown, 0, len(mds))
	for _, md := range mds {
		docs = append(docs, SemanticMarkdown{File: md.file, Lines: md.lines})
	}
	return docs
}
