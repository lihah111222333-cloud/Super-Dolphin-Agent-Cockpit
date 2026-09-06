package unifiedv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	toolspkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

var (
	// ErrSendDisabled 表示 send_message 尚未由 canonical coordinator 装配。
	ErrSendDisabled = errors.New("unified_v2 send_message is disabled")
	// ErrSendUnavailable 表示 send_message 调用缺少可信 transport 身份。
	ErrSendUnavailable = errors.New("unified_v2 send_message is unavailable")
	// ErrUnifiedSendConflict 表示同一幂等键绑定了不同请求。
	ErrUnifiedSendConflict = errors.New("unified_v2 send_message idempotency conflict")
	// ErrUnifiedSendIndeterminate 表示发送效果需要按原操作继续查询。
	ErrUnifiedSendIndeterminate = errors.New("unified_v2 send_message submission is indeterminate")
)

// SendTarget 是严格 send_message 请求的 grant 目标判别联合。
type SendTarget struct {
	// Kind 固定为 grant，不接受 agent 或 thread 标识。
	Kind string `json:"kind"`
	// GrantRef 是服务端预先签发的 opaque grant 引用。
	GrantRef string `json:"grant_ref"`
}

// SendMessage 是 content.single 文本消息 DTO。
type SendMessage struct {
	// Kind 固定为 content.single。
	Kind string `json:"kind"`
	// Text 是受限长度且 UTF-8 有效的正文。
	Text string `json:"text"`
}

// Message 是 SendMessage 的兼容语义别名。
type Message = SendMessage

// BusyPolicy 固定 busy 时的 fail 策略。
type BusyPolicy struct {
	// Action 固定为 fail，禁止排队或隐式中断。
	Action string `json:"action"`
}

// SendDelivery 是严格 new_turn 投递判别联合。
type SendDelivery struct {
	// Kind 固定为 new_turn。
	Kind string `json:"kind"`
	// BusyPolicy 描述目标忙时的明确处理方式。
	BusyPolicy BusyPolicy `json:"busy_policy"`
}

// Delivery 是 SendDelivery 的兼容语义别名。
type Delivery = SendDelivery

// SendInput 是 strict unified_v2 send_message 的完整请求 DTO。
type SendInput struct {
	// Target 只能引用预先签发的 grant。
	Target SendTarget `json:"target"`
	// Message 只能是单一 content.single 文本。
	Message SendMessage `json:"message"`
	// Delivery 显式指定 new_turn/fail 策略。
	Delivery SendDelivery `json:"delivery"`
	// IdempotencyKey 是调用方提供的有界重放键。
	IdempotencyKey string `json:"idempotency_key"`
}

// SendMessageInput 是 SendInput 的兼容语义别名。
type SendMessageInput = SendInput

// SendMessageRequest 是 SendInput 的兼容语义别名。
type SendMessageRequest = SendInput

// SendResult 是 coordinator 返回的 non-authorizing 状态摘要。
type SendResult struct {
	// Status 只能是 accepted、aborted、indeterminate 或 unknown。
	Status string `json:"status"`
	// CommandID 是 canonical OUT owner 生成的命令引用。
	CommandID string `json:"command_id,omitempty"`
	// RequestDigest 绑定原始请求和完整身份围栏。
	RequestDigest string `json:"request_digest,omitempty"`
	// SubmissionFence 是待查询的原操作围栏摘要。
	SubmissionFence string `json:"submission_fence,omitempty"`
	// ReceiptRef 是 OUT/provider owner 返回的 opaque receipt 引用。
	ReceiptRef string `json:"receipt_ref,omitempty"`
}

// UnifiedSendCoordinator 唯一拥有幂等、CONTENT/RESOURCE、custody 和 OUT/provider receipt 原子编排。
type UnifiedSendCoordinator interface {
	// Dispatch 在一个 canonical owner 内执行完整 send 生命周期。
	Dispatch(context.Context, common.CallerPrincipal, common.CallerAttribution, SendInput) (SendResult, error)
}

