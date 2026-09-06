package standalone

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// validateConfigJSON 从实际 Go 类型发现允许字段，拒绝重复、大小写别名和未知字段。
func validateConfigJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := checkConfigValue(decoder, reflect.TypeFor[Config]()); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("orch config has trailing JSON")
	}
	return nil
}

// checkConfigValue 按结构形状逐 token 检查，不用 json.Unmarshal 的末字段覆盖语义。
func checkConfigValue(decoder *json.Decoder, kind reflect.Type) error {
	token, err := decoder.Token()
	if err != nil || token == nil {
		return errors.New("orch config contains missing, null or malformed JSON")
	}
	switch kind.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return errors.New("orch config object required")
		}
		return checkConfigObject(decoder, kind)
	case reflect.Slice:
		if token != json.Delim('[') {
			return errors.New("orch config array required")
		}
		for decoder.More() {
			if err := checkConfigValue(decoder, kind.Elem()); err != nil {
				return err
			}
		}
		return requireConfigDelimiter(decoder, ']')
	default:
		if _, delimiter := token.(json.Delim); delimiter {
			return errors.New("orch config scalar required")
		}
		return nil
	}
}

// checkConfigObject 动态枚举 producer 字段，字段新增后同一校验器自动覆盖。
func checkConfigObject(decoder *json.Decoder, kind reflect.Type) error {
	fields, err := configFieldTypes(kind)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("orch config field name is malformed")
		}
		name, ok := token.(string)
		field, known := fields[name]
		if !ok || !known {
			return errors.New("orch config contains unknown field")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("orch config contains duplicate field")
		}
		seen[name] = struct{}{}
		if err := checkConfigValue(decoder, field.typ); err != nil {
			return err
		}
	}
	if hasMissingRequiredConfigField(fields, seen) {
		return errors.New("orch config is missing required fields")
	}
	return requireConfigDelimiter(decoder, '}')
}

type configField struct {
	typ      reflect.Type
	required bool
}

// configFieldTypes 以唯一 JSON tag 枚举配置类型，并保留 omitempty 的可选语义。
func configFieldTypes(kind reflect.Type) (map[string]configField, error) {
	fields := make(map[string]configField, kind.NumField())
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" || !field.IsExported() {
			return nil, errors.New("orch config schema contains an unregistered field")
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, errors.New("orch config schema contains duplicate fields")
		}
		fields[name] = configField{typ: field.Type, required: !hasJSONOption(tag, "omitempty")}
	}
	return fields, nil
}

func hasJSONOption(tag, want string) bool {
	for _, option := range strings.Split(tag, ",")[1:] {
		if option == want {
			return true
		}
	}
	return false
}

func hasMissingRequiredConfigField(fields map[string]configField, seen map[string]struct{}) bool {
	for name, field := range fields {
		if field.required {
			if _, ok := seen[name]; !ok {
				return true
			}
		}
	}
	return false
}

// requireConfigDelimiter 检查对象/数组闭合，错误不会被当成空配置。
func requireConfigDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil || token != expected {
		return errors.New("orch config JSON delimiter is invalid")
	}
	return nil
}
