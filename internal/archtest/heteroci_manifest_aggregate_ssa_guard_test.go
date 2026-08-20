package archtest

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

// TestHeterociManifestAggregateSSAArchitectureBoundary closes the function-value
// aggregate gap while keeping the existing direct-caller owner guard authoritative.
func TestHeterociManifestAggregateSSAArchitectureBoundary(t *testing.T) {
	guard := loadHeterociManifestSSAGuard(t, nil)
	if findings := guard.aggregateFindings(); len(findings) != 0 {
		t.Fatalf("heteroci manifest aggregate SSA boundary violations:\n%s", strings.Join(findings, "\n"))
	}
	if findings := guard.findings(); len(findings) != 0 {
		t.Fatalf("existing heteroci manifest SSA owner guard violations:\n%s", strings.Join(findings, "\n"))
	}
}

func TestHeterociManifestAggregateSSARejectsHiddenFunctionValues(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{name: "slice", src: `
func illicitManifestSliceAggregate(path string) (Manifest, error) {
		decoders := []aggregateDecode{DecodeManifest}
		finishers := []aggregateFinish{finishManifestFileLoad}
		manifest, err := decoders[0](strings.NewReader(path))
		return finishers[0](manifest, err, func() error { return nil })
}
`},
		{name: "array", src: `
func illicitManifestArrayAggregate(path string) (Manifest, error) {
		var decoders [1]aggregateDecode
		decoders[0] = DecodeManifest
		var finishers [1]aggregateFinish
		finishers[0] = finishManifestFileLoad
		manifest, err := decoders[0](strings.NewReader(path))
		return finishers[0](manifest, err, func() error { return nil })
}
`},
		{name: "struct-field", src: `
type aggregateHolder struct {
		decode aggregateDecode
		finish aggregateFinish
}

func illicitManifestStructAggregate(path string) (Manifest, error) {
		value := aggregateHolder{decode: DecodeManifest, finish: finishManifestFileLoad}
		manifest, err := value.decode(strings.NewReader(path))
		return value.finish(manifest, err, func() error { return nil })
}
`},
		{name: "pointer-field", src: `
type aggregateHolder struct {
		decode aggregateDecode
		finish aggregateFinish
}

func illicitManifestPointerAggregate(path string) (Manifest, error) {
		value := &aggregateHolder{decode: DecodeManifest, finish: finishManifestFileLoad}
		manifest, err := value.decode(strings.NewReader(path))
		return value.finish(manifest, err, func() error { return nil })
}
`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := `package heteroci

import (
	"io"
	"strings"
)

type aggregateDecode func(io.Reader) (Manifest, error)
type aggregateFinish func(Manifest, error, func() error) (Manifest, error)
` + testCase.src
			guard := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source))
			findings := guard.aggregateFindings()
			if len(findings) == 0 {
				t.Fatalf("aggregate %s function values were not rejected", testCase.name)
			}
			if !containsManifestAggregateFinding(findings, "DecodeManifest") ||
				!containsManifestAggregateFinding(findings, "finishManifestFileLoad") {
				t.Fatalf("aggregate %s findings=%v, want both manifest lifecycle targets", testCase.name, findings)
			}
		})
	}
}

type heterociManifestAggregateClosureCase struct {
	name string
	src  string
}

func TestHeterociManifestAggregateSSARejectsClosureCallers(t *testing.T) {
	for _, testCase := range heterociManifestAggregateClosureCases() {
		t.Run(testCase.name, func(t *testing.T) {
			runHeterociManifestAggregateClosureCase(t, testCase)
		})
	}
}

func runHeterociManifestAggregateClosureCase(t *testing.T, testCase heterociManifestAggregateClosureCase) {
	t.Helper()
	source := heterociManifestAggregateClosureSource(testCase.src)
	guard := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source))
	findings := guard.aggregateFindings()
	if !containsManifestAggregateFinding(findings, "DecodeManifest") ||
		!containsManifestAggregateFinding(findings, "finishManifestFileLoad") {
		t.Fatalf("closure %s findings=%v, want decoder and finisher caller rejection", testCase.name, findings)
	}
}