// SendHandler 是未注册的 strict unified_v2 send_message 候选处理器。
type SendHandler struct{ coordinator UnifiedSendCoordinator }

// NewSend 保留旧构造名但始终禁用未证明原子性的六端口路径。
func NewSend(...any) (*SendHandler, error) { return nil, ErrSendDisabled }

// NewSendWithCoordinator 构造唯一可执行的候选处理器。
func NewSendWithCoordinator(c UnifiedSendCoordinator) (*SendHandler, error) {
	if isNil(c) {
		return nil, ErrSendDisabled
	}
	return &SendHandler{coordinator: c}, nil
}

// NewSendMessage 保留旧名称并保持禁用。
func NewSendMessage(...any) (*SendHandler, error) { return NewSend() }

// ToolHandler 返回未注册候选处理函数。
func (h *SendHandler) ToolHandler() toolspkg.ToolHandler {
	return func(c context.Context, r json.RawMessage) (any, error) { return h.Handle(c, r) }
}

// Handle 仅取得 transport 身份并严格解码，副作用全部由 coordinator 拥有。
func (h *SendHandler) Handle(c context.Context, r json.RawMessage) (any, error) {
	p, a, in, e := guardSendInput(h, c, r)
	if e != nil {
		return nil, e
	}
	out, e := h.coordinator.Dispatch(c, p, a, in)
	if e != nil {
		return out, e
	}
	if out.Status == "accepted" && (out.CommandID == "" || out.RequestDigest == "" || out.ReceiptRef == "") {
		return SendResult{Status: "unknown"}, ErrUnifiedSendIndeterminate
	}
	switch out.Status {
	case "accepted", "indeterminate", "aborted", "unknown":
		return out, nil
	}
	return SendResult{Status: "unknown"}, ErrUnifiedSendIndeterminate
}

// guardSendInput 在 owner 访问前完成 coordinator、principal、attribution 和解码检查。
func guardSendInput(h *SendHandler, c context.Context, r json.RawMessage) (common.CallerPrincipal, common.CallerAttribution, SendInput, error) {
	if h == nil || isNil(h.coordinator) {
		return common.CallerPrincipal{}, common.CallerAttribution{}, SendInput{}, ErrSendDisabled
	}
	if c == nil {
		return common.CallerPrincipal{}, common.CallerAttribution{}, SendInput{}, ErrSendUnavailable
	}
	p, e := common.RequireCallerPrincipal(c)
	if e != nil {
		return p, common.CallerAttribution{}, SendInput{}, fmt.Errorf("%w: %w", ErrSendUnavailable, e)
	}
	if strings.EqualFold(p.Profile, "operator") {
		return common.CallerPrincipal{}, common.CallerAttribution{}, SendInput{}, fmt.Errorf("%w: operator principal has no task attribution", ErrSendUnavailable)
	}
	a, e := common.RequireCallerAttribution(c)
	if e != nil {
		return p, a, SendInput{}, fmt.Errorf("%w: %w", ErrSendUnavailable, e)
	}
	in, e := decodeSendInput(r)
	return p, a, in, e
}

// decodeSendInput 拒绝未知、重复、null、尾随和非规范 JSON。
func decodeSendInput(r json.RawMessage) (SendInput, error) {
	t := bytes.TrimSpace(r)
	if len(t) == 0 || t[0] != '{' || bytes.Equal(t, []byte("null")) || !bytes.Equal(r, t) {
		return SendInput{}, fmt.Errorf("%w: canonical object required", orchestrationcommand.ErrInvalidIntent)
	}
	if e := scanSend(t); e != nil {
		return SendInput{}, e
	}
	var in SendInput
	if e := json.Unmarshal(t, &in); e != nil {
		return SendInput{}, e
	}
	if !validSendFields(in) {
		return SendInput{}, fmt.Errorf("%w: invalid send input", orchestrationcommand.ErrInvalidIntent)
	}
	b, _ := json.Marshal(in)
	if !bytes.Equal(t, b) {
		return SendInput{}, fmt.Errorf("%w: noncanonical JSON", orchestrationcommand.ErrInvalidIntent)
	}
	return in, nil
}

