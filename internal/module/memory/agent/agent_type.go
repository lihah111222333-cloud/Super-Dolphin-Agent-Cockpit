package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"golang.org/x/text/unicode/norm"
)

const agentTypeMaxLen = 128

func SanitizeAgentType(raw string) string {
	normalized := normalizeAgentType(raw)
	switch {
	case normalized == "":
		return ""
	case utf8.RuneCountInString(normalized) > agentTypeMaxLen:
		return ""
	case containsTraversalSegment(normalized):
		return ""
	case needsHashedAgentType(normalized):
		return fallbackAgentTypeName(normalized)
	default:
		return normalized
	}
}

func normalizeAgentType(raw string) string {
	normalized := norm.NFC.String(strings.TrimSpace(raw))
	return strings.ReplaceAll(normalized, ":", "-")
}

func containsTraversalSegment(raw string) bool {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

func needsHashedAgentType(raw string) bool {
	if raw == "" || strings.HasPrefix(raw, ".") || strings.HasSuffix(raw, ".") {
		return true
	}
	if isReservedWindowsSegment(raw) {
		return true
	}
	for _, r := range raw {
		if unicode.IsControl(r) || isBidiControl(r) {
			return true
		}
		if !isPortableAgentRune(r) {
			return true
		}
	}
	return false
}

func isPortableAgentRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case ' ', '.', '_', '-', '@':
		return true
	default:
		return false
	}
}

func isBidiControl(r rune) bool {
	switch {
	case r == 0x061C, r == 0x200E, r == 0x200F:
		return true
	case 0x202A <= r && r <= 0x202E:
		return true
	case 0x2066 <= r && r <= 0x2069:
		return true
	default:
		return false
	}
}

func isReservedWindowsSegment(raw string) bool {
	segment := raw
	if idx := strings.IndexRune(segment, '.'); idx >= 0 {
		segment = segment[:idx]
	}
	switch strings.ToUpper(strings.TrimSpace(segment)) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func fallbackAgentTypeName(raw string) string {
	prefix := readableAgentTypePrefix(raw)
	if prefix == "" {
		prefix = "agent"
	}
	const hashLen = 8
	maxPrefixRunes := agentTypeMaxLen - hashLen - 1
	if utf8.RuneCountInString(prefix) > maxPrefixRunes {
		prefix = truncateRunes(prefix, maxPrefixRunes)
		prefix = strings.Trim(prefix, " ._-")
	}
	if prefix == "" {
		prefix = "agent"
	}
	return prefix + "-" + shared.ShortHash(raw)
}

func readableAgentTypePrefix(raw string) string {
	var builder strings.Builder
	lastSeparator := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastSeparator = false
		case r == '.' || r == '_' || r == '-' || r == '@':
			if lastSeparator {
				continue
			}
			builder.WriteRune(r)
			lastSeparator = true
		default:
			if lastSeparator {
				continue
			}
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	return strings.Trim(builder.String(), " ._-")
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func cloneStrings(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	return append([]string(nil), lines...)
}

func nonEmpty(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

func canonicalName(raw string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(raw))), " ")
}

const (
	entrypointMaxLines      = 200
	entrypointMaxCodeUnits  = 25_000
	agentMemoryMaxLines     = entrypointMaxLines
	agentMemoryMaxCodeUnits = entrypointMaxCodeUnits
)

type agentMemoryTruncation struct {
	content          string
	lineCount        int
	codeUnitCount    int
	wasLineTruncated bool
	wasByteTruncated bool
}

func truncateAgentMemoryContent(raw string) agentMemoryTruncation {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return agentMemoryTruncation{}
	}
	lines := strings.Split(trimmed, "\n")
	codeUnits := parse.JSStringLength(trimmed)
	result := agentMemoryTruncation{
		content:          trimmed,
		lineCount:        len(lines),
		codeUnitCount:    codeUnits,
		wasLineTruncated: len(lines) > entrypointMaxLines,
		wasByteTruncated: codeUnits > entrypointMaxCodeUnits,
	}
	if !result.wasLineTruncated && !result.wasByteTruncated {
		return result
	}
	if result.wasLineTruncated {
		trimmed = strings.Join(lines[:entrypointMaxLines], "\n")
	}
	if parse.JSStringLength(trimmed) > entrypointMaxCodeUnits {
		trimmed = parse.TruncateAtCodeUnitLimit(trimmed, entrypointMaxCodeUnits)
	}
	result.content = trimmed + "\n\n> WARNING: MEMORY.md is " + truncateAgentMemoryReason(result) + ". Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files."
	return result
}

func truncateAgentMemoryReason(result agentMemoryTruncation) string {
	switch {
	case result.wasByteTruncated && !result.wasLineTruncated:
		return fmt.Sprintf("%s (limit: %s) — index entries are too long", formatEntrypointSize(result.codeUnitCount), formatEntrypointSize(entrypointMaxCodeUnits))
	case result.wasLineTruncated && !result.wasByteTruncated:
		return fmt.Sprintf("%d lines (limit: %d)", result.lineCount, entrypointMaxLines)
	default:
		return fmt.Sprintf("%d lines and %s", result.lineCount, formatEntrypointSize(result.codeUnitCount))
	}
}

func formatEntrypointSize(size int) string {
	kb := float64(size) / 1024
	switch {
	case kb < 1:
		return fmt.Sprintf("%d bytes", size)
	case kb < 1024:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", kb), ".0") + "KB"
	case kb/1024 < 1024:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", kb/1024), ".0") + "MB"
	default:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", kb/1024/1024), ".0") + "GB"
	}
}

type defaultPathHelper struct{}

func (defaultPathHelper) ValidateRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	cleaned, err := shared.ValidateMemoryRoot(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	return cleaned, nil
}

func (defaultPathHelper) CleanAbsolute(raw string) (string, error) {
	return shared.CleanAbsolutePath(raw)
}

func (defaultPathHelper) CanonicalGitRoot(_ context.Context, projectRoot string) (string, error) {
	return shared.CleanAbsolutePath(projectRoot)
}

func (defaultPathHelper) SanitizePath(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	if slug := strings.Trim(builder.String(), "-"); slug != "" {
		return slug
	}
	return "project"
}

func (defaultPathHelper) MemoryIndexPath(root string) string { return filepath.Join(root, "MEMORY.md") }

var cleanAbsolutePathFallback = shared.CleanAbsolutePath