func heterociManifestAggregateClosureSource(fixture string) string {
	return `package heteroci

import (
	"io"
	"strings"
)

type aggregateDecode func(io.Reader) (Manifest, error)
type aggregateFinish func(Manifest, error, func() error) (Manifest, error)
` + fixture
}

func heterociManifestAggregateClosureCases() []heterociManifestAggregateClosureCase {
	cases := heterociManifestAggregateClosureDirectCases()
	return append(cases, heterociManifestAggregateClosureAggregateCases()...)
}

func heterociManifestAggregateClosureDirectCases() []heterociManifestAggregateClosureCase {
	return []heterociManifestAggregateClosureCase{
		{name: "direct-closure", src: `
func illicitManifestDirectClosure(path string) (Manifest, error) {
		decode := func(reader io.Reader) (Manifest, error) { return DecodeManifest(reader) }
		finish := func(manifest Manifest, operationErr error, closeFile func() error) (Manifest, error) {
			return finishManifestFileLoad(manifest, operationErr, closeFile)
		}
		manifest, err := decode(strings.NewReader(path))
		return finish(manifest, err, func() error { return nil })
}
`},
		{name: "slice-closure", src: `
func illicitManifestSliceClosure(path string) (Manifest, error) {
		decoders := []aggregateDecode{func(reader io.Reader) (Manifest, error) { return DecodeManifest(reader) }}
		finishers := []aggregateFinish{func(manifest Manifest, operationErr error, closeFile func() error) (Manifest, error) {
			return finishManifestFileLoad(manifest, operationErr, closeFile)
		}}
		manifest, err := decoders[0](strings.NewReader(path))
		return finishers[0](manifest, err, func() error { return nil })
}
`},
	}
}

func heterociManifestAggregateClosureAggregateCases() []heterociManifestAggregateClosureCase {
	return []heterociManifestAggregateClosureCase{
		{name: "struct-closure", src: `
type aggregateClosureHolder struct {
		decode aggregateDecode
		finish aggregateFinish
}

func illicitManifestStructClosure(path string) (Manifest, error) {
		value := aggregateClosureHolder{
			decode: func(reader io.Reader) (Manifest, error) { return DecodeManifest(reader) },
			finish: func(manifest Manifest, operationErr error, closeFile func() error) (Manifest, error) {
				return finishManifestFileLoad(manifest, operationErr, closeFile)
			},
		}
		manifest, err := value.decode(strings.NewReader(path))
		return value.finish(manifest, err, func() error { return nil })
}
`},
		{name: "pointer-closure", src: `
type aggregateClosureHolder struct {
		decode aggregateDecode
		finish aggregateFinish
}

func illicitManifestPointerClosure(path string) (Manifest, error) {
		value := &aggregateClosureHolder{
			decode: func(reader io.Reader) (Manifest, error) { return DecodeManifest(reader) },
			finish: func(manifest Manifest, operationErr error, closeFile func() error) (Manifest, error) {
				return finishManifestFileLoad(manifest, operationErr, closeFile)
			},
		}
		manifest, err := value.decode(strings.NewReader(path))
		return value.finish(manifest, err, func() error { return nil })
}
`},
		{name: "nested-closure", src: `
func illicitManifestNestedClosure(path string) (Manifest, error) {
		makeFunctions := func() (aggregateDecode, aggregateFinish) {
			decode := func(reader io.Reader) (Manifest, error) { return DecodeManifest(reader) }
			finish := func(manifest Manifest, operationErr error, closeFile func() error) (Manifest, error) {
				return finishManifestFileLoad(manifest, operationErr, closeFile)
			}
			return decode, finish
		}
		decode, finish := makeFunctions()
		manifest, err := decode(strings.NewReader(path))
		return finish(manifest, err, func() error { return nil })
}
`},
	}
}

