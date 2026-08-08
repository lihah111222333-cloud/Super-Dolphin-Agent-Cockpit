package remoteci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// BuildWorkloadInventory 从精确 commit tree 和 base..commit 变更范围生成确定性测试清单。
func BuildWorkloadInventory(
	ctx context.Context,
	repositoryRoot string,
	commit string,
	base string,
	platform string,
) (gate.WorkloadInventory, error) {
	if !validInventoryInput(ctx, repositoryRoot, commit) {
		return gate.WorkloadInventory{}, errors.New("workload inventory Git identity is incomplete")
	}
	files, err := inventoryGitLines(ctx, repositoryRoot, "ls-tree", "-r", "--name-only", commit, "--")
	if err != nil {
		return gate.WorkloadInventory{}, err
	}
	changed, err := inventoryChangedFiles(ctx, repositoryRoot, base, commit, files)
	if err != nil {
		return gate.WorkloadInventory{}, err
	}
	vitestPolicy, err := loadInventoryVitestSuitePolicy(ctx, repositoryRoot, commit)
	if err != nil {
		return gate.WorkloadInventory{}, err
	}
	fullTargets, err := inventoryFullTargets(files, vitestPolicy)
	if err != nil {
		return gate.WorkloadInventory{}, err
	}
	fullTargets.goPackages, err = inventoryPlatformGoPackages(
		ctx,
		repositoryRoot,
		commit,
		platform,
		fullTargets.goPackages,
	)
	if err != nil {
		return gate.WorkloadInventory{}, err
	}
	goTests, goRaceTests, err := inventoryAtomicGoTests(ctx, repositoryRoot, commit, platform, fullTargets.goPackages)
	if err != nil {
		return gate.WorkloadInventory{}, err
	}
	return gate.WorkloadInventory{
		GoPackages:           fullTargets.goPackages,
		NestedGoModules:      fullTargets.nestedGoModules,
		GoTests:              goTests,
		GoRaceTests:          goRaceTests,
		FrontendChangedTests: changedVitestTargets(changed, fullTargets.files, vitestPolicy),
		FrontendFullTests:    fullTargets.frontendTests,
	}, nil
}

// inventoryPlatformGoPackages 依据精确 commit tree 和目标平台过滤可编译的 Go 包，
// 使后续测试清单不受当前工作区或其他平台文件影响。
func inventoryPlatformGoPackages(
	ctx context.Context,
	repositoryRoot string,
	revision string,
	platform string,
	packages []string,
) ([]string, error) {
	goos, goarch, err := remoteGoTestPlatform(platform)
	if err != nil {
		return nil, err
	}
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, revision)
	if err != nil {
		return nil, err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(packages))
	for _, packageTarget := range packages {
		directory, err := remoteGoPackageDirectory(packageTarget)
		if err != nil {
			return nil, err
		}
		matched, err := snapshot.remoteGoPackageMatchesPlatform(directory, goos, goarch, false)
		if err != nil {
			return nil, err
		}
		if matched {
			filtered = append(filtered, packageTarget)
		}
	}
	return filtered, nil
}

// inventoryAtomicGoTests 从候选 tree 枚举已知超时包在目标平台的普通与 race 顶层测试。
func inventoryAtomicGoTests(
	ctx context.Context,
	repositoryRoot string,
	revision string,
	platform string,
	packages []string,
) ([]gate.GoTestTarget, []gate.GoTestTarget, error) {
	goos, goarch, err := remoteGoTestPlatform(platform)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, revision)
	if err != nil {
		return nil, nil, err
	}
	var normalTargets, raceTargets []gate.GoTestTarget
	for _, packageTarget := range gate.AtomicGoPackageTargets() {
		if !slices.Contains(packages, packageTarget) {
			continue
		}
		directory := strings.TrimPrefix(packageTarget, "./")
		paths := atomicGoTestSourcePaths(snapshot, directory)
		if len(paths) == 0 {
			continue
		}
		snapshot.goSources, err = snapshot.readGitBlobs(ctx, paths)
		if err != nil {
			return nil, nil, err
		}
		normal, err := snapshot.remoteGoPackageTestInventory(directory, goos, goarch, false)
		if err != nil {
			return nil, nil, err
		}
		race, err := snapshot.remoteGoPackageTestInventory(directory, goos, goarch, true)
		if err != nil {
			return nil, nil, err
		}
		normalTargets = append(normalTargets, inventoryGoTestTargets(packageTarget, normal)...)
		raceTargets = append(raceTargets, inventoryGoTestTargets(packageTarget, race)...)
	}
	return normalTargets, raceTargets, nil
}

