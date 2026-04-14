package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func ensureStartAssemblySnapshot(assembly contract.StartAssembly, provider string) contract.StartAssembly {
	assembly.DisplayName = normalizeStartDisplayName(strings.TrimSpace(assembly.DisplayName))
	assembly.BaseInstructions = strings.TrimSpace(assembly.BaseInstructions)
	assembly.DeveloperInstructions = strings.TrimSpace(assembly.DeveloperInstructions)
	snapshot := normalizePromptSnapshotContent(assembly.Snapshot)
	if snapshot.DisplayName == "" {
		snapshot.DisplayName = assembly.DisplayName
	}
	if snapshot.BaseInstructions == "" {
		snapshot.BaseInstructions = assembly.BaseInstructions
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
	snapshot.Hash = promptSnapshotHash(snapshot.DisplayName, snapshot.BaseInstructions, snapshot.DeveloperInstructions, snapshot.Provider)
	assembly.Snapshot = snapshot
	return assembly
}

func normalizePromptSnapshotContent(snapshot contract.PromptAssemblySnapshot) contract.PromptAssemblySnapshot {
	snapshot.DisplayName = strings.TrimSpace(snapshot.DisplayName)
	snapshot.BaseInstructions = strings.TrimSpace(snapshot.BaseInstructions)
	snapshot.DeveloperInstructions = strings.TrimSpace(snapshot.DeveloperInstructions)
	snapshot.Provider = strings.TrimSpace(snapshot.Provider)
	snapshot.Hash = strings.TrimSpace(snapshot.Hash)
	snapshot.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	return snapshot
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

func promptSnapshotHash(displayName, base, dev, provider string) string {
	h := sha256.New()
	for _, part := range []string{displayName, base, dev, provider} {
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
		if !promptSnapshotBlank(stored) && s.logger != nil {
			s.logger.Warn("thread: ignore incompatible stored prompt snapshot", "thread_id", threadID)
		}
	} else if s.logger != nil {
		s.logger.Warn("thread: load stored prompt snapshot failed", "thread_id", threadID, "error", err)
	}
	return normalizeCallerPromptSnapshot(fallback, provider)
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
	return snapshot.Hash == promptSnapshotHash(snapshot.DisplayName, snapshot.BaseInstructions, snapshot.DeveloperInstructions, snapshot.Provider)
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
		snapshot.Hash = promptSnapshotHash(snapshot.DisplayName, snapshot.BaseInstructions, snapshot.DeveloperInstructions, snapshot.Provider)
	}
	return snapshot
}

func promptSnapshotBlank(snapshot contract.PromptAssemblySnapshot) bool {
	snapshot = normalizePromptSnapshotContent(snapshot)
	return snapshot.DisplayName == "" &&
		snapshot.BaseInstructions == "" &&
		snapshot.DeveloperInstructions == "" &&
		snapshot.Provider == "" &&
		snapshot.Version == 0 &&
		snapshot.Hash == "" &&
		len(snapshot.SectionSnapshot) == 0 &&
		snapshot.Generation == 0
}
