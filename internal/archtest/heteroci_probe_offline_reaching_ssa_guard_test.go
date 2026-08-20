package archtest

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func (guard *heterociProbeSSAGuard) offlineReachingDefinitions(function *ast.FuncDecl, evidence *heterociProbeOfflineEvidence) bool {
	if function == nil {
		return false
	}
	ssaFunction := guard.built.Func(function.Name.Name)
	producer := guard.offlineSSAProducerCalls(ssaFunction, evidence)
	if !producer.valid() {
		return false
	}
	bodyValue, rawErr := offlineProbeResult(producer.probe, 0), offlineProbeResult(producer.probe, 1)
	if bodyValue == nil || rawErr == nil {
		return false
	}
	if !offlineSSAArg(producer.bodyNil, bodyValue) || !offlineSSAArg(producer.snapshot, rawErr) {
		return false
	}
	if !guard.offlineSSAControls(ssaFunction, producer.offline, producer.bodyNil) || !guard.offlineSSAControls(ssaFunction, producer.offline, producer.ctxErr) {
		return false
	}
	field := guard.localStructFieldIndex("probeExternalErrorSnapshot", "onlyUnavailable")
	return guard.offlineSSAFieldControls(ssaFunction, producer.offline, producer.snapshot, field)
}

type heterociProbeOfflineSSAProducerCalls struct {
	offline, probe, bodyNil, ctxErr, snapshot *ssa.Call
}

func (calls heterociProbeOfflineSSAProducerCalls) valid() bool {
	for _, call := range []*ssa.Call{calls.offline, calls.probe, calls.bodyNil, calls.ctxErr, calls.snapshot} {
		if call == nil {
			return false
		}
	}
	return true
}

func (guard *heterociProbeSSAGuard) offlineSSAProducerCalls(function *ssa.Function, evidence *heterociProbeOfflineEvidence) heterociProbeOfflineSSAProducerCalls {
	if function == nil || evidence == nil {
		return heterociProbeOfflineSSAProducerCalls{}
	}
	return heterociProbeOfflineSSAProducerCalls{
		offline:  guard.uniqueDirectCall(function, "offlineProbeSnapshot"),
		probe:    guard.offlineSSAInvokeAt(function, evidence.probeCall, guard.packageInterfaceMethodObject(guard.pkg.Types, "ProbeTransport", "Probe")),
		bodyNil:  guard.offlineSSAStaticAt(function, evidence.bodyNilCall, guard.pkg.Types.Scope().Lookup("isNilProbeValue")),
		ctxErr:   guard.offlineSSAInvokeAt(function, evidence.ctxErrCall, guard.contextErrMethodObject()),
		snapshot: guard.offlineSSAStaticAt(function, evidence.snapshotCall, guard.pkg.Types.Scope().Lookup("snapshotProbeExternalError")),
	}
}

func offlineProbeResult(call *ssa.Call, index int) ssa.Value {
	if call == nil {
		return nil
	}
	count := 0
	for range call.Common().Signature().Results().Variables() {
		count++
	}
	values := heterociProbeCallResultValues(call, index, count)
	if len(values) == 1 {
		return values[0]
	}
	return nil
}

func offlineSSAArg(call *ssa.Call, want ssa.Value) bool {
	return call != nil && len(call.Common().Args) == 1 && offlineSSAExact(call.Common().Args[0], want)
}

func (guard *heterociProbeSSAGuard) offlineSSAStaticAt(function *ssa.Function, node ast.Node, object types.Object) *ssa.Call {
	if node == nil {
		return nil
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Pos() < node.Pos() || call.Pos() > node.End() {
				continue
			}
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Object() == object {
				return call
			}
		}
	}
	return nil
}

func (guard *heterociProbeSSAGuard) offlineSSAInvokeAt(function *ssa.Function, node ast.Node, method *types.Func) *ssa.Call {
	if node == nil {
		return nil
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Pos() >= node.Pos() && call.Pos() <= node.End() && call.Common().IsInvoke() && call.Common().Method == method {
				return call
			}
		}
	}
	return nil
}

func (guard *heterociProbeSSAGuard) offlineSSAControls(function *ssa.Function, call *ssa.Call, value ssa.Value) bool {
	for _, block := range function.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 || !block.Succs[0].Dominates(call.Block()) {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if ok && offlineSSAConditionUses(branch.Cond, value) {
			return true
		}
	}
	return false
}

func offlineSSAConditionUses(value, target ssa.Value) bool {
	if offlineSSAExact(value, target) {
		return true
	}
	binary, ok := value.(*ssa.BinOp)
	return ok && (offlineSSAConditionUses(binary.X, target) || offlineSSAConditionUses(binary.Y, target))
}

