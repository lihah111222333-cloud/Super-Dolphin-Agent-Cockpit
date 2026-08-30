package claudecli

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// requireProviderNativeSkillsLocked 在持锁状态下拒绝缺少 provider-owned 能力证据的显式技能选择。
// schema=0 仅为兼容没有 native context 的旧手工 session；生产 start/resume 始终记录非零 context。
func (s *session) requireProviderNativeSkillsLocked(skills []dto.SkillRef) error {
	if len(skills) == 0 || s == nil || s.nativeContext.SchemaVersion == 0 {
		return nil
	}
	if err := s.nativeContext.RequireProviderFeature(contract.ProviderNativeFeatureSkills); err != nil {
		return fmt.Errorf("claudecli: explicit skills require provider-native capability: %w", err)
	}
	return nil
}

// requireProviderNativeSkills 为锁外组装 payload 的 steer 执行同一能力闸门。
// 先在锁内复制不可变 context，避免 session close/restart 与能力检查产生竞态。
func (s *session) requireProviderNativeSkills(skills []dto.SkillRef) error {
	if len(skills) == 0 || s == nil {
		return nil
	}
	s.mu.Lock()
	ctx := s.nativeContext
	s.mu.Unlock()
	if ctx.SchemaVersion == 0 {
		return nil
	}
	if err := ctx.RequireProviderFeature(contract.ProviderNativeFeatureSkills); err != nil {
		return fmt.Errorf("claudecli: explicit skills require provider-native capability: %w", err)
	}
	return nil
}
