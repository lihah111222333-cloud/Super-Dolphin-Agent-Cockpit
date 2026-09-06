package issuer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
)

const endpointURLTextLimit = 2048

// receiptMatchesRequest 逐字段比较 binding receipt、请求和 prepared process。
func receiptMatchesRequest(receipt BindingReceipt, request BindRequest, prepared managedruntime.PreparedReceipt) error {
	if !matchesBindingRequest(receipt, request) || !matchesPreparedIdentity(receipt, prepared) {
		return fmt.Errorf("%w: receipt field mapping mismatch", ErrInvalidBinding)
	}
	return nil
}

// matchesBindingRequest 比较 receipt 与 bind request 的 operation/fence/endpoint 字段。
func matchesBindingRequest(receipt BindingReceipt, request BindRequest) bool {
	return matchesBindingIdentity(receipt, request) && matchesBindingCredential(receipt, request) &&
		matchesBindingProvider(receipt, request) && matchesBindingEndpoint(receipt, request)
}

// matchesBindingIdentity 比较 binding 与 request 的 operation/prepared 字段。
func matchesBindingIdentity(receipt BindingReceipt, request BindRequest) bool {
	return receipt.OperationID == request.OperationID && receipt.BindingRequestDigest == request.RequestDigest &&
		receipt.PreparedOperationID == request.PreparedOperationID && receipt.PreparedRequestDigest == request.PreparedRequestDigest
}

// matchesBindingCredential 比较 binding 与 request 的 credential 引用。
func matchesBindingCredential(receipt BindingReceipt, request BindRequest) bool {
	return receipt.CredentialLeaseID == request.CredentialLeaseID && receipt.CredentialDigest == request.CredentialDigest
}

// matchesBindingProvider 比较 binding 与 request 的 provider/profile/fence 摘要。
func matchesBindingProvider(receipt BindingReceipt, request BindRequest) bool {
	return receipt.ProviderProfileRef == request.ProviderProfileRef && receipt.ProviderProfileDigest == request.ProviderProfileDigest &&
		receipt.ParentExecutionIncarnationID == request.ParentExecutionIncarnationID && receipt.ParentFenceDigest == request.ParentFenceDigest &&
		receipt.ConnectionNonceDigest == request.ConnectionNonceDigest && receipt.Provider == request.Provider
}

// matchesBindingEndpoint 比较 binding 与 request 的 endpoint identity 摘要。
func matchesBindingEndpoint(receipt BindingReceipt, request BindRequest) bool {
	return receipt.EndpointDigest == request.Endpoint.Digest && receipt.EndpointInstanceID == request.Endpoint.InstanceID
}

// matchesPreparedIdentity 比较 receipt 与 verifier 已验证 prepared process 的 provider/session/代际。
func matchesPreparedIdentity(receipt BindingReceipt, prepared managedruntime.PreparedReceipt) bool {
	return receipt.ProviderThreadID == prepared.ProviderThreadID && receipt.ProviderSessionID == prepared.ProviderSessionID &&
		receipt.RuntimeOwnerID == prepared.RuntimeOwnerID && receipt.RuntimeGeneration == prepared.RuntimeGeneration &&
		receipt.ProcessGeneration == prepared.ProcessGeneration && receipt.RuntimeWriterEpoch == prepared.RuntimeWriterEpoch &&
		receipt.PreparedReceiptRef == prepared.ReceiptRef && receipt.PreparedReceiptDigest == prepared.ReceiptDigest
}

// validateClosedReceipt 比较关闭 receipt 的操作、终态和证据引用。
func validateClosedReceipt(receipt ClosedReceipt, operationID, bindingOperationID string, prepared *managedruntime.PreparedReceipt, state State, operationDigest, expectedRef, expectedDigest, evidenceRef, evidenceDigest string) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if !matchesClosedOperation(receipt, operationID, bindingOperationID, state, operationDigest) ||
		!matchesClosedEvidence(receipt, expectedRef, expectedDigest, evidenceRef, evidenceDigest) {
		return fmt.Errorf("%w: closed receipt mapping mismatch", ErrInvalidClosure)
	}
	if prepared != nil && !matchesClosedPrepared(receipt, *prepared) {
		return fmt.Errorf("%w: closed prepared mapping mismatch", ErrInvalidClosure)
	}
	return nil
}