// atomicGoTestSourcePaths 返回目标包中可由 Git tree 批量读取的测试源码路径。
func atomicGoTestSourcePaths(snapshot *remoteGitTreeSnapshot, directory string) []string {
	paths := make([]string, 0)
	for _, entry := range snapshot.entries {
		if entry.kind == "blob" && path.Dir(entry.path) == directory && strings.HasSuffix(entry.path, "_test.go") {
			paths = append(paths, entry.path)
		}
	}
	sort.Strings(paths)
	return paths
}

// inventoryGoTestTargets 将排序后的顶层测试名称绑定到精确包目标。
func inventoryGoTestTargets(packageTarget string, names []string) []gate.GoTestTarget {
	targets := make([]gate.GoTestTarget, len(names))
	for index, name := range names {
		targets[index] = gate.GoTestTarget{Package: packageTarget, Name: name}
	}
	return targets
}

// validInventoryInput 判断 Git 清单构建所需的上下文、仓库和提交是否齐全。
func validInventoryInput(ctx context.Context, repositoryRoot string, commit string) bool {
	return ctx != nil && strings.TrimSpace(repositoryRoot) != "" && commit != ""
}

// inventoryChangedFiles 返回 base 未给定时的全量文件或精确变更文件。
func inventoryChangedFiles(ctx context.Context, repositoryRoot string, base string, commit string, files []string) ([]string, error) {
	if base == "" {
		return files, nil
	}
	return inventoryGitLines(ctx, repositoryRoot, "diff", "--name-only", "--diff-filter=ACMRTUXB", base, commit, "--")
}

type inventoryTargetSet struct {
	files           map[string]struct{}
	goPackages      []string
	nestedGoModules []string
	frontendTests   []string
}

const inventoryVitestSuitePolicyPath = "frontend-app/config/vitest-suite-policy.json"

type inventoryVitestSuitePolicy struct {
	SchemaVersion   int      `json:"schemaVersion"`
	DefaultExcludes []string `json:"defaultExcludes"`
}

// loadInventoryVitestSuitePolicy 从候选 Git tree 读取前端与远程清单共享的测试发现策略。
func loadInventoryVitestSuitePolicy(
	ctx context.Context,
	repositoryRoot string,
	revision string,
) (inventoryVitestSuitePolicy, error) {
	command := exec.CommandContext(ctx, "git", "show", revision+":"+inventoryVitestSuitePolicyPath)
	command.Dir = repositoryRoot
	output, err := command.Output()
	if err != nil {
		return inventoryVitestSuitePolicy{}, fmt.Errorf("read Vitest suite policy from Git tree: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var policy inventoryVitestSuitePolicy
	if err := decoder.Decode(&policy); err != nil {
		return inventoryVitestSuitePolicy{}, fmt.Errorf("decode Vitest suite policy: %w", err)
	}
	if err := requireInventoryJSONEOF(decoder); err != nil {
		return inventoryVitestSuitePolicy{}, err
	}
	if err := validateInventoryVitestSuitePolicy(policy); err != nil {
		return inventoryVitestSuitePolicy{}, err
	}
	return policy, nil
}

func requireInventoryJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Vitest suite policy has trailing JSON values")
		}
		return fmt.Errorf("decode trailing Vitest suite policy: %w", err)
	}
	return nil
}

// validateInventoryVitestSuitePolicy 检查 Vitest 排除策略的版本、非空约束、glob 合法性和重复项，
// 任何不确定策略都在构建清单前失败，避免静默改变测试范围。
func validateInventoryVitestSuitePolicy(policy inventoryVitestSuitePolicy) error {
	if policy.SchemaVersion != 1 || len(policy.DefaultExcludes) == 0 {
		return errors.New("Vitest suite policy schema is invalid")
	}
	seen := make(map[string]struct{}, len(policy.DefaultExcludes))
	for _, pattern := range policy.DefaultExcludes {
		if err := validateInventoryVitestGlob(pattern); err != nil {
			return err
		}
		if _, exists := seen[pattern]; exists {
			return fmt.Errorf("Vitest suite policy exclude %q is duplicated", pattern)
		}
		seen[pattern] = struct{}{}
	}
	return nil
}

