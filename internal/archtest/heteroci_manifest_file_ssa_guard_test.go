package archtest

import (
	"fmt"
	"go/ast"
	gotoken "go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

const heterociManifestSSAImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"

type heterociManifestSSAFunction struct {
	object *types.Func
	ssa    *ssa.Function
	file   string
}

type heterociManifestSSAValueContext struct {
	value    ssa.Value
	bindings heterociManifestSSABindings
}

type heterociManifestSSABindings map[ssa.Value][]heterociManifestSSAValueContext

type heterociManifestSSATarget struct {
	function *ssa.Function
	bindings heterociManifestSSABindings
}

type heterociManifestSSAGuard struct {
	root          string
	pkg           *packages.Package
	program       *ssa.Program
	ssaPkg        *ssa.Package
	manifestType  types.Type
	loader        *types.Func
	decoder       *types.Func
	finisher      *types.Func
	functions     []heterociManifestSSAFunction
	globals       map[*ssa.Global][]ssa.Value
	forbidden     map[types.Object]string
	finishDenied  map[types.Object]string
	allowedInvoke map[types.Object]bool
}

func TestHeterociManifestFileSSAArchitectureBoundary(t *testing.T) {
	guard := loadHeterociManifestSSAGuard(t, nil)
	if findings := guard.findings(); len(findings) != 0 {
		t.Fatalf("heteroci manifest SSA boundary violations:\n%s", strings.Join(findings, "\n"))
	}
}

func TestHeterociManifestFileSSARejectsAliasedMethodProducer(t *testing.T) {
	overlay := heterociManifestSSAAdditionalOverlay(t, `package heteroci
type manifestAlias = Manifest
type secondManifestLoader struct{}
func (secondManifestLoader) LoadAgain(string) (manifestAlias, error) { return Manifest{}, nil }
`)
	assertHeterociManifestSSAFinding(t, loadHeterociManifestSSAGuard(t, overlay).findings(), "manifest-producer", "LoadAgain")
}

func TestHeterociManifestFileSSARejectsAliasedFinalizerCaller(t *testing.T) {
	overlay := heterociManifestSSAAdditionalOverlay(t, `package heteroci
func illicitFinishCaller(manifest Manifest) error {
	first := finishManifestFileLoad
	second := first
	_, err := second(manifest, nil, func() error { return nil })
	return err
}
`)
	assertHeterociManifestSSAFinding(t, loadHeterociManifestSSAGuard(t, overlay).findings(), "finish-caller", "illicitFinishCaller")
}

func TestHeterociManifestFileSSARejectsReceiverEnvironmentHelper(t *testing.T) {
	overlay := heterociManifestSSALoaderOverlay(t, `package heteroci
import (
	stdos "os"
	"strings"
)
type manifestPathReceiver struct{}
func (manifestPathReceiver) path() string { return stdos.Getenv("HETEROCI_MANIFEST_PATH") }
func LoadManifestFile(path string) (Manifest, error) {
	var source manifestPathReceiver
	pathSource := source.path
	_ = pathSource()
	manifest, err := DecodeManifest(strings.NewReader(path))
	return finishManifestFileLoad(manifest, err, func() error { return nil })
}
`)
	assertHeterociManifestSSAFinding(t, loadHeterociManifestSSAGuard(t, overlay).findings(), "host-discovery", "os.Getenv")
}

func TestHeterociManifestFileSSARejectsTwoHopFunctionValue(t *testing.T) {
	overlay := heterociManifestSSALoaderOverlay(t, `package heteroci
import (
	stdos "os"
	"runtime"
	"strings"
)
func manifestPathFromEnvironment() string { return stdos.Getenv("HETEROCI_MANIFEST_PATH") }
func LoadManifestFile(path string) (Manifest, error) {
	first := manifestPathFromEnvironment
	second := first
	_ = second()
	_ = runtime.GOOS
	manifest, err := DecodeManifest(strings.NewReader(path))
	return finishManifestFileLoad(manifest, err, func() error { return nil })
}
`)
	assertHeterociManifestSSAFinding(t, loadHeterociManifestSSAGuard(t, overlay).findings(), "host-discovery", "os.Getenv")
	assertHeterociManifestSSAFinding(t, loadHeterociManifestSSAGuard(t, overlay).findings(), "host-discovery", "runtime.GOOS")
}

func TestHeterociManifestFileSSAIgnoresLocalShadow(t *testing.T) {
	overlay := heterociManifestSSALoaderOverlay(t, `package heteroci
import "strings"
type localOS struct{}
func (localOS) Getenv(string) string { return "explicit" }
func LoadManifestFile(path string) (Manifest, error) {
	os := localOS{}
	_ = os.Getenv("HETEROCI_MANIFEST_PATH")
	manifest, err := DecodeManifest(strings.NewReader(path))
	return finishManifestFileLoad(manifest, err, func() error { return nil })
}
`)
	if findings := loadHeterociManifestSSAGuard(t, overlay).findings(); len(findings) != 0 {
		t.Fatalf("local shadow produced false positive:\n%s", strings.Join(findings, "\n"))
	}
}

func heterociManifestSSAAdditionalOverlay(t *testing.T, source string) map[string][]byte {
	t.Helper()
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "devtools", "heteroci", "manifest_ssa_guard_overlay.go")
	return map[string][]byte{path: []byte(source)}
}

