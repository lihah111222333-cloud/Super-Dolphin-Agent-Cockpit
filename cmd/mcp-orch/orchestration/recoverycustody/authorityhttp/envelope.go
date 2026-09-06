package authorityhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/creachadair/jrpc2"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

type rpcCode = jrpc2.Code

const (
	parseErrorCode        = jrpc2.ParseError
	invalidRequestCode    = jrpc2.InvalidRequest
	methodNotFoundCode    = jrpc2.MethodNotFound
	invalidParamsCode     = jrpc2.InvalidParams
	internalErrorCode     = jrpc2.InternalError
	parseErrorMessage     = "Parse error"
	invalidRequestMessage = "Invalid Request"
	methodNotFoundMessage = "Method not found"
	invalidParamsMessage  = "Invalid params"
	internalErrorMessage  = "Internal error"
)

type rpcRequest struct {
	id     json.RawMessage
	method rpcwire.Method
	params json.RawMessage
}

type requestEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  rpcwire.Method  `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jrpc2.Error    `json:"error,omitempty"`
}

type requestFailure struct {
	id      json.RawMessage
	code    rpcCode
	message string
}

// decodeRequest 按 JSON 语法、对象形状、请求信封和关闭方法集四层解析请求。
func decodeRequest(payload []byte) (rpcRequest, *requestFailure) {
	if failure := validateRawRequest(payload); failure != nil {
		return rpcRequest{}, failure
	}
	envelope, failure := decodeEnvelope(payload)
	if failure != nil {
		return rpcRequest{}, failure
	}
	if failure := validateEnvelope(envelope, payload); failure != nil {
		return rpcRequest{}, failure
	}
	return rpcRequest{id: envelope.ID, method: envelope.Method, params: envelope.Params}, nil
}

// validateRawRequest 区分 JSON 语法错误与有效 JSON 的非对象请求。
func validateRawRequest(payload []byte) *requestFailure {
	if !json.Valid(payload) {
		return newRequestFailure(nil, parseErrorCode, parseErrorMessage)
	}
	if err := validateJSONObject(payload); err != nil {
		return newRequestFailure(nil, invalidRequestCode, invalidRequestMessage)
	}
	return nil
}

// decodeEnvelope 严格拒绝信封未知字段和尾随 JSON 值。
func decodeEnvelope(payload []byte) (requestEnvelope, *requestFailure) {
	var envelope requestEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return requestEnvelope{}, newRequestFailure(nil, invalidRequestCode, invalidRequestMessage)
	}
	if err := requireDecodeEOF(decoder); err != nil {
		return requestEnvelope{}, newRequestFailure(nil, invalidRequestCode, invalidRequestMessage)
	}
	return envelope, nil
}

// validateEnvelope 锁定必填字段、ID、canonical 编码、关闭方法集和方法专属参数形状。
func validateEnvelope(envelope requestEnvelope, payload []byte) *requestFailure {
	if !hasRequiredEnvelopeFields(envelope) {
		return newRequestFailure(nil, invalidRequestCode, invalidRequestMessage)
	}
	if !canonicalIDMatches(envelope.ID) {
		return newRequestFailure(nil, invalidRequestCode, invalidRequestMessage)
	}
	if !canonicalEnvelopeMatches(envelope, payload) {
		return newRequestFailure(nil, invalidRequestCode, invalidRequestMessage)
	}
	if err := envelope.Method.Validate(); err != nil {
		return newRequestFailure(envelope.ID, methodNotFoundCode, methodNotFoundMessage)
	}
	return validateMethodParams(envelope)
}

func hasRequiredEnvelopeFields(envelope requestEnvelope) bool {
	return envelope.JSONRPC == "2.0" && len(envelope.ID) != 0 && envelope.Method != ""
}

// validateMethodParams 要求 identity 唯一省略 params，并要求 submit/query 使用对象参数。
func validateMethodParams(envelope requestEnvelope) *requestFailure {
	if envelope.Method == rpcwire.MethodIdentity {
		if len(envelope.Params) != 0 {
			return newRequestFailure(envelope.ID, invalidParamsCode, invalidParamsMessage)
		}
		return nil
	}
	if len(envelope.Params) == 0 || envelope.Params[0] != '{' {
		return newRequestFailure(envelope.ID, invalidParamsCode, invalidParamsMessage)
	}
	return nil
}

func canonicalIDMatches(id json.RawMessage) bool {
	canonical, err := canonicalRequestID(id)
	return err == nil && bytes.Equal(canonical, id)
}

func canonicalEnvelopeMatches(envelope requestEnvelope, payload []byte) bool {
	canonical, err := json.Marshal(envelope)
	return err == nil && bytes.Equal(canonical, payload)
}

func newRequestFailure(id json.RawMessage, code rpcCode, message string) *requestFailure {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &requestFailure{id: id, code: code, message: message}
}

// canonicalRequestID 只接受可无损回写的 bounded string 或 int64 请求 ID。
func canonicalRequestID(payload []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errorsNewInvalidID()
	}
	if err := requireDecodeEOF(decoder); err != nil {
		return nil, errorsNewInvalidID()
	}
	switch typed := value.(type) {
	case string:
		if len(typed) > 128 {
			return nil, errorsNewInvalidID()
		}
		return json.Marshal(typed)
	case json.Number:
		integer, err := strconv.ParseInt(typed.String(), 10, 64)
		if err != nil {
			return nil, errorsNewInvalidID()
		}
		return []byte(strconv.FormatInt(integer, 10)), nil
	default:
		return nil, errorsNewInvalidID()
	}
}

func errorsNewInvalidID() error { return fmt.Errorf("invalid JSON-RPC request id") }

// validateJSONObject 递归拒绝顶层数组、重复字段、null 和尾随 token。
func validateJSONObject(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return fmt.Errorf("request is not an object")
	}
	if err := scanObject(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("request has trailing tokens")
	}
	return nil
}

// scanObject 扫描单个 JSON 对象并在任意重复键处失败。
func scanObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("invalid object key")
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("object key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate object key")
		}
		seen[key] = struct{}{}
		if err := scanValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("invalid object ending")
	}
	return nil
}

// scanValue 递归检查对象和数组内部值并拒绝显式 null。
func scanValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON value")
	}
	if token == nil {
		return fmt.Errorf("null JSON value")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanObject(decoder)
	case '[':
		for decoder.More() {
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid array ending")
		}
		return nil
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
}

func requireDecodeEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func marshalResultResponse(id, result []byte) ([]byte, error) {
	return json.Marshal(responseEnvelope{
		JSONRPC: "2.0",
		ID:      append(json.RawMessage(nil), id...),
		Result:  append(json.RawMessage(nil), result...),
	})
}

func marshalErrorResponse(id []byte, code rpcCode, message string) ([]byte, error) {
	if len(id) == 0 {
		id = []byte("null")
	}
	return json.Marshal(responseEnvelope{
		JSONRPC: "2.0",
		ID:      append(json.RawMessage(nil), id...),
		Error:   &jrpc2.Error{Code: code, Message: message},
	})
}