func validateInventoryVitestGlob(pattern string) error {
	if invalidInventoryVitestGlobPattern(pattern) {
		return fmt.Errorf("Vitest suite policy exclude %q is invalid", pattern)
	}
	for segment := range strings.SplitSeq(pattern, "/") {
		if err := validateInventoryVitestGlobSegment(pattern, segment); err != nil {
			return err
		}
	}
	return nil
}

func invalidInventoryVitestGlobPattern(pattern string) bool {
	return pattern == "" || strings.TrimSpace(pattern) != pattern || path.IsAbs(pattern) ||
		strings.ContainsAny(pattern, "\\\x00\r\n")
}

// validateInventoryVitestGlobSegment 拒绝 glob 路径段中的空段、目录穿越和非法通配符，
// 仅允许独立的 ** 作为跨目录匹配段。
func validateInventoryVitestGlobSegment(pattern string, segment string) error {
	if segment == "" || segment == "." || segment == ".." ||
		(segment != "**" && strings.Contains(segment, "**")) {
		return fmt.Errorf("Vitest suite policy exclude %q is invalid", pattern)
	}
	if segment == "**" {
		return nil
	}
	if _, err := path.Match(segment, "probe"); err != nil {
		return fmt.Errorf("Vitest suite policy exclude %q is invalid: %w", pattern, err)
	}
	return nil
}

func (policy inventoryVitestSuitePolicy) excludes(target string) bool {
	for _, pattern := range policy.DefaultExcludes {
		if inventoryVitestGlobMatches(pattern, target) {
			return true
		}
	}
	return false
}

// inventoryVitestGlobMatches 按路径段递归匹配 Vitest glob，明确处理 ** 的零段或多段展开。
func inventoryVitestGlobMatches(pattern string, target string) bool {
	patternSegments := strings.Split(pattern, "/")
	targetSegments := strings.Split(target, "/")
	var match func(int, int) bool
	match = func(patternIndex int, targetIndex int) bool {
		if patternIndex == len(patternSegments) {
			return targetIndex == len(targetSegments)
		}
		if patternSegments[patternIndex] == "**" {
			return match(patternIndex+1, targetIndex) ||
				targetIndex < len(targetSegments) && match(patternIndex, targetIndex+1)
		}
		if targetIndex == len(targetSegments) {
			return false
		}
		matched, err := path.Match(patternSegments[patternIndex], targetSegments[targetIndex])
		return err == nil && matched && match(patternIndex+1, targetIndex+1)
	}
	return match(0, 0)
}

// inventoryFullTargets 从完整树自动发现根模块包、嵌套模块和前端全量测试。
func inventoryFullTargets(files []string, vitestPolicy inventoryVitestSuitePolicy) (inventoryTargetSet, error) {
	nestedModules, err := inventoryNestedGoModules(files)
	if err != nil {
		return inventoryTargetSet{}, err
	}
	fileSet, packages, frontend := make(map[string]struct{}, len(files)), make(map[string]struct{}), []string{}
	for _, file := range files {
		fileSet[file] = struct{}{}
		if packageTarget, ok := gate.GoPackageTargetForSource(file); ok && !withinNestedGoModule(file, nestedModules) {
			packages[packageTarget] = struct{}{}
		}
		if relative, ok := frontendVitestPath(file, vitestPolicy); ok {
			frontend = append(frontend, relative)
		}
	}
	goPackages := make([]string, 0, len(packages))
	for packageName := range packages {
		goPackages = append(goPackages, packageName)
	}
	sort.Strings(goPackages)
	sort.Strings(frontend)
	return inventoryTargetSet{
		files:           fileSet,
		goPackages:      goPackages,
		nestedGoModules: nestedModules,
		frontendTests:   frontend,
	}, nil
}

