package trustcandidate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// MarshalCanonical 生成无空白、固定字段顺序的严格 envelope JSON。
func MarshalCanonical(envelope TrustEnvelope) ([]byte, error) {
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: envelope JSON encoding failed", ErrInvalidJSON)
	}
	if len(payload) > MaxEnvelopeJSONBytes {
		return nil, fmt.Errorf("%w: envelope JSON exceeds bound", ErrJSONTooLarge)
	}
	return payload, nil
}

// DecodeCanonical 严格解码 envelope，拒绝 duplicate/unknown/null/trailing 字段。
func DecodeCanonical(payload []byte) (TrustEnvelope, error) {
	if err := validateJSONShape(payload); err != nil {
		return TrustEnvelope{}, err
	}
	var envelope TrustEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return TrustEnvelope{}, fmt.Errorf("%w: strict envelope decode failed", ErrUnknownField)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return TrustEnvelope{}, err
	}
	if err := envelope.Validate(); err != nil {
		return TrustEnvelope{}, err
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(payload, canonical) {
		return TrustEnvelope{}, fmt.Errorf("%w: envelope JSON is not canonical", ErrInvalidJSON)
	}
	return envelope, nil
}

// MarshalCanonicalEvent 生成只含 envelope/ref 的 canonical event JSON。
func MarshalCanonicalEvent(event CanonicalEvent, ticket ContentTicket) ([]byte, error) {
	if err := event.Validate(ticket); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("%w: event JSON encoding failed", ErrInvalidJSON)
	}
	if len(payload) > MaxEnvelopeJSONBytes {
		return nil, fmt.Errorf("%w: event JSON exceeds bound", ErrJSONTooLarge)
	}
	return payload, nil
}

// DecodeCanonicalEvent 严格解码 event，并由 ticket 再次验证正文绑定。
func DecodeCanonicalEvent(payload []byte, ticket ContentTicket) (CanonicalEvent, error) {
	if err := validateJSONShape(payload); err != nil {
		return CanonicalEvent{}, err
	}
	var event CanonicalEvent
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return CanonicalEvent{}, fmt.Errorf("%w: strict event decode failed", ErrUnknownField)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return CanonicalEvent{}, err
	}
	if err := event.Validate(ticket); err != nil {
		return CanonicalEvent{}, err
	}
	canonical, err := json.Marshal(event)
	if err != nil || !bytes.Equal(payload, canonical) {
		return CanonicalEvent{}, fmt.Errorf("%w: event JSON is not canonical", ErrInvalidJSON)
	}
	return event, nil
}

// MarshalCanonicalControl 明确不序列化控制分支；candidate 没有签名 owner。
func MarshalCanonicalControl(control CanonicalControl) ([]byte, error) {
	return nil, ErrControlRejected
}

// DecodeCanonicalControl 明确不接受任何控制 JSON，避免通过 parser 绕过 trust root。
func DecodeCanonicalControl(payload []byte) (CanonicalControl, error) {
	return CanonicalControl{}, ErrControlRejected
}

// validateJSONShape 递归限制 JSON 大小、深度、成员数，并拒绝 duplicate/null。
func validateJSONShape(payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty JSON", ErrInvalidJSON)
	}
	if len(payload) > MaxEnvelopeJSONBytes {
		return fmt.Errorf("%w: JSON payload exceeds bound", ErrJSONTooLarge)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidJSON)
	}
	if first == nil {
		return fmt.Errorf("%w: top-level null is forbidden", ErrNullField)
	}
	if first != json.Delim('{') {
		return fmt.Errorf("%w: top-level JSON value must be an object", ErrInvalidJSON)
	}
	if err := scanJSONObject(decoder, 0); err != nil {
		return err
	}
	return requireJSONTokenEOF(decoder)
}

// scanJSONObject 递归扫描对象并拒绝 duplicate key、超深与超多成员。
func scanJSONObject(decoder *json.Decoder, depth int) error {
	if depth > MaxJSONDepth {
		return fmt.Errorf("%w: JSON object nesting exceeds bound", ErrJSONTooDeep)
	}
	seen := make(map[string]struct{}, MaxObjectMembers)
	members := 0
	for decoder.More() {
		members++
		if members > MaxObjectMembers {
			return fmt.Errorf("%w: object member count exceeds bound", ErrInvalidJSON)
		}
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: malformed object key", ErrInvalidJSON)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("%w: object key is not a string", ErrInvalidJSON)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate field %q", ErrDuplicateField, key)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder, depth); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("%w: malformed object", ErrInvalidJSON)
	}
	return nil
}

// scanJSONArray 递归扫描数组并限制深度与成员数量。
func scanJSONArray(decoder *json.Decoder, depth int) error {
	if depth > MaxJSONDepth {
		return fmt.Errorf("%w: JSON array nesting exceeds bound", ErrJSONTooDeep)
	}
	members := 0
	for decoder.More() {
		members++
		if members > MaxObjectMembers {
			return fmt.Errorf("%w: array member count exceeds bound", ErrInvalidJSON)
		}
		if err := scanJSONValue(decoder, depth); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return fmt.Errorf("%w: malformed array", ErrInvalidJSON)
	}
	return nil
}

// scanJSONValue 扫描一个 JSON value，显式 null 和异常 delimiter 均失败。
func scanJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON value", ErrInvalidJSON)
	}
	if token == nil {
		return fmt.Errorf("%w: null is forbidden", ErrNullField)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanJSONObject(decoder, depth+1)
	case '[':
		return scanJSONArray(decoder, depth+1)
	default:
		return fmt.Errorf("%w: unexpected JSON delimiter", ErrInvalidJSON)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidJSON)
	}
	return nil
}

func requireJSONTokenEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON token", ErrInvalidJSON)
	}
	return nil
}
