package main

import (
	"slices"
	"strings"

	gateexecutor "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// gatePlanForScope 仅在显式推送范围中追加高成本风险门禁，保持提交阶段轻量。
func gatePlanForScope(files []string, pushGates bool) (gatePlan, error) {
	plan, err := buildGatePlan(files)
	if err != nil || !pushGates {
		return plan, err
	}
	if len(affectedNilnessPackages(plan)) > 0 {
		plan.RequiredGates = appendOrderedGate(plan.RequiredGates, "backend:nilness")
	}
	if slices.Contains(plan.RequiredGates, "backend:test_with_guard") && !requiresFullArchtest(plan.ChangedFiles) {
		plan.RequiredGates = appendOrderedGate(plan.RequiredGates, "backend:archtest")
	}
	if len(affectedRacePackagesForPlan(plan)) > 0 {
		plan.RequiredGates = appendOrderedGate(plan.RequiredGates, "backend:race")
	}
	if slices.ContainsFunc(plan.ChangedFiles, frontendBuildRelevant) {
		plan.RequiredGates = appendOrderedGate(plan.RequiredGates, "frontend:embed-verify")
	}
	if slices.ContainsFunc(plan.ChangedFiles, frontendPerformanceRelevant) {
		plan.RequiredGates = appendOrderedGate(plan.RequiredGates, "frontend:performance-verify")
	}
	plan.RequiredGates = orderGateNames(plan.RequiredGates)
	return plan, nil
}

func removeGate(gates []string, target string) []string {
	result := gates[:0]
	for _, gate := range gates {
		if gate != target {
			result = append(result, gate)
		}
	}
	return result
}

func appendOrderedGate(gates []string, gate string) []string {
	if slices.Contains(gates, gate) {
		return gates
	}
	return append(gates, gate)
}

func orderGateNames(gates []string) []string {
	set := make(map[string]bool, len(gates))
	for _, gate := range gates {
		set[gate] = true
	}
	return orderedGates(set)
}

func affectedBackendGoPackages(files []string) []string {
	packages := map[string]bool{}
	for _, file := range files {
		if strings.HasPrefix(file, "testdata/") || strings.Contains(file, "/testdata/") {
			continue
		}
		if pkg, ok := changedGoPackage(file); ok {
			packages[pkg] = true
		}
	}
	return sortedKeys(packages)
}

// affectedNilnessPackages 对模块清单变更使用计划的完整受影响包，否则保持直接变更包范围。
func affectedNilnessPackages(plan gatePlan) []string {
	if hasGoModuleChange(plan.ChangedFiles) {
		return excludeTestdataPackages(plan.AffectedGoPackages)
	}
	return affectedBackendGoPackages(plan.ChangedFiles)
}

func excludeTestdataPackages(packages []string) []string {
	result := map[string]bool{}
	for _, pkg := range packages {
		if strings.HasPrefix(pkg, "./testdata/") || strings.Contains(pkg, "/testdata/") {
			continue
		}
		result[pkg] = true
	}
	return sortedKeys(result)
}

func hasGoModuleChange(files []string) bool {
	return slices.ContainsFunc(files, goModuleFile)
}

// affectedRacePackages 只返回已登记并发生产面的受影响 Go 包。
func affectedRacePackages(files []string) []string {
	packages := map[string]bool{}
	prefixes := gateexecutor.RaceSensitivePathPrefixes()
	for _, file := range files {
		if !hasAnyPrefix(file, prefixes) {
			continue
		}
		if pkg, ok := changedGoPackage(file); ok {
			packages[pkg] = true
		}
	}
	return sortedKeys(packages)
}

// affectedRacePackagesForPlan 在模块清单变化时覆盖计划内全部登记并发包。
func affectedRacePackagesForPlan(plan gatePlan) []string {
	if !hasGoModuleChange(plan.ChangedFiles) {
		return affectedRacePackages(plan.ChangedFiles)
	}
	return gateexecutor.RaceSensitivePackagePatterns()
}

func hasAnyPrefix(value string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(prefix string) bool {
		return strings.HasPrefix(value, prefix)
	})
}
