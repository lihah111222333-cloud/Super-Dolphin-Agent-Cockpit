package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

const (
	envAdditionalDirectoriesClaudeMd = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"
	envManagedClaudeMdRoot           = "CLAUDE_CODE_MANAGED_POLICY_DIR"
	claudeMdFileName                 = "CLAUDE.md"
	claudeLocalMdFileName            = "CLAUDE.local.md"
)

const (
	sourceOriginManaged = "managed"
	sourceOriginUser    = "user"
	sourceOriginProject = "project"
	sourceOriginAddDir  = "add_dir"
	sourceOriginAutoMem = "automem"
	sourceOriginTeamMem = "teammem"
)

const (
	sourceTypeManaged = "managed"
	sourceTypeUser    = "user"
	sourceTypeProject = "project"
	sourceTypeLocal   = "local"
	sourceTypeAutoMem = "automem"
	sourceTypeTeamMem = "teammem"
)

type ClaudeMdSource = contract.ClaudeMdSource

type ClaudeMdResolveConfig struct {
	BuildCtx     contract.BuildCtx
	MemoryConfig *Config
	TeamMemPath  string
	ManagedRoots []string
	UserRoot     string
}

type ClaudeMdSourcesProvider struct {
	cfg    *Config
	team   *TeamMemoryManager
	nested *NestedRuntime

	mu    sync.RWMutex
	cache map[string][]ClaudeMdSource
}

type claudeMdCandidate struct {
	Path      string
	Type      string
	Origin    string
	BaseDir   string
	RuleScope string
	IsRule    bool
	Digest    string
}

func NewClaudeMdSourcesProvider(cfg *Config, team *TeamMemoryManager, nested *NestedRuntime) *ClaudeMdSourcesProvider {
	cfg = memoryConfig(cfg)
	if nested == nil {
		nested = NewNestedRuntime(cfg, team)
	}
	return &ClaudeMdSourcesProvider{
		cfg:    cfg,
		team:   team,
		nested: nested,
		cache:  map[string][]ClaudeMdSource{},
	}
}

func (p *ClaudeMdSourcesProvider) ResolveClaudeMdSources(ctx context.Context, buildCtx contract.BuildCtx) []contract.ClaudeMdSource {
	gate := ResolveMemoryGate(buildCtx, p.cfg)
	if gate.DisableClaudeMds {
		return nil
	}
	resolveCfg := ClaudeMdResolveConfig{
		BuildCtx:     buildCtx,
		MemoryConfig: p.cfg,
		TeamMemPath:  providerTeamMemPath(p.team, buildCtx),
		ManagedRoots: defaultManagedClaudeMdRoots(),
		UserRoot:     defaultUserClaudeMdRoot(),
	}
	manifestDigest := digestClaudeMdCandidates(resolveClaudeMdCandidates(resolveCfg))
	cacheKey := claudeMdSourceCacheKey(buildCtx, gate, manifestDigest)
	if sources, ok := p.lookup(cacheKey); ok {
		return sources
	}
	sources := ResolveClaudeMdSources(ctx, resolveCfg)
	sources = FilterInjectedMemoryFiles(sources, gate, buildCtx.ClaudeMdExcludes)
	p.store(cacheKey, sources)
	return cloneClaudeMdSources(sources)
}

func (p *ClaudeMdSourcesProvider) OnPromptInvalidate(reason prompt.InvalidateReason) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.cache = map[string][]ClaudeMdSource{}
	p.mu.Unlock()
	if p.nested != nil {
		p.nested.OnPromptInvalidate(reason)
	}
}

func ResolveClaudeMdSources(ctx context.Context, cfg ClaudeMdResolveConfig) []ClaudeMdSource {
	candidates := resolveClaudeMdCandidates(cfg)
	return loadClaudeMdSources(ctx, candidates)
}

