package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRemoteCIRefreshResultSchemaUsesOnlyRefreshOutcomeVocabulary keeps the
// detached refresh CLI report distinct from normal workload test receipts.
func TestRemoteCIRefreshResultSchemaUsesOnlyRefreshOutcomeVocabulary(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "cmd", "super-dolphin-gate", "remote_refresh.go")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read refresh result schema: %v", err)
	}
	if violations := remoteCIRefreshResultSchemaViolations(t, string(contents)); len(violations) != 0 {
		t.Fatalf("refresh result schema retained normal-test vocabulary or lost strict fields: %v", violations)
	}
}

func TestRemoteCIRefreshResultSchemaGuardCounterexample(t *testing.T) {
	safe := `package fixture
type remoteBaselineRefreshResult struct {
 SchemaVersion uint32 ` + "`json:\"schema_version\"`" + `
 Authority string ` + "`json:\"authority\"`" + `
 Outcome string ` + "`json:\"outcome\"`" + `
 Phase string ` + "`json:\"phase\"`" + `
 State string ` + "`json:\"state\"`" + `
}`
	if violations := remoteCIRefreshResultSchemaViolations(t, safe); len(violations) != 0 {
		t.Fatalf("strict refresh-only schema was rejected: %v", violations)
	}
	legacy := `package fixture
type remoteBaselineRefreshResult struct {
 SchemaVersion uint32 ` + "`json:\"schema_version\"`" + `
 Reused bool ` + "`json:\"reused\"`" + `
 Passed bool ` + "`json:\"passed\"`" + `
 TestPass bool ` + "`json:\"test_pass\"`" + `
}`
	if violations := remoteCIRefreshResultSchemaViolations(t, legacy); len(violations) < 4 {
		t.Fatalf("legacy normal-test schema was not fully rejected: %v", violations)
	}
}

func remoteCIRefreshResultSchemaViolations(t *testing.T, source string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "refresh_result.go", source, 0)
	if err != nil {
		t.Fatalf("parse refresh result schema: %v", err)
	}
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "remoteBaselineRefreshResult" {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return []string{"result is not a struct"}
			}
			return remoteCIRefreshResultStructViolations(structType)
		}
	}
	return []string{"missing remoteBaselineRefreshResult schema"}
}

func remoteCIRefreshResultStructViolations(schema *ast.StructType) []string {
	required := map[string]bool{
		"schema_version": false,
		"authority":      false,
		"outcome":        false,
		"phase":          false,
		"state":          false,
	}
	var violations []string
	for _, field := range schema.Fields.List {
		name := ""
		if len(field.Names) != 0 {
			name = field.Names[0].Name
		}
		jsonName := ""
		if field.Tag != nil {
			jsonName = strings.Split(reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json"), ",")[0]
		}
		vocabulary := strings.ToLower(name + " " + jsonName)
		for _, forbidden := range []string{"reused", "passed", "test_pass"} {
			if strings.Contains(vocabulary, forbidden) {
				violations = append(violations, "forbidden refresh result vocabulary "+forbidden)
			}
		}
		if _, ok := required[jsonName]; ok {
			required[jsonName] = true
		}
	}
	for field, found := range required {
		if !found {
			violations = append(violations, "missing required refresh result field "+field)
		}
	}
	return violations
}
