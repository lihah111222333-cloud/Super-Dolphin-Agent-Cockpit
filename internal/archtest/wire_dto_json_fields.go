package archtest

import (
	"fmt"
	"reflect"
	"strings"
)

// wireDTOJSONField 是从 producer 类型动态推导的 JSON 字段描述符。
type wireDTOJSONField struct {
	jsonName string
	goName   string
	index    []int
}

// wireDTOJSONFields 枚举 producer 的 JSON 字段，作为所有 wire DTO guard 的唯一事实源。
func wireDTOJSONFields(typ reflect.Type) ([]wireDTOJSONField, error) {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("wire DTO producer %v must be a struct", typ)
	}

	fields := make([]wireDTOJSONField, 0, typ.NumField())
	seen := make(map[string]string, typ.NumField())
	if err := collectWireDTOJSONFields(typ, nil, &fields, seen); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("wire DTO producer %s has zero JSON fields", typ)
	}
	return fields, nil
}

// collectWireDTOJSONFields 展开未标记的匿名结构字段，并保留访问路径。
func collectWireDTOJSONFields(
	typ reflect.Type,
	prefix []int,
	fields *[]wireDTOJSONField,
	seen map[string]string,
) error {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		index := append(append([]int(nil), prefix...), i)
		if field.Anonymous && field.Tag.Get("json") == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() != reflect.Struct {
				return fmt.Errorf("embedded wire DTO producer field %s must be a struct", field.Name)
			}
			if err := collectWireDTOJSONFields(embedded, index, fields, seen); err != nil {
				return err
			}
			continue
		}

		jsonName, ok := wireDTOJSONFieldName(field)
		if !ok {
			continue
		}
		if previous, exists := seen[jsonName]; exists {
			return fmt.Errorf("wire DTO producer field %s duplicates JSON field %q from %s", field.Name, jsonName, previous)
		}
		seen[jsonName] = field.Name
		*fields = append(*fields, wireDTOJSONField{jsonName: jsonName, goName: field.Name, index: index})
	}
	return nil
}

// wireDTOJSONFieldName 解析结构字段的有效 JSON tag 名称。
func wireDTOJSONFieldName(field reflect.StructField) (string, bool) {
	tag := strings.TrimSpace(field.Tag.Get("json"))
	if tag == "" || tag == "-" {
		return "", false
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	return tag, tag != "" && tag != "-"
}
