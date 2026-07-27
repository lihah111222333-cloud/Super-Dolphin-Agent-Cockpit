package archtest_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestHistoricalRefactorBaselineIsAbsent 锁定已被真实 freeze registry 取代的零值基线不再回流。
func TestHistoricalRefactorBaselineIsAbsent(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "guards", "refactor_baseline.json")
	_, err := os.Stat(path)
	if err == nil {
		t.Fatal("historical internal/guards/refactor_baseline.json must remain deleted")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat historical refactor baseline: %v", err)
	}
}

// TestOrphanGuardManifestIsAbsent 锁定无 consumer、空 guards 的旧 V3 清单不再冒充 SSOT。
func TestOrphanGuardManifestIsAbsent(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "guards", "guard_manifest.json")
	_, err := os.Stat(path)
	if err == nil {
		t.Fatal("orphan internal/guards/guard_manifest.json must remain deleted")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat orphan guard manifest: %v", err)
	}
}

// TestArchtestsDoNotReadHistoricalProjectSkills 锁定架构守卫只验证运行时可达的 canonical skill owner。
func TestArchtestsDoNotReadHistoricalProjectSkills(t *testing.T) {
	root := filepath.Join(repoRoot(t), "internal", "archtest")
	forbidden := strings.Join([]string{".agent", "skills"}, "/")
	violations, err := scanHistoricalSkillPaths(root, forbidden)
	if err != nil {
		t.Fatalf("scan archtest sources: %v", err)
	}
	for _, violation := range violations {
		t.Errorf("%s constructs historical project skill root %q", violation, forbidden)
	}
}

// TestHistoricalSkillPathScannerRejectsNestedHelpers 锁定 helper、子目录和常量拼接不能绕过。
func TestHistoricalSkillPathScannerRejectsNestedHelpers(t *testing.T) {
	root := t.TempDir()
	forbidden := strings.Join([]string{".agent", "skills"}, "/")
	fixtures := map[string]string{
		"direct.go": "package fixture\nconst root = \"" + forbidden + "\"\n",
		"join.go":   "package fixture\nimport \"path/filepath\"\nvar root = filepath.Join(\".agent\", \"skills\")\n",
		"named_const.go": "package fixture\nimport \"path/filepath\"\nconst oldRoot = \".agent\"\n" +
			"const oldLeaf = \"skills\"\nvar root = filepath.Join(oldRoot, oldLeaf)\n",
		"sub/helper.go": "package helper\nvar root = \".agent\" + \"/skills\"\n",
	}
	for relative, body := range fixtures {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir fixture parent: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", relative, err)
		}
	}
	violations, err := scanHistoricalSkillPaths(root, forbidden)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != len(fixtures) {
		t.Fatalf("violations = %v, want all %d fixtures", violations, len(fixtures))
	}
}

// scanHistoricalSkillPaths 递归解析 archtest 的生产与测试 Go 文件。
func scanHistoricalSkillPaths(root, forbidden string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		found, err := goFileConstructsPath(path, forbidden)
		if err != nil {
			return err
		}
		if found {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			violations = append(violations, filepath.ToSlash(relative))
		}
		return nil
	})
	return violations, err
}

// goFileConstructsPath 识别直接字面量、字符串拼接和 path/filepath.Join。
func goFileConstructsPath(path, forbidden string) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	constants := fileConstantExpressions(file)
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		expression, ok := node.(ast.Expr)
		if !ok || found {
			return !found
		}
		value, ok := constantPathExpression(expression, constants, nil)
		if ok && strings.Contains(filepath.ToSlash(value), forbidden) {
			found = true
			return false
		}
		return true
	})
	return found, nil
}

// fileConstantExpressions 收集同文件内可静态折叠的命名常量。
func fileConstantExpressions(file *ast.File) map[string]ast.Expr {
	constants := make(map[string]ast.Expr)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok || len(values.Names) != len(values.Values) {
				continue
			}
			for index, name := range values.Names {
				constants[name.Name] = values.Values[index]
			}
		}
	}
	return constants
}

// constantPathExpression 折叠 guard 中常见的静态路径构造。
func constantPathExpression(expression ast.Expr, constants map[string]ast.Expr, visiting map[string]bool) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		decoded, err := strconv.Unquote(value.Value)
		return decoded, err == nil
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := constantPathExpression(value.X, constants, visiting)
		right, rightOK := constantPathExpression(value.Y, constants, visiting)
		return left + right, leftOK && rightOK
	case *ast.ParenExpr:
		return constantPathExpression(value.X, constants, visiting)
	case *ast.Ident:
		return constantIdentifierExpression(value.Name, constants, visiting)
	case *ast.CallExpr:
		return constantJoinExpression(value, constants, visiting)
	default:
		return "", false
	}
}

// constantIdentifierExpression 递归折叠命名常量并拒绝自引用循环。
func constantIdentifierExpression(name string, constants map[string]ast.Expr, visiting map[string]bool) (string, bool) {
	expression, ok := constants[name]
	if !ok {
		return "", false
	}
	if visiting == nil {
		visiting = make(map[string]bool)
	}
	if visiting[name] {
		return "", false
	}
	visiting[name] = true
	value, ok := constantPathExpression(expression, constants, visiting)
	delete(visiting, name)
	return value, ok
}

// constantJoinExpression 折叠 path.Join/filepath.Join 的纯字符串参数。
func constantJoinExpression(call *ast.CallExpr, constants map[string]ast.Expr, visiting map[string]bool) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Join" {
		return "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "path" && packageName.Name != "filepath" {
		return "", false
	}
	parts := make([]string, 0, len(call.Args))
	for _, argument := range call.Args {
		part, ok := constantPathExpression(argument, constants, visiting)
		if !ok {
			return "", false
		}
		parts = append(parts, part)
	}
	return filepath.Join(parts...), true
}
