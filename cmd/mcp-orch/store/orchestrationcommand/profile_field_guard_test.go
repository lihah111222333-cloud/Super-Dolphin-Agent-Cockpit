package orchestrationcommand

import (
	"database/sql"
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
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const profileTableName = "orchestration_command_intent_profiles"

type profileFieldSpec struct {
	Path   string
	Column string
}

func TestSendProfileSelectorMirrorsIdentityCurrentBinding(t *testing.T) {
	currentType := reflect.TypeFor[identity.CurrentBinding]()
	selectorType := reflect.TypeFor[ActiveBindingSelector]()
	current := profileStructFields(t, currentType)
	selector := profileStructFields(t, selectorType)
	exemptions := map[string]string{
		"Version": "profile selector version discriminator",
		"Kind":    "profile selector kind discriminator",
	}
	aliases := map[string]string{
		"FenceRef":             "SourceFenceRef",
		"BindingSchemaVersion": "SchemaVersion",
	}
	typeConversions := map[string]string{
		"UpdatedAt": "identity RFC3339Nano string is parsed into profile UTC time.Time",
	}
	assertProfileSelectorRegistries(t, selector, current, exemptions, aliases, typeConversions)
	assertProfileSelectorCoverage(t, selectorType, current, exemptions, aliases, typeConversions)
}

func assertProfileSelectorRegistries(t *testing.T, selector, current map[string]reflect.StructField, exemptions, aliases, conversions map[string]string) {
	t.Helper()
	for name, reason := range exemptions {
		if _, ok := selector[name]; !ok || strings.TrimSpace(reason) == "" {
			t.Fatalf("ActiveBindingSelector.%s exemption is stale or has no reason", name)
		}
	}
	for selectorName, currentName := range aliases {
		if _, ok := selector[selectorName]; !ok {
			t.Fatalf("ActiveBindingSelector alias source %s is stale", selectorName)
		}
		if _, ok := current[currentName]; !ok {
			t.Fatalf("identity.CurrentBinding alias target %s is stale", currentName)
		}
	}
	for name, reason := range conversions {
		if _, ok := selector[name]; !ok || strings.TrimSpace(reason) == "" {
			t.Fatalf("ActiveBindingSelector.%s type conversion is stale or has no reason", name)
		}
	}
}

func assertProfileSelectorCoverage(t *testing.T, selectorType reflect.Type, current map[string]reflect.StructField, exemptions, aliases, conversions map[string]string) {
	t.Helper()
	covered := make(map[string]bool, len(current))
	for index := 0; index < selectorType.NumField(); index++ {
		field := selectorType.Field(index)
		if _, ok := exemptions[field.Name]; ok {
			continue
		}
		currentName := aliases[field.Name]
		if currentName == "" {
			currentName = field.Name
		}
		currentField, ok := current[currentName]
		if !ok {
			t.Fatalf("ActiveBindingSelector.%s has no identity.CurrentBinding field (canonical %s)", field.Name, currentName)
		}
		if field.Type != currentField.Type && strings.TrimSpace(conversions[field.Name]) == "" {
			t.Fatalf("ActiveBindingSelector.%s type = %s, identity.CurrentBinding.%s type = %s", field.Name, field.Type, currentName, currentField.Type)
		}
		covered[currentName] = true
	}
	for name := range current {
		if !covered[name] {
			t.Fatalf("identity.CurrentBinding.%s is missing from ActiveBindingSelector", name)
		}
	}
}

func TestSendProfilePersistenceFieldsStayExact(t *testing.T) {
	want := profilePersistenceColumns(t)
	rowColumns := profileDBFields(t, reflect.TypeFor[storedb.OrchestrationCommandIntentProfile]())
	assertProfileExactSet(t, "SQLC profile row", want, rowColumns)
	db := openCommandIntentTestDB(t)
	schemaColumns := profileSQLiteColumns(t, db, profileTableName)
	assertProfileExactSet(t, "SQLite profile table", want, schemaColumns)
	assertProfileColumnsExcludeLifecycleAndPayload(t, schemaColumns)
	assertProfileWriteParamColumns(t)
}

func TestSendProfileMapperCoverage(t *testing.T) {
	functions := parseProfileMappingFunctions(t, profileMappingSourcePath(t))
	assertProfileWriteMapper(t, functions)
	assertProfileReadMapper(t, functions)
	if os.Getenv("ORCH_PROFILE_MAPPING_FIXTURE") == "" {
		assertProfileRuntimeRoundTrip(t)
	}
}

func profileStructFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if _, exists := fields[field.Name]; exists {
			t.Fatalf("%s has duplicate field %s", typ.Name(), field.Name)
		}
		fields[field.Name] = field
	}
	return fields
}

