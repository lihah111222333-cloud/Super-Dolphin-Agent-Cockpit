package capcontract

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// discoverPackageDirs 只遍历一次扫描根，产出可能包含非测试 Go 文件的稳定目录索引。
func discoverPackageDirs(repoRoot string, roots []string) ([]string, error) {
	dirs := make(map[string]struct{})
	for _, root := range roots {
		absRoot := filepath.Join(repoRoot, filepath.FromSlash(root))
		if err := indexPackageRoot(absRoot, dirs); err != nil {
			return nil, fmt.Errorf("index capability root %s: %w", root, err)
		}
	}
	result := make([]string, 0, len(dirs))
	for dir := range dirs {
		result = append(result, dir)
	}
	sort.Strings(result)
	return result, nil
}

// indexPackageRoot 复用 filepath.WalkDir 的目录项，跳过 Go ./... 明确排除的目录。
func indexPackageRoot(root string, dirs map[string]struct{}) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredPackageDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			dirs[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
}

// ignoredPackageDir 对齐 go list ./... 对隐藏目录、下划线目录、testdata 与 vendor 的过滤。
func ignoredPackageDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// scanPackageDir 使用 go/build 做平台文件筛选，再只解析当前包自身的 AST。
func scanPackageDir(repoRoot, dir, goos, goarch string) (*PackageManifest, error) {
	buildContext := build.Default
	buildContext.GOOS = goos
	buildContext.GOARCH = goarch
	buildContext.CgoEnabled = false
	buildContext.BuildTags = nil
	pkg, err := buildContext.ImportDir(dir, build.ImportComment)
	if err != nil {
		var noGo *build.NoGoError
		if errors.As(err, &noGo) {
			return nil, nil
		}
		return nil, fmt.Errorf("load package directory %s: %w", dir, err)
	}
	return parsePackageManifest(repoRoot, dir, pkg.Name, pkg.GoFiles)
}

// parsePackageManifest 解析已筛选文件并构造单个平台的包能力面。
func parsePackageManifest(repoRoot, dir, packageName string, fileNames []string) (*PackageManifest, error) {
	relPath, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		return nil, err
	}
	manifest := &PackageManifest{Path: filepath.ToSlash(relPath), Name: packageName}
	fileSet := token.NewFileSet()
	files := make([]*ast.File, 0, len(fileNames))
	for _, name := range fileNames {
		path := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ParseComments|parser.AllErrors)
		if parseErr != nil {
			return nil, fmt.Errorf("parse package file %s: %w", path, parseErr)
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		return fileSet.Position(files[i].Package).Filename < fileSet.Position(files[j].Package).Filename
	})
	for _, file := range files {
		extractPackageFile(file, manifest)
	}
	sortFunctions(manifest.Functions)
	sortMethods(manifest.Methods)
	sortInterfaces(manifest.Interfaces)
	sortStructs(manifest.Structs)
	return manifest, nil
}

// extractPackageFile 读取包注释并把当前文件的声明追加到清单。
func extractPackageFile(file *ast.File, manifest *PackageManifest) {
	if manifest.Description == "" && file.Doc != nil {
		line, _, _ := strings.Cut(file.Doc.Text(), "\n")
		line = strings.TrimPrefix(line, "Package "+manifest.Name+" ")
		manifest.Description = strings.TrimSpace(strings.TrimPrefix(line, "Package "+manifest.Name))
	}
	extractFile(file, manifest)
}
