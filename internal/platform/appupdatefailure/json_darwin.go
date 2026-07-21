//go:build darwin

package appupdatefailure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var recordFields = [...]string{"version", "generation", "state", "code", "retryable", "action", "transaction_id"}

func encodeRecord(value record) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode app update failure sidecar: %w", err)
	}
	return raw, nil
}

// decodeRecord 以 token 级解析拒绝重复键、嵌套值与尾随 JSON。
func decodeRecord(raw []byte) (record, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return record{}, errors.New("app update failure sidecar must be a flat JSON object")
	}
	value, seen, err := decodeRecordObject(decoder)
	if err != nil {
		return record{}, err
	}
	if err := requireRecordFields(seen); err != nil {
		return record{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return record{}, err
	}
	if err := validateRecord(value); err != nil {
		return record{}, err
	}
	return value, nil
}

// decodeRecordObject 解析顶层对象并对每个字段执行唯一性检查。
func decodeRecordObject(decoder *json.Decoder) (record, map[string]struct{}, error) {
	value := record{}
	seen := make(map[string]struct{}, len(recordFields))
	for decoder.More() {
		field, err := nextUniqueField(decoder, seen)
		if err != nil {
			return record{}, nil, err
		}
		if err := decodeRecordField(decoder, field, &value); err != nil {
			return record{}, nil, errors.New("app update failure sidecar field is malformed")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return record{}, nil, errors.New("app update failure sidecar object is not closed")
	}
	return value, seen, nil
}

func nextUniqueField(decoder *json.Decoder, seen map[string]struct{}) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", errors.New("app update failure sidecar field name is malformed")
	}
	field, ok := token.(string)
	if !ok {
		return "", errors.New("app update failure sidecar field name is not a string")
	}
	if _, duplicate := seen[field]; duplicate {
		return "", errors.New("app update failure sidecar contains a duplicate field")
	}
	if !knownRecordField(field) {
		return "", errors.New("app update failure sidecar contains an unknown field")
	}
	seen[field] = struct{}{}
	return field, nil
}

// decodeRecordField 按固定原始类型解码一个已知字段，从而拒绝嵌套值。
func decodeRecordField(decoder *json.Decoder, field string, value *record) error {
	switch field {
	case "version":
		return decoder.Decode(&value.Version)
	case "generation":
		return decoder.Decode(&value.Generation)
	case "state":
		return decoder.Decode(&value.State)
	case "code":
		return decoder.Decode(&value.Code)
	case "retryable":
		return decoder.Decode(&value.Retryable)
	case "action":
		return decoder.Decode(&value.Action)
	case "transaction_id":
		return decoder.Decode(&value.TransactionID)
	default:
		return errors.New("app update failure sidecar field is unknown")
	}
}

func requireRecordFields(seen map[string]struct{}) error {
	if len(seen) != len(recordFields) {
		return errors.New("app update failure sidecar schema is incomplete")
	}
	for _, field := range recordFields {
		if _, ok := seen[field]; !ok {
			return errors.New("app update failure sidecar schema is incomplete")
		}
	}
	return nil
}

func knownRecordField(candidate string) bool {
	for _, field := range recordFields {
		if candidate == field {
			return true
		}
	}
	return false
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("app update failure sidecar contains trailing data")
	}
	return nil
}
