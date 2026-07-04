package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

const legacyPromptSnapshotMigrationRuntimeKey = "legacyPromptSnapshotMigration"

// ensureStartAssemblySnapshot 把 start 提示整理成可保存的 snapshot。
// start 和 resume rebuild 都走这里，避免 provider 和 thread store 各拿一份。
func ensureStartAssemblySnapshot(assembly contract.StartAssembly, provider string) contract.StartAssembly {
	assembly.DisplayName = normalizeStartDisplayName(strings.TrimSpace(assembly.DisplayName))
	assembly.BaseInstructions = strings.TrimSpace(assembly.BaseInstructions)
	assembly.Boundary = clonePromptBoundary(assembly.Boundary)
	assembly.DeveloperInstructions = strings.TrimSpace(assembly.DeveloperInstructions)
	snapshot := normalizePromptSnapshotContent(assembly.Snapshot)
	if snapshot.DisplayName == "" {
		snapshot.DisplayName = assembly.DisplayName
	}
	if snapshot.BaseInstructions == "" {
		snapshot.BaseInstructions = assembly.BaseInstructions
	}
	if snapshot.Boundary == nil {
		snapshot.Boundary = clonePromptBoundary(assembly.Boundary)
	}
	if snapshot.DeveloperInstructions == "" {
		snapshot.DeveloperInstructions = assembly.DeveloperInstructions
	}
	if snapshot.Provider == "" {
		snapshot.Provider = strings.TrimSpace(provider)
	}
	if snapshot.Version == 0 {
		snapshot.Version = contract.PromptAssemblySnapshotVersion
	}
	if len(snapshot.SectionSnapshot) == 0 {
		snapshot.SectionSnapshot = promptSnapshotSectionMap(assembly.ResolvedSections)
	}
	snapshot = applyStartRuntimeContextToSnapshot(snapshot, assembly)
	snapshot.Hash = promptSnapshotHash(
		snapshot.DisplayName,
		snapshot.BaseInstructions,
		snapshot.DeveloperInstructions,
		snapshot.Provider,
		snapshot.Boundary,
		snapshot.SectionSnapshot,
		snapshot.Generation,
	)
	assembly.Boundary = clonePromptBoundary(snapshot.Boundary)
	assembly.Snapshot = snapshot
	return assembly
}

// applyStartRuntimeContextToSnapshot 保存 provider 启动时实际追加的运行时上下文。
// resume 只依赖 snapshot，不能再从已丢失的 StartAssembly.UserContext 里临时重建。
func applyStartRuntimeContextToSnapshot(
	snapshot contract.PromptAssemblySnapshot,
	assembly contract.StartAssembly,
) contract.PromptAssemblySnapshot {
	runtimeContext := strings.TrimSpace(contract.RenderStartRuntimeContext(assembly))
	if runtimeContext == "" {
		return snapshot
	}
	if snapshot.Boundary != nil {
		boundary := *snapshot.Boundary
		boundary.UncachedTail = appendPromptSnapshotBlock(boundary.UncachedTail, runtimeContext)
		snapshot.Boundary = clonePromptBoundary(&boundary)
		return snapshot
	}
	snapshot.BaseInstructions = contract.AppendStartRuntimeContext(snapshot.BaseInstructions, assembly)
	return snapshot
}

func appendPromptSnapshotBlock(base, block string) string {
	base = strings.TrimSpace(base)
	block = strings.TrimSpace(block)
	if block == "" {
		return base
	}
	if base == "" {
		return block
	}
	if strings.Contains(base, block) {
		return base
	}
	return base + "\n\n" + block
}

func normalizePromptSnapshotContent(snapshot contract.PromptAssemblySnapshot) contract.PromptAssemblySnapshot {
	snapshot.DisplayName = strings.TrimSpace(snapshot.DisplayName)
	snapshot.BaseInstructions = strings.TrimSpace(snapshot.BaseInstructions)
	snapshot.Boundary = clonePromptBoundary(snapshot.Boundary)
	snapshot.DeveloperInstructions = strings.TrimSpace(snapshot.DeveloperInstructions)
	snapshot.Provider = strings.TrimSpace(snapshot.Provider)
	snapshot.Hash = strings.TrimSpace(snapshot.Hash)
	snapshot.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	return snapshot
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

func promptBoundaryBlank(boundary *dto.PromptAssemblyBoundary) bool {
	return boundary == nil ||
		(strings.TrimSpace(boundary.CachedPrefix) == "" && strings.TrimSpace(boundary.UncachedTail) == "")
}

func promptBoundaryCachedPrefix(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.CachedPrefix)
}

