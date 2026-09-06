package messageassociation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// MarshalCanonical 校验候选并生成无空白、固定字段顺序的 JSON。
func MarshalCanonical(association Association) ([]byte, error) {
	if err := association.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(association)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical JSON", ErrInvalidAssociation)
	}
	return payload, nil
}

// DecodeCanonical 严格解码候选；重复、未知、null、尾随和非 canonical JSON 均拒绝。
func DecodeCanonical(payload []byte) (Association, error) {
	var association Association
	if err := validateJSONShape(payload); err != nil {
		return Association{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&association); err != nil {
		return Association{}, fmt.Errorf("%w: decode strict JSON object", ErrInvalidAssociation)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Association{}, err
	}
	if err := association.Validate(); err != nil {
		return Association{}, err
	}
	canonical, err := json.Marshal(association)
	if err != nil || !bytes.Equal(payload, canonical) {
		return Association{}, fmt.Errorf("%w: JSON is not canonical", ErrInvalidAssociation)
	}
	return association, nil
}

// validateJSONShape 先执行 payload 大小检查，再递归检查对象、数组和 token 形状。
func validateJSONShape(payload []byte) error {
	if len(payload) == 0 || len(payload) > maxIntentJSONBytes {
		return fmt.Errorf("%w: JSON payload exceeds bounded size", ErrInvalidAssociation)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidAssociation)
	}
	if first != json.Delim('{') {
		return fmt.Errorf("%w: top-level value must be an object", ErrInvalidAssociation)
	}
	if err := scanJSONObject(decoder, 0); err != nil {
		return err
	}
	return requireJSONTokenEOF(decoder)
}

// scanJSONObject 递归扫描对象键，拒绝 duplicate 和裸 correlation/reply 关联字段。
func scanJSONObject(decoder *json.Decoder, depth int) error {
	if depth > maxIntentJSONDepth {
		return fmt.Errorf("%w: JSON nesting exceeds bounded depth", ErrInvalidAssociation)
	}
	if err := scanObjectMembers(decoder, depth); err != nil {
		return err
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("%w: malformed object", ErrInvalidAssociation)
	}
	return nil
}

func scanObjectMembers(decoder *json.Decoder, depth int) error {
	seen := make(map[string]struct{})
	members := 0
	for decoder.More() {
		members++
		if members > maxIntentObjectMembers {
			return fmt.Errorf("%w: JSON object has too many members", ErrInvalidAssociation)
		}
		if err := scanObjectMember(decoder, depth, seen); err != nil {
			return err
		}
	}
	return nil
}

// scanObjectMember 校验单个对象成员的 key 唯一性、裸关联字段和递归 value。
func scanObjectMember(decoder *json.Decoder, depth int, seen map[string]struct{}) error {
	keyToken, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed object key", ErrInvalidAssociation)
	}
	key, ok := keyToken.(string)
	if !ok {
		return fmt.Errorf("%w: object key is not a string", ErrInvalidAssociation)
	}
	if _, duplicate := seen[key]; duplicate {
		return fmt.Errorf("%w: duplicate object field", ErrInvalidAssociation)
	}
	seen[key] = struct{}{}
	if key == "correlation_id" || key == "reply_to" {
		return ErrBareAssociation
	}
	return scanJSONValue(decoder, depth)
}

// scanJSONValue 递归拒绝显式 null、非法 delimiter 和嵌套对象歧义。
func scanJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON value", ErrInvalidAssociation)
	}
	if token == nil {
		return fmt.Errorf("%w: null is forbidden", ErrInvalidAssociation)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanJSONObject(decoder, depth+1)
	case '[':
		return scanJSONArray(decoder, depth)
	default:
		return fmt.Errorf("%w: unexpected JSON delimiter", ErrInvalidAssociation)
	}
}

// scanJSONArray 限制数组深度和成员数量，并递归扫描每个元素。
func scanJSONArray(decoder *json.Decoder, depth int) error {
	if depth+1 > maxIntentJSONDepth {
		return fmt.Errorf("%w: JSON nesting exceeds bounded depth", ErrInvalidAssociation)
	}
	members := 0
	for decoder.More() {
		members++
		if members > maxIntentObjectMembers {
			return fmt.Errorf("%w: JSON array has too many members", ErrInvalidAssociation)
		}
		if err := scanJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	end, arrayErr := decoder.Token()
	if arrayErr != nil || end != json.Delim(']') {
		return fmt.Errorf("%w: malformed array", ErrInvalidAssociation)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidAssociation)
	}
	return nil
}

func requireJSONTokenEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON token", ErrInvalidAssociation)
	}
	return nil
}
