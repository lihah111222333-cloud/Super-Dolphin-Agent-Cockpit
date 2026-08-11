package archtest

import "go/ast"

// localPassEnvironmentDigestParityErrors dynamically compares every producer
// field to the exact digest payload mapper. Only the policy hash derivation is
// registered; schema and domain are canonical framing, not producer fields.
func localPassEnvironmentDigestParityErrors(environment, payload *ast.StructType, digest *ast.FuncDecl) []string {
	receiver, problems, ok := localPassEnvironmentDigestPreconditions(environment, payload, digest)
	if !ok {
		return problems
	}
	producerFields := localPassGoJSONFields(environment)
	payloadFields := localPassGoJSONFields(payload)
	payloadSet := localPassFieldSet(payloadFields)
	problems = append(problems, localPassDuplicateFieldProblems("producer", producerFields)...)
	problems = append(problems, localPassDuplicateFieldProblems("payload", payloadFields)...)
	literal, found := localPassDigestPayloadLiteral(digest, "localWorkloadPassEnvironmentPayload")
	if !found {
		return append(problems, "local PASS environment digest must construct exactly one localWorkloadPassEnvironmentPayload")
	}
	values, literalProblems := localPassPayloadLiteralValues(literal)
	problems = append(problems, literalProblems...)
	problems = append(problems, localPassPayloadFieldParityProblems(payloadFields, localPassFieldSet(producerFields), values, digest, receiver)...)
	problems = append(problems, localPassMapperStalePayloadProblems(values, payloadSet)...)
	return append(problems, localPassProducerFieldParityProblems(producerFields, payloadSet)...)
}

func localPassEnvironmentDigestPreconditions(environment, payload *ast.StructType, digest *ast.FuncDecl) (string, []string, bool) {
	if environment == nil || payload == nil || digest == nil {
		return "", []string{"local PASS environment, digest payload and digest function are all required"}, false
	}
	receiver, ok := localPassEnvironmentDigestReceiver(digest)
	if !ok {
		return "", []string{"local PASS environment digest must name its LocalWorkloadPassEnvironment parameter"}, false
	}
	return receiver, nil, true
}

