package turn

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type sourceLocator struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
}

type callLocator struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Calls  string `json:"calls"`
}

type schemaRegistryEntry struct {
	GoType      sourceLocator `json:"goType"`
	GoValidator sourceLocator `json:"goValidator"`
	GoConsumers []callLocator `json:"goConsumers"`
	JSValidator sourceLocator `json:"jsValidator"`
	JSConsumers []callLocator `json:"jsConsumers"`
}

type goChainLocator struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Symbol      string   `json:"symbol"`
	Calls       []string `json:"calls"`
	Strings     []string `json:"strings"`
	Identifiers []string `json:"identifiers"`
}

type goConstantLocator struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Value  string `json:"value"`
}

type mapperFieldRegistry struct {
	Aliases []string `json:"aliases"`
	Wire    string   `json:"wire"`
}

type jsMapperRegistry struct {
	Name   string                         `json:"name"`
	Path   string                         `json:"path"`
	Symbol string                         `json:"symbol"`
	Fields map[string]mapperFieldRegistry `json:"fields"`
}

type jsTerminalChainLocator struct {
	Name                 string            `json:"name"`
	Path                 string            `json:"path"`
	Symbol               string            `json:"symbol"`
	Calls                []string          `json:"calls"`
	MemberPaths          []string          `json:"memberPaths"`
	CallArguments        []string          `json:"callArguments"`
	CallMemberPaths      []string          `json:"callMemberPaths"`
	ForbiddenMemberPaths []string          `json:"forbiddenMemberPaths"`
	ForbiddenProjections []string          `json:"forbiddenProjections"`
	Projections          map[string]string `json:"projections"`
	JSXProps             []string          `json:"jsxProps"`
}

var requiredJSTerminalChainNames = []string{
	"terminal-runtime-dispatch",
	"terminal-public-error-projection",
	"terminal-public-error-notice",
	"terminal-timeline-render",
	"terminal-public-error-clipboard-sink",
	"terminal-public-error-diagnostic-projection",
}

type consumerRegistry struct {
	Version          int                            `json:"version"`
	Schemas          map[string]schemaRegistryEntry `json:"schemas"`
	GoChains         []goChainLocator               `json:"goChains"`
	GoConstants      []goConstantLocator            `json:"goConstants"`
	JSTerminalChains []jsTerminalChainLocator       `json:"jsTerminalChains"`
	JSMappers        []jsMapperRegistry             `json:"jsMappers"`
}

// TestTurnContractFieldGuard derives producer fields from canonical schemas and Go JSON tags,
// then resolves every registered production symbol through the Go AST.
func TestTurnContractFieldGuard(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadConsumerRegistry(t, root)
	if err := validateConsumerRegistry(root, registry, nil); err != nil {
		t.Fatal(err)
	}
}

