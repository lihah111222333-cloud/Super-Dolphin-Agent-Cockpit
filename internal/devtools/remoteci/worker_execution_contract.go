package remoteci

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	workerExecutionContractSchemaVersion = 4
	workerExecutionPlatform              = cicontract.TargetPlatform
)

type workerExecutionRoot struct {
	directory string
	symbol    string
}

var errWorkerExecutionRootUnavailable = errors.New("worker execution root is unavailable in historical tree")

// workerExecutionRoots are worker process boundaries and the small, canonical
// request helpers that define how that process is started. Coordinator
// orchestration, resource planning, job identity, and ECI bookkeeping are not
// roots; their non-semantic edits must not invalidate EnvironmentV10.
var workerExecutionRoots = []workerExecutionRoot{
	{directory: "cmd/super-dolphin-gate", symbol: "stageRemoteSourceObjects"},
	{directory: "cmd/super-dolphin-gate", symbol: "verifyRemoteMaterializedGateCLICompileClosure"},
	{directory: "cmd/super-dolphin-gate", symbol: "verifyRemoteOCIProjectCache"},
	{directory: "cmd/super-dolphin-gate", symbol: "verifyRemoteSourceManifestBinding"},
	{directory: "internal/devtools/gate", symbol: "ensureJSONEOF"},
	{directory: "internal/devtools/gate", symbol: "executeProgram"},
	{directory: "internal/devtools/gate", symbol: "sourceVariantCount"},
	{directory: "internal/devtools/gate", symbol: "validateCommitSource"},
	{directory: "internal/devtools/gate", symbol: "validateOID"},
	{directory: "internal/devtools/gate", symbol: "validateRangeSource"},
	{directory: "internal/devtools/gate", symbol: "validateTreeSource"},
	{directory: "internal/devtools/remoteci", symbol: "checkoutMaterializedSource"},
	{directory: "internal/devtools/remoteci", symbol: "importSourceWorktreeBundle"},
	{directory: "internal/devtools/remoteci", symbol: "materializeSourceWorktree"},
	{directory: "internal/devtools/remoteci", symbol: "remoteShardBootstrapSH"},
	{directory: "internal/devtools/remoteci", symbol: "remoteWorkerEnvironment"},
	{directory: "internal/devtools/remoteci", symbol: "remoteWorkerSupervisorCommand"},
	{directory: "internal/devtools/remoteci", symbol: "validateCanonicalDirectory"},
	{directory: "internal/devtools/remoteci", symbol: "validateManifestCommitIdentity"},
	{directory: "internal/devtools/remoteci", symbol: "validateManifestIdentityFields"},
	{directory: "internal/devtools/remoteci", symbol: "validateManifestSyntheticBase"},
	{directory: "internal/devtools/remoteci", symbol: "validatePublishedArtifacts"},
	{directory: "internal/devtools/remoteci", symbol: "validateSourceBaseline"},
	{directory: "internal/devtools/remoteci", symbol: "verifyPublishedSourceBundle"},
}

// workerExecutionPreviousPreciseV4Roots 冻结收窄执行边界前的 stable-key 根集合。
// 它只用于验证历史 PASS 的来源环境，不生产新的 worker 身份。
func workerExecutionPreviousPreciseV4Roots() []workerExecutionRoot {
	return []workerExecutionRoot{
		{directory: "cmd/super-dolphin-gate", symbol: "runRemoteMaterialize"},
		{directory: "cmd/super-dolphin-gate", symbol: "runWorkerCLI"},
		{directory: "internal/devtools/remoteci", symbol: "remoteShardBootstrapSH"},
		{directory: "internal/devtools/remoteci", symbol: "remoteWorkerEnvironment"},
		{directory: "internal/devtools/remoteci", symbol: "remoteWorkerSupervisorCommand"},
	}
}

// workerExecutionLegacyV4Roots freezes the pre-precision v4 contract for
// migration proof only. It is never used for new PASS identity production.
func workerExecutionLegacyV4Roots() []workerExecutionRoot {
	return []workerExecutionRoot{
		{directory: "cmd/super-dolphin-gate", symbol: "runRemoteMaterialize"},
		{directory: "cmd/super-dolphin-gate", symbol: "runWorkerCLI"},
		{directory: "internal/devtools/remoteci", symbol: "createRequest"},
	}
}

type workerExecutionGoImport struct {
	importPath string
	directory  string
	local      bool
}

type workerExecutionGoUnit struct {
	key          string
	directory    string
	filePath     string
	packageName  string
	kind         string
	names        []string
	receiver     string
	source       []byte
	fileSet      *token.FileSet
	node         ast.Node
	content      ast.Node
	signature    ast.Node
	dependencies ast.Node
	localTypes   map[string]ast.Expr
	localNames   map[string]struct{}
	imports      map[string]workerExecutionGoImport
}

