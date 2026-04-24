package toolbridge

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

var proxyAddr atomic.Value

var Module = fx.Module("toolbridge",
	fx.Provide(
		NewHandler,
		provideWorkDirResolver,
		provideDiffEmitter,
		newDiffFallbackTracker,
		provideProxyAddrFn,
		// P22 P2 Finding 9: proxy HTTP serve loop owned by run.Group via the
		// root group:"runners" aggregation. registerProxyLifecycle keeps only
		// the listener setup + addr publish; ServeProxy runs inside
		// ProxyRunner.Run (proxy_runner.go).
		NewProxyRunner,
		fx.Annotate(asRunnerGroup, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(
		bindCodexHandlers,
		registerDiffFallbackLifecycle,
		registerProxyLifecycle,
	),
)

type handlerIn struct {
	fx.In

	Registry     *mcpcontrol.ToolRegistry
	Emitter      difftracker.DiffEmitter
	Resolver     difftracker.WorkDirResolver
	DiffFallback *diffFallbackTracker
	BindingStore bindingstore.Store
	ThreadStore  threadRuntimeConfigStore `optional:"true"`
	Config       *platformconfig.Config   `optional:"true"`
	Logger       *pkglogger.Logger        `optional:"true"`
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
			Revision:  diff.Revision,
		})
		return nil
	}
}

func provideProxyAddrFn() func() string {
	return func() string {
		addr, _ := proxyAddr.Load().(string)
		return strings.TrimSpace(addr)
	}
}

func registerDiffFallbackLifecycle(lifecycle fx.Lifecycle, dispatcher *event.Dispatcher, tracker *diffFallbackTracker) {
	if lifecycle == nil || dispatcher == nil || tracker == nil {
		return
	}
	var cancel context.CancelFunc
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancel = platformbus.ResilientSubscribe(dispatcher, tracker.handleToolCallEnd, tracker.logger)
			return nil
		},
		OnStop: func(context.Context) error {
			if cancel != nil {
				cancel()
				cancel = nil
			}
			return nil
		},
	})
}

// registerProxyLifecycle performs the synchronous setup half of the proxy:
// open the listener, publish the address, and hand the listener to the
// ProxyRunner that will serve it from run.Group. Serve + listener-close are
// owned by ProxyRunner.Run after P22 P2 Finding 9, so this hook is now a
// pure wiring step with no OnStop concerns besides clearing the published
// address.
func registerProxyLifecycle(lifecycle fx.Lifecycle, h *Handler, runner *ProxyRunner) {
	if lifecycle == nil || h == nil || runner == nil {
		return
	}
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return err
			}
			addr := strings.TrimSpace(listener.Addr().String())
			proxyAddr.Store(addr)
			runner.SetListener(listener)
			logger := h.logger
			if logger == nil {
				logger = pkglogger.Get()
			}
			logger.Warn("toolbridge: proxy started", "addr", addr)
			return nil
		},
		OnStop: func(context.Context) error {
			proxyAddr.Store("")
			return nil
		},
	})
}


