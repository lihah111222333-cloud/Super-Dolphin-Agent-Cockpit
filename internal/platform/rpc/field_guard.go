package rpc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// RejectUnknownJSONFields 用 wire 结构体的 json tag 生成允许字段集合。
// 自定义 UnmarshalJSON 先调用它，避免兼容解码用普通 json.Unmarshal 时吞掉拼错字段。
func RejectUnknownJSONFields(data []byte, method string, wireShapes ...any) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	allowed := jsonWireFieldSet(wireShapes...)
	for key := range payload {
		if _, ok := allowed[key]; !ok {
			return ErrInvalidParams(fmt.Sprintf("%s: unknown field %q", method, key))
		}
	}
	return nil
}

func jsonWireFieldSet(wireShapes ...any) map[string]struct{} {
	fields := make(map[string]struct{})
	for _, shape := range wireShapes {
		collectJSONWireFields(fields, reflect.TypeOf(shape))
	}
	return fields
}

// collectJSONWireFields 递归读取 wire 结构体上可导出的 json 字段名。
// 匿名嵌入且未声明 json 名称时展开，避免组合结构体漏掉基础字段。
func collectJSONWireFields(fields map[string]struct{}, typ reflect.Type) {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		collectJSONWireField(fields, typ.Field(i))
	}
}

// collectJSONWireField 处理单个 struct 字段，过滤未导出字段和 json:"-"。
// 匿名嵌入字段按 encoding/json 的展开习惯继续递归收集。
func collectJSONWireField(fields map[string]struct{}, field reflect.StructField) {
	if field.PkgPath != "" && !field.Anonymous {
		return
	}
	name, hasName := jsonFieldName(field)
	if name == "-" {
		return
	}
	if !hasName && field.Anonymous {
		collectJSONWireFields(fields, field.Type)
		return
	}
	if name != "" {
		fields[name] = struct{}{}
	}
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name, false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return field.Name, false
	}
	return name, true
}
