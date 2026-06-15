package wails

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	droppedFileAccessTTL           = 5 * time.Minute
	maxDroppedTextFileBytes  int64 = 2 << 20
	maxDroppedXLSXFileBytes  int64 = 8 << 20
	maxDroppedXLSXEntryBytes       = 8 << 20
	maxDroppedXLSXSheets           = 10
	maxDroppedXLSXRows             = 200
	maxDroppedXLSXCols             = 20
	maxDroppedXLSXCellRunes        = 500
)

type droppedFileRecord struct {
	TargetID  string
	ExpiresAt time.Time
}

type readDroppedTextFilesParams struct {
	Files    []string `json:"files"`
	TargetID string   `json:"targetId,omitempty"`
	clientMetaParams
}

type droppedTextFile struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Text      string `json:"text"`
	SizeBytes int64  `json:"sizeBytes"`
}

type readDroppedTextFilesResult struct {
	Files []droppedTextFile `json:"files"`
}

// recordDroppedFiles 记录dropped文件。
func (a *App) recordDroppedFiles(files []string, details *application.DropTargetDetails) {
	if a == nil || len(files) == 0 {
		return
	}
	targetID := ""
	if details != nil {
		targetID = strings.TrimSpace(details.ElementID)
	}
	now := time.Now()

	a.droppedFilesMu.Lock()
	defer a.droppedFilesMu.Unlock()
	if a.droppedFiles == nil {
		a.droppedFiles = make(map[string]droppedFileRecord)
	}
	a.pruneDroppedFileRecordsLocked(now)
	for _, raw := range files {
		path, err := normalizeDroppedFilePath(raw)
		if err != nil {
			continue
		}
		a.droppedFiles[path] = droppedFileRecord{
			TargetID:  targetID,
			ExpiresAt: now.Add(droppedFileAccessTTL),
		}
	}
}

// hasRecentDroppedFile 判断recentdropped文件是否可用。
func (a *App) hasRecentDroppedFile(raw, targetID string) bool {
	if a == nil {
		return false
	}
	path, err := normalizeDroppedFilePath(raw)
	if err != nil {
		return false
	}
	now := time.Now()
	wantTarget := strings.TrimSpace(targetID)

	a.droppedFilesMu.Lock()
	defer a.droppedFilesMu.Unlock()
	a.pruneDroppedFileRecordsLocked(now)
	record, ok := a.droppedFiles[path]
	if !ok || now.After(record.ExpiresAt) {
		delete(a.droppedFiles, path)
		return false
	}
	if wantTarget != "" && record.TargetID != "" && record.TargetID != wantTarget {
		return false
	}
	return true
}

func (a *App) pruneDroppedFileRecordsLocked(now time.Time) {
	for path, record := range a.droppedFiles {
		if now.After(record.ExpiresAt) {
			delete(a.droppedFiles, path)
		}
	}
}

func readDroppedTextFiles(app *App, params readDroppedTextFilesParams) (readDroppedTextFilesResult, error) {
	paths, err := normalizeDroppedFileList(params.Files)
	if err != nil {
		return readDroppedTextFilesResult{}, err
	}
	if len(paths) == 0 {
		return readDroppedTextFilesResult{}, errors.New("ui/readDroppedTextFiles: files are required")
	}
	result := readDroppedTextFilesResult{
		Files: make([]droppedTextFile, 0, len(paths)),
	}
	for _, path := range paths {
		file, err := readOneDroppedTextFile(app, path, params.TargetID)
		if err != nil {
			return readDroppedTextFilesResult{}, err
		}
		result.Files = append(result.Files, file)
	}
	return result, nil
}

func normalizeDroppedFileList(files []string) ([]string, error) {
	out := make([]string, 0, len(files))
	seen := map[string]struct{}{}
	for _, raw := range files {
		path, err := normalizeDroppedFilePath(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out, nil
}

func readOneDroppedTextFile(app *App, rawPath, targetID string) (droppedTextFile, error) {
	path, err := normalizeDroppedFilePath(rawPath)
	if err != nil {
		return droppedTextFile{}, err
	}
	if !app.hasRecentDroppedFile(path, targetID) {
		return droppedTextFile{}, fmt.Errorf("ui/readDroppedTextFiles: file %q was not recently dropped into this window", path)
	}
	info, data, err := readDroppedFileBytes(path)
	if err != nil {
		return droppedTextFile{}, err
	}
	text, err := droppedFileImportText(path, data)
	if err != nil {
		return droppedTextFile{}, err
	}
	return droppedTextFile{
		Path:      path,
		Name:      filepath.Base(path),
		Text:      text,
		SizeBytes: info.Size(),
	}, nil
}

// readDroppedFileBytes 读取dropped文件bytes。
func readDroppedFileBytes(path string) (os.FileInfo, []byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("ui/readDroppedTextFiles: path %q is a directory", path)
	}
	if limit, kind := maxDroppedImportFileBytes(path); info.Size() > limit {
		return nil, nil, fmt.Errorf("ui/readDroppedTextFiles: file %q exceeds %s import size limit", path, kind)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("ui/readDroppedTextFiles: file %q is empty", path)
	}
	return info, data, nil
}

func maxDroppedImportFileBytes(path string) (int64, string) {
	if isDroppedXLSXPath(path) {
		return maxDroppedXLSXFileBytes, "xlsx"
	}
	return maxDroppedTextFileBytes, "text"
}

// droppedFileImportText 处理dropped文件import文本。
func droppedFileImportText(path string, data []byte) (string, error) {
	if isDroppedXLSXPath(path) {
		text, err := droppedXLSXText(data)
		if err != nil {
			return "", fmt.Errorf("ui/readDroppedTextFiles: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("ui/readDroppedTextFiles: xlsx file %q has no readable table content", path)
		}
		return text, nil
	}
	if isBinaryPreview(data) {
		return "", fmt.Errorf("ui/readDroppedTextFiles: binary file is not supported: %q", path)
	}
	text := normalizeFileText(string(data))
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("ui/readDroppedTextFiles: file %q has no text content", path)
	}
	return text, nil
}

func normalizeDroppedFilePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("ui/readDroppedTextFiles: file path is required")
	}
	absPath, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absPath), nil
}

