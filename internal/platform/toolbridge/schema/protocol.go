package schema

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

const (
	ProtocolID        = "reasonix.mcp-schema-helper/v1"
	maxEnvelopeBytes  = 384 * 1024
	maxArgumentsBytes = 64 * 1024
	maxStdoutBytes    = 64 * 1024
	maxStderrBytes    = 16 * 1024
	maxMessageBytes   = 4 * 1024
)

// Operation 是 helper 唯一支持的 one-shot 操作。
type Operation string

const (
	OperationCompile  Operation = "compile"
	OperationValidate Operation = "validate"
)

// Invocation 绑定 schema 请求身份和 authority generation，但不创建 authority。
type Invocation struct {
	Operation           Operation
	RequestID           string
	ServerID            string
	ToolName            string
	AuthorityGeneration uint64
	Schema              CanonicalSchema
	Arguments           json.RawMessage
}

// FenceStage 标识父端 authority callback 的执行阶段。
type FenceStage string

const (
	FenceBeforeLaunch FenceStage = "before_launch"
	FenceAfterSuccess FenceStage = "after_success"
)

// FenceIdentity 是 authority owner callback 需要复核的不可变身份。
type FenceIdentity struct {
	ServerID            string
	ToolName            string
	AuthorityGeneration uint64
	SchemaDigest        string
}

// FenceHook 由 Task 4B authority owner 提供；Task 4A 只调用并 fail-fast。
type FenceHook func(context.Context, FenceStage, FenceIdentity) error

// Result 是完成严格协议和 digest fence 后的父端结果。
type Result struct {
	Operation      Operation
	SchemaDigest   string
	CompiledDigest string
	ArgumentsValid bool
	Code           Code
	Message        string
}

type protocolRequest struct {
	Protocol            string          `json:"protocol"`
	Operation           Operation       `json:"operation"`
	RequestID           string          `json:"request_id"`
	ServerID            string          `json:"server_id"`
	ToolName            string          `json:"tool_name"`
	AuthorityGeneration uint64          `json:"authority_generation"`
	SchemaDigest        string          `json:"schema_digest"`
	Draft               string          `json:"draft"`
	CanonicalSchema     json.RawMessage `json:"canonical_schema"`
	Arguments           json.RawMessage `json:"arguments,omitempty"`
}

type protocolResponse struct {
	Protocol            string    `json:"protocol"`
	Operation           Operation `json:"operation"`
	RequestID           string    `json:"request_id"`
	ServerID            string    `json:"server_id"`
	ToolName            string    `json:"tool_name"`
	AuthorityGeneration uint64    `json:"authority_generation"`
	Draft               string    `json:"draft"`
	SchemaDigest        string    `json:"schema_digest"`
	CompiledDigest      string    `json:"compiled_digest"`
	OK                  bool      `json:"ok"`
	Code                Code      `json:"code"`
	Message             string    `json:"message"`
	ArgumentsValid      *bool     `json:"arguments_valid,omitempty"`
}

func newProtocolRequest(invocation Invocation) (protocolRequest, error) {
	if err := validateInvocationIdentity(invocation); err != nil {
		return protocolRequest{}, err
	}
	if err := validateCanonicalIdentity(invocation.Schema); err != nil {
		return protocolRequest{}, err
	}
	if err := validateInvocationArguments(invocation); err != nil {
		return protocolRequest{}, err
	}
	return protocolRequest{
		Protocol:            ProtocolID,
		Operation:           invocation.Operation,
		RequestID:           invocation.RequestID,
		ServerID:            invocation.ServerID,
		ToolName:            invocation.ToolName,
		AuthorityGeneration: invocation.AuthorityGeneration,
		SchemaDigest:        invocation.Schema.Digest,
		Draft:               invocation.Schema.Draft,
		CanonicalSchema:     append(json.RawMessage(nil), invocation.Schema.Bytes...),
		Arguments:           append(json.RawMessage(nil), invocation.Arguments...),
	}, nil
}

