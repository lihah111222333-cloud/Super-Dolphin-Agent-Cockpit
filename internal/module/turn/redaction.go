package turn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Redactor is the second-pass redactor used by the skill extractor. Redact
// returns the redacted text, the names of the patterns that fired (for
// metric labelling), and a redactor-internal failure (NOT a "no match"
// signal).
//
// hits non-empty = at least one pattern fired; hits empty = nothing
// matched. err != nil means the redactor itself failed (e.g. a regex
// compile fault) and the caller should treat the redaction as failed.
type Redactor interface {
	Redact(input string) (output string, hits []string, err error)
}

// redactionPattern is the must-cover spec table. Each named pattern is
// replaced with [REDACTED:<name>] when matched, and <name> is appended to
// hits. When adding a pattern, mirror the change in redaction_test.go so
// coverage stays load-bearing.
type redactionPattern struct {
	name string
	re   *regexp.Regexp
}

// DefaultRedactor implements Redactor with a fixed regex pattern set. The
// patterns are evaluated in declaration order; one input may be matched by
// more than one pattern in a single Redact call.
type DefaultRedactor struct {
	patterns []redactionPattern
}

// NewDefaultRedactor compiles the fixed pattern set. A compile failure
// panics so the fault is caught at process start, not deep inside a turn.
func NewDefaultRedactor() *DefaultRedactor {
	specs := []struct {
		name, expr string
	}{
		{"bearer_token", `(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`},
		{"jwt", `eyJ[A-Za-z0-9_=\-]+\.[A-Za-z0-9_=\-]+\.?[A-Za-z0-9_.+/=\-]*`},
		// credential env name followed by '=' or ':'; value runs to the next whitespace.
		{"credential_env", `(?i)\b(OPENAI_API_KEY|ANTHROPIC_API_KEY|AWS_(?:SECRET_)?ACCESS_KEY(?:_ID)?|GITHUB_TOKEN|HF_TOKEN|HUGGINGFACE_TOKEN)\s*[=:]\s*\S+`},
		// HTTP cookie header (Cookie / Set-Cookie).
		{"http_cookie", `(?i)\b(?:Cookie|Set-Cookie)\s*:\s*[^\r\n]+`},
		// Generic long hex / base64 blobs (32+ chars). long_hex is listed
		// first because pure-hex strings are also valid base64; ordering
		// the more specific pattern first preserves accurate hit labels.
		{"long_hex", `\b[0-9a-fA-F]{32,}\b`},
		{"long_base64", `[A-Za-z0-9+/=]{32,}`},
	}
	rs := make([]redactionPattern, 0, len(specs))
	for _, s := range specs {
		re, err := regexp.Compile(s.expr)
		if err != nil {
			panic(fmt.Sprintf("redaction pattern %q compile failed: %v", s.name, err))
		}
		rs = append(rs, redactionPattern{name: s.name, re: re})
	}
	return &DefaultRedactor{patterns: rs}
}

// Redact applies every pattern in declaration order. The same text may be
// rewritten by multiple patterns. nil receiver is a documented no-op so
// callers can plug a zero value for tests / partial wiring.
func (r *DefaultRedactor) Redact(input string) (string, []string, error) {
	if r == nil || len(r.patterns) == 0 {
		return input, nil, nil
	}
	out := input
	var hits []string
	for _, p := range r.patterns {
		if !p.re.MatchString(out) {
			continue
		}
		replacement := "[REDACTED:" + p.name + "]"
		out = p.re.ReplaceAllString(out, replacement)
		hits = append(hits, p.name)
	}
	return out, hits, nil
}

// RepoFingerprint derives a short, stable scope key from cwd: filepath.Abs
// then sha256, kept to the first 12 hex chars. Empty / whitespace cwd
// returns the empty string. The Step 5 review gate refuses an empty
// fingerprint when scope == project, so leaking the empty value cannot
// silently widen the scope. The function is colocated here so Step 5 can
// share it without an import cycle.
func RepoFingerprint(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:12]
}

// Compile-time assertion that *DefaultRedactor satisfies Redactor.
var _ Redactor = (*DefaultRedactor)(nil)
