package wails

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

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
	location := path
	if line > 0 {
		if column <= 0 {
			column = 1
		}
		location = path + ":" + strings.TrimSpace(intString(line)) + ":" + strings.TrimSpace(intString(column))
	}
	return []string{"-g", location}
}

func openSystemPath(command, path string) bool {
	binary, err := exec.LookPath(command)
	if err != nil {
		return false
	}
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