type workerExecutionGoIndex struct {
	symbols                    map[string]map[string][]*workerExecutionGoUnit
	methods                    map[string]map[string][]*workerExecutionGoUnit
	receiverMethods            map[string]map[string][]*workerExecutionGoUnit
	initializers               map[string][]*workerExecutionGoUnit
	routes                     map[string]map[string][]*workerExecutionGoUnit
	parseErrors                map[string][]error
	unitKeyStrategy            workerExecutionUnitKeyStrategy
	previousGroupedDeclaration bool
	unitKeyOrdinals            map[string]int
}

type workerExecutionUnitKeyStrategy uint8

const (
	workerExecutionStableUnitKeys workerExecutionUnitKeyStrategy = iota
	workerExecutionPositionalUnitKeys
)

type workerExecutionGoClosure struct {
	index                     *workerExecutionGoIndex
	selected                  map[string]*workerExecutionGoUnit
	usedImports               map[string]map[string]struct{}
	reached                   map[string]struct{}
	queue                     []*workerExecutionGoUnit
	resolved                  int
	commands                  [][]string
	includeAllReceiverMethods bool
}

type workerExecutionFragment struct {
	kind    string
	path    string
	name    string
	content []byte
}

type workerExecutionAssets struct {
	snapshot       *remoteGitTreeSnapshot
	entries        map[string]remoteGitTreeEntry
	fragments      map[string]workerExecutionFragment
	scannedScripts map[string]struct{}
	scriptQueue    []string
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func validateWorkerExecutionRoots(roots []workerExecutionRoot) error {
	if len(roots) == 0 {
		return errors.New("worker execution roots are empty")
	}
	previous := workerExecutionRoot{}
	for index, root := range roots {
		if !validRemoteGitTreePath(root.directory) || strings.TrimSpace(root.symbol) == "" {
			return errors.New("worker execution root is invalid")
		}
		if index > 0 && (root.directory < previous.directory ||
			(root.directory == previous.directory && root.symbol <= previous.symbol)) {
			return errors.New("worker execution roots are not strictly sorted")
		}
		previous = root
	}
	return nil
}

// workerExecutionSourceDiagnostic 只输出 exact-tree 源码覆盖计数，定位 root 缺失而不泄露源码内容。
func (snapshot *remoteGitTreeSnapshot) workerExecutionSourceDiagnostic() string {
	if snapshot == nil {
		return "snapshot=nil"
	}
	counts := make(map[string]int)
	for filePath := range snapshot.goSources {
		for _, directory := range []string{"cmd/super-dolphin-gate", "internal/devtools/gate", "internal/devtools/remoteci"} {
			if strings.HasPrefix(filePath, directory+"/") {
				counts[directory]++
			}
		}
	}
	return fmt.Sprintf("tree=%s go_sources=%d cmd_sources=%d gate_sources=%d remoteci_sources=%d",
		snapshot.tree, len(snapshot.goSources), counts["cmd/super-dolphin-gate"], counts["internal/devtools/gate"], counts["internal/devtools/remoteci"])
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func (snapshot *remoteGitTreeSnapshot) workerExecutionContractDigest(ctx context.Context) (string, error) {
	if err := validateWorkerExecutionRoots(workerExecutionRoots); err != nil {
		return "", err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	closure, err := snapshot.resolveWorkerExecutionClosure()
	if err != nil {
		return "", err
	}
	assets := &workerExecutionAssets{
		snapshot:       snapshot,
		entries:        make(map[string]remoteGitTreeEntry),
		fragments:      make(map[string]workerExecutionFragment),
		scannedScripts: make(map[string]struct{}),
	}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		return "", err
	}
	if err := assets.resolveWorkerExecutionAssets(ctx, closure); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionExecutorConfigFragment(); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionSourceManifestAsset(); err != nil {
		return "", err
	}
	return digestWorkerExecutionClosure(closure, assets)
}

// workerExecutionContractDigestPreviousGroupedDeclarationV4 重建 ValueSpec
// 收窄前的精确 v4 摘要；只用于验证既有 PASS 来源环境。
func (snapshot *remoteGitTreeSnapshot) workerExecutionContractDigestPreviousGroupedDeclarationV4(ctx context.Context) (string, error) {
	if err := validateWorkerExecutionRoots(workerExecutionRoots); err != nil {
		return "", err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	closure, err := snapshot.resolveWorkerExecutionClosurePreviousGroupedDeclaration()
	if err != nil {
		return "", err
	}
	assets := &workerExecutionAssets{
		snapshot:       snapshot,
		entries:        make(map[string]remoteGitTreeEntry),
		fragments:      make(map[string]workerExecutionFragment),
		scannedScripts: make(map[string]struct{}),
	}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		return "", err
	}
	if err := assets.resolveWorkerExecutionAssetsPreviousGroupedDeclaration(ctx, closure); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionExecutorConfigFragment(); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionSourceManifestAsset(); err != nil {
		return "", err
	}
	return digestWorkerExecutionClosure(closure, assets)
}

// workerExecutionContractDigestLegacyV4 recomputes the broad v4 digest that
// older PASS evidence recorded. It exists solely for source-tree migration
// proof; callers must not use it as the current worker identity.
// 仅用于验证历史来源树的旧环境证据，不参与新的复用身份计算。
func (snapshot *remoteGitTreeSnapshot) workerExecutionContractDigestLegacyV4(ctx context.Context) (string, error) {
	roots := workerExecutionLegacyV4Roots()
	if err := validateWorkerExecutionRoots(roots); err != nil {
		return "", err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	closure, err := snapshot.resolveWorkerExecutionClosureWithRootsAndKeyStrategy(
		roots, true, workerExecutionPositionalUnitKeys,
	)
	if err != nil {
		return "", err
	}
	assets := &workerExecutionAssets{
		snapshot:       snapshot,
		entries:        make(map[string]remoteGitTreeEntry),
		fragments:      make(map[string]workerExecutionFragment),
		scannedScripts: make(map[string]struct{}),
	}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		return "", err
	}
	if err := assets.resolveWorkerExecutionAssets(ctx, closure); err != nil {
		return "", err
	}
	return digestWorkerExecutionClosure(closure, assets)
}

// workerExecutionContractDigestPreviousPreciseV4 仅重建稳定语义键修复前的精确 v4 摘要，用于验证已存 PASS 来源环境。
func (snapshot *remoteGitTreeSnapshot) workerExecutionContractDigestPreviousPreciseV4(ctx context.Context) (string, error) {
	roots := workerExecutionPreviousPreciseV4Roots()
	if err := validateWorkerExecutionRoots(roots); err != nil {
		return "", err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	closure, err := snapshot.resolveWorkerExecutionClosureWithRootsAndKeyStrategy(
		roots, false, workerExecutionPositionalUnitKeys,
	)
	if err != nil {
		return "", err
	}
	assets := &workerExecutionAssets{
		snapshot:       snapshot,
		entries:        make(map[string]remoteGitTreeEntry),
		fragments:      make(map[string]workerExecutionFragment),
		scannedScripts: make(map[string]struct{}),
	}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		return "", err
	}
	if err := assets.resolveWorkerExecutionAssets(ctx, closure); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
		return "", err
	}
	return digestWorkerExecutionClosure(closure, assets)
}

// workerExecutionContractDigestPreviousStableV4 重建收窄根集合前的 stable-key 摘要。
// 该摘要只能证明历史来源环境，不能替代当前精确 worker 执行摘要。
func (snapshot *remoteGitTreeSnapshot) workerExecutionContractDigestPreviousStableV4(ctx context.Context) (string, error) {
	roots := workerExecutionPreviousPreciseV4Roots()
	if err := validateWorkerExecutionRoots(roots); err != nil {
		return "", err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return "", err
	}
	closure, err := snapshot.resolveWorkerExecutionClosureWithRoots(roots, false)
	if err != nil {
		return "", err
	}
	assets := &workerExecutionAssets{
		snapshot: snapshot, entries: make(map[string]remoteGitTreeEntry),
		fragments: make(map[string]workerExecutionFragment), scannedScripts: make(map[string]struct{}),
	}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		return "", err
	}
	if err := assets.resolveWorkerExecutionAssets(ctx, closure); err != nil {
		return "", err
	}
	if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
		return "", err
	}
	return digestWorkerExecutionClosure(closure, assets)
}

