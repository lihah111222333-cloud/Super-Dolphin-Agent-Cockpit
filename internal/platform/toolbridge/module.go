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

// proxyAddr 保存当前 toolbridge proxy 的监听地址，供 provider manifest 注入。
var proxyAddr atomic.Value

// Module 装配 toolbridge 核心能力。
// store adapter、Codex handler 绑定和 WorkDirResolver 都留在 app 装配层，
// 这里不直接导入 module/provider/store 实现，保持平台包边界干净。
var Module = fx.Module("toolbridge",
	fx.Provide(
		NewHandler,
		provideHostToolRegistry,
		provideDiffEmitter,
		newDiffFallbackTracker,
		NewToolbridgeDiffFallbackSubscribers,
		provideProxyAddrFn,
		// proxy HTTP serve loop 由 run.Group 接管；fx lifecycle 只负责 listener
		// 创建和地址发布，避免启动/关闭路径分散在多个 goroutine owner 中。
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
	// store 依赖只通过 ports.go 的窄接口进入 toolbridge；具体 adapter 留在 app 装配层。
	BindingStore agentThreadLookup         `optional:"true"`
	ThreadStore  threadConfigOverrideStore `optional:"true"`
	Preferences  uiPreferenceReader        `optional:"true"`
	Config       *platformconfig.Config    `optional:"true"`
	Logger       *pkglogger.Logger         `optional:"true"`
	Tracer       *observability.Service    `optional:"true"`
	Dispatcher   *event.Dispatcher         `optional:"true"`
	// HostTools 是 Fx 可选字段：agent-terminal 生产图由 provideHostToolRegistry 填充；
	// 测试或无 provider 图可以留空，Handler 会走 peer 路径。
	HostTools  HostToolRegistry           `optional:"true"`
	SkillTools contract.SkillToolProvider `optional:"true"`
}

// hostToolRegistryIn 聚合 host-direct 工具 registry 所需的可选依赖。
type hostToolRegistryIn struct {
	fx.In

	Reader contract.AgentMemoryReader `optional:"true"`
	Writer contract.AgentMemoryWriter `optional:"true"`
	Tracer *observability.Service     `optional:"true"`
}

// provideHostToolRegistry 组装 Codex 可见的 host-direct 工具 registry。
// 某项能力缺失或关闭时对应子 registry 为 nil，组合 registry 会跳过它，避免暴露半可用工具。
func provideHostToolRegistry(in hostToolRegistryIn) HostToolRegistry {
	return NewCompositeHostToolRegistry(
		NewMemoryReadHostToolRegistry(in.Reader, memoryReadHostToolOptions(in.Reader)),
		NewMemoryWriteHostToolRegistry(in.Writer, memoryWriteHostToolOptions(in.Writer)),
		NewObservabilityTraceHostToolRegistry(in.Tracer),
		NewWorkflowTemplateHostToolRegistry(),
	)
}

// ProvideHostToolRegistryForTesting 为测试提供与生产一致的 host tool registry 装配入口。
func ProvideHostToolRegistryForTesting(in hostToolRegistryIn) HostToolRegistry {
	return provideHostToolRegistry(in)
}

// NewHandlerForTesting 创建测试用 Handler，只注入 registry、hostTools 和默认 logger。
func NewHandlerForTesting(registry activePeerRegistry, hostTools HostToolRegistry) *Handler {
	return &Handler{registry: registry, hostTools: hostTools, logger: pkglogger.Get()}
}

// memoryReadHostToolOptions 从 reader 能力位生成 memory_read host tool 开关。
func memoryReadHostToolOptions(reader contract.AgentMemoryReader) MemoryReadHostToolOptions {
	if reader == nil {
		return MemoryReadHostToolOptions{}
	}
	return MemoryReadHostToolOptions{Enabled: reader.MemoryReadEnabled(), ToolsEnabled: reader.MemoryReadToolsEnabled()}
}

// memoryWriteHostToolOptions 从 writer 能力位生成 memory_write host tool 开关。
func memoryWriteHostToolOptions(writer contract.AgentMemoryWriter) MemoryWriteHostToolOptions {
	if writer == nil {
		return MemoryWriteHostToolOptions{}
	}
	return MemoryWriteHostToolOptions{Enabled: writer.MemoryWriteEnabled(), ToolsEnabled: writer.MemoryWriteToolsEnabled()}
}

// provideDiffEmitter 将 difftracker 结果发布为 ToolDiffUpdated 总线事件。
// dispatcher 未装配时返回 nil，调用方会跳过 diff 发布；已发布事件会复制文件列表，避免后续修改污染总线消息。
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

// resolverFunc 将普通函数适配为 difftracker.WorkDirResolver。
// 该类型仅供内部和测试装配使用；生产实现由 app 层 adapter 提供，避免 toolbridge 反向依赖绑定 store。
type resolverFunc func(context.Context, string) (string, error)

// ResolveAgentCWD 调用底层函数解析 agent 工作目录。
func (fn resolverFunc) ResolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	return fn(ctx, agentID)
}

// provideProxyAddrFn 返回可被 provider manifest builder 调用的 proxy 地址读取函数。
func provideProxyAddrFn() func() string {
	return func() string {
		addr, _ := proxyAddr.Load().(string)
		return strings.TrimSpace(addr)
	}
}

// registerProxyLifecycle 完成 proxy 的同步装配：监听端口、发布地址并交给 ProxyRunner。
// Serve 与 listener close 由 ProxyRunner.Run 负责，这里 OnStop 只清空已发布地址。
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
