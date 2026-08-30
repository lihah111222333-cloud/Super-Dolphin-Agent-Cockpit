package prompt

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// SectionInvalidator 是 prompt section 缓存失效能力的本包兼容别名。
type SectionInvalidator = contract.SectionInvalidator

// AsSectionInvalidator 将聚合 Service 暴露为 section 失效契约。
func AsSectionInvalidator(svc Service) SectionInvalidator {
	return svc
}

// InvalidateSections 清空指定动态 section 缓存，并通知支持失效回调的 provider。
func (s *service) InvalidateSections(reason InvalidateReason, names ...string) uint64 {
	generation := s.cache.InvalidateSections(names...)
	s.notifySectionInvalidationProviders(reason, names)
	if s.logger != nil {
		s.logger.Debug("prompt sections invalidated", "reason", reason, "sections", compactSectionNames(names), "generation", generation)
	}
	return generation
}

// notifySectionInvalidationProviders 对去重后的 section 名称触发 provider 失效回调。
func (s *service) notifySectionInvalidationProviders(reason InvalidateReason, names []string) {
	if len(names) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		s.dynamicMu.RLock()
		provider := s.dynamic[key]
		s.dynamicMu.RUnlock()
		aware, ok := provider.(InvalidationAwareProvider)
		if ok {
			aware.OnPromptInvalidate(reason)
		}
	}
}

// compactSectionNames 清理并去重 section 名称，供日志输出使用。
func compactSectionNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
