package memory

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	entrypointMaxLines      = 200
	entrypointMaxCodeUnits  = 25_000
	agentMemoryMaxLines     = entrypointMaxLines
	agentMemoryMaxCodeUnits = entrypointMaxCodeUnits
)

type EntrypointTruncation struct {
	Content          string
	LineCount        int
	CodeUnitCount    int
	WasLineTruncated bool
	WasByteTruncated bool
	Warning          string
}

type agentMemoryTruncation struct {
	content          string
	lineCount        int
	codeUnitCount    int
	wasLineTruncated bool
	wasByteTruncated bool
}

func TruncateEntrypointContent(raw string) EntrypointTruncation {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return EntrypointTruncation{}
	}

	contentLines := strings.Split(trimmed, "\n")
	result := EntrypointTruncation{
		Content:       trimmed,
		LineCount:     len(contentLines),
		CodeUnitCount: jsStringLength(trimmed),
	}
	result.WasLineTruncated = result.LineCount > entrypointMaxLines
	result.WasByteTruncated = result.CodeUnitCount > entrypointMaxCodeUnits
	if !result.WasLineTruncated && !result.WasByteTruncated {
		return result
	}

	truncated := trimmed
	if result.WasLineTruncated {
		truncated = strings.Join(contentLines[:entrypointMaxLines], "\n")
	}
	if jsStringLength(truncated) > entrypointMaxCodeUnits {
		truncated = truncateAtCodeUnitLimit(truncated, entrypointMaxCodeUnits)
	}
	result.Warning = "MEMORY.md is " + truncateEntrypointReason(result) + ". Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files."
	result.Content = truncated + "\n\n> WARNING: " + result.Warning
	return result
}

func truncateAgentMemoryContent(raw string) agentMemoryTruncation {
	result := TruncateEntrypointContent(raw)
	return agentMemoryTruncation{
		content:          result.Content,
		lineCount:        result.LineCount,
		codeUnitCount:    result.CodeUnitCount,
		wasLineTruncated: result.WasLineTruncated,
		wasByteTruncated: result.WasByteTruncated,
	}
}

func truncateAgentMemoryReason(result agentMemoryTruncation) string {
	return truncateEntrypointReason(EntrypointTruncation{
		LineCount:        result.lineCount,
		CodeUnitCount:    result.codeUnitCount,
		WasLineTruncated: result.wasLineTruncated,
		WasByteTruncated: result.wasByteTruncated,
	})
}

func truncateEntrypointReason(result EntrypointTruncation) string {
	switch {
	case result.WasByteTruncated && !result.WasLineTruncated:
		return fmt.Sprintf("%s (limit: %s) — index entries are too long", formatEntrypointSize(result.CodeUnitCount), formatEntrypointSize(entrypointMaxCodeUnits))
	case result.WasLineTruncated && !result.WasByteTruncated:
		return fmt.Sprintf("%d lines (limit: %d)", result.LineCount, entrypointMaxLines)
	default:
		return fmt.Sprintf("%d lines and %s", result.LineCount, formatEntrypointSize(result.CodeUnitCount))
	}
}

func truncateAtCodeUnitLimit(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if jsStringLength(content) <= limit {
		return content
	}

	bytePos := 0
	codeUnits := 0
	lastNewline := -1
	for bytePos < len(content) {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		units := utf16CodeUnits(r)
		if codeUnits+units > limit {
			break
		}
		if r == '\n' {
			lastNewline = bytePos
		}
		codeUnits += units
		bytePos += size
	}
	if lastNewline > 0 {
		return content[:lastNewline]
	}
	return content[:bytePos]
}

func jsStringLength(content string) int {
	count := 0
	for bytePos := 0; bytePos < len(content); {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		count += utf16CodeUnits(r)
		bytePos += size
	}
	return count
}

func utf16CodeUnits(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

func formatEntrypointSize(size int) string {
	kb := float64(size) / 1024
	if kb < 1 {
		return fmt.Sprintf("%d bytes", size)
	}
	if kb < 1024 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", kb), ".0") + "KB"
	}
	mb := kb / 1024
	if mb < 1024 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", mb), ".0") + "MB"
	}
	gb := mb / 1024
	return strings.TrimSuffix(fmt.Sprintf("%.1f", gb), ".0") + "GB"
}
