package archtest

// cloneBackendBoundaryRegistry 深拷贝 canonical registry，避免测试或调用方修改共享切片。
func cloneBackendBoundaryRegistry(registry BackendBoundaryRegistry) BackendBoundaryRegistry {
	cloned := BackendBoundaryRegistry{
		Owners:          make([]BackendBoundaryOwner, len(registry.Owners)),
		Rules:           make([]BackendBoundaryRule, len(registry.Rules)),
		Guards:          make([]BackendBoundaryGuard, len(registry.Guards)),
		Surfaces:        make([]BackendBoundarySurface, len(registry.Surfaces)),
		canonicalSource: registry.canonicalSource,
	}
	for i, owner := range registry.Owners {
		cloned.Owners[i] = BackendBoundaryOwner{ID: owner.ID, FilePatterns: append([]string(nil), owner.FilePatterns...), Reason: owner.Reason}
	}
	for i, rule := range registry.Rules {
		cloned.Rules[i] = cloneBackendBoundaryRule(rule)
	}
	for i, guard := range registry.Guards {
		cloned.Guards[i] = BackendBoundaryGuard{
			ID:        guard.ID,
			File:      guard.File,
			TestNames: append([]string(nil), guard.TestNames...),
			BuildTags: append([]string(nil), guard.BuildTags...),
			AppliesTo: append([]BoundarySurfaceID(nil), guard.AppliesTo...),
			Reason:    guard.Reason,
		}
	}
	for i, surface := range registry.Surfaces {
		cloned.Surfaces[i] = BackendBoundarySurface{Path: surface.Path, RuleIDs: append([]BoundaryRuleID(nil), surface.RuleIDs...), GuardIDs: append([]BoundaryGuardID(nil), surface.GuardIDs...), Reason: surface.Reason}
	}
	return cloned
}

func cloneBackendBoundaryRule(rule BackendBoundaryRule) BackendBoundaryRule {
	cloned := rule
	cloned.FilePatterns = append([]string(nil), rule.FilePatterns...)
	cloned.Allow = append([]BoundaryImportPolicy(nil), rule.Allow...)
	cloned.Deny = append([]BoundaryImportPolicy(nil), rule.Deny...)
	cloned.ScopeAllow = append([]BoundaryFilePolicy(nil), rule.ScopeAllow...)
	cloned.Exceptions = append([]BoundaryException(nil), rule.Exceptions...)
	cloned.DependencyPackages = append([]string(nil), rule.DependencyPackages...)
	return cloned
}
