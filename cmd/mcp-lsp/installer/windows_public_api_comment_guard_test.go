package installer

// 本静态守卫故意不加 windows build tag：所有 CI 平台都必须检查 Windows 公共
// 文件名、显式平台标签和中文注释，避免只在 Windows 流水线才发现契约漂移。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"
)

// TestWindowsLSPPublicAPIHasPlatformVisibleFilesAndChineseComments 防止 Windows LSP 公共 API 重新出现泛化文件名或无意义注释。
func TestWindowsLSPPublicAPIHasPlatformVisibleFilesAndChineseComments(t *testing.T) {
	t.Parallel()

	files := []string{
		filepath.Join("..", "..", "..", "internal", "platform", "runtimeenv", "windows_lsp_product_runtime_windows.go"),
		"node_runtime_install_windows.go",
		"node_runtime_windows.go",
		"provision_windows.go",
		"windows_asset_archive_shared.go",
		"windows_asset_cache_shared.go",
		"windows_catalog_windows.go",
		"windows_host_platform_shared.go",
		"windows_install_resolver_windows.go",
		"windows_jdtls_args_windows.go",
		"windows_locked_asset_shared.go",
		"windows_runtime_dependency_archive_windows.go",
		"windows_runtime_dependency_catalog.go",
		"windows_runtime_dependency_provision.go",
		"windows_vclibs_desktop_app_local_windows.go",
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Windows public API guard source directory")
	}
	sourceDir := filepath.Dir(currentFile)
	for _, name := range files {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(strings.ToLower(name), "windows") {
				t.Fatalf("Windows 专属实现文件名必须一眼可见平台：%q", name)
			}
			assertWindowsPublicComments(t, filepath.Join(sourceDir, name))
		})
	}
}

// assertWindowsPublicComments 校验一个 Windows 安装桥源码文件中的导出声明和导出结构字段。
func assertWindowsPublicComments(t *testing.T, path string) {
	t.Helper()

	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 Windows 安装桥源码 %s：%v", path, err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 Windows 安装桥源码 %s：%v", path, err)
	}

	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Name.IsExported() && (declaration.Recv == nil || exportedWindowsReceiver(declaration.Recv)) {
				if declaration.Recv == nil {
					assertWindowsPublicName(t, declaration.Name.Name)
				}
				assertMeaningfulWindowsPublicComment(t, fileSet, declaration.Name.Name, declaration.Doc)
			}
		case *ast.GenDecl:
			for _, rawSpec := range declaration.Specs {
				switch spec := rawSpec.(type) {
				case *ast.TypeSpec:
					if spec.Name.IsExported() {
						assertWindowsPublicName(t, spec.Name.Name)
						comment := spec.Doc
						if comment == nil && len(declaration.Specs) == 1 {
							comment = declaration.Doc
						}
						assertMeaningfulWindowsPublicComment(t, fileSet, spec.Name.Name, comment)
					}
					if spec.Name.IsExported() {
						if structure, ok := spec.Type.(*ast.StructType); ok {
							assertWindowsPublicFieldComments(t, fileSet, structure)
						}
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if !name.IsExported() {
							continue
						}
						assertWindowsPublicName(t, name.Name)
						comment := spec.Doc
						if comment == nil && len(declaration.Specs) == 1 {
							comment = declaration.Doc
						}
						assertMeaningfulWindowsPublicComment(t, fileSet, name.Name, comment)
					}
				}
			}
		}
	}
}

// exportedWindowsReceiver 判断方法接收者是否为本包具名公共类型；未导出内部 reader 的 Read/Close 不属于 Windows 公共 API 守卫范围。
func exportedWindowsReceiver(receiver *ast.FieldList) bool {
	if receiver == nil || len(receiver.List) != 1 {
		return false
	}
	expression := receiver.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.IsExported()
}

// assertWindowsPublicName 要求 Windows 安装桥的顶层导出标识符以平台名或“动作+平台名”开头，拒绝 FooWindowsBar 之类后置伪装。
func assertWindowsPublicName(t *testing.T, name string) {
	t.Helper()
	if windowsPublicNameHasVisiblePrefix(name) {
		return
	}
	t.Errorf("Windows 安装桥顶层导出标识符 %s 必须以 Windows 或受支持的动作+Windows 前缀开头", name)
}

func windowsPublicNameHasVisiblePrefix(name string) bool {
	for _, prefix := range []string{
		"Windows",
		"ErrWindows",
		"ErrUnsupportedWindows",
		"ErrUnknownWindows",
		"NewWindows",
		"ProvisionWindows",
		"ResolveWindows",
		"SelectWindows",
		"ValidateWindows",
		"DetectWindows",
		"NormalizeWindows",
		"CheckWindows",
		"PrependWindows",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// TestWindowsPublicNameGuardRejectsBuriedPlatformTokens 证明平台词埋在标识符中部不能逃过公共命名门禁。
func TestWindowsPublicNameGuardRejectsBuriedPlatformTokens(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"WindowsAsset", "NewWindowsAsset", "ErrWindowsAsset", "ErrUnsupportedWindowsHost", "ResolveWindowsAsset"} {
		if !windowsPublicNameHasVisiblePrefix(name) {
			t.Errorf("visible Windows prefix %q was rejected", name)
		}
	}
	for _, name := range []string{"FooWindowsBar", "InstallFooWindowsAsset", "CrossPlatformWindowsMaybe"} {
		if windowsPublicNameHasVisiblePrefix(name) {
			t.Errorf("buried Windows token %q passed the public naming guard", name)
		}
	}
}

// assertWindowsPublicFieldComments 校验导出结构字段具有说明其用途的中文注释。
func assertWindowsPublicFieldComments(t *testing.T, fileSet *token.FileSet, structure *ast.StructType) {
	t.Helper()
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			comment := field.Doc
			if comment == nil {
				comment = field.Comment
			}
			assertMeaningfulWindowsPublicComment(t, fileSet, name.Name, comment)
		}
	}
}

// assertMeaningfulWindowsPublicComment 拒绝缺失、非中文或已知模板化的公共注释。
func assertMeaningfulWindowsPublicComment(t *testing.T, fileSet *token.FileSet, name string, comment *ast.CommentGroup) {
	t.Helper()
	position := fileSet.Position(token.NoPos)
	if comment != nil {
		position = fileSet.Position(comment.Pos())
	}
	if comment == nil {
		t.Errorf("%s：导出标识符 %s 缺少中文注释", position, name)
		return
	}
	text := strings.TrimSpace(comment.Text())
	if !strings.HasPrefix(text, name+" ") {
		t.Errorf("%s：导出标识符 %s 的注释必须以“%s ”开头", position, name, name)
	}
	if !strings.ContainsFunc(text, func(character rune) bool {
		return unicode.Is(unicode.Han, character)
	}) {
		t.Errorf("%s：导出标识符 %s 的注释必须包含中文用途说明", position, name)
	}
	for _, forbidden := range []string{
		"Windows 生产桥实现说明；不改变运行逻辑",
		"常量或变量；失败必须",
		"类型；失败必须",
		"函数；失败必须",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("%s：导出标识符 %s 仍使用无意义模板注释 %q", position, name, forbidden)
		}
	}
}
