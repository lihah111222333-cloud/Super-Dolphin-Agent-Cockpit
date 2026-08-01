package wails

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// maxCodeOpenFileBytes 是代码预览读取的 10 MiB 容量守卫。
const maxCodeOpenFileBytes int64 = 10 << 20

// codeSaveResult 是 ui/code/save 返回给前端的保存结果。
// JSON 字段保持前端既有 camelCase wire 名称，避免桌面 UI 版本间不兼容。
type codeSaveResult struct {
	Ok             bool   `json:"ok"`
	FilePath       string `json:"filePath"`
	Relative       string `json:"relative"`
	TotalLines     int    `json:"totalLines"`
	ContentVersion string `json:"contentVersion"`
}

// codeLocateMatch 描述一次 ui/code/locate 命中的文件元数据。
// 仅暴露轻量路径和大小信息，避免 locate 阶段读取大文件内容。
type codeLocateMatch struct {
	Path       string `json:"path"`
	Relative   string `json:"relative,omitempty"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	TotalLines int    `json:"totalLines,omitempty"`
}

// codeLocateResult 是 ui/code/locate 的返回载荷。
// Truncated 明确告诉前端结果被上限截断，不能把 paths 当作全量匹配集合。
type codeLocateResult struct {
	Ok        bool              `json:"ok"`
	Paths     []string          `json:"paths"`
	Truncated bool              `json:"truncated"`
	Matches   []codeLocateMatch `json:"matches,omitempty"`
}

// codeSnippetLine 是文本预览跨前端传输的单行结构。
// 行号和文本都用 omitempty，兼容旧前端只读取 snippet 文本的路径。
type codeSnippetLine struct {
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

// codeOpenResult 是 ui/code/open 的返回载荷，图片和文本预览共用该结构。
// LocateResult 不出现在 JSON wire 中，只供后端 handler 复用 locate 阶段结果。
type codeOpenResult struct {
	Ok             bool              `json:"ok"`
	Type           string            `json:"type,omitempty"`
	Path           string            `json:"path,omitempty"`
	FilePath       string            `json:"filePath,omitempty"`
	Relative       string            `json:"relative,omitempty"`
	Image          bool              `json:"image,omitempty"`
	MediaType      string            `json:"mediaType,omitempty"`
	SizeBytes      int64             `json:"sizeBytes,omitempty"`
	PreviewMode    string            `json:"previewMode,omitempty"`
	ContentVersion string            `json:"contentVersion,omitempty"`
	RangeStartLine int               `json:"rangeStartLine,omitempty"`
	RangeEndLine   int               `json:"rangeEndLine,omitempty"`
	StartLine      int               `json:"startLine,omitempty"`
	EndLine        int               `json:"endLine,omitempty"`
	TotalLines     int               `json:"totalLines,omitempty"`
	Snippet        any               `json:"snippet,omitempty"`
	Opened         bool              `json:"opened,omitempty"`
	LocateResult   *codeLocateResult `json:"-"`
}

// saveScopedFile 在允许范围内覆盖已有文件，并保持原文件权限。
func saveScopedFile(rawPath, content string, roots []string, createNew bool, previewMode, contentVersion string) (codeSaveResult, error) {
	if strings.TrimSpace(previewMode) != "full" {
		return codeSaveResult{}, errors.New("ui/code/save: previewMode must be full")
	}
	if strings.TrimSpace(contentVersion) == "" {
		return codeSaveResult{}, errors.New("ui/code/save: contentVersion is required")
	}
	target, err := resolveSaveTarget(rawPath, roots, createNew)
	if err != nil {
		return codeSaveResult{}, err
	}
	existing, err := os.ReadFile(target.Abs)
	if err != nil {
		return codeSaveResult{}, err
	}
	if version := codeContentVersion(existing); version != strings.TrimSpace(contentVersion) {
		return codeSaveResult{}, fmt.Errorf("ui/code/save: contentVersion mismatch for %q", target.Abs)
	}
	body := normalizeFileText(content)
	bodyBytes := []byte(body)
	if err := replaceFileAtomically(target.Abs, bodyBytes, writeFileMode(target.Abs), os.Rename); err != nil {
		return codeSaveResult{}, err
	}
	return codeSaveResult{
		Ok:             true,
		FilePath:       target.Abs,
		Relative:       target.Relative,
		TotalLines:     countTextLines(body),
		ContentVersion: codeContentVersion(bodyBytes),
	}, nil
}

type codeSaveRename func(oldPath, newPath string) error

// replaceFileAtomically 将完整内容先持久化到同目录临时文件，再原子替换已有目标。
// 发布前任一步失败都只清理临时文件，不截断或改写原文件。
func replaceFileAtomically(path string, data []byte, mode fs.FileMode, rename codeSaveRename) (retErr error) {
	if rename == nil {
		return errors.New("ui/code/save: atomic rename is required")
	}
	mode = mode.Perm()
	if mode == 0 {
		return errors.New("ui/code/save: target file mode is required")
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".save-*")
	if err != nil {
		return fmt.Errorf("ui/code/save: create temp file: %w", err)
	}
	tempPath := temp.Name()
	closed := false
	published := false
	defer func() {
		retErr = errors.Join(retErr, cleanupUnpublishedAtomicTemp(temp, tempPath, closed, published))
	}()
	closed, err = writeAndCloseAtomicTemp(temp, data, mode)
	if err != nil {
		return err
	}
	if err := rename(tempPath, path); err != nil {
		return fmt.Errorf("ui/code/save: replace target file: %w", err)
	}
	published = true
	return syncAtomicTargetDirectory(dir)
}

// writeAndCloseAtomicTemp 保留目标权限，持久化完整内容并确保关闭只尝试一次。
func writeAndCloseAtomicTemp(temp *os.File, data []byte, mode fs.FileMode) (bool, error) {
	if err := temp.Chmod(mode); err != nil {
		return false, fmt.Errorf("ui/code/save: preserve target mode: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return false, fmt.Errorf("ui/code/save: write temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return false, fmt.Errorf("ui/code/save: sync temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return true, fmt.Errorf("ui/code/save: close temp file: %w", err)
	}
	return true, nil
}

// cleanupUnpublishedAtomicTemp 关闭未关闭的临时文件，并仅在尚未发布时删除它。
func cleanupUnpublishedAtomicTemp(temp *os.File, tempPath string, closed, published bool) error {
	var cleanupErr error
	if !closed {
		cleanupErr = temp.Close()
	}
	if published {
		return cleanupErr
	}
	if err := os.Remove(tempPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("ui/code/save: remove temp file: %w", err))
	}
	return cleanupErr
}

// syncAtomicTargetDirectory 持久化目录项，并聚合目录同步与关闭错误。
func syncAtomicTargetDirectory(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("ui/code/save: open target directory for sync: %w", err)
	}
	syncErr := dirHandle.Sync()
	closeErr := dirHandle.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("ui/code/save: sync target directory: %w", err)
	}
	return nil
}

// locateScopedFile 查找项目范围内的候选文件并补充轻量元数据。
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

// openScopedFile 构建代码预览，并尽力在本地编辑器中打开对应位置。
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

// buildCodeOpenResult 构建代码打开结果；大文件、图片和二进制走不同预览策略。
func buildCodeOpenResult(target scopedPath, line int) (codeOpenResult, error) {
	info, err := os.Stat(target.Abs)
	if err != nil {
		return codeOpenResult{}, err
	}
	result := codeOpenResult{
		Ok:          true,
		Path:        target.Abs,
		FilePath:    target.Abs,
		Relative:    target.Relative,
		SizeBytes:   info.Size(),
		PreviewMode: "binary",
	}
	if info.Size() > maxCodeOpenFileBytes {
		return codeOpenResult{}, fmt.Errorf("ui/code/open: file %q exceeds preview size limit", target.Abs)
	}
	if mediaType := previewMediaType(target.Abs); mediaType != "" {
		sniffed, err := sniffPreviewImageMediaType(target.Abs)
		if err != nil {
			return codeOpenResult{}, err
		}
		if sniffed != mediaType {
			return codeOpenResult{}, fmt.Errorf("ui/code/open: image media type mismatch for %q: extension=%s sniffed=%s", target.Abs, mediaType, sniffed)
		}
		result.Type = "image"
		result.Image = true
		result.MediaType = mediaType
		result.PreviewMode = "image"
		return result, nil
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

// buildFullTextResult 返回适合全文展示的文本文件内容。
func buildFullTextResult(result codeOpenResult, data []byte) codeOpenResult {
	text := normalizeFileText(string(data))
	totalLines := countTextLines(text)
	result.PreviewMode = "full"
	result.ContentVersion = codeContentVersion(data)
	result.StartLine = 1
	result.EndLine = totalLines
	result.RangeStartLine = result.StartLine
	result.RangeEndLine = result.EndLine
	result.TotalLines = totalLines
	result.Snippet = text
	return result
}

// buildSnippetResult 返回围绕目标行的短代码片段。
func buildSnippetResult(result codeOpenResult, data []byte, line int) codeOpenResult {
	text := normalizeFileText(string(data))
	lines := splitTextLines(text)
	result.PreviewMode = "snippet"
	result.TotalLines = len(lines)
	if len(lines) == 0 {
		result.RangeStartLine = 0
		result.RangeEndLine = 0
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
	result.RangeStartLine = startLine
	result.RangeEndLine = endLine
	result.Snippet = snippet
	return result
}

// codeContentVersion returns the overwrite token for the exact bytes read from disk.
func codeContentVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

// readCodeLocateMatch 读取候选文件大小和行数，超大文件不计算行数。
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

// fileLineCount 统计文件行数，读取失败时返回 0 作为不可用元数据。
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

// writeFileMode 返回覆盖写入时应保留的文件权限。
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

// countTextLines 统计规范化换行后的文本行数。
func countTextLines(text string) int {
	return len(splitTextLines(normalizeFileText(text)))
}

// splitTextLines 按 LF 拆分文本，末尾换行不额外算空行。
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

// normalizeFileText 统一 CRLF/CR 换行为 LF。
func normalizeFileText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// snippetRange 计算最多 9 行的预览窗口，并尽量让目标行居中。
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
	start = max(start, 1)
	end := start + 8
	end = min(end, total)
	if span := end - start; span < 8 && start > 1 {
		start -= 8 - span
		start = max(start, 1)
	}
	return start, end
}

// isFullTextPreviewPath 判断文件是否适合直接全文返回。
func isFullTextPreviewPath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".md", ".markdown", ".txt", ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// previewMediaType 按扩展名识别可直接预览的位图图片类型。
func previewMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	default:
		return ""
	}
}

// sniffPreviewImageMediaType 读取文件头确认图片预览的真实媒体类型。
// 这里不只信任扩展名，避免非图片内容被包装成前端可渲染的 data URL。
func sniffPreviewImageMediaType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	header := make([]byte, 512)
	n, err := file.Read(header)
	if err != nil && err != io.EOF {
		return "", err
	}
	header = header[:n]
	if mediaType := imageMediaTypeFromMagic(header); mediaType != "" {
		return mediaType, nil
	}
	detected := http.DetectContentType(header)
	if strings.HasPrefix(detected, "image/") {
		return detected, nil
	}
	return "", fmt.Errorf("ui/code/open: image header is not a supported bitmap image for %q", path)
}

// imageMediaTypeFromMagic 根据常见图片魔数返回稳定媒体类型。
// 只覆盖 Wails 代码预览允许的位图格式，避免 SVG 等主动内容进入内联预览。
func imageMediaTypeFromMagic(header []byte) string {
	switch {
	case bytes.HasPrefix(header, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case bytes.HasPrefix(header, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(header, []byte("GIF87a")) || bytes.HasPrefix(header, []byte("GIF89a")):
		return "image/gif"
	case len(header) >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(header, []byte{0x00, 0x00, 0x01, 0x00}):
		return "image/x-icon"
	default:
		return ""
	}
}

// isBinaryPreview 判断字节内容是否不适合作为文本预览。
func isBinaryPreview(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

// openCodeEditor 尽力用本机编辑器或系统默认程序打开路径。
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
		return openWindowsPath(path)
	default:
		return false
	}
}

// codeOpenArgs 构造 VS Code -g 参数。
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

// renderXLSXSheet 将单个 XLSX sheet 渲染成 Markdown 表格。
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

// xlsxHeaderRow 生成表头，空表头用列号兜住展示。
func xlsxHeaderRow(row []string, colCount int) []string {
	header := xlsxSizedRow(row, colCount)
	for i, cell := range header {
		if strings.TrimSpace(cell) == "" {
			header[i] = fmt.Sprintf("列%d", i+1)
		}
	}
	return header
}

// xlsxSizedRow 将行裁剪或补齐到指定列数。
func xlsxSizedRow(row []string, colCount int) []string {
	out := make([]string, colCount)
	for i := 0; i < colCount && i < len(row); i++ {
		out[i] = row[i]
	}
	return out
}

// xlsxColumnCount 计算可展示列数，并施加 XLSX 列上限。
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

// xlsxRowHasText 判断行中是否存在非空文本。
func xlsxRowHasText(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return true
		}
	}
	return false
}

// markdownSeparatorRow 生成 Markdown 表格分隔行。
func markdownSeparatorRow(colCount int) string {
	cells := make([]string, colCount)
	for i := range cells {
		cells[i] = "---"
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

// markdownTableRow 生成 Markdown 表格数据行。
func markdownTableRow(cells []string) string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = markdownCell(cell)
	}
	return "| " + strings.Join(out, " | ") + " |"
}

// markdownCell 转义 Markdown 表格单元格并限制超长文本。
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

// xlsxTruncationNote 生成 XLSX 预览截断说明。
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

// resolveXLSXRelationshipTarget 将 workbook rel target 解析为 zip 内路径。
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

// readXLSXZipEntry 读取 XLSX zip 条目，并对单条目大小做上限保护。
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

// findXLSXZipEntry 在 zip 中按规范化路径查找条目。
func findXLSXZipEntry(reader *zip.Reader, name string) *zip.File {
	want := strings.TrimPrefix(path.Clean(name), "/")
	for _, file := range reader.File {
		if strings.TrimPrefix(path.Clean(file.Name), "/") == want {
			return file
		}
	}
	return nil
}

// xmlLocalAttr 从 XML start element 读取指定 local name 的属性。
func xmlLocalAttr(start xml.StartElement, local string) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

// decodeElementText 解码当前 XML 元素的文本内容。
func decodeElementText(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var text string
	if err := decoder.DecodeElement(&text, &start); err != nil {
		return "", err
	}
	return text, nil
}

// openSystemPath 用指定系统命令打开路径，命令和参数不经 shell。
func openSystemPath(command, path string) bool {
	binary, err := exec.LookPath(command)
	if err != nil {
		return false
	}
	// 误判防护：openSystemPath 通过 exec.Command(binary, path) 传参，不拼接 shell 命令。
	return exec.Command(binary, path).Run() == nil
}

// openWindowsPath 使用 Windows 文件协议处理器打开路径。
func openWindowsPath(path string) bool {
	command, args := windowsPathOpenCommand(path)
	binary, err := exec.LookPath(command)
	if err != nil {
		return false
	}
	return exec.Command(binary, args...).Run() == nil
}

// windowsPathOpenCommand 使用 argv 方式打开文件，避免 cmd /c start 解析路径中的 shell 字符。
func windowsPathOpenCommand(path string) (string, []string) {
	return "rundll32.exe", []string{"url.dll,FileProtocolHandler", path}
}

// newLineScanner 创建支持较长代码行的 Scanner。
func newLineScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return scanner
}

// intString 把 int 转成字符串并去掉保护性空白。
func intString(value int) string {
	return strings.TrimSpace(strconvItoa(value))
}

// strconvItoa 是轻量整数转字符串实现，避免在该文件额外引入 strconv。
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
