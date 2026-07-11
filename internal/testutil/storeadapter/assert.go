package storeadaptertest

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// AssertFieldsMap 逐字段构造 one-hot 源值，并校验无错误 mapper 的同名同值映射。
func AssertFieldsMap[Source, Target any](t *testing.T, mapField func(Source) Target) {
	t.Helper()
	assertFieldsMap(t, func(source Source) (Target, error) {
		return mapField(source), nil
	})
}

// AssertFieldsMapE 逐字段构造 one-hot 源值，并校验可返回错误的 mapper 的同名同值映射。
func AssertFieldsMapE[Source, Target any](t *testing.T, mapField func(Source) (Target, error)) {
	t.Helper()
	assertFieldsMap(t, mapField)
}

// assertFieldsMap 共享字段集合与 one-hot 反射校验引擎。
func assertFieldsMap[Source, Target any](t *testing.T, mapField func(Source) (Target, error)) {
	t.Helper()
	sourceType := reflect.TypeFor[Source]()
	targetType := reflect.TypeFor[Target]()
	assertFieldSets(t, sourceType, targetType)
	for index := range sourceType.NumField() {
		sourceField := sourceType.Field(index)
		if !sourceField.IsExported() {
			continue
		}
		assertFieldMaps(t, sourceType, targetType, sourceField, index, mapField)
	}
}

// assertFieldSets 要求 DTO 两侧的导出字段集合和类型一致。
func assertFieldSets(t *testing.T, sourceType, targetType reflect.Type) {
	t.Helper()
	for index := range sourceType.NumField() {
		sourceField := sourceType.Field(index)
		if !sourceField.IsExported() {
			continue
		}
		targetField, ok := targetType.FieldByName(sourceField.Name)
		if !ok || targetField.Type != sourceField.Type {
			t.Fatalf("target %s lacks compatible exported field %s (%s)", targetType, sourceField.Name, sourceField.Type)
		}
	}
	for index := range targetType.NumField() {
		targetField := targetType.Field(index)
		if targetField.IsExported() {
			if _, ok := sourceType.FieldByName(targetField.Name); !ok {
				t.Fatalf("source %s lacks exported field %s", sourceType, targetField.Name)
			}
		}
	}
}

// assertFieldMaps 校验一个字段的 one-hot 映射。
func assertFieldMaps[Source, Target any](
	t *testing.T,
	sourceType reflect.Type,
	targetType reflect.Type,
	sourceField reflect.StructField,
	index int,
	mapField func(Source) (Target, error),
) {
	t.Helper()
	source := reflect.New(sourceType).Elem()
	sample, err := sampleValue(sourceField.Type, sourceField.Name)
	if err != nil {
		t.Fatalf("sample field %s: %v", sourceField.Name, err)
	}
	source.Field(index).Set(sample)
	got, err := mapField(source.Interface().(Source))
	if err != nil {
		t.Fatalf("map field %s: %v", sourceField.Name, err)
	}
	expected := reflect.New(targetType).Elem()
	expected.FieldByName(sourceField.Name).Set(sample)
	if !reflect.DeepEqual(got, expected.Interface().(Target)) {
		t.Fatalf("field %s mapping mismatch: got %#v want %#v", sourceField.Name, got, expected.Interface())
	}
}

// sampleValue 为 Store adapter DTO 字段递归构造稳定非零测试值。
func sampleValue(fieldType reflect.Type, fieldName string) (reflect.Value, error) {
	if fieldType == reflect.TypeFor[time.Time]() {
		return reflect.ValueOf(time.Date(2026, time.July, 10, 1, 2, 3, 0, time.UTC)), nil
	}
	switch fieldType.Kind() {
	case reflect.Pointer:
		return samplePointerValue(fieldType, fieldName)
	case reflect.Slice:
		return sampleSliceValue(fieldType, fieldName)
	case reflect.Map:
		return sampleMapValue(fieldType, fieldName)
	case reflect.Struct:
		return sampleStructValue(fieldType, fieldName)
	default:
		return sampleScalarValue(fieldType, fieldName)
	}
}

// samplePointerValue 为指针字段构造非 nil 测试值。
func samplePointerValue(fieldType reflect.Type, fieldName string) (reflect.Value, error) {
	sample, err := sampleValue(fieldType.Elem(), fieldName)
	if err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(fieldType.Elem())
	value.Elem().Set(sample)
	return value, nil
}

// sampleSliceValue 为切片字段构造两个稳定元素。
func sampleSliceValue(fieldType reflect.Type, fieldName string) (reflect.Value, error) {
	first, err := sampleValue(fieldType.Elem(), fieldName)
	if err != nil {
		return reflect.Value{}, err
	}
	second, err := sampleValue(fieldType.Elem(), fieldName+"Second")
	if err != nil {
		return reflect.Value{}, err
	}
	value := reflect.MakeSlice(fieldType, 2, 2)
	value.Index(0).Set(first)
	value.Index(1).Set(second)
	return value, nil
}

// sampleMapValue 为 map 字段构造一个稳定键值对。
func sampleMapValue(fieldType reflect.Type, fieldName string) (reflect.Value, error) {
	key, err := sampleValue(fieldType.Key(), fieldName+"Key")
	if err != nil {
		return reflect.Value{}, err
	}
	element, err := sampleValue(fieldType.Elem(), fieldName+"Value")
	if err != nil {
		return reflect.Value{}, err
	}
	value := reflect.MakeMapWithSize(fieldType, 1)
	value.SetMapIndex(key, element)
	return value, nil
}

// sampleScalarValue 为常见标量类型构造稳定非零测试值。
func sampleScalarValue(fieldType reflect.Type, fieldName string) (reflect.Value, error) {
	switch fieldType.Kind() {
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(fieldType), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(41)).Convert(fieldType), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflect.ValueOf(uint64(41)).Convert(fieldType), nil
	case reflect.Float32, reflect.Float64:
		return reflect.ValueOf(4.1).Convert(fieldType), nil
	case reflect.Complex64, reflect.Complex128:
		return reflect.ValueOf(complex(4.1, 2.3)).Convert(fieldType), nil
	case reflect.String:
		return reflect.ValueOf("value-" + fieldName).Convert(fieldType), nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported store adapter sample type %s (kind %s)", fieldType, fieldType.Kind())
	}
}

// sampleStructValue 只写入普通结构体的导出字段，避免触碰未导出实现细节。
func sampleStructValue(fieldType reflect.Type, fieldName string) (reflect.Value, error) {
	value := reflect.New(fieldType).Elem()
	for index := range fieldType.NumField() {
		field := fieldType.Field(index)
		if field.IsExported() {
			sample, err := sampleValue(field.Type, fieldName+field.Name)
			if err != nil {
				return reflect.Value{}, err
			}
			value.Field(index).Set(sample)
		}
	}
	return value, nil
}
