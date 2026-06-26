package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	notifyplatform "github.com/anthropic-ai/super-agent-v3/internal/platform/notify"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module 装配 mcp-orch 侧通知栈。
// 它复用 internal/platform/notify 的 Resolver、Notifier、Flusher 和 WebhookClient，
// 但实例绑定到 orch 自己的 dispatcher 与 runners group，避免编排进程 import core 通知模块。
//
// Provides:
//   - notifyplatform.Resolver：解析 NotifyConfig，JSON 错误在启动期 fail-fast。
//   - *notifyplatform.WebhookClient：按配置应用 AllowPrivateCIDR 和 Timeout。
//   - *notifyplatform.Notifier：同时暴露为 contract.MessageNotifier 供订阅器注入。
//   - *notifyplatform.Flusher：放入 group:"runners"，由 RunGroup 管理 drain 生命周期。
//   - *DAGNotifier 与它的 Subscribe 生命周期钩子。
var Module = fx.Module("orch-notify",
	fx.Provide(
		provideOrchResolver,
		provideOrchWebhookClient,
		provideOrchNotifier,
		provideMessageNotifier,
		provideOrchFlusher,
		NewDAGNotifier,
		provideAgentAliasResolver,
		provideTurnNotifier,
		provideNotifyTap,
		provideDispatchRetryAlertSink,
	),
	fx.Provide(fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Provide(fx.Annotate(dagNotifierAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Invoke(registerDAGSubscriberLifecycle),
)

// provideAgentAliasResolver 返回默认丢弃策略的 alias resolver。
// 需要按 agent/thread 路由的部署可用 fx.Decorate 替换为持久化查询实现。
func provideAgentAliasResolver() AgentAliasResolver { return dropAllAliasResolver }

// provideTurnNotifier 创建 turn 事件通知器，并注入 agent 到渠道别名的解析端口。
func provideTurnNotifier(logger *slog.Logger, notifier contract.MessageNotifier, resolver AgentAliasResolver) *TurnNotifier {
	return NewTurnNotifier(logger, notifier, resolver)
}

// provideNotifyTap 把 *TurnNotifier 收窄为 orchestration.NotifyTap。
// orchestration 只依赖可选 tap 端口，不知道 notify 包里的具体实现。
func provideNotifyTap(t *TurnNotifier) orchestration.NotifyTap { return t }

// provideOrchResolver 解析通知渠道配置；空配置返回空 resolver 而不是隐式默认渠道。
func provideOrchResolver(cfg *platformconfig.Config) (notifyplatform.Resolver, error) {
	if cfg == nil {
		return notifyplatform.ParseChannelsJSON("")
	}
	return notifyplatform.ParseChannelsJSON(cfg.Notify.ChannelsJSON)
}

// provideOrchWebhookClient 根据平台配置创建 webhook client。
func provideOrchWebhookClient(cfg *platformconfig.Config) *notifyplatform.WebhookClient {
	wcfg := notifyplatform.WebhookClientConfig{}
	if cfg != nil {
		wcfg.AllowPrivateCIDR = cfg.Notify.AllowPrivateCIDR
		if cfg.Notify.TimeoutSeconds > 0 {
			wcfg.Timeout = time.Duration(cfg.Notify.TimeoutSeconds) * time.Second
		}
	}
	return notifyplatform.NewWebhookClient(wcfg)
}

// provideOrchNotifier 创建 orch 专用通知队列，容量来自配置或平台默认值。
func provideOrchNotifier(logger *slog.Logger, cfg *platformconfig.Config, resolver notifyplatform.Resolver) *notifyplatform.Notifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	capacity := notifyplatform.DefaultQueueCapacity
	if cfg != nil && cfg.Notify.QueueCapacity > 0 {
		capacity = cfg.Notify.QueueCapacity
	}
	return notifyplatform.NewNotifier(logger, resolver, capacity)
}

// provideMessageNotifier 把平台 Notifier 暴露成 contract.MessageNotifier。
func provideMessageNotifier(n *notifyplatform.Notifier) contract.MessageNotifier { return n }

// provideOrchFlusherParams 是构造通知 flusher 需要的 fx 参数集合。
type provideOrchFlusherParams struct {
	fx.In

	Logger   *slog.Logger
	Cfg      *platformconfig.Config
	Notifier *notifyplatform.Notifier
	Client   *notifyplatform.WebhookClient
}

// provideOrchFlusher 创建队列刷出 runner，退出 drain 时长由配置控制。
func provideOrchFlusher(p provideOrchFlusherParams) *notifyplatform.Flusher {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	drain := notifyplatform.DefaultDrainTimeout
	if p.Cfg != nil && p.Cfg.Notify.DrainSeconds > 0 {
		drain = time.Duration(p.Cfg.Notify.DrainSeconds) * time.Second
	}
	return notifyplatform.NewFlusher(logger, p.Notifier, p.Client, drain)
}

// flusherAsRunner 把通知 flusher 收窄为 RunGroup 可管理的 runner。
func flusherAsRunner(f *notifyplatform.Flusher) platformrunner.Runner { return f }

// dagNotifierAsRunner 把 DAGNotifier 收窄为 RunGroup 可管理的 runner。
func dagNotifierAsRunner(n *DAGNotifier) platformrunner.Runner { return n }

// dagSubscribeParams 是 DAG 通知订阅生命周期的 fx 参数集合。
type dagSubscribeParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher
	Notifier   *DAGNotifier
	Logger     *pkglogger.Logger `optional:"true"`
}

// registerDAGSubscriberLifecycle 在 OnStart 注册 DAG bus 订阅，并在 OnStop 取消。
// worker goroutine 由 DAGNotifier.Run 交给 RunGroup 管理，这里只负责事件订阅生命周期。
func registerDAGSubscriberLifecycle(p dagSubscribeParams) {
	cancel := func() {}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			cancel = p.Notifier.Subscribe(p.Dispatcher, p.Logger)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			cancel = func() {}
			return nil
		},
	})
}