// TestTurnContractFieldGuardFailsFirst proves real producer, consumer, and registry mutations fail closed.
func TestTurnContractFieldGuardFailsFirst(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadConsumerRegistry(t, root)
	t.Run("missing schema registration", func(t *testing.T) {
		mutated := cloneConsumerRegistry(t, registry)
		delete(mutated.Schemas, "TurnRefV1")
		assertGuardFailure(t, validateConsumerRegistry(root, mutated, nil), "missing schema TurnRefV1")
	})
	t.Run("missing production validator call", func(t *testing.T) {
		path := "internal/dto/turn/terminal.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "ValidateTurnTerminalV2(terminal)", "ValidateTurnRefV1(terminal)", 1)
		if mutated == source {
			t.Fatal("terminal validator mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing call ValidateTurnTerminalV2")
	})
	t.Run("unregistered production validator consumer", func(t *testing.T) {
		path := "internal/dto/turn/terminal.go"
		source := readRepositorySource(t, root, path)
		mutated := source + "\nfunc unregisteredTurnRefConsumer(value TurnRefV1) error { return ValidateTurnRefV1(value) }\n"
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "TurnRefV1 Go production consumers")
	})
	t.Run("stale Go JSON field", func(t *testing.T) {
		path := "internal/dto/turn/terminal.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "json:\"turnId\"", "json:\"legacyTurnId\"", 1)
		if mutated == source {
			t.Fatal("Go JSON tag mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "field coverage")
	})
	t.Run("missing raw terminal field", func(t *testing.T) {
		path := "internal/provider/shared/terminal_outcome.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "payload[\"status\"]", "payload[\"state\"]", 1)
		if mutated == source {
			t.Fatal("raw terminal mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing string status")
	})
	t.Run("legacy terminal wire method", func(t *testing.T) {
		path := "internal/platform/eventsurface/bind.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, `MethodTurnTerminal   = "turn/terminal"`, `MethodTurnTerminal   = "turn/completed"`, 1)
		if mutated == source {
			t.Fatal("terminal wire method mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "want \"turn/terminal\"")
	})
	t.Run("missing canonical remote republish", func(t *testing.T) {
		path := "internal/platform/eventsurface/bind.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "CanonicalTurnTerminal(ev)", "missingCanonicalTurnTerminal(ev)", 1)
		if mutated == source {
			t.Fatal("canonical republish mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing call CanonicalTurnTerminal")
	})
}

func validateConsumerRegistry(root string, registry consumerRegistry, overrides map[string]string) error {
	if registry.Version != 2 {
		return fmt.Errorf("consumer registry version = %d, want 2", registry.Version)
	}
	producers, err := canonicalSchemaFields(filepath.Join(root, "internal", "dto", "turn", "schema"))
	if err != nil {
		return err
	}
	for name, fields := range producers {
		entry, ok := registry.Schemas[name]
		if !ok {
			return fmt.Errorf("consumer registry missing schema %s", name)
		}
		if err := validateSchemaRegistryEntry(root, name, fields, entry, overrides); err != nil {
			return err
		}
	}
	for name := range registry.Schemas {
		if _, ok := producers[name]; !ok {
			return fmt.Errorf("consumer registry has stale schema %s", name)
		}
	}
	return validateRegistryChains(root, registry, overrides)
}

func validateRegistryChains(root string, registry consumerRegistry, overrides map[string]string) error {
	if err := validateExactGoConsumerCoverage(root, registry.Schemas, overrides); err != nil {
		return err
	}
	if err := validateGoChains(root, registry.GoChains, overrides); err != nil {
		return err
	}
	if err := validateGoConstants(root, registry.GoConstants, overrides); err != nil {
		return err
	}
	if err := validateJSTerminalChains(root, registry.JSTerminalChains); err != nil {
		return err
	}
	return validateJSMapperRegistry(root, registry.JSMappers)
}

func validateSchemaRegistryEntry(root, name string, schemaFields map[string]bool, entry schemaRegistryEntry, overrides map[string]string) error {
	if err := validateGoSchemaRegistryEntry(root, name, schemaFields, entry, overrides); err != nil {
		return err
	}
	return validateJSSchemaRegistryEntry(root, name, entry)
}

func validateGoSchemaRegistryEntry(root, name string, schemaFields map[string]bool, entry schemaRegistryEntry, overrides map[string]string) error {
	typeFields, err := goStructJSONFields(root, entry.GoType, overrides)
	if err != nil {
		return fmt.Errorf("%s Go type: %w", name, err)
	}
	if err := assertExactFieldCoverage(name, schemaFields, typeFields); err != nil {
		return err
	}
	if _, err := goFunction(root, entry.GoValidator, overrides); err != nil {
		return fmt.Errorf("%s Go validator: %w", name, err)
	}
	if len(entry.GoConsumers) == 0 {
		return fmt.Errorf("%s has no Go production consumer", name)
	}
	for _, consumer := range entry.GoConsumers {
		if err := validateGoCall(root, consumer.sourceLocator(), consumer.Calls, overrides); err != nil {
			return fmt.Errorf("%s Go consumer: %w", name, err)
		}
	}
	return nil
}

func validateJSSchemaRegistryEntry(root, name string, entry schemaRegistryEntry) error {
	if err := validateSourceLocator(root, entry.JSValidator, ".js"); err != nil {
		return fmt.Errorf("%s JS validator: %w", name, err)
	}
	if len(entry.JSConsumers) == 0 {
		return fmt.Errorf("%s has no JS production consumer", name)
	}
	for _, consumer := range entry.JSConsumers {
		if err := validateSourceLocator(root, consumer.sourceLocator(), ".js"); err != nil {
			return fmt.Errorf("%s JS consumer: %w", name, err)
		}
		if strings.TrimSpace(consumer.Calls) == "" {
			return fmt.Errorf("%s JS consumer has blank call target", name)
		}
	}
	return nil
}

func validateGoChains(root string, chains []goChainLocator, overrides map[string]string) error {
	if len(chains) == 0 {
		return fmt.Errorf("consumer registry has no Go production chains")
	}
	seen := map[string]bool{}
	for _, chain := range chains {
		if err := validateGoChain(root, chain, seen, overrides); err != nil {
			return err
		}
	}
	return nil
}

func validateGoChain(root string, chain goChainLocator, seen map[string]bool, overrides map[string]string) error {
	if strings.TrimSpace(chain.Name) == "" || seen[chain.Name] {
		return fmt.Errorf("Go production chain has blank or duplicate name %q", chain.Name)
	}
	seen[chain.Name] = true
	fn, err := goFunction(root, sourceLocator{Path: chain.Path, Symbol: chain.Symbol}, overrides)
	if err != nil {
		return fmt.Errorf("Go production chain %s: %w", chain.Name, err)
	}
	calls, stringsFound, identifiers := functionEvidence(fn)
	for _, requirement := range []struct {
		kind   string
		values []string
		found  map[string]bool
	}{
		{kind: "call", values: chain.Calls, found: calls},
		{kind: "string", values: chain.Strings, found: stringsFound},
		{kind: "identifier", values: chain.Identifiers, found: identifiers},
	} {
		if err := requireGoChainEvidence(chain.Name, requirement.kind, requirement.values, requirement.found); err != nil {
			return err
		}
	}
	return nil
}

func requireGoChainEvidence(chainName, kind string, values []string, found map[string]bool) error {
	for _, value := range values {
		if !found[value] {
			return fmt.Errorf("Go production chain %s missing %s %s", chainName, kind, value)
		}
	}
	return nil
}

func validateGoConstants(root string, constants []goConstantLocator, overrides map[string]string) error {
	if len(constants) == 0 {
		return fmt.Errorf("consumer registry has no Go wire constants")
	}
	seen := map[string]bool{}
	for _, constant := range constants {
		if strings.TrimSpace(constant.Name) == "" || seen[constant.Name] {
			return fmt.Errorf("Go constant has blank or duplicate name %q", constant.Name)
		}
		seen[constant.Name] = true
		value, err := goStringConstant(root, sourceLocator{Path: constant.Path, Symbol: constant.Symbol}, overrides)
		if err != nil {
			return fmt.Errorf("Go constant %s: %w", constant.Name, err)
		}
		if value != constant.Value {
			return fmt.Errorf("Go constant %s value = %q, want %q", constant.Name, value, constant.Value)
		}
	}
	return nil
}

func validateJSMapperRegistry(root string, mappers []jsMapperRegistry) error {
	if len(mappers) == 0 {
		return fmt.Errorf("consumer registry has no JS mapper chains")
	}
	seen := map[string]bool{}
	for _, mapper := range mappers {
		if err := validateJSMapper(root, mapper, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateJSTerminalChains(root string, chains []jsTerminalChainLocator) error {
	if len(chains) == 0 {
		return fmt.Errorf("consumer registry has no JS terminal chains")
	}
	seen := map[string]bool{}
	registered := make([]string, 0, len(chains))
	for _, chain := range chains {
		if strings.TrimSpace(chain.Name) == "" || seen[chain.Name] {
			return fmt.Errorf("JS terminal chain has blank or duplicate name %q", chain.Name)
		}
		seen[chain.Name] = true
		registered = append(registered, chain.Name)
		if err := validateJSSourceLocator(root, sourceLocator{Path: chain.Path, Symbol: chain.Symbol}); err != nil {
			return fmt.Errorf("JS terminal chain %s: %w", chain.Name, err)
		}
		if err := validateJSTerminalChainContract(chain); err != nil {
			return fmt.Errorf("JS terminal chain %s: %w", chain.Name, err)
		}
	}
	return assertExactConsumerSet("JS terminal chain registry", requiredJSTerminalChainNames, registered)
}

func validateJSTerminalChainContract(chain jsTerminalChainLocator) error {
	for _, requirement := range []struct {
		name   string
		values []string
	}{
		{name: "calls", values: chain.Calls},
		{name: "member paths", values: chain.MemberPaths},
		{name: "call arguments", values: chain.CallArguments},
		{name: "call member paths", values: chain.CallMemberPaths},
		{name: "forbidden member paths", values: chain.ForbiddenMemberPaths},
		{name: "forbidden projections", values: chain.ForbiddenProjections},
		{name: "JSX props", values: chain.JSXProps},
	} {
		for _, value := range requirement.values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s contains a blank value", requirement.name)
			}
		}
	}
	for target, source := range chain.Projections {
		if strings.TrimSpace(target) == "" || strings.TrimSpace(source) == "" {
			return fmt.Errorf("projections contains a blank target or source")
		}
	}
	return nil
}

func validateJSMapper(root string, mapper jsMapperRegistry, seen map[string]bool) error {
	if strings.TrimSpace(mapper.Name) == "" || seen[mapper.Name] {
		return fmt.Errorf("JS mapper has blank or duplicate name %q", mapper.Name)
	}
	seen[mapper.Name] = true
	if err := validateSourceLocator(root, sourceLocator{Path: mapper.Path, Symbol: mapper.Symbol}, ".js"); err != nil {
		return fmt.Errorf("JS mapper %s: %w", mapper.Name, err)
	}
	if len(mapper.Fields) == 0 {
		return fmt.Errorf("JS mapper %s has no fields", mapper.Name)
	}
	for field, mapping := range mapper.Fields {
		if strings.TrimSpace(field) == "" || len(mapping.Aliases) == 0 || strings.TrimSpace(mapping.Wire) == "" {
			return fmt.Errorf("JS mapper %s field %s is incomplete", mapper.Name, field)
		}
	}
	return nil
}

func (locator callLocator) sourceLocator() sourceLocator {
	return sourceLocator{Path: locator.Path, Symbol: locator.Symbol}
}

func validateGoCall(root string, locator sourceLocator, call string, overrides map[string]string) error {
	fn, err := goFunction(root, locator, overrides)
	if err != nil {
		return err
	}
	calls, _, _ := functionEvidence(fn)
	if !calls[call] {
		return fmt.Errorf("%s:%s missing call %s", locator.Path, locator.Symbol, call)
	}
	return nil
}

func functionEvidence(fn *ast.FuncDecl) (map[string]bool, map[string]bool, map[string]bool) {
	calls := map[string]bool{}
	stringsFound := map[string]bool{}
	identifiers := map[string]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CallExpr:
			if name := calledSymbol(typed.Fun); name != "" {
				calls[name] = true
			}
		case *ast.BasicLit:
			if typed.Kind == token.STRING {
				if value, err := strconv.Unquote(typed.Value); err == nil {
					stringsFound[value] = true
				}
			}
		case *ast.Ident:
			identifiers[typed.Name] = true
		}
		return true
	})
	return calls, stringsFound, identifiers
}

func calledSymbol(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func goFunction(root string, locator sourceLocator, overrides map[string]string) (*ast.FuncDecl, error) {
	if err := validateLocator(locator, ".go"); err != nil {
		return nil, err
	}
	file, err := parseGoFile(root, locator.Path, overrides)
	if err != nil {
		return nil, err
	}
	var found *ast.FuncDecl
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != locator.Symbol {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%s:%s resolves more than once", locator.Path, locator.Symbol)
		}
		found = fn
	}
	if found == nil || found.Body == nil {
		return nil, fmt.Errorf("%s:%s was not found as a function with a body", locator.Path, locator.Symbol)
	}
	return found, nil
}

func goStructJSONFields(root string, locator sourceLocator, overrides map[string]string) (map[string]bool, error) {
	if err := validateLocator(locator, ".go"); err != nil {
		return nil, err
	}
	file, err := parseGoFile(root, locator.Path, overrides)
	if err != nil {
		return nil, err
	}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != locator.Symbol {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return nil, fmt.Errorf("%s:%s is not a struct", locator.Path, locator.Symbol)
			}
			return structJSONFields(locator, structure)
		}
	}
	return nil, fmt.Errorf("%s:%s was not found as a type", locator.Path, locator.Symbol)
}