func profileFieldSpecs(t *testing.T) []profileFieldSpec {
	t.Helper()
	profileType := reflect.TypeFor[SendMessageProfile]()
	specs := make([]profileFieldSpec, 0, 56)
	for index := 0; index < profileType.NumField(); index++ {
		field := profileType.Field(index)
		switch field.Type {
		case reflect.TypeFor[CallerSnapshot](), reflect.TypeFor[AuthorizationDecision](), reflect.TypeFor[ActiveBindingSelector]():
			specs = append(specs, profileNestedFieldSpecs(t, field)...)
		default:
			specs = append(specs, profileFieldSpec{Path: field.Name, Column: profileTopColumn(t, field)})
		}
	}
	assertProfileSpecsUnique(t, specs)
	return specs
}

func profileNestedFieldSpecs(t *testing.T, parent reflect.StructField) []profileFieldSpec {
	t.Helper()
	specs := make([]profileFieldSpec, 0, parent.Type.NumField())
	for index := 0; index < parent.Type.NumField(); index++ {
		field := parent.Type.Field(index)
		specs = append(specs, profileFieldSpec{
			Path:   parent.Name + "." + field.Name,
			Column: profileNestedColumn(t, parent.Name, field),
		})
	}
	return specs
}

func profileTopColumn(t *testing.T, field reflect.StructField) string {
	t.Helper()
	name := profileJSONFieldName(t, field)
	if field.Name == "Kind" || field.Name == "Version" {
		return "profile_" + name
	}
	return name
}

func profileNestedColumn(t *testing.T, parent string, field reflect.StructField) string {
	t.Helper()
	name := profileJSONFieldName(t, field)
	switch parent {
	case "Caller":
		if field.Name == "ExpiresAt" {
			return "caller_auth_expires_at"
		}
		return "caller_" + name
	case "Authorization":
		if field.Name == "GrantRequestDigest" || field.Name == "GrantIssuedAt" || field.Name == "GrantUpdatedAt" {
			return name
		}
		return "authorization_" + name
	case "Source", "Target":
		return profileSelectorColumn(strings.ToLower(parent), field.Name, name)
	default:
		t.Fatalf("unsupported nested profile owner %s", parent)
		return ""
	}
}

func profileSelectorColumn(prefix, fieldName, jsonName string) string {
	switch fieldName {
	case "Version", "Kind":
		return prefix + "_selector_" + jsonName
	case "State", "UpdatedAt":
		return prefix + "_binding_" + jsonName
	default:
		return prefix + "_" + jsonName
	}
}

func profileJSONFieldName(t *testing.T, field reflect.StructField) string {
	t.Helper()
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "" || name == "-" {
		t.Fatalf("%s has no enumerable JSON tag", field.Name)
	}
	return name
}

func assertProfileSpecsUnique(t *testing.T, specs []profileFieldSpec) {
	t.Helper()
	paths := make(map[string]bool, len(specs))
	columns := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if paths[spec.Path] || columns[spec.Column] {
			t.Fatalf("duplicate profile field mapping path=%s column=%s", spec.Path, spec.Column)
		}
		paths[spec.Path] = true
		columns[spec.Column] = true
	}
}

func profilePersistenceColumns(t *testing.T) map[string]bool {
	t.Helper()
	columns := map[string]bool{"command_id": true, "schema_version": true, "recorded_at": true}
	for _, spec := range profileFieldSpecs(t) {
		columns[spec.Column] = true
	}
	return columns
}

