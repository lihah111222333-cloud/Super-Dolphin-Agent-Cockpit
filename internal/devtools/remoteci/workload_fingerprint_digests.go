package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	profile := remoteGoBuildProfile{race: parent == gate.GateIDBackendTestGuardWithRace}
	if !targeted {
		return snapshot.canonicalGateInputDigest(parent)
	}
	switch targetKind {
	case gate.WorkloadTargetGoGuard:
		return snapshot.goGuardInputDigest(ctx, target)
	case gate.WorkloadTargetGoPackage:
		return snapshot.goPackageInputDigest(ctx, target, profile)
	case gate.WorkloadTargetGoTest:
		return snapshot.goTestInputDigest(ctx, target, profile)
	case gate.WorkloadTargetGoBenchmark:
		return snapshot.goBenchmarkInputDigest(ctx, target, profile)
	case gate.WorkloadTargetVitest:
		return snapshot.vitestInputDigest(target)
	case gate.WorkloadTargetPlaywright:
		return snapshot.playwrightInputDigest(target)
	default:
		return "", fmt.Errorf("workload target kind %q has no source fingerprint policy", targetKind)
	}
}

func (snapshot *remoteGitTreeSnapshot) goTestInputDigest(ctx context.Context, target string, profile remoteGoBuildProfile) (string, error) {
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		return "", err
	}
	return snapshot.goExactTestInputDigest(ctx, testTarget, profile)
}

// goExactTestInputDigest 为单一 Go 测试或基准建立编译、声明和观察输入摘要。
func (snapshot *remoteGitTreeSnapshot) goExactTestInputDigest(ctx context.Context, testTarget gate.GoTestTarget, profile remoteGoBuildProfile) (string, error) {
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
	if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
		return "", err
	}
	observesWholeTree, err := snapshot.addGoExactTestEntries(targetDirectory, selected, profile)
	if err != nil {
		return "", err
	}
	if observesWholeTree {
		return snapshot.digestEntries(snapshot.entries)
	}
	testSources, observesWholeTree, err := snapshot.goTestSources(testTarget.Name, targetDirectory, selected, profile)
	if err != nil {
		return "", err
	}
	if observesWholeTree {
		return snapshot.digestEntries(snapshot.entries)
	}
	return digestGoTestEntries(sortedRemoteGitTreeEntries(selected), testSources)
}

// addGoExactTestEntries 汇总编译和生产运行时观察；任何动态观察都要求完整 Git tree。
func (snapshot *remoteGitTreeSnapshot) addGoExactTestEntries(
	directory string,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) (bool, error) {
	observesWholeTree, err := snapshot.addGoExactTestCompileEntries(directory, selected, profile)
	if err != nil || observesWholeTree {
		return observesWholeTree, err
	}
	return snapshot.addGoExactTestProductionObservedEntries(directory, selected, profile)
}

