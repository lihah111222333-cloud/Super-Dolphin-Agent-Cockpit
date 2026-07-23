// Package pdftext 提供 PDF 正文抽取和统一质量门禁。
package pdftext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	pdf "github.com/dslipak/pdf"
)

const (
	// ExtractorName 是持久化审计信息使用的稳定抽取器名称。
	ExtractorName = "github.com/dslipak/pdf"
	// ExtractorVersion 是固定依赖版本，升级时必须重新运行 CID/ToUnicode fixture。
	ExtractorVersion = "v0.0.2"
	// maxRepairableCorruptRunes 限制单篇文档可显式规范化的异常字符绝对数量。
	maxRepairableCorruptRunes int64 = 256
	// maxRepairableCorruptRunesPerMillion 限制长文档异常字符占比为千分之一。
	maxRepairableCorruptRunesPerMillion int64 = 1000
	// smallDocumentRepairAllowance 允许短文本修复极少量孤立异常字符。
	smallDocumentRepairAllowance int64 = 8
)

var (
	// ErrInputTooLarge 表示 PDF 文件超过输入预算。
	ErrInputTooLarge = errors.New("pdftext: input exceeds budget")
	// ErrOutputTooLarge 表示所有页面累计正文超过输出预算。
	ErrOutputTooLarge = errors.New("pdftext: extracted text exceeds aggregate budget")
	// ErrQualityRejected 表示正文未通过可检索质量门禁。
	ErrQualityRejected = errors.New("pdftext: extracted text failed quality gate")
)

// Limits 限制输入文件和所有页面累计输出字节数。
type Limits struct {
	MaxInputBytes  int64
	MaxOutputBytes int64
}

// Metadata 是可持久化的抽取器与正文质量统计。
type Metadata struct {
	Status           string
	Reason           string
	ExtractorName    string
	ExtractorVersion string
	PageCount        int32
	RuneCount        int64
	VisibleRunes     int64
	ControlRunes     int64
	NULRunes         int64
	ReplacementRunes int64
	UnmappedFonts    int64
}

// Result 返回通过质量门禁的正文及审计元数据。
type Result struct {
	Text     string
	Metadata Metadata
}

// ExtractFile 按 page tree 顺序抽取所有页面正文，并在返回前完成聚合预算和质量校验。
func ExtractFile(ctx context.Context, path string, limits Limits) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("pdftext: context is required")
	}
	if limits.MaxInputBytes <= 0 || limits.MaxOutputBytes <= 0 {
		return Result{}, errors.New("pdftext: positive input and output budgets are required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	content, err := readFile(ctx, path, limits.MaxInputBytes)
	if err != nil {
		return Result{}, err
	}
	reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return Result{}, fmt.Errorf("pdftext: open pdf: %w", err)
	}
	pageCount := reader.NumPage()
	if pageCount <= 0 {
		return Result{}, fmt.Errorf("%w: page count must be positive", ErrQualityRejected)
	}

	text, err := extractPages(ctx, reader, pageCount, limits.MaxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Finalize(Result{Text: text, Metadata: Analyze(text, int32(pageCount))})
}

// extractPages 按 page tree 顺序抽取正文并聚合解压与输出预算。
func extractPages(ctx context.Context, reader *pdf.Reader, pageCount int, maxOutputBytes int64) (string, error) {
	var text strings.Builder
	remainingDecoded := maxOutputBytes
	for pageNumber := 1; pageNumber <= pageCount; pageNumber++ {
		pageText, err := extractPage(ctx, reader.Page(pageNumber), pageNumber, &remainingDecoded)
		if err != nil {
			return "", err
		}
		separatorBytes := 0
		if pageNumber > 1 {
			separatorBytes = 1
		}
		if int64(text.Len())+int64(separatorBytes)+int64(len(pageText)) > maxOutputBytes {
			return "", ErrOutputTooLarge
		}
		if separatorBytes > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(pageText)
	}
	return text.String(), nil
}

// extractPage 完整校验并抽取单页正文，任一页面失败都会阻断整篇结果。
func extractPage(ctx context.Context, page pdf.Page, pageNumber int, remainingDecoded *int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if page.V.IsNull() {
		return "", fmt.Errorf("pdftext: page %d is unavailable", pageNumber)
	}
	if err := rejectUnknownMappings(page, pageNumber); err != nil {
		return "", err
	}
	if err := preflightPageContents(ctx, page.V.Key("Contents"), remainingDecoded); err != nil {
		return "", fmt.Errorf("pdftext: inspect page %d contents: %w", pageNumber, err)
	}
	pageText, err := page.GetPlainText(nil)
	if err != nil {
		return "", fmt.Errorf("pdftext: extract page %d: %w", pageNumber, err)
	}
	return pageText, nil
}

// preflightPageContents 流式解码页面内容流并从整篇聚合预算扣减。
func preflightPageContents(ctx context.Context, value pdf.Value, remaining *int64) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("decode content stream: %v", recovered)
		}
	}()
	switch value.Kind() {
	case pdf.Array:
		for index := 0; index < value.Len(); index++ {
			if err := preflightPageContents(ctx, value.Index(index), remaining); err != nil {
				return err
			}
		}
		return nil
	case pdf.Stream:
		reader := value.Reader()
		defer reader.Close()
		read, err := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: io.LimitReader(reader, *remaining+1)})
		if err != nil {
			return err
		}
		if read > *remaining {
			return ErrOutputTooLarge
		}
		*remaining -= read
		return nil
	default:
		return errors.New("page contents must be a stream or stream array")
	}
}