func localPassPayloadFieldParityProblems(payloadFields []string, producers map[string]bool, values map[string]ast.Expr, digest *ast.FuncDecl, receiver string) []string {
	var problems []string
	for _, field := range payloadFields {
		if problem := localPassPayloadFieldParityProblem(field, producers, values[field], values[field] != nil, digest, receiver); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems
}

func localPassPayloadFieldParityProblem(field string, producers map[string]bool, value ast.Expr, present bool, digest *ast.FuncDecl, receiver string) string {
	if !present {
		return "local PASS digest payload field " + field + " is not produced"
	}
	if localPassCanonicalPayloadField(field, value) {
		return ""
	}
	if field == "RunnerSemanticPolicyDigest" {
		return localPassPolicyDerivationProblem(producers, digest, value, receiver)
	}
	if !producers[field] {
		return "local PASS digest payload field " + field + " has no producer field"
	}
	if selected, ok := localPassEnvironmentFieldSelector(value, receiver); !ok || selected != field {
		return "local PASS digest payload field " + field + " must map directly from environment." + field
	}
	return ""
}

func localPassPolicyDerivationProblem(producers map[string]bool, digest *ast.FuncDecl, value ast.Expr, receiver string) string {
	if !producers["RunnerSemanticPolicy"] {
		return "local PASS digest policy derivation has no RunnerSemanticPolicy producer"
	}
	if !localPassRunnerSemanticPolicyDerivationValid(digest, value, receiver) {
		return "local PASS digest policy derivation must hash environment.RunnerSemanticPolicy"
	}
	return ""
}

func localPassMapperStalePayloadProblems(values map[string]ast.Expr, payloadSet map[string]bool) []string {
	var problems []string
	for field := range values {
		if !payloadSet[field] {
			problems = append(problems, "local PASS digest mapper produces stale payload field "+field)
		}
	}
	return problems
}

func localPassProducerFieldParityProblems(producerFields []string, payloadSet map[string]bool) []string {
	var problems []string
	for _, field := range producerFields {
		if field == "RunnerSemanticPolicy" {
			if !payloadSet["RunnerSemanticPolicyDigest"] {
				problems = append(problems, "local PASS producer field RunnerSemanticPolicy is missing its registered digest derivation")
			}
			continue
		}
		if !payloadSet[field] {
			problems = append(problems, "local PASS producer field "+field+" is missing from digest payload")
		}
	}
	return problems
}

func localPassEnvironmentDigestReceiver(function *ast.FuncDecl) (string, bool) {
	if function == nil || function.Type == nil || function.Type.Params == nil {
		return "", false
	}
	for _, parameter := range function.Type.Params.List {
		typeName, named := parameter.Type.(*ast.Ident)
		if !named || typeName.Name != "LocalWorkloadPassEnvironment" || len(parameter.Names) != 1 {
			continue
		}
		return parameter.Names[0].Name, true
	}
	return "", false
}

func localPassFieldSet(fields []string) map[string]bool {
	set := make(map[string]bool, len(fields))
	for _, field := range fields {
		set[field] = true
	}
	return set
}

func localPassDuplicateFieldProblems(kind string, fields []string) []string {
	seen := make(map[string]bool, len(fields))
	var problems []string
	for _, field := range fields {
		if seen[field] {
			problems = append(problems, "local PASS "+kind+" field "+field+" is duplicated")
		}
		seen[field] = true
	}
	return problems
}

func localPassDigestPayloadLiteral(function *ast.FuncDecl, typeName string) (*ast.CompositeLit, bool) {
	var literal *ast.CompositeLit
	ast.Inspect(function.Body, func(node ast.Node) bool {
		candidate, ok := node.(*ast.CompositeLit)
		if !ok || literal != nil {
			return literal == nil
		}
		name, named := candidate.Type.(*ast.Ident)
		if named && name.Name == typeName {
			literal = candidate
		}
		return literal == nil
	})
	return literal, literal != nil
}

func localPassPayloadLiteralValues(literal *ast.CompositeLit) (map[string]ast.Expr, []string) {
	values := make(map[string]ast.Expr, len(literal.Elts))
	var problems []string
	for _, element := range literal.Elts {
		entry, keyed := element.(*ast.KeyValueExpr)
		key, named := entry.Key.(*ast.Ident)
		if !keyed || !named {
			problems = append(problems, "local PASS digest payload requires named fields")
			continue
		}
		if _, exists := values[key.Name]; exists {
			problems = append(problems, "local PASS digest payload maps field "+key.Name+" more than once")
		}
		values[key.Name] = entry.Value
	}
	return values, problems
}

func localPassCanonicalPayloadField(field string, value ast.Expr) bool {
	selector, ok := value.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok || owner.Name != "cicontract" {
		return false
	}
	return (field == "SchemaVersion" && selector.Sel.Name == "LocalWorkloadPassEnvironmentSchemaVersion") ||
		(field == "Domain" && selector.Sel.Name == "LocalWorkloadPassEnvironmentDomain")
}

func localPassEnvironmentFieldSelector(value ast.Expr, receiver string) (string, bool) {
	selector, ok := value.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok || owner.Name != receiver {
		return "", false
	}
	return selector.Sel.Name, true
}

func localPassRunnerSemanticPolicyDerivationValid(function *ast.FuncDecl, value ast.Expr, receiver string) bool {
	arguments, ok := localPassSelectorCallArguments(value, "fmt", "Sprintf", 2)
	return ok && localPassSprintfPolicyDigestArguments(arguments) && localPassPolicyDigestAssignment(function, receiver)
}

func localPassPolicyDigestAssignment(function *ast.FuncDecl, receiver string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, assigned := node.(*ast.AssignStmt)
		if !assigned || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		name, named := assignment.Lhs[0].(*ast.Ident)
		if !named || name.Name != "policyDigest" || !localPassSHA256PolicyDigestCall(assignment.Rhs[0], receiver) {
			return true
		}
		found = true
		return false
	})
	return found
}

func localPassSHA256PolicyDigestCall(value ast.Expr, receiver string) bool {
	arguments, ok := localPassSelectorCallArguments(value, "sha256", "Sum256", 1)
	return ok && localPassBytePolicyConversion(arguments[0], receiver)
}

func localPassSelectorCallArguments(value ast.Expr, ownerName, method string, count int) ([]ast.Expr, bool) {
	call, ok := value.(*ast.CallExpr)
	if !ok || len(call.Args) != count {
		return nil, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok || owner.Name != ownerName || selector.Sel.Name != method {
		return nil, false
	}
	return call.Args, true
}

func localPassSprintfPolicyDigestArguments(arguments []ast.Expr) bool {
	format, literal := arguments[0].(*ast.BasicLit)
	policyDigest, identifier := arguments[1].(*ast.Ident)
	return literal && format.Value == `"sha256:%x"` && identifier && policyDigest.Name == "policyDigest"
}

func localPassBytePolicyConversion(value ast.Expr, receiver string) bool {
	conversion, ok := value.(*ast.CallExpr)
	if !ok || len(conversion.Args) != 1 {
		return false
	}
	conversionType, ok := conversion.Fun.(*ast.ArrayType)
	if !ok || conversionType.Len != nil {
		return false
	}
	element, ok := conversionType.Elt.(*ast.Ident)
	field, selected := localPassEnvironmentFieldSelector(conversion.Args[0], receiver)
	return ok && element.Name == "byte" && selected && field == "RunnerSemanticPolicy"
}
