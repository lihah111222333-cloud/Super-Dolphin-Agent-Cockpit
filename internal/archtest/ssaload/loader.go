// Package ssaload 提供 archtest 共享、确定性的 Go package 与 SSA 装载原语。
package ssaload

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// Options 描述共享 loader 的仓库根、模式、测试元数据、overlay 与候选过滤器。
type Options struct {
	RepoRoot string
	Patterns []string
	Tests    bool
	Overlay  map[string][]byte
	Include  func(*packages.Package) bool
}

// Load 按固定语法模式加载并筛选确定性 package 集合。
func Load(opts Options) ([]*packages.Package, error) {
	mode := packages.LoadSyntax
	if opts.Tests {
		mode |= packages.NeedForTest
	}
	loaded, err := packages.Load(&packages.Config{Mode: mode, Dir: opts.RepoRoot, Tests: opts.Tests, Overlay: opts.Overlay}, opts.Patterns...)
	if err != nil {
		return nil, err
	}
	byID, messages := collectLoadCandidates(loaded, opts.Include)
	if len(messages) > 0 {
		sort.Strings(messages)
		return nil, fmt.Errorf("package load failed: %s", strings.Join(messages, "; "))
	}
	result := make([]*packages.Package, 0, len(byID))
	for _, pkg := range byID {
		result = append(result, pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].PkgPath < result[j].PkgPath || result[i].PkgPath == result[j].PkgPath && result[i].ID < result[j].ID
	})
	if len(result) == 0 {
		return nil, fmt.Errorf("package load failed: no candidates")
	}
	return result, nil
}

// collectLoadCandidates 分离顶层候选选择与依赖错误聚合，保持旧扫描语义。
func collectLoadCandidates(loaded []*packages.Package, include func(*packages.Package) bool) (map[string]*packages.Package, []string) {
	byID := map[string]*packages.Package{}
	var messages []string
	for _, pkg := range loaded {
		if pkg == nil {
			messages = append(messages, "nil top-level package")
			continue
		}
		if include != nil && !include(pkg) {
			continue
		}
		if len(pkg.Syntax) == 0 {
			messages = append(messages, fmt.Sprintf("package %s has empty syntax", pkg.ID))
		}
		byID[pkg.ID] = pkg
	}
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		if pkg == nil || include != nil && !include(pkg) {
			return
		}
		for _, pkgErr := range pkg.Errors {
			messages = append(messages, fmt.Sprintf("%s: %s", pkg.ID, pkgErr.Error()))
		}
	})
	return byID, messages
}

// Build 校验精确 package 后构建 SSA，并把 builder panic 转为错误。
func Build(pkgs []*packages.Package) (program *ssa.Program, built []*ssa.Package, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			program = nil
			built = nil
			err = fmt.Errorf("SSA build failed: %v", recovered)
		}
	}()
	if len(pkgs) == 0 {
		return nil, nil, fmt.Errorf("SSA build failed: no packages")
	}
	fset, err := validateBuildPackages(pkgs)
	if err != nil {
		return nil, nil, err
	}
	program = ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	createBuildImportStubs(program, pkgs)
	for _, pkg := range pkgs {
		built = append(built, program.CreatePackage(pkg.Types, pkg.Syntax, pkg.TypesInfo, true))
	}
	program.Build()
	for _, pkg := range built {
		if pkg == nil {
			return nil, nil, fmt.Errorf("SSA build failed: nil package")
		}
	}
	return program, built, nil
}

// validateBuildPackages 校验精确目标的语法、类型信息和共享文件集。
func validateBuildPackages(pkgs []*packages.Package) (*token.FileSet, error) {
	var fset *token.FileSet
	for _, pkg := range pkgs {
		if pkg == nil || pkg.Fset == nil || len(pkg.Syntax) == 0 || pkg.Types == nil || pkg.TypesInfo == nil {
			return nil, fmt.Errorf("SSA build failed: incomplete package")
		}
		if fset == nil {
			fset = pkg.Fset
		} else if fset != pkg.Fset {
			return nil, fmt.Errorf("SSA build failed: mismatched file sets")
		}
	}
	return fset, nil
}

// createBuildImportStubs 为目标依赖递归创建无语法 SSA stub。
func createBuildImportStubs(program *ssa.Program, pkgs []*packages.Package) {
	targets := map[*types.Package]bool{}
	for _, pkg := range pkgs {
		targets[pkg.Types] = true
	}
	created := map[*types.Package]bool{}
	var visit func(*types.Package)
	visit = func(pkg *types.Package) {
		for _, imp := range pkg.Imports() {
			if created[imp] || targets[imp] {
				continue
			}
			visit(imp)
			program.CreatePackage(imp, nil, nil, true)
			created[imp] = true
		}
	}
	for _, pkg := range pkgs {
		visit(pkg.Types)
	}
}