// inventoryNestedGoModules 从 Git tree 的 go.mod 边界发现全部嵌套模块。
func inventoryNestedGoModules(files []string) ([]string, error) {
	rootModule := false
	modules := make([]string, 0)
	for _, file := range files {
		if file == "go.mod" {
			rootModule = true
			continue
		}
		if path.Base(file) != "go.mod" {
			continue
		}
		module := path.Dir(file)
		if module == "." || strings.HasPrefix(module, "../") || path.IsAbs(module) || path.Clean(module) != module {
			return nil, fmt.Errorf("nested Go module path %q is invalid", module)
		}
		modules = append(modules, module)
	}
	if !rootModule {
		return nil, errors.New("workload inventory tree omits root go.mod")
	}
	sort.Strings(modules)
	return modules, nil
}

// withinNestedGoModule 报告源码是否属于另一个 Go 模块而非仓库根模块。
func withinNestedGoModule(file string, modules []string) bool {
	for _, module := range modules {
		if strings.HasPrefix(file, module+"/") {
			return true
		}
	}
	return false
}

// inventoryGitLines 执行 Git 路径查询并拒绝不规范的输出行。
func inventoryGitLines(ctx context.Context, repositoryRoot string, args ...string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line == "" || strings.TrimSpace(line) != line || strings.ContainsAny(line, "\x00\r") {
			return nil, errors.New("Git inventory contains a non-canonical path")
		}
	}
	return lines, nil
}

// frontendVitestPath 将树内 Vitest 文件映射为前端工作目录相对路径。
func frontendVitestPath(file string, policy inventoryVitestSuitePolicy) (string, bool) {
	const prefix = "frontend-app/"
	if !strings.HasPrefix(file, prefix) {
		return "", false
	}
	relative := strings.TrimPrefix(file, prefix)
	if (!strings.HasPrefix(relative, "src/") && !strings.HasPrefix(relative, "scripts/")) ||
		(!strings.Contains(relative, ".test.") && !strings.Contains(relative, ".spec.")) ||
		policy.excludes(relative) {
		return "", false
	}
	switch path.Ext(relative) {
	case ".js", ".jsx", ".mjs", ".ts", ".tsx":
		return relative, true
	default:
		return "", false
	}
}

// changedVitestTargets 为变更的测试或前端源码选择存在的 Vitest 目标。
func changedVitestTargets(
	changed []string,
	files map[string]struct{},
	policy inventoryVitestSuitePolicy,
) []string {
	selected := make(map[string]struct{})
	for _, file := range changed {
		addChangedVitestTargets(selected, file, files, policy)
	}
	result := make([]string, 0, len(selected))
	for file := range selected {
		result = append(result, file)
	}
	sort.Strings(result)
	return result
}

// addChangedVitestTargets 将一个变更文件对应的现有测试加入选择集。
func addChangedVitestTargets(
	selected map[string]struct{},
	file string,
	files map[string]struct{},
	policy inventoryVitestSuitePolicy,
) {
	if relative, ok := frontendVitestPath(file, policy); ok {
		if _, exists := files[file]; exists {
			selected[relative] = struct{}{}
		}
		return
	}
	for _, candidate := range frontendSourceTestCandidates(file) {
		if _, exists := files[candidate]; exists {
			selected[strings.TrimPrefix(candidate, "frontend-app/")] = struct{}{}
		}
	}
}

// frontendSourceTestCandidates 返回前端源码文件的同名测试候选。
func frontendSourceTestCandidates(file string) []string {
	const prefix = "frontend-app/"
	if !strings.HasPrefix(file, prefix) {
		return nil
	}
	relative := strings.TrimPrefix(file, prefix)
	extension := path.Ext(relative)
	if !strings.HasPrefix(relative, "src/") || !containsFrontendSourceExtension(extension) || strings.Contains(relative, ".test.") || strings.Contains(relative, ".spec.") {
		return nil
	}
	base := strings.TrimSuffix(relative, extension)
	return []string{prefix + base + ".test.ts", prefix + base + ".test.tsx", prefix + base + ".test.js", prefix + base + ".test.jsx"}
}

func containsFrontendSourceExtension(extension string) bool {
	switch extension {
	case ".js", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}