type heterociManifestAggregateEscapeCase struct {
	name    string
	imports string
	src     string
}

func TestHeterociManifestAggregateSSARejectsEscapedFunctionValues(t *testing.T) {
	for _, testCase := range heterociManifestAggregateEscapeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			runHeterociManifestAggregateEscapeCase(t, testCase)
		})
	}
}

func runHeterociManifestAggregateEscapeCase(t *testing.T, testCase heterociManifestAggregateEscapeCase) {
	t.Helper()
	source := heterociManifestAggregateEscapeSource(testCase.imports, testCase.src)
	guard := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source))
	findings := guard.aggregateFindings()
	for _, target := range []string{"DecodeManifest", "finishManifestFileLoad"} {
		if !containsManifestAggregateFinding(findings, target) {
			t.Fatalf("escape %s findings=%v, want %s rejection", testCase.name, findings, target)
		}
	}
}

func heterociManifestAggregateEscapeSource(imports, fixture string) string {
	return `package heteroci

import (
	"io"
	"strings"
` + imports + `
)

type aggregateDecode func(io.Reader) (Manifest, error)
type aggregateFinish func(Manifest, error, func() error) (Manifest, error)
` + fixture
}

func heterociManifestAggregateEscapeCases() []heterociManifestAggregateEscapeCase {
	cases := heterociManifestAggregateEscapeReflectionCases()
	cases = append(cases, heterociManifestAggregateEscapeContainerCases()...)
	return append(cases, heterociManifestAggregateEscapeCallCases()...)
}

func heterociManifestAggregateEscapeReflectionCases() []heterociManifestAggregateEscapeCase {
	return []heterociManifestAggregateEscapeCase{
		{name: "reflect-value-call", imports: `	"reflect"`, src: `
func illicitManifestReflectEscape(path string) (Manifest, error) {
		decoder := reflect.ValueOf(DecodeManifest)
		decoder.Call([]reflect.Value{reflect.ValueOf(strings.NewReader(path))})
		finisher := reflect.ValueOf(finishManifestFileLoad)
		finisher.Call(nil)
		return Manifest{}, nil
}
`},
		{name: "any-escape", imports: `	"reflect"`, src: `
func illicitManifestAnyEscape(path string) (Manifest, error) {
		var decoder any = DecodeManifest
		var finisher any = finishManifestFileLoad
		reflect.ValueOf(decoder).Call(nil)
		reflect.ValueOf(finisher).Call(nil)
		_ = strings.NewReader(path)
		return Manifest{}, nil
}
`},
	}
}

func heterociManifestAggregateEscapeContainerCases() []heterociManifestAggregateEscapeCase {
	return []heterociManifestAggregateEscapeCase{{name: "container-escape", imports: `	"reflect"`, src: `
func illicitManifestContainerEscape(path string) (Manifest, error) {
		decoders := []any{DecodeManifest}
		finishers := []any{finishManifestFileLoad}
		reflect.ValueOf(decoders[0]).Call(nil)
		reflect.ValueOf(finishers[0]).Call(nil)
		_ = strings.NewReader(path)
		return Manifest{}, nil
}
`}}
}

func heterociManifestAggregateEscapeCallCases() []heterociManifestAggregateEscapeCase {
	return []heterociManifestAggregateEscapeCase{
		{name: "return-escape", src: `
func escapedManifestDecoder() aggregateDecode { return DecodeManifest }
func escapedManifestFinisher() aggregateFinish { return finishManifestFileLoad }
func illicitManifestReturnEscape(path string) (Manifest, error) {
		decoder := escapedManifestDecoder()
		finisher := escapedManifestFinisher()
		_, _ = decoder(strings.NewReader(path))
		_, _ = finisher(Manifest{}, nil, func() error { return nil })
		return Manifest{}, nil
}
`},
		{name: "call-argument-escape", src: `
func consumeManifestDecoder(aggregateDecode) {}
func consumeManifestFinisher(aggregateFinish) {}
func illicitManifestArgumentEscape(path string) (Manifest, error) {
		consumeManifestDecoder(DecodeManifest)
		consumeManifestFinisher(finishManifestFileLoad)
		_ = strings.NewReader(path)
		return Manifest{}, nil
}
`},
	}
}

