// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var errPDFTextNotFound = errors.New("datasource: pdf text content not found")

// extractPDFText 从 PDF 文件中提取所有 stream 的可读文本。
// 这是轻量解析器，只服务 datasource 文本导入；无可用文本时返回 errPDFTextNotFound，避免空内容入库。
func extractPDFText(sourcePath string) (string, error) {
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read pdf datasource: %w", err)
	}
	streams, err := extractPDFStreams(content)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(streams))
	for _, stream := range streams {
		if text := extractPDFTextFromStream(stream); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", errPDFTextNotFound
	}
	return strings.Join(parts, "\n"), nil
}

// extractPDFStreams 扫描 PDF stream/endstream 片段并按字典声明解压内容。
// stream 边界损坏或压缩流无法解码时直接返回错误，避免导入不完整文本。
func extractPDFStreams(content []byte) ([][]byte, error) {
	streams := make([][]byte, 0)
	offset := 0
	for {
		streamIndex := bytes.Index(content[offset:], []byte("stream"))
		if streamIndex < 0 {
			break
		}
		streamStart := offset + streamIndex
		bodyStart := skipPDFStreamLineBreak(content, streamStart+len("stream"))
		streamEndRelative := bytes.Index(content[bodyStart:], []byte("endstream"))
		if streamEndRelative < 0 {
			return nil, errors.New("datasource: malformed pdf stream")
		}
		streamEnd := bodyStart + streamEndRelative
		streamBody := bytes.TrimRight(content[bodyStart:streamEnd], "\r\n")
		decoded, err := decodePDFStream(pdfStreamDictionary(content[:streamStart]), streamBody)
		if err != nil {
			return nil, err
		}
		streams = append(streams, decoded)
		offset = streamEnd + len("endstream")
	}
	return streams, nil
}

// skipPDFStreamLineBreak 跳过 stream 关键字后的换行符（支持 CR、LF 和 CRLF）。
func skipPDFStreamLineBreak(content []byte, index int) int {
	if index < len(content) && content[index] == '\r' {
		index++
		if index < len(content) && content[index] == '\n' {
			return index + 1
		}
		return index
	}
	if index < len(content) && content[index] == '\n' {
		return index + 1
	}
	return index
}

// pdfStreamDictionary 从 stream 前缀中取最近的字典片段。
// 找不到完整字典时返回 nil，后续解码会按未压缩 stream 处理。
func pdfStreamDictionary(prefix []byte) []byte {
	start := bytes.LastIndex(prefix, []byte("<<"))
	end := bytes.LastIndex(prefix, []byte(">>"))
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	return prefix[start : end+2]
}

// decodePDFStream 根据 stream 字典决定是否执行 FlateDecode 解压。
// 当前只支持明文和 zlib 压缩流，解压失败会阻断 datasource 导入。
func decodePDFStream(dictionary, body []byte) ([]byte, error) {
	if !bytes.Contains(dictionary, []byte("/FlateDecode")) {
		return body, nil
	}
	reader, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode flate pdf stream: %w", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read flate pdf stream: %w", err)
	}
	return decoded, nil
}

