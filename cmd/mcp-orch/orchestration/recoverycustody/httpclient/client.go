// Package httpclient 实现 Recovery Custody 专用的 JSON-RPC 2.0 loopback HTTP 客户端。
// 本包只负责传输、wire 校验和调用方 receipt 验证，不拥有 authority 或持久化状态。
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

const (
	jsonRPCVersion      = "2.0"
	mediaTypeJSON       = "application/json"
	authorizationPrefix = "Bearer "
	redactedCredential  = "[redacted]"
	identityRequestID   = "recovery-custody/identity"
	remoteFailureReason = "recovery_custody_remote_failure"
	unavailableReason   = "recovery_custody_unavailable"
	maxConfiguredBytes  = 64 << 20
	maxEndpointBytes    = 4096
)

var (
	errInvalidConfig   = errors.New("recovery custody HTTP client configuration is invalid")
	errRedirectRefused = errors.New("recovery custody HTTP redirect is forbidden")
	// ErrClosed indicates that the HTTP client lifecycle has ended.
	ErrClosed = errors.New("recovery custody HTTP client is closed")
)

// BearerCredential 是只写入 Authorization 请求头的不可展示凭据。
type BearerCredential struct {
	value string
}

// NewBearerCredential 校验并封装不含空白、控制字符或 header 注入字符的 opaque bearer 值。
func NewBearerCredential(value string) (BearerCredential, error) {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return BearerCredential{}, fmt.Errorf("%w: bearer credential is invalid", errInvalidConfig)
	}
	for _, char := range value {
		if char <= 0x20 || char >= 0x7f {
			return BearerCredential{}, fmt.Errorf("%w: bearer credential is invalid", errInvalidConfig)
		}
	}
	return BearerCredential{value: value}, nil
}

// String 永远返回脱敏占位符。
func (BearerCredential) String() string { return redactedCredential }

// GoString 永远返回脱敏占位符。
func (BearerCredential) GoString() string {
	return "httpclient.BearerCredential(" + redactedCredential + ")"
}

// MarshalJSON 永远只序列化脱敏占位符。
func (BearerCredential) MarshalJSON() ([]byte, error) { return json.Marshal(redactedCredential) }

// Config 包含构造 Recovery Custody HTTP 客户端所需的全部显式配置。
type Config struct {
	Endpoint         string
	Credential       BearerCredential
	ExpectedIdentity rpcwire.AuthorityIdentity
	MaxRequestBytes  int64
	MaxResponseBytes int64
	ReceiptVerifier  recoverycustody.ReceiptVerifier
}

// String 返回不包含凭据的配置摘要。
func (Config) String() string { return "recovery custody HTTP client config" }

// GoString 返回不包含凭据的配置摘要。
func (Config) GoString() string { return "httpclient.Config{credential:" + redactedCredential + "}" }

// MarshalJSON 仅输出脱敏占位符，防止配置被误序列化为凭据载体。
func (Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Credential string `json:"credential"`
	}{Credential: redactedCredential})
}

// Client 是无重试、无代理、无重定向的 Recovery Custody HTTP adapter。
type Client struct {
	endpoint         string
	credential       BearerCredential
	expectedIdentity rpcwire.AuthorityIdentity
	maxRequestBytes  int64
	maxResponseBytes int64
	receiptVerifier  recoverycustody.ReceiptVerifier
	httpClient       *http.Client
	closed           atomic.Bool
}

// ReadinessProbe is the explicit authority identity readiness contract.
type ReadinessProbe interface {
	ProbeIdentity(context.Context) error
}

// Lifecycle combines the recovery custody client with readiness and shutdown.
// It remains adapter-local so recoverycustody.Client stays transport-neutral.
type Lifecycle interface {
	recoverycustody.Client
	ReadinessProbe
	Close() error
}

// String 返回不包含 endpoint、凭据或响应内容的客户端摘要。
func (*Client) String() string { return "recovery custody HTTP client" }

// GoString 返回不包含 endpoint、凭据或响应内容的客户端摘要。
func (*Client) GoString() string { return "httpclient.Client{credential:" + redactedCredential + "}" }

// MarshalJSON 只输出脱敏占位符，禁止把客户端内部配置变成序列化载体。
func (*Client) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Credential string `json:"credential"`
	}{Credential: redactedCredential})
}