func decodeProtocolRequest(raw []byte) (protocolRequest, error) {
	var request protocolRequest
	if err := strictDecodeObject(raw, maxEnvelopeBytes, &request); err != nil {
		return protocolRequest{}, err
	}
	if request.Protocol != ProtocolID {
		return protocolRequest{}, newDiagnostic(CodeInvalidEnvelope, "unsupported protocol", nil)
	}
	invocation := Invocation{
		Operation:           request.Operation,
		RequestID:           request.RequestID,
		ServerID:            request.ServerID,
		ToolName:            request.ToolName,
		AuthorityGeneration: request.AuthorityGeneration,
		Schema: CanonicalSchema{
			Bytes:  request.CanonicalSchema,
			Digest: request.SchemaDigest,
			Draft:  request.Draft,
		},
		Arguments: request.Arguments,
	}
	if _, err := newProtocolRequest(invocation); err != nil {
		return protocolRequest{}, err
	}
	return request, nil
}

// decodeProtocolResponse 严格解码并校验单个 helper 响应。
func decodeProtocolResponse(raw []byte) (protocolResponse, error) {
	var response protocolResponse
	if err := strictDecodeObject(raw, maxStdoutBytes, &response); err != nil {
		return protocolResponse{}, newDiagnostic(CodeProtocolViolation, "invalid helper response envelope", err)
	}
	if err := validateResponseIdentity(response); err != nil {
		return protocolResponse{}, err
	}
	if len(response.Message) > maxMessageBytes {
		return protocolResponse{}, newDiagnostic(CodeOutputTooLarge, "helper message exceeds 4 KiB", nil)
	}
	if err := validateResponseOperationShape(response); err != nil {
		return protocolResponse{}, err
	}
	if err := validateResponseDiagnosticShape(response); err != nil {
		return protocolResponse{}, err
	}
	return response, nil
}

// validateResponseIdentity 校验响应中的协议身份字段完整且合法。
func validateResponseIdentity(response protocolResponse) error {
	if response.Protocol != ProtocolID ||
		(response.Operation != OperationCompile && response.Operation != OperationValidate) {
		return newDiagnostic(CodeProtocolViolation, "helper response protocol identity is invalid", nil)
	}
	if response.RequestID == "" || response.ServerID == "" || response.ToolName == "" {
		return newDiagnostic(CodeProtocolViolation, "helper response request identity is incomplete", nil)
	}
	if response.AuthorityGeneration == 0 || response.Draft == "" || response.SchemaDigest == "" {
		return newDiagnostic(CodeProtocolViolation, "helper response schema identity is incomplete", nil)
	}
	return nil
}

func validateResponseOperationShape(response protocolResponse) error {
	if response.Operation == OperationValidate && response.ArgumentsValid == nil {
		return newDiagnostic(CodeProtocolViolation, "validate response omits arguments_valid", nil)
	}
	if response.Operation == OperationCompile && response.ArgumentsValid != nil {
		return newDiagnostic(CodeProtocolViolation, "compile response includes arguments_valid", nil)
	}
	return nil
}

// validateResponseDiagnosticShape 校验成功和失败响应的字段组合。
func validateResponseDiagnosticShape(response protocolResponse) error {
	if response.OK {
		if response.Code != "" || response.Message != "" || response.CompiledDigest == "" {
			return newDiagnostic(CodeProtocolViolation, "success response has invalid diagnostic fields", nil)
		}
		return nil
	}
	if !isKnownCode(response.Code) || response.Message == "" || response.CompiledDigest != "" {
		return newDiagnostic(CodeProtocolViolation, "error response has invalid diagnostic fields", nil)
	}
	return nil
}

// validateInvocationIdentity 校验父侧调用身份字段和操作类型。
func validateInvocationIdentity(invocation Invocation) error {
	if invocation.Operation != OperationCompile && invocation.Operation != OperationValidate {
		return newDiagnostic(CodeInvalidEnvelope, "operation must be compile or validate", nil)
	}
	if invocation.RequestID == "" || invocation.ServerID == "" ||
		invocation.ToolName == "" || invocation.AuthorityGeneration == 0 {
		return newDiagnostic(CodeInvalidEnvelope, "request identity is incomplete", nil)
	}
	return nil
}

func validateCanonicalIdentity(schema CanonicalSchema) error {
	verified, err := Canonicalize(schema.Bytes)
	if err != nil {
		return err
	}
	if !bytes.Equal(verified.Bytes, schema.Bytes) {
		return newDiagnostic(CodeDigestMismatch, "canonical schema bytes are inconsistent", nil)
	}
	if verified.Digest != schema.Digest || verified.Draft != schema.Draft {
		return newDiagnostic(CodeDigestMismatch, "canonical schema identity is inconsistent", nil)
	}
	return nil
}

