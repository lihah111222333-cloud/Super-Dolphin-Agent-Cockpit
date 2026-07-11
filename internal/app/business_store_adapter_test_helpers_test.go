package app

import (
	"reflect"
	"testing"
	"time"
)

// assertBusinessStoreAdapterFieldsMap 逐字段构造 one-hot 源值，并要求目标仅出现同名同值字段。
func assertBusinessStoreAdapterFieldsMap[Source, Target any](t *testing.T, mapField func(Source) (Target, error)) {
	t.Helper()
	sourceType := reflect.TypeFor[Source]()
	targetType := reflect.TypeFor[Target]()
	assertBusinessStoreAdapterFieldSets(t, sourceType, targetType)
	for index := range sourceType.NumField() {
		sourceField := sourceType.Field(index)
		if !sourceField.IsExported() {
			continue
		}
		assertBusinessStoreAdapterFieldMaps(t, sourceType, targetType, sourceField, index, mapField)
	}
}

// assertBusinessStoreAdapterFieldSets 要求 DTO 两侧的导出字段集合和类型一致。
func assertBusinessStoreAdapterFieldSets(t *testing.T, sourceType, targetType reflect.Type) {
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

// assertBusinessStoreAdapterFieldMaps 校验一个字段的 one-hot 映射。
func assertBusinessStoreAdapterFieldMaps[Source, Target any](
	t *testing.T,
	sourceType reflect.Type,
	targetType reflect.Type,
	sourceField reflect.StructField,
	index int,
	mapField func(Source) (Target, error),
) {
	t.Helper()
	source := reflect.New(sourceType).Elem()
	sample := businessStoreAdapterSampleValue(sourceField.Type, sourceField.Name)
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

// businessStoreAdapterSampleValue 为 Store adapter DTO 的常用字段类型构造稳定非零测试值。
func businessStoreAdapterSampleValue(fieldType reflect.Type, fieldName string) reflect.Value {
	switch fieldType.Kind() {
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(fieldType)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(41)).Convert(fieldType)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(uint64(41)).Convert(fieldType)
	case reflect.String:
		return reflect.ValueOf("value-" + fieldName).Convert(fieldType)
	case reflect.Pointer:
		value := reflect.New(fieldType.Elem())
		value.Elem().Set(businessStoreAdapterSampleValue(fieldType.Elem(), fieldName))
		return value
	case reflect.Slice:
		value := reflect.MakeSlice(fieldType, 2, 2)
		value.Index(0).Set(businessStoreAdapterSampleValue(fieldType.Elem(), fieldName))
		value.Index(1).Set(businessStoreAdapterSampleValue(fieldType.Elem(), fieldName+"Second"))
		return value
	case reflect.Struct:
		return reflect.ValueOf(time.Date(2026, time.July, 10, 1, 2, 3, 0, time.UTC)).Convert(fieldType)
	default:
		return reflect.Zero(fieldType)
	}
}