// NewClient 校验全部显式配置并构造只访问固定 loopback IP endpoint 的客户端。
func NewClient(config Config) (*Client, error) {
	endpoint, err := validateEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.Credential.value == "" {
		return nil, fmt.Errorf("%w: bearer credential is required", errInvalidConfig)
	}
	if err := config.ExpectedIdentity.Validate(); err != nil {
		return nil, fmt.Errorf("%w: expected authority identity is invalid", errInvalidConfig)
	}
	if !validByteLimit(config.MaxRequestBytes) || !validByteLimit(config.MaxResponseBytes) {
		return nil, fmt.Errorf("%w: request and response limits are invalid", errInvalidConfig)
	}
	if isNilReceiptVerifier(config.ReceiptVerifier) {
		return nil, fmt.Errorf("%w: receipt verifier is required", errInvalidConfig)
	}
	if int64(len(authorizationPrefix)+len(config.Credential.value)) > config.MaxRequestBytes {
		return nil, fmt.Errorf("%w: authorization header exceeds request limit", errInvalidConfig)
	}

	transport := &http.Transport{
		Proxy:                  nil,
		DisableCompression:     true,
		MaxResponseHeaderBytes: config.MaxResponseBytes,
	}
	return &Client{
		endpoint:         endpoint,
		credential:       config.Credential,
		expectedIdentity: config.ExpectedIdentity,
		maxRequestBytes:  config.MaxRequestBytes,
		maxResponseBytes: config.MaxResponseBytes,
		receiptVerifier:  config.ReceiptVerifier,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errRedirectRefused
			},
		},
	}, nil
}

// Submit 在任何网络操作前派生稳定 handle，并且只发出一次 POST。
func (c *Client) Submit(ctx context.Context, request recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
	if err := c.ensureOpen(); err != nil {
		return recoverycustody.Outcome{}, err
	}
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	params := rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request}
	return c.callForOutcome(ctx, rpcwire.MethodSubmit, requestID(rpcwire.MethodSubmit, handle), params, handle)
}

// Query 只按调用方提供并验证过的原 handle 查询，不派生或替换 handle。
func (c *Client) Query(ctx context.Context, handle recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	if err := c.ensureOpen(); err != nil {
		return recoverycustody.Outcome{}, err
	}
	if err := handle.Validate(); err != nil {
		return recoverycustody.Outcome{}, err
	}
	params := rpcwire.QueryParams{SchemaVersion: rpcwire.SchemaVersion, Handle: handle}
	return c.callForOutcome(ctx, rpcwire.MethodQuery, requestID(rpcwire.MethodQuery, handle), params, handle)
}

// ProbeIdentity 执行只读 identity RPC，并要求响应与构造时固定的 expected identity 完全一致。
func (c *Client) ProbeIdentity(ctx context.Context) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	result, err := c.call(ctx, rpcwire.MethodIdentity, identityRequestID, nil)
	if err != nil {
		return err
	}
	if result.Authority != c.expectedIdentity {
		return unavailableError("authority identity changed")
	}
	return nil
}

// callForOutcome 把 wire result 收紧到原 handle，并在暴露终态前执行客户端 verifier。
func (c *Client) callForOutcome(
	ctx context.Context,
	method rpcwire.Method,
	id string,
	params interface{ Validate() error },
	handle recoverycustody.SubmissionHandle,
) (recoverycustody.Outcome, error) {
	result, err := c.call(ctx, method, id, params)
	if err != nil {
		return unavailableOutcome(handle), err
	}
	if result.Authority != c.expectedIdentity {
		return unavailableOutcome(handle), unavailableError("authority identity changed")
	}
	if result.Outcome != nil && result.Outcome.Handle != handle {
		return unavailableOutcome(handle), unavailableError("response handle changed")
	}
	if err := result.ValidateForClient(ctx, method, c.receiptVerifier); err != nil {
		return unavailableOutcome(handle), unavailableError("response verification failed")
	}
	if result.Failure != nil {
		mapped := mapFailure(*result.Failure)
		if result.Outcome != nil {
			return *result.Outcome, mapped
		}
		return remoteFailureOutcome(handle), mapped
	}
	return *result.Outcome, nil
}

// call 执行单次 HTTP 请求，并在读取响应前完成客户端生命周期校验。
func (c *Client) call(
	ctx context.Context,
	method rpcwire.Method,
	id string,
	params interface{ Validate() error },
) (rpcwire.Result, error) {
	if err := c.ensureOpen(); err != nil {
		return rpcwire.Result{}, err
	}
	payload, err := marshalRequest(method, id, params)
	if err != nil {
		return rpcwire.Result{}, unavailableError("request encoding failed")
	}
	if int64(len(payload)) > c.maxRequestBytes {
		return rpcwire.Result{}, unavailableError("request body exceeds configured limit")
	}
	request, err := c.newRequest(ctx, payload)
	if err != nil {
		return rpcwire.Result{}, unavailableError("request construction failed")
	}
	if err := c.ensureOpen(); err != nil {
		return rpcwire.Result{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return rpcwire.Result{}, unavailableError("HTTP request failed")
	}
	return c.readResponse(response, method, id)
}

// Close 结束客户端生命周期、拒绝后续调用并关闭 transport 空闲连接；重复关闭安全幂等。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.closed.Swap(true) {
		return nil
	}
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	return nil
}

func (c *Client) ensureOpen() error {
	if c == nil || c.closed.Load() {
		return ErrClosed
	}
	return nil
}

// newRequest 构造固定 endpoint 的单次 JSON POST，并只在此处附加 bearer 凭据。
func (c *Client) newRequest(ctx context.Context, payload []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", authorizationPrefix+c.credential.value)
	request.Header.Set("Content-Type", mediaTypeJSON)
	request.Header.Set("Accept", mediaTypeJSON)
	return request, nil
}

