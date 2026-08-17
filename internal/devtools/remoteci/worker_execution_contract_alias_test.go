package remoteci

import (
	"go/ast"
	"testing"
)

func TestWorkerExecutionImportedLocalTypeAliasMethodResolves(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{
		moduleMappings: []remoteGoModuleMapping{{importPath: "example.invalid/fixture", directory: "."}},
		goSources: map[string][]byte{
			"fixture.go": []byte(`package fixture

import "example.invalid/fixture/owner"

type Cache = owner.Cache
func root(cache Cache) { cache.Validate() }
`),
			"owner/cache.go": []byte(`package owner

type Cache struct{}
func (Cache) Validate() { validateDependency() }
func validateDependency() {}
`),
		},
	}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("imported local type alias method was not resolved precisely: %v", err)
	}
}

func callSelector(call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	if call == nil {
		return nil, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return selector, ok
}

func TestWorkerExecutionLocalMethodExternalResultResolves(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "bytes"

type Runner struct{}
func (Runner) command() (*bytes.Buffer, error) { return &bytes.Buffer{}, nil }
func root(runner Runner) { command, err := runner.command(); if err != nil { return }; _ = command.String() }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	var commandCall *ast.CallExpr
	ast.Inspect(root.node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		selector, selected := callSelector(call)
		if ok && selected && selector.Sel.Name == "command" {
			commandCall = call
		}
		return true
	})
	if commandCall == nil || !workerExecutionExternalCallResult(closure, root, commandCall, 0, map[string]struct{}{}) {
		t.Fatal("local method external result was not classified by its method declaration")
	}
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("local method external result was not resolved precisely: %v", err)
	}
}
