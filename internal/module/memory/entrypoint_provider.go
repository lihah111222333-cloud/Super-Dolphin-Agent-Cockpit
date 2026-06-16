package memory

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var _ contract.DynamicSectionProvider = (*MemoryEntrypointProvider)(nil)

// MemoryEntrypointProvider injects the durable memory entrypoint files
// (private MEMORY.md plus, when team memory is active, the team MEMORY.md)
// at session start. It complements MemoryRulesProvider, which only carries
// the behavioural rules: this provider carries the actual index content.
//
// The provider is start-only — turn-time retrieval is handled by
// MemoryContextProvider / RelevantMemoryFinder. It is suppressed when
// gate.InjectPromptEntrypoint is false (today identical to InjectMemoryIndex,
// see resolveMemoryGate).
type MemoryEntrypointProvider struct {
	cfg    *Config
	team   *TeamMemoryManager
	logger *pkglogger.Logger
}

// NewEntrypointProvider returns a MemoryEntrypointProvider wired to the
// shared memory config and (optionally) the team memory manager. Either may
// be nil; the provider is fully nil-safe and degrades to "no entrypoint".
// NewEntrypointProvider 创建entrypointprovider。
func NewEntrypointProvider(cfg *Config, team *TeamMemoryManager, logger *pkglogger.Logger) *MemoryEntrypointProvider {
	return &MemoryEntrypointProvider{cfg: memoryConfig(cfg), team: team, logger: logger}
}

// SectionName implements contract.DynamicSectionProvider.
// SectionName 处理section名称。
func (p *MemoryEntrypointProvider) SectionName() string {
	return contract.DynamicSectionMemoryEntrypoint
}

// Resolve implements contract.DynamicSectionProvider. It runs only at
// session start. Child agents use the prompt system for role-specific context.
// Resolve 解析记忆。
func (p *MemoryEntrypointProvider) Resolve(_ context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || input.Start == nil || input.Turn != nil {
		return nil, nil
	}
	if !memoryProductEnabled(p.cfg) {
		return nil, nil
	}
	gate := ResolveMemoryGate(input.BuildCtx, p.cfg)
	if !gate.AutoEnabled || !gate.InjectPromptEntrypoint {
		return nil, nil
	}
	autoBlock := p.loadEntrypointBlock(p.resolvedAutoMemPath(input.BuildCtx), entrypointSourceAuto)
	teamBlock := ""
	if gate.InjectTeamMemIndex {
		teamBlock = p.loadEntrypointBlock(p.resolvedTeamMemPath(input.BuildCtx), entrypointSourceTeam)
	}
	body := joinNonEmpty([]string{autoBlock, teamBlock}, "\n\n")
	if body == "" {
		return nil, nil
	}
	wrapped := "## " + contract.DynamicSectionMemoryEntrypoint + "\n\n" + body
	return &wrapped, nil
}

const (
	entrypointSourceAuto = "auto"
	entrypointSourceTeam = "team"
)

// loadEntrypointBlock returns the rendered block for one entrypoint root, or
// an empty string when the file is missing/empty/unreadable/secret-tainted.
// The block is frontmatter- and HTML-comment-stripped, BOM-trimmed, and
// entrypoint-truncated using the same limits applied elsewhere.
//
// Rendering uses the relative file name "MEMORY.md" instead of the full
// absolute path so the OS user / home prefix is not leaked into the prompt.
// loadEntrypointBlock 加载entrypointblock。
func (p *MemoryEntrypointProvider) loadEntrypointBlock(root, source string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	raw, _, err := shared.SafeReadEntrypoint(root, memoryIndexPath(root))
	if err != nil {
		if !errors.Is(err, shared.ErrSafeReadNotFound) && p.logger != nil {
			p.logger.Warn("memory entrypoint read failed",
				"source", source,
				"root", root,
				"error", err,
			)
		}
		return ""
	}
	cleaned := cleanEntrypointContent(string(raw))
	if cleaned == "" {
		return ""
	}
	if source == entrypointSourceTeam {
		if findings := ScanTeamMemContent(cleaned); len(findings) > 0 {
			p.logTeamSecretSkip(findings)
			return ""
		}
	}
	truncation := TruncateEntrypointContent(cleaned)
	content := strings.TrimSpace(truncation.Content)
	if content == "" {
		return ""
	}
	header := "Contents of MEMORY.md (source=" + source + "):"
	return header + "\n" + content
}

func (p *MemoryEntrypointProvider) logTeamSecretSkip(findings []TeamMemSecretFinding) {
	if p == nil || p.logger == nil {
		return
	}
	ruleIDs := make([]string, 0, len(findings))
	for _, f := range findings {
		ruleIDs = append(ruleIDs, f.RuleID)
	}
	p.logger.Warn("team memory entrypoint skipped due to secret findings",
		"memory_type", "team",
		"finding_count", len(findings),
		"rule_ids", ruleIDs,
	)
}

// cleanEntrypointContent strips BOM, YAML frontmatter, and HTML block
// comments before truncation runs. This mirrors what Claude's claudemd
// layer applies to AutoMem / TeamMem entrypoints so injected text is not
// padded with metadata that is irrelevant to the model.
func cleanEntrypointContent(raw string) string {
	trimmed := strings.TrimSpace(parse.StripUTF8BOM(raw))
	if trimmed == "" {
		return ""
	}
	if _, body, ok := parse.SplitFrontmatter(trimmed); ok {
		trimmed = strings.TrimSpace(body)
	}
	stripped := strings.TrimSpace(parse.StripHTMLComments(trimmed))
	return stripped
}

// resolvedAutoMemPath mirrors MemoryRulesProvider.resolvedAutoMemPath so
// both providers see the same AutoMem root for a given BuildCtx.
func (p *MemoryEntrypointProvider) resolvedAutoMemPath(buildCtx contract.BuildCtx) string {
	cfg := memoryConfig(p.cfg)
	projectRoot := strings.TrimSpace(buildCtx.GitRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(buildCtx.CWD)
	}
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	autoDir, err := resolvedStoreRoot(cfg.RootDir, projectRoot, configuredAutoMemPathOverride(cfg))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(autoDir)
}

// resolvedTeamMemPath returns the team-memory root for the current build
// context, or empty when team memory is not configured / available.
func (p *MemoryEntrypointProvider) resolvedTeamMemPath(buildCtx contract.BuildCtx) string {
	if p == nil || p.team == nil {
		return ""
	}
	return strings.TrimSpace(p.team.GetTeamMemPath(buildCtx))
}

func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, sep)
}