func profileDBFields(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		if name == "" || name == "-" || fields[name] {
			t.Fatalf("%s.%s has invalid or duplicate db tag %q", typ.Name(), field.Name, name)
		}
		fields[name] = true
	}
	return fields
}

func profileSQLiteColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("inspect SQLite table %s: %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan SQLite table %s: %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite table %s: %v", table, err)
	}
	return columns
}

func assertProfileExactSet(t *testing.T, label string, want, got map[string]bool) {
	t.Helper()
	missing, stale := make([]string, 0), make([]string, 0)
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if !want[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s field drift: missing=%v stale=%v", label, missing, stale)
	}
}

func assertProfileColumnsExcludeLifecycleAndPayload(t *testing.T, columns map[string]bool) {
	t.Helper()
	for column := range columns {
		lower := strings.ToLower(column)
		for _, forbidden := range []string{"accepted", "pending", "dispatch", "provider", "content", "resource"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("profile persistence field %q contains forbidden concept %q", column, forbidden)
			}
		}
	}
}

func profileWriteFieldSpecs(t *testing.T) map[string]string {
	t.Helper()
	constantPaths := map[string]string{
		"Version": "profile version SQL literal", "Authorization.RequestVersion": "authorization version SQL literal",
		"Source.Version": "source selector version SQL literal", "Source.BindingSchemaVersion": "source binding schema SQL literal",
		"Target.Version": "target selector version SQL literal", "Target.BindingSchemaVersion": "target binding schema SQL literal",
	}
	specs := profileFieldSpecs(t)
	allPaths := make(map[string]bool, len(specs))
	for _, spec := range specs {
		allPaths[spec.Path] = true
	}
	for path, reason := range constantPaths {
		if !allPaths[path] || strings.TrimSpace(reason) == "" {
			t.Fatalf("profile write exemption %s is stale or has no reason", path)
		}
	}
	fields := map[string]string{"command_id": "$command_id"}
	for _, spec := range specs {
		if _, constant := constantPaths[spec.Path]; constant {
			continue
		}
		fields[spec.Column] = spec.Path
	}
	return fields
}

func assertProfileWriteParamColumns(t *testing.T) {
	t.Helper()
	wantPaths := profileWriteFieldSpecs(t)
	want := make(map[string]bool, len(wantPaths))
	for column := range wantPaths {
		want[column] = true
	}
	got := profileDBFields(t, reflect.TypeFor[storedb.CreateOrchestrationCommandIntentProfileParams]())
	assertProfileExactSet(t, "SQLC profile write params", want, got)
}

func profileMappingSourcePath(t *testing.T) string {
	t.Helper()
	if fixture := os.Getenv("ORCH_PROFILE_MAPPING_FIXTURE"); fixture != "" {
		return fixture
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate profile field guard source")
	}
	return filepath.Join(filepath.Dir(currentFile), "profile_mapping.go")
}

func parseProfileMappingFunctions(t *testing.T, path string) map[string]*ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse profile mapper %s: %v", path, err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil {
			continue
		}
		functions[function.Name.Name] = function
	}
	return functions
}

func assertProfileWriteMapper(t *testing.T, functions map[string]*ast.FuncDecl) {
	t.Helper()
	function := requireProfileFunction(t, functions, "sendProfileParams")
	literal := requireProfileCompositeLiteral(t, function, "CreateOrchestrationCommandIntentProfileParams")
	paramColumns := profileDBTagByGoName(t, reflect.TypeFor[storedb.CreateOrchestrationCommandIntentProfileParams]())
	want := profileWriteFieldSpecs(t)
	got := make(map[string]string, len(literal.Elts))
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		key, keyed := pair.Key.(*ast.Ident)
		if !ok || !keyed {
			t.Fatal("sendProfileParams must use explicit keyed fields")
		}
		column, ok := paramColumns[key.Name]
		if !ok || got[column] != "" {
			t.Fatalf("sendProfileParams has unknown or duplicate key %s", key.Name)
		}
		got[column] = profileWriteExpressionPath(t, key.Name, pair.Value)
	}
	assertProfileMapperPairs(t, "sendProfileParams", want, got)
}