func heterociManifestSSALoaderOverlay(t *testing.T, source string) map[string][]byte {
	t.Helper()
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "devtools", "heteroci", "manifest_file.go")
	source += `
func finishManifestFileLoad(manifest Manifest, operationErr error, closeFile func() error) (Manifest, error) {
	closeErr := closeFile()
	if operationErr != nil {
		return Manifest{}, operationErr
	}
	if closeErr != nil {
		return Manifest{}, closeErr
	}
	return manifest, nil
}
`
	return map[string][]byte{path: []byte(source)}
}

func loadHeterociManifestSSAGuard(t *testing.T, overlay map[string][]byte) *heterociManifestSSAGuard {
	t.Helper()
	root := findRepoRoot(t)
	pkgs, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./internal/devtools/heteroci"},
		Overlay:  overlay,
		Include: func(pkg *packages.Package) bool {
			return pkg.PkgPath == heterociManifestSSAImportPath
		},
	})
	if err != nil {
		t.Fatalf("load heteroci manifest SSA package: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("load heteroci manifest SSA package count = %d, want 1", len(pkgs))
	}
	program, built, err := ssaload.Build(pkgs)
	if err != nil {
		t.Fatalf("build heteroci manifest SSA package: %v", err)
	}
	if len(built) != 1 {
		t.Fatalf("build heteroci manifest SSA package count = %d, want 1", len(built))
	}
	guard := &heterociManifestSSAGuard{
		root:          root,
		pkg:           pkgs[0],
		program:       program,
		ssaPkg:        built[0],
		globals:       make(map[*ssa.Global][]ssa.Value),
		forbidden:     make(map[types.Object]string),
		finishDenied:  make(map[types.Object]string),
		allowedInvoke: make(map[types.Object]bool),
	}
	guard.initialize(t)
	return guard
}

func (guard *heterociManifestSSAGuard) initialize(t *testing.T) {
	t.Helper()
	manifest := guard.pkg.Types.Scope().Lookup("Manifest")
	loader, loaderOK := guard.pkg.Types.Scope().Lookup("LoadManifestFile").(*types.Func)
	decoder, decoderOK := guard.pkg.Types.Scope().Lookup("DecodeManifest").(*types.Func)
	finisher, finisherOK := guard.pkg.Types.Scope().Lookup("finishManifestFileLoad").(*types.Func)
	if manifest == nil || !loaderOK || !decoderOK || !finisherOK {
		t.Fatal("heteroci manifest SSA package is missing Manifest or its loader lifecycle functions")
	}
	guard.manifestType = manifest.Type()
	guard.loader = loader
	guard.decoder = decoder
	guard.finisher = finisher
	guard.functions = guard.sourceFunctions(t)
	guard.collectGlobalValues()
	guard.collectForbiddenObjects(t)
}