// validSendFields 校验固定 kind、正文大小和必需幂等键。
func validSendFields(in SendInput) bool {
	return in.Target.Kind == "grant" && txt(in.Target.GrantRef) && in.Message.Kind == "content.single" && len(in.Message.Text) >= 1 && len(in.Message.Text) <= 65536 && utf8.ValidString(in.Message.Text) && in.Delivery.Kind == "new_turn" && in.Delivery.BusyPolicy.Action == "fail" && txt(in.IdempotencyKey)
}

// validateSendInput 将固定字段谓词包装为测试与未来 owner 共用的稳定错误接口。
func validateSendInput(in SendInput) error {
	if !validSendFields(in) {
		return fmt.Errorf("%w: invalid send input", orchestrationcommand.ErrInvalidIntent)
	}
	return nil
}

func txt(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && len(s) <= 512
}
func scanSend(r []byte) error {
	d := json.NewDecoder(bytes.NewReader(r))
	if e := scanSendValue(d); e != nil {
		return e
	}
	var x any
	if e := d.Decode(&x); !errors.Is(e, io.EOF) {
		return errors.New("trailing JSON payload")
	}
	return nil
}

// scanSendValue 递归拒绝 null 并检查嵌套结构。
func scanSendValue(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if t == nil {
		return errors.New("null values are not allowed")
	}
	if x, ok := t.(json.Delim); ok {
		if x == '{' {
			return scanSendObject(d)
		}
		if x == '[' {
			return scanSendArray(d)
		}
	}
	return nil
}
func scanSendObject(d *json.Decoder) error {
	s := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return e
		}
		n := k.(string)
		if s[n] {
			return fmt.Errorf("duplicate field %q", n)
		}
		s[n] = true
		if e := scanSendValue(d); e != nil {
			return e
		}
	}
	_, e := d.Token()
	return e
}
func scanSendArray(d *json.Decoder) error {
	for d.More() {
		if e := scanSendValue(d); e != nil {
			return e
		}
	}
	_, e := d.Token()
	return e
}

// SendDefinition 返回未来 manifest 的严格 schema；该定义本身不改变 registry。
func SendDefinition() toolspkg.ToolDefinition {
	grantRef := toolspkg.StringSchema("Server-issued opaque grant reference.")
	grantRef["minLength"], grantRef["maxLength"] = 1, 512
	text := toolspkg.StringSchema("UTF-8 content.single text.")
	text["minLength"], text["maxLength"] = 1, 65536
	idempotencyKey := toolspkg.StringSchema("Bounded caller-provided replay key.")
	idempotencyKey["minLength"], idempotencyKey["maxLength"] = 1, 512
	target := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"kind":      toolspkg.EnumStringSchema("Target discriminator.", "grant"),
		"grant_ref": grantRef,
	}, "kind", "grant_ref")
	message := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"kind": toolspkg.EnumStringSchema("Message discriminator.", "content.single"),
		"text": text,
	}, "kind", "text")
	busyPolicy := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"action": toolspkg.EnumStringSchema("Busy-target action.", "fail"),
	}, "action")
	delivery := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"kind":        toolspkg.EnumStringSchema("Delivery discriminator.", "new_turn"),
		"busy_policy": busyPolicy,
	}, "kind", "busy_policy")
	return toolspkg.ToolDefinition{Name: "send_message", Description: "Submit a caller-authorized content.single turn.", InputSchema: toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"target":          target,
		"message":         message,
		"delivery":        delivery,
		"idempotency_key": idempotencyKey,
	}, "target", "message", "delivery", "idempotency_key")}
}
