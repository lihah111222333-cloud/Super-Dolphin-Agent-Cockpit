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
	var missingAudit []string
	for _, occurrence := range occurrences {
		classification, ok := classifications[occurrence.key()]
		if !ok {
			unclassified = append(unclassified, occurrence.String())
			continue
		}
		if violation := classification.auditViolation(occurrence); violation != "" {
			missingAudit = append(missingAudit, violation)
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
	if len(missingAudit) > 0 {
		sort.Strings(missingAudit)
		t.Fatalf("optional dependency classifications missing audit evidence:\n%s", strings.Join(missingAudit, "\n"))
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
	owner      string
	evidence   string
}

func (c optionalDependencyClassification) auditViolation(occurrence optionalDependencyOccurrence) string {
	if strings.TrimSpace(string(c.category)) == "" {
		return occurrence.String() + " classification category is empty"
	}
	if strings.TrimSpace(c.owner) == "" {
		return occurrence.String() + " owner is empty"
	}
	if strings.TrimSpace(c.evidence) == "" {
		return occurrence.String() + " evidence is empty"
	}
	if !strings.Contains(c.evidence, occurrence.RelPath) {
		return occurrence.String() + " evidence must reference source path " + occurrence.RelPath
	}
	if c.category == optionalDependencyAbsence {
		if strings.TrimSpace(c.dependency) == "" {
			return occurrence.String() + " dependency_absence dependency is empty"
		}
		if strings.TrimSpace(string(c.profile)) == "" {
			return occurrence.String() + " dependency_absence profile is empty"
		}
		return ""
	}
	if strings.TrimSpace(c.dependency) != "" || strings.TrimSpace(string(c.profile)) != "" {
		return occurrence.String() + " non-policy optional classification must not carry dependency policy fields"
	}
	return ""
}

func registeredOptionalDependencyClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	dependency := func(name string, profile contract.DependencyProfile, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: optionalDependencyAbsence, dependency: name, profile: profile, owner: owner, evidence: evidence}
	}
	appAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/app", path+": "+evidence)
	}
	appDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/app", path+": "+evidence)
	}
	threadAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/module/thread", path+": "+evidence)
	}
	threadDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/module/thread", path+": "+evidence)
	}
	toolbridgeAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/platform/toolbridge", path+": "+evidence)
	}
	toolbridgeDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/platform/toolbridge", path+": "+evidence)
	}
	providerAdjunct := func(path, owner, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, owner, path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/app/runtime_reporter_adapter.go:optional_tag:Service":    appDependency("internal/app/runtime_reporter_adapter.go", "runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost, "newRuntimeReporter gates absent orchestration service through dependency policy before desktopExternalRuntimeReporter"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Logger":     appAdjunct("internal/app/runtime_reporter_adapter.go", "desktopExternalRuntimeReporter uses logger only for debug diagnostics"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Dependency": appAdjunct("internal/app/runtime_reporter_adapter.go", "appDependencyProfile resolves the mode-aware policy from Dependency or Config"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Config":     appAdjunct("internal/app/runtime_reporter_adapter.go", "appDependencyProfile resolves the fallback dependency profile from Config"),
		"internal/app/runner.go:optional_tag:RootCtx":                      appAdjunct("internal/app/runner.go", "BindRuntime validates runtime pre-drain ownership before accepting nil-adjacent root context behavior"),
		"internal/app/runner.go:optional_tag:Lifecycle":                    appAdjunct("internal/app/runner.go", "reportRuntimeExit treats Wails lifecycle as notification-only adjunct"),
		"internal/app/runner.go:optional_tag:ExtractionDrainer":            appAdjunct("internal/app/runner.go", "registerRuntimePreDrain fail-fast requires the drainer before production runtime stop hooks run"),

		"internal/app/thread_orchestration_adapter.go:typed_unsupported:thread.bind_session_generation": appDependency("internal/app/thread_orchestration_adapter.go", "thread.bind_session_generation", contract.DependencyProfileDesktopHost, "BindSessionGeneration returns MissingDependencyModeError under desktop-host facade mode"),
		"internal/app/thread_orchestration_adapter.go:noop_success:LaunchAgent":                         appAdjunct("internal/app/thread_orchestration_adapter.go", "LaunchAgent is a documented no-op because thread Start/SpawnIfNeeded owns local provider session launch"),

		"internal/module/thread/lifecycle.go:typed_unsupported:thread.bind_session_generation": threadDependency("internal/module/thread/lifecycle.go", "thread.bind_session_generation", contract.DependencyProfileDesktopHost, "BindSessionGeneration propagates MissingDependencyModeError for profiles without session-generation binding"),
		"internal/module/thread/module.go:optional_tag:Store":                                  threadAdjunct("internal/module/thread/module.go", "store port adapters preserve Fx closure while service methods fail-fast when missing"),
		"internal/module/thread/module.go:optional_tag:Catalog":                                threadAdjunct("internal/module/thread/module.go", "catalog injection is a prompt assembly adjunct covered by runtime catalog construction"),
		"internal/module/thread/module.go:optional_tag:Registrar":                              threadAdjunct("internal/module/thread/module.go", "thread prompt registration tolerates nil registrar as no-op registration boundary"),
		"internal/module/thread/module.go:optional_tag:PromptStore":                            threadAdjunct("internal/module/thread/module.go", "runtime prompt catalog can be built with nil store for test and desktop fallback surfaces"),
		"internal/module/thread/module.go:optional_tag:Builtin":                                threadAdjunct("internal/module/thread/module.go", "runtime prompt catalog treats builtin prompt registry as adjunct input"),
		"internal/module/thread/module.go:optional_tag:PromptCatalog":                          threadAdjunct("internal/module/thread/module.go", "registerThreadPromptProviders synthesizes catalog from PromptStore/Builtin when omitted"),

		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.agent_thread_lookup":          toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.agent_thread_lookup", contract.DependencyProfileDesktopHost, "validateToolbridgeDependencies maps missing BindingStore to typed dependency policy outside production"),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.thread_config_override_store": toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.thread_config_override_store", contract.DependencyProfileDesktopHost, "validateToolbridgeDependencies maps missing ThreadStore to typed dependency policy outside production"),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.lifecycle_backfiller":         toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.lifecycle_backfiller", contract.DependencyProfileTest, "validateToolbridgeDependencies maps missing lifecycle backfiller to test-only typed policy"),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.skill_tools":                  toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.skill_tools", contract.DependencyProfileTest, "validateToolbridgeDependencies maps missing skill tools to test-only typed policy"),
		"internal/platform/toolbridge/module.go:optional_tag:Resolver":                                      toolbridgeAdjunct("internal/platform/toolbridge/module.go", "validateToolbridgeDependencies in handler.go requires workdir resolver in production"),
		"internal/platform/toolbridge/module.go:optional_tag:BindingStore":                                  toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.agent_thread_lookup", contract.DependencyProfileDesktopHost, "handler.go validates BindingStore before Handler construction"),
		"internal/platform/toolbridge/module.go:optional_tag:ThreadStore":                                   toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.thread_config_override_store", contract.DependencyProfileDesktopHost, "handler.go validates ThreadStore before Handler construction"),
		"internal/platform/toolbridge/module.go:optional_tag:Preferences":                                   toolbridgeAdjunct("internal/platform/toolbridge/module.go", "validateToolbridgeDependencies in handler.go requires preferences in production"),
		"internal/platform/toolbridge/module.go:optional_tag:Config":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideToolbridgeDependencyConfig requires config and dependency profile before Handler"),
		"internal/platform/toolbridge/module.go:optional_tag:Logger":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "logger is diagnostic-only and NewHandler falls back to package logger"),
		"internal/platform/toolbridge/module.go:optional_tag:Tracer":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "tracer is observability adjunct and not a tool execution dependency"),
		"internal/platform/toolbridge/module.go:optional_tag:Dispatcher":                                    toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideDiffEmitter fails construction when dispatcher is nil"),
		"internal/platform/toolbridge/module.go:optional_tag:Lifecycle":                                     toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.lifecycle_backfiller", contract.DependencyProfileTest, "handler.go validates lifecycle backfiller before toolbridge startup"),
		"internal/platform/toolbridge/module.go:optional_tag:HostTools":                                     toolbridgeAdjunct("internal/platform/toolbridge/module.go", "validateToolbridgeDependencies in handler.go requires host tools in production"),
		"internal/platform/toolbridge/module.go:optional_tag:SkillTools":                                    toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.skill_tools", contract.DependencyProfileTest, "handler.go validates skill tools before exposing toolbridge"),
		"internal/platform/toolbridge/module.go:optional_tag:Reader":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips memory read registry when capability is absent"),
		"internal/platform/toolbridge/module.go:optional_tag:Writer":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips memory write registry when capability is absent"),
		"internal/platform/toolbridge/module.go:optional_tag:History":                                       toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips history registry when status port is absent"),
		"internal/platform/toolbridge/module.go:optional_tag:Templates":                                     toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips workflow template registry when template registry is absent"),

		"internal/provider/claudecli/module.go:optional_tag:Dependency": providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "newModeAwareRuntimeReporter requires an explicit dependency profile from Dependency or Config"),
		"internal/provider/claudecli/module.go:optional_tag:Config":     providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "dependencyProfileFromFactoryParams uses Config only to resolve profile"),
		"internal/provider/claudecli/module.go:optional_tag:Recovery":   providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "Recovery is passed to driver as an optional replay reporter"),
		"internal/provider/claudecli/module.go:optional_tag:Tracer":     providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "Tracer is observability-only and firstClaudeTracer handles nil"),

		"internal/provider/codexapp/module.go:optional_tag:Dependency":  providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "newModeAwareRuntimeReporter requires an explicit dependency profile from Dependency or Config"),
		"internal/provider/codexapp/module.go:optional_tag:Config":      providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "dependencyProfileFromFactoryParams uses Config only to resolve profile"),
		"internal/provider/codexapp/module.go:optional_tag:Recovery":    providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "Recovery is optional session recovery reporting for transport reconnects"),
		"internal/provider/codexapp/module.go:optional_tag:Logger":      providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "logger is diagnostic-only for driver factory and server manager"),
		"internal/provider/codexapp/module.go:optional_tag:PIDRegistry": providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "server manager and transport spawner tolerate nil pid registry without masking pool errors"),

		"internal/provider/unified/module.go:optional_tag:Logger":        providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "logger is diagnostic-only for client and dream executor"),
		"internal/provider/unified/module.go:optional_tag:Tracer":        providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "tracer is observability-only for unified client"),
		"internal/provider/unified/module.go:optional_tag:ThreadStore":   providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "session resolver treats thread lookup as optional cross-module recovery surface"),
		"internal/provider/unified/module.go:optional_tag:BindingStore":  providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "session resolver treats binding lookup as optional cross-module recovery surface"),
		"internal/provider/unified/module.go:optional_tag:BindingWriter": providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "session resolver treats binding writer as optional cross-module recovery surface"),
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
