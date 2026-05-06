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
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		t.Fatalf("read %s: %v", memoryDir, err)
	}

	var violations []string

	// ---- Pass 1: scan all files for forbidden package-level patterns ----
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(memoryDir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				if decl.Tok != token.VAR {
					continue
				}
				for _, spec := range decl.Specs {
					varSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, ident := range varSpec.Names {
						if ident.Name == "diskStoreLocks" {
							violations = append(violations, filepath.ToSlash(filepath.Join("internal/module/memory", name))+": package-level diskStoreLocks must not exist")
						}
					}
					for _, ident := range varSpec.Names {
						if strings.Contains(strings.ToLower(ident.Name), "disk") && strings.Contains(strings.ToLower(ident.Name), "lock") && isSyncMapType(varSpec.Type) {
							violations = append(violations, filepath.ToSlash(filepath.Join("internal/module/memory", name))+": package-level disk lock sync.Map must not exist")
						}
					}
				}
			case *ast.FuncDecl:
				if decl.Name.Name == "withDiskStoreLock" && decl.Recv == nil {
					violations = append(violations, filepath.ToSlash(filepath.Join("internal/module/memory", name))+": withDiskStoreLock must not be a package-level function")
				}
			}
		}
	}

	// ---- Pass 2: consolidation_lock.go — coordinator struct ----
	coordFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(memoryDir, "consolidation_lock.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse consolidation_lock.go: %v", err)
	}
	if !structHasSyncMapField(coordFile, "diskLockCoordinator", "locks") {
		violations = append(violations, "internal/module/memory/consolidation_lock.go: diskLockCoordinator must own locks sync.Map")
	}
	if !hasMethod(coordFile, "diskLockCoordinator", "withDiskStoreLock") {
		violations = append(violations, "internal/module/memory/consolidation_lock.go: diskLockCoordinator must expose withDiskStoreLock as a method")
	}

	// ---- Pass 3: store.go — diskStore holds *diskLockCoordinator ----
	storeFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(memoryDir, "store.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse store.go: %v", err)
	}
	if !structHasPointerField(storeFile, "diskStore", "locks", "diskLockCoordinator") {
		violations = append(violations, "internal/module/memory/store.go: diskStore must hold a *diskLockCoordinator field named locks")
	}

	if len(violations) > 0 {
		t.Fatalf("memory disk lock coordinator ownership violations:\n%s", strings.Join(violations, "\n"))
	}
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
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structType.Fields.List {
				if !isSyncMapType(field.Type) {
					continue
				}
				for _, name := range field.Names {
					if name.Name == fieldName {
						return true
					}
				}
			}
		}
	}
	return false
}

func structHasPointerField(file *ast.File, structName, fieldName, pointedType string) bool {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structType.Fields.List {
				star, ok := field.Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				ident, ok := star.X.(*ast.Ident)
				if !ok || ident.Name != pointedType {
					continue
				}
				for _, name := range field.Names {
					if name.Name == fieldName {
						return true
					}
				}
			}
		}
	}
	return false
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
