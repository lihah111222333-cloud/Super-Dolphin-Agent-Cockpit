package remoteci

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// workloadInputDigest 按 workload 类型计算其可观察生产输入摘要。
func (snapshot *remoteGitTreeSnapshot) workloadInputDigest(ctx context.Context, workload gate.Workload) (string, error) {
	parent, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return "", err
	}
	if !targeted {
		return snapshot.canonicalGateInputDigest(parent)
	}
	switch targetKind {
	case gate.WorkloadTargetGoGuard:
		return snapshot.goGuardInputDigest(ctx, target)
	case gate.WorkloadTargetGoPackage:
		return snapshot.goPackageInputDigest(ctx, target)
	case gate.WorkloadTargetGoTest:
		return snapshot.goTestInputDigest(ctx, target)
	case gate.WorkloadTargetGoBenchmark:
		return snapshot.goBenchmarkInputDigest(ctx, target)
	case gate.WorkloadTargetVitest:
		return snapshot.vitestInputDigest(target)
	default:
		return "", fmt.Errorf("workload target kind %q has no source fingerprint policy", targetKind)
	}
}

func (snapshot *remoteGitTreeSnapshot) goTestInputDigest(ctx context.Context, target string) (string, error) {
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		return "", err
	}
	return snapshot.goExactTestInputDigest(ctx, testTarget)
}

// goExactTestInputDigest 为单一 Go 测试或基准建立编译、声明和观察输入摘要。
func (snapshot *remoteGitTreeSnapshot) goExactTestInputDigest(ctx context.Context, testTarget gate.GoTestTarget) (string, error) {
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	targetDirectory, err := remoteGoPackageDirectory(testTarget.Package)
	if err != nil {
		return "", err
	}
	selected, err := snapshot.requiredGoPackageEntries()
	if err != nil {
		return "", err
	}
	if err := snapshot.addGoExactTestCompileEntries(targetDirectory, selected); err != nil {
		return "", err
	}
	testSources, observesWholeTree, err := snapshot.goTestSources(testTarget.Name, targetDirectory, selected)
	if err != nil {
		return "", err
	}
	if observesWholeTree {
		return snapshot.digestEntries(snapshot.entries)
	}
	return digestGoTestEntries(sortedRemoteGitTreeEntries(selected), testSources)
}
func (snapshot *remoteGitTreeSnapshot) goBenchmarkInputDigest(ctx context.Context, target string) (string, error) {
	benchmarkTarget, err := gate.ParseGoBenchmarkTarget(target)
	if err != nil {
		return "", err
	}
	return snapshot.goExactTestInputDigest(ctx, benchmarkTarget)
}

// goGuardInputDigest 为原子守卫选取真正可观察的生产输入。
func (snapshot *remoteGitTreeSnapshot) goGuardInputDigest(ctx context.Context, target string) (string, error) {
	switch target {
	case gate.GoGuardTargetCanonical,
		gate.GoGuardTargetSource,
		gate.GoGuardTargetSourceRawGoTest:
		return snapshot.digestEntries(snapshot.entries)
	case gate.GoGuardTargetSourceCodeSize:
		return snapshot.sourceCodeSizeInputDigest()
	case gate.GoGuardTargetAIMaintenanceUnit, gate.GoGuardTargetAIMaintenanceGate:
		return snapshot.canonicalGateInputDigest(gate.GateIDAIMaintenanceSelfTest)
	case gate.GoGuardTargetCopylocksProvider:
		return snapshot.goPackageTreeInputDigest(ctx, "internal/provider")
	case gate.GoGuardTargetCopylocksPlatform:
		return snapshot.goPackageTreeInputDigest(ctx, "internal/platform")
	case gate.GoGuardTargetCopylocksThread:
		return snapshot.goPackageTreeInputDigest(ctx, "internal/module/thread")
	default:
		module, err := gate.ParseNestedGoModuleGuardTarget(target)
		if err != nil {
			return "", fmt.Errorf("Go guard target %q has no source fingerprint policy", target)
		}
		return snapshot.nestedGoModuleInputDigest(module)
	}
}

func (snapshot *remoteGitTreeSnapshot) sourceCodeSizeInputDigest() (string, error) {
	return snapshot.digestDomainMatching("go-guard/source-code-size-v2", func(entry remoteGitTreeEntry) bool {
		switch entry.path {
		case "go.mod", "go.sum", "internal/archtest/freeze_baseline.json":
			return true
		}
		if !strings.HasSuffix(entry.path, ".go") {
			return false
		}
		for _, root := range []string{"cmd/", "internal/", "pkg/", "scripts/"} {
			if strings.HasPrefix(entry.path, root) {
				return true
			}
		}
		return false
	})
}

