// Package authorityhttp 通过 HTTP 暴露关闭的 Recovery Custody authority RPC 接口。
// 本包是专用 JSON-RPC transport，不注册 MCP 生命周期或工具方法。
package authorityhttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

// Config 绑定 authority backend、不可变公开身份、认证密钥和资源上限。
type Config struct {
	Backend       recoverycustody.Client
	Identity      rpcwire.AuthorityIdentity
	BearerToken   BearerCredential
	MaxBodyBytes  int64
	MaxConcurrent int
}

type authorityHandler struct {
	backend      recoverycustody.Client
	authority    rpcwire.AuthorityIdentity
	bearerDigest [sha256.Size]byte
	maxBodyBytes int64
	requestSlots chan struct{}
}

// NewHandler 以 fail-fast 配置构造只含三个关闭方法的有界 HTTP handler。
func NewHandler(config Config) (http.Handler, error) {
	if isNilClient(config.Backend) {
		return nil, errors.New("recovery custody authority HTTP backend is required")
	}
	if _, ok := config.Backend.(recoverycustody.VerifiedClient); !ok {
		return nil, errors.New("recovery custody authority HTTP backend must verify terminal receipts")
	}
	if err := config.Identity.Validate(); err != nil {
		return nil, errors.New("recovery custody authority HTTP identity is invalid")
	}
	if !validBearerToken(config.BearerToken.value) {
		return nil, errors.New("recovery custody authority HTTP bearer token is invalid")
	}
	if config.MaxBodyBytes <= 0 {
		return nil, errors.New("recovery custody authority HTTP max body bytes must be positive")
	}
	if config.MaxConcurrent <= 0 {
		return nil, errors.New("recovery custody authority HTTP max concurrency must be positive")
	}
	return &authorityHandler{
		backend:      config.Backend,
		authority:    config.Identity,
		bearerDigest: sha256.Sum256([]byte(config.BearerToken.value)),
		maxBodyBytes: config.MaxBodyBytes,
		requestSlots: make(chan struct{}, config.MaxConcurrent),
	}, nil
}

// ServeHTTP 在派发关闭 JSON-RPC 方法前完成认证、body 和并发约束。
func (h *authorityHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !h.preflight(w, request) {
		return
	}
	select {
	case h.requestSlots <- struct{}{}:
		defer func() { <-h.requestSlots }()
	default:
		http.Error(w, "authority request capacity exceeded", http.StatusServiceUnavailable)
		return
	}

	request.Body = http.MaxBytesReader(w, request.Body, h.maxBodyBytes)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "request body unavailable", http.StatusBadRequest)
		return
	}

	rpcRequest, rpcFailure := decodeRequest(payload)
	if rpcFailure != nil {
		writeRPCError(w, rpcFailure.id, rpcFailure.code, rpcFailure.message)
		return
	}
	h.dispatch(request.Context(), w, rpcRequest)
}