func FilterInjectedMemoryFiles(sources []ClaudeMdSource, gate MemoryGateSnapshot, excludes []string) []ClaudeMdSource {
	patterns := normalizeClaudeMdExcludePatterns(excludes)
	filtered := make([]ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		if shouldSkipInjectedSource(source, gate) {
			continue
		}
		if shouldExcludeClaudeMdSource(source, patterns) {
			continue
		}
		if gate.SkipProjectLocalClaudeMd && isProjectOrLocalClaudeMdSource(source) {
			continue
		}
		filtered = append(filtered, cloneClaudeMdSource(source))
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func (p *ClaudeMdSourcesProvider) lookup(key string) ([]ClaudeMdSource, bool) {
	if p == nil || strings.TrimSpace(key) == "" {
		return nil, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	sources, ok := p.cache[key]
	if !ok {
		return nil, false
	}
	return cloneClaudeMdSources(sources), true
}

func (p *ClaudeMdSourcesProvider) store(key string, sources []ClaudeMdSource) {
	if p == nil || strings.TrimSpace(key) == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = cloneClaudeMdSources(sources)
}

func claudeMdSourceCacheKey(buildCtx contract.BuildCtx, gate MemoryGateSnapshot, manifestDigest string) string {
	payload := []string{
		strings.TrimSpace(manifestDigest),
		boolToken(gate.InjectMemoryIndex),
		boolToken(gate.InjectTeamMemIndex),
		boolToken(gate.SkipProjectLocalClaudeMd),
		strings.Join(normalizeStringSlice(buildCtx.ClaudeMdExcludes), "|"),
		strings.Join(normalizeStringSlice(buildCtx.AdditionalWorkingDirectories), "|"),
	}
	digest := sha256.Sum256([]byte(strings.Join(payload, "\n")))
	return hex.EncodeToString(digest[:])
}

func resolveClaudeMdCandidates(cfg ClaudeMdResolveConfig) []claudeMdCandidate {
	seen := make(map[string]struct{}, 16)
	candidates := make([]claudeMdCandidate, 0, 16)
	appendManagedClaudeMdCandidates(&candidates, seen, cfg)
	appendUserClaudeMdCandidates(&candidates, seen, cfg)
	appendProjectClaudeMdCandidates(&candidates, seen, cfg)
	appendAdditionalDirClaudeMdCandidates(&candidates, seen, cfg)
	appendMemoryEntrypointCandidate(&candidates, seen, sourceTypeAutoMem, sourceOriginAutoMem, autoMemRoot(cfg))
	appendMemoryEntrypointCandidate(&candidates, seen, sourceTypeTeamMem, sourceOriginTeamMem, strings.TrimSpace(cfg.TeamMemPath))
	return candidates
}

func appendManagedClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) {
	for _, root := range managedClaudeMdRoots(cfg) {
		appendProjectStyleCandidates(candidates, seen, root, sourceTypeManaged, sourceOriginManaged, false)
	}
}

func appendUserClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) {
	root := strings.TrimSpace(cfg.UserRoot)
	if root == "" {
		root = defaultUserClaudeMdRoot()
	}
	appendProjectStyleCandidates(candidates, seen, root, sourceTypeUser, sourceOriginUser, false)
}

func appendProjectClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) {
	for _, dir := range ancestorWalkDirs(cfg.BuildCtx.GitRoot, cfg.BuildCtx.CWD) {
		appendProjectStyleCandidates(candidates, seen, dir, sourceTypeProject, sourceOriginProject, true)
	}
}

func appendAdditionalDirClaudeMdCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, cfg ClaudeMdResolveConfig) {
	if !parseBoolEnv(envAdditionalDirectoriesClaudeMd, false) {
		return
	}
	for _, dir := range normalizeStringSlice(cfg.BuildCtx.AdditionalWorkingDirectories) {
		appendProjectStyleCandidates(candidates, seen, dir, sourceTypeProject, sourceOriginAddDir, true)
	}
}

func appendProjectStyleCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, dir, sourceType, origin string, includeLocal bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	baseDir := cleanClaudeMdPath(dir)
	appendClaudeMdCandidateAtPath(candidates, seen, filepath.Join(baseDir, claudeMdFileName), sourceType, origin, baseDir, "", false)
	appendClaudeMdCandidateAtPath(candidates, seen, filepath.Join(baseDir, ".claude", claudeMdFileName), sourceType, origin, baseDir, "", false)
	appendRuleCandidates(candidates, seen, filepath.Join(baseDir, ".claude", "rules"), sourceType, origin, baseDir)
	if includeLocal {
		appendClaudeMdCandidateAtPath(candidates, seen, filepath.Join(baseDir, claudeLocalMdFileName), sourceTypeLocal, origin, baseDir, "", false)
	}
}

func appendRuleCandidates(candidates *[]claudeMdCandidate, seen map[string]struct{}, root, sourceType, origin, baseDir string) {
	for _, path := range ruleMarkdownFiles(root) {
		appendClaudeMdCandidateAtPath(candidates, seen, path, sourceType, origin, baseDir, origin, true)
	}
}

func appendMemoryEntrypointCandidate(candidates *[]claudeMdCandidate, seen map[string]struct{}, sourceType, origin, root string) {
	root = strings.TrimSpace(root)
	if root == "" {
		return
	}
	appendClaudeMdCandidateAtPath(candidates, seen, memoryIndexPath(root), sourceType, origin, cleanClaudeMdPath(root), "", false)
}

func appendClaudeMdCandidateAtPath(candidates *[]claudeMdCandidate, seen map[string]struct{}, path, sourceType, origin, baseDir, ruleScope string, isRule bool) {
	appendClaudeMdCandidate(candidates, seen, claudeMdCandidate{Path: path, Type: sourceType, Origin: origin, BaseDir: baseDir, RuleScope: ruleScope, IsRule: isRule})
}

func appendClaudeMdCandidate(candidates *[]claudeMdCandidate, seen map[string]struct{}, candidate claudeMdCandidate) {
	resolvedPath, digest, ok := resolveClaudeMdCandidatePath(candidate.Path)
	if !ok {
		return
	}
	if _, exists := seen[resolvedPath]; exists {
		return
	}
	seen[resolvedPath] = struct{}{}
	candidate.Path = resolvedPath
	candidate.Digest = digest
	*candidates = append(*candidates, candidate)
}

func loadClaudeMdSources(ctx context.Context, candidates []claudeMdCandidate) []ClaudeMdSource {
	sources := make([]ClaudeMdSource, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			break
		}
		source, ok := loadClaudeMdSource(candidate)
		if !ok {
			continue
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}

func loadClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool) {
	switch candidate.Type {
	case sourceTypeAutoMem, sourceTypeTeamMem:
		return loadMemoryClaudeMdSource(candidate)
	default:
		return loadStandardClaudeMdSource(candidate)
	}
}

func loadStandardClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool) {
	raw, err := os.ReadFile(candidate.Path)
	if err != nil {
		return ClaudeMdSource{}, false
	}
	content := strings.TrimSpace(StripHTMLComments(stripUTF8BOM(string(raw))))
	metadata := claudeRuleMetadata{}
	if candidate.IsRule {
		metadata, content = parseClaudeRuleContent(content)
	}
	if strings.TrimSpace(content) == "" {
		return ClaudeMdSource{}, false
	}
	return ClaudeMdSource{
		Path:        candidate.Path,
		Content:     content,
		Type:        candidate.Type,
		Description: metadata.Description,
		Origin:      candidate.Origin,
		Conditional: len(metadata.Globs) > 0,
		Globs:       metadata.Globs,
		BaseDir:     candidate.BaseDir,
		RuleScope:   candidate.RuleScope,
		Digest:      candidate.Digest,
	}, true
}

func loadMemoryClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool) {
	parsed, err := ParseMemoryFile(candidate.Path)
	if err != nil || parsed == nil {
		return ClaudeMdSource{}, false
	}
	content := strings.TrimSpace(parsed.Content)
	if content == "" {
		return ClaudeMdSource{}, false
	}
	return ClaudeMdSource{
		Path:      candidate.Path,
		Content:   content,
		Type:      candidate.Type,
		Origin:    candidate.Origin,
		BaseDir:   candidate.BaseDir,
		RuleScope: candidate.RuleScope,
		Digest:    candidate.Digest,
	}, true
}

func shouldSkipInjectedSource(source ClaudeMdSource, gate MemoryGateSnapshot) bool {
	switch source.Type {
	case sourceTypeAutoMem:
		return !gate.InjectMemoryIndex
	case sourceTypeTeamMem:
		return !gate.InjectMemoryIndex || !gate.InjectTeamMemIndex
	default:
		return false
	}
}

func shouldExcludeClaudeMdSource(source ClaudeMdSource, patterns []string) bool {
	if len(patterns) == 0 || !isExcludeEligibleClaudeMdSource(source) {
		return false
	}
	target := filepath.ToSlash(cleanClaudeMdPath(source.Path))
	for _, pattern := range patterns {
		if matchClaudeMdExclude(pattern, target) {
			return true
		}
	}
	return false
}

func isExcludeEligibleClaudeMdSource(source ClaudeMdSource) bool {
	if source.Origin == sourceOriginAddDir {
		return false
	}
	switch source.Type {
	case sourceTypeUser, sourceTypeProject, sourceTypeLocal:
		return true
	default:
		return false
	}
}

func isProjectOrLocalClaudeMdSource(source ClaudeMdSource) bool {
	if source.Origin == sourceOriginAddDir {
		return false
	}
	switch source.Type {
	case sourceTypeProject, sourceTypeLocal:
		return true
	default:
		return false
	}
}

func normalizeClaudeMdExcludePatterns(excludes []string) []string {
	patterns := make([]string, 0, len(excludes))
	for _, exclude := range excludes {
		exclude = filepath.ToSlash(cleanClaudeMdPath(exclude))
		if exclude != "" {
			patterns = append(patterns, exclude)
		}
	}
	return normalizeStringSlice(patterns)
}

func matchClaudeMdExclude(pattern, target string) bool {
	regex := globPatternToRegexp(pattern)
	matched, err := regexp.MatchString(regex, target)
	return err == nil && matched
}

func globPatternToRegexp(pattern string) string {
	var builder strings.Builder
	builder.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				builder.WriteString(".*")
				i++
				continue
			}
			builder.WriteString("[^/]*")
		case '?':
			builder.WriteString(".")
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	builder.WriteString("$")
	return builder.String()
}

func parseClaudeRuleContent(content string) (claudeRuleMetadata, string) {
	frontmatter, body, ok := splitMemoryFrontmatter(content)
	if !ok {
		return claudeRuleMetadata{}, strings.TrimSpace(content)
	}
	metadata := parseClaudeRuleMetadata(frontmatter)
	return metadata, strings.TrimSpace(StripHTMLComments(body))
}

type claudeRuleMetadata struct {
	Description string
	Globs       []string
}

func parseClaudeRuleMetadata(frontmatter string) claudeRuleMetadata {
	metadata := claudeRuleMetadata{Globs: parseFrontmatterPaths(frontmatter)}
	for line := range strings.SplitSeq(strings.ReplaceAll(frontmatter, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "description", "name":
			if metadata.Description == "" {
				metadata.Description = parseScalar(value)
			}
		}
	}
	return metadata
}