func TestHeterociManifestAggregateSSARejectsUnresolvedExactSignatures(t *testing.T) {
	source := `package heteroci

import (
	"io"
	"strings"
)

type aggregateDecode func(io.Reader) (Manifest, error)
type aggregateFinish func(Manifest, error, func() error) (Manifest, error)

func illicitManifestUnresolvedAggregate(path string) (Manifest, error) {
	var decoder aggregateDecode
	var finisher aggregateFinish
	manifest, err := decoder(strings.NewReader(path))
	return finisher(manifest, err, func() error { return nil })
}
`
	guard := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source))
	findings := guard.aggregateFindings()
	if !containsManifestAggregateFinding(findings, "DecodeManifest") ||
		!containsManifestAggregateFinding(findings, "finishManifestFileLoad") {
		t.Fatalf("unresolved exact-signature calls findings=%v, want decoder and finisher rejection", findings)
	}
}

func containsManifestAggregateFinding(findings []string, target string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, target) {
			return true
		}
	}
	return false
}

type heterociManifestAggregateState struct {
	memory map[string][]ssa.Value
}

type heterociManifestAggregateReference struct {
	caller      *ssa.Function
	instruction ssa.Instruction
}

type heterociManifestAggregateTarget struct {
	function *ssa.Function
	name     string
}

func (guard *heterociManifestSSAGuard) aggregateFindings() []string {
	var findings []string
	for _, function := range guard.allFunctions() {
		findings = append(findings, guard.aggregateFunctionFindings(function)...)
	}
	findings = append(findings, guard.aggregateEscapeFindings()...)
	findings = append(findings, guard.aggregateManifestBoundaryFindings()...)
	sort.Strings(findings)
	return dedupeHeterociManifestSSAStrings(findings)
}

func (guard *heterociManifestSSAGuard) aggregateEscapeFindings() []string {
	targets := []heterociManifestAggregateTarget{
		{function: guard.lookupFunction(guard.decoder), name: "DecodeManifest"},
		{function: guard.lookupFunction(guard.finisher), name: "finishManifestFileLoad"},
	}
	owners := guard.aggregateInstructionOwners()
	var findings []string
	for _, target := range targets {
		for _, reference := range guard.aggregateTargetReferences(target.function, owners) {
			if guard.aggregateReferenceAllowed(reference, target.function) {
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"manifest-aggregate %s function escape from %s via %s",
				target.name, guard.functionIdentity(reference.caller), heterociManifestAggregateReferenceKind(reference.instruction),
			))
		}
	}
	return findings
}

func (guard *heterociManifestSSAGuard) aggregateInstructionOwners() map[ssa.Instruction]*ssa.Function {
	owners := make(map[ssa.Instruction]*ssa.Function)
	for _, function := range guard.allFunctions() {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				owners[instruction] = function
			}
		}
	}
	return owners
}

func (guard *heterociManifestSSAGuard) aggregateTargetReferences(
	target *ssa.Function,
	owners map[ssa.Instruction]*ssa.Function,
) []heterociManifestAggregateReference {
	if target == nil {
		return nil
	}
	seen := make(map[ssa.Instruction]bool)
	var references []heterociManifestAggregateReference
	add := func(instruction ssa.Instruction) {
		if seen[instruction] || !heterociManifestAggregateInstructionUses(instruction, target) {
			return
		}
		caller := owners[instruction]
		if caller == nil {
			return
		}
		seen[instruction] = true
		references = append(references, heterociManifestAggregateReference{caller: caller, instruction: instruction})
	}
	if referrers := target.Referrers(); referrers != nil {
		for _, instruction := range *referrers {
			add(instruction)
		}
	}
	for instruction := range owners {
		add(instruction)
	}
	return references
}