func offlineSSAExact(value, target ssa.Value) bool {
	if value == target {
		return true
	}
	switch value := value.(type) {
	case *ssa.ChangeInterface:
		return offlineSSAExact(value.X, target)
	case *ssa.MakeInterface:
		return offlineSSAExact(value.X, target)
	case *ssa.ChangeType:
		return offlineSSAExact(value.X, target)
	case *ssa.Convert:
		return offlineSSAExact(value.X, target)
	case *ssa.Phi:
		if len(value.Edges) == 0 {
			return false
		}
		for _, edge := range value.Edges {
			if !offlineSSAExact(edge, target) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (guard *heterociProbeSSAGuard) offlineSSAFieldControls(function *ssa.Function, call *ssa.Call, snapshot ssa.Value, fieldIndex int) bool {
	if fieldIndex < 0 {
		return false
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			value := guard.offlineSSAFieldValue(instruction, function, call, snapshot, fieldIndex)
			if value != nil && guard.offlineSSAControls(function, call, value) {
				return true
			}
		}
	}
	return false
}

func (guard *heterociProbeSSAGuard) offlineSSAFieldValue(instruction ssa.Instruction, function *ssa.Function, offline *ssa.Call, snapshot ssa.Value, fieldIndex int) ssa.Value {
	switch field := instruction.(type) {
	case *ssa.Field:
		if field.Field == fieldIndex && offlineSSAExact(field.X, snapshot) {
			return field
		}
	case *ssa.UnOp:
		address, ok := field.X.(*ssa.FieldAddr)
		if ok && address.Field == fieldIndex && guard.offlineSSAAddress(address, function, snapshot, offline) {
			return field
		}
	}
	return nil
}

func (guard *heterociProbeSSAGuard) offlineSSAAddress(address *ssa.FieldAddr, function *ssa.Function, snapshot ssa.Value, offline *ssa.Call) bool {
	if guard.offlineSSAProtectedEscapes(address.X, function, snapshot, offline) {
		return false
	}
	roots := heterociProbeSSAMemoryRoots(address.X)
	count := 0
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			store, ok := instruction.(*ssa.Store)
			if ok && heterociProbeSetsIntersect(roots, heterociProbeSSAMemoryRoots(store.Addr)) {
				count++
				if !offlineSSAExact(store.Val, snapshot) {
					return false
				}
			}
		}
	}
	return count == 1
}

func (guard *heterociProbeSSAGuard) offlineSSAProtectedEscapes(root ssa.Value, function *ssa.Function, snapshot ssa.Value, offline *ssa.Call) bool {
	roots := heterociProbeSSAMemoryRoots(root)
	start, ok := snapshot.(*ssa.Call)
	if !ok {
		return true
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			position := instruction.Pos()
			if position != token.NoPos && (position <= start.Pos() || position >= offline.Pos()) {
				continue
			}
			if offlineSSAInstructionEscapes(instruction, roots) {
				return true
			}
		}
	}
	return false
}

func offlineSSAInstructionEscapes(instruction ssa.Instruction, roots map[ssa.Value]bool) bool {
	if offlineSSAInstructionPure(instruction) {
		return false
	}
	switch instruction := instruction.(type) {
	case *ssa.Store:
		return offlineSSAAddressArgument(instruction.Val, roots)
	case *ssa.Return:
		return offlineSSAResultsEscape(instruction.Results, roots)
	case ssa.CallInstruction:
		return offlineSSAResultsEscape(instruction.Common().Args, roots)
	default:
		return offlineSSAOperandsEscape(instruction, roots)
	}
}

func offlineSSAInstructionPure(instruction ssa.Instruction) bool {
	switch instruction.(type) {
	case *ssa.UnOp, *ssa.Field, *ssa.FieldAddr, *ssa.IndexAddr, *ssa.Slice, *ssa.Phi, *ssa.Extract, *ssa.ChangeInterface, *ssa.ChangeType, *ssa.Convert:
		return true
	default:
		return false
	}
}

func offlineSSAResultsEscape(values []ssa.Value, roots map[ssa.Value]bool) bool {
	for _, value := range values {
		if offlineSSAAddressArgument(value, roots) {
			return true
		}
	}
	return false
}

func offlineSSAOperandsEscape(instruction ssa.Instruction, roots map[ssa.Value]bool) bool {
	for _, operand := range instruction.Operands(nil) {
		if operand != nil && offlineSSAAddressArgument(*operand, roots) {
			return true
		}
	}
	return false
}

func offlineSSAAddressArgument(value ssa.Value, roots map[ssa.Value]bool) bool {
	if value == nil {
		return false
	}
	if offlineSSAAddressArgumentChildren(value, roots) {
		return true
	}
	return offlineSSAPointerLike(value) && heterociProbeSetsIntersect(roots, heterociProbeSSAMemoryRoots(value))
}

func offlineSSAAddressArgumentChildren(value ssa.Value, roots map[ssa.Value]bool) bool {
	if wrapped, ok := offlineSSAAddressWrapper(value); ok {
		return offlineSSAAddressArgument(wrapped, roots)
	}
	phi, ok := value.(*ssa.Phi)
	return ok && offlineSSAAddressPhi(phi.Edges, roots)
}

func offlineSSAAddressWrapper(value ssa.Value) (ssa.Value, bool) {
	switch value := value.(type) {
	case *ssa.ChangeInterface:
		return value.X, true
	case *ssa.MakeInterface:
		return value.X, true
	case *ssa.ChangeType:
		return value.X, true
	case *ssa.Convert:
		return value.X, true
	case *ssa.Extract:
		return value.Tuple, true
	case *ssa.Field:
		return value.X, true
	case *ssa.Index:
		return value.X, true
	default:
		return nil, false
	}
}

func offlineSSAAddressPhi(edges []ssa.Value, roots map[ssa.Value]bool) bool {
	for _, edge := range edges {
		if offlineSSAAddressArgument(edge, roots) {
			return true
		}
	}
	return false
}

func offlineSSAPointerLike(value ssa.Value) bool {
	_, pointer := value.Type().Underlying().(*types.Pointer)
	return pointer
}
