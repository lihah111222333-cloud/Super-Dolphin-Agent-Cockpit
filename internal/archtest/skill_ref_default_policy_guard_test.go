package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const dtoProviderImportPath = modulePath + "/internal/dto/provider"

func TestSkillRefFullModeLiteralGuard(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := walkGoFiles(t, root, "internal", "cmd")
	var violations []string
	for _, absPath := range files {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		if strings.HasPrefix(relPath, "internal/dto/provider/") {
			continue
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, absPath, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", relPath, err)
		}
		aliases := dtoProviderImportAliases(file)
		if len(aliases) == 0 {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isDTOProviderSkillRefLiteral(lit, aliases) {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Mode" {
					continue
				}
				if isDTOProviderSkillModeFull(kv.Value, aliases) {
					pos := fset.Position(kv.Value.Pos())
					violations = append(violations, relPath+":"+strconv.Itoa(pos.Line)+": raw dto.SkillRef{Mode: dto.SkillModeFull} literal bypasses provider-aware default policy; use an explicit helper/policy path instead")
				}
			}
			return true
		})
	}

	if len(violations) > 0 {
		t.Fatalf("SkillRef default policy guard violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

func dtoProviderImportAliases(file *ast.File) map[string]struct{} {
	aliases := map[string]struct{}{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != dtoProviderImportPath {
			continue
		}
		if imp.Name == nil {
			aliases["provider"] = struct{}{}
			continue
		}
		aliases[imp.Name.Name] = struct{}{}
	}
	return aliases
}

func isDTOProviderSkillRefLiteral(lit *ast.CompositeLit, aliases map[string]struct{}) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "SkillRef" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = aliases[ident.Name]
	return ok
}

func isDTOProviderSkillModeFull(expr ast.Expr, aliases map[string]struct{}) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if ok && sel.Sel.Name == "SkillModeFull" {
		ident, ok := sel.X.(*ast.Ident)
		if ok {
			_, ok = aliases[ident.Name]
			return ok
		}
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident.Name != "SkillModeFull" {
		return false
	}
	_, ok = aliases["."]
	return ok
}
