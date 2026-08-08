package pdftext

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractFileCIDToUnicodeAndIgnoreDecoy(t *testing.T) {
	path := writeFixture(t, pdfFixture([]string{"<0001> Tj"}, true, "BT (DECOY) Tj ET"))
	result, err := ExtractFile(context.Background(), path, Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "中" {
		t.Fatalf("text = %q, want 中", result.Text)
	}
	if strings.ContainsRune(result.Text, 0) || result.Metadata.NULRunes != 0 {
		t.Fatalf("unexpected NUL: %#v", result.Metadata)
	}
	if result.Metadata.PageCount != 1 || result.Metadata.ExtractorVersion != ExtractorVersion {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestExtractFilePreservesPageAndTextOperatorOrder(t *testing.T) {
	path := writeFixture(t, pdfFixture([]string{"<0001> Tj [<0002> -20 <0001>] TJ", "<0002> Tj"}, true, ""))
	result, err := ExtractFile(context.Background(), path, Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "中A中\nA" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestExtractFileRejectsUnknownType0Mapping(t *testing.T) {
	path := writeFixture(t, pdfFixture([]string{"<0001> Tj"}, false, ""))
	_, err := ExtractFile(context.Background(), path, Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	if !errors.Is(err, ErrQualityRejected) {
		t.Fatalf("error = %v, want quality rejection", err)
	}
}

func TestExtractFileEnforcesAggregateOutputBudget(t *testing.T) {
	path := writeFixture(t, pdfFixture([]string{"<0001> Tj", "<0002> Tj"}, true, ""))
	_, err := ExtractFile(context.Background(), path, Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 3})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("error = %v, want output budget", err)
	}
}

func TestExtractFileRejectsCompressedStreamOutputBudget(t *testing.T) {
	path := writeFixture(t, compressedPDFFixture(t, strings.Repeat("x", 32*1024)))
	_, err := ExtractFile(context.Background(), path, Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 128})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("error = %v, want compressed stream output budget rejection", err)
	}
}

func TestExtractFileHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ExtractFile(ctx, "unused.pdf", Limits{MaxInputBytes: 1, MaxOutputBytes: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestFinalizeNormalizesSparseExtractionArtifacts(t *testing.T) {
	text := strings.Repeat("可检索正文", 50_000) +
		strings.Repeat("\x00", 5) +
		strings.Repeat("\uFFFD", 68)
	metadata := Analyze(text, 190)
	result, err := Finalize(Result{Text: text, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(result.Text, 0) || strings.ContainsRune(result.Text, '\uFFFD') {
		t.Fatal("normalized text still contains extraction artifacts")
	}
	if result.Metadata.Status != "passed" || result.Metadata.NULRunes != 5 || result.Metadata.ReplacementRunes != 68 {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
	if !strings.Contains(result.Metadata.Reason, "normalized minor extraction artifacts") {
		t.Fatalf("quality reason = %q", result.Metadata.Reason)
	}
}

func TestFinalizeRejectsHighRatioExtractionArtifacts(t *testing.T) {
	text := strings.Repeat("正文", 100) + strings.Repeat("\uFFFD", 20)
	_, err := Finalize(Result{Text: text, Metadata: Analyze(text, 1)})
	if !errors.Is(err, ErrQualityRejected) {
		t.Fatalf("error = %v, want quality rejection", err)
	}
}

func writeFixture(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pdfFixture(pageOperators []string, withToUnicode bool, decoy string) []byte {
	pageIDs := make([]int, len(pageOperators))
	objects := []string{"", ""}
	for index, operators := range pageOperators {
		pageID := len(objects) + 1
		contentID := pageID + 1
		pageIDs[index] = pageID
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", 3+2*len(pageOperators), contentID),
			streamObject("BT /F1 12 Tf "+operators+" ET"),
		)
	}
	fontID := len(objects) + 1
	descendantID := fontID + 1
	cmapID := descendantID + 1
	toUnicode := ""
	if withToUnicode {
		toUnicode = fmt.Sprintf(" /ToUnicode %d 0 R", cmapID)
	}
	objects = append(objects,
		fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Fixture /Encoding /Identity-H /DescendantFonts [%d 0 R]%s >>", descendantID, toUnicode),
		"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Fixture /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> >>",
	)
	if withToUnicode {
		cmap := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def /CMapName /Fixture def /CMapType 2 def 1 begincodespacerange <0000> <FFFF> endcodespacerange 2 beginbfchar <0001> <4E2D> <0002> <0041> endbfchar endcmap CMapName currentdict /CMap defineresource pop end end"
		objects = append(objects, streamObject(cmap))
	}
	if decoy != "" {
		objects = append(objects, streamObject(decoy))
	}
	kids := make([]string, len(pageIDs))
	for index, id := range pageIDs {
		kids[index] = fmt.Sprintf("%d 0 R", id)
	}
	objects[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))

	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}

func streamObject(content string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
}

func compressedPDFFixture(t *testing.T, text string) []byte {
	t.Helper()
	body := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("compress PDF fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close PDF fixture compressor: %v", err)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", compressed.Len(), compressed.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
