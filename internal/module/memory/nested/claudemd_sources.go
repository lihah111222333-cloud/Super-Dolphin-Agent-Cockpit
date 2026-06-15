package nested

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
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

type GateSnapshot struct {
	BareMode                 bool
	HasAdditionalDirsForBare bool
	DisableClaudeMds         bool
	SkipProjectLocalClaudeMd bool
	InjectMemoryIndex        bool
	InjectTeamMemIndex       bool
	// SuppressForOverlay mirrors MemoryGateSnapshot.SuppressForOverlay() into
	// the nested-package gate so claudeMd source loading lets the underlying
	// CLI harness's native CLAUDE.md handling take over (claude_code overlay).
	// Today the rendered claudeMd output is dropped before reaching providers
	// (start_session_helpers.go drops UserContext / UserContextText), so this
	// short-circuit is defense-in-depth: if a provider ever re-consumes
	// UserContextText, claude_code overlay must not double-inject CLAUDE.md.
	SuppressForOverlay bool
}

type Dependencies struct {
	NestedEnabled bool
	Gate          func(contract.BuildCtx) GateSnapshot
	AutoMemRoot   func(contract.BuildCtx) string
	TeamRoot      func(contract.BuildCtx) string
}

func (d Dependencies) resolveGate(buildCtx contract.BuildCtx) GateSnapshot {
	if d.Gate == nil {
		return GateSnapshot{}
	}
	return d.Gate(buildCtx)
}

func (d Dependencies) autoMemRoot(buildCtx contract.BuildCtx) string {
	if d.AutoMemRoot == nil {
		return ""
	}
	return cleanClaudeMdPath(d.AutoMemRoot(buildCtx))
}

func (d Dependencies) teamRoot(buildCtx contract.BuildCtx) string {
	if d.TeamRoot == nil {
		return ""
	}
	return cleanClaudeMdPath(d.TeamRoot(buildCtx))
}

type ClaudeMdResolveConfig struct {
	BuildCtx          contract.BuildCtx
	Dependencies      Dependencies
	TeamMemPath       string
	TeamMemEntrypoint string
	ManagedRoots      []string
	UserRoot          string
}

