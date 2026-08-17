// Package lineprotocol 实现 mcp-lsp 结果使用的紧凑可逆文本语法。
package lineprotocol

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const grammarVersion = 1

// Header 保存限制前总数和实际展示数。
type Header struct {
	Total, Showing int
	Truncated      bool
	Unit           string
}

// ErrorHeader 保存失败结果的稳定错误码和可重试语义。
type ErrorHeader struct {
	Code      string
	Retryable bool
}

// Field 保存记录中的一个有序键值对。
type Field struct{ Key, Value string }

// Record 保存 header 后的一条已解码记录。
type Record struct {
	Kind, Value string
	Fields      map[string]string
}

// Document 保存一个解析成功的结果文档。
type Document struct {
	Header  Header
	Error   *ErrorHeader
	Records []Record
}

// Escape 只转义会破坏行与字段边界的字符，普通 UTF-8、路径和代码保持原样。
func Escape(value string) string {
	var escaped strings.Builder
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if r == utf8.RuneError && size == 1 {
			writeHexByte(&escaped, value[0])
			value = value[1:]
			continue
		}
		writeEscapedRune(&escaped, r)
		value = value[size:]
	}
	return escaped.String()
}

func writeEscapedRune(output *strings.Builder, value rune) {
	switch value {
	case '\\':
		output.WriteString(`\\`)
	case '\t':
		output.WriteString(`\t`)
	case '\n':
		output.WriteString(`\n`)
	case '\r':
		output.WriteString(`\r`)
	default:
		writeEscapedControlOrRune(output, value)
	}
}

func writeEscapedControlOrRune(output *strings.Builder, value rune) {
	if value < 0x20 || value == 0x7f {
		writeHexByte(output, byte(value))
		return
	}
	if unicode.IsControl(value) {
		fmt.Fprintf(output, `\u{%X}`, value)
		return
	}
	output.WriteRune(value)
}

func writeHexByte(output *strings.Builder, value byte) {
	fmt.Fprintf(output, `\x%02X`, value)
}

// HeaderLine 渲染稳定的 OK 头。
func HeaderLine(total, showing int, truncated bool, unit string) string {
	return fmt.Sprintf("OK total=%d showing=%d truncated=%d unit=%s", total, showing, boolInt(truncated), unit)
}

// ErrorLine 渲染稳定的 ERROR 头。
func ErrorLine(code string, retryable bool) string {
	code = strings.TrimSpace(code)
	return fmt.Sprintf("ERROR code=%s retryable=%d", code, boolInt(retryable))
}

// TextRecord 渲染带单个转义值的文本记录。
func TextRecord(kind, value string) string {
	return kind + "\t" + Escape(value)
}

// FieldsRecord 渲染 ROW、FILE 等有序字段记录。
func FieldsRecord(kind string, fields ...Field) string {
	parts := make([]string, 1, len(fields)+1)
	parts[0] = kind
	for _, field := range fields {
		parts = append(parts, field.Key+"="+Escape(field.Value))
	}
	return strings.Join(parts, "\t")
}

// Parse 严格校验并解码 OK 或 ERROR 文档，拒绝未知 header 字段和畸形转义。
func Parse(text string) (Document, error) {
	if strings.ContainsAny(text, "\r\x00") {
		return Document{}, fmt.Errorf("malformed line protocol: CR and NUL are forbidden")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return Document{}, fmt.Errorf("missing line protocol header")
	}
	doc, err := parseDocumentHeader(lines[0], len(lines)-1)
	if err != nil {
		return Document{}, err
	}
	for index, line := range lines[1:] {
		record, err := parseRecord(line)
		if err != nil {
			return Document{}, fmt.Errorf("malformed line protocol record %d: %w", index+2, err)
		}
		doc.Records = append(doc.Records, record)
	}
	if doc.Error != nil && !hasRecordKind(doc.Records, "MESSAGE") {
		return Document{}, fmt.Errorf("ERROR line protocol requires at least one MESSAGE record")
	}
	return doc, nil
}

func parseDocumentHeader(line string, recordCount int) (Document, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return Document{}, fmt.Errorf("missing line protocol header")
	}
	doc := Document{Records: make([]Record, 0, recordCount)}
	switch parts[0] {
	case "OK":
		header, err := parseOKHeader(parts)
		doc.Header = header
		return doc, err
	case "ERROR":
		header, err := parseErrorHeader(parts)
		doc.Error = header
		return doc, err
	default:
		return Document{}, fmt.Errorf("missing OK or ERROR line protocol header")
	}
}

// parseOKHeader 解码并校验唯一 OK header。
func parseOKHeader(parts []string) (Header, error) {
	values, err := parseHeaderFields("OK", parts[1:], map[string]bool{
		"total": true, "showing": true, "truncated": true, "unit": true,
	})
	if err != nil {
		return Header{}, err
	}
	for _, key := range []string{"total", "showing", "truncated", "unit"} {
		if values[key] == "" {
			return Header{}, fmt.Errorf("missing required OK field %q", key)
		}
	}
	total, err := nonNegativeInt("total", values["total"])
	if err != nil {
		return Header{}, err
	}
	showing, err := nonNegativeInt("showing", values["showing"])
	if err != nil {
		return Header{}, err
	}
	truncated, err := parseBit("truncated", values["truncated"])
	if err != nil {
		return Header{}, err
	}
	header := Header{Total: total, Showing: showing, Truncated: truncated, Unit: values["unit"]}
	if !validHeader(header) {
		return Header{}, fmt.Errorf("malformed OK count or unit contract")
	}
	return header, nil
}

