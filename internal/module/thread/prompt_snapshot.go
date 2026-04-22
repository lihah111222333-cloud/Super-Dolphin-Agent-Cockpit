package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

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
	snapshot.Hash = promptSnapshotHash(
		snapshot.DisplayName,
		snapshot.BaseInstructions,
		snapshot.DeveloperInstructions,
		snapshot.Provider,
		snapshot.Boundary,
	)
	assembly.Boundary = clonePromptBoundary(snapshot.Boundary)
	assembly.Snapshot = snapshot
	return assembly
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

func toStoredPromptBoundary(boundary *dto.PromptAssemblyBoundary) *threadstore.PromptBoundary {
	if promptBoundaryBlank(boundary) {
		return nil
	}
	return &threadstore.PromptBoundary{
		CachedPrefix: promptBoundaryCachedPrefix(boundary),
		UncachedTail: promptBoundaryUncachedTail(boundary),
	}
}

func fromStoredPromptBoundary(boundary *threadstore.PromptBoundary) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	return clonePromptBoundary(&dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	})
}

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
) string {
	h := sha256.New()
	for _, part := range []string{
		displayName,
		base,
		dev,
		provider,
		promptBoundaryCachedPrefix(boundary),
		promptBoundaryUncachedTail(boundary),
	} {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *service) savePromptSnapshot(ctx context.Context, threadID string, assembly contract.StartAssembly) error {
	if s == nil || s.threadStore == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	if promptSnapshotBlank(assembly.Snapshot) {
		return nil
	}
	return s.threadStore.SavePromptSnapshot(ctx, strings.TrimSpace(threadID), toStoredPromptSnapshot(assembly.Snapshot))
}

func toStoredPromptSnapshot(snapshot contract.PromptAssemblySnapshot) threadstore.PromptSnapshot {
	snapshot = normalizePromptSnapshotContent(snapshot)
	return threadstore.PromptSnapshot{
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

func (s *service) resolveStablePromptSnapshot(
	ctx context.Context,
	threadID string,
	provider string,
	fallback contract.PromptAssemblySnapshot,
) contract.PromptAssemblySnapshot {
	if stored, err := s.loadStoredPromptSnapshot(ctx, threadID); err == nil {
		if storedPromptSnapshotValid(stored, provider) {
			return stored
		}
		// Phase 3 parity decision: when the stored snapshot does not match the
		// current hash (e.g. SnapshotVersion bumped from v1 to v2, or section
		// content changed), silently re-compute with a debug log. Warn level
		// is reserved for actual storage failures below.
		if !promptSnapshotBlank(stored) && s.logger != nil {
			s.logger.Debug("thread: recomputing prompt snapshot due to hash/version mismatch",
				"thread_id", threadID, "stored_version", stored.Version)
		}
	} else if s.logger != nil {
		s.logger.Warn("thread: load stored prompt snapshot failed", "thread_id", threadID, "error", err)
	}
	return normalizeCallerPromptSnapshot(fallback, provider)
}

func (s *service) resolveResumePromptSnapshot(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
) contract.PromptAssemblySnapshot {
	provider := strings.TrimSpace(shared.FirstNonEmpty(req.Provider, state.Provider))
	if stored, ok := s.preferredStoredPromptSnapshot(ctx, state.PublicThreadID, provider); ok {
		return stored
	}
	caller := normalizeCallerPromptSnapshot(req.PromptSnapshot, provider)
	if !promptSnapshotBlank(caller) {
		return caller
	}
	rebuilt, err := s.rebuildResumePromptSnapshot(ctx, state, provider)
	if err != nil {
		s.logResumePromptRebuildFailure(state, err)
		return contract.PromptAssemblySnapshot{}
	}
	s.logResumePromptRebuilt(state, rebuilt)
	return rebuilt
}

func (s *service) rebuildResumePromptSnapshot(
	ctx context.Context,
	state resumeState,
	provider string,
) (contract.PromptAssemblySnapshot, error) {
	if s == nil || s.promptAssembly == nil {
		return contract.PromptAssemblySnapshot{}, nil
	}
	if strings.TrimSpace(state.ParentAgentID) == "" ||
		strings.TrimSpace(state.AgentType) == "" ||
		strings.TrimSpace(state.AgentMemoryScope) == "" {
		return contract.PromptAssemblySnapshot{}, nil
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
	input, cleanupScratchpad, err := s.buildStartAssemblyInput(req, state.PublicThreadID)
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
	return normalizeCallerPromptSnapshot(assembly.Snapshot, provider), nil
}

func (s *service) preferredStoredPromptSnapshot(
	ctx context.Context,
	threadID, provider string,
) (contract.PromptAssemblySnapshot, bool) {
	stored, err := s.loadStoredPromptSnapshot(ctx, threadID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("thread: load stored prompt snapshot failed", "thread_id", threadID, "error", err)
		}
		return contract.PromptAssemblySnapshot{}, false
	}
	if storedPromptSnapshotValid(stored, provider) {
		return stored, true
	}
	// Phase 3 parity decision: hash/version mismatch triggers recompute via
	// rebuildResumePromptSnapshot; downgrade to debug since this is the
	// expected path after SnapshotVersion bumps.
	if !promptSnapshotBlank(stored) && s.logger != nil {
		s.logger.Debug("thread: recomputing prompt snapshot on resume due to hash/version mismatch",
			"thread_id", threadID, "stored_version", stored.Version)
	}
	return contract.PromptAssemblySnapshot{}, false
}

func (s *service) logResumePromptRebuildFailure(state resumeState, err error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("thread: rebuild resume prompt snapshot failed",
		"thread_id", state.PublicThreadID,
		"agent_type", state.AgentType,
		"agent_memory_scope", state.AgentMemoryScope,
		"error", err,
	)
}

func (s *service) logResumePromptRebuilt(state resumeState, snapshot contract.PromptAssemblySnapshot) {
	if s == nil || s.logger == nil || promptSnapshotBlank(snapshot) {
		return
	}
	s.logger.Info("thread: rebuilt resume prompt snapshot from agent identity metadata",
		"thread_id", state.PublicThreadID,
		"agent_type", state.AgentType,
		"agent_memory_scope", state.AgentMemoryScope,
	)
}

func (s *service) loadStoredPromptSnapshot(ctx context.Context, threadID string) (contract.PromptAssemblySnapshot, error) {
	if s == nil || s.threadStore == nil || strings.TrimSpace(threadID) == "" {
		return contract.PromptAssemblySnapshot{}, nil
	}
	snapshot, err := s.threadStore.LoadPromptSnapshot(ctx, strings.TrimSpace(threadID))
	if err != nil || snapshot == nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	return fromStoredPromptSnapshot(snapshot), nil
}

func fromStoredPromptSnapshot(snapshot *threadstore.PromptSnapshot) contract.PromptAssemblySnapshot {
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
		)
	}
	return snapshot
}

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
