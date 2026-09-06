package rpcwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// MarshalCanonical 校验后生成无空白、固定字段顺序的 canonical JSON。
func MarshalCanonical(value interface{ Validate() error }) ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical JSON", ErrInvalidWire)
	}
	return payload, nil
}

// DecodeSubmitParams 严格解码 canonical submit 对象参数。
func DecodeSubmitParams(payload []byte) (SubmitParams, error) {
	var params SubmitParams
	if err := decodeCanonical(payload, &params, paramsValidator(&params)); err != nil {
		return SubmitParams{}, err
	}
	return params, nil
}

// DecodeQueryParams 严格解码 canonical query 对象参数。
func DecodeQueryParams(payload []byte) (QueryParams, error) {
	var params QueryParams
	if err := decodeCanonical(payload, &params, paramsValidator(&params)); err != nil {
		return QueryParams{}, err
	}
	return params, nil
}

// DecodeResult 严格解码 canonical 共用响应信封。
func DecodeResult(payload []byte) (Result, error) {
	var result Result
	if err := decodeCanonical(payload, &result, paramsValidator(&result)); err != nil {
		return Result{}, err
	}
	return result, nil
}

// DecodeResultForMethod 严格解码并校验指定方法的响应形状。
func DecodeResultForMethod(payload []byte, method Method) (Result, error) {
	var result Result
	if err := decodeCanonical(payload, &result, func() error { return result.ValidateForMethod(method) }); err != nil {
		return Result{}, err
	}
	return result, nil
}

func paramsValidator(value interface{ Validate() error }) func() error {
	return value.Validate
}

// decodeCanonical 按形状、字段、领域校验和重编码一致性四层拒绝歧义 JSON。
func decodeCanonical(payload []byte, target any, validate func() error) error {
	if err := validateJSONShape(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode strict JSON object", ErrInvalidWire)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(payload, canonical) {
		return fmt.Errorf("%w: JSON is not canonical", ErrInvalidWire)
	}
	return nil
}

func validateJSONShape(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidWire)
	}
	if first != json.Delim('{') {
		return fmt.Errorf("%w: top-level value must be an object", ErrInvalidWire)
	}
	if err := scanJSONObject(decoder); err != nil {
		return err
	}
	return requireJSONTokenEOF(decoder)
}

// scanJSONObject 递归扫描对象键并拒绝任意层级的重复字段。
func scanJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: malformed object key", ErrInvalidWire)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("%w: object key is not a string", ErrInvalidWire)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate object field", ErrInvalidWire)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("%w: malformed object", ErrInvalidWire)
	}
	return nil
}

// scanJSONValue 递归检查嵌套值并拒绝显式 null。
func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON value", ErrInvalidWire)
	}
	if token == nil {
		return fmt.Errorf("%w: null is forbidden", ErrInvalidWire)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, arrayErr := decoder.Token()
		if arrayErr != nil || end != json.Delim(']') {
			return fmt.Errorf("%w: malformed array", ErrInvalidWire)
		}
		return nil
	default:
		return fmt.Errorf("%w: unexpected JSON delimiter", ErrInvalidWire)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidWire)
	}
	return nil
}

func requireJSONTokenEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON token", ErrInvalidWire)
	}
	return nil
}
