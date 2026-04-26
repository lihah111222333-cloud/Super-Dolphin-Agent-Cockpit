package nested

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
}

type Dependencies struct {
	NestedEnabled     bool
	Gate              func(contract.BuildCtx) GateSnapshot
	AutoMemRoot       func(contract.BuildCtx) string
	TeamRoot          func(contract.BuildCtx) string
	IsAgentMemoryPath func(string) bool
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

func (d Dependencies) isAgentMemoryPath(path string) bool {
	return d.IsAgentMemoryPath != nil && d.IsAgentMemoryPath(path)
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

func (p *ClaudeMdSourcesProvider) ResolveClaudeMdSources(ctx context.Context, buildCtx contract.BuildCtx) []contract.ClaudeMdSource {
	gate := p.deps.resolveGate(buildCtx)
	if shouldDisableClaudeMdSources(gate) {
		return nil
	}
	resolveCfg := ClaudeMdResolveConfig{
		BuildCtx:          buildCtx,
		Dependencies:      p.deps,
		TeamMemPath:       resolveTeamMemPath(p.team, buildCtx),
		TeamMemEntrypoint: resolveTeamMemEntrypoint(p.team, buildCtx),
		ManagedRoots:      defaultManagedClaudeMdRoots(),
		UserRoot:          defaultUserClaudeMdRoot(),
	}
	candidates := resolveClaudeMdCandidates(resolveCfg, gate)
	manifestDigest := digestClaudeMdCandidates(candidates)
	cacheKey := claudeMdSourceCacheKey(buildCtx, gate, manifestDigest)
	if sources, ok := p.lookup(cacheKey); ok {
		return sources
	}
	sources := loadClaudeMdSources(ctx, candidates)
	sources = FilterInjectedMemoryFiles(sources, buildCtx, gate, buildCtx.ClaudeMdExcludes)
	p.store(cacheKey, sources)
	return cloneClaudeMdSources(sources)
}

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

func ResolveClaudeMdSources(ctx context.Context, cfg ClaudeMdResolveConfig) []ClaudeMdSource {
	gate := cfg.Dependencies.resolveGate(cfg.BuildCtx)
	if shouldDisableClaudeMdSources(gate) {
		return nil
	}
	candidates := resolveClaudeMdCandidates(cfg, gate)
	return loadClaudeMdSources(ctx, candidates)
}

func shouldDisableClaudeMdSources(gate GateSnapshot) bool {
	return gate.DisableClaudeMds || (gate.BareMode && !gate.HasAdditionalDirsForBare)
}

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

func loadClaudeMdSources(ctx context.Context, candidates []claudeMdCandidate) []ClaudeMdSource {
	if ctx == nil {
		ctx = context.Background()
	}
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
	// Phase 1.6 removed AutoMem / TeamMem MEMORY.md from the nested
	// ClaudeMd candidate set, so loadMemoryClaudeMdSource is no longer
	// reachable from production. The shared filter (shouldSkipInjectedSource)
	// also rejects those source types defensively. All remaining nested
	// sources go through loadStandardClaudeMdSource.
	return loadStandardClaudeMdSource(candidate)
}

func loadStandardClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool) {
	// Phase 2.1.A: defense-in-depth read. Even though appendClaudeMdCandidate
	// already verified the candidate path stays under BaseDir post-EvalSymlinks,
	// re-check at load time so a symlink swapped between candidate-time and
	// load-time still cannot redirect the read outside BaseDir.
	if candidate.BaseDir == "" {
		return ClaudeMdSource{}, false
	}
	raw, _, err := memshared.SafeReadEntrypoint(candidate.BaseDir, candidate.Path)
	if err != nil {
		return ClaudeMdSource{}, false
	}
	content := parse.StripUTF8BOM(string(raw))
	metadata := claudeRuleMetadata{}
	if candidate.IsRule {
		metadata, content = parseClaudeRuleContent(content)
	} else {
		content = strings.TrimSpace(parse.StripHTMLComments(content))
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
