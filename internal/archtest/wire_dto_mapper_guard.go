package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
)

// WireDTOMapperExemption 描述 producer 字段不进入指定消费方向的显式证据。
type WireDTOMapperExemption struct {
	Field     string
	Direction string
	Reason    string
	Evidence  string
	Owner     string
}

// WireDTOMapperProjection 将 producer JSON 字段绑定到一个精确 consumer 输出键。
// Transform 为 nil 时，consumer 值必须等于写入 producer 的哨兵值。
type WireDTOMapperProjection struct {
	Field          string
	ConsumerKey    string
	Transform      func(input any, sentinel any) any
	ExpectedOutput func(input any) map[string]any
}

type wireDTOMapperTestingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AssertWireDTOMapperASTReferencesProducerFields 从真实函数 AST 派生字段引用并与 producer 动态差集。
func AssertWireDTOMapperASTReferencesProducerFields[T any](
	t wireDTOMapperTestingT,
	sourcePath string,
	functionName string,
	parameterName string,
) {
	t.Helper()
	fields, err := wireDTOMapperJSONFields(reflect.TypeFor[T]())
	if err != nil {
		t.Fatalf("%v", err)
	}
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read mapper source %s: %v", sourcePath, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, body, 0)
	if err != nil {
		t.Fatalf("parse mapper source %s: %v", sourcePath, err)
	}
	references := mapperParameterSelectorReferences(file, functionName, parameterName)
	var missing []string
	for _, field := range fields {
		if !references[field.goName] {
			missing = append(missing, field.jsonName)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("%s AST does not reference producer JSON fields: %v", functionName, missing)
	}
}

// mapperParameterSelectorReferences 收集目标函数中指定参数的直接字段选择表达式。
func mapperParameterSelectorReferences(file *ast.File, functionName, parameterName string) map[string]bool {
	references := make(map[string]bool)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == parameterName {
				references[selector.Sel.Name] = true
			}
			return true
		})
	}
	return references
}

// AssertWireDTOMapperConsumesProducerFields 逐字段驱动真实 mapper，并验证精确 projection/exempt 完备且互斥。
func AssertWireDTOMapperConsumesProducerFields[T any](
	t wireDTOMapperTestingT,
	mapper func(T) map[string]any,
	exemptionList []WireDTOMapperExemption,
	projectionList []WireDTOMapperProjection,
) {
	var zero T
	AssertWireDTOMapperConsumesProducerFieldsFrom(t, zero, mapper, exemptionList, projectionList)
}

// AssertWireDTOMapperConsumesProducerFieldsFrom 从一份有效基线逐字段注入哨兵并断言精确 output delta。
func AssertWireDTOMapperConsumesProducerFieldsFrom[T any](
	t wireDTOMapperTestingT,
	baselineValue T,
	mapper func(T) map[string]any,
	exemptionList []WireDTOMapperExemption,
	projectionList []WireDTOMapperProjection,
) {
	t.Helper()
	fields, err := wireDTOMapperJSONFields(reflect.TypeFor[T]())
	if err != nil {
		t.Fatalf("%v", err)
	}
	exemptions, err := wireDTOMapperExemptionRegistry(exemptionList)
	if err != nil {
		t.Fatalf("%v", err)
	}
	projections, err := wireDTOMapperProjectionRegistry(projectionList)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := validateWireDTOMapperCoverage(fields, exemptions, projections); err != nil {
		t.Fatalf("%v", err)
	}

	baseline := mapper(baselineValue)
	for _, descriptor := range fields {
		value := reflect.New(reflect.TypeFor[T]()).Elem()
		value.Set(reflect.ValueOf(baselineValue))
		field := value.FieldByIndex(descriptor.index)
		if err := setWireDTOMapperFieldSentinel(field); err != nil {
			t.Fatalf("set producer field %s: %v", descriptor.goName, err)
		}
		got := mapper(value.Interface().(T))
		if _, exempt := exemptions[descriptor.jsonName]; exempt {
			if !reflect.DeepEqual(got, baseline) {
				t.Fatalf(
					"producer JSON field %q (%s) is both mapped and exempt",
					descriptor.jsonName,
					descriptor.goName,
				)
			}
			continue
		}
		if err := assertWireDTOMapperExactDelta(
			baseline,
			got,
			projections[descriptor.jsonName],
			value.Interface(),
			field.Interface(),
		); err != nil {
			t.Fatalf("producer JSON field %q (%s): %v", descriptor.jsonName, descriptor.goName, err)
		}
	}
}