func (guard *heterociManifestSSAGuard) sourceFunctions(t *testing.T) []heterociManifestSSAFunction {
	t.Helper()
	var functions []heterociManifestSSAFunction
	for _, file := range guard.pkg.Syntax {
		for _, declaration := range file.Decls {
			decl, ok := declaration.(*ast.FuncDecl)
			if !ok || decl.Name == nil {
				continue
			}
			object, _ := guard.pkg.TypesInfo.Defs[decl.Name].(*types.Func)
			if object == nil {
				t.Fatalf("function %s has no go/types object", decl.Name.Name)
			}
			function := guard.lookupFunction(object)
			if function == nil {
				t.Fatalf("function %s has no SSA body", types.ObjectString(object, nil))
			}
			functions = append(functions, heterociManifestSSAFunction{
				object: object,
				ssa:    function,
				file:   guard.relativePosition(object.Pos()),
			})
		}
	}
	sort.Slice(functions, func(i, j int) bool {
		return guard.functionIdentity(functions[i].ssa) < guard.functionIdentity(functions[j].ssa)
	})
	return functions
}

func (guard *heterociManifestSSAGuard) lookupFunction(object *types.Func) *ssa.Function {
	signature, _ := object.Type().(*types.Signature)
	if signature == nil {
		return nil
	}
	if signature.Recv() == nil {
		return guard.ssaPkg.Func(object.Name())
	}
	return guard.program.LookupMethod(signature.Recv().Type(), object.Pkg(), object.Name())
}

func (guard *heterociManifestSSAGuard) findings() []string {
	findings := guard.producerFindings()
	findings = append(findings, guard.decodeCallerFindings()...)
	findings = append(findings, guard.finishLifecycleFindings()...)
	findings = append(findings, guard.loaderClosureFindings()...)
	sort.Strings(findings)
	return dedupeHeterociManifestSSAStrings(findings)
}

func (guard *heterociManifestSSAGuard) producerFindings() []string {
	allowedSeen := map[string]int{}
	var findings []string
	for _, function := range guard.functions {
		if !guard.returnsManifest(function.object) {
			continue
		}
		owner, allowed := guard.allowedProducer(function)
		if !allowed {
			findings = append(findings, fmt.Sprintf(
				"manifest-producer %s:%s returns types.Identical(Manifest) without an explicit owner",
				function.file,
				types.ObjectString(function.object, nil),
			))
			continue
		}
		allowedSeen[owner]++
	}
	for _, owner := range []string{"DecodeManifest", "LoadManifestFile", "finishManifestFileLoad", "cloneRoutingManifest"} {
		if allowedSeen[owner] != 1 {
			findings = append(findings, fmt.Sprintf("manifest-producer owner %s count=%d want=1", owner, allowedSeen[owner]))
		}
	}
	return findings
}

func (guard *heterociManifestSSAGuard) returnsManifest(object *types.Func) bool {
	signature, _ := object.Type().(*types.Signature)
	if signature == nil {
		return false
	}
	for result := range signature.Results().Variables() {
		if types.Identical(result.Type(), guard.manifestType) {
			return true
		}
	}
	return false
}

func (guard *heterociManifestSSAGuard) allowedProducer(function heterociManifestSSAFunction) (string, bool) {
	signature, _ := function.object.Type().(*types.Signature)
	if signature == nil || signature.Recv() != nil {
		return "", false
	}
	key := function.file + ":" + function.object.Name()
	switch key {
	case "internal/devtools/heteroci/manifest.go:DecodeManifest":
		return "DecodeManifest", guard.exactManifestErrorResults(signature)
	case "internal/devtools/heteroci/manifest_file.go:LoadManifestFile":
		return "LoadManifestFile", guard.allowedLoader(signature)
	case "internal/devtools/heteroci/manifest_file.go:finishManifestFileLoad":
		return "finishManifestFileLoad", guard.exactFinishSignature(signature)
	case "internal/devtools/heteroci/routing_lease_book.go:cloneRoutingManifest":
		return "cloneRoutingManifest", guard.allowedClone(signature)
	default:
		return "", false
	}
}

func (guard *heterociManifestSSAGuard) allowedLoader(signature *types.Signature) bool {
	return guard.exactManifestErrorResults(signature) && signature.Params().Len() == 1 &&
		types.Identical(signature.Params().At(0).Type(), types.Typ[types.String])
}