func profileWriteExpressionPath(t *testing.T, key string, expression ast.Expr) string {
	t.Helper()
	if key == "CommandID" {
		identifier, ok := expression.(*ast.Ident)
		if !ok || identifier.Name != "commandID" {
			t.Fatalf("sendProfileParams.CommandID is not sourced from commandID")
		}
		return "$command_id"
	}
	known := make(map[string]bool)
	for _, spec := range profileFieldSpecs(t) {
		known[spec.Path] = true
	}
	paths := make(map[string]bool)
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok {
			if path, rooted := profileSelectorPath(selector); rooted && known[path] {
				paths[path] = true
			}
		}
		return true
	})
	if len(paths) != 1 {
		t.Fatalf("sendProfileParams.%s source paths = %v, want one profile leaf", key, sortedProfileKeys(paths))
	}
	return sortedProfileKeys(paths)[0]
}

func profileSelectorPath(expression ast.Expr) (string, bool) {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	parts := []string{selector.Sel.Name}
	current := selector.X
	for {
		switch value := current.(type) {
		case *ast.SelectorExpr:
			parts = append([]string{value.Sel.Name}, parts...)
			current = value.X
		case *ast.Ident:
			return strings.Join(parts, "."), value.Name == "profile"
		default:
			return "", false
		}
	}
}

func assertProfileReadMapper(t *testing.T, functions map[string]*ast.FuncDecl) {
	t.Helper()
	cases := []struct {
		function, typeName, prefix string
		typ                        reflect.Type
	}{
		{"sendProfileFromRow", "SendMessageProfile", "", reflect.TypeFor[SendMessageProfile]()},
		{"sendProfileFromRow", "CallerSnapshot", "Caller", reflect.TypeFor[CallerSnapshot]()},
		{"authorizationFromRow", "AuthorizationDecision", "Authorization", reflect.TypeFor[AuthorizationDecision]()},
		{"selectorFromSourceRow", "ActiveBindingSelector", "Source", reflect.TypeFor[ActiveBindingSelector]()},
		{"selectorFromTargetRow", "ActiveBindingSelector", "Target", reflect.TypeFor[ActiveBindingSelector]()},
	}
	for _, item := range cases {
		function := requireProfileFunction(t, functions, item.function)
		literal := requireProfileCompositeLiteral(t, function, item.typeName)
		assertProfileLiteralFields(t, item.function, item.prefix, item.typ, literal)
	}
	assertProfileReadRowFields(t, functions)
}

func assertProfileLiteralFields(t *testing.T, function, prefix string, typ reflect.Type, literal *ast.CompositeLit) {
	t.Helper()
	want := make(map[string]bool, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		want[typ.Field(index).Name] = true
	}
	got := make(map[string]bool, len(literal.Elts))
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		key, keyed := pair.Key.(*ast.Ident)
		if !ok || !keyed || got[key.Name] {
			t.Fatalf("%s %s literal has invalid or duplicate field", function, typ.Name())
		}
		got[key.Name] = true
	}
	missing, stale := profilePathDifference(prefix, want, got)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s mapper field drift: missing=%v stale=%v", function, missing, stale)
	}
}

func assertProfileReadRowFields(t *testing.T, functions map[string]*ast.FuncDecl) {
	t.Helper()
	rowColumns := profileDBTagByGoName(t, reflect.TypeFor[storedb.OrchestrationCommandIntentProfile]())
	got := make(map[string]bool)
	for _, function := range functions {
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			root, rooted := selector.X.(*ast.Ident)
			if rooted && root.Name == "row" {
				column, exists := rowColumns[selector.Sel.Name]
				if !exists {
					t.Fatalf("profile read mapper references unknown row field %s", selector.Sel.Name)
				}
				got[column] = true
			}
			return true
		})
	}
	want := map[string]bool{"recorded_at": true}
	for _, spec := range profileFieldSpecs(t) {
		want[spec.Column] = true
	}
	assertProfileExactSet(t, "profile read row references", want, got)
}

