package codemapindex

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// repositorySymbolExists 校验 package.Symbol、file.go.Symbol 及 Type.Method。
func repositorySymbolExists(path string, info os.FileInfo, symbols []string) bool {
	if len(symbols) == 0 {
		return false
	}
	files, err := symbolSourceFiles(path, info)
	if err != nil {
		return false
	}
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			return false
		}
		if parsedFileDeclaresSymbols(parsed, symbols) {
			return true
		}
	}
	return false
}

// symbolSourceFiles 返回一个 Go 文件或包目录的生产 Go 源文件。
func symbolSourceFiles(path string, info os.FileInfo) ([]string, error) {
	if !info.IsDir() {
		if strings.HasSuffix(path, ".go") {
			return []string{path}, nil
		}
		return nil, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			files = append(files, filepath.Join(path, entry.Name()))
		}
	}
	return files, nil
}

// parsedFileDeclaresSymbols 查找顶层声明或指定 receiver 的方法。
func parsedFileDeclaresSymbols(file *ast.File, symbols []string) bool {
	if len(symbols) == 1 {
		return fileDeclaresTopLevel(file, symbols[0])
	}
	if len(symbols) == 2 {
		return fileDeclaresMethod(file, symbols[0], symbols[1])
	}
	return false
}

// fileDeclaresTopLevel 校验函数、类型、变量或常量名。
func fileDeclaresTopLevel(file *ast.File, name string) bool {
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if value.Name.Name == name {
				return true
			}
		case *ast.GenDecl:
			if generalDeclaresName(value, name) {
				return true
			}
		}
	}
	return false
}

// generalDeclaresName 校验 type/value spec 声明名。
func generalDeclaresName(declaration *ast.GenDecl, name string) bool {
	for _, spec := range declaration.Specs {
		switch value := spec.(type) {
		case *ast.TypeSpec:
			if value.Name.Name == name {
				return true
			}
		case *ast.ValueSpec:
			for _, identifier := range value.Names {
				if identifier.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// fileDeclaresMethod 校验 Type.Method 形式的 receiver 方法。
func fileDeclaresMethod(file *ast.File, receiver, method string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != method {
			continue
		}
		if len(function.Recv.List) > 0 && receiverTypeName(function.Recv.List[0].Type) == receiver {
			return true
		}
	}
	return false
}

// receiverTypeName 解包指针和泛型 receiver，返回底层类型名。
func receiverTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverTypeName(value.X)
	case *ast.IndexExpr:
		return receiverTypeName(value.X)
	case *ast.IndexListExpr:
		return receiverTypeName(value.X)
	default:
		return ""
	}
}
