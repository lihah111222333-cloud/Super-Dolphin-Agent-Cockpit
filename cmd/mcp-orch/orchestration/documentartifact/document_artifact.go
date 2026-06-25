package documentartifact

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
)

const (
	documentContentTypeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	documentContentTypePDF  = "application/pdf"
)

var cleanupRegistry sync.Map

// BuildImportParams 把结构化正文渲染成临时真实文件，并返回可交给 sharedfile importer 的参数。
func BuildImportParams(plan nodeexec.ArtifactTextPlan, updatedBy string) (sharedfilestore.ImportLocalFileParams, func(), error) {
	format, contentType, err := resolveDocumentArtifactFormat(plan.TargetPath, plan.ContentType)
	if err != nil {
		return sharedfilestore.ImportLocalFileParams{}, nil, err
	}
	data, err := renderDocumentArtifactBytes(format, plan.SourceText)
	if err != nil {
		return sharedfilestore.ImportLocalFileParams{}, nil, err
	}
	sourcePath, cleanup, err := writeDocumentArtifactTempFile(format, data)
	if err != nil {
		return sharedfilestore.ImportLocalFileParams{}, nil, err
	}
	params := sharedfilestore.ImportLocalFileParams{
		SourcePath:         sourcePath,
		TargetPath:         plan.TargetPath,
		ContentType:        contentType,
		AllowedExtensions:  []string{"." + format},
		AllowedSourceRoots: []string{filepath.Dir(sourcePath)},
		MaxBytes:           plan.MaxBytes,
		Overwrite:          plan.Overwrite,
		UpdatedBy:          updatedBy,
	}
	return params, cleanup, nil
}

// BuildImportParamsFromTarget 从 agent 原始结果里提取正文，并渲染成 importer 可读取的本地文件。
func BuildImportParamsFromTarget(target *nodeexec.ArtifactTarget, rawResult string, runID int64, updatedBy string) (sharedfilestore.ImportLocalFileParams, error) {
	if strings.TrimSpace(target.SourceTextField) == "" {
		plan, err := nodeexec.BuildArtifactImportPlan(target, rawResult, runID)
		if err != nil {
			return sharedfilestore.ImportLocalFileParams{}, err
		}
		params := sharedfilestore.ImportLocalFileParams{SourcePath: plan.SourcePath, TargetPath: plan.TargetPath, ContentType: plan.ContentType, AllowedExtensions: plan.AllowedExtensions, AllowedSourceRoots: plan.AllowedSourceRoots, MaxBytes: plan.MaxBytes, Overwrite: plan.Overwrite, UpdatedBy: updatedBy}
		return params, nil
	}
	plan, err := nodeexec.BuildArtifactTextPlan(target, rawResult, runID)
	if err != nil {
		return sharedfilestore.ImportLocalFileParams{}, err
	}
	params, cleanup, err := BuildImportParams(plan, updatedBy)
	if err != nil {
		return sharedfilestore.ImportLocalFileParams{}, err
	}
	cleanupRegistry.Store(params.SourcePath, cleanup)
	return params, nil
}

// CleanupSource 清理本包为文本 artifact 生成的临时源文件；普通本地文件 artifact 不会被删除。
func CleanupSource(sourcePath string) {
	if cleanup, ok := cleanupRegistry.LoadAndDelete(sourcePath); ok {
		cleanup.(func())()
	}
}

func resolveDocumentArtifactFormat(targetPath, contentType string) (string, string, error) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(targetPath)), ".")
	switch ext {
	case "docx":
		return "docx", firstNonEmptyContentType(contentType, documentContentTypeDOCX), nil
	case "pdf":
		return "pdf", firstNonEmptyContentType(contentType, documentContentTypePDF), nil
	default:
		return "", "", fmt.Errorf("generated document artifact target extension %q is not supported", ext)
	}
}

func firstNonEmptyContentType(raw, fallback string) string {
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		return trimmed
	}
	return fallback
}

func renderDocumentArtifactBytes(format, text string) ([]byte, error) {
	switch format {
	case "docx":
		return renderDOCXDocument(text)
	case "pdf":
		return renderPDFDocument(text)
	default:
		return nil, fmt.Errorf("generated document artifact format %q is not supported", format)
	}
}

// writeDocumentArtifactTempFile 把文档字节写入 os.TempDir 下的临时文件，返回路径和清理函数。
// 写入或关闭失败时 defer 保证不留临时文件；调用方在 import 完成后须调用 cleanup 删除文件。
func writeDocumentArtifactTempFile(format string, data []byte) (string, func(), error) {
	if len(data) == 0 {
		return "", nil, errors.New("generated document artifact is empty")
	}
	tmp, err := os.CreateTemp("", "super-dolphin-document-*."+format)
	if err != nil {
		return "", nil, fmt.Errorf("create generated document temp file: %w", err)
	}
	sourcePath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(sourcePath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", nil, fmt.Errorf("write generated document temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", nil, fmt.Errorf("sync generated document temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", nil, fmt.Errorf("close generated document temp file: %w", err)
	}
	keep = true
	return sourcePath, func() { _ = os.Remove(sourcePath) }, nil
}