// matchesClosedOperation 比较 closed receipt 的操作和状态字段。
func matchesClosedOperation(receipt ClosedReceipt, operationID, bindingOperationID string, state State, operationDigest string) bool {
	return receipt.OperationID == operationID && receipt.BindingOperationID == bindingOperationID && receipt.State == state &&
		receipt.OperationDigest == operationDigest
}

// matchesClosedEvidence 比较 closed receipt 的 expected receipt 与关闭证据。
func matchesClosedEvidence(receipt ClosedReceipt, expectedRef, expectedDigest, evidenceRef, evidenceDigest string) bool {
	return receipt.ExpectedReceiptRef == expectedRef && receipt.ExpectedReceiptDigest == expectedDigest &&
		receipt.ClosureEvidenceRef == evidenceRef && receipt.ClosureEvidenceDigest == evidenceDigest
}

// matchesClosedPrepared 比较 closed receipt 与原 prepared process 的 identity。
func matchesClosedPrepared(receipt ClosedReceipt, prepared managedruntime.PreparedReceipt) bool {
	return receipt.PreparedOperationID == prepared.OperationID && receipt.PreparedRequestDigest == prepared.RequestDigest
}

// validateQueryPresence 验证 query result 仅出现当前状态允许的 receipt 分支。
func validateQueryPresence(result QueryResult) error {
	wantPrepared, wantBinding, wantClosed, wantUnknown := false, false, false, false
	switch result.State {
	case StateCandidate:
	case StatePrepared:
		wantPrepared = true
	case StateBound:
		wantBinding = true
	case StateAborted, StateRevoked:
		wantClosed = true
	case StateOutcomeUnknown:
		wantUnknown = true
	default:
		return ErrInvalidQuery
	}
	gotPrepared := completePair(result.PreparedReceiptRef, result.PreparedReceiptDigest)
	gotBinding := completePair(result.BindingReceiptRef, result.BindingReceiptDigest)
	gotClosed := completePair(result.ClosedReceiptRef, result.ClosedReceiptDigest)
	gotUnknown := completePair(result.UncertaintyReceiptRef, result.UncertaintyDigest)
	if gotPrepared != wantPrepared || gotBinding != wantBinding || gotClosed != wantClosed || gotUnknown != wantUnknown {
		return ErrInvalidQuery
	}
	return nil
}

// validateQueryRefs 校验 query result 当前分支中的引用和摘要格式。
func validateQueryRefs(result QueryResult) error {
	pairs := [4]struct {
		ref, digest string
	}{
		{result.PreparedReceiptRef, result.PreparedReceiptDigest},
		{result.BindingReceiptRef, result.BindingReceiptDigest},
		{result.ClosedReceiptRef, result.ClosedReceiptDigest},
		{result.UncertaintyReceiptRef, result.UncertaintyDigest},
	}
	for _, pair := range pairs {
		if pair.ref == "" && pair.digest == "" {
			continue
		}
		if !validText(pair.ref, 512) || !validDigest(pair.digest) {
			return ErrInvalidQuery
		}
	}
	return nil
}

// validLoopbackURL 限制 endpoint 为显式 127.0.0.1 HTTP /mcp 地址。
func validLoopbackURL(value string) bool {
	if value == "" || len(value) > endpointURLTextLimit {
		return false
	}
	parsed, err := url.Parse(value)
	if !validURLShape(parsed, err, value) {
		return false
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return false
	}
	return validPort(parsed.Port())
}

// validURLShape 校验 endpoint URL 的 scheme、host、path 和 canonical 表示。
func validURLShape(parsed *url.URL, err error, value string) bool {
	return validURLAuthority(parsed, err) && validURLPath(parsed) && !strings.HasSuffix(value, "#") && parsed.String() == value
}

// validURLAuthority 校验 endpoint URL 的 scheme、user 信息和 loopback host。
func validURLAuthority(parsed *url.URL, err error) bool {
	return err == nil && parsed != nil && parsed.Scheme == "http" && parsed.User == nil && parsed.RawQuery == "" &&
		!parsed.ForceQuery && parsed.Fragment == "" && parsed.Opaque == "" && parsed.Hostname() == "127.0.0.1"
}