func goStringConstant(root string, locator sourceLocator, overrides map[string]string) (string, error) {
	if err := validateLocator(locator, ".go"); err != nil {
		return "", err
	}
	file, err := parseGoFile(root, locator.Path, overrides)
	if err != nil {
		return "", err
	}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		value, found, err := stringConstantFromSpecs(general.Specs, locator)
		if err != nil {
			return "", err
		}
		if found {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s:%s was not found as a constant", locator.Path, locator.Symbol)
}

func stringConstantFromSpecs(specs []ast.Spec, locator sourceLocator) (string, bool, error) {
	for _, spec := range specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		value, found, err := stringConstantFromValueSpec(valueSpec, locator)
		if err != nil {
			return "", false, err
		}
		if found {
			return value, true, nil
		}
	}
	return "", false, nil
}

func stringConstantFromValueSpec(spec *ast.ValueSpec, locator sourceLocator) (string, bool, error) {
	for index, name := range spec.Names {
		if name.Name != locator.Symbol || index >= len(spec.Values) {
			continue
		}
		literal, ok := spec.Values[index].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return "", false, fmt.Errorf("%s:%s is not a string constant", locator.Path, locator.Symbol)
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return "", false, fmt.Errorf("decode %s:%s: %w", locator.Path, locator.Symbol, err)
		}
		return value, true, nil
	}
	return "", false, nil
}

