package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
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
	if _, err := snapshot.addGoExactTestCompileEntries(targetDirectory, selected, profile); err != nil {
		return "", err
	}
	if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
		return "", err
	}
	testSources, observesWholeTree, _, err := snapshot.goTestSources(testTarget.Name, targetDirectory, selected, profile, true)
	if err != nil {
		return "", err
	}
	if observesWholeTree {
		return snapshot.digestEntries(snapshot.entries)
	}
	entries := sortedRemoteGitTreeEntries(selected)
	return digestGoTestEntries(entries, testSources)
}

// goExactTestSemanticInputDigest 只摘要 selector 声明、静态运行时观察和其生产调用闭包。
// 同包未选测试的编译输入由 broad digest 单独投票，不得凭单票扩大 selector MISS。
func (snapshot *remoteGitTreeSnapshot) goExactTestSemanticInputDigest(ctx context.Context, testTarget gate.GoTestTarget, profile remoteGoBuildProfile) (string, error) {
	digest, _, err := snapshot.goExactTestRuntimeInputDigest(ctx, testTarget, profile)
	return digest, err
}

// goExactTestRuntimeInputDigest 摘要 selector 的静态运行时观察闭包；
// 无法精确闭合时明确标记全树 fallback，供投票层避免重复计票。
func (snapshot *remoteGitTreeSnapshot) goExactTestRuntimeInputDigest(ctx context.Context, testTarget gate.GoTestTarget, profile remoteGoBuildProfile) (string, bool, error) {
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", false, err
	}
	targetDirectory, err := remoteGoPackageDirectory(testTarget.Package)
	if err != nil {
		return "", false, err
	}
	selected := make(map[string]remoteGitTreeEntry)
	testSources, observesWholeTree, runtimeFallback, err := snapshot.goTestSources(testTarget.Name, targetDirectory, selected, profile, false)
	if err != nil {
		return "", false, err
	}
	if observesWholeTree {
		digest, err := snapshot.digestEntries(snapshot.entries)
		return digest, true, err
	}
	digest, err := digestGoTestRuntimeEntries(sortedRemoteGitTreeEntries(selected), testSources)
	return digest, runtimeFallback, err
}

