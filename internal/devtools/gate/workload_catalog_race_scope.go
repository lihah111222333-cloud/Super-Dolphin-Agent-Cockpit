package gate

import (
	"errors"
	"slices"
)

// raceGoTestTargets 从 inventory 的 race 清单中移除 normal-only 静态守卫。
func raceGoTestTargets(inventory WorkloadInventory) []GoTestTarget {
	excluded := raceExcludedStaticGoTestTargets()
	selected := make([]GoTestTarget, 0, len(inventory.GoRaceTests))
	for _, target := range inventory.GoRaceTests {
		if slices.Contains(excluded, target) {
			continue
		}
		selected = append(selected, target)
	}
	return selected
}

// prepareRaceGoTestInputs 过滤 normal-only 目标并拒绝静默回退到整包 race。
func prepareRaceGoTestInputs(packages []string, inventory WorkloadInventory) ([]string, []GoTestTarget, error) {
	tests := raceGoTestTargets(inventory)
	packages = removeRaceExcludedOnlyAtomicPackages(packages, inventory.GoRaceTests, tests)
	if len(packages) == 0 && len(tests) == 0 && len(inventory.GoRaceTests) > 0 {
		return nil, nil, errors.New("race Go inventory contains no runnable tests after normal-only exclusions")
	}
	return packages, tests, nil
}

// encodeAtomicGoTestTargets 将原子包中的顶层测试编码为稳定 selector。
func encodeAtomicGoTestTargets(tests []GoTestTarget, atomicPackages []string) ([]string, error) {
	encoded := make([]string, 0, len(tests))
	for _, test := range tests {
		if !slices.Contains(atomicPackages, test.Package) {
			continue
		}
		target, err := encodeGoTestTarget(test)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, target)
	}
	return encoded, nil
}

// filterAtomicGoPackages 删除已由精确顶层测试覆盖的整包目标。
func filterAtomicGoPackages(packages, atomicPackages []string) []string {
	filtered := make([]string, 0, len(packages)-len(atomicPackages))
	for _, packageTarget := range packages {
		if !slices.Contains(atomicPackages, packageTarget) {
			filtered = append(filtered, packageTarget)
		}
	}
	return filtered
}

// removeRaceExcludedOnlyAtomicPackages 删除只剩 normal-only 目标的原子包，避免 race 回退整包执行。
func removeRaceExcludedOnlyAtomicPackages(
	packages []string,
	allRaceTests []GoTestTarget,
	runnableRaceTests []GoTestTarget,
) []string {
	if len(allRaceTests) == len(runnableRaceTests) {
		return packages
	}
	atomicPackages := AtomicGoPackageTargets()
	filtered := make([]string, 0, len(packages))
	for _, packageTarget := range packages {
		if !slices.Contains(atomicPackages, packageTarget) ||
			!racePackageHasTargets(packageTarget, allRaceTests) ||
			racePackageHasTargets(packageTarget, runnableRaceTests) {
			filtered = append(filtered, packageTarget)
		}
	}
	return filtered
}

// racePackageHasTargets 判断 race inventory 是否声明了某个包的顶层测试。
func racePackageHasTargets(packageTarget string, targets []GoTestTarget) bool {
	for _, target := range targets {
		if target.Package == packageTarget {
			return true
		}
	}
	return false
}
