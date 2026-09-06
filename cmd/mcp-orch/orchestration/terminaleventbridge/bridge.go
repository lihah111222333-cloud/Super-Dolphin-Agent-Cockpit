// Package terminaleventbridge contains an unregistered candidate bridge for
// provider terminal observations. It deliberately has no service, Fx, or
// provider dependency and cannot become a second canonical event owner.
package terminaleventbridge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	runtimeeventwriter "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/runtimeeventwriter"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

var (
	ErrUnavailable    = errors.New("terminal event bridge is unavailable")
	ErrInvalid        = errors.New("terminal event bridge observation is invalid")
	ErrDuplicate      = errors.New("terminal event bridge observation is duplicate")
	ErrLateGeneration = errors.New("terminal event bridge observation is from a late generation")
	ErrGap            = errors.New("terminal event bridge observation has a source gap")
	ErrOutcomeUnknown = errors.New("terminal event bridge outcome is unknown")
)

const (
	EventTurnCompleted   = "turn.completed"
	EventTurnInterrupted = "turn.interrupted"
)

// Binding is the authenticated execution tuple required to attribute a
// provider observation. Bridge code never infers any member from a DTO.
type Binding = runtimeeventwriter.Binding

// BindingVerifier 保留旧的非原子检查形状，仅用于识别不合格调用方；它
// 不能满足 New 的 transaction-bound 要求，也不会被 Submit 调用。
type BindingVerifier interface {
	VerifyTerminalBinding(context.Context, Binding) error
}

// BindingVerifierTx 是事务内完整 binding 复核端口的别名。
type BindingVerifierTx = runtimeeventwriter.BindingVerifierTx

// Observation is a fully bound terminal provider observation. Event ID,
// writer epoch, trust class and content role are intentionally mandatory.
type Observation struct {
	Binding     Binding         `json:"binding"`
	EventID     string          `json:"event_id"`
	EventType   string          `json:"event_type"`
	Lane        eventstore.Lane `json:"lane"`
	WriterEpoch string          `json:"writer_epoch"`
	Provenance  string          `json:"provenance"`
	TrustClass  string          `json:"trust_class"`
	ContentRole string          `json:"content_role"`
	ContentRef  string          `json:"content_ref"`
	ContentSize int64           `json:"content_size"`
	MediaType   string          `json:"media_type"`
	ProviderAt  *string         `json:"provider_at,omitempty"`
	ObservedAt  string          `json:"observed_at"`
}

// Bridge maps only explicitly bound observations to CandidateIngressAdapter.
// It is candidate-only: construction is opt-in and no Fx/public registry uses
// this type.
type Bridge struct {
	ingress  runtimeeventwriter.BoundIngress
	verifier runtimeeventwriter.BindingVerifierTx
}

var _ runtimeeventwriter.Ingress = (*runtimeeventwriter.CandidateIngressAdapter)(nil)

// New 构造包裹既有 candidate ingress adapter 的桥；缺 verifier 时返回 typed unavailable。
func New(ingress any, verifier any) (*Bridge, error) {
	bound, ok := ingress.(runtimeeventwriter.BoundIngress)
	if !ok || isNil(bound) {
		return nil, fmt.Errorf("%w: atomic bound ingress is required", ErrUnavailable)
	}
	if capability, ok := ingress.(interface{ AtomicBindingAvailable() bool }); ok && !capability.AtomicBindingAvailable() {
		return nil, fmt.Errorf("%w: wrapped ingress has no atomic binding seam", ErrUnavailable)
	}
	txVerifier, ok := verifier.(runtimeeventwriter.BindingVerifierTx)
	if !ok || isNil(txVerifier) {
		return nil, fmt.Errorf("%w: transaction-bound durable binding verifier is required", ErrUnavailable)
	}
	return &Bridge{ingress: bound, verifier: txVerifier}, nil
}