type ClaudeMdSourcesProvider struct {
	deps   Dependencies
	team   contract.TeamMemoryManager
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

// NewClaudeMdSourcesProvider 创建claudemdsourcesprovider。
func NewClaudeMdSourcesProvider(deps Dependencies, team contract.TeamMemoryManager, nested *NestedRuntime) *ClaudeMdSourcesProvider {
	if nested == nil {
		nested = NewNestedRuntime(deps)
	}
	return &ClaudeMdSourcesProvider{
		deps:   deps,
		team:   team,
		nested: nested,
		cache:  map[string][]ClaudeMdSource{},
	}
}

// ResolveClaudeMdSources 解析claudemdsources。
func (p *ClaudeMdSourcesProvider) ResolveClaudeMdSources(ctx context.Context, buildCtx contract.BuildCtx) ([]contract.ClaudeMdSource, error) {
	gate := p.deps.resolveGate(buildCtx)
	if shouldDisableClaudeMdSources(gate) {
		return nil, nil
	}
	resolveCfg := ClaudeMdResolveConfig{
		BuildCtx:          buildCtx,
		Dependencies:      p.deps,
		TeamMemPath:       resolveTeamMemPath(p.team, buildCtx),
		TeamMemEntrypoint: resolveTeamMemEntrypoint(p.team, buildCtx),
		ManagedRoots:      defaultManagedClaudeMdRoots(),
		UserRoot:          defaultUserClaudeMdRoot(),
	}
	candidates, err := resolveClaudeMdCandidates(resolveCfg, gate)
	if err != nil {
		return nil, err
	}
	manifestDigest := digestClaudeMdCandidates(candidates)
	cacheKey := claudeMdSourceCacheKey(buildCtx, gate, manifestDigest)
	if sources, ok := p.lookup(cacheKey); ok {
		return sources, nil
	}
	sources, err := loadClaudeMdSources(ctx, candidates)
	if err != nil {
		return nil, err
	}
	sources = FilterInjectedMemoryFiles(sources, buildCtx, gate, buildCtx.ClaudeMdExcludes)
	p.store(cacheKey, sources)
	return cloneClaudeMdSources(sources), nil
}

// OnPromptInvalidate 处理onpromptinvalidate。
func (p *ClaudeMdSourcesProvider) OnPromptInvalidate(reason contract.InvalidateReason) {
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

// ResolveClaudeMdSources 解析claudemdsources。
func ResolveClaudeMdSources(ctx context.Context, cfg ClaudeMdResolveConfig) ([]ClaudeMdSource, error) {
	gate := cfg.Dependencies.resolveGate(cfg.BuildCtx)
	if shouldDisableClaudeMdSources(gate) {
		return nil, nil
	}
	candidates, err := resolveClaudeMdCandidates(cfg, gate)
	if err != nil {
		return nil, err
	}
	return loadClaudeMdSources(ctx, candidates)
}

func shouldDisableClaudeMdSources(gate GateSnapshot) bool {
	return gate.SuppressForOverlay || gate.DisableClaudeMds || (gate.BareMode && !gate.HasAdditionalDirsForBare)
}

// FilterInjectedMemoryFiles 处理过滤条件injected记忆文件。
func FilterInjectedMemoryFiles(sources []ClaudeMdSource, buildCtx contract.BuildCtx, gate GateSnapshot, excludes []string) []ClaudeMdSource {
	patterns := normalizeClaudeMdExcludePatterns(excludes)
	projectFilter := resolveProjectSourceFilter(buildCtx, gate)
	filtered := make([]ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		if shouldSkipInjectedSource(source, gate) {
			continue
		}
		if shouldExcludeClaudeMdSource(source, patterns) {
			continue
		}
		if shouldSkipProjectSource(source, projectFilter) {
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

func claudeMdSourceCacheKey(buildCtx contract.BuildCtx, gate GateSnapshot, manifestDigest string) string {
	payload := []string{
		strings.TrimSpace(manifestDigest),
		boolToken(gate.DisableClaudeMds),
		boolToken(gate.InjectMemoryIndex),
		boolToken(gate.InjectTeamMemIndex),
		boolToken(gate.SkipProjectLocalClaudeMd),
		boolToken(buildCtx.IsWorktree),
		strings.Join(normalizeStringSlice(buildCtx.ClaudeMdExcludes), "|"),
		strings.Join(normalizeStringSlice(buildCtx.AdditionalWorkingDirectories), "|"),
	}
	digest := sha256.Sum256([]byte(strings.Join(payload, "\n")))
	return hex.EncodeToString(digest[:])
}

// loadClaudeMdSources 加载claudemdsources。
func loadClaudeMdSources(ctx context.Context, candidates []claudeMdCandidate) ([]ClaudeMdSource, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sources := make([]ClaudeMdSource, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			break
		}
		source, ok, err := loadClaudeMdSource(candidate)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return nil, nil
	}
	return sources, nil
}

func loadClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool, error) {
	// Phase 1.6 removed AutoMem / TeamMem MEMORY.md from the nested
	// ClaudeMd candidate set, so loadMemoryClaudeMdSource is no longer
	// reachable from production. The shared filter (shouldSkipInjectedSource)
	// also rejects those source types defensively. All remaining nested
	// sources go through loadStandardClaudeMdSource.
	return loadStandardClaudeMdSource(candidate)
}

// loadStandardClaudeMdSource 加载standardclaudemdsource。
func loadStandardClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool, error) {
	// Phase 2.1.A: defense-in-depth read. Even though appendClaudeMdCandidate
	// already verified the candidate path stays under BaseDir post-EvalSymlinks,
	// re-check at load time so a symlink swapped between candidate-time and
	// load-time still cannot redirect the read outside BaseDir.
	if candidate.BaseDir == "" {
		return ClaudeMdSource{}, false, nil
	}
	raw, _, err := memshared.SafeReadEntrypoint(candidate.BaseDir, candidate.Path)
	if err != nil {
		return ClaudeMdSource{}, false, fmt.Errorf("load ClaudeMd source %q: %w", candidate.Path, err)
	}
	content := parse.StripUTF8BOM(string(raw))
	metadata := claudeRuleMetadata{}
	if candidate.IsRule {
		metadata, content = parseClaudeRuleContent(content)
	} else {
		content = strings.TrimSpace(parse.StripHTMLComments(content))
	}
	if strings.TrimSpace(content) == "" {
		return ClaudeMdSource{}, false, nil
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
	}, true, nil
}
