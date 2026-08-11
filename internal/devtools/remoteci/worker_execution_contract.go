package remoteci

import (
	"context"
	"errors"
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

// workerExecutionRoots are worker process boundaries and the small, canonical
// request helpers that define how that process is started. Coordinator
// orchestration, resource planning, job identity, and ECI bookkeeping are not
// roots; their non-semantic edits must not invalidate EnvironmentV10.
var workerExecutionRoots = []workerExecutionRoot{
	{directory: "cmd/super-dolphin-gate", symbol: "runRemoteMaterialize"},
	{directory: "cmd/super-dolphin-gate", symbol: "runWorkerCLI"},
	{directory: "internal/devtools/remoteci", symbol: "remoteShardBootstrapSH"},
	{directory: "internal/devtools/remoteci", symbol: "remoteWorkerEnvironment"},
	{directory: "internal/devtools/remoteci", symbol: "remoteWorkerSupervisorCommand"},
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
	symbols         map[string]map[string][]*workerExecutionGoUnit
	methods         map[string]map[string][]*workerExecutionGoUnit
	receiverMethods map[string]map[string][]*workerExecutionGoUnit
	initializers    map[string][]*workerExecutionGoUnit
	routes          map[string]map[string][]*workerExecutionGoUnit
	parseErrors     map[string][]error
}

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
	closure, err := snapshot.resolveWorkerExecutionClosureWithRoots(roots, true)
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

// resolveWorkerExecutionClosure 解析全部受控根的 Go 依赖闭包。
func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosure() (*workerExecutionGoClosure, error) {
	return snapshot.resolveWorkerExecutionClosureWithRoots(workerExecutionRoots, false)
}

func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosureWithRoots(
	roots []workerExecutionRoot,
	includeAllReceiverMethods bool,
) (*workerExecutionGoClosure, error) {
	index := snapshot.buildWorkerExecutionGoIndex()
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