// validURLPath 校验 endpoint URL 使用固定 /mcp 路径且无 RawPath 绕过。
func validURLPath(parsed *url.URL) bool {
	return parsed != nil && parsed.Path == "/mcp" && parsed.RawPath == ""
}

// validPort 校验 endpoint 的显式合法 TCP 端口。
func validPort(port string) bool {
	if port == "" {
		return false
	}
	var number int
	if _, err := fmt.Sscanf(port, "%d", &number); err != nil || number < 1 || number > 65535 || fmt.Sprint(number) != port {
		return false
	}
	return true
}

// validProvider 固定 provider profile 的官方 CLI 枚举。
func validProvider(value string) bool {
	switch value {
	case ProviderCodex, ProviderClaude, ProviderAntigravity, ProviderGrok:
		return true
	default:
		return false
	}
}

// validText 检查有界 UTF-8 canonical 文本和控制字符。
func validText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// validInstanceID 检查 endpoint 实例标识不携带路径或 URL 分隔符。
func validInstanceID(value string) bool {
	return validText(value, 256) && !strings.ContainsAny(value, `/\\?#`)
}

// validOptionalText 检查可为空的 bounded canonical 文本。
func validOptionalText(value string, limit int) bool {
	return value == "" || validText(value, limit)
}

// validDigest 检查小写 SHA-256 十六进制摘要。
func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

// validState 检查生命周期状态是否属于显式集合。
func validState(value State) bool {
	switch value {
	case StateCandidate, StatePrepared, StateBound, StateRevoked, StateAborted, StateOutcomeUnknown:
		return true
	default:
		return false
	}
}

// validateSelfDigest 计算并比较指定对象的 domain-separated 自摘要。
func validateSelfDigest(base error, domain, got string, value any) error {
	digest, ok := computeRequestSelfDigest(domain, value)
	if !ok {
		digest, ok = computeReceiptSelfDigest(domain, value)
	}
	if !ok || digest != got {
		return base
	}
	return nil
}

// computeRequestSelfDigest 为 request 类型安全计算自摘要。
func computeRequestSelfDigest(domain string, value any) (string, bool) {
	switch domain {
	case bindDigestDomain:
		request, ok := value.(BindRequest)
		if !ok {
			return "", false
		}
		digest, err := ComputeBindRequestDigest(request)
		return digest, err == nil
	case abortDigestDomain:
		request, ok := value.(AbortRequest)
		if !ok {
			return "", false
		}
		digest, err := ComputeAbortRequestDigest(request)
		return digest, err == nil
	case revokeDigestDomain:
		request, ok := value.(RevokeRequest)
		if !ok {
			return "", false
		}
		digest, err := ComputeRevokeRequestDigest(request)
		return digest, err == nil
	default:
		return "", false
	}
}

// computeReceiptSelfDigest 为 receipt 类型安全计算自摘要。
func computeReceiptSelfDigest(domain string, value any) (string, bool) {
	switch domain {
	case bindingDigestDomain:
		receipt, ok := value.(BindingReceipt)
		if !ok {
			return "", false
		}
		digest, err := ComputeBindingReceiptDigest(receipt)
		return digest, err == nil
	case closedDigestDomain:
		receipt, ok := value.(ClosedReceipt)
		if !ok {
			return "", false
		}
		digest, err := ComputeClosedReceiptDigest(receipt)
		return digest, err == nil
	case queryDigestDomain:
		result, ok := value.(QueryResult)
		if !ok {
			return "", false
		}
		digest, err := ComputeQueryResultDigest(result)
		return digest, err == nil
	default:
		return "", false
	}
}

// isNilVerifier 拒绝 nil interface 和 typed-nil process receipt verifier。
func isNilVerifier(verifier ProcessReceiptVerifier) bool {
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

// completePair 判断引用和摘要是否同时存在。
func completePair(ref, digest string) bool { return ref != "" && digest != "" }

// hasPartial 判断引用和摘要是否只出现其中一个。
func hasPartial(ref, digest string) bool { return (ref == "") != (digest == "") }

// digestCanonical 以 domain-separated JSON 计算稳定 SHA-256 摘要。
func digestCanonical(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode managed runtime binding digest payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
