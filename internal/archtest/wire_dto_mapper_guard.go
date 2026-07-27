package archtest

import (
	"fmt"
	"reflect"
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

type wireDTOMapperJSONField struct {
	jsonName string
	goName   string
	index    []int
}

// AssertWireDTOMapperConsumesProducerFields 逐字段驱动真实 mapper，并验证 mapped/exempt 完备且互斥。
func AssertWireDTOMapperConsumesProducerFields[T any](
	t wireDTOMapperTestingT,
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

	var zero T
	baseline := mapper(zero)
	seenExemptions := make(map[string]bool, len(exemptions))
	for _, descriptor := range fields {
		value := reflect.New(reflect.TypeFor[T]()).Elem()
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

// wireDTOMapperJSONFields 展开 producer 的 JSON 字段并拒绝空或非结构体输入。
func wireDTOMapperJSONFields(typ reflect.Type) ([]wireDTOMapperJSONField, error) {
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("mapper producer %v must be a struct", typ)
	}
	var fields []wireDTOMapperJSONField
	if err := collectWireDTOMapperJSONFields(typ, nil, &fields); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("mapper producer %s has zero JSON fields", typ)
	}
	return fields, nil
}

// collectWireDTOMapperJSONFields 递归收集嵌入结构及普通 tagged 字段。
func collectWireDTOMapperJSONFields(typ reflect.Type, prefix []int, fields *[]wireDTOMapperJSONField) error {
	seen := make(map[string]string, len(*fields))
	for _, field := range *fields {
		seen[field.jsonName] = field.goName
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		index := append(append([]int(nil), prefix...), i)
		if field.Anonymous && field.Tag.Get("json") == "" {
			if err := collectEmbeddedWireDTOMapperJSONFields(field, index, fields, seen); err != nil {
				return err
			}
			continue
		}
		if err := appendWireDTOMapperJSONField(field, index, fields, seen); err != nil {
			return err
		}
	}
	return nil
}

// collectEmbeddedWireDTOMapperJSONFields 收集单个嵌入字段并校验其新增 JSON 名称。
func collectEmbeddedWireDTOMapperJSONFields(
	field reflect.StructField,
	index []int,
	fields *[]wireDTOMapperJSONField,
	seen map[string]string,
) error {
	embedded := field.Type
	if embedded.Kind() == reflect.Pointer {
		embedded = embedded.Elem()
	}
	if embedded.Kind() != reflect.Struct {
		return fmt.Errorf("embedded mapper producer field %s must be a struct", field.Name)
	}
	start := len(*fields)
	if err := collectWireDTOMapperJSONFields(embedded, index, fields); err != nil {
		return err
	}
	for _, nested := range (*fields)[start:] {
		if previous, exists := seen[nested.jsonName]; exists {
			return fmt.Errorf("producer field %s duplicates JSON field %q from %s", nested.goName, nested.jsonName, previous)
		}
		seen[nested.jsonName] = nested.goName
	}
	return nil
}

// appendWireDTOMapperJSONField 登记单个 tagged 字段并拒绝重复 JSON 名称。
func appendWireDTOMapperJSONField(
	field reflect.StructField,
	index []int,
	fields *[]wireDTOMapperJSONField,
	seen map[string]string,
) error {
	jsonName, ok := wireDTOMapperJSONFieldName(field)
	if !ok {
		return nil
	}
	if previous, exists := seen[jsonName]; exists {
		return fmt.Errorf("producer field %s duplicates JSON field %q from %s", field.Name, jsonName, previous)
	}
	seen[jsonName] = field.Name
	*fields = append(*fields, wireDTOMapperJSONField{jsonName: jsonName, goName: field.Name, index: index})
	return nil
}

// wireDTOMapperJSONFieldName 解析结构字段的有效 JSON tag 名称。
func wireDTOMapperJSONFieldName(field reflect.StructField) (string, bool) {
	tag := strings.TrimSpace(field.Tag.Get("json"))
	if tag == "" || tag == "-" {
		return "", false
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	return tag, tag != "" && tag != "-"
}

// setWireDTOMapperFieldSentinel 写入能触发 mapper 分支的类型匹配哨兵值。
func setWireDTOMapperFieldSentinel(field reflect.Value) error {
	if !field.CanSet() {
		return fmt.Errorf("field is not settable")
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString("wire-mapper-sentinel")
	case reflect.Bool:
		field.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(37)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(37)
	case reflect.Float32, reflect.Float64:
		field.SetFloat(37.5)
	case reflect.Struct:
		if field.Type() != reflect.TypeFor[time.Time]() {
			return fmt.Errorf("unsupported mapper producer field kind %s", field.Kind())
		}
		field.Set(reflect.ValueOf(time.Unix(37, 0).UTC()))
	default:
		return fmt.Errorf("unsupported mapper producer field kind %s", field.Kind())
	}
	return nil
}
