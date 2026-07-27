package nested

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

var _ contract.TurnAttachmentProvider = (*ClaudeMdSourcesProvider)(nil)

// ResolveTurnAttachments 根据本轮附件触发 nested CLAUDE.md 规则注入。
// 只在 nested 启用且 gate 允许 CLAUDE.md 时工作；触发路径来自 turn attachments，
// 不直接扫描用户未提及的任意文件。
func (p *ClaudeMdSourcesProvider) ResolveTurnAttachments(
	ctx context.Context,
	buildCtx contract.BuildCtx,
	turn contract.TurnInput,
	baseSources []ClaudeMdSource,
) ([]dto.AttachmentEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil || p.nested == nil || !nestedMemoryEnabled(p.deps) {
		return nil, nil
	}
	if p.deps.resolveGate(buildCtx).DisableClaudeMds {
		return nil, nil
	}
	p.nested.ObserveBuildContext(turn.ThreadID, buildCtx)
	// TurnInput.Attachments 是带路径输入的统一触发通道；显式 @ 文件和前端/IDE
	// 选择的文件都会在 turn 汇编阶段规整到这里。
	if err := p.nested.AddTriggers(turn.ThreadID, buildCtx, turn.Attachments); err != nil {
		return nil, fmt.Errorf("nested memory: add turn attachment triggers: %w", err)
	}
	triggers := p.nested.ConsumePending(turn.ThreadID, buildCtx)
	if len(triggers) == 0 {
		return nil, nil
	}
	return p.GetNestedMemoryAttachments(ctx, buildCtx, turn.ThreadID, triggers, baseSources)
}

// GetNestedMemoryAttachments 根据 pending trigger 生成 nested memory 附件。
// 每个 trigger 都会按上下文重新解析条件规则，已在本 thread 注入过的来源会被跳过。
func (p *ClaudeMdSourcesProvider) GetNestedMemoryAttachments(
	ctx context.Context,
	buildCtx contract.BuildCtx,
	threadID string,
	triggers []string,
	baseSources []ClaudeMdSource,
) ([]dto.AttachmentEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil || p.nested == nil || len(triggers) == 0 {
		return nil, nil
	}
	attachments := make([]dto.AttachmentEnvelope, 0, len(triggers))
	for _, target := range normalizeStringSlice(triggers) {
		if err := ctx.Err(); err != nil {
			break
		}
		next, err := p.appendNestedMemoryAttachments(attachments, ctx, buildCtx, threadID, target, baseSources)
		if err != nil {
			return nil, err
		}
		attachments = next
	}
	if len(attachments) == 0 {
		return nil, nil
	}
	return attachments, nil
}

// appendNestedMemoryAttachments 解析单个目标路径对应的条件来源并追加附件。
// 条件来源加载失败会返回错误，避免把部分损坏规则静默忽略。
func (p *ClaudeMdSourcesProvider) appendNestedMemoryAttachments(
	attachments []dto.AttachmentEnvelope,
	ctx context.Context,
	buildCtx contract.BuildCtx,
	threadID, target string,
	baseSources []ClaudeMdSource,
) ([]dto.AttachmentEnvelope, error) {
	sources, err := resolveNestedConditionalSources(ctx, buildCtx, p.deps, target, baseSources)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		attachment, ok, err := p.resolveNestedMemoryAttachment(threadID, buildCtx, source)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

// resolveNestedMemoryAttachment 将单个 ClaudeMdSource 转换为附件并标记已加载。
// MarkLoaded 失败表示该 thread 已注入过该来源或来源为空，调用方应跳过。
func (p *ClaudeMdSourcesProvider) resolveNestedMemoryAttachment(
	threadID string,
	buildCtx contract.BuildCtx,
	source ClaudeMdSource,
) (dto.AttachmentEnvelope, bool, error) {
	attachment, ok, err := nestedMemoryAttachment(source)
	if err != nil {
		return dto.AttachmentEnvelope{}, false, err
	}
	if !ok {
		return dto.AttachmentEnvelope{}, false, nil
	}
	if !p.nested.MarkLoaded(threadID, buildCtx, source) {
		return dto.AttachmentEnvelope{}, false, nil
	}
	return attachment, true, nil
}

// nestedMemoryEnabled 判断 nested 规则功能是否启用。
// 保留 helper 便于后续加入更多依赖级 gate。
func nestedMemoryEnabled(deps Dependencies) bool {
	return deps.NestedEnabled
}

// nestedMemoryAttachment 读取来源文件元信息并构造 nested 附件。
// 文件消失或为空时返回 ok=false；其它 stat 错误会上抛。
func nestedMemoryAttachment(source ClaudeMdSource) (dto.AttachmentEnvelope, bool, error) {
	content := strings.TrimSpace(source.Content)
	if content == "" {
		return dto.AttachmentEnvelope{}, false, nil
	}
	info, err := os.Stat(source.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return dto.AttachmentEnvelope{}, false, nil
		}
		return dto.AttachmentEnvelope{}, false, fmt.Errorf("nested memory stat %q: %w", source.Path, err)
	}
	if info.IsDir() {
		return dto.AttachmentEnvelope{}, false, nil
	}
	updatedAt := info.ModTime().UTC()
	attachment := contract.NormalizeAttachmentEnvelope(dto.AttachmentEnvelope{
		Kind:      dto.AttachmentKindNestedMemory,
		Path:      strings.TrimSpace(source.Path),
		Header:    nestedMemoryHeader(source),
		Content:   content,
		MtimeMs:   updatedAt.UnixMilli(),
		UpdatedAt: updatedAt.Format(time.RFC3339),
	})
	return attachment, contract.IsValidAttachmentEnvelope(attachment), nil
}

