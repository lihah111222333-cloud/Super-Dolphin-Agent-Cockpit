package wails

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// 误判防护：maxCodeOpenFileBytes 是代码预览读取的 10 MiB 容量守卫。
const maxCodeOpenFileBytes int64 = 10 << 20

type codeSaveResult struct {
	Ok         bool   `json:"ok"`
	FilePath   string `json:"filePath"`
	Relative   string `json:"relative"`
	TotalLines int    `json:"totalLines"`
}

type codeLocateMatch struct {
	Path       string `json:"path"`
	Relative   string `json:"relative,omitempty"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	TotalLines int    `json:"totalLines,omitempty"`
}

type codeLocateResult struct {
	Ok        bool              `json:"ok"`
	Paths     []string          `json:"paths"`
	Truncated bool              `json:"truncated"`
	Matches   []codeLocateMatch `json:"matches,omitempty"`
}

type codeSnippetLine struct {
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

type codeOpenResult struct {
	Ok           bool              `json:"ok"`
	Type         string            `json:"type,omitempty"`
	Path         string            `json:"path,omitempty"`
	FilePath     string            `json:"filePath,omitempty"`
	Relative     string            `json:"relative,omitempty"`
	Image        bool              `json:"image,omitempty"`
	MediaType    string            `json:"mediaType,omitempty"`
	SizeBytes    int64             `json:"sizeBytes,omitempty"`
	StartLine    int               `json:"startLine,omitempty"`
	EndLine      int               `json:"endLine,omitempty"`
	TotalLines   int               `json:"totalLines,omitempty"`
	Snippet      any               `json:"snippet,omitempty"`
	Opened       bool              `json:"opened,omitempty"`
	LocateResult *codeLocateResult `json:"-"`
}

func saveScopedFile(rawPath, content string, roots []string, createNew bool) (codeSaveResult, error) {
	target, err := resolveSaveTarget(rawPath, roots, createNew)
	if err != nil {
		return codeSaveResult{}, err
	}
	body := normalizeFileText(content)
	if err := os.MkdirAll(filepath.Dir(target.Abs), 0o755); err != nil {
		return codeSaveResult{}, err
	}
	if err := os.WriteFile(target.Abs, []byte(body), writeFileMode(target.Abs)); err != nil {
		return codeSaveResult{}, err
	}
	return codeSaveResult{
		Ok:         true,
		FilePath:   target.Abs,
		Relative:   target.Relative,
		TotalLines: countTextLines(body),
	}, nil
}

func locateScopedFile(ctx context.Context, rawPath string, roots []string, limit int) (codeLocateResult, error) {
	matches, truncated, err := findScopedFiles(ctx, rawPath, roots, limit)
	if err != nil {
		return codeLocateResult{}, err
	}
	result := codeLocateResult{
		Ok:        true,
		Paths:     make([]string, 0, len(matches)),
		Truncated: truncated,
		Matches:   make([]codeLocateMatch, 0, len(matches)),
	}
	for _, match := range matches {
		meta := readCodeLocateMatch(match)
		result.Paths = append(result.Paths, match.Abs)
		result.Matches = append(result.Matches, meta)
	}
	return result, nil
}

func openScopedFile(ctx context.Context, rawPath string, line, column int, roots []string) (codeOpenResult, error) {
	target, err := resolveOpenTarget(ctx, rawPath, roots)
	if err != nil {
		return codeOpenResult{}, err
	}
	result, err := buildCodeOpenResult(target, line)
	if err != nil {
		return codeOpenResult{}, err
	}
	result.Opened = openCodeEditor(target.Abs, line, column)
	return result, nil
}

// buildCodeOpenResult 构建代码打开结果。
func buildCodeOpenResult(target scopedPath, line int) (codeOpenResult, error) {
	info, err := os.Stat(target.Abs)
	if err != nil {
		return codeOpenResult{}, err
	}
	result := codeOpenResult{
		Ok:        true,
		Path:      target.Abs,
		FilePath:  target.Abs,
		Relative:  target.Relative,
		SizeBytes: info.Size(),
	}
	if mediaType := previewMediaType(target.Abs); mediaType != "" {
		result.Type = "image"
		result.Image = true
		result.MediaType = mediaType
		return result, nil
	}
	if info.Size() > maxCodeOpenFileBytes {
		return codeOpenResult{}, fmt.Errorf("ui/code/open: file %q exceeds preview size limit", target.Abs)
	}
	data, err := os.ReadFile(target.Abs)
	if err != nil {
		return codeOpenResult{}, err
	}
	if isFullTextPreviewPath(target.Relative) {
		return buildFullTextResult(result, data), nil
	}
	if isBinaryPreview(data) {
		return result, nil
	}
	return buildSnippetResult(result, data, line), nil
}

func buildFullTextResult(result codeOpenResult, data []byte) codeOpenResult {
	text := normalizeFileText(string(data))
	totalLines := countTextLines(text)
	result.StartLine = 1
	result.EndLine = totalLines
	result.TotalLines = totalLines
	result.Snippet = text
	return result
}

func buildSnippetResult(result codeOpenResult, data []byte, line int) codeOpenResult {
	text := normalizeFileText(string(data))
	lines := splitTextLines(text)
	result.TotalLines = len(lines)
	if len(lines) == 0 {
		result.Snippet = []codeSnippetLine{}
		return result
	}
	startLine, endLine := snippetRange(line, len(lines))
	snippet := make([]codeSnippetLine, 0, endLine-startLine+1)
	for current := startLine; current <= endLine; current++ {
		snippet = append(snippet, codeSnippetLine{Line: current, Text: lines[current-1]})
	}
	result.StartLine = startLine
	result.EndLine = endLine
	result.Snippet = snippet
	return result
}

func readCodeLocateMatch(target scopedPath) codeLocateMatch {
	info, err := os.Stat(target.Abs)
	if err != nil {
		return codeLocateMatch{Path: target.Abs, Relative: target.Relative}
	}
	totalLines := 0
	if info.Size() <= maxCodeOpenFileBytes {
		totalLines = fileLineCount(target.Abs)
	}
	return codeLocateMatch{
		Path:       target.Abs,
		Relative:   target.Relative,
		SizeBytes:  info.Size(),
		TotalLines: totalLines,
	}
}

func fileLineCount(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := newLineScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		return 0
	}
	return lines
}

func writeFileMode(path string) fs.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return 0o644
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		return 0o644
	}
	return mode
}

func countTextLines(text string) int {
	return len(splitTextLines(normalizeFileText(text)))
}

func splitTextLines(text string) []string {
	if text == "" {
		return []string{}
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func normalizeFileText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// snippetRange 处理snippet范围。
func snippetRange(line, total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	current := line
	if current <= 0 {
		current = 1
	}
	if current > total {
		current = total
	}
	start := current - 3
	if start < 1 {
		start = 1
	}
	end := start + 8
	if end > total {
		end = total
	}
	if span := end - start; span < 8 && start > 1 {
		start -= 8 - span
		if start < 1 {
			start = 1
		}
	}
	return start, end
}

func isFullTextPreviewPath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".md", ".markdown", ".txt", ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// previewMediaType 处理previewmediatype。
func previewMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	default:
		return ""
	}
}

func isBinaryPreview(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

func openCodeEditor(path string, line, column int) bool {
	if command, err := exec.LookPath("code"); err == nil {
		// 误判防护：openCodeEditor 使用 exec.Command argv，不经 shell 拼接执行路径。
		return exec.Command(command, codeOpenArgs(path, line, column)...).Run() == nil
	}
	switch runtime.GOOS {
	case "darwin":
		return openSystemPath("open", path)
	case "linux":
		return openSystemPath("xdg-open", path)
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Run() == nil
	default:
		return false
	}
}

func codeOpenArgs(path string, line, column int) []string {
	// 误判防护：codeOpenArgs 只构造 argv 参数，配合 openCodeEditor 避免 shell 注入。
	if line <= 0 {
		return []string{path}
	}
	location := path
	if column <= 0 {
		column = 1
	}
	location = path + ":" + strings.TrimSpace(intString(line)) + ":" + strings.TrimSpace(intString(column))
	return []string{"-g", location}
}

func renderXLSXSheet(name string, rows [][]string, truncatedRows, truncatedCols bool) string {
	colCount := xlsxColumnCount(rows)
	if colCount == 0 {
		return ""
	}
	header := xlsxHeaderRow(rows[0], colCount)
	lines := []string{"Sheet：" + name, "", markdownTableRow(header), markdownSeparatorRow(colCount)}
	for _, row := range rows[1:] {
		lines = append(lines, markdownTableRow(xlsxSizedRow(row, colCount)))
	}
	if truncatedRows || truncatedCols {
		lines = append(lines, "", xlsxTruncationNote(truncatedRows, truncatedCols))
	}
	return strings.Join(lines, "\n")
}

func xlsxHeaderRow(row []string, colCount int) []string {
	header := xlsxSizedRow(row, colCount)
	for i, cell := range header {
		if strings.TrimSpace(cell) == "" {
			header[i] = fmt.Sprintf("列%d", i+1)
		}
	}
	return header
}

func xlsxSizedRow(row []string, colCount int) []string {
	out := make([]string, colCount)
	for i := 0; i < colCount && i < len(row); i++ {
		out[i] = row[i]
	}
	return out
}

func xlsxColumnCount(rows [][]string) int {
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols > maxDroppedXLSXCols {
		return maxDroppedXLSXCols
	}
	return maxCols
}

func xlsxRowHasText(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return true
		}
	}
	return false
}

func markdownSeparatorRow(colCount int) string {
	cells := make([]string, colCount)
	for i := range cells {
		cells[i] = "---"
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

func markdownTableRow(cells []string) string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = markdownCell(cell)
	}
	return "| " + strings.Join(out, " | ") + " |"
}

func markdownCell(text string) string {
	value := strings.TrimSpace(normalizeFileText(text))
	value = strings.ReplaceAll(value, "\n", "<br>")
	value = strings.ReplaceAll(value, "|", `\|`)
	runes := []rune(value)
	if len(runes) > maxDroppedXLSXCellRunes {
		value = string(runes[:maxDroppedXLSXCellRunes]) + "..."
	}
	return value
}

func xlsxTruncationNote(truncatedRows, truncatedCols bool) string {
	parts := make([]string, 0, 2)
	if truncatedRows {
		parts = append(parts, fmt.Sprintf("前 %d 行", maxDroppedXLSXRows))
	}
	if truncatedCols {
		parts = append(parts, fmt.Sprintf("前 %d 列", maxDroppedXLSXCols))
	}
	return "（已截断：仅显示" + strings.Join(parts, "、") + "）"
}

func resolveXLSXRelationshipTarget(target string) string {
	value := strings.ReplaceAll(strings.TrimSpace(target), "\\", "/")
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "/") {
		return strings.TrimPrefix(path.Clean(value), "/")
	}
	return path.Clean(path.Join("xl", value))
}

// readXLSXZipEntry 读取xlsxzip条目。
func readXLSXZipEntry(reader *zip.Reader, name string, required bool) ([]byte, error) {
	file := findXLSXZipEntry(reader, name)
	if file == nil {
		if required {
			return nil, fmt.Errorf("xlsx entry %q is missing", name)
		}
		return nil, nil
	}
	if file.UncompressedSize64 > maxDroppedXLSXEntryBytes {
		return nil, fmt.Errorf("xlsx entry %q exceeds import size limit", name)
	}
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(rc, maxDroppedXLSXEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if n > maxDroppedXLSXEntryBytes {
		return nil, fmt.Errorf("xlsx entry %q exceeds import size limit", name)
	}
	return buf.Bytes(), nil
}

func findXLSXZipEntry(reader *zip.Reader, name string) *zip.File {
	want := strings.TrimPrefix(path.Clean(name), "/")
	for _, file := range reader.File {
		if strings.TrimPrefix(path.Clean(file.Name), "/") == want {
			return file
		}
	}
	return nil
}

func xmlLocalAttr(start xml.StartElement, local string) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

func decodeElementText(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var text string
	if err := decoder.DecodeElement(&text, &start); err != nil {
		return "", err
	}
	return text, nil
}

func openSystemPath(command, path string) bool {
	binary, err := exec.LookPath(command)
	if err != nil {
		return false
	}
	// 误判防护：openSystemPath 通过 exec.Command(binary, path) 传参，不拼接 shell 命令。
	return exec.Command(binary, path).Run() == nil
}

func newLineScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return scanner
}

func intString(value int) string {
	return strings.TrimSpace(strconvItoa(value))
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	buffer := [20]byte{}
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + (value % 10))
		value /= 10
	}
	return sign + string(buffer[index:])
}