func (guard *heterociManifestSSAGuard) allowedClone(signature *types.Signature) bool {
	return signature.Params().Len() == 1 && signature.Results().Len() == 1 &&
		types.Identical(signature.Params().At(0).Type(), guard.manifestType) &&
		types.Identical(signature.Results().At(0).Type(), guard.manifestType)
}

func (guard *heterociManifestSSAGuard) exactManifestErrorResults(signature *types.Signature) bool {
	return signature.Results().Len() == 2 &&
		types.Identical(signature.Results().At(0).Type(), guard.manifestType) &&
		types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type())
}

func (guard *heterociManifestSSAGuard) exactFinishSignature(signature *types.Signature) bool {
	if !guard.exactManifestErrorResults(signature) || signature.Params().Len() != 3 {
		return false
	}
	closeSignature, _ := signature.Params().At(2).Type().(*types.Signature)
	return types.Identical(signature.Params().At(0).Type(), guard.manifestType) &&
		types.Identical(signature.Params().At(1).Type(), types.Universe.Lookup("error").Type()) &&
		closeSignature != nil && closeSignature.Params().Len() == 0 && closeSignature.Results().Len() == 1 &&
		types.Identical(closeSignature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

func (guard *heterociManifestSSAGuard) decodeCallerFindings() []string {
	callers := make(map[*types.Func]bool)
	for _, root := range guard.functions {
		guard.walk(root.ssa, func(caller *ssa.Function, target heterociManifestSSATarget) {
			if target.function.Object() == guard.decoder {
				if object, _ := caller.Object().(*types.Func); object != nil {
					callers[object] = true
				}
			}
		}, nil)
	}
	if len(callers) == 1 && callers[guard.loader] {
		return nil
	}
	var names []string
	for caller := range callers {
		names = append(names, types.ObjectString(caller, nil))
	}
	sort.Strings(names)
	return []string{fmt.Sprintf("decode-caller callers=%v want=[LoadManifestFile]", names)}
}

func (guard *heterociManifestSSAGuard) finishLifecycleFindings() []string {
	callers := make(map[*types.Func]bool)
	for _, root := range guard.functions {
		guard.walk(root.ssa, func(caller *ssa.Function, target heterociManifestSSATarget) {
			if target.function.Object() == guard.finisher {
				if object, _ := caller.Object().(*types.Func); object != nil {
					callers[object] = true
				}
			}
		}, nil)
	}
	var findings []string
	if len(callers) != 1 || !callers[guard.loader] {
		var names []string
		for caller := range callers {
			names = append(names, types.ObjectString(caller, nil))
		}
		sort.Strings(names)
		findings = append(findings, fmt.Sprintf("finish-caller callers=%v want=[LoadManifestFile]", names))
	}
	loader := guard.lookupFunction(guard.loader)
	guard.walk(loader, func(caller *ssa.Function, target heterociManifestSSATarget) {
		if caller.Object() != guard.finisher {
			return
		}
		if reason, denied := guard.finishDenied[target.function.Object()]; denied {
			findings = append(findings, fmt.Sprintf("finish-lifecycle finishManifestFileLoad reaches %s", reason))
		}
	}, nil)
	return findings
}

func (guard *heterociManifestSSAGuard) loaderClosureFindings() []string {
	loader := guard.lookupFunction(guard.loader)
	if loader == nil {
		return []string{"host-discovery LoadManifestFile has no SSA function"}
	}
	var findings []string
	guard.walk(loader, func(caller *ssa.Function, target heterociManifestSSATarget) {
		if reason, forbidden := guard.forbiddenReason(target.function.Object()); forbidden {
			findings = append(findings, fmt.Sprintf("host-discovery %s reaches %s", guard.functionIdentity(caller), reason))
		}
	}, func(function *ssa.Function) {
		findings = append(findings, guard.forbiddenSyntaxFindings(function)...)
		findings = append(findings, heterociManifestSSAInvokeFindings(guard, function)...)
	})
	return findings
}

func (guard *heterociManifestSSAGuard) walk(
	root *ssa.Function,
	visitCall func(*ssa.Function, heterociManifestSSATarget),
	visitFunction func(*ssa.Function),
) {
	queue := []heterociManifestSSATarget{{function: root, bindings: make(heterociManifestSSABindings)}}
	visited := make(map[string]bool)
	for len(queue) > 0 {
		target := queue[0]
		queue = queue[1:]
		if target.function == nil || !guard.localFunction(target.function) {
			continue
		}
		key := guard.targetKey(target)
		if visited[key] {
			continue
		}
		visited[key] = true
		if visitFunction != nil {
			visitFunction(target.function)
		}
		for _, block := range target.function.Blocks {
			queue = append(queue, guard.walkBlock(target, block, visitCall)...)
		}
	}
}

func (guard *heterociManifestSSAGuard) walkBlock(
	caller heterociManifestSSATarget,
	block *ssa.BasicBlock,
	visitCall func(*ssa.Function, heterociManifestSSATarget),
) []heterociManifestSSATarget {
	var local []heterociManifestSSATarget
	for _, instruction := range block.Instrs {
		call, ok := instruction.(ssa.CallInstruction)
		if !ok {
			continue
		}
		for _, callee := range guard.resolveCall(call.Common(), caller.bindings) {
			if visitCall != nil {
				visitCall(caller.function, callee)
			}
			if guard.localFunction(callee.function) {
				local = append(local, callee)
			}
		}
	}
	return local
}

func (guard *heterociManifestSSAGuard) resolveCall(call *ssa.CallCommon, bindings heterociManifestSSABindings) []heterociManifestSSATarget {
	if call == nil {
		return nil
	}
	if static := call.StaticCallee(); static != nil {
		return []heterociManifestSSATarget{{function: static, bindings: bindHeterociManifestSSAParams(static, call.Args, bindings)}}
	}
	if _, builtin := call.Value.(*ssa.Builtin); builtin {
		return nil
	}
	contexts := guard.resolveValues(heterociManifestSSAValueContext{value: call.Value, bindings: bindings}, make(map[ssa.Value]bool))
	var targets []heterociManifestSSATarget
	for _, context := range contexts {
		if target, ok := heterociManifestSSATargetFromValue(context, call.Args, bindings); ok {
			targets = append(targets, target)
		}
	}
	return dedupeHeterociManifestSSATargets(targets)
}

func (guard *heterociManifestSSAGuard) resolveValues(
	context heterociManifestSSAValueContext,
	seen map[ssa.Value]bool,
) []heterociManifestSSAValueContext {
	value := context.value
	if heterociManifestSSAValueSeen(value, seen) {
		return nil
	}
	seen[value] = true
	defer delete(seen, value)
	if bound, ok := context.bindings[value]; ok {
		return guard.resolveValueList(bound, seen)
	}
	if heterociManifestSSAIsFunctionValue(value) {
		return []heterociManifestSSAValueContext{context}
	}
	if phi, ok := value.(*ssa.Phi); ok {
		values := make([]heterociManifestSSAValueContext, 0, len(phi.Edges))
		for _, edge := range phi.Edges {
			values = append(values, heterociManifestSSAValueContext{value: edge, bindings: context.bindings})
		}
		return guard.resolveValueList(values, seen)
	}
	if unwrapped, ok := heterociManifestSSAUnwrap(value); ok {
		return guard.resolveValues(heterociManifestSSAValueContext{value: unwrapped, bindings: context.bindings}, seen)
	}
	unary, ok := value.(*ssa.UnOp)
	if !ok {
		return nil
	}
	global, ok := unary.X.(*ssa.Global)
	if !ok {
		return nil
	}
	values := make([]heterociManifestSSAValueContext, 0, len(guard.globals[global]))
	for _, stored := range guard.globals[global] {
		values = append(values, heterociManifestSSAValueContext{value: stored, bindings: context.bindings})
	}
	return guard.resolveValueList(values, seen)
}

func (guard *heterociManifestSSAGuard) resolveValueList(
	values []heterociManifestSSAValueContext,
	seen map[ssa.Value]bool,
) []heterociManifestSSAValueContext {
	var resolved []heterociManifestSSAValueContext
	for _, value := range values {
		resolved = append(resolved, guard.resolveValues(value, seen)...)
	}
	return resolved
}

func heterociManifestSSAIsFunctionValue(value ssa.Value) bool {
	switch value.(type) {
	case *ssa.Function, *ssa.MakeClosure:
		return true
	default:
		return false
	}
}

func heterociManifestSSAUnwrap(value ssa.Value) (ssa.Value, bool) {
	switch typed := value.(type) {
	case *ssa.ChangeType:
		return typed.X, true
	case *ssa.Convert:
		return typed.X, true
	case *ssa.ChangeInterface:
		return typed.X, true
	case *ssa.MakeInterface:
		return typed.X, true
	default:
		return nil, false
	}
}

func heterociManifestSSATargetFromValue(
	context heterociManifestSSAValueContext,
	args []ssa.Value,
	callerBindings heterociManifestSSABindings,
) (heterociManifestSSATarget, bool) {
	switch value := context.value.(type) {
	case *ssa.Function:
		return heterociManifestSSATarget{function: value, bindings: bindHeterociManifestSSAParams(value, args, callerBindings)}, true
	case *ssa.MakeClosure:
		function, _ := value.Fn.(*ssa.Function)
		if function == nil {
			return heterociManifestSSATarget{}, false
		}
		bindings := make(heterociManifestSSABindings)
		for index, free := range function.FreeVars {
			if index < len(value.Bindings) {
				bindings[free] = []heterociManifestSSAValueContext{{value: value.Bindings[index], bindings: context.bindings}}
			}
		}
		bindHeterociManifestSSAValues(bindings, function.Params, args, callerBindings)
		return heterociManifestSSATarget{function: function, bindings: bindings}, true
	default:
		return heterociManifestSSATarget{}, false
	}
}

func bindHeterociManifestSSAParams(
	function *ssa.Function,
	args []ssa.Value,
	callerBindings heterociManifestSSABindings,
) heterociManifestSSABindings {
	bindings := make(heterociManifestSSABindings)
	bindHeterociManifestSSAValues(bindings, function.Params, args, callerBindings)
	return bindings
}

func bindHeterociManifestSSAValues(
	bindings heterociManifestSSABindings,
	parameters []*ssa.Parameter,
	args []ssa.Value,
	callerBindings heterociManifestSSABindings,
) {
	for index, parameter := range parameters {
		if index < len(args) {
			bindings[parameter] = []heterociManifestSSAValueContext{{value: args[index], bindings: callerBindings}}
		}
	}
}

func (guard *heterociManifestSSAGuard) collectGlobalValues() {
	for _, function := range guard.allFunctions() {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				store, ok := instruction.(*ssa.Store)
				if !ok {
					continue
				}
				if global, ok := store.Addr.(*ssa.Global); ok {
					guard.globals[global] = append(guard.globals[global], store.Val)
				}
			}
		}
	}
}