func isDroppedXLSXPath(path string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(path)), ".xlsx")
}

type xlsxWorkbookSheet struct {
	Name string
	RID  string
}

func droppedXLSXText(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("invalid xlsx file")
	}
	sheets, err := parseXLSXWorkbookSheets(reader)
	if err != nil {
		return "", err
	}
	rels, err := parseXLSXWorkbookRelationships(reader)
	if err != nil {
		return "", err
	}
	shared, err := parseXLSXSharedStrings(reader)
	if err != nil {
		return "", err
	}
	return renderXLSXWorkbook(reader, sheets, rels, shared)
}

// renderXLSXWorkbook 渲染xlsxworkbook。
func renderXLSXWorkbook(reader *zip.Reader, sheets []xlsxWorkbookSheet, rels map[string]string, shared []string) (string, error) {
	sections := make([]string, 0, len(sheets))
	for index, sheet := range sheets {
		if index >= maxDroppedXLSXSheets {
			sections = append(sections, fmt.Sprintf("（已截断：仅显示前 %d 个 Sheet）", maxDroppedXLSXSheets))
			break
		}
		target := resolveXLSXRelationshipTarget(rels[sheet.RID])
		if target == "" {
			return "", fmt.Errorf("xlsx sheet %q has no worksheet relationship", sheet.Name)
		}
		rows, truncatedRows, truncatedCols, err := parseXLSXWorksheet(reader, target, shared)
		if err != nil {
			return "", err
		}
		if section := renderXLSXSheet(sheet.Name, rows, truncatedRows, truncatedCols); section != "" {
			sections = append(sections, section)
		}
	}
	if len(sections) == 0 {
		return "", errors.New("xlsx file has no readable table content")
	}
	return strings.Join(sections, "\n\n"), nil
}

// parseXLSXWorkbookSheets 解析xlsxworkbooksheets。
func parseXLSXWorkbookSheets(reader *zip.Reader) ([]xlsxWorkbookSheet, error) {
	data, err := readXLSXZipEntry(reader, "xl/workbook.xml", true)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var sheets []xlsxWorkbookSheet
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read xlsx workbook.xml: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		sheet := xlsxWorkbookSheet{
			Name: strings.TrimSpace(xmlLocalAttr(start, "name")),
			RID:  strings.TrimSpace(xmlLocalAttr(start, "id")),
		}
		if sheet.Name == "" {
			sheet.Name = fmt.Sprintf("Sheet%d", len(sheets)+1)
		}
		sheets = append(sheets, sheet)
	}
	if len(sheets) == 0 {
		return nil, errors.New("xlsx workbook has no sheets")
	}
	return sheets, nil
}

// parseXLSXWorkbookRelationships 解析xlsxworkbookrelationships。
func parseXLSXWorkbookRelationships(reader *zip.Reader) (map[string]string, error) {
	data, err := readXLSXZipEntry(reader, "xl/_rels/workbook.xml.rels", true)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	rels := map[string]string{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read xlsx workbook relationships: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		id := strings.TrimSpace(xmlLocalAttr(start, "Id"))
		target := strings.TrimSpace(xmlLocalAttr(start, "Target"))
		if id != "" && target != "" {
			rels[id] = target
		}
	}
	return rels, nil
}

// parseXLSXSharedStrings 解析xlsxsharedstrings。
func parseXLSXSharedStrings(reader *zip.Reader) ([]string, error) {
	data, err := readXLSXZipEntry(reader, "xl/sharedStrings.xml", false)
	if err != nil || data == nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read xlsx shared strings: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "si" {
			continue
		}
		text, err := parseXLSXSharedString(decoder)
		if err != nil {
			return nil, err
		}
		out = append(out, text)
	}
}

