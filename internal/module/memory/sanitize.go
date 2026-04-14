package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"

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
	if raw == "." || raw == ".." {
		return true
	}
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
	return prefix + "-" + shortHash(raw)
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