func readFile(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pdftext: open file: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(&contextReader{ctx: ctx, reader: io.LimitReader(file, maxBytes+1)})
	if err != nil {
		return nil, fmt.Errorf("pdftext: read file: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, ErrInputTooLarge
	}
	return content, nil
}

func rejectUnknownMappings(page pdf.Page, pageNumber int) error {
	for _, name := range page.Fonts() {
		font := page.Font(name)
		if font.V.Key("Subtype").Name() != "Type0" {
			continue
		}
		if font.V.Key("ToUnicode").Kind() != pdf.Stream {
			return fmt.Errorf("%w: page %d Type0 font %s has no ToUnicode map", ErrQualityRejected, pageNumber, name)
		}
	}
	return nil
}

// Analyze 统计已抽取文本，供共享写入门禁和非 PDF 文本导入复用。
func Analyze(text string, pageCount int32) Metadata {
	metadata := Metadata{Status: "passed", ExtractorName: ExtractorName, ExtractorVersion: ExtractorVersion, PageCount: pageCount}
	for _, r := range text {
		metadata.RuneCount++
		switch {
		case r == 0:
			metadata.NULRunes++
		case r == unicode.ReplacementChar:
			metadata.ReplacementRunes++
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			metadata.ControlRunes++
		}
		if unicode.IsPrint(r) && !unicode.IsSpace(r) {
			metadata.VisibleRunes++
		}
	}
	return metadata
}

// Finalize 对少量抽取瑕疵做显式、可审计的规范化，并阻断结构性或高比例污染。
func Finalize(result Result) (Result, error) {
	if err := Validate(result); err != nil {
		return Result{}, err
	}
	corruptRunes := extractionCorruptRunes(result.Metadata)
	if corruptRunes == 0 {
		result.Metadata.Status = "passed"
		result.Metadata.Reason = ""
		return result, nil
	}
	result.Text = normalizeMinorExtractionArtifacts(result.Text)
	if Analyze(result.Text, result.Metadata.PageCount).VisibleRunes <= 0 {
		return Result{}, fmt.Errorf("%w: visible body is empty after normalization", ErrQualityRejected)
	}
	result.Metadata.Status = "passed"
	result.Metadata.Reason = fmt.Sprintf(
		"normalized minor extraction artifacts: NUL=%d replacement=%d control=%d",
		result.Metadata.NULRunes,
		result.Metadata.ReplacementRunes,
		result.Metadata.ControlRunes,
	)
	return result, nil
}

// Validate 对抽取结果执行写 chunk、embedding 和 ready 之前的统一质量门禁。
func Validate(result Result) error {
	m := result.Metadata
	switch {
	case m.PageCount <= 0:
		return fmt.Errorf("%w: page count must be positive", ErrQualityRejected)
	case !utf8.ValidString(result.Text):
		return fmt.Errorf("%w: text is not valid UTF-8", ErrQualityRejected)
	case m.VisibleRunes <= 0:
		return fmt.Errorf("%w: visible body is empty", ErrQualityRejected)
	case m.UnmappedFonts != 0:
		return fmt.Errorf("%w: unmapped font count is %d", ErrQualityRejected, m.UnmappedFonts)
	case !repairableExtractionArtifacts(m):
		return fmt.Errorf(
			"%w: extraction artifacts exceed repair budget (NUL=%d replacement=%d control=%d runes=%d)",
			ErrQualityRejected,
			m.NULRunes,
			m.ReplacementRunes,
			m.ControlRunes,
			m.RuneCount,
		)
	default:
		return nil
	}
}

func extractionCorruptRunes(metadata Metadata) int64 {
	return metadata.NULRunes + metadata.ReplacementRunes + metadata.ControlRunes
}

func repairableExtractionArtifacts(metadata Metadata) bool {
	corruptRunes := extractionCorruptRunes(metadata)
	if corruptRunes == 0 || corruptRunes <= smallDocumentRepairAllowance {
		return true
	}
	if metadata.RuneCount <= 0 || corruptRunes > maxRepairableCorruptRunes {
		return false
	}
	return corruptRunes*1_000_000 <= metadata.RuneCount*maxRepairableCorruptRunesPerMillion
}

func normalizeMinorExtractionArtifacts(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0, r == unicode.ReplacementChar:
			return ' '
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			return ' '
		default:
			return r
		}
	}, text)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每次底层读取前传播 context 取消。
func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
