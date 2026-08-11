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
	case gate.WorkloadTargetGoGuard, gate.WorkloadTargetGoPackage, gate.WorkloadTargetGoTest,
		gate.WorkloadTargetGoBenchmark, gate.WorkloadTargetVitest,
		gate.WorkloadTargetPlaywright, gate.WorkloadTargetFrontendGuard:
	default:
		return "", fmt.Errorf("workload target kind %q has no source fingerprint policy", targetKind)
	}
	return snapshot.workloadTargetInputDigest(ctx, targetKind, target, profile)
}

// workloadTargetInputDigest 分派已登记的 atomic target 到其真实观察闭包。
func (snapshot *remoteGitTreeSnapshot) workloadTargetInputDigest(
	ctx context.Context,
	targetKind gate.WorkloadTargetKind,
	target string,
	profile remoteGoBuildProfile,
) (string, error) {
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
		return snapshot.frontendPlaywrightInputDigest(ctx, target)
	case gate.WorkloadTargetFrontendGuard:
		return snapshot.frontendPreflightInputDigest(target)
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
	observesWholeTree, err := snapshot.addGoExactTestCompileEntries(targetDirectory, selected, profile)
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
	entries := sortedRemoteGitTreeEntries(selected)
	return digestGoTestEntries(entries, testSources)
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
	case gate.GateIDFrontendLint:
		return snapshot.digestMatching(frontendLintInputEntry)
	case gate.GateIDFrontendPreflight:
		return snapshot.frontendPreflightInputDigest()
	case gate.GateIDFrontendTest, gate.GateIDFrontendFullTest:
		return snapshot.frontendNonE2EInputDigest()
	case gate.GateIDFrontendE2E:
		return snapshot.frontendPlaywrightParentInputDigest(context.Background())
	case gate.GateIDFrontendBuild:
		return snapshot.frontendBuildInputDigest()
	case gate.GateIDProjectMapCheck:
		return snapshot.projectMapInputDigest()
	case gate.GateIDFrontendEmbedVerify:
		return snapshot.digestMatching(frontendEmbedInputEntry)
	default:
		return snapshot.digestEntries(snapshot.entries)
	}
}

func frontendLintInputEntry(entry remoteGitTreeEntry) bool {
	return strings.HasPrefix(entry.path, "frontend-app/")
}

// frontendEmbedInputEntry 选择前端 embed 校验实际读取的仓库输入。
func frontendEmbedInputEntry(entry remoteGitTreeEntry) bool {
	base := path.Base(entry.path)
	if base == ".gitignore" || base == ".gitattributes" {
		return true
	}
	if strings.HasPrefix(entry.path, "cmd/agent-terminal/") && path.Ext(entry.path) == ".go" && !strings.HasSuffix(entry.path, "_test.go") {
		return true
	}
	return strings.HasPrefix(entry.path, "frontend-app/") || strings.HasPrefix(entry.path, "cmd/agent-terminal/web-dist/") ||
		entry.path == "Makefile" || entry.path == "scripts/frontend_embed_verify.sh"
}

// vitestInputDigest 计算指定 Vitest target 的精确输入摘要。
func (snapshot *remoteGitTreeSnapshot) vitestInputDigest(target string) (string, error) {
	targetPath := "frontend-app/" + target
	if _, ok := snapshot.byPath[targetPath]; !ok {
		return "", fmt.Errorf("Vitest target %q is absent from the exact Git tree", target)
	}
	if preflightTarget, ok := gate.ParseFrontendPreflightCarrierTarget(target); ok {
		return snapshot.frontendPreflightInputDigest(preflightTarget)
	}
	if target == gate.FrontendChangedSuiteCarrierTarget || target == gate.FrontendFullSuiteCarrierTarget {
		return snapshot.frontendNonE2EInputDigest()
	}
	if strings.HasPrefix(targetPath, "frontend-app/tests/e2e/") {
		return "", fmt.Errorf("Vitest target %q overlaps Playwright e2e tests", target)
	}
	return snapshot.frontendNonE2EInputDigest()
}

// goPackageInputDigest 为 Go 包测试的共享编译输入建立摘要。
func (snapshot *remoteGitTreeSnapshot) goPackageInputDigest(ctx context.Context, target string, profile remoteGoBuildProfile) (string, error) {
	key := remoteGoPackageInputDigestKey{target: target, race: profile.race}
	snapshot.cacheMu.Lock()
	if cached, ok := snapshot.goPackageInputDigestCache[key]; ok {
		snapshot.cacheMu.Unlock()
		return cached, nil
	}
	snapshot.cacheMu.Unlock()
	entries, err := snapshot.computeGoPackageInputEntries(ctx, target, profile)
	if err != nil {
		return "", err
	}
	digest, err := snapshot.digestEntries(entries)
	if err != nil {
		return "", err
	}
	snapshot.cacheMu.Lock()
	if snapshot.goPackageInputDigestCache == nil {
		snapshot.goPackageInputDigestCache = make(map[remoteGoPackageInputDigestKey]string)
	}
	if cached, ok := snapshot.goPackageInputDigestCache[key]; ok {
		digest = cached
	} else {
		snapshot.goPackageInputDigestCache[key] = digest
	}
	snapshot.cacheMu.Unlock()
	return digest, nil
}

// computeGoPackageInputEntries 收集整包 Go workload 的编译输入闭包。
func (snapshot *remoteGitTreeSnapshot) computeGoPackageInputEntries(ctx context.Context, target string, profile remoteGoBuildProfile) ([]remoteGitTreeEntry, error) {
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return nil, err
	}
	targetDirectory, err := remoteGoPackageDirectory(target)
	if err != nil {
		return nil, err
	}
	selected, err := snapshot.requiredGoPackageEntries()
	if err != nil {
		return nil, err
	}
	if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
		return nil, err
	}
	if _, err := snapshot.addGoTestPackageCompileEntries(targetDirectory, selected, profile); err != nil {
		return nil, err
	}
	entries := sortedRemoteGitTreeEntries(selected)
	return entries, nil
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
