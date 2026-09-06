package httpclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/creachadair/jrpc2"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

type requestEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  rpcwire.Method  `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func marshalRequest(method rpcwire.Method, id string, params interface{ Validate() error }) ([]byte, error) {
	if err := method.Validate(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fmt.Errorf("JSON-RPC request ID is empty")
	}
	var rawParams json.RawMessage
	if params != nil {
		payload, err := rpcwire.MarshalCanonical(params)
		if err != nil {
			return nil, err
		}
		rawParams = payload
	}
	return json.Marshal(requestEnvelope{JSONRPC: jsonRPCVersion, ID: id, Method: method, Params: rawParams})
}

// decodeResponse 严格校验 JSON-RPC 版本、ID、result/error 互斥和方法对应的 wire result。
func decodeResponse(payload []byte, method rpcwire.Method, expectedID string) (rpcwire.Result, error) {
	if err := validateJSONObject(payload); err != nil {
		return rpcwire.Result{}, unavailableError("JSON-RPC response envelope is invalid")
	}
	var envelope responseEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return rpcwire.Result{}, unavailableError("JSON-RPC response envelope is invalid")
	}
	if err := requireEOF(decoder); err != nil {
		return rpcwire.Result{}, unavailableError("JSON-RPC response envelope is invalid")
	}
	if envelope.JSONRPC != jsonRPCVersion || !matchingID(envelope.ID, expectedID) ||
		(len(envelope.Result) == 0) == (len(envelope.Error) == 0) {
		return rpcwire.Result{}, unavailableError("JSON-RPC response envelope is invalid")
	}
	if len(envelope.Error) != 0 {
		if err := validateRPCError(envelope.Error); err != nil {
			return rpcwire.Result{}, unavailableError("JSON-RPC error envelope is invalid")
		}
		return rpcwire.Result{}, unavailableError("JSON-RPC server returned an error")
	}
	result, err := rpcwire.DecodeResultForMethod(envelope.Result, method)
	if err != nil {
		return rpcwire.Result{}, unavailableError("JSON-RPC result is invalid")
	}
	return result, nil
}

func matchingID(raw json.RawMessage, expected string) bool {
	var actual string
	if err := json.Unmarshal(raw, &actual); err != nil || actual != expected {
		return false
	}
	canonical, err := json.Marshal(actual)
	return err == nil && bytes.Equal(raw, canonical)
}

// validateRPCError 校验标准 jrpc2 error 对象形状，但不向调用方传播远端 message 或 data。
func validateRPCError(payload []byte) error {
	if err := validateJSONObject(payload); err != nil {
		return err
	}
	var rpcError jrpc2.Error
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rpcError); err != nil {
		return err
	}
	if err := requireEOF(decoder); err != nil {
		return err
	}
	if rpcError.Code == 0 || rpcError.Message == "" || len(rpcError.Message) > 1024 ||
		!utf8.ValidString(rpcError.Message) || strings.ContainsRune(rpcError.Message, 0) {
		return fmt.Errorf("invalid JSON-RPC error object")
	}
	return nil
}

func validateJSONObject(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return fmt.Errorf("top-level JSON value must be an object")
	}
	if err := scanObject(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON token")
	}
	return nil
}

// scanObject 递归拒绝任何层级的重复 JSON 对象键。
func scanObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyValue, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyValue.(string)
		if !ok {
			return fmt.Errorf("JSON object key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate JSON object key")
		}
		seen[key] = struct{}{}
		if err := scanValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("malformed JSON object")
	}
	return nil
}

// scanValue 递归扫描 JSON 值并拒绝会模糊 optional 字段语义的显式 null。
func scanValue(decoder *json.Decoder) error {
	value, err := decoder.Token()
	if err != nil {
		return err
	}
	if value == nil {
		return fmt.Errorf("JSON null is forbidden")
	}
	delimiter, ok := value.(json.Delim)
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
			return fmt.Errorf("malformed JSON array")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