func structJSONFields(locator sourceLocator, structure *ast.StructType) (map[string]bool, error) {
	fields := map[string]bool{}
	for _, field := range structure.Fields.List {
		if len(field.Names) != 1 || field.Tag == nil {
			return nil, fmt.Errorf("%s:%s has an untagged or embedded field", locator.Path, locator.Symbol)
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return nil, fmt.Errorf("%s:%s.%s has invalid struct tag: %w", locator.Path, locator.Symbol, field.Names[0].Name, err)
		}
		jsonName, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
		if jsonName == "" || jsonName == "-" || fields[jsonName] {
			return nil, fmt.Errorf("%s:%s.%s has blank, ignored, or duplicate JSON field %q", locator.Path, locator.Symbol, field.Names[0].Name, jsonName)
		}
		fields[jsonName] = true
	}
	return fields, nil
}

func parseGoFile(root, relativePath string, overrides map[string]string) (*ast.File, error) {
	source, err := repositorySource(root, relativePath, overrides)
	if err != nil {
		return nil, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), relativePath, source, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relativePath, err)
	}
	return file, nil
}

func repositorySource(root, relativePath string, overrides map[string]string) (string, error) {
	if source, ok := overrides[relativePath]; ok {
		return source, nil
	}
	if err := validateRelativePath(relativePath); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", relativePath, err)
	}
	return string(raw), nil
}

