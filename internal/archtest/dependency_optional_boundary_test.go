package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type optionalDependencyCategory string

const (
	optionalDependencyAbsence optionalDependencyCategory = "dependency_absence"
	optionalAdjunct           optionalDependencyCategory = "adjunct_optional"
	optionalTestOrTemplate    optionalDependencyCategory = "test_or_template"
)

func TestOptionalDependencyBoundary(t *testing.T) {
	t.Parallel()

	occurrences := scanOptionalDependencyOccurrences(t, optionalDependencyRepoRoot(t))
	classifications := registeredOptionalDependencyClassifications()
	var unclassified []string
	for _, occurrence := range occurrences {
		classification, ok := classifications[occurrence.key()]
		if !ok {
			unclassified = append(unclassified, occurrence.String())
			continue
		}
		if classification.category != optionalDependencyAbsence {
			continue
		}
		if !contract.AllowsMissingDependency(classification.dependency, classification.profile) {
			t.Fatalf("%s is dependency_absence but policy denies %s in %s", occurrence, classification.dependency, classification.profile)
		}
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("unclassified optional dependency boundaries:\n%s", strings.Join(unclassified, "\n"))
	}
}

type optionalDependencyOccurrence struct {
	RelPath string
	Line    int
	Kind    string
	Value   string
}

func (o optionalDependencyOccurrence) key() string {
	return o.RelPath + ":" + o.Kind + ":" + o.Value
}

func (o optionalDependencyOccurrence) String() string {
	return o.RelPath + ":" + strconv.Itoa(o.Line) + " " + o.Kind + " " + o.Value
}

type optionalDependencyClassification struct {
	category   optionalDependencyCategory
	dependency string
	profile    contract.DependencyProfile
}

func registeredOptionalDependencyClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory) optionalDependencyClassification {
		return optionalDependencyClassification{category: category}
	}
	dependency := func(name string, profile contract.DependencyProfile) optionalDependencyClassification {
		return optionalDependencyClassification{category: optionalDependencyAbsence, dependency: name, profile: profile}
	}
	return map[string]optionalDependencyClassification{
		"internal/app/runtime_reporter_adapter.go:optional_tag:Service":    dependency("runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Logger":     classify(optionalAdjunct),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Dependency": classify(optionalAdjunct),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Config":     classify(optionalAdjunct),
		"internal/app/runner.go:optional_tag:RootCtx":                      classify(optionalAdjunct),
		"internal/app/runner.go:optional_tag:Lifecycle":                    classify(optionalAdjunct),
		"internal/app/runner.go:optional_tag:ExtractionDrainer":            classify(optionalAdjunct),

		"internal/app/thread_orchestration_adapter.go:typed_unsupported:thread.bind_session_generation": dependency("thread.bind_session_generation", contract.DependencyProfileDesktopHost),
		"internal/app/thread_orchestration_adapter.go:noop_success:LaunchAgent":                         classify(optionalAdjunct),

		"internal/module/thread/lifecycle.go:typed_unsupported:thread.bind_session_generation": dependency("thread.bind_session_generation", contract.DependencyProfileDesktopHost),
		"internal/module/thread/module.go:optional_tag:Store":                                  classify(optionalAdjunct),
		"internal/module/thread/module.go:optional_tag:Catalog":                                classify(optionalAdjunct),
		"internal/module/thread/module.go:optional_tag:Registrar":                              classify(optionalAdjunct),
		"internal/module/thread/module.go:optional_tag:PromptStore":                            classify(optionalAdjunct),
		"internal/module/thread/module.go:optional_tag:Builtin":                                classify(optionalAdjunct),
		"internal/module/thread/module.go:optional_tag:PromptCatalog":                          classify(optionalAdjunct),

		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.agent_thread_lookup":          dependency("toolbridge.agent_thread_lookup", contract.DependencyProfileDesktopHost),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.thread_config_override_store": dependency("toolbridge.thread_config_override_store", contract.DependencyProfileDesktopHost),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.lifecycle_backfiller":         dependency("toolbridge.lifecycle_backfiller", contract.DependencyProfileTest),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.skill_tools":                  dependency("toolbridge.skill_tools", contract.DependencyProfileTest),
		"internal/platform/toolbridge/module.go:optional_tag:Resolver":                                      classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:BindingStore":                                  dependency("toolbridge.agent_thread_lookup", contract.DependencyProfileDesktopHost),
		"internal/platform/toolbridge/module.go:optional_tag:ThreadStore":                                   dependency("toolbridge.thread_config_override_store", contract.DependencyProfileDesktopHost),
		"internal/platform/toolbridge/module.go:optional_tag:Preferences":                                   classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Config":                                        classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Logger":                                        classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Tracer":                                        classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Dispatcher":                                    classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Lifecycle":                                     dependency("toolbridge.lifecycle_backfiller", contract.DependencyProfileTest),
		"internal/platform/toolbridge/module.go:optional_tag:HostTools":                                     classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:SkillTools":                                    dependency("toolbridge.skill_tools", contract.DependencyProfileTest),
		"internal/platform/toolbridge/module.go:optional_tag:Reader":                                        classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Writer":                                        classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:History":                                       classify(optionalAdjunct),
		"internal/platform/toolbridge/module.go:optional_tag:Templates":                                     classify(optionalAdjunct),

		"internal/provider/claudecli/module.go:optional_tag:Dependency": classify(optionalAdjunct),
		"internal/provider/claudecli/module.go:optional_tag:Config":     classify(optionalAdjunct),
		"internal/provider/claudecli/module.go:optional_tag:Recovery":   classify(optionalAdjunct),
		"internal/provider/claudecli/module.go:optional_tag:Tracer":     classify(optionalAdjunct),

		"internal/provider/codexapp/module.go:optional_tag:Dependency":  classify(optionalAdjunct),
		"internal/provider/codexapp/module.go:optional_tag:Config":      classify(optionalAdjunct),
		"internal/provider/codexapp/module.go:optional_tag:Recovery":    classify(optionalAdjunct),
		"internal/provider/codexapp/module.go:optional_tag:Logger":      classify(optionalAdjunct),
		"internal/provider/codexapp/module.go:optional_tag:PIDRegistry": classify(optionalAdjunct),

		"internal/provider/unified/module.go:optional_tag:Logger":        classify(optionalAdjunct),
		"internal/provider/unified/module.go:optional_tag:Tracer":        classify(optionalAdjunct),
		"internal/provider/unified/module.go:optional_tag:ThreadStore":   classify(optionalAdjunct),
		"internal/provider/unified/module.go:optional_tag:BindingStore":  classify(optionalAdjunct),
		"internal/provider/unified/module.go:optional_tag:BindingWriter": classify(optionalAdjunct),
	}
}