// validateInvocationArguments 校验 validate 参数的大小和严格 JSON framing。
func validateInvocationArguments(invocation Invocation) error {
	if invocation.Operation == OperationCompile {
		if len(invocation.Arguments) != 0 {
			return newDiagnostic(CodeInvalidEnvelope, "compile forbids arguments", nil)
		}
		return nil
	}
	if len(invocation.Arguments) == 0 {
		return newDiagnostic(CodeInvalidEnvelope, "validate requires arguments", nil)
	}
	if len(invocation.Arguments) > maxArgumentsBytes {
		return newDiagnostic(CodeInputTooLarge, "arguments exceed 64 KiB", nil)
	}
	if _, _, err := parseJSON(invocation.Arguments, false); err != nil {
		return err
	}
	return nil
}

// strictDecodeObject 按真实 DTO 字段严格拒绝未知、缺失、重复和尾随数据。
func strictDecodeObject(raw []byte, limit int, target any) error {
	if err := validateEnvelopeBytes(raw, limit); err != nil {
		return err
	}
	fields, err := strictObjectFieldNames(raw)
	if err != nil {
		return err
	}
	allowed, required, err := jsonTypeFields(target)
	if err != nil {
		return newDiagnostic(CodeInvalidEnvelope, "protocol target is invalid", err)
	}
	if err := validateProtocolFields(fields, allowed, required); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return newDiagnostic(CodeInvalidEnvelope, "protocol field type mismatch", err)
	}
	if err := ensureDecoderEOF(decoder); err != nil {
		return newDiagnostic(CodeInvalidEnvelope, "protocol envelope has trailing bytes", err)
	}
	return nil
}

func strictObjectFieldNames(raw []byte) ([]string, error) {
	root, _, err := parseJSON(raw, false)
	if err != nil {
		return nil, err
	}
	if root.kind != kindObject {
		return nil, newDiagnostic(CodeInvalidEnvelope, "protocol envelope must be an object", nil)
	}
	fields := make([]string, 0, len(root.object))
	for field := range root.object {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields, nil
}

func encodedJSONFieldNames(producer any) ([]string, error) {
	raw, err := json.Marshal(producer)
	if err != nil {
		return nil, err
	}
	return strictObjectFieldNames(raw)
}

// jsonTypeFields 从真实 DTO 标签动态导出允许字段和必需字段。
func jsonTypeFields(target any) (map[string]struct{}, map[string]struct{}, error) {
	typeOf := reflect.TypeOf(target)
	if typeOf.Kind() != reflect.Pointer || typeOf.Elem().Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("target must be pointer to struct")
	}
	typeOf = typeOf.Elem()
	allowed := make(map[string]struct{}, typeOf.NumField())
	required := make(map[string]struct{}, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		tag := typeOf.Field(index).Tag.Get("json")
		name, options, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			return nil, nil, fmt.Errorf("field %s lacks a protocol JSON name", typeOf.Field(index).Name)
		}
		allowed[name] = struct{}{}
		if !strings.Contains(options, "omitempty") {
			required[name] = struct{}{}
		}
	}
	return allowed, required, nil
}

func ensureDecoderEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("second JSON value")
		}
		return err
	}
	return nil
}

func recomputeDigest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func validateEnvelopeBytes(raw []byte, limit int) error {
	if len(raw) == 0 || len(raw) > limit {
		return newDiagnostic(CodeInputTooLarge, "JSON envelope size is outside the frozen limit", nil)
	}
	if !bytes.Equal(raw, bytes.TrimSpace(raw)) {
		return newDiagnostic(CodeInvalidEnvelope, "JSON envelope has leading or trailing whitespace", nil)
	}
	return nil
}

func validateProtocolFields(
	fields []string,
	allowed map[string]struct{},
	required map[string]struct{},
) error {
	present := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if _, ok := allowed[field]; !ok {
			return newDiagnostic(CodeInvalidEnvelope, "unknown protocol field", nil)
		}
		present[field] = struct{}{}
	}
	for field := range required {
		if _, ok := present[field]; !ok {
			return newDiagnostic(CodeInvalidEnvelope, "missing protocol field", nil)
		}
	}
	return nil
}
