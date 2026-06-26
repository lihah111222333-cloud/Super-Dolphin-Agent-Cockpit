package contract

import (
	"context"
	"errors"
)

// MessageNotifier 是外部 webhook 出站通知的窄门面。
// 调用方只提交已渲染消息和 channel alias；实现负责解析 NotifyConfig、查找 secret、
// 非阻塞入队并在 flush worker 中执行 HTTPS POST。nil error 仅表示成功入队，不代表送达成功。
type MessageNotifier interface {
	TryEnqueue(ctx context.Context, req NotifyRequest) error
}

// ErrNotifyQueueFull 表示 bounded queue 已满，TryEnqueue 无法接收请求。
// 调用方不能同步重试压垮发布路径，应丢弃本次信号并等待下一次终态事件。
var ErrNotifyQueueFull = errors.New("notify: queue full")

// ErrNotifyAliasNotFound 表示 channel alias 未在 NotifyConfig 中配置。
// 调用方可据此区分配置错误和瞬时队列压力。
var ErrNotifyAliasNotFound = errors.New("notify: channel alias not found")

// NotifyRequest 是 bus subscriber 或其他生产者提交给 notifier 的 wire 请求。
// 请求不携带 webhook URL/HMAC 等 secret，只携带 alias 和平台无关消息体。
type NotifyRequest struct {
	// ChannelAlias 是 NotifyConfig 中的稳定键；空值必须被拒绝，避免静默丢通知。
	ChannelAlias string
	// Message 是调用方构造好的平台无关内容；不要直接传 raw provider event。
	Message NotifyMessage
}

// NotifyMessage 是平台无关的通知正文。
// Title/Body 应由调用方处理成可安全渲染文本；Level 供平台渲染器选择颜色或图标。
type NotifyMessage struct {
	Title string
	Body  string
	Level NotifyLevel
}

// NotifyLevel 是外部通知的严重级别枚举。
// 各平台渲染器把它映射到自己的视觉提示。
type NotifyLevel string

const (
	NotifyLevelInfo  NotifyLevel = "info"
	NotifyLevelWarn  NotifyLevel = "warn"
	NotifyLevelError NotifyLevel = "error"
)
