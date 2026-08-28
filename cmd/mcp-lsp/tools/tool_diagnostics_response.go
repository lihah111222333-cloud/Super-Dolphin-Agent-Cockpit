package tools

import (
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// ToPlainText 按稳定行协议渲染 diagnostics producer，供 gate 和模型共用同一文本真值。
func (r diagnosticsResponse) ToPlainText() string {
	if !r.Success {
		return "ERROR code=diagnostics_failed retryable=0\n" + lineprotocol.TextRecord("MESSAGE", "diagnostics retrieval failed")
	}
	lines := []string{lineprotocol.HeaderLine(r.Total, r.Showing, r.Truncated, "diagnostic")}
	lines = append(lines, diagnosticsResponseMessage(r.Meta.Message)...)
	lines = append(lines, diagnosticsResponseFilter(r.Meta.DiffFilter)...)
	lines = append(lines, diagnosticsResponseRows(r.Data)...)
	if hint := strings.TrimSpace(r.Hint); hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", hint))
	}
	return strings.Join(lines, "\n")
}

func diagnosticsResponseMessage(message string) []string {
	if message = strings.TrimSpace(message); message != "" {
		return []string{lineprotocol.TextRecord("MESSAGE", message)}
	}
	return nil
}

func diagnosticsResponseFilter(filter *diagnosticsFilterMeta) []string {
	if filter == nil {
		return nil
	}
	lines := []string{lineprotocol.FieldsRecord("FILTERED",
		lineprotocol.Field{Key: "mode", Value: filter.Mode},
		lineprotocol.Field{Key: "count", Value: fmt.Sprint(filter.Filtered)},
	)}
	for _, entry := range filter.FilteredDiagnostics {
		lines = append(lines, lineprotocol.FieldsRecord("FILTERED", diagnosticsFilteredFields(entry)...))
	}
	return lines
}

func diagnosticsFilteredFields(entry diagnosticsFilteredEntry) []lineprotocol.Field {
	fields := []lineprotocol.Field{
		{Key: "file", Value: entry.File},
		{Key: "line", Value: fmt.Sprint(entry.Line)},
		{Key: "col", Value: fmt.Sprint(entry.Column)},
		{Key: "severity", Value: entry.Severity},
	}
	fields = appendOptionalDiagnosticField(fields, "source", entry.Source)
	fields = appendOptionalDiagnosticField(fields, "code", entry.Code)
	fields = appendOptionalDiagnosticField(fields, "message", entry.Message)
	return fields
}

func appendOptionalDiagnosticField(fields []lineprotocol.Field, key string, value string) []lineprotocol.Field {
	if value == "" {
		return fields
	}
	return append(fields, lineprotocol.Field{Key: key, Value: value})
}

// diagnosticsResponseRows 将表格行按文件和原始顺序转换为稳定 ROW 记录。
func diagnosticsResponseRows(tables []diagnosticsTable) []string {
	var lines []string
	for _, table := range tables {
		for _, row := range table.Rows {
			if fields := diagnosticTextFields(table.File, row); fields != nil {
				lines = append(lines, lineprotocol.FieldsRecord("ROW", fields...))
			}
		}
	}
	return lines
}

func diagnosticTextFields(file string, row []any) []lineprotocol.Field {
	if len(row) < 4 {
		return nil
	}
	fields := []lineprotocol.Field{
		{Key: "file", Value: file},
		{Key: "line", Value: fmt.Sprint(numericRowValue(row[0]))},
		{Key: "col", Value: fmt.Sprint(numericRowValue(row[1]))},
		{Key: "severity", Value: fmt.Sprint(row[2])},
		{Key: "message", Value: fmt.Sprint(row[3])},
	}
	for index, key := range []string{"source", "code"} {
		if position := index + 4; position < len(row) && fmt.Sprint(row[position]) != "" {
			fields = append(fields, lineprotocol.Field{Key: key, Value: fmt.Sprint(row[position])})
		}
	}
	return fields
}
