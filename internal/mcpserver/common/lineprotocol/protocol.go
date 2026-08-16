// Package lineprotocol 实现 mcp-lsp 结果使用的紧凑可逆文本语法。
package lineprotocol

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Header 保存限制前总数和实际展示数。
type Header struct {
	Total, Showing int
	Truncated      bool
	Unit           string
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
	Records []Record
}

// Escape 将任意 UTF-8 文本编码为可逆单行值。
func Escape(value string) string { return url.QueryEscape(value) }

// HeaderLine 渲染稳定的 OK 头。
func HeaderLine(total, showing int, truncated bool, unit string) string {
	return fmt.Sprintf("OK total=%d showing=%d truncated=%d unit=%s", total, showing, boolInt(truncated), unit)
}

// TextRecord 渲染带单个转义值的文本记录。
func TextRecord(kind, value string) string { return kind + "\t" + Escape(value) }

// FieldsRecord 渲染 ROW、FILE 等有序字段记录。
func FieldsRecord(kind string, fields ...Field) string {
	parts := make([]string, 1, len(fields)+1)
	parts[0] = kind
	for _, field := range fields {
		parts = append(parts, field.Key+"="+Escape(field.Value))
	}
	return strings.Join(parts, "\t")
}

// Parse 严格校验并解码成功结果，拒绝未知 header 字段。
func Parse(text string) (Document, error) {
	if strings.ContainsAny(text, "\r\x00") {
		return Document{}, fmt.Errorf("malformed line protocol: CR and NUL are forbidden")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return Document{}, fmt.Errorf("missing line protocol header")
	}
	header, err := parseHeader(lines[0])
	if err != nil {
		return Document{}, err
	}
	doc := Document{Header: header, Records: make([]Record, 0, len(lines)-1)}
	for index, line := range lines[1:] {
		record, err := parseRecord(line)
		if err != nil {
			return Document{}, fmt.Errorf("malformed line protocol record %d: %w", index+2, err)
		}
		doc.Records = append(doc.Records, record)
	}
	return doc, nil
}

// parseHeader 解码并校验唯一 OK header。
func parseHeader(line string) (Header, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 || parts[0] != "OK" {
		return Header{}, fmt.Errorf("missing OK line protocol header")
	}
	values, err := parseHeaderFields(parts[1:])
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

// parseHeaderFields 拒绝畸形、重复或未知的 OK 字段。
func parseHeaderFields(parts []string) (map[string]string, error) {
	values := make(map[string]string, 4)
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok || !isToken(key) {
			return nil, fmt.Errorf("malformed header field %q", part)
		}
		switch key {
		case "total", "showing", "truncated", "unit":
		default:
			return nil, fmt.Errorf("unknown OK field %q", key)
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("duplicate OK field %q", key)
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
		value, err := url.QueryUnescape(parts[1])
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
		value, err := url.QueryUnescape(encoded)
		if err != nil {
			return Record{}, err
		}
		record.Fields[key] = value
	}
	return record, nil
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