func (guard *heterociManifestSSAGuard) allFunctions() []*ssa.Function {
	seen := make(map[*ssa.Function]bool)
	var functions []*ssa.Function
	var appendFunction func(*ssa.Function)
	appendFunction = func(function *ssa.Function) {
		if function == nil || seen[function] {
			return
		}
		seen[function] = true
		functions = append(functions, function)
		for _, child := range function.AnonFuncs {
			appendFunction(child)
		}
	}
	for _, member := range guard.ssaPkg.Members {
		if function, ok := member.(*ssa.Function); ok {
			appendFunction(function)
		}
	}
	for _, function := range guard.functions {
		appendFunction(function.ssa)
	}
	return functions
}

func (guard *heterociManifestSSAGuard) collectForbiddenObjects(t *testing.T) {
	t.Helper()
	osPackage := findHeterociManifestSSATypesPackage(guard.pkg.Types, "os", make(map[*types.Package]bool))
	fsPackage := findHeterociManifestSSATypesPackage(guard.pkg.Types, "io/fs", make(map[*types.Package]bool))
	if osPackage == nil || fsPackage == nil {
		t.Fatal("heteroci manifest SSA type graph is missing os or io/fs")
	}
	fileInfo := fsPackage.Scope().Lookup("FileInfo")
	if fileInfo == nil {
		t.Fatal("heteroci manifest SSA type graph is missing io/fs.FileInfo")
	}
	mode := types.NewMethodSet(fileInfo.Type()).Lookup(fsPackage, "Mode")
	if mode == nil {
		t.Fatal("heteroci manifest SSA type graph is missing io/fs.FileInfo.Mode")
	}
	guard.allowedInvoke[mode.Obj()] = true
	for _, name := range []string{"Args", "Getenv", "LookupEnv", "Environ", "UserHomeDir", "Hostname", "Getwd", "Executable"} {
		guard.addForbiddenObject(t, osPackage, name)
	}
	open := osPackage.Scope().Lookup("Open")
	if open == nil {
		t.Fatal("finalizer denied object os.Open is missing")
	}
	guard.finishDenied[open] = "os.Open"
	guard.finishDenied[guard.decoder] = "DecodeManifest"
}

