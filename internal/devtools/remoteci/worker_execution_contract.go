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
	workerExecutionContractSchemaVersion = 3
	workerExecutionPlatform              = cicontract.TargetPlatform
)

type workerExecutionRoot struct {
	directory string
	symbol    string
}

// workerExecutionRoots are process boundaries, not source-file registrations.
// Every production declaration reachable from these roots is derived from the
// cicontract.TargetPlatform Git tree below.
var workerExecutionRoots = []workerExecutionRoot{
	{directory: "cmd/super-dolphin-gate", symbol: "runRemoteMaterialize"},
	{directory: "cmd/super-dolphin-gate", symbol: "runWorkerCLI"},
	{directory: "internal/devtools/remoteci", symbol: "createRequest"},
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
	index       *workerExecutionGoIndex
	selected    map[string]*workerExecutionGoUnit
	usedImports map[string]map[string]struct{}
	reached     map[string]struct{}
	queue       []*workerExecutionGoUnit
	resolved    int
	commands    [][]string
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

type workerExecutionMakeTarget struct {
	name         string
	dependencies []string
	content      []byte
}

type workerExecutionMakeVariable struct {
	name    string
	value   string
	content []byte
}

type workerExecutionMakefile struct {
	targets   map[string]workerExecutionMakeTarget
	variables map[string]workerExecutionMakeVariable
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
	if err := assets.resolveWorkerExecutionAssets(ctx, closure); err != nil {
		return "", err
	}
	return digestWorkerExecutionClosure(closure, assets)
}

// resolveWorkerExecutionClosure 解析全部受控根的 Go 依赖闭包。
func (snapshot *remoteGitTreeSnapshot) resolveWorkerExecutionClosure() (*workerExecutionGoClosure, error) {
	index := snapshot.buildWorkerExecutionGoIndex()
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