func validateSourceLocator(root string, locator sourceLocator, extension string) error {
	if err := validateLocator(locator, extension); err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(locator.Path))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", locator.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("locator path %s is not a regular file", locator.Path)
	}
	return nil
}

func validateJSSourceLocator(root string, locator sourceLocator) error {
	if err := validateRelativePath(locator.Path); err != nil {
		return err
	}
	if strings.TrimSpace(locator.Symbol) == "" {
		return fmt.Errorf("locator symbol is blank")
	}
	if extension := filepath.Ext(locator.Path); extension != ".js" && extension != ".jsx" {
		return fmt.Errorf("locator path %s must end with .js or .jsx", locator.Path)
	}
	path := filepath.Join(root, filepath.FromSlash(locator.Path))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", locator.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("locator path %s is not a regular file", locator.Path)
	}
	return nil
}

func validateLocator(locator sourceLocator, extension string) error {
	if strings.TrimSpace(locator.Symbol) == "" {
		return fmt.Errorf("locator symbol is blank")
	}
	if err := validateRelativePath(locator.Path); err != nil {
		return err
	}
	if filepath.Ext(locator.Path) != extension {
		return fmt.Errorf("locator path %s must end with %s", locator.Path, extension)
	}
	return nil
}

func validateRelativePath(path string) error {
	path = strings.TrimSpace(path)
	clean := filepath.ToSlash(filepath.Clean(path))
	if path == "" || filepath.IsAbs(path) || clean != path || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("locator path %q must be normalized and repository-confined", path)
	}
	return nil
}