// resolveWorkerExecutionClosure 解析全部受控根的 Go 依赖闭包。
func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosure() (*workerExecutionGoClosure, error) {
	return snapshot.resolveWorkerExecutionClosureWithRoots(workerExecutionRoots, false)
}

func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosurePreviousGroupedDeclaration() (*workerExecutionGoClosure, error) {
	index := snapshot.buildWorkerExecutionGoIndexPreviousGroupedDeclaration()
	closure := newWorkerExecutionGoClosure(index)
	for _, root := range workerExecutionRoots {
		unit, err := index.resolveRoot(root)
		if err != nil {
			return nil, err
		}
		closure.enqueue(unit)
	}
	if err := closure.resolve(); err != nil {
		return nil, err
	}
	if err := closure.resolveSelfCommands(); err != nil {
		return nil, err
	}
	return closure, nil
}

func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosureWithRoots(
	roots []workerExecutionRoot,
	includeAllReceiverMethods bool,
) (*workerExecutionGoClosure, error) {
	return snapshot.resolveWorkerExecutionClosureWithRootsAndKeyStrategy(
		roots, includeAllReceiverMethods, workerExecutionStableUnitKeys,
	)
}

func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosureWithRootsAndKeyStrategy(
	roots []workerExecutionRoot,
	includeAllReceiverMethods bool,
	strategy workerExecutionUnitKeyStrategy,
) (*workerExecutionGoClosure, error) {
	index := snapshot.buildWorkerExecutionGoIndexWithKeyStrategy(strategy)
	closure := newWorkerExecutionGoClosure(index)
	closure.includeAllReceiverMethods = includeAllReceiverMethods
	for _, root := range roots {
		unit, err := index.resolveRoot(root)
		if err != nil {
			return nil, err
		}
		closure.enqueue(unit)
	}
	if err := closure.resolve(); err != nil {
		return nil, err
	}
	if err := closure.resolveSelfCommands(); err != nil {
		return nil, err
	}
	return closure, nil
}