// extractPDFTextFromStream 从解码后的 stream 中收集 literal string 和 hex string。
// 注释、字典标记和空片段会被跳过，最后按 PDF 文本片段顺序合并。
func extractPDFTextFromStream(stream []byte) string {
	parts := make([]string, 0)
	for index := 0; index < len(stream); index++ {
		switch stream[index] {
		case '%':
			index = skipPDFComment(stream, index)
		case '(':
			text, next := readPDFLiteralString(stream, index)
			index = next
			if text != "" {
				parts = append(parts, text)
			}
		case '<':
			if index+1 < len(stream) && stream[index+1] == '<' {
				continue
			}
			text, next := readPDFHexString(stream, index)
			index = next
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(normalizePDFTextParts(parts), " ")
}

// skipPDFComment 跳过以 % 开头的 PDF 注释行。
func skipPDFComment(stream []byte, index int) int {
	for index < len(stream) && stream[index] != '\n' && stream[index] != '\r' {
		index++
	}
	return index
}

// readPDFLiteralString 读取括号包裹的 PDF literal string。
// 它维护括号嵌套深度并处理反斜杠转义，未闭合时返回空文本和末尾位置。
func readPDFLiteralString(stream []byte, start int) (string, int) {
	var out []byte
	depth := 1
	for index := start + 1; index < len(stream); index++ {
		ch := stream[index]
		if ch == '\\' {
			decoded, next := readPDFEscapedByte(stream, index)
			index = next
			if decoded >= 0 {
				out = append(out, byte(decoded))
			}
			continue
		}
		if ch == '(' {
			depth++
		}
		if ch == ')' {
			depth--
			if depth == 0 {
				return pdfBytesToString(out), index
			}
		}
		out = append(out, ch)
	}
	return "", len(stream) - 1
}

// readPDFEscapedByte 解析 literal string 中反斜杠后的单个转义。
// 返回 -1 表示续行转义不产生字符，第二个返回值是调用方应继续扫描的位置。
func readPDFEscapedByte(stream []byte, slash int) (int, int) {
	if slash+1 >= len(stream) {
		return -1, slash
	}
	ch := stream[slash+1]
	if decoded, ok := simplePDFEscape(ch); ok {
		return decoded, slash + 1
	}
	if ch == '\n' || ch == '\r' {
		return -1, skipPDFEscapedLineBreak(stream, slash+1)
	}
	if ch >= '0' && ch <= '7' {
		return readPDFOctalEscape(stream, slash+1)
	}
	return int(ch), slash + 1
}

// simplePDFEscape 处理 PDF 单字符转义序列（\n \r \t \b \f \( \) \\）。
func simplePDFEscape(ch byte) (int, bool) {
	switch ch {
	case 'n':
		return int('\n'), true
	case 'r':
		return int('\r'), true
	case 't':
		return int('\t'), true
	case 'b':
		return int('\b'), true
	case 'f':
		return int('\f'), true
	case '(', ')', '\\':
		return int(ch), true
	default:
		return 0, false
	}
}

// skipPDFEscapedLineBreak 跳过反斜杠续行后的换行符（CR、CRLF）。
func skipPDFEscapedLineBreak(stream []byte, index int) int {
	if stream[index] == '\r' && index+1 < len(stream) && stream[index+1] == '\n' {
		return index + 1
	}
	return index
}

// readPDFOctalEscape 解析最多 3 位的 PDF 八进制转义序列。
func readPDFOctalEscape(stream []byte, start int) (int, int) {
	value := 0
	index := start
	for ; index < len(stream) && index < start+3; index++ {
		ch := stream[index]
		if ch < '0' || ch > '7' {
			break
		}
		value = value*8 + int(ch-'0')
	}
	return value, index - 1
}

// readPDFHexString 读取尖括号包裹的 PDF hex string。
// 奇数字节按 PDF 规则补零；非法 hex 内容被视为不可读文本并跳过。
func readPDFHexString(stream []byte, start int) (string, int) {
	end := start + 1
	for end < len(stream) && stream[end] != '>' {
		end++
	}
	if end >= len(stream) {
		return "", len(stream) - 1
	}
	raw := stripPDFHexWhitespace(stream[start+1 : end])
	if len(raw)%2 == 1 {
		raw = append(raw, '0')
	}
	decoded := make([]byte, hex.DecodedLen(len(raw)))
	if _, err := hex.Decode(decoded, raw); err != nil {
		return "", end
	}
	return pdfBytesToString(decoded), end
}

// stripPDFHexWhitespace 移除 hex string 中允许出现的空白和 NUL 字节。
func stripPDFHexWhitespace(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	for _, ch := range raw {
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' && ch != '\f' && ch != 0 {
			out = append(out, ch)
		}
	}
	return out
}

// pdfBytesToString 将 PDF 字节串转为 Go 字符串。
// UTF-16BE BOM 优先按 Unicode 解码，其他非法 UTF-8 字节会被安全剔除。
func pdfBytesToString(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return utf16BEToString(raw[2:])
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	return strings.ToValidUTF8(string(raw), "")
}

// utf16BEToString 将偶数字节序列按 big-endian UTF-16 转为字符串。
func utf16BEToString(raw []byte) string {
	codePoints := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		codePoints = append(codePoints, uint16(raw[index])<<8|uint16(raw[index+1]))
	}
	return string(utf16.Decode(codePoints))
}

// normalizePDFTextParts 压缩片段内部空白并丢弃空文本。
func normalizePDFTextParts(parts []string) []string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Join(strings.Fields(part), " ")
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	return normalized
}