func (snapshot *remoteGitTreeSnapshot) goBenchmarkInputDigest(ctx context.Context, target string, profile remoteGoBuildProfile) (string, error) {
	benchmarkTarget, err := gate.ParseGoBenchmarkTarget(target)
	if err != nil {
		return "", err
	}
	return snapshot.goExactTestInputDigest(ctx, benchmarkTarget, profile)
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
		return snapshot.nestedGoModuleInputDigest(ctx, module)
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
	if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
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
func (snapshot *remoteGitTreeSnapshot) nestedGoModuleInputDigest(ctx context.Context, moduleDirectory string) (string, error) {
	if _, ok := snapshot.byPath[moduleDirectory+"/go.mod"]; !ok {
		return "", fmt.Errorf("nested Go guard module %q has no tracked go.mod", moduleDirectory)
	}
	selected := make(map[string]remoteGitTreeEntry)
	for _, entry := range snapshot.entries {
		if strings.HasPrefix(entry.path, moduleDirectory+"/") ||
			entry.path == "scripts/check_nested_go_modules.sh" || entry.path == "scripts/real_go_resolver.sh" {
			selected[entry.path] = entry
		}
	}
	if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
		return "", err
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

// canonicalGateInputDigest 按父门禁语义选择精确 Git tree 输入集合。
func (snapshot *remoteGitTreeSnapshot) canonicalGateInputDigest(parent gate.GateID) (string, error) {
	switch parent {
	case gate.GateIDFrontendLint, gate.GateIDFrontendTest, gate.GateIDFrontendE2E, gate.GateIDFrontendFullTest, gate.GateIDFrontendBuild:
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

// playwrightInputDigest 将 e2e spec、Playwright 配置和可观察前端源码闭包绑定到精确 Git tree。
// 目前 Playwright 配置允许动态导入，因此保守摘要整个 frontend-app，避免漏掉可执行依赖。
func (snapshot *remoteGitTreeSnapshot) playwrightInputDigest(target string) (string, error) {
	targetPath := "frontend-app/" + target
	if _, ok := snapshot.byPath[targetPath]; !ok {
		return "", fmt.Errorf("Playwright target %q is absent from the exact Git tree", target)
	}
	return snapshot.digestMatching(func(entry remoteGitTreeEntry) bool {
		return strings.HasPrefix(entry.path, "frontend-app/")
	})
}

// goPackageInputDigest 为 Go 包测试的共享编译输入建立摘要。
func (snapshot *remoteGitTreeSnapshot) goPackageInputDigest(ctx context.Context, target string, profile remoteGoBuildProfile) (string, error) {
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
	if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
		return "", err
	}
	files, err := snapshot.addGoTestPackageCompileEntries(targetDirectory, selected, profile)
	if err != nil {
		return "", err
	}
	if files == nil {
		return snapshot.digestEntries(snapshot.entries)
	}
	entries := sortedRemoteGitTreeEntries(selected)
	return snapshot.digestEntries(entries)
}

const (
	remoteCanonicalScriptFingerprintBegin = "# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_BEGIN\n"
	remoteCanonicalScriptFingerprintEnd   = "# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_END\n"
	remoteGoWorkloadSharedScriptPath      = "scripts/test_with_guard.sh#shared-execution"
)

// addGoWorkloadSharedScriptEntry 将每个 Go 工作负载绑定到共享测试运行语义，隔离仅后端专用逻辑。
func (snapshot *remoteGitTreeSnapshot) addGoWorkloadSharedScriptEntry(
	ctx context.Context,
	selected map[string]remoteGitTreeEntry,
) error {
	snapshot.cacheMu.Lock()
	cached := snapshot.goWorkloadSharedScript
	snapshot.cacheMu.Unlock()
	if cached != nil {
		selected[cached.path] = *cached
		return nil
	}
	entry, ok := snapshot.byPath["scripts/test_with_guard.sh"]
	if !ok || entry.kind != "blob" {
		return errors.New("Go workload fingerprint test runner script is absent")
	}
	blobs, err := snapshot.readGitBlobs(ctx, []string{"scripts/test_with_guard.sh"})
	if err != nil {
		return err
	}
	shared, err := remoteGoWorkloadSharedScript(blobs["scripts/test_with_guard.sh"])
	if err != nil {
		return err
	}
	sum := sha256.Sum256(shared)
	semantic := remoteGitTreeEntry{
		mode: "100644", kind: "semantic", objectID: hex.EncodeToString(sum[:]), path: remoteGoWorkloadSharedScriptPath,
	}
	snapshot.cacheMu.Lock()
	if snapshot.goWorkloadSharedScript == nil {
		snapshot.goWorkloadSharedScript = &semantic
	}
	semantic = *snapshot.goWorkloadSharedScript
	snapshot.cacheMu.Unlock()
	selected[semantic.path] = semantic
	return nil
}

func remoteGoWorkloadSharedScript(script []byte) ([]byte, error) {
	if bytes.Count(script, []byte(remoteCanonicalScriptFingerprintBegin)) != 1 ||
		bytes.Count(script, []byte(remoteCanonicalScriptFingerprintEnd)) != 1 {
		return nil, errors.New("Go workload fingerprint canonical script boundary is ambiguous")
	}
	start := bytes.Index(script, []byte(remoteCanonicalScriptFingerprintBegin))
	end := bytes.Index(script, []byte(remoteCanonicalScriptFingerprintEnd))
	if start < 0 || end < start {
		return nil, errors.New("Go workload fingerprint canonical script boundary is invalid")
	}
	end += len(remoteCanonicalScriptFingerprintEnd)
	return append(append([]byte(nil), script[:start]...), script[end:]...), nil
}