// wireDTOMapperExemptionRegistry 校验豁免方向、原因、证据、owner 与字段唯一性。
func wireDTOMapperExemptionRegistry(exemptions []WireDTOMapperExemption) (map[string]WireDTOMapperExemption, error) {
	registry := make(map[string]WireDTOMapperExemption, len(exemptions))
	for _, exemption := range exemptions {
		fields := map[string]string{
			"field":     exemption.Field,
			"direction": exemption.Direction,
			"reason":    exemption.Reason,
			"evidence":  exemption.Evidence,
			"owner":     exemption.Owner,
		}
		for name, value := range fields {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("mapper exemption %q has empty %s", exemption.Field, name)
			}
		}
		if _, exists := registry[exemption.Field]; exists {
			return nil, fmt.Errorf("mapper exemption field %q is duplicate", exemption.Field)
		}
		registry[exemption.Field] = exemption
	}
	return registry, nil
}

// wireDTOMapperProjectionRegistry 校验每个 producer/consumer 对唯一且完整。
func wireDTOMapperProjectionRegistry(projections []WireDTOMapperProjection) (map[string]map[string]WireDTOMapperProjection, error) {
	registry := make(map[string]map[string]WireDTOMapperProjection)
	for _, projection := range projections {
		if strings.TrimSpace(projection.Field) == "" {
			return nil, fmt.Errorf("mapper projection has empty field")
		}
		if strings.TrimSpace(projection.ConsumerKey) == "" {
			return nil, fmt.Errorf("mapper projection %q has empty consumer key", projection.Field)
		}
		byConsumer := registry[projection.Field]
		if byConsumer == nil {
			byConsumer = make(map[string]WireDTOMapperProjection)
			registry[projection.Field] = byConsumer
		}
		if _, exists := byConsumer[projection.ConsumerKey]; exists {
			return nil, fmt.Errorf("mapper projection %q -> %q is duplicate", projection.Field, projection.ConsumerKey)
		}
		if projection.ExpectedOutput != nil && len(byConsumer) != 0 {
			return nil, fmt.Errorf("mapper projection %q with expected output must be its only consumer registration", projection.Field)
		}
		for _, existing := range byConsumer {
			if existing.ExpectedOutput != nil {
				return nil, fmt.Errorf("mapper projection %q with expected output must be its only consumer registration", projection.Field)
			}
		}
		byConsumer[projection.ConsumerKey] = projection
	}
	return registry, nil
}

