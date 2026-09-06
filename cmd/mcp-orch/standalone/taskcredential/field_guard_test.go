package taskcredential

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// recordFieldConsumers 显式返回每个 credential record 字段的 owner 消费职责。
func recordFieldConsumers() map[string]string {
	return map[string]string{
		"digest":     "digest index and active identity snapshot key",
		"leaseID":    "lease index and revocation lookup",
		"state":      "pending active revoked transition guard",
		"generation": "activate and authenticate CAS fence",
		"expiresAt":  "issue activate and authenticate expiry fence",
	}
}

func TestCredentialRecordFieldGuard(t *testing.T) {
	consumers := recordFieldConsumers()
	productionFields := reflectedRecordFields()
	registeredFields := sortedMapKeys(consumers)
	assertExactStringSet(t, "credentialRecord fields", productionFields, registeredFields)

	selectors := productionSelectors(t)
	for _, field := range productionFields {
		if strings.TrimSpace(consumers[field]) == "" {
			t.Errorf("credentialRecord field %q has an empty consumer reason", field)
		}
		if !selectors[field] {
			t.Errorf("credentialRecord field %q is registered but has no production selector consumer", field)
		}
	}
}

func TestIssuedCredentialMaterialFieldGuard(t *testing.T) {
	issuedFields := reflectedTypeFields[IssuedCredential]()
	assertExactStringSet(t, "IssuedCredential fields", issuedFields, []string{"holder"})

	holderFields := reflectedTypeFields[issuedBearer]()
	assertExactStringSet(t, "issuedBearer fields", holderFields, []string{"bearer", "leaseID", "mu"})

	selectors := productionSelectors(t)
	for _, field := range append(issuedFields, holderFields...) {
		if !selectors[field] {
			t.Errorf("issued credential field %q is registered but has no production selector consumer", field)
		}
	}
}

func TestCredentialStateRegistrationGuard(t *testing.T) {
	productionFile := parseProductionFile(t)
	declared := declaredCredentialStates(t, productionFile)
	registered := registeredCredentialStateNames(t, productionFile)
	assertExactStringSet(t, "credential states", declared, registered)

	states := allCredentialStates()
	seenValues := make(map[credentialState]string, len(states))
	for _, state := range states {
		if previous, exists := seenValues[state]; exists {
			t.Errorf("credential state value %d is duplicated by %s", state, previous)
		}
		seenValues[state] = "credentialStates"
		if !validCredentialState(state) {
			t.Errorf("registered credential state %d is rejected by validCredentialState", state)
		}
	}
	if validCredentialState(0) || validCredentialState(^credentialState(0)) {
		t.Fatal("validCredentialState accepted an unregistered value")
	}
}

func reflectedRecordFields() []string {
	return reflectedTypeFields[credentialRecord]()
}

func reflectedTypeFields[T any]() []string {
	recordType := reflect.TypeFor[T]()
	fields := make([]string, 0, recordType.NumField())
	for index := range recordType.NumField() {
		fields = append(fields, recordType.Field(index).Name)
	}
	sort.Strings(fields)
	return fields
}

func productionSelectors(t *testing.T) map[string]bool {
	t.Helper()
	selectors := make(map[string]bool)
	ast.Inspect(parseProductionFile(t), func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok {
			selectors[selector.Sel.Name] = true
		}
		return true
	})
	return selectors
}

func declaredCredentialStates(t *testing.T, file *ast.File) []string {
	t.Helper()
	states := make([]string, 0)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range valueSpec.Names {
				if strings.HasPrefix(name.Name, "credentialState") {
					states = append(states, name.Name)
				}
			}
		}
	}
	sort.Strings(states)
	return states
}

func registeredCredentialStateNames(t *testing.T, file *ast.File) []string {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "allCredentialStates" {
			continue
		}
		return returnedCompositeIdentifierNames(t, function)
	}
	t.Fatal("allCredentialStates registration was not found")
	return nil
}

func returnedCompositeIdentifierNames(t *testing.T, function *ast.FuncDecl) []string {
	t.Helper()
	if function.Body == nil || len(function.Body.List) != 1 {
		t.Fatal("allCredentialStates must contain exactly one return statement")
	}
	returnStatement, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		t.Fatal("allCredentialStates must return exactly one value")
	}
	composite, ok := returnStatement.Results[0].(*ast.CompositeLit)
	if !ok {
		t.Fatal("allCredentialStates must return a composite literal")
	}
	names := make([]string, 0, len(composite.Elts))
	for _, element := range composite.Elts {
		identifier, ok := element.(*ast.Ident)
		if !ok {
			t.Fatal("allCredentialStates entries must be direct state identifiers")
		}
		names = append(names, identifier.Name)
	}
	sort.Strings(names)
	return names
}

func parseProductionFile(t *testing.T) *ast.File {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve field guard source path")
	}
	productionPath := filepath.Join(filepath.Dir(testFile), "registry.go")
	source, err := os.ReadFile(productionPath)
	if err != nil {
		t.Fatalf("read %s: %v", productionPath, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), productionPath, source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", productionPath, err)
	}
	return file
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func assertExactStringSet(t *testing.T, name string, produced, registered []string) {
	t.Helper()
	missing := stringSetDifference(produced, registered)
	stale := stringSetDifference(registered, produced)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s drift: missing=%v stale=%v", name, missing, stale)
	}
}

func stringSetDifference(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	difference := make([]string, 0)
	for _, value := range left {
		if _, exists := rightSet[value]; !exists {
			difference = append(difference, value)
		}
	}
	return difference
}
