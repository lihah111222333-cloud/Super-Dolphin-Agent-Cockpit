package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrCallerAttributionUnavailable 表示服务端 transport 没有为当前调用绑定归因快照。
var ErrCallerAttributionUnavailable = errors.New("caller attribution unavailable")

// ErrCallerAttributionInvalid 表示服务端绑定的调用归因缺失完整执行围栏或已被篡改。
var ErrCallerAttributionInvalid = errors.New("caller attribution invalid")

const (
	// CallerAttributionSchemaVersion 是当前 transport-private 调用归因结构版本。
	CallerAttributionSchemaVersion = 1
	// CallerAttributionSubjectKindTask 将 unified_v2 调用主体固定为 V3 task。
	CallerAttributionSubjectKindTask = "task"
	// CallerAttributionAudience 将归因限制到 mcp-orch unified_v2 服务端入口。
	CallerAttributionAudience = "mcp-orch/unified_v2"

	callerAttributionTextLimit  = 512
	callerAttributionDigestSize = 64
)

// CallerAttributionIssuerKind 是能够签发 unified_v2 调用归因的关闭 issuer 集合。
type CallerAttributionIssuerKind string

const (
	// CallerAttributionIssuerProviderBridge 表示 provider bridge 认证边界。
	CallerAttributionIssuerProviderBridge CallerAttributionIssuerKind = "provider_bridge"
	// CallerAttributionIssuerManagedRuntime 表示 managed runtime 认证边界。
	CallerAttributionIssuerManagedRuntime CallerAttributionIssuerKind = "managed_runtime"
	// CallerAttributionIssuerA2AGateway 表示 A2A gateway 认证边界。
	CallerAttributionIssuerA2AGateway CallerAttributionIssuerKind = "a2a_gateway"
)

// ExecutionFence 固定 logical agent 当前可执行 incarnation 的完整代际围栏。
//
// 该 DTO 只携带服务端已验证的身份引用和 digest，不包含 credential、token 或
// JTI，也不能由 ToolScope、工具参数、thread 或 cwd 推导。
type ExecutionFence struct {
	AgentID               string `json:"agent_id"`
	ReservationID         string `json:"reservation_id"`
	ReservationGeneration int64  `json:"reservation_generation"`
	IncarnationID         string `json:"incarnation_id"`
	IncarnationGeneration int64  `json:"incarnation_generation"`
	BindingVersion        int64  `json:"binding_version"`
	BindingDigest         string `json:"binding_digest"`
	SourceFenceRef        string `json:"source_fence_ref"`
}

// IssuerBinding 固定签发归因的 transport/runtime owner 及其当前代际。
type IssuerBinding struct {
	Kind       CallerAttributionIssuerKind `json:"kind"`
	BindingRef string                      `json:"binding_ref"`
	Generation int64                       `json:"generation"`
	Digest     string                      `json:"digest"`
}

// CallerAttribution 是进入 unified_v2 领域边界前由服务端 transport 写入的不可变快照。
//
// SubjectKind、Audience 和 SchemaVersion 都是固定值；TaskID 必须逐字等于
// Source.AgentID。该结构不是公开 MCP DTO，也不接受客户端 metadata 补全。
type CallerAttribution struct {
	SchemaVersion int            `json:"schema_version"`
	SubjectKind   string         `json:"subject_kind"`
	TaskID        string         `json:"task_id"`
	Source        ExecutionFence `json:"source"`
	Issuer        IssuerBinding  `json:"issuer"`
	Audience      string         `json:"audience"`
}

// NewCallerAttribution 使用服务端已验证的 source 与 issuer 围栏构造归因快照。
func NewCallerAttribution(taskID string, source ExecutionFence, issuer IssuerBinding) (CallerAttribution, error) {
	attribution := CallerAttribution{
		SchemaVersion: CallerAttributionSchemaVersion,
		SubjectKind:   CallerAttributionSubjectKindTask,
		TaskID:        taskID,
		Source:        source,
		Issuer:        issuer,
		Audience:      CallerAttributionAudience,
	}
	if err := attribution.Validate(); err != nil {
		return CallerAttribution{}, err
	}
	return attribution, nil
}

