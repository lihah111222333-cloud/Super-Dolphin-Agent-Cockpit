package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

var _ contract.DynamicSectionProvider = (*MemoryEntrypointProvider)(nil)

// MemoryEntrypointProvider 在会话启动时注入持久化记忆入口文件。
// 它只负责 AutoMem/TeamMem 的索引正文；每轮相关记忆检索由 retrieval 流程处理，
// 并且受 ResolveMemoryGate 控制，避免底层 CLI 已注入时重复写入 prompt。
type MemoryEntrypointProvider struct {
	cfg    *Config
	team   *TeamMemoryManager
	logger *slog.Logger
}

// NewEntrypointProvider 绑定记忆配置和可选团队记忆管理器。
// 调用方允许传入 nil；解析阶段会按空入口处理，不在构造期读盘。
func NewEntrypointProvider(cfg *Config, team *TeamMemoryManager, logger *slog.Logger) *MemoryEntrypointProvider {
	return &MemoryEntrypointProvider{cfg: memoryConfig(cfg), team: team, logger: logger}
}

// SectionName 返回 prompt 动态区块名，供 prompt 汇编器按区块缓存和失效。
func (p *MemoryEntrypointProvider) SectionName() string {
	return contract.DynamicSectionMemoryEntrypoint
}

// Resolve 在会话启动阶段读取可注入的记忆入口内容。
// 非启动 turn、功能关闭或 gate 禁止注入时返回空；读盘失败只跳过该入口并记录告警，
// 避免记忆索引缺失阻断会话启动。
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

// loadEntrypointBlock 从单个记忆根读取 MEMORY.md 并渲染为 prompt 片段。
// 这里会清理 BOM、frontmatter、HTML 注释并做长度截断；团队记忆还会扫描密钥。
// 渲染时只暴露相对文件名，避免把本机用户目录泄漏进模型上下文。
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

// cleanEntrypointContent 统一清理入口文件外壳，只保留要注入模型的正文。
// 该步骤和 nested 的 CLAUDE.md 解析保持同一套语义，避免 frontmatter 或模板注释
// 被当成用户记忆长期带入 prompt。
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

// resolvedAutoMemPath 按 BuildCtx 和配置解析 AutoMem 根目录。
// 入口注入与规则提示必须共用同一套根目录解析，否则会出现规则指向一处、
// 实际索引读取另一处的跨模块错配。
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

// resolvedTeamMemPath 读取当前构建上下文对应的团队记忆根目录。
// 团队记忆未启用或管理器缺失时返回空，让 Resolve 自然跳过团队入口。
func (p *MemoryEntrypointProvider) resolvedTeamMemPath(buildCtx contract.BuildCtx) string {
	if p == nil || p.team == nil {
		return ""
	}
	return strings.TrimSpace(p.team.GetTeamMemPath(buildCtx))
}

// joinNonEmpty 拼接非空区块，并在拼接前统一裁剪外层空白。
// 入口区块来自不同记忆根，保持这里的空值过滤可避免生成空标题或多余分隔符。
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
