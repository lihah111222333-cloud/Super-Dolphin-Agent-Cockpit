package toolbridge

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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
		NewToolbridgeDiffFallbackSubscribers,
		provideProxyAddrFn,
		// P22 P4 S3d: assembly adapters bridging the concrete store
		// types to the toolbridge-local narrow ports (see ports.go). fx
		// resolves by exact type name, so even though bindingstore.Store
		// structurally satisfies agentThreadLookup we still need an
		// explicit provider. provideThreadConfigOverrideStore goes further
		// and performs the actual row → ConfigOverride projection.
		provideAgentThreadLookup,
		provideThreadConfigOverrideStore,
		// P22 P2 Finding 9: proxy HTTP serve loop owned by run.Group via the
		// root group:"runners" aggregation. registerProxyLifecycle keeps only
		// the listener setup + addr publish; ServeProxy runs inside
		// ProxyRunner.Run (proxy_runner.go).
		NewProxyRunner,
		fx.Annotate(asRunnerGroup, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(
		bindCodexHandlers,
		registerProxyLifecycle,
	),
)

type handlerIn struct {
	fx.In

	Registry     *mcpcontrol.ToolRegistry
	Emitter      difftracker.DiffEmitter
	Resolver     difftracker.WorkDirResolver
	DiffFallback *diffFallbackTracker
	// P22 P4 S3d: field types are the narrow ports from ports.go.
	// BindingStore is still satisfied structurally by the production
	// bindingstore.Store (GetThreadByAgent has identical signature), so
	// fx continues to wire the concrete store here without an adapter.
	// ThreadStore is satisfied via provideThreadConfigOverrideStore.
	BindingStore agentThreadLookup
	ThreadStore  threadConfigOverrideStore `optional:"true"`
	Config       *platformconfig.Config    `optional:"true"`
	Logger       *pkglogger.Logger         `optional:"true"`
}

// threadConfigOverrideAdapter wraps the production threadstore.Store so
// the handler can consume a narrow port (threadConfigOverrideStore,
// defined in ports.go) that returns only the ConfigOverride bytes. This
// lets handler.go / proxy.go stop importing internal/store/thread
// directly while keeping the module-level assembly code as the single
// file that knows the concrete store type.
type threadConfigOverrideAdapter struct {
	inner threadstore.Store
}

func (a threadConfigOverrideAdapter) GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error) {
	if a.inner == nil {
		return nil, nil
	}
	row, err := a.inner.GetByThreadID(ctx, threadID)
	if err != nil || row == nil {
		return nil, err
	}
	return row.ConfigOverride, nil
}

func provideThreadConfigOverrideStore(store threadstore.Store) threadConfigOverrideStore {
	if store == nil {
		return nil
	}
	return threadConfigOverrideAdapter{inner: store}
}

type agentThreadLookupAdapter struct {
	inner bindingstore.Store
}

func (a agentThreadLookupAdapter) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	return a.inner.GetThreadByAgent(ctx, agentID)
}

func (a agentThreadLookupAdapter) GetBindingByAgent(ctx context.Context, agentID string) (toolCallBinding, error) {
	if a.inner == nil {
		return toolCallBinding{}, nil
	}
	binding, err := a.inner.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return toolCallBinding{}, err
	}
	return toolCallBindingFromStore(binding), nil
}

func (a agentThreadLookupAdapter) GetBindingByProviderThread(ctx context.Context, provider, providerThreadID string) (toolCallBinding, error) {
	if a.inner == nil {
		return toolCallBinding{}, nil
	}
	binding, err := a.inner.GetByProviderThread(ctx, provider, providerThreadID)
	if err != nil || binding == nil {
		return toolCallBinding{}, err
	}
	return toolCallBindingFromStore(binding), nil
}

func toolCallBindingFromStore(binding *bindingstore.Binding) toolCallBinding {
	if binding == nil {
		return toolCallBinding{}
	}
	return toolCallBinding{
		AgentID:            strings.TrimSpace(binding.AgentID),
		Provider:           strings.TrimSpace(binding.Provider),
		ProviderThreadID:   strings.TrimSpace(binding.ProviderThreadID),
		CodexThreadID:      strings.TrimSpace(binding.CodexThreadID),
		CWD:                strings.TrimSpace(binding.Cwd),
		CodexHome:          strings.TrimSpace(binding.CodexHome),
		CodexInstanceKey:   strings.TrimSpace(binding.CodexInstanceKey),
		CodexModelProvider: strings.TrimSpace(binding.CodexModelProvider),
	}
}

// provideAgentThreadLookup is the fx bridge from the concrete
// bindingstore.Store to the toolbridge-local narrow ports (see ports.go).
func provideAgentThreadLookup(store bindingstore.Store) agentThreadLookup {
	if store == nil {
		return nil
	}
	return agentThreadLookupAdapter{inner: store}
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
