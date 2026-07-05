// Package nested 管理 CLAUDE.md 及相关规则文件的发现、加载与注入。
// 负责从 managed/user/project/addDir 四类来源解析候选项，过滤后提供给 prompt 构建流程。
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

// ClaudeMdSource 是 prompt 层可消费的 CLAUDE.md 来源别名。
// nested 包会把 auto/team memory entrypoint 也包装成这种 provider-visible 来源。
type ClaudeMdSource = contract.ClaudeMdSource

// GateSnapshot 是 nested 包内使用的门控快照，控制 CLAUDE.md 来源加载行为。
// SuppressForOverlay 为 true 时表示由底层 CLI 原生处理 CLAUDE.md，nested 不再注入，防止双重注入。
type GateSnapshot struct {
	BareMode                 bool
	HasAdditionalDirsForBare bool
	DisableClaudeMds         bool
	SkipProjectLocalClaudeMd bool
	InjectMemoryIndex        bool
	InjectTeamMemIndex       bool
	// SuppressForOverlay 表示当前 CLI harness 会原生处理 CLAUDE.md。
	// nested 包看到该标志后停止加载来源，作为防重复注入边界；即使 provider 后续重新消费
	// UserContextText，也不会和底层 overlay 同时注入 CLAUDE.md。
	SuppressForOverlay bool
}

// Dependencies 是 nested 包的外部依赖注入结构，调用方按需提供各回调。
type Dependencies struct {
	NestedEnabled bool
	Gate          func(contract.BuildCtx) GateSnapshot
	AutoMemRoot   func(contract.BuildCtx) string
	TeamRoot      func(contract.BuildCtx) string
}

// resolveGate 调用注入的 Gate 回调获取门控快照，Gate 为 nil 时返回空快照。
func (d Dependencies) resolveGate(buildCtx contract.BuildCtx) GateSnapshot {
	if d.Gate == nil {
		return GateSnapshot{}
	}
	return d.Gate(buildCtx)
}

// autoMemRoot 返回清理后的 AutoMem 根路径，AutoMemRoot 为 nil 时返回空字符串。
func (d Dependencies) autoMemRoot(buildCtx contract.BuildCtx) string {
	if d.AutoMemRoot == nil {
		return ""
	}
	return cleanClaudeMdPath(d.AutoMemRoot(buildCtx))
}

// teamRoot 返回清理后的 team 记忆根路径，TeamRoot 为 nil 时返回空字符串。
func (d Dependencies) teamRoot(buildCtx contract.BuildCtx) string {
	if d.TeamRoot == nil {
		return ""
	}
	return cleanClaudeMdPath(d.TeamRoot(buildCtx))
}

// ClaudeMdResolveConfig 描述一次 CLAUDE.md 来源解析所需的运行上下文。
// TeamMemPath 和 TeamMemEntrypoint 决定团队记忆是否作为 provider-visible source 注入。
type ClaudeMdResolveConfig struct {
	BuildCtx          contract.BuildCtx
	Dependencies      Dependencies
	TeamMemPath       string
	TeamMemEntrypoint string
	ManagedRoots      []string
	UserRoot          string
}

// ClaudeMdSourcesProvider 缓存并提供当前 BuildCtx 下的 CLAUDE.md 来源列表，线程安全。
type ClaudeMdSourcesProvider struct {
	deps   Dependencies
	team   contract.TeamMemoryManager
	nested *NestedRuntime

	mu    sync.RWMutex
	cache map[string][]ClaudeMdSource
}

// claudeMdCandidate 表示一个待加载的 CLAUDE.md 候选文件信息。
type claudeMdCandidate struct {
	Path      string
	Type      string
	Origin    string
	BaseDir   string
	RuleScope string
	IsRule    bool
	Digest    string
}

// NewClaudeMdSourcesProvider 创建 ClaudeMdSourcesProvider，nested 为 nil 时自动创建。
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

// ResolveClaudeMdSources 解析当前 BuildCtx 下的 CLAUDE.md 来源列表，结果按 manifestDigest 缓存。
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

// OnPromptInvalidate 清空来源缓存，并将事件传递给 NestedRuntime。
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

// ResolveClaudeMdSources 是无状态版本，直接解析并返回来源列表，不使用缓存。
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

// shouldDisableClaudeMdSources 判断是否应禁用 CLAUDE.md 来源加载。
func shouldDisableClaudeMdSources(gate GateSnapshot) bool {
	return gate.SuppressForOverlay || gate.DisableClaudeMds || (gate.BareMode && !gate.HasAdditionalDirsForBare)
}

// FilterInjectedMemoryFiles 过滤 CLAUDE.md 来源列表，依次应用 injected/exclude/project 过滤规则。
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

// lookup 线程安全地从缓存读取来源列表，key 为空或不存在时返回 false。
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

// store 线程安全地将来源列表写入缓存，key 为空时跳过。
func (p *ClaudeMdSourcesProvider) store(key string, sources []ClaudeMdSource) {
	if p == nil || strings.TrimSpace(key) == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = cloneClaudeMdSources(sources)
}

// claudeMdSourceCacheKey 根据 BuildCtx、GateSnapshot 和 manifestDigest 生成缓存键。
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

// loadClaudeMdSources 依次加载候选项，ctx 取消时中断，返回已成功加载的来源列表。
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
	// 当前 nested 候选只包含 CLAUDE.md 和规则文件；AutoMem/TeamMem 入口文件
	// 由父包入口 provider 负责。这里统一走标准加载路径，保持读取和过滤边界单一。
	return loadStandardClaudeMdSource(candidate)
}

// loadStandardClaudeMdSource 从磁盘读取候选文件，在加载时二次校验路径包含关系（defense-in-depth）。
// BaseDir 为空时跳过，防止路径逃逸攻击。
func loadStandardClaudeMdSource(candidate claudeMdCandidate) (ClaudeMdSource, bool, error) {
	// 收集候选时已经校验过解析后的路径仍在 BaseDir 下；加载时仍通过
	// SafeReadEntrypoint 再校验一次，防止候选收集和实际读取之间符号链接被替换。
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