func promptBoundaryUncachedTail(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return strings.TrimSpace(boundary.UncachedTail)
}

func toStoredPromptBoundary(boundary *dto.PromptAssemblyBoundary) *promptBoundaryRecord {
	if promptBoundaryBlank(boundary) {
		return nil
	}
	return &promptBoundaryRecord{
		CachedPrefix: promptBoundaryCachedPrefix(boundary),
		UncachedTail: promptBoundaryUncachedTail(boundary),
	}
}

func fromStoredPromptBoundary(boundary *promptBoundaryRecord) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	return clonePromptBoundary(&dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	})
}

// promptSnapshotSectionMap 把 prompt section 列表整理成按名称索引的 map。
func promptSnapshotSectionMap(sections []contract.ResolvedPromptSection) map[string]string {
	if len(sections) == 0 {
		return nil
	}
	out := make(map[string]string, len(sections))
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		content := strings.TrimSpace(section.Content)
		if name != "" && content != "" {
			out[name] = content
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clonePromptSectionMap 复制 prompt section map，避免调用方修改缓存。
func clonePromptSectionMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func promptSnapshotHash(
	displayName, base, dev, provider string,
	boundary *dto.PromptAssemblyBoundary,
	sections map[string]string,
	generation uint64,
) string {
	h := sha256.New()
	for _, part := range promptSnapshotHashParts(
		displayName,
		base,
		dev,
		provider,
		boundary,
		sections,
		generation,
	) {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// promptSnapshotHashParts 按稳定顺序展开 snapshot hash 字段。
// section 内容和非零 generation 必须进入 hash，避免恢复时接受被篡改的动态段落。
func promptSnapshotHashParts(
	displayName, base, dev, provider string,
	boundary *dto.PromptAssemblyBoundary,
	sections map[string]string,
	generation uint64,
) []string {
	parts := []string{
		displayName,
		base,
		dev,
		provider,
		promptBoundaryCachedPrefix(boundary),
		promptBoundaryUncachedTail(boundary),
	}
	if generation != 0 {
		parts = append(parts, fmt.Sprintf("generation:%d", generation))
	}
	if len(sections) == 0 {
		return parts
	}
	names := make([]string, 0, len(sections))
	for name := range sections {
		names = append(names, strings.TrimSpace(name))
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, "section:"+name, sections[name])
	}
	return parts
}

func toStoredPromptSnapshot(snapshot contract.PromptAssemblySnapshot) promptSnapshotRecord {
	snapshot = normalizePromptSnapshotContent(snapshot)
	return promptSnapshotRecord{
		DisplayName:           snapshot.DisplayName,
		BaseInstructions:      snapshot.BaseInstructions,
		Boundary:              toStoredPromptBoundary(snapshot.Boundary),
		DeveloperInstructions: snapshot.DeveloperInstructions,
		Provider:              snapshot.Provider,
		Version:               snapshot.Version,
		Hash:                  snapshot.Hash,
		SectionSnapshot:       clonePromptSectionMap(snapshot.SectionSnapshot),
		Generation:            snapshot.Generation,
	}
}

// resolveStablePromptSnapshot 给 fork/recover 找一份可信的旧 start 提示。
// version、provider、hash 对不上就不要复用，避免把旧格式继续交给 provider。
func (s *service) resolveStablePromptSnapshot(
	ctx context.Context,
	threadID string,
	provider string,
	fallback contract.PromptAssemblySnapshot,
) (contract.PromptAssemblySnapshot, error) {
	stored, err := s.loadStoredPromptSnapshot(ctx, threadID)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, fmt.Errorf("load stable prompt snapshot for %q: %w", strings.TrimSpace(threadID), err)
	}
	if storedPromptSnapshotValid(stored, provider) {
		return stored, nil
	}
	if !promptSnapshotBlank(stored) && s.logger != nil {
		s.logger.Debug("thread: recomputing prompt snapshot due to hash/version mismatch",
			"thread_id", threadID, "stored_version", stored.Version)
	}
	if !promptSnapshotBlank(stored) {
		return contract.PromptAssemblySnapshot{}, invalidPromptSnapshotError(threadID)
	}
	fallback = normalizeCallerPromptSnapshot(fallback, provider)
	if !promptSnapshotBlank(fallback) {
		return fallback, nil
	}
	meta, err := s.requireThreadMeta(ctx, threadID)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	legacyAllowed, err := legacyPromptSnapshotMigrationAllowed(resumeState{
		PublicThreadID:    strings.TrimSpace(threadID),
		ConfigOverrideRaw: meta.ConfigOverride,
	})
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	if legacyAllowed {
		return ensureStartAssemblySnapshot(contract.StartAssembly{DisplayName: meta.Name}, provider).Snapshot, nil
	}
	return contract.PromptAssemblySnapshot{}, missingPromptSnapshotError(threadID)
}

// resolveResumePromptSnapshot 决定 resume 用哪份 prompt snapshot。
// 先用已保存的，再看调用方传入的，最后才按旧 thread 身份重建；这里不重新选 prompt。
func (s *service) resolveResumePromptSnapshot(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
) (contract.PromptAssemblySnapshot, error) {
	provider := strings.TrimSpace(util.FirstNonEmpty(req.Provider, state.Provider))
	stored, err := s.loadStoredPromptSnapshot(ctx, state.PublicThreadID)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, fmt.Errorf("load stored prompt snapshot for %q: %w", state.PublicThreadID, err)
	}
	if storedPromptSnapshotValid(stored, provider) {
		return stored, nil
	}
	if !promptSnapshotBlank(stored) {
		return contract.PromptAssemblySnapshot{}, invalidPromptSnapshotError(state.PublicThreadID)
	}
	legacyAllowed, err := legacyPromptSnapshotMigrationAllowed(state)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	if !legacyAllowed {
		return contract.PromptAssemblySnapshot{}, missingPromptSnapshotError(state.PublicThreadID)
	}
	caller := normalizeCallerPromptSnapshot(req.PromptSnapshot, provider)
	if !promptSnapshotBlank(caller) {
		return caller, nil
	}
	rebuilt, err := s.rebuildResumePromptSnapshot(ctx, state, provider)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	return rebuilt, nil
}

// legacyPromptSnapshotMigrationAllowed 只接受显式布尔开关放行旧线程的无 snapshot 迁移。
func legacyPromptSnapshotMigrationAllowed(state resumeState) (bool, error) {
	runtimeConfig := state.ConfigOverride.Runtime
	if len(runtimeConfig) == 0 && len(state.ConfigOverrideRaw) > 0 {
		stored, err := decodeStoredThreadConfig(state.ConfigOverrideRaw)
		if err != nil {
			return false, fmt.Errorf("decode legacy prompt snapshot migration gate: %w", err)
		}
		runtimeConfig = stored.Runtime
	}
	raw, ok := runtimeConfig[legacyPromptSnapshotMigrationRuntimeKey]
	if !ok {
		return false, nil
	}
	allowed, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("legacy prompt snapshot migration gate %q must be a boolean", legacyPromptSnapshotMigrationRuntimeKey)
	}
	return allowed, nil
}