func heterociManifestAggregateInstructionUses(instruction ssa.Instruction, target *ssa.Function) bool {
	for _, operand := range instruction.Operands(nil) {
		if operand != nil && *operand == target {
			return true
		}
	}
	return false
}

func (guard *heterociManifestSSAGuard) aggregateReferenceAllowed(reference heterociManifestAggregateReference, target *ssa.Function) bool {
	call, ok := reference.instruction.(*ssa.Call)
	if !ok {
		return false
	}
	common := call.Common()
	return common != nil && common.Value == target && common.StaticCallee() == target && guard.aggregateCallerIsLoader(reference.caller)
}

func heterociManifestAggregateReferenceKind(instruction ssa.Instruction) string {
	switch instruction.(type) {
	case *ssa.MakeInterface:
		return "MakeInterface"
	case *ssa.Store:
		return "Store"
	case *ssa.Return:
		return "Return"
	case *ssa.MakeClosure:
		return "closure binding"
	case *ssa.Phi, *ssa.IndexAddr, *ssa.FieldAddr, *ssa.Slice:
		return "aggregate"
	case *ssa.Call, *ssa.Go, *ssa.Defer:
		return "call"
	default:
		return fmt.Sprintf("%T", instruction)
	}
}

func (guard *heterociManifestSSAGuard) aggregateFunctionFindings(function *ssa.Function) []string {
	state := heterociManifestAggregateState{memory: make(map[string][]ssa.Value)}
	guard.collectAggregateMemory(function, state.memory)
	var findings []string
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			findings = append(findings, guard.aggregateCallFindings(function, call.Common(), state.memory)...)
		}
	}
	return findings
}

func (guard *heterociManifestSSAGuard) collectAggregateMemory(function *ssa.Function, memory map[string][]ssa.Value) {
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			store, ok := instruction.(*ssa.Store)
			if !ok {
				continue
			}
			key := heterociManifestAggregateAddress(store.Addr)
			if key != "" {
				memory[key] = append(memory[key], store.Val)
			}
		}
	}
}

func (guard *heterociManifestSSAGuard) aggregateCallFindings(
	function *ssa.Function,
	common *ssa.CallCommon,
	memory map[string][]ssa.Value,
) []string {
	if common == nil || common.Value == nil {
		return nil
	}
	targetName := guard.manifestAggregateTarget(common.Signature())
	if targetName == "" {
		return nil
	}
	if static := common.StaticCallee(); static != nil {
		return guard.aggregateResolvedFindings(function, []*ssa.Function{static})
	}
	targets := heterociManifestAggregateFunctions(common.Value, memory, nil)
	if len(targets) == 0 {
		return []string{fmt.Sprintf(
			"manifest-aggregate unresolved dynamic call in %s signature matches %s",
			guard.functionIdentity(function), targetName,
		)}
	}
	return guard.aggregateResolvedFindings(function, targets)
}

func (guard *heterociManifestSSAGuard) aggregateResolvedFindings(function *ssa.Function, targets []*ssa.Function) []string {
	var findings []string
	for _, target := range targets {
		if target.Object() == guard.decoder && !guard.aggregateCallerIsLoader(function) {
			findings = append(findings, fmt.Sprintf(
				"manifest-aggregate DecodeManifest caller %s must be LoadManifestFile",
				guard.functionIdentity(function),
			))
		}
		if target.Object() == guard.finisher && !guard.aggregateCallerIsLoader(function) {
			findings = append(findings, fmt.Sprintf(
				"manifest-aggregate finishManifestFileLoad caller %s must be LoadManifestFile",
				guard.functionIdentity(function),
			))
		}
	}
	return findings
}

