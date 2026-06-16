package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// JSONRPCVersion is the JSON-RPC protocol version emitted by this sidecar.
const JSONRPCVersion = "2.0"

var (
	ErrInvalidEnvelope = errors.New("protocol: invalid json-rpc envelope")
	ErrInvalidRequest  = errors.New("protocol: invalid request")
	ErrInvalidResponse = errors.New("protocol: invalid response")
)

// Request is a JSON-RPC request with an ID that expects a response.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification is a JSON-RPC notification without an ID.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response containing either a result or an error.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError is the JSON-RPC error object returned for failed requests.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Envelope is the loose JSON-RPC shape decoded before classifying a payload as
// request, notification, or response.
type Envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// BuildRequest 构建请求。
func BuildRequest(method string, id any, params any) (Request, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return Request{}, fmt.Errorf("method is required")
	}
	reqID, err := marshalPayload(id)
	if err != nil {
		return Request{}, fmt.Errorf("build request id: %w", err)
	}
	if !hasRequestID(reqID) {
		return Request{}, fmt.Errorf("%w: id is required", ErrInvalidRequest)
	}
	rawParams, err := marshalPayload(params)
	if err != nil {
		return Request{}, fmt.Errorf("build request params: %w", err)
	}
	return Request{
		JSONRPC: JSONRPCVersion,
		ID:      reqID,
		Method:  method,
		Params:  rawParams,
	}, nil
}

// BuildNotification 构建notification。
func BuildNotification(method string, params any) (Notification, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return Notification{}, fmt.Errorf("method is required")
	}
	rawParams, err := marshalPayload(params)
	if err != nil {
		return Notification{}, fmt.Errorf("build notification params: %w", err)
	}
	return Notification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  rawParams,
	}, nil
}

// BuildSuccessResponse 构建success响应。
func BuildSuccessResponse(id json.RawMessage, result any) (Response, error) {
	rawResult, err := marshalPayload(result)
	if err != nil {
		return Response{}, fmt.Errorf("build response result: %w", err)
	}
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      shared.CloneRawMessage(id),
		Result:  rawResult,
	}, nil
}

// BuildErrorResponse 构建错误响应。
func BuildErrorResponse(id json.RawMessage, code int, message string, data any) (Response, error) {
	rawData, err := marshalPayload(data)
	if err != nil {
		return Response{}, fmt.Errorf("build response error data: %w", err)
	}
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      shared.CloneRawMessage(id),
		Error: &ResponseError{
			Code:    code,
			Message: message,
			Data:    rawData,
		},
	}, nil
}

// EncodeMessage 编码消息。
func EncodeMessage(message any) ([]byte, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}
	return payload, nil
}

// DecodeEnvelope 解码包装。
func DecodeEnvelope(payload []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
	}
	if strings.TrimSpace(env.JSONRPC) != JSONRPCVersion {
		return Envelope{}, fmt.Errorf("%w: jsonrpc must be %s", ErrInvalidEnvelope, JSONRPCVersion)
	}
	return env, nil
}

// DecodeRequest 解码请求。
func DecodeRequest(payload []byte) (Request, error) {
	env, err := DecodeEnvelope(payload)
	if err != nil {
		return Request{}, err
	}
	if strings.TrimSpace(env.Method) == "" || !hasRequestID(env.ID) {
		return Request{}, fmt.Errorf("%w: method and id are required", ErrInvalidRequest)
	}
	return Request{
		JSONRPC: env.JSONRPC,
		ID:      shared.CloneRawMessage(env.ID),
		Method:  env.Method,
		Params:  shared.CloneRawMessage(env.Params),
	}, nil
}

// DecodeNotification 解码notification。
func DecodeNotification(payload []byte) (Notification, error) {
	env, err := DecodeEnvelope(payload)
	if err != nil {
		return Notification{}, err
	}
	if strings.TrimSpace(env.Method) == "" || hasRequestID(env.ID) {
		return Notification{}, fmt.Errorf("%w: expected notification", ErrInvalidEnvelope)
	}
	return Notification{
		JSONRPC: env.JSONRPC,
		Method:  env.Method,
		Params:  shared.CloneRawMessage(env.Params),
	}, nil
}

// DecodeResponse 解码响应。
func DecodeResponse(payload []byte) (Response, error) {
	env, err := DecodeEnvelope(payload)
	if err != nil {
		return Response{}, err
	}
	hasResult := len(bytes.TrimSpace(env.Result)) > 0
	if strings.TrimSpace(env.Method) != "" {
		return Response{}, fmt.Errorf("%w: response cannot contain method", ErrInvalidResponse)
	}
	if !hasRequestID(env.ID) {
		return Response{}, fmt.Errorf("%w: id is required", ErrInvalidResponse)
	}
	if hasResult && env.Error != nil {
		return Response{}, fmt.Errorf("invalid JSON-RPC response: result and error are mutually exclusive")
	}
	if !hasResult && env.Error == nil {
		return Response{}, fmt.Errorf("%w: result or error is required", ErrInvalidResponse)
	}
	return Response{
		JSONRPC: env.JSONRPC,
		ID:      shared.CloneRawMessage(env.ID),
		Result:  shared.CloneRawMessage(env.Result),
		Error:   cloneResponseError(env.Error),
	}, nil
}

func hasRequestID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && string(trimmed) != "null"
}

// marshalPayload 编码载荷。
func marshalPayload(value any) (json.RawMessage, error) {
	switch raw := value.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return shared.CloneRawMessage(raw), nil
	case []byte:
		if json.Valid(raw) {
			return shared.CloneRawMessage(raw), nil
		}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func cloneResponseError(err *ResponseError) *ResponseError {
	if err == nil {
		return nil
	}
	out := *err
	out.Data = shared.CloneRawMessage(err.Data)
	return &out
}