func requireProfileFunction(t *testing.T, functions map[string]*ast.FuncDecl, name string) *ast.FuncDecl {
	t.Helper()
	function, ok := functions[name]
	if !ok || function.Body == nil {
		t.Fatalf("profile mapper function %s is missing", name)
	}
	return function
}

func requireProfileCompositeLiteral(t *testing.T, function *ast.FuncDecl, typeName string) *ast.CompositeLit {
	t.Helper()
	var matches []*ast.CompositeLit
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if ok && len(literal.Elts) != 0 && profileASTTypeName(literal.Type) == typeName {
			matches = append(matches, literal)
		}
		return true
	})
	if len(matches) != 1 {
		t.Fatalf("%s has %d keyed %s literals, want one", function.Name.Name, len(matches), typeName)
	}
	return matches[0]
}

func profileASTTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func profileDBTagByGoName(t *testing.T, typ reflect.Type) map[string]string {
	t.Helper()
	fields := make(map[string]string, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		if name == "" || name == "-" || fields[field.Name] != "" {
			t.Fatalf("%s.%s has invalid db tag %q", typ.Name(), field.Name, name)
		}
		fields[field.Name] = name
	}
	return fields
}

func assertProfileMapperPairs(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	missing, stale := make([]string, 0), make([]string, 0)
	for column, path := range want {
		if got[column] == "" {
			missing = append(missing, path)
		} else if got[column] != path {
			t.Fatalf("%s column %s source = %s, want %s", label, column, got[column], path)
		}
	}
	for column, path := range got {
		if want[column] == "" {
			stale = append(stale, path)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s mapper field drift: missing=%v stale=%v", label, missing, stale)
	}
}

func profilePathDifference(prefix string, want, got map[string]bool) ([]string, []string) {
	missing, stale := make([]string, 0), make([]string, 0)
	path := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}
	for name := range want {
		if !got[name] {
			missing = append(missing, path(name))
		}
	}
	for name := range got {
		if !want[name] {
			stale = append(stale, path(name))
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}

func sortedProfileKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func assertProfileRuntimeRoundTrip(t *testing.T) {
	t.Helper()
	profile := reflectedProfileFixture(t)
	params := sendProfileParams("profile-field-guard-command", profile)
	assertProfileWriteValues(t, profile, params)
	row := reflectedProfileRow(t, profile)
	got, recordedAt, err := sendProfileFromRow(row)
	if err != nil {
		t.Fatalf("sendProfileFromRow() error = %v", err)
	}
	if !reflect.DeepEqual(got, profile) {
		t.Fatalf("sendProfileFromRow() = %#v, want %#v", got, profile)
	}
	wantRecordedAt, err := time.Parse(time.RFC3339Nano, row.RecordedAt)
	if err != nil || !recordedAt.Equal(wantRecordedAt) {
		t.Fatalf("recorded_at = %s, want %s (parse error %v)", recordedAt, wantRecordedAt, err)
	}
}

func reflectedProfileFixture(t *testing.T) SendMessageProfile {
	t.Helper()
	value := reflect.New(reflect.TypeFor[SendMessageProfile]()).Elem()
	ordinal := 0
	fillProfileStruct(t, value, "", &ordinal)
	return value.Interface().(SendMessageProfile)
}

func fillProfileStruct(t *testing.T, value reflect.Value, prefix string, ordinal *int) {
	t.Helper()
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		path := field.Name
		if prefix != "" {
			path = prefix + "." + field.Name
		}
		current := value.Field(index)
		if current.Kind() == reflect.Struct && current.Type() != reflect.TypeFor[time.Time]() {
			fillProfileStruct(t, current, path, ordinal)
			continue
		}
		*ordinal++
		setProfileLeaf(t, current, path, *ordinal)
	}
}

func setProfileLeaf(t *testing.T, value reflect.Value, path string, ordinal int) {
	t.Helper()
	switch {
	case value.Kind() == reflect.String:
		value.SetString("profile-field-" + strings.ReplaceAll(path, ".", "-"))
	case value.Kind() == reflect.Int64:
		value.SetInt(int64(ordinal + 10))
	case value.Type() == reflect.TypeFor[time.Time]():
		value.Set(reflect.ValueOf(time.Date(2026, time.September, 1, 0, ordinal, 0, ordinal, time.UTC)))
	default:
		t.Fatalf("unsupported profile fixture field %s type %s", path, value.Type())
	}
}

func assertProfileWriteValues(t *testing.T, profile SendMessageProfile, params storedb.CreateOrchestrationCommandIntentProfileParams) {
	t.Helper()
	paramsValue := reflect.ValueOf(params)
	paramsByColumn := profileValueFieldsByDBTag(t, paramsValue)
	for _, spec := range profileFieldSpecs(t) {
		paramValue, writable := paramsByColumn[spec.Column]
		if !writable {
			continue
		}
		assertProfileStoredValue(t, spec.Path, profilePathValue(t, profile, spec.Path), paramValue)
	}
	command := paramsByColumn["command_id"]
	if !command.IsValid() || command.String() != "profile-field-guard-command" {
		t.Fatalf("write command_id = %v, want fixture command", command)
	}
}

func reflectedProfileRow(t *testing.T, profile SendMessageProfile) storedb.OrchestrationCommandIntentProfile {
	t.Helper()
	row := reflect.New(reflect.TypeFor[storedb.OrchestrationCommandIntentProfile]()).Elem()
	fields := profileValueFieldsByDBTag(t, row)
	for _, spec := range profileFieldSpecs(t) {
		setProfileStoredValue(t, spec.Path, fields[spec.Column], profilePathValue(t, profile, spec.Path))
	}
	fields["command_id"].SetString("profile-field-guard-command")
	fields["schema_version"].SetInt(1)
	fields["recorded_at"].SetString(time.Date(2026, time.September, 1, 23, 59, 0, 1, time.UTC).Format(time.RFC3339Nano))
	return row.Interface().(storedb.OrchestrationCommandIntentProfile)
}

func profileValueFieldsByDBTag(t *testing.T, value reflect.Value) map[string]reflect.Value {
	t.Helper()
	fields := make(map[string]reflect.Value, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		if name == "" || name == "-" || fields[name].IsValid() {
			t.Fatalf("%s.%s has invalid or duplicate db tag %q", value.Type().Name(), field.Name, name)
		}
		fields[name] = value.Field(index)
	}
	return fields
}

func profilePathValue(t *testing.T, profile SendMessageProfile, path string) reflect.Value {
	t.Helper()
	value := reflect.ValueOf(profile)
	for _, name := range strings.Split(path, ".") {
		value = value.FieldByName(name)
		if !value.IsValid() {
			t.Fatalf("profile path %s is invalid", path)
		}
	}
	return value
}

func setProfileStoredValue(t *testing.T, path string, stored, source reflect.Value) {
	t.Helper()
	if !stored.IsValid() {
		t.Fatalf("profile row is missing %s", path)
	}
	if source.Type() == reflect.TypeFor[time.Time]() && stored.Kind() == reflect.String {
		stored.SetString(profileTime(source.Interface().(time.Time)))
		return
	}
	if stored.Type() != source.Type() {
		t.Fatalf("profile row %s type = %s, want %s", path, stored.Type(), source.Type())
	}
	stored.Set(source)
}

func assertProfileStoredValue(t *testing.T, path string, source, stored reflect.Value) {
	t.Helper()
	if source.Type() == reflect.TypeFor[time.Time]() && stored.Kind() == reflect.String {
		if stored.String() != profileTime(source.Interface().(time.Time)) {
			t.Fatalf("profile write %s = %q, want %q", path, stored.String(), profileTime(source.Interface().(time.Time)))
		}
		return
	}
	if stored.Type() != source.Type() || !reflect.DeepEqual(stored.Interface(), source.Interface()) {
		t.Fatalf("profile write %s = %v, want %v", path, stored.Interface(), source.Interface())
	}
}
