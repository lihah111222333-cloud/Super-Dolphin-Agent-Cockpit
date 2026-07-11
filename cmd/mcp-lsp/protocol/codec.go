package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// JSONRPCVersion 是本包接受和生成的 JSON-RPC 协议版本。
const JSONRPCVersion = "2.0"

// JSON-RPC 编解码错误用于区分 envelope、request 和 response 的失败边界。
var (
	ErrInvalidEnvelope = errors.New("protocol: invalid json-rpc envelope")
	ErrInvalidRequest  = errors.New("protocol: invalid request")
	ErrInvalidResponse = errors.New("protocol: invalid response")
)

// Request 表示带 id 的 JSON-RPC 请求，params 保持原始 JSON 以延后到具体方法解码。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification 表示无 id 的 JSON-RPC 通知，适用于 publishDiagnostics 等服务端推送。
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 表示 JSON-RPC 响应，result 与 error 必须互斥。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError 保存 JSON-RPC error 对象，Data 延后到调用方按方法解析。
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Envelope 是松散 JSON-RPC 外壳，先承接未知消息再按 request/notification/response 校验。
type Envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// BuildRequest 构建合法请求，方法名和 id 缺失时立即返回错误。
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

// BuildNotification 构建无 id 通知，方法名为空时 fail-fast。
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

// BuildSuccessResponse 构建成功响应，并复制 id/result 避免调用方后续修改 RawMessage。
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

// BuildErrorResponse 构建错误响应，data 编码失败会阻断而不是吞掉附加信息。
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

// EncodeMessage 将已校验消息编码为 JSON payload。
func EncodeMessage(message any) ([]byte, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}
	return payload, nil
}

// DecodeEnvelope 先校验 JSON-RPC 版本，保留其余字段供后续严格解码。
func DecodeEnvelope(payload []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if strings.TrimSpace(env.JSONRPC) != JSONRPCVersion {
		return Envelope{}, fmt.Errorf("%w: jsonrpc must be %s", ErrInvalidEnvelope, JSONRPCVersion)
	}
	return env, nil
}

// DecodeRequest 解码带 id 请求，拒绝缺 method 或缺 id 的半包。
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

// DecodeNotification 解码无 id 通知，出现 id 时视为 envelope 类型错误。
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

// DecodeResponse 解码响应并强制 result/error 互斥。
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

// marshalPayload 标准化可复用 RawMessage，传入合法 JSON bytes 时不二次编码。
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

// cloneResponseError 深拷贝 error data，避免 Response 共享调用方 RawMessage 底层切片。
func cloneResponseError(err *ResponseError) *ResponseError {
	if err == nil {
		return nil
	}
	out := *err
	out.Data = shared.CloneRawMessage(err.Data)
	return &out
}
