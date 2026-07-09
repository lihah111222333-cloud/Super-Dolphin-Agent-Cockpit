package archtest_test

import (
	"go/importer"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const wideOrchestrationFamilyThreshold = 2

func loadWideOrchestrationTypeGuardPackages(t *testing.T, root string) []*orchestrationServiceCheckedPackage {
	t.Helper()

	cfg := &packages.Config{
		Dir:     root,
		Mode:    packages.LoadSyntax,
		Tests:   false,
		Overlay: wideOrchestrationGuardOverlay(root),
	}
	loaded, err := packages.Load(cfg, "./cmd/...", "./internal/...")
	if err != nil {
		t.Fatalf("load production packages: %v", err)
	}
	if errors := wideOrchestrationPackageLoadErrors(loaded); len(errors) > 0 {
		t.Fatalf("load production packages returned errors:\n%s", strings.Join(errors, "\n"))
	}

	checked := make([]*orchestrationServiceCheckedPackage, 0, len(loaded))
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		if pkg == nil || !isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) || len(pkg.Syntax) == 0 {
			return
		}
		checked = append(checked, &orchestrationServiceCheckedPackage{
			pkgPath:   pkg.PkgPath,
			fset:      pkg.Fset,
			syntax:    pkg.Syntax,
			types:     pkg.Types,
			typesInfo: pkg.TypesInfo,
		})
	})
	sort.Slice(checked, func(i, j int) bool {
		return checked[i].pkgPath < checked[j].pkgPath
	})
	return checked
}

func wideOrchestrationGuardOverlay(root string) map[string][]byte {
	return map[string][]byte{
		filepath.Join(root, "cmd", "agent-terminal", "web-dist", "index.html"): []byte("<!doctype html><title>archtest</title>\n"),
	}
}

func wideOrchestrationPackageLoadErrors(pkgs []*packages.Package) []string {
	seen := map[string]bool{}
	var out []string
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg == nil || !isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) {
			return
		}
		for _, err := range pkg.Errors {
			line := pkg.PkgPath + ": " + err.Error()
			if seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
		}
	})
	sort.Strings(out)
	return out
}

func typeCheckWideOrchestrationFixturePackage(t *testing.T, importPath string, files map[string]string) *orchestrationServiceCheckedPackage {
	t.Helper()

	fset, syntax := parseOrchestrationServiceTypeGuardFixture(t, files)
	info := newOrchestrationServiceTypesInfo()
	conf := types.Config{Importer: wideOrchestrationFixtureImporter{fallback: importer.Default()}}
	checked, err := conf.Check(importPath, fset, syntax, info)
	if err != nil {
		t.Fatalf("type check wide orchestration fixture: %v", err)
	}
	return &orchestrationServiceCheckedPackage{
		pkgPath:   importPath,
		fset:      fset,
		syntax:    syntax,
		types:     checked,
		typesInfo: info,
	}
}

type wideOrchestrationFixtureImporter struct {
	fallback types.Importer
}

func (i wideOrchestrationFixtureImporter) Import(path string) (*types.Package, error) {
	if path == "go.uber.org/fx" {
		return newWideOrchestrationFxPackage(), nil
	}
	return i.fallback.Import(path)
}

func newWideOrchestrationFxPackage() *types.Package {
	pkg := types.NewPackage("go.uber.org/fx", "fx")
	inType := types.NewTypeName(token.NoPos, pkg, "In", types.NewStruct(nil, nil))
	pkg.Scope().Insert(inType)
	pkg.MarkComplete()
	return pkg
}

func wideOrchestrationType(typ types.Type) bool {
	return len(wideOrchestrationTypeFamilies(typ, map[types.Type]bool{})) >= wideOrchestrationFamilyThreshold
}

