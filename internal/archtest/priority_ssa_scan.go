package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

const prioritySSAModulePath = "github.com/anthropic-ai/super-agent-v3"

type prioritySSAPackage struct {
	pkgPath   string
	repoRoot  string
	fset      *token.FileSet
	syntax    []*ast.File
	types     *types.Package
	typesInfo *types.Info
}

type prioritySSATargetSpec struct {
	importPath string
	name       string
}

// CollectPrioritySSAViolations 扫描生产包中的 priority SSA 规则违规。
func CollectPrioritySSAViolations(opts CheckOptions) ([]PrioritySSAViolation, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	pkgs, err := loadPrioritySSAPackages(repoRoot)
	if err != nil {
		return nil, err
	}
	targets, err := prioritySSAWidePortTargets(pkgs)
	if err != nil {
		return nil, err
	}
	var violations []PrioritySSAViolation
	for _, pkg := range pkgs {
		ssaPkg, err := buildPrioritySSAPackage(pkg)
		if err != nil {
			return nil, err
		}
		violations = append(violations, collectPrioritySSAPackageViolations(pkg, ssaPkg, targets)...)
	}
	sortPrioritySSAViolations(violations)
	return dedupePrioritySSAViolations(violations), nil
}

func loadPrioritySSAPackages(repoRoot string) ([]*prioritySSAPackage, error) {
	loaded, err := ssaload.Load(ssaload.Options{RepoRoot: repoRoot, Patterns: []string{"./cmd/...", "./internal/..."}, Tests: false, Overlay: prioritySSAOverlay(repoRoot), Include: func(pkg *packages.Package) bool {
		return pkg != nil && prioritySSAProductionPackagePath(pkg.PkgPath) && len(pkg.GoFiles) > 0
	}})
	if err != nil {
		return nil, fmt.Errorf("load priority SSA packages: %w", err)
	}
	return prioritySSACheckedPackages(repoRoot, loaded), nil
}

func prioritySSAOverlay(repoRoot string) map[string][]byte {
	return map[string][]byte{
		filepath.Join(repoRoot, "cmd", "agent-terminal", "web-dist", "index.html"): []byte("<!doctype html><title>archtest</title>\n"),
	}
}

func prioritySSACheckedPackages(repoRoot string, loaded []*packages.Package) []*prioritySSAPackage {
	pkgs := make([]*prioritySSAPackage, 0, len(loaded))
	for _, pkg := range loaded {
		if !prioritySSAProductionPackagePath(pkg.PkgPath) || len(pkg.Syntax) == 0 {
			continue
		}
		pkgs = append(pkgs, &prioritySSAPackage{
			pkgPath:   pkg.PkgPath,
			repoRoot:  repoRoot,
			fset:      pkg.Fset,
			syntax:    pkg.Syntax,
			types:     pkg.Types,
			typesInfo: pkg.TypesInfo,
		})
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i].pkgPath < pkgs[j].pkgPath
	})
	return pkgs
}

func prioritySSAProductionPackagePath(pkgPath string) bool {
	if pkgPath == "" || strings.HasPrefix(pkgPath, prioritySSAModulePath+"/internal/archtest") {
		return false
	}
	return strings.HasPrefix(pkgPath, prioritySSAModulePath+"/cmd/") ||
		strings.HasPrefix(pkgPath, prioritySSAModulePath+"/internal/")
}

