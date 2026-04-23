package codexapp

import (
	"sort"
	"strings"
)

// codexSpawnEnvAllowlist is the set of parent-process environment
// variables that are propagated into a spawned codex app-server child.
// Everything else (including rogue CODEX_* / OPENAI_* pollutants left
// over from another instance) is dropped so a per-instance CODEX_HOME
// — injected explicitly by the ServerPool — is the sole authority on
// which identity the child operates as.
//
// The list is deliberately minimal: we pull enough for shell script
// correctness (PATH / HOME / USER / locale / TZ / TMPDIR) and TLS
// cert discovery (SSL_CERT_FILE / SSL_CERT_DIR) and nothing else.
var codexSpawnEnvAllowlist = []string{
	"PATH",
	"HOME",
	"USER",
	"LOGNAME",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LC_MESSAGES",
	"TZ",
	"TMPDIR",
	"TEMP",
	"TMP",
	"SHELL",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
}

// buildAllowlistedSpawnEnv builds a cmd.Env slice containing only the
// parent environment variables named in codexSpawnEnvAllowlist plus
// every explicit override. Overrides win on key collision so callers
// can force CODEX_HOME to the canonicalized realpath without worrying
// about an inherited value. Output is deterministically sorted so
// tests can assert exact content.
func buildAllowlistedSpawnEnv(parent []string, overrides map[string]string) []string {
	allowed := make(map[string]struct{}, len(codexSpawnEnvAllowlist))
	for _, key := range codexSpawnEnvAllowlist {
		allowed[key] = struct{}{}
	}
	merged := make(map[string]string, len(allowed)+len(overrides))
	for _, kv := range parent {
		key, val, ok := splitEnv(kv)
		if !ok {
			continue
		}
		if _, permitted := allowed[key]; !permitted {
			continue
		}
		merged[key] = val
	}
	// Overrides land last so they win even when the key was not in the
	// allowlist (e.g. CODEX_HOME is injected explicitly below).
	for key, val := range overrides {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		merged[key] = val
	}
	out := make([]string, 0, len(merged))
	for key, val := range merged {
		out = append(out, key+"="+val)
	}
	sort.Strings(out)
	return out
}

// splitEnv splits a single KEY=VALUE line. The POSIX env format has no
// formal escape; we only trim spaces from the key to catch common
// misconfigurations. Missing "=" yields ok=false.
func splitEnv(kv string) (string, string, bool) {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(kv[:idx]), kv[idx+1:], true
}