func canonicalSchemaFields(dir string) (map[string]map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read canonical schema directory: %w", err)
	}
	result := map[string]map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "field_consumers.json" {
			continue
		}
		title, fields, err := readCanonicalSchema(filepath.Join(dir, entry.Name()), entry.Name())
		if err != nil {
			return nil, err
		}
		if _, exists := result[title]; exists {
			return nil, fmt.Errorf("duplicate canonical schema title %s", title)
		}
		result[title] = fields
	}
	if len(result) != 3 {
		return nil, fmt.Errorf("expected exactly three canonical turn schemas, found %d", len(result))
	}
	return result, nil
}

func readCanonicalSchema(path, name string) (string, map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read canonical schema %s: %w", name, err)
	}
	var document struct {
		Title      string         `json:"title"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", nil, fmt.Errorf("parse canonical schema %s: %w", name, err)
	}
	if document.Title == "" || len(document.Properties) == 0 {
		return "", nil, fmt.Errorf("canonical schema %s has no title or properties", name)
	}
	fields := map[string]bool{}
	for field := range document.Properties {
		fields[field] = true
	}
	return document.Title, fields, nil
}

func assertExactFieldCoverage(name string, producerFields, consumerFields map[string]bool) error {
	missing := fieldDifference(producerFields, consumerFields)
	stale := fieldDifference(consumerFields, producerFields)
	if len(missing) == 0 && len(stale) == 0 {
		return nil
	}
	return fmt.Errorf("%s field coverage missing=%v stale=%v", name, missing, stale)
}

func fieldDifference(left, right map[string]bool) []string {
	values := make([]string, 0)
	for field := range left {
		if !right[field] {
			values = append(values, field)
		}
	}
	sort.Strings(values)
	return values
}

func loadConsumerRegistry(t *testing.T, root string) consumerRegistry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "internal", "dto", "turn", "schema", "field_consumers.json"))
	if err != nil {
		t.Fatalf("read consumer registry: %v", err)
	}
	var registry consumerRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatalf("parse consumer registry: %v", err)
	}
	return registry
}

func cloneConsumerRegistry(t *testing.T, registry consumerRegistry) consumerRegistry {
	t.Helper()
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	var clone consumerRegistry
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func readRepositorySource(t *testing.T, root, path string) string {
	t.Helper()
	source, err := repositorySource(root, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func assertGuardFailure(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("guard error = %v, want fragment %q", err, fragment)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve field guard path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