// validateWireDTOMapperCoverage 计算 producer 与 projection/exemption 的 missing、stale 差集。
func validateWireDTOMapperCoverage(
	fields []wireDTOJSONField,
	exemptions map[string]WireDTOMapperExemption,
	projections map[string]map[string]WireDTOMapperProjection,
) error {
	producer := make(map[string]bool, len(fields))
	var missing []string
	for _, field := range fields {
		producer[field.jsonName] = true
		_, exempt := exemptions[field.jsonName]
		_, projected := projections[field.jsonName]
		if exempt && projected {
			return fmt.Errorf("producer JSON field %q is both projected and exempt", field.jsonName)
		}
		if !exempt && !projected {
			missing = append(missing, field.jsonName)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("producer JSON fields missing from mapper projection registry: %v", missing)
	}
	var stale []string
	for field := range exemptions {
		if !producer[field] {
			stale = append(stale, "exemption:"+field)
		}
	}
	for field := range projections {
		if !producer[field] {
			stale = append(stale, "projection:"+field)
		}
	}
	if len(stale) != 0 {
		sort.Strings(stale)
		return fmt.Errorf("mapper registry references fields that no longer exist: %v", stale)
	}
	return nil
}

// assertWireDTOMapperExactDelta 拒绝错误键、错误值、漏项和任何未登记的额外 delta。
func assertWireDTOMapperExactDelta(
	baseline map[string]any,
	got map[string]any,
	projections map[string]WireDTOMapperProjection,
	input any,
	sentinel any,
) error {
	delta := wireDTOMapperDelta(baseline, got)
	for _, projection := range projections {
		if projection.ExpectedOutput != nil {
			wantDelta := wireDTOMapperDelta(baseline, projection.ExpectedOutput(input))
			if !reflect.DeepEqual(delta, wantDelta) {
				return fmt.Errorf("output delta = %#v, want exact transformed delta %#v", delta, wantDelta)
			}
			return nil
		}
	}
	if len(delta) != len(projections) {
		return fmt.Errorf("output delta keys = %v, want exactly registered keys %v", sortedWireDTOMapperKeys(delta), sortedWireDTOMapperProjectionKeys(projections))
	}
	for key, projection := range projections {
		actual, exists := delta[key]
		if !exists {
			return fmt.Errorf("output delta missing consumer key %q", key)
		}
		want := sentinel
		if projection.Transform != nil {
			want = projection.Transform(input, sentinel)
		}
		if !reflect.DeepEqual(actual, want) {
			return fmt.Errorf("consumer key %q value = %#v, want %#v", key, actual, want)
		}
	}
	return nil
}

func wireDTOMapperDelta(baseline, got map[string]any) map[string]any {
	delta := make(map[string]any)
	for key, value := range got {
		baselineValue, exists := baseline[key]
		if !exists || !reflect.DeepEqual(value, baselineValue) {
			delta[key] = value
		}
	}
	for key := range baseline {
		if _, exists := got[key]; !exists {
			delta[key] = nil
		}
	}
	return delta
}

func sortedWireDTOMapperKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedWireDTOMapperProjectionKeys(values map[string]WireDTOMapperProjection) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// wireDTOMapperJSONFields 保留 mapper guard 的调用面，并委托给共享 producer descriptor collector。
func wireDTOMapperJSONFields(typ reflect.Type) ([]wireDTOJSONField, error) {
	return wireDTOJSONFields(typ)
}

// setWireDTOMapperFieldSentinel 写入能触发 mapper 分支的类型匹配哨兵值。
func setWireDTOMapperFieldSentinel(field reflect.Value) error {
	if !field.CanSet() {
		return fmt.Errorf("field is not settable")
	}
	if field.Kind() == reflect.Pointer {
		return setWireDTOMapperPointerSentinel(field)
	}
	if field.Kind() == reflect.Slice {
		return setWireDTOMapperSliceSentinel(field)
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString("wire-mapper-sentinel")
	case reflect.Bool:
		field.SetBool(!field.Bool())
	case reflect.Struct:
		return setWireDTOMapperStructSentinel(field)
	default:
		return setWireDTOMapperNumericSentinel(field)
	}
	return nil
}

func setWireDTOMapperStructSentinel(field reflect.Value) error {
	if field.Type() == reflect.TypeFor[time.Time]() {
		field.Set(reflect.ValueOf(time.Unix(37, 0).UTC()))
		return nil
	}
	for _, nested := range field.Fields() {
		if err := setWireDTOMapperFieldSentinel(nested); err != nil {
			return err
		}
	}
	return nil
}

func setWireDTOMapperPointerSentinel(field reflect.Value) error {
	value := reflect.New(field.Type().Elem())
	if err := setWireDTOMapperFieldSentinel(value.Elem()); err != nil {
		return err
	}
	field.Set(value)
	return nil
}

func setWireDTOMapperSliceSentinel(field reflect.Value) error {
	value := reflect.New(field.Type().Elem()).Elem()
	if err := setWireDTOMapperFieldSentinel(value); err != nil {
		return err
	}
	field.Set(reflect.Append(reflect.MakeSlice(field.Type(), 0, 1), value))
	return nil
}

func setWireDTOMapperNumericSentinel(field reflect.Value) error {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(37)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(37)
	case reflect.Float32, reflect.Float64:
		field.SetFloat(37.5)
	default:
		return fmt.Errorf("unsupported mapper producer field kind %s", field.Kind())
	}
	return nil
}
