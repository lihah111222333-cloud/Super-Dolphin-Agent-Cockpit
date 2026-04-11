package toolbridge

import (
	"context"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

var Module = fx.Module("toolbridge",
	fx.Provide(
		NewHandler,
		provideWorkDirResolver,
		provideDiffEmitter,
		provideDiffAggregator,
	),
	fx.Invoke(bindCodexHandlers),
	fx.Invoke(registerAggregatorLifecycle),
	fx.Invoke(registerCleanupLifecycle),
)

type handlerIn struct {
	fx.In

	Registry   *mcpcontrol.ToolRegistry
	Aggregator *difftracker.DiffAggregator
	Resolver   difftracker.WorkDirResolver
	Logger     *pkglogger.Logger `optional:"true"`
}

func bindCodexHandlers(mgr *codexapp.ServerManager, factory *codexapp.DriverFactory, h *Handler) {
	if mgr == nil || factory == nil || h == nil {
		return
	}
	mgr.SetToolHandler(h.HandleToolCall)
	factory.SetListTools(h.ListToolsForCodex)
}

type resolverFunc func(context.Context, string) (string, error)

func (fn resolverFunc) ResolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	return fn(ctx, agentID)
}

func provideWorkDirResolver(bindingStore bindingstore.Store) difftracker.WorkDirResolver {
	if bindingStore == nil {
		return nil
	}
	return resolverFunc(func(ctx context.Context, agentID string) (string, error) {
		if strings.TrimSpace(agentID) == "" {
			return "", nil
		}
		binding, err := bindingStore.GetByAgentID(ctx, agentID)
		if err != nil || binding == nil {
			return "", err
		}
		return strings.TrimSpace(binding.Cwd), nil
	})
}

func provideDiffEmitter(dispatcher *event.Dispatcher) difftracker.DiffEmitter {
	if dispatcher == nil {
		return nil
	}
	return func(ctx context.Context, diff difftracker.DiffResult) error {
		event.Publish(dispatcher, tooldto.ToolDiffUpdated{
			Timestamp: time.Now(),
			ThreadID:  diff.ThreadID,
			AgentID:   diff.AgentID,
			CallID:    diff.CallID,
			ToolName:  diff.ToolName,
			DiffText:  diff.DiffText,
			Files:     append([]string(nil), diff.Files...),
		})
		return nil
	}
}

func provideDiffAggregator(emitter difftracker.DiffEmitter) *difftracker.DiffAggregator {
	if emitter == nil {
		return difftracker.NewDiffAggregator()
	}
	return difftracker.NewDiffAggregator(difftracker.WithEmitter(emitter))
}

func registerAggregatorLifecycle(lc fx.Lifecycle, aggregator *difftracker.DiffAggregator) {
	if aggregator == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			aggregator.Start()
			return nil
		},
		OnStop: func(context.Context) error {
			aggregator.Stop()
			return nil
		},
	})
}

type cleanupLifecycleIn struct {
	fx.In

	Dispatcher *event.Dispatcher
	Aggregator *difftracker.DiffAggregator
	Logger     *pkglogger.Logger `optional:"true"`
}

func registerCleanupLifecycle(lc fx.Lifecycle, in cleanupLifecycleIn) {
	if in.Dispatcher == nil || in.Aggregator == nil {
		return
	}
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}

	var cancels []context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = []context.CancelFunc{
				platformbus.ResilientSubscribe(in.Dispatcher, func(ev agentdto.AgentStopped) {
					in.Aggregator.CleanupAgent(ev.AgentID)
				}, logger),
				platformbus.ResilientSubscribe(in.Dispatcher, func(ev agentdto.AgentFailed) {
					in.Aggregator.CleanupAgent(ev.AgentID)
				}, logger),
				platformbus.ResilientSubscribe(in.Dispatcher, func(ev agentdto.AgentError) {
					in.Aggregator.CleanupAgent(ev.AgentID)
				}, logger),
			}
			return nil
		},
		OnStop: func(context.Context) error {
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			return nil
		},
	})
}