// nestedMemoryHeader 生成 nested 附件头部。
// description 仅作为来源说明，不改变附件内容的信任级别。
func nestedMemoryHeader(source ClaudeMdSource) string {
	header := "Contents of " + strings.TrimSpace(source.Path)
	description := strings.TrimSpace(source.Description)
	if description == "" {
		return header + ":"
	}
	return header + " (" + description + "):"
}

// parseFrontmatterPaths 从规则 frontmatter 中解析 paths/globs。
// 支持单行值和 YAML 列表，返回前会统一清洗去重。
func parseFrontmatterPaths(frontmatter string) []string {
	paths := make([]string, 0, 4)
	activeList := false
	for line := range strings.SplitSeq(strings.ReplaceAll(frontmatter, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if activeList && strings.HasPrefix(trimmed, "- ") {
			paths = append(paths, parseScalar(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		activeList = false
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "paths", "path", "globs", "glob":
			if strings.TrimSpace(value) == "" {
				activeList = true
				continue
			}
			paths = append(paths, parseStringList(value)...)
		}
	}
	return normalizeStringSlice(paths)
}

// MatchTargetPath 判断目标文件是否命中规则 glob。
// 目标必须位于 baseDir 内；glob 只匹配相对路径，避免规则越界影响其它目录。
func MatchTargetPath(target string, globs []string, baseDir string) bool {
	if strings.TrimSpace(target) == "" || len(globs) == 0 {
		return false
	}
	baseDir = cleanClaudeMdPath(baseDir)
	target = cleanClaudeMdPath(target)
	if baseDir == "" || target == "" || !isAncestorOrSame(baseDir, target) {
		return false
	}
	relPath, err := filepath.Rel(baseDir, target)
	if err != nil {
		return false
	}
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "./")
	for _, glob := range normalizeStringSlice(globs) {
		for _, variant := range targetGlobVariants(glob) {
			if matchClaudeMdExclude(variant, relPath) {
				return true
			}
		}
	}
	return false
}

// targetGlobVariants 生成用于匹配的 glob 变体。
// 去掉 `**/` 的紧凑变体可兼容常见规则写法，同时保持原始 glob。
func targetGlobVariants(glob string) []string {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return nil
	}
	variants := []string{glob}
	compact := strings.ReplaceAll(glob, "**/", "")
	if compact != glob {
		variants = append(variants, compact)
	}
	return normalizeStringSlice(variants)
}

// resolveNestedConditionalSources 解析目标文件可触发的条件 nested 来源。
// 它只扫描 CWD 层级和目标路径层级，并过滤掉 baseSources 已注入的来源。
func resolveNestedConditionalSources(
	ctx context.Context,
	buildCtx contract.BuildCtx,
	deps Dependencies,
	target string,
	baseSources []ClaudeMdSource,
) ([]ClaudeMdSource, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dirs := nestedLayerDirs(buildCtx, target)
	if len(dirs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(dirs)*4)
	candidates := make([]claudeMdCandidate, 0, len(dirs)*4)
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			break
		}
		if err := appendProjectStyleCandidates(&candidates, seen, dir, sourceTypeProject, sourceOriginProject, true); err != nil {
			return nil, err
		}
	}
	sources, err := loadClaudeMdSources(ctx, candidates)
	if err != nil {
		return nil, err
	}
	sources = FilterInjectedMemoryFiles(sources, buildCtx, deps.resolveGate(buildCtx), buildCtx.ClaudeMdExcludes)
	return filterNestedConditionalDelta(sources, target, baseSources), nil
}