// readResponse 对 HTTP status、header、body 上限和 JSON-RPC 信封执行顺序化 fail-fast 校验。
func (c *Client) readResponse(
	response *http.Response,
	method rpcwire.Method,
	id string,
) (rpcwire.Result, error) {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return rpcwire.Result{}, unavailableError("HTTP response status is not OK")
	}
	if !validContentType(response.Header.Values("Content-Type")) {
		return rpcwire.Result{}, unavailableError("HTTP response content type is invalid")
	}
	if response.ContentLength > c.maxResponseBytes {
		return rpcwire.Result{}, unavailableError("HTTP response body exceeds configured limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return rpcwire.Result{}, unavailableError("HTTP response body is incomplete")
	}
	if int64(len(body)) > c.maxResponseBytes {
		return rpcwire.Result{}, unavailableError("HTTP response body exceeds configured limit")
	}
	return decodeResponse(body, method, id)
}

// validateEndpoint 只接受无 userinfo、query、fragment 的显式 loopback IP HTTP endpoint。
func validateEndpoint(raw string) (string, error) {
	if !validEndpointText(raw) {
		return "", fmt.Errorf("%w: endpoint must be bounded UTF-8", errInvalidConfig)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: endpoint must be a plain HTTP URL", errInvalidConfig)
	}
	if !isPlainHTTPURL(parsed) {
		return "", fmt.Errorf("%w: endpoint must be a plain HTTP URL", errInvalidConfig)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || !validPort(port) {
		return "", fmt.Errorf("%w: endpoint must contain an explicit IP port", errInvalidConfig)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%w: endpoint host must be a loopback IP literal", errInvalidConfig)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func validEndpointText(raw string) bool {
	return raw != "" && len(raw) <= maxEndpointBytes && utf8.ValidString(raw)
}

func isPlainHTTPURL(parsed *url.URL) bool {
	return parsed.Scheme == "http" && parsed.Opaque == "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == ""
}

func validPort(port string) bool {
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535 && strconv.Itoa(value) == port
}

func validByteLimit(limit int64) bool { return limit > 0 && limit <= maxConfiguredBytes }

func requestID(method rpcwire.Method, handle recoverycustody.SubmissionHandle) string {
	return string(method) + ":" + handle.HandleDigest
}

func unavailableOutcome(handle recoverycustody.SubmissionHandle) recoverycustody.Outcome {
	return recoverycustody.Outcome{
		Handle: handle, Status: recoverycustody.StatusIndeterminate, ReasonCode: unavailableReason,
	}
}

func remoteFailureOutcome(handle recoverycustody.SubmissionHandle) recoverycustody.Outcome {
	return recoverycustody.Outcome{
		Handle: handle, Status: recoverycustody.StatusIndeterminate, ReasonCode: remoteFailureReason,
	}
}

func unavailableError(reason string) error {
	return fmt.Errorf("%s: %w", reason, recoverycustody.ErrCustodyUnavailable)
}

func mapFailure(failure rpcwire.Failure) error {
	for _, mapping := range clientFailureMappings() {
		if mapping.code == failure.Code {
			return mapping.target
		}
	}
	return recoverycustody.ErrCustodyUnavailable
}

func clientFailureMappings() []struct {
	code   rpcwire.FailureCode
	target error
} {
	return []struct {
		code   rpcwire.FailureCode
		target error
	}{
		{rpcwire.FailureInvalidRequest, recoverycustody.ErrInvalidRequest},
		{rpcwire.FailureInvalidHandle, recoverycustody.ErrInvalidHandle},
		{rpcwire.FailureOperationConflict, recoverycustody.ErrCustodyConflict},
		{rpcwire.FailureReceiptRejected, recoverycustody.ErrReceiptRejected},
		{rpcwire.FailureResolutionConflict, recoverycustody.ErrResolutionConflict},
		{rpcwire.FailureOperationNotFound, recoverycustody.ErrOperationNotFound},
		{rpcwire.FailureAuthorityUnavailable, recoverycustody.ErrCustodyUnavailable},
		{rpcwire.FailureAuthorityReadOnly, recoverycustody.ErrCustodyUnavailable},
		{rpcwire.FailureLeaseRejected, recoverycustody.ErrCustodyUnavailable},
		{rpcwire.FailureCommitOutcomeUnknown, recoverycustody.ErrCustodyUnavailable},
	}
}

func validContentType(values []string) bool {
	return len(values) == 1 && strings.EqualFold(values[0], mediaTypeJSON)
}

func isNilReceiptVerifier(verifier recoverycustody.ReceiptVerifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

var _ recoverycustody.Client = (*Client)(nil)
var _ ReadinessProbe = (*Client)(nil)
var _ Lifecycle = (*Client)(nil)
var _ fmt.Stringer = BearerCredential{}
var _ fmt.GoStringer = BearerCredential{}
var _ json.Marshaler = BearerCredential{}
var _ fmt.Stringer = (*Client)(nil)
var _ fmt.GoStringer = (*Client)(nil)
var _ json.Marshaler = (*Client)(nil)
