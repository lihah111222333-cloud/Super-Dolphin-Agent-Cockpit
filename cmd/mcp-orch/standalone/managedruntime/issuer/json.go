package issuer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	maxJSONBytes   = 256 * 1024
	maxJSONDepth   = 16
	maxJSONMembers = 64
)

// DecodeEndpoint 严格解码 token-free endpoint identity。
func DecodeEndpoint(payload []byte) (Endpoint, error) {
	var endpoint Endpoint
	if err := decodeStrict(payload, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("%w: %v", ErrInvalidEndpoint, err)
	}
	if err := endpoint.Validate(); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

// DecodeBindRequest 严格解码 endpoint/process binding 请求并校验自摘要。
func DecodeBindRequest(payload []byte) (BindRequest, error) {
	var request BindRequest
	if err := decodeStrict(payload, &request); err != nil {
		return BindRequest{}, fmt.Errorf("%w: %v", ErrInvalidBindRequest, err)
	}
	if err := request.Validate(); err != nil {
		return BindRequest{}, err
	}
	return request, nil
}

// DecodeBindingReceipt 严格解码 endpoint/process binding receipt。
func DecodeBindingReceipt(payload []byte) (BindingReceipt, error) {
	var receipt BindingReceipt
	if err := decodeStrict(payload, &receipt); err != nil {
		return BindingReceipt{}, fmt.Errorf("%w: %v", ErrInvalidBinding, err)
	}
	if err := receipt.Validate(); err != nil {
		return BindingReceipt{}, err
	}
	return receipt, nil
}

// DecodeAbortRequest 严格解码 pre-bind abort 请求并校验自摘要。
func DecodeAbortRequest(payload []byte) (AbortRequest, error) {
	var request AbortRequest
	if err := decodeStrict(payload, &request); err != nil {
		return AbortRequest{}, fmt.Errorf("%w: %v", ErrInvalidClosure, err)
	}
	if err := request.Validate(); err != nil {
		return AbortRequest{}, err
	}
	return request, nil
}

// DecodeRevokeRequest 严格解码 post-bind revoke 请求并校验自摘要。
func DecodeRevokeRequest(payload []byte) (RevokeRequest, error) {
	var request RevokeRequest
	if err := decodeStrict(payload, &request); err != nil {
		return RevokeRequest{}, fmt.Errorf("%w: %v", ErrInvalidClosure, err)
	}
	if err := request.Validate(); err != nil {
		return RevokeRequest{}, err
	}
	return request, nil
}

// DecodeClosedReceipt 严格解码 definitive abort/revoke receipt。
func DecodeClosedReceipt(payload []byte) (ClosedReceipt, error) {
	var receipt ClosedReceipt
	if err := decodeStrict(payload, &receipt); err != nil {
		return ClosedReceipt{}, fmt.Errorf("%w: %v", ErrInvalidClosure, err)
	}
	if err := receipt.Validate(); err != nil {
		return ClosedReceipt{}, err
	}
	return receipt, nil
}

// DecodeQueryRequest 严格解码原操作查询请求。
func DecodeQueryRequest(payload []byte) (QueryRequest, error) {
	var request QueryRequest
	if err := decodeStrict(payload, &request); err != nil {
		return QueryRequest{}, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	if err := request.Validate(); err != nil {
		return QueryRequest{}, err
	}
	return request, nil
}

// DecodeQueryResult 严格解码 one-hot lifecycle query projection。
func DecodeQueryResult(payload []byte) (QueryResult, error) {
	var result QueryResult
	if err := decodeStrict(payload, &result); err != nil {
		return QueryResult{}, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	if err := result.Validate(); err != nil {
		return QueryResult{}, err
	}
	return result, nil
}

// decodeStrict 拒绝重复/未知/null/尾随字段，并要求 JSON 为 canonical struct 编码。
func decodeStrict(payload []byte, destination any) error {
	if err := validateJSONShape(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := requireEOF(decoder); err != nil {
		return err
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(payload, canonical) {
		return errors.New("JSON is not canonical")
	}
	return nil
}

// validateJSONShape 递归检查 JSON object、重复键、null、深度和成员上限。
func validateJSONShape(payload []byte) error {
	if len(payload) == 0 || len(payload) > maxJSONBytes {
		return errors.New("JSON payload is empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("top-level JSON object is required")
	}
	if err := scanObject(decoder, 1); err != nil {
		return err
	}
	return requireTokenEOF(decoder)
}

// scanObject 扫描对象键和值，拒绝重复键并限制嵌套深度。
func scanObject(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("JSON nesting is too deep")
	}
	seen := make(map[string]struct{})
	count := 0
	for decoder.More() {
		count++
		if count > maxJSONMembers {
			return errors.New("JSON object has too many members")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("JSON object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON field %q", key)
		}
		seen[key] = struct{}{}
		if err := scanValue(decoder, depth); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON object is malformed")
	}
	return nil
}

// scanValue 递归拒绝显式 null 并校验 object/array delimiter。
func scanValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is forbidden")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanObject(decoder, depth+1)
	case '[':
		return scanArray(decoder, depth+1)
	default:
		return errors.New("JSON delimiter is malformed")
	}
}

// scanArray 递归扫描数组并限制成员与深度。
func scanArray(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("JSON nesting is too deep")
	}
	count := 0
	for decoder.More() {
		count++
		if count > maxJSONMembers {
			return errors.New("JSON array has too many members")
		}
		if err := scanValue(decoder, depth); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return errors.New("JSON array is malformed")
	}
	return nil
}

// requireEOF 确认解码器没有第二个 JSON 值。
func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

// requireTokenEOF 确认 token scanner 没有第二个 JSON 文档。
func requireTokenEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON token")
	}
	return nil
}
