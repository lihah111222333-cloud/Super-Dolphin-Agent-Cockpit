package skill

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMirrorLockOwnerArchGuard 禁止恢复包级锁 singleton 或无 owner 的发布入口。
func TestMirrorLockOwnerArchGuard(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	path := filepath.Join(filepath.Dir(thisFile), "mirror_publisher.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var publish *ast.FuncDecl
	var cleanup *ast.FuncDecl
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if ok && gen.Tok == token.VAR {
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range valueSpec.Names {
					if name.Name == "skillMirrorRootLocks" {
						t.Fatal("package-level skillMirrorRootLocks singleton is forbidden")
					}
				}
			}
		}
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "PublishSkillMirrors" {
			publish = fn
		}
	}
	cleanupPath := filepath.Join(filepath.Dir(thisFile), "mirror_publisher_write_time.go")
	cleanupFile, err := parser.ParseFile(token.NewFileSet(), cleanupPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", cleanupPath, err)
	}
	for _, decl := range cleanupFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "cleanupSuppressedPersonalMirrorRecord" {
			cleanup = fn
		}
	}
	assertMirrorLockOwnerParameter(t, "PublishSkillMirrors", publish)
	assertMirrorLockOwnerParameter(t, "cleanupSuppressedPersonalMirrorRecord", cleanup)
}

// assertMirrorLockOwnerParameter 验证所有直接 mirror 写入口都显式接收锁 owner。
func assertMirrorLockOwnerParameter(t *testing.T, name string, fn *ast.FuncDecl) {
	t.Helper()
	if fn == nil || fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		t.Fatalf("%s declaration is missing", name)
	}
	first, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		t.Fatalf("%s first parameter must be *MirrorRootLockRegistry", name)
	}
	owner, ok := first.X.(*ast.Ident)
	if !ok || owner.Name != "MirrorRootLockRegistry" {
		t.Fatalf("%s first parameter must be *MirrorRootLockRegistry", name)
	}
}