func (guard *heterociManifestSSAGuard) addForbiddenObject(t *testing.T, pkg *types.Package, name string) {
	t.Helper()
	object := pkg.Scope().Lookup(name)
	if object == nil {
		t.Fatalf("forbidden object %s.%s is missing", pkg.Path(), name)
	}
	guard.forbidden[object] = pkg.Path() + "." + name
}

func (guard *heterociManifestSSAGuard) forbiddenReason(object types.Object) (string, bool) {
	if reason, forbidden := guard.forbidden[object]; forbidden {
		return reason, true
	}
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "runtime" ||
		object.Name() != "GOOS" && object.Name() != "GOARCH" {
		return "", false
	}
	return "runtime." + object.Name(), true
}

func (guard *heterociManifestSSAGuard) forbiddenSyntaxFindings(function *ssa.Function) []string {
	node := function.Syntax()
	if node == nil {
		return nil
	}
	var findings []string
	ast.Inspect(node, func(current ast.Node) bool {
		if literal, ok := current.(*ast.FuncLit); ok && literal != node {
			return false
		}
		identifier, ok := current.(*ast.Ident)
		if !ok {
			return true
		}
		object := guard.pkg.TypesInfo.Uses[identifier]
		if reason, forbidden := guard.forbiddenReason(object); forbidden {
			findings = append(findings, fmt.Sprintf("host-discovery %s references %s", guard.functionIdentity(function), reason))
		}
		return true
	})
	return findings
}