// digestGoTestRuntimeEntries 对 selector 真实运行时观察和声明源码建立独立摘要。
func digestGoTestRuntimeEntries(entries []remoteGitTreeEntry, sources []remoteGoTestSource) (string, error) {
	if len(sources) == 0 {
		return "", errors.New("selector runtime input set is empty")
	}
	hasher := sha256.New()
	fmt.Fprintln(hasher, "domain remote-go-selector-runtime/v1")
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	for _, source := range sources {
		fmt.Fprintf(hasher, "test %s\t%x\n", source.path, sha256.Sum256(source.text))
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// goExactTestDeclarationInputDigest 只摘要目标 selector 及其声明依赖，
// 与同包编译闭包和运行时观察闭包保持独立。
func (snapshot *remoteGitTreeSnapshot) goExactTestDeclarationInputDigest(ctx context.Context, testTarget gate.GoTestTarget, profile remoteGoBuildProfile) (string, error) {
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	directory, err := remoteGoPackageDirectory(testTarget.Package)
	if err != nil {
		return "", err
	}
	files, declarations, fallback := snapshot.remoteGoTestDeclarations(directory, profile)
	if fallback {
		return "", fmt.Errorf("parse selector declaration input for %q", testTarget.Name)
	}
	targets := declarations[testTarget.Name]
	if len(targets) != 1 {
		return "", fmt.Errorf("selector declaration %q count = %d, want 1", testTarget.Name, len(targets))
	}
	selected, _ := remoteGoTestSelectedDeclarations(targets[0], files, declarations)
	sources := make([]remoteGoTestSource, 0, len(selected))
	if len(selected) == 0 {
		sources = append(sources, remoteGoTestSource{path: targets[0].filePath, text: remoteGoTestDeclarationText(targets[0])})
	}
	for _, declaration := range selected {
		sources = append(sources, remoteGoTestSource{path: declaration.filePath, text: remoteGoTestDeclarationText(declaration)})
	}
	sort.Slice(sources, func(left, right int) bool {
		if sources[left].path == sources[right].path {
			return bytes.Compare(sources[left].text, sources[right].text) < 0
		}
		return sources[left].path < sources[right].path
	})
	if len(sources) == 0 {
		return "", errors.New("selector declaration input set is empty")
	}
	hasher := sha256.New()
	fmt.Fprintln(hasher, "domain remote-go-selector-declaration/v1")
	for _, source := range sources {
		fmt.Fprintf(hasher, "test %s\t%x\n", source.path, sha256.Sum256(source.text))
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

type remoteWorkloadInputVoteDigests struct {
	compile         string
	declaration     string
	runtime         string
	runtimeFallback bool
}

// remoteGoWorkloadInputTarget 解析可参与 compile-first replay 的 Go selector 和构建 profile。
func remoteGoWorkloadInputTarget(workload gate.Workload) (gate.GoTestTarget, remoteGoBuildProfile, bool, error) {
	parent, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil || !targeted {
		return gate.GoTestTarget{}, remoteGoBuildProfile{}, false, err
	}
	profile := remoteGoBuildProfile{race: parent == gate.GateIDBackendTestGuardWithRace}
	var parsed gate.GoTestTarget
	switch targetKind {
	case gate.WorkloadTargetGoTest:
		parsed, err = gate.ParseGoTestTarget(target)
	case gate.WorkloadTargetGoBenchmark:
		parsed, err = gate.ParseGoBenchmarkTarget(target)
	default:
		return gate.GoTestTarget{}, remoteGoBuildProfile{}, false, nil
	}
	if err != nil {
		return gate.GoTestTarget{}, remoteGoBuildProfile{}, false, err
	}
	return parsed, profile, true, nil
}

// workloadInputVoteDigests 使用调用方按包缓存的编译摘要，并独立计算声明与运行时观察输入。
func (snapshot *remoteGitTreeSnapshot) workloadInputVoteDigests(ctx context.Context, workload gate.Workload, compile string) (remoteWorkloadInputVoteDigests, bool, error) {
	parsed, profile, supported, err := remoteGoWorkloadInputTarget(workload)
	if err != nil || !supported {
		return remoteWorkloadInputVoteDigests{}, supported, err
	}
	declaration, err := snapshot.goExactTestDeclarationInputDigest(ctx, parsed, profile)
	if err != nil {
		return remoteWorkloadInputVoteDigests{}, false, err
	}
	runtime, fallback, err := snapshot.goExactTestRuntimeInputDigest(ctx, parsed, profile)
	if err != nil {
		return remoteWorkloadInputVoteDigests{}, false, err
	}
	return remoteWorkloadInputVoteDigests{compile: compile, declaration: declaration, runtime: runtime, runtimeFallback: fallback}, true, nil
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
	case gate.GoGuardTargetMcpLSPWorkloadCatalog:
		return snapshot.mcpLSPWorkloadCatalogGuardInputDigest(ctx)
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

const mcpLSPQuickWorkloadCatalogPath = catalog.Path

// mcpLSPWorkloadCatalogGuardInputDigest 计算 catalog 守卫真实读取闭包的摘要。
func (snapshot *remoteGitTreeSnapshot) mcpLSPWorkloadCatalogGuardInputDigest(ctx context.Context) (string, error) {
	raw, err := snapshot.exactTreeBlob(ctx, mcpLSPQuickWorkloadCatalogPath)
	if err != nil {
		return "", err
	}
	workloads, err := catalog.ParseImplementedRemoteTestsFromRaw(raw)
	if err != nil {
		return "", err
	}
	selected, err := snapshot.mcpLSPCatalogGuardBaseEntries(workloads)
	if err != nil {
		return "", err
	}
	packages, testEntries, err := snapshot.mcpLSPCatalogTestPackages(ctx, workloads)
	if err != nil {
		return "", err
	}
	for _, workload := range workloads {
		projection, err := catalog.ProjectRemoteTestCommand(workload.Command, catalog.DefaultRemoteTestEnvironment(), selectMcpLSPTestPackages(packages, workload.Command.Packages))
		if err != nil {
			return "", fmt.Errorf("workload %q: %w", workload.WorkloadID, err)
		}
		for _, input := range projection.InputFiles {
			selected[input] = testEntries[input]
		}
	}
	return snapshot.digestEntries(sortedRemoteGitTreeEntries(selected))
}

func (snapshot *remoteGitTreeSnapshot) mcpLSPCatalogGuardBaseEntries(workloads []catalog.ImplementedRemoteTest) (map[string]remoteGitTreeEntry, error) {
	selected := make(map[string]remoteGitTreeEntry)
	addMcpLSPCatalogGuardEntries(snapshot, selected)
	if err := addMcpLSPCatalogRequiredEntries(snapshot, selected); err != nil {
		return nil, err
	}
	if err := addMcpLSPCatalogProducerEntries(snapshot, selected, workloads); err != nil {
		return nil, err
	}
	return selected, nil
}

func addMcpLSPCatalogGuardEntries(snapshot *remoteGitTreeSnapshot, selected map[string]remoteGitTreeEntry) {
	for _, entry := range snapshot.entries {
		if mcpLSPWorkloadCatalogGuardInputEntry(entry) {
			selected[entry.path] = entry
		}
	}
}

func addMcpLSPCatalogRequiredEntries(snapshot *remoteGitTreeSnapshot, selected map[string]remoteGitTreeEntry) error {
	requiredPaths := []string{"go.mod", "go.sum", mcpLSPQuickWorkloadCatalogPath, "scripts/check_mcp_lsp_workload_catalog.sh"}
	for _, path := range catalog.WorkloadConsumerPaths() {
		requiredPaths = append(requiredPaths, path)
	}
	for _, required := range requiredPaths {
		entry, ok := snapshot.byPath[required]
		if !ok {
			return fmt.Errorf("MCP-LSP catalog guard required input %q is unavailable", required)
		}
		if entry.kind != "blob" {
			return fmt.Errorf("MCP-LSP catalog guard required input %q is unavailable", required)
		}
		selected[required] = entry
	}
	return nil
}

func addMcpLSPCatalogProducerEntries(snapshot *remoteGitTreeSnapshot, selected map[string]remoteGitTreeEntry, workloads []catalog.ImplementedRemoteTest) error {
	for _, workload := range workloads {
		if workload.ProducerImplementationStatus != "implemented" {
			continue
		}
		entry, ok := snapshot.byPath[workload.ProducerWorkflowPath]
		if ok && entry.kind == "blob" {
			selected[entry.path] = entry
			continue
		}
		return fmt.Errorf("MCP-LSP workload %q producer workflow %q is unavailable", workload.WorkloadID, workload.ProducerWorkflowPath)
	}
	return nil
}

func (snapshot *remoteGitTreeSnapshot) mcpLSPCatalogTestPackages(ctx context.Context, workloads []catalog.ImplementedRemoteTest) ([]catalog.RemoteTestPackageFiles, map[string]remoteGitTreeEntry, error) {
	packageDirectories := mcpLSPCatalogPackageDirectories(workloads)
	entries, err := mcpLSPCatalogTestEntries(snapshot, packageDirectories)
	if err != nil {
		return nil, nil, err
	}
	contents, err := snapshot.mcpLSPCatalogTestContents(ctx, entries)
	if err != nil {
		return nil, nil, err
	}
	packages, indexed := groupMcpLSPCatalogTestEntries(entries, packageDirectories, contents)
	slices.SortFunc(packages, func(left, right catalog.RemoteTestPackageFiles) int {
		return strings.Compare(left.Package, right.Package)
	})
	return packages, indexed, nil
}

func mcpLSPCatalogPackageDirectories(workloads []catalog.ImplementedRemoteTest) map[string]string {
	packageDirectories := make(map[string]string)
	for _, workload := range workloads {
		for _, packagePath := range workload.Command.Packages {
			packageDirectories[strings.TrimPrefix(packagePath, "./")] = packagePath
		}
	}
	return packageDirectories
}

// mcpLSPCatalogTestEntries 收集候选包目录下所有适用 Go 源文件输入。
func mcpLSPCatalogTestEntries(snapshot *remoteGitTreeSnapshot, packageDirectories map[string]string) ([]remoteGitTreeEntry, error) {
	entries := make([]remoteGitTreeEntry, 0)
	for _, entry := range snapshot.entries {
		if _, ok := packageDirectories[path.Dir(entry.path)]; !ok {
			continue
		}
		if entry.kind != "blob" || path.Ext(entry.path) != ".go" {
			continue
		}
		if entry.mode != "100644" && entry.mode != "100755" {
			return nil, fmt.Errorf("MCP-LSP catalog test input %q is not a regular blob", entry.path)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left, right remoteGitTreeEntry) int { return strings.Compare(left.path, right.path) })
	return entries, nil
}

// mcpLSPCatalogTestContents 从 exact tree 读取候选文件 blob 内容。
func (snapshot *remoteGitTreeSnapshot) mcpLSPCatalogTestContents(ctx context.Context, entries []remoteGitTreeEntry) (map[string][]byte, error) {
	contents := make(map[string][]byte, len(entries))
	missing := make([]string, 0)
	for _, entry := range entries {
		if raw, ok := snapshot.remoteGitBlob(entry.objectID); ok {
			contents[entry.path] = raw
			continue
		}
		missing = append(missing, entry.path)
	}
	if len(missing) == 0 {
		return contents, nil
	}
	loaded, err := snapshot.readGitBlobs(ctx, missing)
	if err != nil {
		return nil, err
	}
	maps.Copy(contents, loaded)
	return contents, nil
}

func groupMcpLSPCatalogTestEntries(entries []remoteGitTreeEntry, packageDirectories map[string]string, contents map[string][]byte) ([]catalog.RemoteTestPackageFiles, map[string]remoteGitTreeEntry) {
	grouped := make(map[string][]catalog.RemoteTestFile, len(packageDirectories))
	indexed := make(map[string]remoteGitTreeEntry, len(entries))
	for _, entry := range entries {
		packagePath := packageDirectories[path.Dir(entry.path)]
		grouped[packagePath] = append(grouped[packagePath], catalog.RemoteTestFile{Path: entry.path, Name: path.Base(entry.path), Source: contents[entry.path]})
		indexed[entry.path] = entry
	}
	packages := make([]catalog.RemoteTestPackageFiles, 0, len(packageDirectories))
	for _, packagePath := range packageDirectories {
		packages = append(packages, catalog.RemoteTestPackageFiles{Package: packagePath, Files: grouped[packagePath]})
	}
	return packages, indexed
}

// selectMcpLSPTestPackages 按 catalog package 投影选择并保持稳定顺序。
func selectMcpLSPTestPackages(packages []catalog.RemoteTestPackageFiles, selected []string) []catalog.RemoteTestPackageFiles {
	result := make([]catalog.RemoteTestPackageFiles, 0, len(selected))
	for _, packagePath := range selected {
		for _, packageFiles := range packages {
			if packageFiles.Package == packagePath {
				result = append(result, packageFiles)
				break
			}
		}
	}
	return result
}

// exactTreeBlob 从 immutable tree 读取单个真实输入 blob。
func (snapshot *remoteGitTreeSnapshot) exactTreeBlob(ctx context.Context, filePath string) ([]byte, error) {
	entry, ok := snapshot.byPath[filePath]
	if !ok || entry.kind != "blob" {
		return nil, fmt.Errorf("exact-tree blob %q is unavailable", filePath)
	}
	if raw, found := snapshot.remoteGitBlob(entry.objectID); found {
		return raw, nil
	}
	blobs, err := snapshot.readGitBlobs(ctx, []string{filePath})
	if err != nil {
		return nil, err
	}
	raw, ok := blobs[filePath]
	if !ok {
		return nil, fmt.Errorf("exact-tree blob %q was not returned", filePath)
	}
	return raw, nil
}

func mcpLSPWorkloadCatalogGuardInputEntry(entry remoteGitTreeEntry) bool {
	if strings.HasSuffix(entry.path, ".go") && !strings.HasSuffix(entry.path, "_test.go") {
		return true
	}
	return entry.path == "Makefile" || strings.HasSuffix(entry.path, ".sh")
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
	case gate.GateIDWorkflowActionlint:
		return snapshot.digestMatching(workflowActionlintInputEntry)
	default:
		return snapshot.digestEntries(snapshot.entries)
	}
}

func workflowActionlintInputEntry(entry remoteGitTreeEntry) bool {
	if entry.path == ".github/actionlint.yaml" {
		return true
	}
	return strings.HasPrefix(entry.path, ".github/workflows/") &&
		(strings.HasSuffix(entry.path, ".yml") || strings.HasSuffix(entry.path, ".yaml"))
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
