package taskeventsource

import (
	"context"
	"errors"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

// Appender 是 canonical EventStore 的唯一写入口。
// Producer 不接受 event bus、legacy projection 或 provider callback，避免产生第二个事件 owner。
type Appender interface {
	Append(context.Context, eventstore.IngressFrame) (eventstore.AppendResult, error)
}

var _ Appender = (*eventstore.Store)(nil)

// EventInput 是可信 observation 到 canonical envelope 的显式映射。
// 三个 identity 快照必须来自同一次 server-side owner 读取；任何字段都不会从
// thread、cwd、tool 参数或 provider metadata 推导。EnvelopeHash 由上游 canonicalizer
// 提供，Producer 不自行替代 canonicalization。
type EventInput struct {
	Reservation identity.Reservation
	Incarnation identity.Incarnation
	Binding     identity.CurrentBinding

	Lane           eventstore.Lane
	SourceSequence int64
	EnvelopeHash   string

	EventID        string
	EventType      string
	SubjectAgentID string
	Provenance     string
	TrustClass     string
	ContentRole    string
	ContentRef     string
	ContentSize    int64
	MediaType      string
	ProviderAt     *string
	ObservedAt     string
	WriterEpoch    string
}

// Producer 是未接入运行时的 bounded candidate；调用者仍须显式选择何时生产事件。
// 它只把已绑定 task identity 适配到 EventStore，不发布事件、不分配序列、不写其它表。
type Producer struct {
	appender Appender
}

// TaskEventProducer 是 canonical task event producer 的最小调用端口。
//
// 该接口只暴露显式 identity snapshot 到 event append 的适配操作；它不提供
// provider、event bus、序列分配或其它任务的读取/写入能力。
type TaskEventProducer interface {
	Append(context.Context, EventInput) (eventstore.AppendResult, error)
}

// BuildFrame 将 canonical identity 快照映射为事务写入所需的 ingress frame。
// 返回 frame 仍须通过 eventstore.IngressFrame.Validate 才能触达 canonical owner。
func BuildFrame(source Source, input EventInput) (eventstore.IngressFrame, error) {
	return buildFrame(source, input)
}

// ValidateIdentitySnapshots 校验生产写入方在同一事务中读取的跨表 identity fence。
func ValidateIdentitySnapshots(input EventInput) (Source, error) {
	return validateIdentitySnapshots(input)
}

var _ TaskEventProducer = (*Producer)(nil)

// NewProducer 创建候选 producer；缺少 canonical appender 时立即失败。
func NewProducer(appender Appender) (*Producer, error) {
	if appender == nil || isNilAppender(appender) {
		return nil, errors.New("task event producer requires canonical event appender")
	}
	return &Producer{appender: appender}, nil
}

// isNilAppender 拒绝 interface 中携带的 typed-nil appender，避免运行期 panic。
func isNilAppender(appender Appender) bool {
	value := reflect.ValueOf(appender)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Append 映射并提交一个已绑定 incarnation 的 canonical task event。
// 任何 identity 交叉检查、source/cursor 构造或 envelope 校验失败都会在 appender
// 被调用前返回；EventStore 自身仍会重读 durable fence 并执行 dense dedup/gap 状态机。
func (p *Producer) Append(ctx context.Context, input EventInput) (eventstore.AppendResult, error) {
	if p == nil || p.appender == nil || isNilAppender(p.appender) {
		return eventstore.AppendResult{}, errors.New("task event producer dependencies are missing")
	}
	if ctx == nil {
		return eventstore.AppendResult{}, errors.New("task event producer context is required")
	}
	source, err := validateIdentitySnapshots(input)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := validateSubjectAgent(input); err != nil {
		return eventstore.AppendResult{}, err
	}
	frame, err := buildFrame(source, input)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := frame.Validate(); err != nil {
		return eventstore.AppendResult{}, err
	}
	return p.appender.Append(ctx, frame)
}

// validateSubjectAgent 限制此候选只生产当前 task 自身事件，不把 subject metadata 当作路由。
func validateSubjectAgent(input EventInput) error {
	if input.SubjectAgentID != input.Binding.AgentID {
		return invalidSource("subject_agent_id must equal bound task agent")
	}
	return nil
}

// buildFrame 由可信 identity 快照构造唯一 source/cursor/envelope，不分配序列或写其它 owner。
func buildFrame(source Source, input EventInput) (eventstore.IngressFrame, error) {
	cursor, err := source.Cursor(input.Lane, input.SourceSequence)
	if err != nil {
		return eventstore.IngressFrame{}, err
	}
	incarnationID := input.Incarnation.IncarnationID
	envelope := eventstore.EventEnvelope{
		EventID: input.EventID, EventType: input.EventType, SchemaVersion: 1,
		RootAgentID: input.Reservation.AgentID, SubjectAgentID: input.SubjectAgentID,
		ReservationID: input.Reservation.ReservationID, IncarnationID: &incarnationID,
		ExternalEpoch: input.Incarnation.ExternalEpoch, IncarnationGeneration: input.Incarnation.IncarnationGeneration,
		IdentityDigest: input.Incarnation.IdentityDigest, BindingVersion: input.Binding.BindingVersion,
		BindingDigest: input.Binding.BindingDigest, SourceFenceRef: input.Binding.SourceFenceRef,
		Provenance: input.Provenance, TrustClass: input.TrustClass, ContentRole: input.ContentRole,
		ContentRef: input.ContentRef, ContentSize: input.ContentSize, MediaType: input.MediaType,
		ProviderAt: cloneStringPtr(input.ProviderAt), ObservedAt: input.ObservedAt, WriterEpoch: input.WriterEpoch,
	}
	return eventstore.IngressFrame{
		SourceID: source.SourceID, SourceEpoch: source.SourceEpoch, Lane: input.Lane,
		SourceSequence: input.SourceSequence, EnvelopeHash: input.EnvelopeHash,
		CanonicalCursor: cursor, Envelope: envelope,
	}, nil
}

// validateIdentitySnapshots 交叉校验 reservation、incarnation 与 active binding 的固定关系。
func validateIdentitySnapshots(input EventInput) (Source, error) {
	if input.Reservation.SchemaVersion != 1 {
		return Source{}, invalidSource("reservation schema_version must be 1")
	}
	if input.Incarnation.SchemaVersion != 1 {
		return Source{}, invalidSource("incarnation schema_version must be 1")
	}
	source, err := FromCurrentBinding(input.Binding)
	if err != nil {
		return Source{}, err
	}
	if err := validateReservationBinding(input.Reservation, input.Binding); err != nil {
		return Source{}, err
	}
	if err := validateIncarnationBinding(input.Reservation, input.Incarnation, input.Binding); err != nil {
		return Source{}, err
	}
	return source, nil
}

// validateReservationBinding 校验 reservation 对 active binding 的逻辑身份与独立代际承诺。
func validateReservationBinding(reservation identity.Reservation, binding identity.CurrentBinding) error {
	if reservation.State != "activated" || reservation.ReservationID != binding.ReservationID ||
		reservation.AgentID != binding.AgentID || reservation.IdentityKind != binding.IdentityKind ||
		reservation.SourceFenceRef != binding.SourceFenceRef ||
		reservation.ReservationGeneration <= 0 || binding.ReservationGeneration <= 0 ||
		reservation.ReservationGeneration != binding.ReservationGeneration {
		return invalidSource("reservation and current binding fence mismatch")
	}
	return nil
}

// validateIncarnationBinding 校验不可变 incarnation 与当前 active binding 的逐字段一致性。
func validateIncarnationBinding(reservation identity.Reservation, incarnation identity.Incarnation, binding identity.CurrentBinding) error {
	if err := validateIncarnationIdentity(reservation, incarnation, binding); err != nil {
		return err
	}
	if incarnation.SourceFenceRef != binding.SourceFenceRef {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if err := validateIncarnationGeneration(incarnation, binding); err != nil {
		return err
	}
	if incarnation.ExternalIdentityRef != binding.ExternalIdentityRef {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	return nil
}

// validateIncarnationIdentity 校验 incarnation 与 reservation/binding 的固定身份字段。
func validateIncarnationIdentity(reservation identity.Reservation, incarnation identity.Incarnation, binding identity.CurrentBinding) error {
	if incarnation.IncarnationID != binding.IncarnationID {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if incarnation.ReservationID != reservation.ReservationID {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if incarnation.AgentID != binding.AgentID {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if incarnation.IdentityKind != reservation.IdentityKind {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if incarnation.IdentityKind != binding.IdentityKind {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	return nil
}

// validateIncarnationGeneration 校验 incarnation 与 active binding 的代际围栏。
func validateIncarnationGeneration(incarnation identity.Incarnation, binding identity.CurrentBinding) error {
	if incarnation.IncarnationGeneration <= 0 {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if binding.IncarnationGeneration <= 0 {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	if incarnation.IncarnationGeneration != binding.IncarnationGeneration {
		return invalidSource("incarnation and current binding fence mismatch")
	}
	return nil
}

// cloneStringPtr 复制可选 provider 时间，避免 frame 持有调用方可变指针。
func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
