package archtest_test

import (
	"go/importer"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
)

const wideOrchestrationFamilyThreshold = 2

func loadWideOrchestrationTypeGuardPackages(t *testing.T, root string) []*orchestrationServiceCheckedPackage {
	t.Helper()

	loaded, err := ssaload.Load(ssaload.Options{RepoRoot: root, Patterns: []string{"./cmd/...", "./internal/..."}, Tests: false, Overlay: wideOrchestrationGuardOverlay(root), Include: func(pkg *packages.Package) bool {
		return pkg != nil && isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) && len(pkg.GoFiles) > 0
	}})
	if err != nil {
		t.Fatalf("load production packages: %v", err)
	}
	checked := make([]*orchestrationServiceCheckedPackage, 0, len(loaded))
	for _, pkg := range loaded {
		if pkg == nil || !isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) || len(pkg.Syntax) == 0 {
			continue
		}
		checked = append(checked, &orchestrationServiceCheckedPackage{
			pkgPath:   pkg.PkgPath,
			fset:      pkg.Fset,
			syntax:    pkg.Syntax,
			types:     pkg.Types,
			typesInfo: pkg.TypesInfo,
		})
	}
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
		if sig, ok := method.Type().(*types.Signature); ok {
			mergeWideOrchestrationFamilies(families, wideOrchestrationSignatureFamilies(sig, seen))
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

	return collectWideOrchestrationProductionViolationMessagesFromPackages(loadWideOrchestrationTypeGuardPackages(t, root))
}

func collectWideOrchestrationProductionViolationMessagesFromPackages(pkgs []*orchestrationServiceCheckedPackage) []string {
	var violations []string
	for _, pkg := range pkgs {
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