// goPackageTreeInputDigest 计算一个注册目录树及其本地 Go 依赖闭包。
func (snapshot *remoteGitTreeSnapshot) goPackageTreeInputDigest(ctx context.Context, directoryPrefix string) (string, error) {
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	selected, err := snapshot.requiredGoPackageEntries()
	if err != nil {
		return "", err
	}
	directories := make(map[string]struct{})
	for _, entry := range snapshot.entries {
		if directory, ok := remoteGoPackageTreeDirectory(entry, directoryPrefix); ok {
			directories[directory] = struct{}{}
		}
	}
	if len(directories) == 0 {
		return "", fmt.Errorf("Go guard source tree %q has no packages", directoryPrefix)
	}
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Strings(ordered)
	for _, directory := range ordered {
		if err := snapshot.addGoPackageEntries(directory, selected); err != nil {
			return "", err
		}
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

func remoteGoPackageTreeDirectory(entry remoteGitTreeEntry, prefix string) (string, bool) {
	directory := path.Dir(entry.path)
	return directory, path.Ext(entry.path) == ".go" && (directory == prefix || strings.HasPrefix(directory, prefix+"/"))
}

// nestedGoModuleInputDigest 仅包含指定嵌套模块和其固定守卫脚本。
func (snapshot *remoteGitTreeSnapshot) nestedGoModuleInputDigest(moduleDirectory string) (string, error) {
	if _, ok := snapshot.byPath[moduleDirectory+"/go.mod"]; !ok {
		return "", fmt.Errorf("nested Go guard module %q has no tracked go.mod", moduleDirectory)
	}
	return snapshot.digestMatching(func(entry remoteGitTreeEntry) bool {
		return strings.HasPrefix(entry.path, moduleDirectory+"/") ||
			entry.path == "scripts/test_with_guard.sh" ||
			entry.path == "scripts/check_nested_go_modules.sh" ||
			entry.path == "scripts/real_go_resolver.sh"
	})
}

// canonicalGateInputDigest 按父门禁语义选择精确 Git tree 输入集合。
func (snapshot *remoteGitTreeSnapshot) canonicalGateInputDigest(parent gate.GateID) (string, error) {
	switch parent {
	case gate.GateIDFrontendLint, gate.GateIDFrontendTest, gate.GateIDFrontendFullTest, gate.GateIDFrontendBuild:
		return snapshot.digestMatching(func(entry remoteGitTreeEntry) bool {
			return strings.HasPrefix(entry.path, "frontend-app/")
		})
	case gate.GateIDFrontendEmbedVerify:
		return snapshot.digestMatching(func(entry remoteGitTreeEntry) bool {
			return strings.HasPrefix(entry.path, "frontend-app/") ||
				strings.HasPrefix(entry.path, "cmd/agent-terminal/web-dist/") ||
				entry.path == "Makefile" || entry.path == "scripts/frontend_embed_verify.sh"
		})
	default:
		return snapshot.digestEntries(snapshot.entries)
	}
}

func (snapshot *remoteGitTreeSnapshot) vitestInputDigest(target string) (string, error) {
	targetPath := "frontend-app/" + target
	if _, ok := snapshot.byPath[targetPath]; !ok {
		return "", fmt.Errorf("Vitest target %q is absent from the exact Git tree", target)
	}
	return snapshot.digestMatching(func(entry remoteGitTreeEntry) bool {
		return strings.HasPrefix(entry.path, "frontend-app/")
	})
}

// goPackageInputDigest 为 Go 包测试的共享编译输入建立摘要。
func (snapshot *remoteGitTreeSnapshot) goPackageInputDigest(ctx context.Context, target string) (string, error) {
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	targetDirectory, err := remoteGoPackageDirectory(target)
	if err != nil {
		return "", err
	}
	selected, err := snapshot.requiredGoPackageEntries()
	if err != nil {
		return "", err
	}
	files, err := snapshot.addGoTestPackageCompileEntries(targetDirectory, selected)
	if err != nil {
		return "", err
	}
	if files == nil {
		return snapshot.digestEntries(snapshot.entries)
	}
	entries := sortedRemoteGitTreeEntries(selected)
	return snapshot.digestEntries(entries)
}