func scanOptionalDependencyOccurrences(t *testing.T, root string) []optionalDependencyOccurrence {
	t.Helper()
	var out []optionalDependencyOccurrence
	for _, dir := range []string{"internal/app", "internal/module/thread", "internal/platform/toolbridge", "internal/provider"} {
		base := filepath.Join(root, filepath.FromSlash(dir))
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			fileOccurrences, err := scanOptionalDependencyFile(path, rel)
			if err != nil {
				return err
			}
			out = append(out, fileOccurrences...)
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
	return out
}

func scanOptionalDependencyFile(path, rel string) ([]optionalDependencyOccurrence, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var out []optionalDependencyOccurrence
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Field:
			if hasOptionalTag(n.Tag) {
				out = append(out, optionalDependencyOccurrence{
					RelPath: rel,
					Line:    fset.Position(n.Pos()).Line,
					Kind:    "optional_tag",
					Value:   fieldName(n),
				})
			}
		case *ast.CallExpr:
			if isNewDependencyModeErrorCall(n) {
				if dependencyName, ok := stringLiteralArg(n, 1); ok {
					out = append(out, optionalDependencyOccurrence{
						RelPath: rel,
						Line:    fset.Position(n.Pos()).Line,
						Kind:    "typed_unsupported",
						Value:   dependencyName,
					})
				}
			}
		case *ast.FuncDecl:
			if isNoopSuccessFunction(n) {
				out = append(out, optionalDependencyOccurrence{
					RelPath: rel,
					Line:    fset.Position(n.Pos()).Line,
					Kind:    "noop_success",
					Value:   n.Name.Name,
				})
			}
		}
		return true
	})
	return out, nil
}

func hasOptionalTag(tag *ast.BasicLit) bool {
	return tag != nil && strings.Contains(tag.Value, `optional:"true"`)
}

func fieldName(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "<embedded>"
	}
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ",")
}

func isNewDependencyModeErrorCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewDependencyModeError" || len(call.Args) < 3 {
		return false
	}
	arg, ok := call.Args[0].(*ast.SelectorExpr)
	return ok && arg.Sel.Name == "ErrUnsupportedDependencyMode"
}

func stringLiteralArg(call *ast.CallExpr, index int) (string, bool) {
	if len(call.Args) <= index {
		return "", false
	}
	lit, ok := call.Args[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value := strings.Trim(lit.Value, "\"`")
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func isNoopSuccessFunction(fn *ast.FuncDecl) bool {
	if fn.Name.Name != "LaunchAgent" || fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && isOptionalNilIdent(ret.Results[0])
}

func isOptionalNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

func optionalDependencyRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}
