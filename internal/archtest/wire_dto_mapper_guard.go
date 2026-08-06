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

// AssertWireDTOMapperConsumesProducerFields 逐字段驱动真实 mapper，并验证 mapped/exempt 完备且互斥。
func AssertWireDTOMapperConsumesProducerFields[T any](
	t wireDTOMapperTestingT,
	mapper func(T) map[string]any,
	exemptionList []WireDTOMapperExemption,
) {
	var zero T
	AssertWireDTOMapperConsumesProducerFieldsFrom(t, zero, mapper, exemptionList)
}

// AssertWireDTOMapperConsumesProducerFieldsFrom 从一份有效基线逐字段注入哨兵，适用于 fail-fast mapper。
func AssertWireDTOMapperConsumesProducerFieldsFrom[T any](
	t wireDTOMapperTestingT,
	baselineValue T,
	mapper func(T) map[string]any,
	exemptionList []WireDTOMapperExemption,
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

	baseline := mapper(baselineValue)
	seenExemptions := make(map[string]bool, len(exemptions))
	for _, descriptor := range fields {
		value := reflect.New(reflect.TypeFor[T]()).Elem()
		value.Set(reflect.ValueOf(baselineValue))
		field := value.FieldByIndex(descriptor.index)
		if err := setWireDTOMapperFieldSentinel(field); err != nil {
			t.Fatalf("set producer field %s: %v", descriptor.goName, err)
		}
		got := mapper(value.Interface().(T))
		if _, exempt := exemptions[descriptor.jsonName]; exempt {
			seenExemptions[descriptor.jsonName] = true
			if !reflect.DeepEqual(got, baseline) {
				t.Fatalf(
					"producer JSON field %q (%s) is both mapped and exempt",
					descriptor.jsonName,
					descriptor.goName,
				)
			}
			continue
		}
		if reflect.DeepEqual(got, baseline) {
			t.Fatalf(
				"producer JSON field %q (%s) does not affect the real mapper output",
				descriptor.jsonName,
				descriptor.goName,
			)
		}
	}
	for field := range exemptions {
		if !seenExemptions[field] {
			t.Fatalf("mapper exemption %q is stale or not a producer JSON field", field)
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
	for i := 0; i < field.NumField(); i++ {
		if err := setWireDTOMapperFieldSentinel(field.Field(i)); err != nil {
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
