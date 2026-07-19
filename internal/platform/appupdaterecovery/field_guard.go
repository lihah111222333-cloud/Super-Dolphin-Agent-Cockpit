package appupdaterecovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

const releaseTransactionJournalChain = "release_transaction_journal"

// validateRequiredJSONFields 从真实 producer 类型递归枚举并校验 journal 字段。
func validateRequiredJSONFields(raw []byte, producer reflect.Type) error {
	return validateRequiredJSONFieldsForChain(raw, producer, releaseTransactionJournalChain)
}

func validateRequiredJSONFieldsForChain(raw []byte, producer reflect.Type, chain string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("field guard chain=%s producer=%s decode: %w", chain, producer.Name(), err)
	}
	return validateRequiredJSONValue(value, producer, "$", chain)
}

func validateRequiredJSONValue(value any, producer reflect.Type, path, chain string) error {
	if value == nil {
		return fmt.Errorf("field guard chain=%s producer=%s field=%s is null", chain, producer.Name(), path)
	}
	for producer.Kind() == reflect.Pointer {
		producer = producer.Elem()
	}
	switch producer.Kind() {
	case reflect.Struct:
		return validateRequiredJSONObject(value, producer, path, chain)
	case reflect.Slice, reflect.Array:
		return validateRequiredJSONArray(value, producer.Elem(), path, chain)
	default:
		return nil
	}
}

// validateRequiredJSONObject 按 producer 字段逐项校验对象和嵌套字段链。
func validateRequiredJSONObject(value any, producer reflect.Type, path, chain string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("field guard chain=%s producer=%s field=%s must be object", chain, producer.Name(), path)
	}
	for index := 0; index < producer.NumField(); index++ {
		field := producer.Field(index)
		name, include := producerJSONField(field)
		if !include {
			continue
		}
		fieldValue, exists := object[name]
		fieldPath := path + "." + name
		if !exists {
			return fmt.Errorf("field guard chain=%s producer=%s field=%s is missing", chain, producer.Name(), fieldPath)
		}
		if err := validateRequiredJSONValue(fieldValue, field.Type, fieldPath, chain); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredJSONArray(value any, element reflect.Type, path, chain string) error {
	array, ok := value.([]any)
	if !ok {
		return fmt.Errorf("field guard chain=%s producer=%s field=%s must be array", chain, element.Name(), path)
	}
	for index, item := range array {
		if err := validateRequiredJSONValue(item, element, fmt.Sprintf("%s[%d]", path, index), chain); err != nil {
			return err
		}
	}
	return nil
}

func producerJSONField(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" {
		return "", false
	}
	tag := field.Tag.Get("json")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", false
	}
	if name == "" {
		name = field.Name
	}
	return name, true
}
