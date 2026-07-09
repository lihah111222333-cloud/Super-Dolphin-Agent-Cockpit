package archtest_test

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

type prioritySSAGuardFixture struct {
	name         string
	files        map[string]string
	targetName   string
	wantContains []string
}

type priorityFunctionTarget struct {
	label     string
	name      string
	pos       token.Pos
	funcPos   token.Pos
	recvType  types.Type
	methodPkg *types.Package
}

func TestPrioritySSAGuardFixtures(t *testing.T) {
	t.Parallel()

	for _, tt := range prioritySSAGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckPrioritySSAFixturePackage(t, tt.files)
			ssaPkg := buildOrchestrationServiceSSAPackage(t, pkg)
			got := collectPrioritySSAGuardMessages(t, pkg, ssaPkg, priorityFixtureTarget(t, pkg, tt.targetName))
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func prioritySSAGuardFixtures() []prioritySSAGuardFixture {
	return []prioritySSAGuardFixture{
		priorityWidePortFixture(),
		priorityIgnoredReturnFixture(),
		priorityContextCancelFixture(),
		priorityRawSQLFixture(),
		priorityErrorStringFixture(),
		priorityFXInvokeFixture(),
		priorityLifecycleFixture(),
	}
}

func priorityWidePortFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:       "wide port propagation",
		files:      map[string]string{"internal/priorityfixture/wide.go": priorityWidePortFixtureSource()},
		targetName: "Wide",
		wantContains: []string{
			"field service uses broad port priorityfixture.Wide",
			"parameter service in returns uses broad port priorityfixture.Wide",
			"return value service in returns uses broad port priorityfixture.Wide",
		},
	}
}

func priorityIgnoredReturnFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "ignored critical return",
		files:        map[string]string{"internal/priorityfixture/ignored_return.go": priorityIgnoredReturnFixtureSource()},
		wantContains: []string{"ignored return from Subscribe"},
	}
}

func priorityContextCancelFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "ignored context cancel",
		files:        map[string]string{"internal/priorityfixture/context_cancel.go": priorityContextCancelFixtureSource()},
		wantContains: []string{"ignored cancel func from context.WithCancel"},
	}
}

func priorityRawSQLFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "raw sql receiver",
		files:        map[string]string{"internal/priorityfixture/sql.go": priorityRawSQLFixtureSource()},
		wantContains: []string{"raw SQL call QueryContext"},
	}
}

func priorityErrorStringFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:  "error string matching",
		files: map[string]string{"internal/priorityfixture/error_string.go": priorityErrorStringFixtureSource()},
		wantContains: []string{
			"error string match strings.Contains",
			"error string match strings.HasSuffix",
		},
	}
}

func priorityFXInvokeFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:         "fx invoke helper side effect",
		files:        map[string]string{"internal/priorityfixture/fx_invoke.go": priorityFXInvokeFixtureSource()},
		wantContains: []string{"fx.Invoke target startRuntime calls exec command"},
	}
}

func priorityLifecycleFixture() prioritySSAGuardFixture {
	return prioritySSAGuardFixture{
		name:  "lifecycle onstart helper side effect",
		files: map[string]string{"internal/priorityfixture/lifecycle.go": priorityLifecycleFixtureSource()},
		wantContains: []string{
			"fx.Hook OnStart target func literal sleeps or creates timer",
			"fx.Hook OnStart target startRuntime calls exec command",
		},
	}
}

func priorityWidePortFixtureSource() string {
	return `package priorityfixture

type Wide interface {
	Launch()
	Stop()
	Snapshot()
}

type holder struct { service Wide }

func returns(service Wide) Wide { return service }
`
}

func priorityIgnoredReturnFixtureSource() string {
	return `package priorityfixture

import "context"

type Dispatcher struct{}
type Event struct{}

func Subscribe(*Dispatcher, func(Event)) context.CancelFunc { return func() {} }

func ignored(dispatcher *Dispatcher) {
	subscribe := Subscribe
	subscribe(dispatcher, func(Event) {})
}
`
}

