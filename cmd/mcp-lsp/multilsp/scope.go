package multilsp

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultLSPToolFamily = "lsp"
	scopeKeySeparator    = "\x00"
)

// LSPToolScope is the trusted, server-side scope supplied by the tool layer.
// Agent/thread identity comes from provider/toolbridge context, not from
// user-controlled request JSON. ResolvedLSPToolScope below is the only place
// canonical keys are attached.
type LSPToolScope struct {
	AgentID  string
	ThreadID string
	TurnID   string
	CallID   string
	CWD      string

	Family     string
	LanguageID string

	TargetPath string
	TargetURI  string

	WorkspaceRoot         string
	RootKind              string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	LanguageSpecific      map[string]string
}

// ResolvedLSPToolScope is the canonical ManagerPool routing result. Callers
// that need diagnostics/cache/bootstrap keys must reuse this value instead of
// reassembling keys independently.
type ResolvedLSPToolScope struct {
	LSPToolScope

	ScopeKey     string
	WorkspaceKey string
	ShardKey     string
	ManagerKey   string
}

type ScopedManager struct {
	Manager       Manager
	ResolvedScope ResolvedLSPToolScope
}

// ResolveLSPToolScope canonicalizes a trusted scope and derives all ManagerPool
// keys. It deliberately excludes turn/call identity from ScopeKey/ManagerKey so
// repeated tool calls in the same agent/thread/workspace reuse a manager.
func ResolveLSPToolScope(scope LSPToolScope) (ResolvedLSPToolScope, error) {
	canonical, err := canonicalizeLSPToolScope(scope)
	if err != nil {
		return ResolvedLSPToolScope{}, err
	}
	workspaceKey, err := buildWorkspaceKey(canonical)
	if err != nil {
		return ResolvedLSPToolScope{}, err
	}
	scopeKey := buildScopeKey(canonical)
	managerKey := workspaceKey
	if scopeKey != "" {
		managerKey = strings.Join([]string{scopeKey, workspaceKey}, scopeKeySeparator)
	}
	shardKey := workspaceKey
	if scopeKey != "" {
		shardKey = scopeKey
	}
	return ResolvedLSPToolScope{
		LSPToolScope: canonical,
		ScopeKey:     scopeKey,
		WorkspaceKey: workspaceKey,
		ShardKey:     shardKey,
		ManagerKey:   managerKey,
	}, nil
}

func canonicalizeLSPToolScope(scope LSPToolScope) (LSPToolScope, error) {
	canonical := LSPToolScope{
		AgentID:          strings.TrimSpace(scope.AgentID),
		ThreadID:         strings.TrimSpace(scope.ThreadID),
		TurnID:           strings.TrimSpace(scope.TurnID),
		CallID:           strings.TrimSpace(scope.CallID),
		Family:           normalizeScopeFamily(scope.Family),
		LanguageID:       normalizeLanguageID(scope.LanguageID),
		RootKind:         normalizeRootKind(scope.RootKind),
		LanguageSpecific: copyLanguageSpecific(scope.LanguageSpecific),
	}
	if canonical.Family != defaultLSPToolFamily {
		return LSPToolScope{}, fmt.Errorf("unsupported LSP scope family %q", canonical.Family)
	}

	canonical.CWD = canonicalScopePath(scope.CWD, "")
	canonical.WorkspaceRoot = canonicalScopePath(scope.WorkspaceRoot, canonical.CWD)
	canonical.LanguageWorkspaceRoot = canonicalScopePath(scope.LanguageWorkspaceRoot, canonical.WorkspaceRoot)
	canonical.ProjectRoot = canonicalScopePath(scope.ProjectRoot, canonical.WorkspaceRoot)
	if canonical.WorkspaceRoot == "" {
		canonical.WorkspaceRoot = firstNonEmpty(canonical.LanguageWorkspaceRoot, canonical.ProjectRoot, canonical.CWD)
	}
	if canonical.LanguageWorkspaceRoot == "" {
		canonical.LanguageWorkspaceRoot = canonical.WorkspaceRoot
	}
	if canonical.ProjectRoot == "" {
		canonical.ProjectRoot = canonical.WorkspaceRoot
	}

	canonical.TargetPath = canonicalScopePath(scope.TargetPath, canonical.WorkspaceRoot)
	canonical.TargetURI = canonicalScopeURI(scope.TargetURI)
	if canonical.TargetURI == "" && canonical.TargetPath != "" {
		canonical.TargetURI = fileURIFromPath(canonical.TargetPath)
	}
	if canonical.TargetPath == "" && strings.TrimSpace(scope.TargetURI) != "" {
		if path, err := absolutePathFromURI(scope.TargetURI); err == nil {
			canonical.TargetPath = path
			canonical.TargetURI = fileURIFromPath(path)
		}
	}
	return canonical, nil
}

func buildScopeKey(scope LSPToolScope) string {
	if strings.TrimSpace(scope.AgentID) == "" && strings.TrimSpace(scope.ThreadID) == "" {
		return ""
	}
	return strings.Join([]string{
		scope.Family,
		scope.AgentID,
		scope.ThreadID,
	}, scopeKeySeparator)
}

func buildWorkspaceKey(scope LSPToolScope) (string, error) {
	if scope.LanguageID == "" {
		return "", fmt.Errorf("LSP scope language is empty")
	}
	if firstNonEmpty(scope.WorkspaceRoot, scope.LanguageWorkspaceRoot, scope.ProjectRoot) == "" {
		return "", fmt.Errorf("LSP scope workspace root is empty")
	}
	return strings.Join([]string{
		scope.LanguageID,
		scope.RootKind,
		scope.WorkspaceRoot,
		scope.LanguageWorkspaceRoot,
		scope.ProjectRoot,
		encodeLanguageSpecific(scope.LanguageSpecific),
	}, scopeKeySeparator), nil
}

func managerWorkspaceRoot(scope ResolvedLSPToolScope) string {
	return firstNonEmpty(scope.WorkspaceRoot, scope.LanguageWorkspaceRoot, scope.ProjectRoot, scope.CWD)
}

func normalizeScopeFamily(family string) string {
	trimmed := strings.ToLower(strings.TrimSpace(family))
	if trimmed == "" {
		return defaultLSPToolFamily
	}
	return trimmed
}

func normalizeRootKind(rootKind string) string {
	trimmed := strings.ToLower(strings.TrimSpace(rootKind))
	if trimmed == "" {
		return "dir_fallback"
	}
	return trimmed
}

func canonicalScopeURI(uri string) string {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return ""
	}
	path, err := absolutePathFromURI(trimmed)
	if err != nil {
		return trimmed
	}
	return fileURIFromPath(path)
}

func canonicalScopePath(value, base string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "file://") {
		if path, err := absolutePathFromURI(trimmed); err == nil {
			return path
		}
	}
	if base != "" && !filepath.IsAbs(trimmed) {
		trimmed = filepath.Join(base, trimmed)
	}
	if normalized, err := platformshared.NormalizeAbsolutePath(trimmed); err == nil && normalized != "" {
		return normalized
	}
	return filepath.Clean(trimmed)
}

func copyLanguageSpecific(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		output[trimmedKey] = strings.TrimSpace(value)
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func encodeLanguageSpecific(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, scopeKeySeparator)
}

func shardIndexForKey(key string, size int) int {
	if size <= 1 {
		return 0
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return int(hash.Sum32() % uint32(size))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
