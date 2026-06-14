// Package identifier provides predicates for thread / session ID strings.
//
// Two predicates with different strictness coexist on purpose:
//
//   - LooksLikeUUID: lax shape check used by binding hygiene, resolver gates,
//     background resume filters, and startup cleanup. It accepts any
//     hex-and-dash form with at least 32 hex characters, which covers v4
//     UUIDs as well as the looser thread IDs codex emits.
//   - IsClaudeCLISessionUUID: strict v4 UUID dash form (8-4-4-4-12) that the
//     Claude CLI accepts as --resume. Used inside claudecli to keep the
//     sanitize / mark-thread-ready / restart decisions aligned.
package identifier

import (
	"regexp"
	"strings"
)

// LooksLikeUUID reports whether s resembles a UUID-like identifier (hex
// characters plus optional dashes, with at least 32 hex digits).
//
// It rejects agent-id placeholders such as "agent_17782..." which are not
// valid provider UUIDs.
// LooksLikeUUID 处理lookslikeUUID。
func LooksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 {
		return false
	}
	hex := 0
	for _, c := range s {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hex++
		case c == '-':
			// dash is allowed but not counted toward hex
		default:
			return false
		}
	}
	return hex >= 32
}

// claudeCLIUUIDRE matches the canonical v4 UUID shape the Claude CLI accepts
// for --resume. The CLI also accepts session titles, but those are arbitrary
// strings and we cannot safely distinguish them from internal thread IDs, so
// only canonical UUIDs are allowed through.
var claudeCLIUUIDRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsClaudeCLISessionUUID reports whether s is a canonical v4 UUID acceptable
// as the Claude CLI --resume argument.
// IsClaudeCLISessionUUID 判断claudeCLI会话UUID是否可用。
func IsClaudeCLISessionUUID(s string) bool {
	return claudeCLIUUIDRE.MatchString(strings.TrimSpace(s))
}