func invalidPromptSnapshotError(threadID string) error {
	return fmt.Errorf("thread %q prompt snapshot is invalid", strings.TrimSpace(threadID))
}

func missingPromptSnapshotError(threadID string) error {
	return fmt.Errorf("thread %q prompt snapshot is missing; legacy prompt snapshot migration gate is required", strings.TrimSpace(threadID))
}

// rebuildResumePromptSnapshot 只给缺 snapshot 的旧线程补一次。
// 子 agent 的 parent/type/memory scope 缺了就报错，不生成近似 prompt。
func (s *service) rebuildResumePromptSnapshot(
	ctx context.Context,
	state resumeState,
	provider string,
) (contract.PromptAssemblySnapshot, error) {
	needsSnapshot, err := resumePromptSnapshotRequired(state)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	if !needsSnapshot {
		return contract.PromptAssemblySnapshot{}, nil
	}
	if s == nil || s.promptAssembly == nil {
		return contract.PromptAssemblySnapshot{}, errors.New("resume prompt assembly service is not configured")
	}
	req := StartRequest{
		Provider:          strings.TrimSpace(provider),
		CWD:               strings.TrimSpace(state.CWD),
		Model:             strings.TrimSpace(state.Model),
		ParentAgentID:     strings.TrimSpace(state.ParentAgentID),
		AgentType:         strings.TrimSpace(state.AgentType),
		AgentMemoryScope:  strings.TrimSpace(state.AgentMemoryScope),
		Name:              strings.TrimSpace(state.Prompt),
		PromptAssemblyRef: s.promptAssembly,
	}
	input, cleanupScratchpad, err := s.buildStartAssemblyInput(ctx, req, state.PublicThreadID)
	if cleanupScratchpad != nil {
		defer cleanupScratchpad()
	}
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	assembly, err := resolveStartPromptAssembly(ctx, req, input)
	if err != nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	assembly = ensureStartAssemblySnapshot(assembly, provider)
	snapshot := normalizeCallerPromptSnapshot(assembly.Snapshot, provider)
	if promptSnapshotBlank(snapshot) {
		return contract.PromptAssemblySnapshot{}, errors.New("resume prompt snapshot rebuild produced an empty snapshot")
	}
	return snapshot, nil
}

