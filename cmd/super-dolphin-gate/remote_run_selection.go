package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func resolveRemoteScenario(options remoteRunOptions) (string, gatecontract.Profile, error) {
	scenario := options.Scenario
	if scenario == "" {
		return "", "", errors.New("remote CI scenario is required")
	}
	profiles := map[string]gatecontract.Profile{
		"commit": gatecontract.ProfileLocalFast,
		"push":   gatecontract.ProfilePush,
		"full":   gatecontract.ProfileRelease,
		"test":   gatecontract.ProfileLocalFast,
	}
	profile, ok := profiles[scenario]
	if !ok {
		return "", "", fmt.Errorf("unsupported remote CI scenario %q", scenario)
	}
	if err := validateRemoteScenarioOptions(options, scenario); err != nil {
		return "", "", err
	}
	return scenario, profile, nil
}

// validateRemoteScenarioOptions 校验测试选择器与显式场景一致。
func validateRemoteScenarioOptions(
	options remoteRunOptions,
	scenario string,
) error {
	if scenario == "test" && len(options.Tests) == 0 {
		return errors.New("remote CI test scenario requires at least one --test selector")
	}
	if scenario != "test" && len(options.Tests) != 0 {
		return errors.New("--test selectors are only valid with scenario test")
	}
	return nil
}

// selectRemoteTests 只从精确源树 inventory 中选择唯一且有效的 workload。
func selectRemoteTests(
	inventory gatecontract.WorkloadInventory,
	selectors []string,
) (gatecontract.WorkloadInventory, error) {
	goPackages := remoteTestSelectorSet(inventory.GoPackages)
	frontendTests := remoteTestSelectorSet(inventory.FrontendFullTests)
	selected := gatecontract.WorkloadInventory{}
	seen := make(map[string]struct{}, len(selectors))
	exactGoPackages := make(map[string]struct{})
	for _, selector := range selectors {
		if err := validateRemoteTestSelector(selector, seen); err != nil {
			return gatecontract.WorkloadInventory{}, err
		}
		if err := appendRemoteTestSelector(
			&selected,
			goPackages,
			frontendTests,
			exactGoPackages,
			selector,
		); err != nil {
			return gatecontract.WorkloadInventory{}, err
		}
	}
	if err := validateRemoteTestSelectorOverlap(selected.GoPackages, exactGoPackages); err != nil {
		return gatecontract.WorkloadInventory{}, err
	}
	return selected, nil
}

// remoteTestSelectorSet 将精确源树清单投影为只读成员集合。
func remoteTestSelectorSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// validateRemoteTestSelector 拒绝歧义、控制字符和重复选择器。
func validateRemoteTestSelector(selector string, seen map[string]struct{}) error {
	if selector == "" || strings.TrimSpace(selector) != selector || strings.ContainsAny(selector, "\x00\r\n,") {
		return fmt.Errorf("test selector %q is invalid", selector)
	}
	if _, duplicate := seen[selector]; duplicate {
		return fmt.Errorf("test selector %q is duplicated", selector)
	}
	seen[selector] = struct{}{}
	return nil
}

// appendRemoteTestSelector 解析一种选择器并追加其唯一 canonical 目标。
func appendRemoteTestSelector(
	selected *gatecontract.WorkloadInventory,
	goPackages map[string]struct{},
	frontendTests map[string]struct{},
	exactGoPackages map[string]struct{},
	selector string,
) error {
	if _, ok := goPackages[selector]; ok {
		selected.GoPackages = append(selected.GoPackages, selector)
		return nil
	}
	if packageTarget, targetName, exact := strings.Cut(selector, "#"); exact {
		return appendExactRemoteGoTarget(selected, goPackages, exactGoPackages, selector, packageTarget, targetName)
	}
	frontend := strings.TrimPrefix(selector, "frontend-app/")
	if _, ok := frontendTests[frontend]; ok {
		selected.FrontendFullTests = append(selected.FrontendFullTests, frontend)
		return nil
	}
	return fmt.Errorf("test selector %q is not present in the exact source tree", selector)
}

// appendExactRemoteGoTarget 校验并追加一个顶层测试或 benchmark 目标。
func appendExactRemoteGoTarget(
	selected *gatecontract.WorkloadInventory,
	goPackages map[string]struct{},
	exactGoPackages map[string]struct{},
	selector string,
	packageTarget string,
	targetName string,
) error {
	if packageTarget == "" || targetName == "" || strings.Contains(targetName, "#") {
		return fmt.Errorf("test selector %q is invalid", selector)
	}
	if _, ok := goPackages[packageTarget]; !ok {
		return fmt.Errorf("test selector %q package is not present in the exact source tree", selector)
	}
	target := gatecontract.GoTestTarget{Package: packageTarget, Name: targetName}
	if strings.HasPrefix(targetName, "Benchmark") {
		if _, err := gatecontract.NewGoBenchmarkWorkload(
			gatecontract.GateIDBackendTestWithGuard,
			packageTarget,
			targetName,
			1,
		); err != nil {
			return fmt.Errorf("test selector %q: %w", selector, err)
		}
		selected.GoBenchmarks = append(selected.GoBenchmarks, target)
	} else {
		if _, err := gatecontract.NewGoTestWorkload(
			gatecontract.GateIDBackendTestWithGuard,
			packageTarget,
			targetName,
			1,
		); err != nil {
			return fmt.Errorf("test selector %q: %w", selector, err)
		}
		selected.GoTests = append(selected.GoTests, target)
	}
	exactGoPackages[packageTarget] = struct{}{}
	return nil
}

// validateRemoteTestSelectorOverlap 阻止整包与包内精确目标在同一请求中重复执行。
func validateRemoteTestSelectorOverlap(
	goPackages []string,
	exactGoPackages map[string]struct{},
) error {
	for _, packageTarget := range goPackages {
		if _, overlaps := exactGoPackages[packageTarget]; overlaps {
			return fmt.Errorf(
				"Go package selector %q overlaps an exact test or benchmark selector",
				packageTarget,
			)
		}
	}
	return nil
}

const remoteCalibrationWorkerTimeout = 10 * time.Minute

// remoteWorkerTimeout 为 worker 选择执行时限；校准三场景共用 canonical 10 分钟，normal 仍按 profile 保留安全上限。
func remoteWorkerTimeout(profile gatecontract.Profile, calibration bool) (time.Duration, error) {
	if err := profile.Validate(); err != nil {
		return 0, err
	}
	if calibration {
		return remoteCalibrationWorkerTimeout, nil
	}
	return remoteProfileDeadline(profile)
}

// remoteProfileDeadline 返回 worker 的安全上限；100 秒只作为分片优化目标。
func remoteProfileDeadline(profile gatecontract.Profile) (time.Duration, error) {
	if err := profile.Validate(); err != nil {
		return 0, err
	}
	switch profile {
	case gatecontract.ProfileLocalFast, gatecontract.ProfilePush:
		return 10 * time.Minute, nil
	case gatecontract.ProfileRelease:
		return 30 * time.Minute, nil
	default:
		return 0, fmt.Errorf("unsupported remote CI profile %q", profile)
	}
}

func remoteContainerDeadline(workerTimeout time.Duration) (time.Duration, error) {
	if err := gatecontract.ValidateExecutorWorkloadTimeout(workerTimeout); err != nil {
		return 0, err
	}
	return workerTimeout + remoteWorkerSetupAllowance + remoteContainerReportAllowance, nil
}