func (guard *heterociManifestSSAGuard) localFunction(function *ssa.Function) bool {
	if function == nil {
		return false
	}
	if function.Package() != nil && function.Package().Pkg == guard.pkg.Types {
		return true
	}
	object := function.Object()
	return object != nil && object.Pkg() == guard.pkg.Types || function.Synthetic != ""
}

func (guard *heterociManifestSSAGuard) relativePosition(position gotoken.Pos) string {
	filename := guard.pkg.Fset.Position(position).Filename
	relative, err := filepath.Rel(guard.root, filename)
	if err != nil {
		return filepath.ToSlash(filename)
	}
	return filepath.ToSlash(relative)
}

func (guard *heterociManifestSSAGuard) functionIdentity(function *ssa.Function) string {
	if function == nil {
		return "<nil>"
	}
	if object := function.Object(); object != nil {
		return types.ObjectString(object, nil)
	}
	return function.String()
}

func (guard *heterociManifestSSAGuard) targetKey(target heterociManifestSSATarget) string {
	parts := []string{fmt.Sprintf("%p", target.function)}
	for _, parameter := range target.function.Params {
		for _, context := range target.bindings[parameter] {
			parts = append(parts, fmt.Sprintf("%p=%p", parameter, context.value))
		}
	}
	for _, free := range target.function.FreeVars {
		for _, context := range target.bindings[free] {
			parts = append(parts, fmt.Sprintf("%p=%p", free, context.value))
		}
	}
	return strings.Join(parts, "|")
}

func dedupeHeterociManifestSSATargets(targets []heterociManifestSSATarget) []heterociManifestSSATarget {
	seen := make(map[string]bool)
	result := make([]heterociManifestSSATarget, 0, len(targets))
	for _, target := range targets {
		key := fmt.Sprintf("%p/%v", target.function, target.bindings)
		if !seen[key] {
			seen[key] = true
			result = append(result, target)
		}
	}
	return result
}