// parseXLSXSharedString 解析xlsxsharedstring。
func parseXLSXSharedString(decoder *xml.Decoder) (string, error) {
	var parts []string
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("read xlsx shared string: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "t" {
				text, err := decodeElementText(decoder, value)
				if err != nil {
					return "", err
				}
				parts = append(parts, text)
			}
		case xml.EndElement:
			if value.Name.Local == "si" {
				return strings.Join(parts, ""), nil
			}
		}
	}
}

// parseXLSXWorksheet 解析xlsxworksheet。
func parseXLSXWorksheet(reader *zip.Reader, entry string, shared []string) ([][]string, bool, bool, error) {
	data, err := readXLSXZipEntry(reader, entry, true)
	if err != nil {
		return nil, false, false, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var rows [][]string
	var truncatedRows bool
	var truncatedCols bool
	for {
		row, rowTruncatedCols, ok, err := nextXLSXWorksheetRow(decoder, shared)
		if err != nil {
			return nil, false, false, fmt.Errorf("read xlsx worksheet %s: %w", entry, err)
		}
		if !ok {
			return rows, truncatedRows, truncatedCols, nil
		}
		truncatedCols = truncatedCols || rowTruncatedCols
		if !xlsxRowHasText(row) {
			continue
		}
		if len(rows) >= maxDroppedXLSXRows {
			truncatedRows = true
			continue
		}
		rows = append(rows, row)
	}
}

// nextXLSXWorksheetRow 处理nextxlsxworksheetrow。
func nextXLSXWorksheetRow(decoder *xml.Decoder, shared []string) ([]string, bool, bool, error) {
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil, false, false, nil
		}
		if err != nil {
			return nil, false, false, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		row, truncated, err := parseXLSXRow(decoder, shared)
		return row, truncated, true, err
	}
}

// parseXLSXRow 解析xlsxrow。
func parseXLSXRow(decoder *xml.Decoder, shared []string) ([]string, bool, error) {
	cells := map[int]string{}
	maxCol := 0
	nextCol := 1
	truncatedCols := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, false, fmt.Errorf("read xlsx row: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local != "c" {
				continue
			}
			col := xlsxCellColumn(value, nextCol)
			nextCol = col + 1
			text, err := parseXLSXCell(decoder, value, shared)
			if err != nil {
				return nil, false, err
			}
			if col > maxDroppedXLSXCols {
				truncatedCols = true
				continue
			}
			cells[col] = text
			if col > maxCol {
				maxCol = col
			}
		case xml.EndElement:
			if value.Name.Local == "row" {
				return xlsxCellsToRow(cells, maxCol), truncatedCols, nil
			}
		}
	}
}

// parseXLSXCell 解析xlsxcell。
func parseXLSXCell(decoder *xml.Decoder, start xml.StartElement, shared []string) (string, error) {
	cellType := strings.TrimSpace(xmlLocalAttr(start, "t"))
	rawValue := ""
	var inlineParts []string
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("read xlsx cell: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "v":
				rawValue, err = decodeElementText(decoder, value)
				if err != nil {
					return "", err
				}
			case "t":
				text, err := decodeElementText(decoder, value)
				if err != nil {
					return "", err
				}
				inlineParts = append(inlineParts, text)
			}
		case xml.EndElement:
			if value.Name.Local == "c" {
				return xlsxCellText(cellType, rawValue, strings.Join(inlineParts, ""), shared)
			}
		}
	}
}

// xlsxCellText 处理xlsxcell文本。
func xlsxCellText(cellType, rawValue, inlineValue string, shared []string) (string, error) {
	switch cellType {
	case "s":
		index, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err != nil || index < 0 || index >= len(shared) {
			return "", fmt.Errorf("xlsx shared string index %q is invalid", rawValue)
		}
		return shared[index], nil
	case "inlineStr":
		return inlineValue, nil
	case "b":
		if strings.TrimSpace(rawValue) == "1" {
			return "TRUE", nil
		}
		return "FALSE", nil
	default:
		if inlineValue != "" {
			return inlineValue, nil
		}
		return rawValue, nil
	}
}

func xlsxCellColumn(start xml.StartElement, fallback int) int {
	if col := xlsxColumnIndex(xmlLocalAttr(start, "r")); col > 0 {
		return col
	}
	return fallback
}

// xlsxColumnIndex 处理xlsxcolumn索引。
func xlsxColumnIndex(ref string) int {
	col := 0
	for _, r := range ref {
		switch {
		case r >= 'A' && r <= 'Z':
			col = col*26 + int(r-'A'+1)
		case r >= 'a' && r <= 'z':
			col = col*26 + int(r-'a'+1)
		default:
			return col
		}
	}
	return col
}

func xlsxCellsToRow(cells map[int]string, maxCol int) []string {
	if maxCol <= 0 {
		return nil
	}
	row := make([]string, maxCol)
	for col, text := range cells {
		if col > 0 && col <= maxCol {
			row[col-1] = text
		}
	}
	return row
}