func priorityRawSQLFixtureSource() string {
	return `package priorityfixture

import (
	"context"
	"database/sql"
)

func raw(ctx context.Context, db *sql.DB) {
	query := db.QueryContext
	query(ctx, "select 1")
}
`
}

func priorityContextCancelFixtureSource() string {
	return `package priorityfixture

import (
	"context"
	"time"
)

func leak(parent context.Context) context.Context {
	ctx, _ := context.WithCancel(parent)
	return ctx
}

func ok(parent context.Context) context.Context {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	return ctx
}
`
}

func priorityErrorStringFixtureSource() string {
	return `package priorityfixture

import (
	"fmt"
	"strings"
)

func badFormatted(err error) bool {
	return strings.Contains(fmt.Sprint(err), "missing")
}

func badHelper(err error) bool {
	return strings.HasSuffix(errorText(err), "missing")
}

func errorText(err error) string {
	return err.Error()
}

func ok(state string) bool {
	return strings.Contains(fmt.Sprintf("state:%s", state), "ready")
}
`
}

func priorityFXInvokeFixtureSource() string {
	return `package priorityfixture

import "os/exec"

type Option struct{}
type fxAPI struct{}

func (fxAPI) Invoke(any) Option { return Option{} }

var fx fxAPI

func register() Option {
	return fx.Invoke(startRuntime)
}

func startRuntime() {
	runCommand()
}

func runCommand() {
	exec.Command("true")
}
`
}

func priorityLifecycleFixtureSource() string {
	return `package priorityfixture

import (
	"context"
	"os/exec"
	"time"
)

type Lifecycle interface { Append(Hook) }
type Hook struct { OnStart func(context.Context) error }
type runtime struct{}

func register(lc Lifecycle, rt *runtime) {
	lc.Append(Hook{OnStart: func(context.Context) error {
		waitRuntime()
		return nil
	}})
	lc.Append(Hook{OnStart: rt.startRuntime})
}

func (rt *runtime) startRuntime(context.Context) error {
	exec.Command("true")
	return nil
}

func waitRuntime() {
	time.Sleep(0)
}
`
}

func typeCheckPrioritySSAFixturePackage(t *testing.T, files map[string]string) *orchestrationServiceCheckedPackage {
	t.Helper()

	fset, syntax := parseOrchestrationServiceTypeGuardFixture(t, files)
	info := newOrchestrationServiceTypesInfo()
	conf := types.Config{
		Importer: importer.Default(),
	}
	checked, err := conf.Check(superAgentModulePath+"/internal/priorityfixture", fset, syntax, info)
	if err != nil {
		t.Fatalf("type check priority SSA fixture: %v", err)
	}
	return &orchestrationServiceCheckedPackage{
		pkgPath:   superAgentModulePath + "/internal/priorityfixture",
		fset:      fset,
		syntax:    syntax,
		types:     checked,
		typesInfo: info,
	}
}

func priorityFixtureTarget(t *testing.T, pkg *orchestrationServiceCheckedPackage, name string) *types.TypeName {
	t.Helper()
	if name == "" {
		return nil
	}
	obj, ok := pkg.types.Scope().Lookup(name).(*types.TypeName)
	if !ok {
		t.Fatalf("fixture target %s not found", name)
	}
	return obj
}

func collectPrioritySSAGuardMessages(
	t *testing.T,
	pkg *orchestrationServiceCheckedPackage,
	ssaPkg *ssa.Package,
	target *types.TypeName,
) []string {
	t.Helper()

	var messages []string
	if target != nil {
		messages = append(messages, collectPriorityWidePortMessages(t, pkg, target)...)
	}
	for _, fn := range orchestrationServiceSSAFunctions(ssaPkg) {
		messages = append(messages, collectPriorityIgnoredReturnMessages(pkg, fn)...)
		messages = append(messages, collectPriorityContextCancelMessages(pkg, fn)...)
		messages = append(messages, collectPriorityRawSQLMessages(pkg, fn)...)
		messages = append(messages, collectPriorityErrorStringMessages(pkg, fn)...)
		messages = append(messages, collectPriorityFXInvokeMessages(pkg, fn)...)
	}
	messages = append(messages, collectPriorityLifecycleMessages(pkg, ssaPkg)...)
	sort.Strings(messages)
	return messages
}

