package codexapp

import (
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// setPromptSnapshot 记录 session 当前拥有的 start/resume prompt 快照。
// map/pointer 字段会复制，避免调用方在 session 建立后篡改 recovery 使用的内容。
func (s *session) setPromptSnapshot(snapshot contract.PromptAssemblySnapshot) {
	if s == nil {
		return
	}
	snapshot = cloneCodexPromptSnapshot(snapshot)
	s.mu.Lock()
	s.promptSnapshot = snapshot
	s.mu.Unlock()
}

// setNativeContext 记录 start/resume 已验证的 provider-native context。
// context 只含身份和能力状态，不携带 prompt、memory 或 skill 正文。
func (s *session) setNativeContext(ctx contract.ProviderNativeContext) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.nativeContext = ctx
	s.mu.Unlock()
}

// nativeContextSnapshot 返回 recovery 使用的 provider-native context 副本。
func (s *session) nativeContextSnapshot() contract.ProviderNativeContext {
	if s == nil {
		return contract.ProviderNativeContext{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nativeContext
}

// validateRecoveryNativeContext 在 reconnect 前复核 start/resume 时记录的
// provider-native 身份和 canonical CWD。旧的手工构造 session 没有 context，
// 仍由 recoveryResumeCWD 执行路径安全校验；生产 session 始终带 context。
func (s *session) validateRecoveryNativeContext() error {
	ctx := s.nativeContextSnapshot()
	if ctx.SchemaVersion == 0 {
		return nil
	}
	if err := providershared.ValidateProviderNativeContextForProvider(ctx, "codex"); err != nil {
		return fmt.Errorf("codexapp: recovery native context: %w", err)
	}
	rawCWD := strings.TrimSpace(s.runtimeConfigString(contract.RuntimeConfigCWD().Canonical))
	if rawCWD == "" {
		rawCWD = strings.TrimSpace(s.runtimeConfigString("cwd"))
	}
	canonicalCWD, err := contract.CanonicalizeProviderNativeCWD(rawCWD)
	if err != nil {
		return fmt.Errorf("codexapp: recovery native context cwd: %w", err)
	}
	if canonicalCWD != ctx.CWD {
		return fmt.Errorf("codexapp: recovery native context cwd mismatch")
	}
	return nil
}

// promptSnapshotSnapshot 返回 recovery 使用的快照副本。
func (s *session) promptSnapshotSnapshot() contract.PromptAssemblySnapshot {
	if s == nil {
		return contract.PromptAssemblySnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCodexPromptSnapshot(s.promptSnapshot)
}

// validateRecoveryPromptSnapshot 在 reconnect 发出 thread/resume 前重新检查 owner、版本和旧 memory 槽位。
// 空快照表示早期 direct-start 调用方没有提供可持久化 snapshot，保持现有 runtime base 路径；
// 一旦携带任意 snapshot 元数据，就必须满足完整 resume contract。
func (s *session) validateRecoveryPromptSnapshot() error {
	snapshot := s.promptSnapshotSnapshot()
	if codexPromptSnapshotBlank(snapshot) {
		return nil
	}
	if err := contract.ValidateResumePromptSnapshotForProvider(snapshot, "codex"); err != nil {
		return fmt.Errorf("codexapp: recovery prompt snapshot: %w", err)
	}
	return nil
}

func cloneCodexPromptSnapshot(snapshot contract.PromptAssemblySnapshot) contract.PromptAssemblySnapshot {
	cloned := snapshot
	if snapshot.Boundary != nil {
		boundary := *snapshot.Boundary
		cloned.Boundary = &boundary
	}
	if len(snapshot.SectionSnapshot) > 0 {
		cloned.SectionSnapshot = make(map[string]string, len(snapshot.SectionSnapshot))
		for key, value := range snapshot.SectionSnapshot {
			cloned.SectionSnapshot[key] = value
		}
	}
	return cloned
}

// codexPromptSnapshotBlank 判断快照是否完全为空；部分字段存在也必须进入完整校验。
func codexPromptSnapshotBlank(snapshot contract.PromptAssemblySnapshot) bool {
	return strings.TrimSpace(snapshot.DisplayName) == "" &&
		strings.TrimSpace(snapshot.BaseInstructions) == "" &&
		codexPromptBoundaryBlank(snapshot.Boundary) &&
		strings.TrimSpace(snapshot.DeveloperInstructions) == "" &&
		strings.TrimSpace(snapshot.Provider) == "" &&
		snapshot.Version == 0 &&
		strings.TrimSpace(snapshot.Hash) == "" &&
		len(snapshot.SectionSnapshot) == 0 &&
		snapshot.Generation == 0
}

func codexPromptBoundaryBlank(boundary *provider.PromptAssemblyBoundary) bool {
	return boundary == nil ||
		(strings.TrimSpace(boundary.CachedPrefix) == "" && strings.TrimSpace(boundary.UncachedTail) == "")
}