// Validate 严格校验固定 schema、task/source 绑定及完整 execution/issuer fence。
func (a CallerAttribution) Validate() error {
	if a.SchemaVersion != CallerAttributionSchemaVersion {
		return invalidCallerAttribution("schema_version must equal 1")
	}
	if a.SubjectKind != CallerAttributionSubjectKindTask {
		return invalidCallerAttribution("subject_kind must equal task")
	}
	if err := validateCallerAttributionText("task_id", a.TaskID); err != nil {
		return err
	}
	if err := a.Source.validate(); err != nil {
		return err
	}
	if a.TaskID != a.Source.AgentID {
		return invalidCallerAttribution("task_id must equal source.agent_id")
	}
	if err := a.Issuer.validate(); err != nil {
		return err
	}
	if a.Audience != CallerAttributionAudience {
		return invalidCallerAttribution("audience must equal mcp-orch/unified_v2")
	}
	return nil
}

// BindCallerAttribution 校验并一次性绑定服务端归因快照，已有值时拒绝覆盖。
func BindCallerAttribution(ctx context.Context, attribution CallerAttribution) (context.Context, error) {
	if ctx == nil {
		return nil, invalidCallerAttribution("context is nil")
	}
	if ctx.Value(callerAttributionContextKey{}) != nil {
		return ctx, invalidCallerAttribution("context attribution is already bound")
	}
	if err := attribution.Validate(); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, callerAttributionContextKey{}, attribution), nil
}

// RequireCallerAttribution 读取并复验服务端绑定的归因；缺失与非法值使用稳定哨兵区分。
func RequireCallerAttribution(ctx context.Context) (CallerAttribution, error) {
	if ctx == nil {
		return CallerAttribution{}, ErrCallerAttributionUnavailable
	}
	value := ctx.Value(callerAttributionContextKey{})
	if value == nil {
		return CallerAttribution{}, ErrCallerAttributionUnavailable
	}
	attribution, ok := value.(CallerAttribution)
	if !ok {
		return CallerAttribution{}, invalidCallerAttribution("context attribution has unexpected type")
	}
	if err := attribution.Validate(); err != nil {
		return CallerAttribution{}, err
	}
	return attribution, nil
}

// callerAttributionContextKey 是仅服务端 common 包可写入的 context key 类型。
type callerAttributionContextKey struct{}

// validate 校验 source execution fence 的文本、正代际和 canonical digest。
func (f ExecutionFence) validate() error {
	texts := []struct {
		name  string
		value string
	}{
		{name: "source.agent_id", value: f.AgentID},
		{name: "source.reservation_id", value: f.ReservationID},
		{name: "source.incarnation_id", value: f.IncarnationID},
		{name: "source.source_fence_ref", value: f.SourceFenceRef},
	}
	for _, text := range texts {
		if err := validateCallerAttributionText(text.name, text.value); err != nil {
			return err
		}
	}
	if f.ReservationGeneration <= 0 {
		return invalidCallerAttribution("source.reservation_generation must be positive")
	}
	if f.IncarnationGeneration <= 0 {
		return invalidCallerAttribution("source.incarnation_generation must be positive")
	}
	if f.BindingVersion <= 0 {
		return invalidCallerAttribution("source.binding_version must be positive")
	}
	return validateCallerAttributionDigest("source.binding_digest", f.BindingDigest)
}

// validate 校验 issuer 的关闭 kind、绑定引用、正代际和 canonical digest。
func (i IssuerBinding) validate() error {
	switch i.Kind {
	case CallerAttributionIssuerProviderBridge,
		CallerAttributionIssuerManagedRuntime,
		CallerAttributionIssuerA2AGateway:
	default:
		return invalidCallerAttribution("issuer.kind is not registered")
	}
	if err := validateCallerAttributionText("issuer.binding_ref", i.BindingRef); err != nil {
		return err
	}
	if i.Generation <= 0 {
		return invalidCallerAttribution("issuer.generation must be positive")
	}
	return validateCallerAttributionDigest("issuer.digest", i.Digest)
}

// validateCallerAttributionText 拒绝空值、非 canonical 边界、控制字符和无界文本。
func validateCallerAttributionText(name, value string) error {
	if value == "" || len(value) > callerAttributionTextLimit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return invalidCallerAttribution(name + " must be bounded canonical text")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return invalidCallerAttribution(name + " must be bounded canonical text")
		}
	}
	return nil
}

// validateCallerAttributionDigest 只接受 64 字节小写十六进制 SHA-256 文本。
func validateCallerAttributionDigest(name, digest string) error {
	if len(digest) != callerAttributionDigestSize {
		return invalidCallerAttribution(name + " must be lowercase sha256 hex")
	}
	for _, char := range digest {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return invalidCallerAttribution(name + " must be lowercase sha256 hex")
		}
	}
	return nil
}

// invalidCallerAttribution 保留稳定 sentinel，同时给服务端测试提供具体拒绝原因。
func invalidCallerAttribution(reason string) error {
	return fmt.Errorf("%w: %s", ErrCallerAttributionInvalid, reason)
}