func wideOrchestrationTypeFamilies(typ types.Type, seen map[types.Type]bool) map[string]bool {
	families := map[string]bool{}
	if typ == nil {
		return families
	}
	if seen[typ] {
		return families
	}
	seen[typ] = true

	switch typed := types.Unalias(typ).(type) {
	case *types.Named:
		return wideOrchestrationTypeFamilies(typed.Underlying(), seen)
	case *types.TypeParam:
		return wideOrchestrationTypeFamilies(typed.Constraint(), seen)
	case *types.Interface:
		return wideOrchestrationInterfaceFamilies(typed, seen)
	case *types.Signature:
		return wideOrchestrationSignatureFamilies(typed, seen)
	case *types.Tuple:
		return wideOrchestrationTupleFamilies(typed, seen)
	default:
		return wideOrchestrationContainerFamilies(typed, seen)
	}
}

func wideOrchestrationInterfaceFamilies(iface *types.Interface, seen map[types.Type]bool) map[string]bool {
	families := map[string]bool{}
	iface.Complete()
	for method := range iface.Methods() {
		if family := wideOrchestrationMethodFamily(method.Name()); family != "" {
			families[family] = true
		}
	}
	for embedded := range iface.EmbeddedTypes() {
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(embedded, seen))
	}
	return families
}

func wideOrchestrationSignatureFamilies(sig *types.Signature, seen map[types.Type]bool) map[string]bool {
	families := wideOrchestrationTupleFamilies(sig.Params(), seen)
	mergeWideOrchestrationFamilies(families, wideOrchestrationTupleFamilies(sig.Results(), seen))
	if recv := sig.Recv(); recv != nil {
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(recv.Type(), seen))
	}
	return families
}

func wideOrchestrationContainerFamilies(typ types.Type, seen map[types.Type]bool) map[string]bool {
	families := map[string]bool{}
	switch typed := typ.(type) {
	case *types.Pointer:
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(typed.Elem(), seen))
	case *types.Slice:
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(typed.Elem(), seen))
	case *types.Array:
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(typed.Elem(), seen))
	case *types.Map:
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(typed.Key(), seen))
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(typed.Elem(), seen))
	case *types.Chan:
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(typed.Elem(), seen))
	}
	return families
}

func wideOrchestrationTupleFamilies(tuple *types.Tuple, seen map[types.Type]bool) map[string]bool {
	families := map[string]bool{}
	if tuple == nil {
		return families
	}
	for variable := range tuple.Variables() {
		mergeWideOrchestrationFamilies(families, wideOrchestrationTypeFamilies(variable.Type(), seen))
	}
	return families
}

func mergeWideOrchestrationFamilies(dst, src map[string]bool) {
	for family := range src {
		dst[family] = true
	}
}

func wideOrchestrationMethodFamily(method string) string {
	switch method {
	case "LaunchAgent", "ListAgents", "StopAgent", "InterruptAgent", "Recover", "Snapshot", "GetState":
		return "agent_lifecycle"
	case "SubmitTurn", "CompleteTurn":
		return "turn_submission"
	case "UpdateRuntime", "BindSessionGeneration":
		return "agent_runtime"
	case "GetReport", "RememberReportRequest", "HandleReportEvent":
		return "agent_report"
	case "CreateDAG", "GetDAG", "ListDAGs", "StartDAG", "TerminateDAG", "ListRuns", "GetRun", "ApplyOps", "DeleteDAG", "UpdateNodeStatus", "DispatchNode":
		return "dag_runtime"
	default:
		return ""
	}
}

func collectWideOrchestrationProductionViolationMessages(t *testing.T, root string) []string {
	t.Helper()

	var violations []string
	for _, pkg := range loadWideOrchestrationTypeGuardPackages(t, root) {
		for _, use := range collectOrchestrationServiceTypeUses(pkg, nil) {
			if isAllowedOrchestrationServiceTypeUse(use) {
				continue
			}
			violations = append(violations, use.violationMessage())
		}
	}
	sort.Strings(violations)
	return violations
}
