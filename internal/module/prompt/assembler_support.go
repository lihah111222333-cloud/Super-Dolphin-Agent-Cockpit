package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func copyMCPSnapshot(snapshot MCPSnapshot) MCPSnapshot {
	cloned := MCPSnapshot{
		Servers:                  append([]string(nil), snapshot.Servers...),
		Tools:                    append([]string(nil), snapshot.Tools...),
		InstructionsDeltaEnabled: snapshot.InstructionsDeltaEnabled,
		InstructionAttachments:   append([]MCPAttachmentRef(nil), snapshot.InstructionAttachments...),
	}
	if len(snapshot.Instructions) > 0 {
		cloned.Instructions = make(map[string]string, len(snapshot.Instructions))
		maps.Copy(cloned.Instructions, snapshot.Instructions)
	}
	return cloned
}

type dynamicTurnAttachmentProvider interface {
	ResolveTurnAttachments(context.Context, SectionContext) []dto.AttachmentEnvelope
}

func (s *service) resolveDynamicTurnAttachments(ctx context.Context, sectionCtx SectionContext) []dto.AttachmentEnvelope {
	sections := s.dynamicSections()
	attachments := make([]dto.AttachmentEnvelope, 0, len(sections))
	s.dynamicMu.RLock()
	defer s.dynamicMu.RUnlock()
	for _, section := range sections {
		provider, ok := s.dynamic[section.Name]
		if !ok || section.StartOnly {
			continue
		}
		attachmentProvider, ok := provider.(dynamicTurnAttachmentProvider)
		if !ok {
			continue
		}
		attachments = append(attachments, attachmentProvider.ResolveTurnAttachments(ctx, sectionCtx)...)
	}
	return attachments
}

func copyOutputStyleConfig(cfg *OutputStyleConfig) *OutputStyleConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.KeepCodingInstructions = copyOptionalBool(cfg.KeepCodingInstructions)
	return &cloned
}

func copyFRCConfig(cfg *contract.FRCConfig) *contract.FRCConfig {
	if cfg == nil {
		return nil
	}
	return cfg.Normalize()
}

func copyOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func copyFlags(flags map[string]bool) map[string]bool {
	if len(flags) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(flags))
	maps.Copy(cloned, flags)
	return cloned
}

func resolvedSection(section PromptSection, value *string) *ResolvedPromptSection {
	if value == nil {
		return nil
	}
	content := strings.TrimSpace(*value)
	if content == "" {
		return nil
	}
	return &ResolvedPromptSection{
		Name:     section.Name,
		Region:   section.Region,
		Volatile: section.Volatile,
		Content:  content,
	}
}

func renderResolvedSectionsByRegion(sections []ResolvedPromptSection, region PromptRegion) string {
	blocks := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.Region != region {
			continue
		}
		if content := strings.TrimSpace(section.Content); content != "" {
			blocks = append(blocks, content)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func startAssemblyBoundary(resolved []ResolvedPromptSection, baseTail string) *dto.PromptAssemblyBoundary {
	prefix := renderResolvedSectionsByRegion(resolved, PromptRegionStatic)
	tail := joinBlocks(renderResolvedSectionsByRegion(resolved, PromptRegionDynamic), baseTail)
	if prefix == "" && tail == "" {
		return nil
	}
	return &dto.PromptAssemblyBoundary{
		CachedPrefix: prefix,
		UncachedTail: tail,
	}
}

// resolvedSectionSnapshot 处理已解析section快照。
func resolvedSectionSnapshot(sections []ResolvedPromptSection) map[string]string {
	if len(sections) == 0 {
		return nil
	}
	snapshot := make(map[string]string, len(sections))
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		content := strings.TrimSpace(section.Content)
		if name != "" && content != "" {
			snapshot[name] = content
		}
	}
	if len(snapshot) == 0 {
		return nil
	}
	return snapshot
}

func joinBlocks(parts ...string) string {
	blocks := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			blocks = append(blocks, trimmed)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func clonePromptBoundary(boundary *dto.PromptAssemblyBoundary) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	cloned := dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	}
	if cloned.CachedPrefix == "" && cloned.UncachedTail == "" {
		return nil
	}
	return &cloned
}

func boundaryCachedPrefix(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.CachedPrefix)
}

func boundaryUncachedTail(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.UncachedTail)
}

func snapshotHashParts(displayName, base, dev, provider string, boundary *dto.PromptAssemblyBoundary) []string {
	parts := []string{displayName, base, dev, provider}
	return append(parts, boundaryCachedPrefix(boundary), boundaryUncachedTail(boundary))
}

func snapshotHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// aggregateSuppressedTools returns prompt soft filters only from user-selected
// disabled tools. Provider-native skills no longer suppress native tools through
// prompt assembly metadata.
func (s *service) aggregateSuppressedTools(ctx context.Context, cwd, provider string) []string {
	seen := make(map[string]struct{})
	provider = strings.TrimSpace(provider)
	if s.disabledToolsFn != nil {
		for _, name := range s.disabledToolsFn(ctx, cwd, provider) {
			seen[name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
