package resourceadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestResourceAdmissionFieldAndArchitectureGuards(t *testing.T) {
	t.Run("closed_resource_vector", testClosedResourceVector)
	t.Run("policy_field_chain", testResourcePolicyFieldChain)
	t.Run("reservation_field_chain", testResourceReservationFieldChain)
	t.Run("empty_usage_contract", testResourceEmptyUsageContract)
	t.Run("reserved_only_architecture", testResourceReservedOnlyArchitecture)
}

func testClosedResourceVector(t *testing.T) {
	vectorType := reflect.TypeFor[ResourceVector]()
	if vectorType.NumField() != 10 {
		t.Fatalf("ResourceVector field count = %d, want closed ten dimensions", vectorType.NumField())
	}
	base := resourceVectorFromBase(401)
	baseDigest, err := ComputeResourceVectorDigest(base)
	if err != nil {
		t.Fatalf("ComputeResourceVectorDigest(base) error = %v", err)
	}
	for index := 0; index < vectorType.NumField(); index++ {
		field := vectorType.Field(index)
		if field.Type.Kind() != reflect.Int64 || field.Tag.Get("json") == "" {
			t.Fatalf("ResourceVector.%s must remain a tagged int64 dimension", field.Name)
		}
		resourceAssertVectorPosition(t, index, field.Name)
		mutated := base
		reflect.ValueOf(&mutated).Elem().Field(index).SetInt(reflect.ValueOf(base).Field(index).Int() + 1)
		got, digestErr := ComputeResourceVectorDigest(mutated)
		if digestErr != nil {
			t.Fatalf("ComputeResourceVectorDigest(%s mutation) error = %v", field.Name, digestErr)
		}
		if got == baseDigest {
			t.Fatalf("resource vector digest ignored one-hot mutation of %s", field.Name)
		}
	}
}

func resourceAssertVectorPosition(t *testing.T, index int, fieldName string) {
	t.Helper()
	oneHot := ResourceVector{}
	reflect.ValueOf(&oneHot).Elem().Field(index).SetInt(int64(index + 1))
	values := resourceVectorValues(oneHot)
	for position, value := range values {
		want := int64(0)
		if position == index {
			want = int64(index + 1)
		}
		if value != want {
			t.Fatalf("resourceVectorValues omitted or reordered %s: got=%v", fieldName, values)
		}
	}
}

func testResourceEmptyUsageContract(t *testing.T) {
	if EmptyUsageWatermark != 0 {
		t.Fatalf("EmptyUsageWatermark = %d, want fixed zero", EmptyUsageWatermark)
	}
	sum := sha256.Sum256([]byte(EmptyUsageLedgerCanonical))
	computed := hex.EncodeToString(sum[:])
	if EmptyUsageLedgerDigest != computed {
		t.Fatalf("empty usage constant = %q computed=%q", EmptyUsageLedgerDigest, computed)
	}
	if computed != "c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31" {
		t.Fatalf("empty usage digest = %q, want frozen c290 contract", computed)
	}
	sqlText := resourceReadSource(t, "../../sql/queries/resource_admission.sql")
	for _, literal := range []string{EmptyUsageLedgerDomain, EmptyUsageLedgerDigest} {
		if strings.Count(sqlText, "'"+literal+"'") != 1 {
			t.Fatalf("resource reservation SQL must contain exactly one fixed literal %q", literal)
		}
	}
	if strings.Contains(sqlText, "sqlc.arg(usage_") {
		t.Fatal("resource reservation SQL made fixed empty usage state caller-controlled")
	}
}

func testResourceReservedOnlyArchitecture(t *testing.T) {
	packageDir := resourcePackageDir(t)
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("read resource admission package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		resourceInspectProductionGo(t, filepath.Join(packageDir, entry.Name()))
	}
	resourceInspectReservedSQL(t, resourceReadSource(t, "../../sql/queries/resource_admission.sql"))
}

func resourceInspectProductionGo(t *testing.T, path string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse production resource file %s: %v", path, err)
	}
	for _, imported := range parsed.Imports {
		value, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			t.Fatalf("unquote import in %s: %v", path, unquoteErr)
		}
		if strings.Contains(value, "/provider") {
			t.Fatalf("reserved-only resource package imports provider path %q in %s", value, path)
		}
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			resourceRejectAuthorityName(t, path, typed.Name.Name)
		case *ast.TypeSpec:
			resourceInspectInterfaceNames(t, path, typed)
		case *ast.BasicLit:
			resourceInspectSQLLiteral(t, path, typed)
		}
		return true
	})
}

func resourceInspectInterfaceNames(t *testing.T, path string, spec *ast.TypeSpec) {
	t.Helper()
	contract, ok := spec.Type.(*ast.InterfaceType)
	if !ok {
		return
	}
	for _, method := range contract.Methods.List {
		for _, name := range method.Names {
			resourceRejectAuthorityName(t, path, name.Name)
		}
	}
}

func resourceRejectAuthorityName(t *testing.T, path, name string) {
	t.Helper()
	lower := strings.ToLower(name)
	for _, forbidden := range []string{"appendusage", "claim", "permit", "release"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("reserved-only resource production declares forbidden authority surface %s in %s", name, path)
		}
	}
}

func resourceInspectSQLLiteral(t *testing.T, path string, literal *ast.BasicLit) {
	t.Helper()
	if literal.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return
	}
	if resourceUsageInsertPattern().MatchString(value) {
		t.Fatalf("reserved-only resource production embeds usage INSERT SQL in %s", path)
	}
}

func resourceInspectReservedSQL(t *testing.T, sqlText string) {
	t.Helper()
	if resourceUsageInsertPattern().MatchString(sqlText) {
		t.Fatal("resource admission query file inserts into resource_usage")
	}
	queryName := regexp.MustCompile(`(?im)^--\s*name:\s*([^\s]+)`)
	for _, match := range queryName.FindAllStringSubmatch(sqlText, -1) {
		resourceRejectAuthorityName(t, "resource_admission.sql", match[1])
	}
}

func resourceUsageInsertPattern() *regexp.Regexp {
	return regexp.MustCompile(`(?is)\binsert\s+into\s+(?:[a-z0-9_]*\.)?[a-z0-9_]*resource_usage[a-z0-9_]*\b`)
}

func resourceReadSource(t *testing.T, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(resourcePackageDir(t), relative))
	if err != nil {
		t.Fatalf("read resource source %s: %v", relative, err)
	}
	return string(content)
}

func resourcePackageDir(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve resource package")
	}
	return filepath.Dir(sourceFile)
}