func digestClaudeMdCandidates(candidates []claudeMdCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	hasher := sha256.New()
	for _, candidate := range candidates {
		hasher.Write([]byte(strings.TrimSpace(candidate.Path)))
		hasher.Write([]byte("\n" + candidate.Type + "\n" + candidate.Origin + "\n" + candidate.Digest + "\n"))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func resolveClaudeMdCandidatePath(path string) (string, string, bool) {
	path = cleanClaudeMdPath(path)
	if path == "" {
		return "", "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", "", false
	}
	resolved := path
	if symlinked, err := filepath.EvalSymlinks(path); err == nil {
		resolved = cleanClaudeMdPath(symlinked)
	}
	digestInput := resolved + "\n" + info.ModTime().UTC().Format(timeLayoutRFC3339Nano) + "\n" + int64String(info.Size())
	digest := sha256.Sum256([]byte(digestInput))
	return resolved, hex.EncodeToString(digest[:]), true
}

func ancestorWalkDirs(root, cwd string) []string {
	cwd = cleanClaudeMdPath(cwd)
	if cwd == "" {
		return nil
	}
	root = cleanClaudeMdPath(root)
	if root == "" || !isAncestorOrSame(root, cwd) {
		return []string{cwd}
	}
	stack := make([]string, 0, 8)
	for dir := cwd; dir != ""; dir = filepath.Dir(dir) {
		stack = append(stack, dir)
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	return stack
}

func ruleMarkdownFiles(root string) []string {
	root = cleanClaudeMdPath(root)
	if root == "" {
		return nil
	}
	files := make([]string, 0, 8)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			files = append(files, cleanClaudeMdPath(path))
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func managedClaudeMdRoots(cfg ClaudeMdResolveConfig) []string {
	roots := normalizeStringSlice(cfg.ManagedRoots)
	if len(roots) > 0 {
		return roots
	}
	return defaultManagedClaudeMdRoots()
}

func defaultManagedClaudeMdRoots() []string {
	if override := strings.TrimSpace(os.Getenv(envManagedClaudeMdRoot)); override != "" {
		return []string{cleanClaudeMdPath(override)}
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{cleanClaudeMdPath("/Library/Application Support/ClaudeCode")}
	case "windows":
		base := strings.TrimSpace(os.Getenv("ProgramFiles"))
		if base == "" {
			base = `C:\Program Files`
		}
		return []string{cleanClaudeMdPath(filepath.Join(base, "ClaudeCode"))}
	default:
		return []string{cleanClaudeMdPath("/etc/claude-code")}
	}
}

func defaultUserClaudeMdRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return cleanClaudeMdPath(filepath.Join(home, ".claude"))
}

func autoMemRoot(cfg ClaudeMdResolveConfig) string {
	memoryCfg := memoryConfig(cfg.MemoryConfig)
	projectRoot := strings.TrimSpace(cfg.BuildCtx.GitRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.BuildCtx.CWD)
	}
	root, err := resolvedStoreRoot(memoryCfg.RootDir, projectRoot, memoryCfg.AutoMemPathOverride)
	if err != nil {
		return ""
	}
	return cleanClaudeMdPath(root)
}

func providerTeamMemPath(team *TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if team == nil {
		return ""
	}
	return cleanClaudeMdPath(team.GetTeamMemPath(buildCtx))
}

func cleanClaudeMdPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func isAncestorOrSame(root, child string) bool {
	root = cleanClaudeMdPath(root)
	child = cleanClaudeMdPath(child)
	if root == "" || child == "" {
		return false
	}
	if root == child {
		return true
	}
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cloneClaudeMdSources(sources []ClaudeMdSource) []ClaudeMdSource {
	if len(sources) == 0 {
		return nil
	}
	cloned := make([]ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		cloned = append(cloned, cloneClaudeMdSource(source))
	}
	return cloned
}

func cloneClaudeMdSource(source ClaudeMdSource) ClaudeMdSource {
	source.Globs = append([]string(nil), source.Globs...)
	return source
}

func boolToken(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func int64String(value int64) string {
	return strings.TrimSpace(strconv.FormatInt(value, 10))
}

const timeLayoutRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