// nestedLayerDirs 计算目标文件触发 nested 规则时需要扫描的目录层级。
// CWD 祖先目录先于目标子目录，顺序去重后用于候选收集。
func nestedLayerDirs(buildCtx contract.BuildCtx, target string) []string {
	seen := make(map[string]struct{}, 8)
	ordered := make([]string, 0, 8)
	appendNestedDirs := func(dirs []string) {
		for _, dir := range dirs {
			dir = cleanClaudeMdPath(dir)
			if dir == "" {
				continue
			}
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			ordered = append(ordered, dir)
		}
	}
	appendNestedDirs(cwdLevelDirs(buildCtx.GitRoot, buildCtx.CWD))
	appendNestedDirs(nestedDirs(buildCtx.CWD, target))
	return ordered
}

// cwdLevelDirs 返回 CWD 到 GitRoot 的祖先目录链。
// 复用 ancestorWalkDirs，保证和基础 CLAUDE.md 发现顺序一致。
func cwdLevelDirs(root, cwd string) []string {
	return ancestorWalkDirs(root, cwd)
}

// nestedDirs 返回从 CWD 到目标文件目录的 nested 层级。
// 目标不在 CWD 下时返回 nil，避免跨工作目录加载规则。
func nestedDirs(cwd, target string) []string {
	cwd = cleanClaudeMdPath(cwd)
	targetDir := cleanClaudeMdPath(filepath.Dir(strings.TrimSpace(target)))
	if cwd == "" || targetDir == "" || !isAncestorOrSame(cwd, targetDir) {
		return nil
	}
	stack := make([]string, 0, 4)
	for dir := targetDir; dir != ""; dir = filepath.Dir(dir) {
		stack = append(stack, dir)
		if dir == cwd {
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

// filterNestedConditionalDelta 过滤本轮新增且匹配目标路径的条件来源。
// 已存在于 baseSources 或本轮重复的来源会跳过，避免同一 CLAUDE.md 重复注入。
func filterNestedConditionalDelta(sources []ClaudeMdSource, target string, baseSources []ClaudeMdSource) []ClaudeMdSource {
	baseKeys := baseUserContextSourceKeys(baseSources)
	seen := make(map[string]struct{}, len(sources))
	filtered := make([]ClaudeMdSource, 0, len(sources))
	for _, source := range sources {
		if !source.Conditional || strings.TrimSpace(source.Content) == "" {
			continue
		}
		if !MatchTargetPath(target, source.Globs, source.BaseDir) {
			continue
		}
		key := nestedSourceKey(source)
		if _, ok := baseKeys[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, cloneClaudeMdSource(source))
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// baseUserContextSourceKeys 为 base user context 中已注入的非条件 CLAUDE.md 来源生成去重键。
// 只纳入有内容的基础来源，条件来源留给 target 匹配阶段处理；键包含路径、origin、scope 和内容摘要，避免同一路径不同内容被误判相同。
func baseUserContextSourceKeys(sources []ClaudeMdSource) map[string]struct{} {
	if len(sources) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if source.Conditional || strings.TrimSpace(source.Content) == "" {
			continue
		}
		keys[nestedSourceKey(source)] = struct{}{}
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func nestedSourceKey(source ClaudeMdSource) string {
	scope := strings.TrimSpace(source.RuleScope)
	if scope == "" {
		scope = strings.TrimSpace(source.Type)
	}
	return strings.Join([]string{
		strings.TrimSpace(source.Path),
		strings.TrimSpace(source.Origin),
		scope,
		nestedSourceDigest(source),
	}, "\n")
}

func nestedSourceDigest(source ClaudeMdSource) string {
	if digest := strings.TrimSpace(source.Digest); digest != "" {
		return digest
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(source.Content)))
	return hex.EncodeToString(sum[:])
}
