package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// copyMCPSnapshot 深拷贝 MCP 快照，避免 prompt snapshot 复用调用方传入的 map/slice。
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

// dynamicTurnAttachmentProvider 是动态 section 可选实现的 turn attachment 接口。
type dynamicTurnAttachmentProvider interface {
	ResolveTurnAttachments(context.Context, SectionContext) []dto.AttachmentEnvelope
}

// resolveDynamicTurnAttachments 收集非 start-only 动态 section 生成的 turn 附件。
// 读取 provider map 时持有读锁，附件生成仍由 provider 自身保证不阻塞或不共享可变状态。
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

// copyOutputStyleConfig 深拷贝输出风格配置，包含可选 bool 指针。
func copyOutputStyleConfig(cfg *OutputStyleConfig) *OutputStyleConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.KeepCodingInstructions = copyOptionalBool(cfg.KeepCodingInstructions)
	return &cloned
}

// copyFRCConfig 标准化并复制 FRC 配置。
func copyFRCConfig(cfg *contract.FRCConfig) *contract.FRCConfig {
	if cfg == nil {
		return nil
	}
	return cfg.Normalize()
}

// copyOptionalBool 深拷贝 bool 指针，nil 保持 nil。
func copyOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// copyFlags 深拷贝 session flags，空 map 收敛为 nil。
func copyFlags(flags map[string]bool) map[string]bool {
	if len(flags) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(flags))
	maps.Copy(cloned, flags)
	return cloned
}

// resolvedSection 将非空 section 内容包装为 ResolvedPromptSection。
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

// renderResolvedSectionsByRegion 渲染指定 region 的已解析 section。
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

// startAssemblyBoundary 将静态 section 放入 cached prefix，动态 section 和 base tail 放入 uncached tail。
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

// resolvedSectionSnapshot 保存已解析 section 的非空内容。
// snapshot 只按 section 名称记录正文，用于 start snapshot 恢复和 hash 复核。
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

// joinBlocks 去掉空白块并用空行连接，保证 prompt 拼接结果稳定。
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

// clonePromptBoundary 深拷贝 prompt boundary，并把空 prefix/tail 收敛为 nil。
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

// boundaryCachedPrefix 返回 boundary 的 cached prefix，nil 时返回空字符串。
func boundaryCachedPrefix(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.CachedPrefix)
}

// boundaryUncachedTail 返回 boundary 的 uncached tail，nil 时返回空字符串。
func boundaryUncachedTail(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.UncachedTail)
}

// snapshotHashParts 返回参与 snapshot hash 的稳定字段列表。
func snapshotHashParts(displayName, base, dev, provider string, boundary *dto.PromptAssemblyBoundary) []string {
	parts := []string{displayName, base, dev, provider}
	return append(parts, boundaryCachedPrefix(boundary), boundaryUncachedTail(boundary))
}

// snapshotHash 计算 prompt snapshot 的稳定 hash，字段之间用零字节分隔避免拼接歧义。
func snapshotHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// aggregateSuppressedTools 汇总用户禁用工具产生的 prompt 软过滤列表。
// provider-native skills 不再通过 prompt assembly metadata 隐式屏蔽原生工具。
func (s *service) aggregateSuppressedTools(ctx context.Context, cwd, provider string) ([]string, error) {
	seen := make(map[string]struct{})
	provider = strings.TrimSpace(provider)
	if s.disabledToolsFn != nil {
		disabledTools, err := s.disabledToolsFn(ctx, cwd, provider)
		if err != nil {
			return nil, err
		}
		for _, name := range disabledTools {
			seen[name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
