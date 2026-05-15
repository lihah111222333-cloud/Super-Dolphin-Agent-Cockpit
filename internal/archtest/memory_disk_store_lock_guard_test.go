package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMemoryDiskStoreLocksOwnedByCoordinator ensures that:
//  1. No package-level mutable var named diskStoreLocks exists.
//  2. No package-level function named withDiskStoreLock exists.
//  3. consolidation_lock.go defines diskLockCoordinator as a struct
//     owning a sync.Map named "locks".
//  4. consolidation_lock.go exposes withDiskStoreLock as a method on
//     diskLockCoordinator (not on diskStore).
//  5. diskStore (store.go) holds a *diskLockCoordinator field named "locks".
func TestMemoryDiskStoreLocksOwnedByCoordinator(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	memoryDir := filepath.Join(root, "internal", "module", "memory")

	violations := collectForbiddenDiskLockGlobals(t, memoryDir)
	violations = append(violations, validateDiskLockCoordinatorShape(t, memoryDir)...)
	violations = append(violations, validateDiskStoreLockCoordinatorField(t, memoryDir)...)

	if len(violations) > 0 {
		t.Fatalf("memory disk lock coordinator ownership violations:\n%s", strings.Join(violations, "\n"))
	}
}

func collectForbiddenDiskLockGlobals(t *testing.T, memoryDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		t.Fatalf("read %s: %v", memoryDir, err)
	}
	var violations []string
	for _, entry := range entries {
		name := entry.Name()
		if skipMemorySourceFile(entry, name) {
			continue
		}
		path := filepath.Join(memoryDir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		rel := filepath.ToSlash(filepath.Join("internal/module/memory", name))
		violations = append(violations, forbiddenDiskLockDeclViolations(rel, file.Decls)...)
	}
	return violations
}

func skipMemorySourceFile(entry os.DirEntry, name string) bool {
	return entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go")
}

func forbiddenDiskLockDeclViolations(rel string, decls []ast.Decl) []string {
	var violations []string
	for _, decl := range decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			violations = append(violations, forbiddenDiskLockVarViolations(rel, gen)...)
			continue
		}
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "withDiskStoreLock" && fn.Recv == nil {
			violations = append(violations, rel+": withDiskStoreLock must not be a package-level function")
		}
	}
	return violations
}

func forbiddenDiskLockVarViolations(rel string, gen *ast.GenDecl) []string {
	if gen.Tok != token.VAR {
		return nil
	}
	var violations []string
	for _, spec := range gen.Specs {
		varSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		violations = append(violations, forbiddenDiskLockNameViolations(rel, varSpec)...)
	}
	return violations
}

func forbiddenDiskLockNameViolations(rel string, spec *ast.ValueSpec) []string {
	var violations []string
	for _, ident := range spec.Names {
		if ident.Name == "diskStoreLocks" {
			violations = append(violations, rel+": package-level diskStoreLocks must not exist")
		}
		if isDiskLockSyncMap(ident.Name, spec.Type) {
			violations = append(violations, rel+": package-level disk lock sync.Map must not exist")
		}
	}
	return violations
}

func isDiskLockSyncMap(name string, expr ast.Expr) bool {
	lowerName := strings.ToLower(name)
	return strings.Contains(lowerName, "disk") && strings.Contains(lowerName, "lock") && isSyncMapType(expr)
}

func validateDiskLockCoordinatorShape(t *testing.T, memoryDir string) []string {
	t.Helper()
	coordFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(memoryDir, "consolidation_lock.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse consolidation_lock.go: %v", err)
	}
	var violations []string
	if !structHasSyncMapField(coordFile, "diskLockCoordinator", "locks") {
		violations = append(violations, "internal/module/memory/consolidation_lock.go: diskLockCoordinator must own locks sync.Map")
	}
	if !hasMethod(coordFile, "diskLockCoordinator", "withDiskStoreLock") {
		violations = append(violations, "internal/module/memory/consolidation_lock.go: diskLockCoordinator must expose withDiskStoreLock as a method")
	}
	return violations
}

func validateDiskStoreLockCoordinatorField(t *testing.T, memoryDir string) []string {
	t.Helper()
	storeFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(memoryDir, "store.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse store.go: %v", err)
	}
	if !structHasPointerField(storeFile, "diskStore", "locks", "diskLockCoordinator") {
		return []string{"internal/module/memory/store.go: diskStore must hold a *diskLockCoordinator field named locks"}
	}
	return nil
}

func isSyncMapType(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "sync" && selector.Sel.Name == "Map"
}

func structHasSyncMapField(file *ast.File, structName, fieldName string) bool {
	structType := findStructType(file, structName)
	return structType != nil && structHasField(structType, fieldName, isSyncMapType)
}

func structHasPointerField(file *ast.File, structName, fieldName, pointedType string) bool {
	structType := findStructType(file, structName)
	return structType != nil && structHasField(structType, fieldName, func(expr ast.Expr) bool {
		return isPointerToType(expr, pointedType)
	})
}

func findStructType(file *ast.File, structName string) *ast.StructType {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		if structType := findStructTypeSpec(gen.Specs, structName); structType != nil {
			return structType
		}
	}
	return nil
}

func findStructTypeSpec(specs []ast.Spec, structName string) *ast.StructType {
	for _, spec := range specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != structName {
			continue
		}
		if structType, ok := typeSpec.Type.(*ast.StructType); ok {
			return structType
		}
	}
	return nil
}

func structHasField(structType *ast.StructType, fieldName string, matchType func(ast.Expr) bool) bool {
	for _, field := range structType.Fields.List {
		if !matchType(field.Type) {
			continue
		}
		if fieldHasName(field, fieldName) {
			return true
		}
	}
	return false
}

func fieldHasName(field *ast.Field, fieldName string) bool {
	for _, name := range field.Names {
		if name.Name == fieldName {
			return true
		}
	}
	return false
}

func isPointerToType(expr ast.Expr, pointedType string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == pointedType
}

func hasMethod(file *ast.File, receiverType, methodName string) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != methodName || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		if receiverBaseName(fn.Recv.List[0].Type) == receiverType {
			return true
		}
	}
	return false
}

func receiverBaseName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return receiverBaseName(expr.X)
	default:
		return ""
	}
}
