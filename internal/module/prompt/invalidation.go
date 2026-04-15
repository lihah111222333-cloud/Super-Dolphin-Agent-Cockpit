package prompt

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type SectionInvalidator = contract.SectionInvalidator

func AsSectionInvalidator(svc Service) SectionInvalidator {
	return svc
}

func (s *service) InvalidateSections(reason InvalidateReason, names ...string) uint64 {
	generation := s.cache.InvalidateSections(names...)
	s.notifySectionInvalidationProviders(reason, names)
	if s.logger != nil {
		s.logger.Debug("prompt sections invalidated", "reason", reason, "sections", compactSectionNames(names), "generation", generation)
	}
	return generation
}

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