func parseErrorHeader(parts []string) (*ErrorHeader, error) {
	values, err := parseHeaderFields("ERROR", parts[1:], map[string]bool{"code": true, "retryable": true})
	if err != nil {
		return nil, err
	}
	if !isToken(values["code"]) || values["retryable"] == "" {
		return nil, fmt.Errorf("missing or malformed required ERROR field")
	}
	retryable, err := parseBit("retryable", values["retryable"])
	if err != nil {
		return nil, err
	}
	return &ErrorHeader{Code: values["code"], Retryable: retryable}, nil
}

// parseHeaderFields 拒绝畸形、重复或未知的 header 字段。
func parseHeaderFields(kind string, parts []string, allowed map[string]bool) (map[string]string, error) {
	values := make(map[string]string, len(allowed))
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok || !isToken(key) {
			return nil, fmt.Errorf("malformed header field %q", part)
		}
		if !allowed[key] {
			return nil, fmt.Errorf("unknown %s field %q", kind, key)
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("duplicate %s field %q", kind, key)
		}
		values[key] = value
	}
	return values, nil
}

// parseRecord 解码文本记录或字段记录。
func parseRecord(line string) (Record, error) {
	parts := strings.Split(line, "\t")
	if len(parts) < 2 || !isRecordKind(parts[0]) {
		return Record{}, fmt.Errorf("invalid record kind")
	}
	if isTextRecord(parts[0]) {
		if len(parts) != 2 {
			return Record{}, fmt.Errorf("%s requires one value", parts[0])
		}
		value, err := unescape(parts[1])
		return Record{Kind: parts[0], Value: value}, err
	}
	record := Record{Kind: parts[0], Fields: make(map[string]string, len(parts)-1)}
	for _, part := range parts[1:] {
		key, encoded, ok := strings.Cut(part, "=")
		if !ok || !isToken(key) {
			return Record{}, fmt.Errorf("invalid field %q", part)
		}
		if _, duplicate := record.Fields[key]; duplicate {
			return Record{}, fmt.Errorf("duplicate field %q", key)
		}
		value, err := unescape(encoded)
		if err != nil {
			return Record{}, err
		}
		record.Fields[key] = value
	}
	return record, nil
}

func unescape(value string) (string, error) {
	var decoded strings.Builder
	for index := 0; index < len(value); {
		if value[index] == '\\' {
			text, consumed, err := decodeEscape(value[index:])
			if err != nil {
				return "", err
			}
			decoded.WriteString(text)
			index += consumed
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && size == 1 {
			return "", fmt.Errorf("invalid UTF-8 byte in line protocol value")
		}
		if unicode.IsControl(r) {
			return "", fmt.Errorf("raw control character in line protocol value")
		}
		decoded.WriteRune(r)
		index += size
	}
	return decoded.String(), nil
}

func decodeEscape(value string) (string, int, error) {
	if len(value) < 2 {
		return "", 0, fmt.Errorf("truncated line protocol escape")
	}
	switch value[1] {
	case '\\':
		return `\`, 2, nil
	case 't':
		return "\t", 2, nil
	case 'n':
		return "\n", 2, nil
	case 'r':
		return "\r", 2, nil
	case 'x':
		return decodeHexEscape(value)
	case 'u':
		return decodeUnicodeEscape(value)
	default:
		return "", 0, fmt.Errorf("unknown line protocol escape %q", value[:2])
	}
}

func decodeHexEscape(value string) (string, int, error) {
	if len(value) < 4 {
		return "", 0, fmt.Errorf("truncated line protocol hex escape")
	}
	parsed, err := strconv.ParseUint(value[2:4], 16, 8)
	if err != nil {
		return "", 0, fmt.Errorf("invalid line protocol hex escape %q", value[:4])
	}
	return string([]byte{byte(parsed)}), 4, nil
}

func decodeUnicodeEscape(value string) (string, int, error) {
	if !strings.HasPrefix(value, `\u{`) {
		return "", 0, fmt.Errorf("malformed line protocol unicode escape")
	}
	end := strings.IndexByte(value[3:], '}')
	if end < 1 {
		return "", 0, fmt.Errorf("truncated line protocol unicode escape")
	}
	end += 3
	parsed, err := strconv.ParseUint(value[3:end], 16, 32)
	r := rune(parsed)
	if err != nil || !utf8.ValidRune(r) || r <= 0x7f || !unicode.IsControl(r) {
		return "", 0, fmt.Errorf("invalid line protocol unicode escape %q", value[:end+1])
	}
	return string(r), end + 1, nil
}

func hasRecordKind(records []Record, kind string) bool {
	for _, record := range records {
		if record.Kind == kind {
			return true
		}
	}
	return false
}

func nonNegativeInt(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("malformed %s=%q", name, value)
	}
	return parsed, nil
}

func parseBit(name, value string) (bool, error) {
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("malformed %s=%q", name, value)
	}
}

func validHeader(header Header) bool {
	return header.Showing <= header.Total && header.Truncated == (header.Showing < header.Total) && isToken(header.Unit)
}

func isTextRecord(kind string) bool { return kind == "MESSAGE" || kind == "HINT" || kind == "WARNING" }

func isRecordKind(kind string) bool {
	return kind != "" && strings.Trim(kind, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == ""
}

func boolInt(value bool) int { return map[bool]int{true: 1}[value] }

// isToken 校验小写 snake token，首字符必须是字母。
func isToken(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			continue
		}
		return false
	}
	return true
}