// resumePromptSnapshotRequired 判断 resume 是否必须重建 snapshot。
// 子 agent 的 memory scope 会影响 prompt；身份字段不全时不能继续。
func resumePromptSnapshotRequired(state resumeState) (bool, error) {
	parent := strings.TrimSpace(state.ParentAgentID)
	agentType := strings.TrimSpace(state.AgentType)
	scope := strings.TrimSpace(state.AgentMemoryScope)
	if parent == "" && agentType == "" && scope == "" {
		return false, nil
	}
	var missing []string
	if parent == "" {
		missing = append(missing, "parent agent id")
	}
	if agentType == "" {
		missing = append(missing, "agent type")
	}
	if scope == "" {
		missing = append(missing, "agent memory scope")
	}
	if len(missing) != 0 {
		return false, fmt.Errorf("resume prompt snapshot identity is incomplete: missing %s", strings.Join(missing, ", "))
	}
	return true, nil
}

func fromStoredPromptSnapshot(snapshot *promptSnapshotRecord) contract.PromptAssemblySnapshot {
	if snapshot == nil {
		return contract.PromptAssemblySnapshot{}
	}
	return contract.PromptAssemblySnapshot{
		DisplayName:           strings.TrimSpace(snapshot.DisplayName),
		BaseInstructions:      strings.TrimSpace(snapshot.BaseInstructions),
		Boundary:              fromStoredPromptBoundary(snapshot.Boundary),
		DeveloperInstructions: strings.TrimSpace(snapshot.DeveloperInstructions),
		Provider:              strings.TrimSpace(snapshot.Provider),
		Version:               snapshot.Version,
		Hash:                  strings.TrimSpace(snapshot.Hash),
		SectionSnapshot:       clonePromptSectionMap(snapshot.SectionSnapshot),
		Generation:            snapshot.Generation,
	}
}

// storedPromptSnapshotValid 判断存储中的 prompt 快照是否仍可复用。
func storedPromptSnapshotValid(snapshot contract.PromptAssemblySnapshot, provider string) bool {
	snapshot = normalizePromptSnapshotContent(snapshot)
	if promptSnapshotBlank(snapshot) || snapshot.Version == 0 || snapshot.Provider == "" || snapshot.Hash == "" {
		return false
	}
	if provider = strings.TrimSpace(provider); provider != "" && snapshot.Provider != provider {
		return false
	}
	return snapshot.Hash == promptSnapshotHash(
		snapshot.DisplayName,
		snapshot.BaseInstructions,
		snapshot.DeveloperInstructions,
		snapshot.Provider,
		snapshot.Boundary,
		snapshot.SectionSnapshot,
		snapshot.Generation,
	)
}

func normalizeCallerPromptSnapshot(snapshot contract.PromptAssemblySnapshot, provider string) contract.PromptAssemblySnapshot {
	snapshot = normalizePromptSnapshotContent(snapshot)
	if promptSnapshotBlank(snapshot) {
		return contract.PromptAssemblySnapshot{}
	}
	if snapshot.Provider == "" {
		snapshot.Provider = strings.TrimSpace(provider)
	}
	if snapshot.Version == 0 {
		snapshot.Version = contract.PromptAssemblySnapshotVersion
	}
	if snapshot.Hash == "" {
		snapshot.Hash = promptSnapshotHash(
			snapshot.DisplayName,
			snapshot.BaseInstructions,
			snapshot.DeveloperInstructions,
			snapshot.Provider,
			snapshot.Boundary,
			snapshot.SectionSnapshot,
			snapshot.Generation,
		)
	}
	return snapshot
}

// promptSnapshotBlank 判断 prompt 快照是否为空。
func promptSnapshotBlank(snapshot contract.PromptAssemblySnapshot) bool {
	snapshot = normalizePromptSnapshotContent(snapshot)
	return snapshot.DisplayName == "" &&
		snapshot.BaseInstructions == "" &&
		promptBoundaryBlank(snapshot.Boundary) &&
		snapshot.DeveloperInstructions == "" &&
		snapshot.Provider == "" &&
		snapshot.Version == 0 &&
		snapshot.Hash == "" &&
		len(snapshot.SectionSnapshot) == 0 &&
		snapshot.Generation == 0
}