func collectPriorityWidePortMessages(
	t *testing.T,
	pkg *orchestrationServiceCheckedPackage,
	target *types.TypeName,
) []string {
	t.Helper()
	label := priorityTargetLabel(target)
	uses := collectOrchestrationServiceSSAUses(t, pkg, target)
	messages := make([]string, 0, len(uses))
	for _, use := range uses {
		messages = append(messages, priorityUseMessage(use, "uses broad port "+label))
	}
	return messages
}

func collectPriorityIgnoredReturnMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !priorityCallResultIgnored(call) {
			continue
		}
		name, ok := priorityIgnoredReturnCallee(&call.Call)
		if ok {
			messages = append(messages, priorityPositionMessage(pkg, call.Pos(), "ignored return from "+name))
		}
	}
	return messages
}

func collectPriorityContextCancelMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !priorityContextCancelResultDiscarded(call) {
			continue
		}
		name, _ := priorityContextCancelCall(&call.Call)
		detail := "ignored cancel func from context." + name
		messages = append(messages, priorityPositionMessage(pkg, call.Pos(), detail))
	}
	return messages
}

func collectPriorityRawSQLMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok {
			continue
		}
		name, ok := priorityRawSQLCall(&call.Call)
		if ok {
			messages = append(messages, priorityPositionMessage(pkg, call.Pos(), "raw SQL call "+name))
		}
	}
	return messages
}

func collectPriorityErrorStringMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok {
			continue
		}
		name, ok := priorityErrorStringMatchCall(&call.Call)
		if ok {
			messages = append(messages, priorityPositionMessage(pkg, call.Pos(), "error string match "+name))
		}
	}
	return messages
}

func collectPriorityFXInvokeMessages(pkg *orchestrationServiceCheckedPackage, fn *ssa.Function) []string {
	var messages []string
	for _, instr := range priorityInstructions(fn) {
		call, ok := instr.(*ssa.Call)
		if !ok || !priorityIsFXInvokeCall(&call.Call) {
			continue
		}
		for _, arg := range call.Call.Args {
			target := priorityFunctionValue(arg)
			if target == nil || priorityRootBridgeAllowed(pkg, target) {
				continue
			}
			for _, reason := range priorityFunctionSideEffects(target, 2, map[*ssa.Function]bool{}) {
				detail := fmt.Sprintf("fx.Invoke target %s %s", target.Name(), reason)
				messages = append(messages, priorityPositionMessage(pkg, call.Pos(), detail))
			}
		}
	}
	return messages
}

func collectPriorityLifecycleMessages(pkg *orchestrationServiceCheckedPackage, ssaPkg *ssa.Package) []string {
	targets := priorityOnStartFunctionTargets(pkg)
	if len(targets) == 0 {
		return nil
	}
	functions := priorityFunctionsByName(ssaPkg)
	functionsByPos := priorityFunctionsByPos(ssaPkg)
	var messages []string
	for _, target := range targets {
		fn := priorityFunctionForTarget(ssaPkg, target, functions, functionsByPos)
		if fn == nil || priorityRootBridgeAllowed(pkg, fn) {
			continue
		}
		for _, reason := range priorityFunctionSideEffects(fn, 2, map[*ssa.Function]bool{}) {
			detail := fmt.Sprintf("fx.Hook OnStart target %s %s", target.label, reason)
			messages = append(messages, priorityPositionMessage(pkg, target.pos, detail))
		}
	}
	return messages
}