func prioritySSAWidePortTargets(pkgs []*prioritySSAPackage) ([]*types.TypeName, error) {
	byPath := map[string]*prioritySSAPackage{}
	for _, pkg := range pkgs {
		byPath[pkg.pkgPath] = pkg
	}
	specs := prioritySSANamedWidePortTargetSpecs()
	targets := make([]*types.TypeName, 0, len(specs))
	for _, spec := range specs {
		target, err := prioritySSAWidePortTarget(byPath, spec)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	targets = append(targets, prioritySSAWideOrchestrationTargets(pkgs)...)
	sort.Slice(targets, func(i, j int) bool {
		return prioritySSATargetSortKey(targets[i]) < prioritySSATargetSortKey(targets[j])
	})
	return targets, nil
}

func prioritySSANamedWidePortTargetSpecs() []prioritySSATargetSpec {
	return []prioritySSATargetSpec{
		{importPath: prioritySSAModulePath + "/cmd/mcp-orch/store/taskdag", name: "Store"},
		{importPath: prioritySSAModulePath + "/internal/module/skill", name: "Service"},
	}
}

func prioritySSAWidePortTarget(byPath map[string]*prioritySSAPackage, spec prioritySSATargetSpec) (*types.TypeName, error) {
	pkg := byPath[spec.importPath]
	if pkg == nil || pkg.types == nil {
		return nil, fmt.Errorf("wide-port target package %s was not loaded", spec.importPath)
	}
	obj, ok := pkg.types.Scope().Lookup(spec.name).(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("wide-port target %s.%s not found", spec.importPath, spec.name)
	}
	return obj, nil
}

func buildPrioritySSAPackage(pkg *prioritySSAPackage) (*ssa.Package, error) {
	prog := ssa.NewProgram(pkg.fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	for _, imported := range pkg.types.Imports() {
		prog.CreatePackage(imported, nil, nil, true)
	}
	ssaPkg := prog.CreatePackage(pkg.types, pkg.syntax, pkg.typesInfo, true)
	var buildErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				buildErr = fmt.Errorf("build SSA for %s: %v", pkg.pkgPath, recovered)
			}
		}()
		ssaPkg.Build()
	}()
	return ssaPkg, buildErr
}

func collectPrioritySSAPackageViolations(
	pkg *prioritySSAPackage,
	ssaPkg *ssa.Package,
	targets []*types.TypeName,
) []PrioritySSAViolation {
	var violations []PrioritySSAViolation
	for _, target := range targets {
		if target.Pkg().Path() == pkg.pkgPath || !prioritySSAPackageMayCarryTarget(pkg, target) {
			continue
		}
		violations = append(violations, collectPrioritySSAWidePortViolations(pkg, ssaPkg, target)...)
	}
	for _, fn := range prioritySSAFunctions(ssaPkg) {
		violations = append(violations, collectPrioritySSAIgnoredReturnViolations(pkg, fn)...)
		violations = append(violations, collectPrioritySSAContextCancelViolations(pkg, fn)...)
		violations = append(violations, collectPrioritySSARawSQLViolations(pkg, fn)...)
		violations = append(violations, collectPrioritySSAErrorStringViolations(pkg, fn)...)
		violations = append(violations, collectPrioritySSAFXInvokeViolations(pkg, fn)...)
	}
	return append(violations, collectPrioritySSAOnStartViolations(pkg, ssaPkg)...)
}

func prioritySSAViolation(pkg *prioritySSAPackage, pos token.Pos, rule PrioritySSARule, detail string) PrioritySSAViolation {
	file, line := prioritySSAPosition(pkg, pos)
	return PrioritySSAViolation{Rule: rule, File: file, Line: line, Detail: detail}
}

// prioritySSAPosition 将 SSA token 位置转为稳定的仓库相对路径和行号。
func prioritySSAPosition(pkg *prioritySSAPackage, pos token.Pos) (string, int) {
	if pkg != nil && pos.IsValid() {
		position := pkg.fset.Position(pos)
		if position.Filename != "" {
			return displayGuardPath(pkg.repoRoot, position.Filename), position.Line
		}
	}
	if pkg == nil || pkg.pkgPath == "" {
		return "(unknown)", 0
	}
	return strings.TrimPrefix(pkg.pkgPath, prioritySSAModulePath+"/"), 0
}

func dedupePrioritySSAViolations(violations []PrioritySSAViolation) []PrioritySSAViolation {
	out := violations[:0]
	var last string
	for _, violation := range violations {
		key := violation.Key()
		if key == last {
			continue
		}
		out = append(out, violation)
		last = key
	}
	return out
}
