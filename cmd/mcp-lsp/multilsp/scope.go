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

// LSPToolScope 是工具层注入的可信服务端 scope。
// agent/thread 身份来自 provider/toolbridge 上下文，不接受用户 JSON 伪造；规范 key 只在解析阶段生成。
type LSPToolScope struct {
	AgentID  string // provider/toolbridge 注入的 agent 身份。
	ThreadID string // provider/toolbridge 注入的 thread 身份。
	TurnID   string // 单次 turn 身份，仅用于追踪，不进入 manager key。
	CallID   string // 单次工具调用身份，仅用于追踪，不进入 manager key。
	CWD      string // 工具调用的可信当前目录。
	// WorkspaceRoots 是 containment 和 root 选择的可信根集合。
	// 它不直接进入 manager key，最终缓存身份由解析后的 WorkspaceRoot/ProjectRoot 决定。
	WorkspaceRoots []string

	Family     string // 工具族，默认 lsp。
	LanguageID string // 调用方或适配器确认的语言 ID。

	TargetPath string // 原始或解析后的目标文件路径。
	TargetURI  string // 目标文件 URI。

	WorkspaceRoot         string            // 选中的 workspace root。
	RootKind              string            // root 来源类型。
	LanguageWorkspaceRoot string            // 语言适配器调整后的 root。
	ProjectRoot           string            // 语言项目根。
	LanguageSpecific      map[string]string // 语言适配器附加 metadata。
}

// ResolvedLSPToolScope 是 ManagerPool 路由后的规范 scope。
// 诊断、缓存和 bootstrap 调用方必须复用这些 key，避免各处独立拼接造成隔离漂移。
type ResolvedLSPToolScope struct {
	LSPToolScope

	ScopeKey     string // agent/thread 粒度的隔离键。
	WorkspaceKey string // workspace/language 粒度的复用键。
	ShardKey     string // ManagerPool 分片选择键。
	ManagerKey   string // scoped manager 的最终缓存键。
}

// ScopedManager 把选中的 manager 和解析后的 scope 一起返回，调用方必须复用该 scope 写诊断缓存。
type ScopedManager struct {
	Manager       Manager              // 可执行 LSP 请求的 manager。
	ResolvedScope ResolvedLSPToolScope // 与 manager 一致的规范 scope。
}

// ResolveLSPToolScope 规范化可信 scope 并生成 ManagerPool 路由 key。
// turn/call 身份只用于追踪，不进入 ScopeKey/ManagerKey，同一 agent/thread/workspace 可复用 manager。
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

// canonicalizeLSPToolScope 清理 scope 字段并补齐目标路径/URI。
// 不支持的工具族会立即报错，避免非 LSP 请求误入 LSP manager 池。
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
	canonical.WorkspaceRoots = normalizeScopeWorkspaceRoots(canonical.CWD, scope.WorkspaceRoots)
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

// canonicalScopePath 将 scope 中的路径或 file URI 转为稳定绝对路径。
// 相对路径只允许基于已验证 base 展开；解析失败时保留清理后的路径供后续 containment 检查。
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

// normalizeScopeWorkspaceRoots 规范化并去重 workspace roots。
// CWD 会作为第一候选根参与 containment，空或非绝对路径会被丢弃。
func normalizeScopeWorkspaceRoots(cwd string, roots []string) []string {
	out := make([]string, 0, len(roots)+1)
	seen := map[string]struct{}{}
	add := func(root string) {
		normalized := canonicalScopePath(root, "")
		if normalized == "" || !filepath.IsAbs(normalized) {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	add(cwd)
	for _, root := range roots {
		add(root)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// selectWorkspaceRootForTarget 为目标文件选择最窄匹配 workspace root。
// 目标不在可信 roots 内时返回错误，阻断跨工作区访问。
func selectWorkspaceRootForTarget(roots []string, target string) (string, error) {
	targetPath, err := absoluteWorkspaceTargetPath(target)
	if err != nil {
		return "", err
	}
	if targetPath == "" {
		return "", nil
	}
	best := ""
	for _, root := range roots {
		if platformshared.ContainsPath(root, targetPath) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return "", fmt.Errorf("path %q is outside workspace roots [%s]", targetPath, strings.Join(roots, ", "))
	}
	return best, nil
}

func ensurePathWithinWorkspaceRoots(roots []string, path string) error {
	targetPath, err := absoluteWorkspaceTargetPath(path)
	if err != nil {
		return err
	}
	if targetPath == "" {
		return nil
	}
	roots = normalizeScopeWorkspaceRoots("", roots)
	for _, root := range roots {
		if platformshared.ContainsPath(root, targetPath) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside workspace roots [%s]", targetPath, strings.Join(roots, ", "))
}

func ensureResolvedLanguageScopeWithinWorkspaceRoots(roots []string, resolved ResolvedLanguageScope) error {
	for _, path := range []string{resolved.WorkspaceRoot, resolved.LanguageWorkspaceRoot, resolved.ProjectRoot} {
		if err := ensurePathWithinWorkspaceRoots(roots, path); err != nil {
			return err
		}
	}
	for _, folder := range resolved.WorkspaceFolders {
		if err := ensurePathWithinWorkspaceRoots(roots, folder.URI); err != nil {
			return err
		}
	}
	return nil
}

func ensureLSPToolScopeWithinWorkspaceRoots(roots []string, scope LSPToolScope) error {
	return ensureResolvedLanguageScopeWithinWorkspaceRoots(roots, ResolvedLanguageScope{
		LanguageID:            scope.LanguageID,
		WorkspaceRoot:         scope.WorkspaceRoot,
		LanguageWorkspaceRoot: scope.LanguageWorkspaceRoot,
		ProjectRoot:           scope.ProjectRoot,
		RootKind:              scope.RootKind,
		LanguageSpecific:      scope.LanguageSpecific,
	})
}

func absoluteWorkspaceTargetPath(target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "file://") {
		path, err := absolutePathFromURI(trimmed)
		if err != nil {
			return "", err
		}
		return canonicalAbsoluteTargetPath(path), nil
	}
	if !filepath.IsAbs(trimmed) {
		return "", nil
	}
	return canonicalAbsoluteTargetPath(trimmed), nil
}

// canonicalAbsoluteTargetPath 规范化绝对目标路径。
// 文件不存在时仍尝试解析父目录符号链接，保证新文件路径也能接受 containment 校验。
func canonicalAbsoluteTargetPath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(cleaned)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(filepath.Clean(resolvedParent), filepath.Base(cleaned))
	}
	if normalized, err := platformshared.NormalizeAbsolutePath(cleaned); err == nil && normalized != "" {
		return normalized
	}
	return cleaned
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
