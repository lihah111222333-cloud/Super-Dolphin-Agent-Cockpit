package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const JSONRPCVersion = "2.0"

var (
	ErrInvalidEnvelope = errors.New("protocol: invalid json-rpc envelope")
	ErrInvalidRequest  = errors.New("protocol: invalid request")
	ErrInvalidResponse = errors.New("protocol: invalid response")
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

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

func BuildSuccessResponse(id json.RawMessage, result any) (Response, error) {
	rawResult, err := marshalPayload(result)
	if err != nil {
		return Response{}, fmt.Errorf("build response result: %w", err)
	}
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      cloneRaw(id),
		Result:  rawResult,
	}, nil
}

func BuildErrorResponse(id json.RawMessage, code int, message string, data any) (Response, error) {
	rawData, err := marshalPayload(data)
	if err != nil {
		return Response{}, fmt.Errorf("build response error data: %w", err)
	}
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      cloneRaw(id),
		Error: &ResponseError{
			Code:    code,
			Message: message,
			Data:    rawData,
		},
	}, nil
}

func EncodeMessage(message any) ([]byte, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}
	return payload, nil
}

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
		ID:      cloneRaw(env.ID),
		Method:  env.Method,
		Params:  cloneRaw(env.Params),
	}, nil
}

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
		Params:  cloneRaw(env.Params),
	}, nil
}

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
		ID:      cloneRaw(env.ID),
		Result:  cloneRaw(env.Result),
		Error:   cloneResponseError(env.Error),
	}, nil
}

func hasRequestID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && string(trimmed) != "null"
}

func marshalPayload(value any) (json.RawMessage, error) {
	switch raw := value.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return cloneRaw(raw), nil
	case []byte:
		if json.Valid(raw) {
			return cloneRaw(raw), nil
		}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func cloneResponseError(err *ResponseError) *ResponseError {
	if err == nil {
		return nil
	}
	out := *err
	out.Data = cloneRaw(err.Data)
	return &out
}