// Submit 校验归因后委托一次 observation；canonical materialization 与提交后通知仍由下层 owner 负责。
func (b *Bridge) Submit(ctx context.Context, observation Observation) (eventstore.AppendResult, error) {
	if b == nil || isNil(b.ingress) || isNil(b.verifier) {
		return eventstore.AppendResult{}, ErrUnavailable
	}
	if ctx == nil {
		return eventstore.AppendResult{}, fmt.Errorf("%w: context is required", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := observation.validate(); err != nil {
		return eventstore.AppendResult{}, err
	}
	event := mapRuntimeEvent(observation)
	result, err := b.ingress.AppendBound(ctx, event, b.verifier)
	if err != nil {
		return result, classify(err)
	}
	if err := ensureMaterialized(result); err != nil {
		return eventstore.AppendResult{}, err
	}
	return result, nil
}

// ensureMaterialized 只允许 canonical EventStore 已物化的结果继续向通知层传播。
func ensureMaterialized(result eventstore.AppendResult) error {
	if err := runtimeeventwriter.ValidateMaterializedResult(result); err != nil {
		return errors.Join(ErrOutcomeUnknown, err)
	}
	return nil
}

// validate 严格校验 terminal observation 的事件类型、lane、身份和元数据。
func (o Observation) validate() error {
	if o.EventType != EventTurnCompleted && o.EventType != EventTurnInterrupted {
		return fmt.Errorf("%w: unsupported terminal event type %q", ErrInvalid, o.EventType)
	}
	if o.Lane != eventstore.LaneOutput && o.Lane != eventstore.LaneControl {
		return fmt.Errorf("%w: terminal lane must be output or control", ErrInvalid)
	}
	if err := validateBinding(o.Binding); err != nil {
		return err
	}
	return validateObservationFields(o)
}

// validateBinding 校验 terminal observation 的完整执行代际绑定。
func validateBinding(b Binding) error {
	if b.IncarnationGeneration == 0 || b.LaunchSequence == 0 {
		return fmt.Errorf("%w: incarnation generation and launch sequence must be positive", ErrInvalid)
	}
	for name, value := range map[string]string{"agent_id": b.AgentID, "incarnation_id": b.IncarnationID, "provider": b.Provider, "provider_session_id": b.ProviderSessionID} {
		if err := validateText(name, value, 128, false); err != nil {
			return err
		}
	}
	return nil
}

// validateObservationFields 校验事件身份、writer epoch、trust/content 元数据。
func validateObservationFields(o Observation) error {
	if err := validateObservationText(o); err != nil {
		return err
	}
	if err := validateObservedTimes(o); err != nil {
		return err
	}
	return validateContentBounds(o)
}

// validateObservationText 校验事件身份、writer epoch 与 trust/content 元数据。
func validateObservationText(o Observation) error {
	for name, value := range map[string]string{
		"event_id":     o.EventID,
		"writer_epoch": o.WriterEpoch,
		"provenance":   o.Provenance, "trust_class": o.TrustClass,
		"content_role": o.ContentRole,
	} {
		limit := observationTextLimit(name)
		if err := validateText(name, value, limit, false); err != nil {
			return err
		}
	}
	return nil
}

func observationTextLimit(name string) int {
	if name == "provenance" {
		return 256
	}
	if name == "trust_class" || name == "content_role" {
		return 64
	}
	return 128
}

// validateObservedTimes 校验 observation 与 provider timestamp 均为 UTC。
func validateObservedTimes(o Observation) error {
	if err := validateText("observed_at", o.ObservedAt, 64, false); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, o.ObservedAt)
	if err != nil || parsed.Location() != time.UTC {
		return fmt.Errorf("%w: observed_at must be RFC3339 UTC", ErrInvalid)
	}
	if o.ProviderAt != nil {
		if err := validateText("provider_at", *o.ProviderAt, 64, false); err != nil {
			return err
		}
		providerAt, err := time.Parse(time.RFC3339Nano, *o.ProviderAt)
		if err != nil || providerAt.Location() != time.UTC {
			return fmt.Errorf("%w: provider_at must be RFC3339 UTC", ErrInvalid)
		}
	}
	return nil
}

// validateContentBounds 校验正文引用与大小上限，不读取正文内容。
func validateContentBounds(o Observation) error {
	if o.ContentRef != "" {
		if err := validateText("content_ref", o.ContentRef, 512, false); err != nil {
			return err
		}
	}
	if o.MediaType != "" {
		if err := validateText("media_type", o.MediaType, 128, false); err != nil {
			return err
		}
	}
	if o.ContentSize < 0 || o.ContentSize > 256*1024 {
		return fmt.Errorf("%w: content size is outside bounded range", ErrInvalid)
	}
	return nil
}

// validateText 校验桥接字段的 UTF-8、长度、空白和控制字符约束。
func validateText(name, value string, limit int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s is not canonical bounded UTF-8 text", ErrInvalid, name)
	}
	return nil
}

// mapRuntimeEvent 将已验证 observation 映射为 runtimeeventwriter 事件，不生成身份或序列。
func mapRuntimeEvent(observation Observation) runtimeeventwriter.RuntimeEvent {
	event := runtimeeventwriter.RuntimeEvent{Binding: observation.Binding, AgentID: observation.Binding.AgentID, Lane: observation.Lane, EventID: observation.EventID, EventType: observation.EventType, Provenance: observation.Provenance, TrustClass: observation.TrustClass, ContentRole: observation.ContentRole, ContentRef: observation.ContentRef, ContentSize: observation.ContentSize, MediaType: observation.MediaType, ProviderAt: cloneStringPtr(observation.ProviderAt), ObservedAt: observation.ObservedAt, WriterEpoch: observation.WriterEpoch}
	return event
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

// AdaptTurnCompleted 明确保持 disabled：existing DTO 缺少
// incarnation, provider-session, launch-sequence, writer-epoch, trust and
// content-role fields required by this bridge. Guessing them would authorize
// an unbound terminal event.
func AdaptTurnCompleted(turndto.TurnCompleted) (Observation, error) {
	return Observation{}, fmt.Errorf("%w: TurnCompleted DTO lacks authenticated binding and ingress trust fields", ErrUnavailable)
}

// AdaptTurnInterrupted 与 AdaptTurnCompleted 共享同一 fail-closed 边界。
func AdaptTurnInterrupted(turndto.TurnInterrupted) (Observation, error) {
	return Observation{}, fmt.Errorf("%w: TurnInterrupted DTO lacks authenticated binding and ingress trust fields", ErrUnavailable)
}

func classify(err error) error {
	switch {
	case errors.Is(err, runtimeeventwriter.ErrStale), errors.Is(err, eventstore.ErrEventFence), errors.Is(err, taskeventsource.ErrRuntimeEventCandidateUnavailable):
		return errors.Join(ErrLateGeneration, err)
	case errors.Is(err, runtimeeventwriter.ErrGap), errors.Is(err, eventstore.ErrEventGap), errors.Is(err, taskeventsource.ErrRuntimeEventTxCandidateGap):
		return errors.Join(ErrGap, err)
	case errors.Is(err, runtimeeventwriter.ErrCommitUnknown), errors.Is(err, runtimeeventwriter.ErrIngressAdapterCommitUnknown), errors.Is(err, eventstore.ErrTransactionCommitUnknown), errors.Is(err, taskeventsource.ErrRuntimeEventTxCandidateCommitUnknown):
		return errors.Join(ErrOutcomeUnknown, err)
	case errors.Is(err, eventstore.ErrEventConflict):
		return errors.Join(ErrDuplicate, err)
	default:
		return err
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