// preflight 在读取 request body 前拒绝 transport 与认证错误。
func (h *authorityHandler) preflight(w http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	authorization := request.Header.Values("Authorization")
	if len(authorization) == 0 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	if len(authorization) != 1 || strings.TrimSpace(authorization[0]) == "" {
		http.Error(w, "duplicate or empty Authorization header", http.StatusBadRequest)
		return false
	}
	if !h.authorized(authorization[0]) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	contentTypes := request.Header.Values("Content-Type")
	if len(contentTypes) != 1 || strings.TrimSpace(contentTypes[0]) == "" {
		http.Error(w, "exactly one JSON Content-Type is required", http.StatusUnsupportedMediaType)
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/json" {
		http.Error(w, "application/json is required", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

// authorized 对定长摘要做常量时间比较，且不规范化 opaque 配置 token。
func (h *authorityHandler) authorized(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	presented := strings.TrimPrefix(header, prefix)
	presentedDigest := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(presentedDigest[:], h.bearerDigest[:]) == 1
}

// dispatch 仅调用 identity、submit 或 query，并把 backend 结果映射为安全 wire 信封。
func (h *authorityHandler) dispatch(ctx context.Context, w http.ResponseWriter, request rpcRequest) {
	switch request.method {
	case rpcwire.MethodIdentity:
		writeRPCResult(w, request.id, rpcwire.Result{SchemaVersion: rpcwire.SchemaVersion, Authority: h.authority})
	case rpcwire.MethodSubmit:
		params, err := rpcwire.DecodeSubmitParams(request.params)
		if err != nil {
			writeRPCError(w, request.id, invalidParamsCode, invalidParamsMessage)
			return
		}
		outcome, backendErr := h.backend.Submit(ctx, params.Request)
		writeRPCResult(w, request.id, h.backendResult(request.method, outcome, backendErr))
	case rpcwire.MethodQuery:
		params, err := rpcwire.DecodeQueryParams(request.params)
		if err != nil {
			writeRPCError(w, request.id, invalidParamsCode, invalidParamsMessage)
			return
		}
		outcome, backendErr := h.backend.Query(ctx, params.Handle)
		writeRPCResult(w, request.id, h.backendResult(request.method, outcome, backendErr))
	default:
		writeRPCError(w, request.id, methodNotFoundCode, methodNotFoundMessage)
	}
}

// backendResult 保留安全领域 outcome，并把未知错误或非法实现结果折叠为 authority_unavailable。
func (h *authorityHandler) backendResult(method rpcwire.Method, outcome recoverycustody.Outcome, backendErr error) rpcwire.Result {
	if backendErr == nil {
		if outcome.Status == recoverycustody.StatusAvailable || outcome.Status == recoverycustody.StatusAborted {
			if _, ok := h.backend.(recoverycustody.VerifiedClient); !ok {
				return h.unavailableResult()
			}
		}
		result := rpcwire.Result{
			SchemaVersion: rpcwire.SchemaVersion,
			Authority:     h.authority,
			Outcome:       &outcome,
		}
		if err := result.ValidateForMethod(method); err == nil {
			return result
		}
		return h.unavailableResult()
	}

	failure, known := rpcwire.FailureFromError(backendErr)
	if !known {
		failure = rpcwire.Failure{Code: rpcwire.FailureAuthorityUnavailable}
	}
	result := rpcwire.Result{
		SchemaVersion: rpcwire.SchemaVersion,
		Authority:     h.authority,
		Failure:       &failure,
	}
	withOutcome := result
	withOutcome.Outcome = &outcome
	if err := withOutcome.ValidateForMethod(method); err == nil {
		return withOutcome
	}
	if err := result.ValidateForMethod(method); err == nil {
		return result
	}
	return h.unavailableResult()
}

func (h *authorityHandler) unavailableResult() rpcwire.Result {
	failure := rpcwire.Failure{Code: rpcwire.FailureAuthorityUnavailable}
	return rpcwire.Result{
		SchemaVersion: rpcwire.SchemaVersion,
		Authority:     h.authority,
		Failure:       &failure,
	}
}

// validBearerToken 拒绝空值、控制字符和无法放入单值 HTTP header 的 token。
func validBearerToken(token string) bool {
	if token == "" || len(token) > 4096 || !utf8.ValidString(token) {
		return false
	}
	for _, char := range token {
		if char <= ' ' || char == 0x7f {
			return false
		}
	}
	return true
}

func isNilClient(client recoverycustody.Client) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func writeRPCResult(w http.ResponseWriter, id []byte, result rpcwire.Result) {
	payload, err := rpcwire.MarshalCanonical(result)
	if err != nil {
		writeRPCError(w, id, internalErrorCode, internalErrorMessage)
		return
	}
	response, err := marshalResultResponse(id, payload)
	if err != nil {
		writeRPCError(w, id, internalErrorCode, internalErrorMessage)
		return
	}
	writeJSON(w, response)
}

func writeRPCError(w http.ResponseWriter, id []byte, code rpcCode, message string) {
	payload, err := marshalErrorResponse(id, code, message)
	if err != nil {
		payload = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"Internal error"}}`)
	}
	writeJSON(w, payload)
}

func writeJSON(w http.ResponseWriter, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(payload); err != nil {
		return
	}
}

var _ http.Handler = (*authorityHandler)(nil)
