package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const standaloneServeSource = "cmd/mcp-orch/serve.go"

// TestStandaloneServeKeepsSingletonBeforeDatabaseAndApp prevents a future
// serve edit from opening product state or constructing the app before the
// state-domain owner is acquired.
func TestStandaloneServeKeepsSingletonBeforeDatabaseAndApp(t *testing.T) {
	t.Parallel()

	root := standaloneServeGuardRepoRoot(t)
	violations := parseStandaloneServeSingletonViolations(t, filepath.Join(root, filepath.FromSlash(standaloneServeSource)))
	if len(violations) != 0 {
		t.Fatalf("standalone serve singleton violations: %s", strings.Join(violations, "; "))
	}
}

func standaloneServeGuardRepoRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve standalone serve guard source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

// TestStandaloneServeSingletonGuardRejectsUnsafeFixture keeps this guard
// fail-closed: a source fixture that opens state before acquiring its owner is
// rejected even when the production file is currently safe.
func TestStandaloneServeSingletonGuardRejectsUnsafeFixture(t *testing.T) {
	t.Parallel()

	const source = `package main
func runServe() error {
		db, err := openStandaloneDatabaseWithLock(ctx, cfg, nil)
		if err != nil { return err }
		lock, err := standalone.AcquireInstance(cfg.StateDir)
		if err != nil { return err }
		_ = newStandaloneAppWithPrincipalIssuer(cfg, token, ready, db, issuer, lock)
		defer lock.Close()
		return nil
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "serve_fixture.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	violations := standaloneServeSingletonViolationsFromAST(file)
	if len(violations) == 0 {
		t.Fatal("unsafe standalone serve fixture passed singleton guard")
	}
}

func parseStandaloneServeSingletonViolations(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", standaloneServeSource, err)
	}
	return standaloneServeSingletonViolationsFromAST(file)
}

func standaloneServeSingletonViolationsFromAST(file *ast.File) []string {
	runServe := standaloneServeFunc(file)
	if runServe == nil || runServe.Body == nil {
		return []string{"runServe function is missing or has no body"}
	}
	facts := collectStandaloneServeFacts(runServe)
	return validateStandaloneServeFacts(facts)
}

type standaloneServeFacts struct {
	positions               map[string]token.Pos
	hasCanonicalStateRebind bool
	hasDeferredLockRelease  bool
}

func collectStandaloneServeFacts(runServe *ast.FuncDecl) standaloneServeFacts {
	facts := standaloneServeFacts{positions: make(map[string]token.Pos)}
	ast.Inspect(runServe.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := standaloneServeSelectorName(call.Fun)
		switch name {
		case "standalone.AcquireInstance", "openStandaloneDatabaseWithLock", "newStandaloneAppWithPrincipalIssuer":
			facts.positions[name] = call.Pos()
		case "lock.CanonicalStateDir":
			facts.hasCanonicalStateRebind = true
		}
		return true
	})
	ast.Inspect(runServe.Body, func(node ast.Node) bool {
		deferCall, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(deferCall.Call, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok && standaloneServeSelectorName(call.Fun) == "lock.Close" {
				facts.hasDeferredLockRelease = true
			}
			return true
		})
		return true
	})
	return facts
}

func validateStandaloneServeFacts(facts standaloneServeFacts) []string {
	var violations []string
	for _, required := range []string{"standalone.AcquireInstance", "openStandaloneDatabaseWithLock", "newStandaloneAppWithPrincipalIssuer"} {
		if _, ok := facts.positions[required]; !ok {
			violations = append(violations, "runServe is missing "+required)
		}
	}
	violations = append(violations, standaloneServeOrderViolations(facts.positions)...)
	if !facts.hasCanonicalStateRebind {
		violations = append(violations, "database/app paths are not rebound to lock canonical state directory")
	}
	if !facts.hasDeferredLockRelease {
		violations = append(violations, "instance lock has no deferred Close release")
	}
	return violations
}

func standaloneServeOrderViolations(positions map[string]token.Pos) []string {
	acquire, acquired := positions["standalone.AcquireInstance"]
	database, databaseOpened := positions["openStandaloneDatabaseWithLock"]
	app, appConstructed := positions["newStandaloneAppWithPrincipalIssuer"]
	if !acquired {
		return nil
	}
	var violations []string
	if databaseOpened && acquire > database {
		violations = append(violations, "database opens before standalone owner acquisition")
	}
	if appConstructed && acquire > app {
		violations = append(violations, "app is constructed before standalone owner acquisition")
	}
	return violations
}

func standaloneServeFunc(file *ast.File) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "runServe" {
			return function
		}
	}
	return nil
}

func standaloneServeSelectorName(expression ast.Expr) string {
	switch selector := expression.(type) {
	case *ast.SelectorExpr:
		qualifier := standaloneServeSelectorName(selector.X)
		if qualifier == "" {
			if ident, ok := selector.X.(*ast.Ident); ok {
				return ident.Name + "." + selector.Sel.Name
			}
			return selector.Sel.Name
		}
		return qualifier + "." + selector.Sel.Name
	case *ast.Ident:
		return selector.Name
	default:
		return ""
	}
}