func (guard *heterociManifestSSAGuard) aggregateCallerIsLoader(function *ssa.Function) bool {
	loader := guard.lookupFunction(guard.loader)
	return loader != nil && guard.functionIdentity(function) == guard.functionIdentity(loader)
}

func (guard *heterociManifestSSAGuard) manifestAggregateTarget(signature *types.Signature) string {
	if signature == nil {
		return ""
	}
	if types.Identical(signature, guard.decoder.Type()) {
		return "DecodeManifest"
	}
	if types.Identical(signature, guard.finisher.Type()) {
		return "finishManifestFileLoad"
	}
	return ""
}

func heterociManifestAggregateFunctions(value ssa.Value, memory map[string][]ssa.Value, seen map[ssa.Value]bool) []*ssa.Function {
	if seen == nil {
		seen = make(map[ssa.Value]bool)
	}
	if seen[value] {
		return nil
	}
	seen[value] = true
	defer delete(seen, value)
	switch typed := value.(type) {
	case *ssa.Function:
		return []*ssa.Function{typed}
	case *ssa.MakeClosure:
		function, _ := typed.Fn.(*ssa.Function)
		return []*ssa.Function{function}
	case *ssa.Phi:
		var functions []*ssa.Function
		for _, edge := range typed.Edges {
			functions = append(functions, heterociManifestAggregateFunctions(edge, memory, seen)...)
		}
		return dedupeHeterociManifestAggregateFunctions(functions)
	case *ssa.UnOp:
		return heterociManifestAggregateStored(typed.X, memory, seen)
	case *ssa.IndexAddr, *ssa.FieldAddr:
		return heterociManifestAggregateStored(value, memory, seen)
	default:
		wrapped, ok := heterociManifestAggregateWrapped(value)
		if !ok {
			return nil
		}
		return heterociManifestAggregateFunctions(wrapped, memory, seen)
	}
}

func heterociManifestAggregateWrapped(value ssa.Value) (ssa.Value, bool) {
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

func heterociManifestAggregateStored(address ssa.Value, memory map[string][]ssa.Value, seen map[ssa.Value]bool) []*ssa.Function {
	key := heterociManifestAggregateAddress(address)
	if key == "" {
		return nil
	}
	var functions []*ssa.Function
	for _, stored := range memory[key] {
		functions = append(functions, heterociManifestAggregateFunctions(stored, memory, seen)...)
	}
	return dedupeHeterociManifestAggregateFunctions(functions)
}

func heterociManifestAggregateAddress(value ssa.Value) string {
	switch typed := value.(type) {
	case *ssa.Alloc:
		return fmt.Sprintf("alloc:%p", typed)
	case *ssa.IndexAddr:
		return "index:" + heterociManifestAggregateAddress(typed.X) + ":" + typed.Index.String()
	case *ssa.FieldAddr:
		return fmt.Sprintf("field:%s:%d", heterociManifestAggregateAddress(typed.X), typed.Field)
	case *ssa.Slice:
		return heterociManifestAggregateAddress(typed.X)
	case *ssa.ChangeType:
		return heterociManifestAggregateAddress(typed.X)
	case *ssa.Convert:
		return heterociManifestAggregateAddress(typed.X)
	case *ssa.ChangeInterface:
		return heterociManifestAggregateAddress(typed.X)
	case *ssa.MakeInterface:
		return heterociManifestAggregateAddress(typed.X)
	case *ssa.UnOp:
		return heterociManifestAggregateAddress(typed.X)
	default:
		return ""
	}
}

func dedupeHeterociManifestAggregateFunctions(functions []*ssa.Function) []*ssa.Function {
	seen := make(map[*ssa.Function]bool)
	result := make([]*ssa.Function, 0, len(functions))
	for _, function := range functions {
		if function != nil && !seen[function] {
			seen[function] = true
			result = append(result, function)
		}
	}
	return result
}
