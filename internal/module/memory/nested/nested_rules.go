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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

var _ contract.TurnAttachmentProvider = (*ClaudeMdSourcesProvider)(nil)

// ResolveTurnAttachments 解析turnattachments。
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
	// TurnInput.Attachments is the shared trigger lane for path-bearing turn inputs.
	// turn.prepareTurnAssembly() builds it from turnAttachmentRefs(req.Inputs), where
	// req.Inputs includes both explicit @mentioned files and path-only frontend/IDE
	// file selections normalized into mention inputs by the turn input assembler.
	p.nested.AddTriggers(turn.ThreadID, buildCtx, turn.Attachments)
	triggers := p.nested.ConsumePending(turn.ThreadID, buildCtx)
	if len(triggers) == 0 {
		return nil, nil
	}
	return p.GetNestedMemoryAttachments(ctx, buildCtx, turn.ThreadID, triggers, baseSources)
}

// GetNestedMemoryAttachments 读取nested记忆attachments。
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

func nestedMemoryEnabled(deps Dependencies) bool {
	return deps.NestedEnabled
}

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

func nestedMemoryHeader(source ClaudeMdSource) string {
	header := "Contents of " + strings.TrimSpace(source.Path)
	description := strings.TrimSpace(source.Description)
	if description == "" {
		return header + ":"
	}
	return header + " (" + description + "):"
}

// parseFrontmatterPaths 解析frontmatter路径。
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

// MatchTargetPath 判断target路径是否匹配。
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

// resolveNestedConditionalSources 解析nestedconditionalsources。
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

func cwdLevelDirs(root, cwd string) []string {
	return ancestorWalkDirs(root, cwd)
}

// nestedDirs 处理nested目录。
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

// filterNestedConditionalDelta 处理过滤条件nestedconditionaldelta。
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

// baseUserContextSourceKeys 处理baseuser上下文source键。
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
