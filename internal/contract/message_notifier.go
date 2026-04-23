package contract

import (
	"context"
	"errors"
)

// MessageNotifier is the external-webhook egress facade. Callers push a
// pre-rendered NotifyRequest referencing a channel alias; the
// implementation resolves the alias (via NotifyConfig) and enqueues the
// HTTPS POST onto its flush worker.
//
// This contract is deliberately narrower than the three existing notify
// surfaces (ToolNotifier / PushBridge / platform/rpc push) so the three
// fanout models stay separately reasoned about. In particular:
//   * TryEnqueue is non-blocking. Callback-style subscribers on a bus
//     can call it directly without risking backpressure onto publish.
//   * Implementations own retry / rate-limit / timeout policy. Callers
//     must not assume delivery success from a nil return.
//   * Secrets (webhook URL / HMAC key) never flow through NotifyRequest;
//     callers pass only the alias and the resolver looks the secret up.
type MessageNotifier interface {
	TryEnqueue(ctx context.Context, req NotifyRequest) error
}

// ErrNotifyQueueFull is returned when TryEnqueue cannot accept the
// request because the implementation's bounded queue is full. The
// caller must not retry in-line (the queue being full implies the
// flush worker is under pressure); it should drop the signal and let
// the next terminal event fire a fresh enqueue.
var ErrNotifyQueueFull = errors.New("notify: queue full")

// ErrNotifyAliasNotFound is returned when the channel alias is not
// configured in NotifyConfig. Explicit so callers can distinguish
// misconfiguration from a transient queue issue.
var ErrNotifyAliasNotFound = errors.New("notify: channel alias not found")

// NotifyRequest is the wire-level request a bus subscriber (or any
// other producer) pushes onto the notifier. The implementation renders
// the Message via the channel's platform template and signs / transmits
// the resulting body.
type NotifyRequest struct {
	// ChannelAlias is the stable key looked up in NotifyConfig. Empty
	// string is rejected so accidental silent drops do not occur; the
	// caller is expected to apply its own alias resolution policy
	// (for example cron job_row.notify_channel) before enqueue.
	ChannelAlias string
	// Message is the fully-constructed payload — rendering rules for
	// each platform still apply (mention suppression, length clamp,
	// markdown escaping) but the content itself is the caller's
	// responsibility. Never feed raw provider events here.
	Message NotifyMessage
}

// NotifyMessage is the platform-agnostic body. Title / Body are
// markdown-safe strings; Level tags severity so platform renderers can
// pick a color / icon. Attachments is intentionally omitted in v1 to
// keep the external-egress attack surface tight.
type NotifyMessage struct {
	Title string
	Body  string
	Level NotifyLevel
}

// NotifyLevel classifies severity. Platform renderers map the level
// onto their respective visual cues (Dingtalk markdown color, Feishu
// card header color, Slack block formatting).
type NotifyLevel string

const (
	NotifyLevelInfo  NotifyLevel = "info"
	NotifyLevelWarn  NotifyLevel = "warn"
	NotifyLevelError NotifyLevel = "error"
)
