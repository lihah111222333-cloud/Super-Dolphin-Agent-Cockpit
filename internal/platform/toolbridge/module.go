package toolbridge

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

var proxyAddr atomic.Value

// Module wires the toolbridge core. Store adapters, codex handler binding,
// and WorkDirResolver live in the app assembly layer
// (internal/app/toolbridge_adapters.go) to keep this platform package free
// of module/provider/store imports.
var Module = fx.Module("toolbridge",
	fx.Provide(
		NewHandler,
		provideHostToolRegistry,
		provideDiffEmitter,
		newDiffFallbackTracker,
		NewToolbridgeDiffFallbackSubscribers,
		provideProxyAddrFn,
		// P22 P2 Finding 9: proxy HTTP serve loop owned by run.Group via the
		// root group:"runners" aggregation. registerProxyLifecycle keeps only
		// the listener setup + addr publish; ServeProxy runs inside
		// ProxyRunner.Run (proxy_runner.go).
		NewProxyRunner,
		fx.Annotate(asRunnerGroup, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(
		registerProxyLifecycle,
	),
)

type handlerIn struct {
	fx.In

	Registry     *mcpcontrol.ToolRegistry
	Emitter      difftracker.DiffEmitter
	Resolver     difftracker.WorkDirResolver `optional:"true"`
	DiffFallback *diffFallbackTracker
	// P22 P4 S3d: field types are the narrow ports from ports.go.
	// Concrete store adapters now live in internal/app/toolbridge_adapters.go
	// and are injected through the narrow port interfaces.
	BindingStore agentThreadLookup         `optional:"true"`
	ThreadStore  threadConfigOverrideStore `optional:"true"`
	Preferences  uiPreferenceReader        `optional:"true"`
	Config       *platformconfig.Config    `optional:"true"`
	Logger       *pkglogger.Logger         `optional:"true"`
	Tracer       *observability.Service    `optional:"true"`
	Dispatcher   *event.Dispatcher         `optional:"true"`
	// HostTools is an fx optional field: in the agent-terminal graph it is
	// filled by provideHostToolRegistry; tests or future no-provider graphs
	// can leave it nil and the Handler falls back to the peer path.
	HostTools HostToolRegistry `optional:"true"`
}

type hostToolRegistryIn struct {
	fx.In

	Reader contract.AgentMemoryReader `optional:"true"`
	Writer contract.AgentMemoryWriter `optional:"true"`
	Tracer *observability.Service     `optional:"true"`
}

// provideHostToolRegistry builds the Codex-facing HostToolRegistry backed by
// host-direct tools. The registry is nil-safe: when a capability is absent or
// disabled, the corresponding child registry is nil and the composite omits it.
func provideHostToolRegistry(in hostToolRegistryIn) HostToolRegistry {
	return NewCompositeHostToolRegistry(
		NewMemoryReadHostToolRegistry(in.Reader, memoryReadHostToolOptions(in.Reader)),
		NewMemoryWriteHostToolRegistry(in.Writer, memoryWriteHostToolOptions(in.Writer)),
		NewObservabilityTraceHostToolRegistry(in.Tracer),
	)
}

// ProvideHostToolRegistryForTesting 为testing提供host工具注册表。
func ProvideHostToolRegistryForTesting(in hostToolRegistryIn) HostToolRegistry {
	return provideHostToolRegistry(in)
}

// NewHandlerForTesting 为testing创建处理器。
func NewHandlerForTesting(registry activePeerRegistry, hostTools HostToolRegistry) *Handler {
	return &Handler{registry: registry, hostTools: hostTools, logger: pkglogger.Get()}
}

func memoryReadHostToolOptions(reader contract.AgentMemoryReader) MemoryReadHostToolOptions {
	if reader == nil {
		return MemoryReadHostToolOptions{}
	}
	return MemoryReadHostToolOptions{Enabled: reader.MemoryReadEnabled(), ToolsEnabled: reader.MemoryReadToolsEnabled()}
}

func memoryWriteHostToolOptions(writer contract.AgentMemoryWriter) MemoryWriteHostToolOptions {
	if writer == nil {
		return MemoryWriteHostToolOptions{}
	}
	return MemoryWriteHostToolOptions{Enabled: writer.MemoryWriteEnabled(), ToolsEnabled: writer.MemoryWriteToolsEnabled()}
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

// resolverFunc wraps a plain function into a difftracker.WorkDirResolver.
// Used internally and by tests; the production provider lives in
// internal/app/toolbridge_adapters.go.
type resolverFunc func(context.Context, string) (string, error)

// ResolveAgentCWD 解析代理工作目录。
func (fn resolverFunc) ResolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	return fn(ctx, agentID)
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
// registerProxyLifecycle 注册proxy生命周期。
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