func renderDOCXDocument(text string) ([]byte, error) {
	lines, err := normalizedDocumentLines(text)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		body string
	}{
		{name: "[Content_Types].xml", body: docxContentTypesXML},
		{name: "_rels/.rels", body: docxRootRelsXML},
		{name: "word/document.xml", body: docxDocumentXML(lines)},
	} {
		if err := writeZipEntry(zw, entry.name, []byte(entry.body)); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close docx zip: %w", err)
	}
	return buf.Bytes(), nil
}

func writeZipEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create docx entry %s: %w", name, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write docx entry %s: %w", name, err)
	}
	return nil
}

const docxContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

const docxRootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

func docxDocumentXML(lines []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	b.WriteString(`<w:body>`)
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			b.WriteString(`<w:p/>`)
			continue
		}
		b.WriteString(`<w:p><w:r><w:t xml:space="preserve">`)
		b.WriteString(xmlEscape(line))
		b.WriteString(`</w:t></w:r></w:p>`)
	}
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr>`)
	b.WriteString(`</w:body></w:document>`)
	return b.String()
}

func xmlEscape(text string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}

func normalizedDocumentLines(text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("generated document source text is empty")
	}
	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n"), nil
}

func renderPDFDocument(text string) ([]byte, error) {
	lines, err := normalizedDocumentLines(text)
	if err != nil {
		return nil, err
	}
	wrapped := wrapPDFLines(lines, 82)
	pages := paginateLines(wrapped, 48)
	return buildPDF(pages), nil
}

func wrapPDFLines(lines []string, maxWidth int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapPDFLine(line, maxWidth)...)
	}
	return out
}

// wrapPDFLine 对单行按视觉宽度折行（CJK 字符计 2，ASCII 计 1），保证 maxWidth 内换行。
func wrapPDFLine(line string, maxWidth int) []string {
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	var out []string
	var current strings.Builder
	width := 0
	for _, r := range line {
		weight := 1
		if r > 127 {
			weight = 2
		}
		if width+weight > maxWidth && current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
			width = 0
		}
		current.WriteRune(r)
		width += weight
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func paginateLines(lines []string, pageSize int) [][]string {
	if len(lines) == 0 {
		return [][]string{{""}}
	}
	var pages [][]string
	for start := 0; start < len(lines); start += pageSize {
		end := start + pageSize
		if end > len(lines) {
			end = len(lines)
		}
		pages = append(pages, lines[start:end])
	}
	return pages
}

func buildPDF(pages [][]string) []byte {
	pageObjectStart := 6
	objectCount := 5 + len(pages)*2
	offsets := make([]int, objectCount+1)
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	writePDFObject(&buf, offsets, 1, "<< /Type /Catalog /Pages 2 0 R >>")
	writePDFObject(&buf, offsets, 2, pdfPagesObject(pages, pageObjectStart))
	writePDFObject(&buf, offsets, 3, "<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [4 0 R] >>")
	writePDFObject(&buf, offsets, 4, "<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 5 >> /FontDescriptor 5 0 R /DW 1000 >>")
	writePDFObject(&buf, offsets, 5, "<< /Type /FontDescriptor /FontName /STSong-Light /Flags 4 /FontBBox [0 -200 1000 900] /ItalicAngle 0 /Ascent 880 /Descent -120 /CapHeight 880 /StemV 80 >>")
	for i, page := range pages {
		pageObj := pageObjectStart + i*2
		contentObj := pageObj + 1
		content := pdfPageContent(page)
		writePDFObject(&buf, offsets, pageObj, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentObj))
		writePDFStreamObject(&buf, offsets, contentObj, content)
	}
	writePDFXref(&buf, offsets, objectCount)
	return buf.Bytes()
}

func pdfPagesObject(pages [][]string, pageObjectStart int) string {
	kids := make([]string, 0, len(pages))
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObjectStart+i*2))
	}
	return fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
}

func pdfPageContent(lines []string) []byte {
	var b bytes.Buffer
	b.WriteString("BT\n/F1 11 Tf\n50 790 Td\n15 TL\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(&b, "<%s> Tj\n", pdfUTF16Hex(line))
		}
		b.WriteString("T*\n")
	}
	b.WriteString("ET\n")
	return b.Bytes()
}

func pdfUTF16Hex(text string) string {
	encoded := utf16.Encode([]rune(text))
	var b strings.Builder
	for _, unit := range encoded {
		fmt.Fprintf(&b, "%04X", unit)
	}
	return b.String()
}

func writePDFObject(buf *bytes.Buffer, offsets []int, id int, body string) {
	offsets[id] = buf.Len()
	fmt.Fprintf(buf, "%d 0 obj\n%s\nendobj\n", id, body)
}

func writePDFStreamObject(buf *bytes.Buffer, offsets []int, id int, content []byte) {
	offsets[id] = buf.Len()
	fmt.Fprintf(buf, "%d 0 obj\n<< /Length %d >>\nstream\n", id, len(content))
	buf.Write(content)
	buf.WriteString("endstream\nendobj\n")
}

func writePDFXref(buf *bytes.Buffer, offsets []int, objectCount int) {
	xref := buf.Len()
	fmt.Fprintf(buf, "xref\n0 %d\n", objectCount+1)
	buf.WriteString("0000000000 65535 f \n")
	for id := 1; id <= objectCount; id++ {
		fmt.Fprintf(buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", objectCount+1, xref)
}
