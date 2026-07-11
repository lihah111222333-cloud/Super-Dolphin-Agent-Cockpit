package toolbridge

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/difftracker"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/workflowtemplates"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"go.uber.org/fx"
)

// proxyAddr 保存当前 toolbridge proxy 的监听地址，供 provider manifest 注入。
var proxyAddr atomic.Value

// Module 装配 toolbridge 核心能力。
// store adapter、Codex handler 绑定和 WorkDirResolver 都留在 app 装配层，
// 这里不直接导入 module/provider/store 实现，保持平台包边界干净。
var Module = fx.Module("toolbridge",
	fx.Provide(
		provideToolbridgeDependencyConfig,
		NewHandler,
		provideHostToolRegistry,
		provideDiffEmitter,
		newDiffFallbackTracker,
		NewToolbridgeDiffFallbackSubscribers,
		fx.Annotate(provideProxyAddrFn, fx.ResultTags(`name:"proxy_addr_fn"`)),
		fx.Annotate(provideProxyTokenFn, fx.ResultTags(`name:"proxy_token_fn"`)),
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
	BindingStore    agentThreadLookup         `optional:"true"`
	ThreadStore     threadConfigOverrideStore `optional:"true"`
	Preferences     uiPreferenceReader        `optional:"true"`
	Config          *platformconfig.Config    `optional:"true"`
	Dependency      contract.DependencyConfig
	Logger          *pkglogger.Logger          `optional:"true"`
	Tracer          *observability.Service     `optional:"true"`
	Dispatcher      *event.Dispatcher          `optional:"true"`
	Lifecycle       mcpToolLifecycleBackfiller `optional:"true"`
	LifecyclePolicy mcpToolLifecyclePolicyReader
	// HostTools 是 Fx 可选字段：agent-terminal 生产图由 provideHostToolRegistry 填充；
	// Handler 构造期会按 dependency profile 校验它不能静默为空。
	HostTools  HostToolRegistry           `optional:"true"`
	SkillTools contract.SkillToolProvider `optional:"true"`
}

func provideToolbridgeDependencyConfig(cfg *platformconfig.Config) (contract.DependencyConfig, error) {
	if cfg == nil {
		return contract.DependencyConfig{}, errors.New("toolbridge: config is required for dependency profile")
	}
	if strings.TrimSpace(string(cfg.Dependency.Profile)) == "" {
		return contract.DependencyConfig{}, errors.New("toolbridge: dependency profile is required")
	}
	return cfg.Dependency, nil
}

// hostToolRegistryIn 聚合 host-direct 工具 registry 所需的可选依赖。
type hostToolRegistryIn struct {
	fx.In

	Reader    contract.AgentMemoryReader  `optional:"true"`
	Writer    contract.AgentMemoryWriter  `optional:"true"`
	History   contract.SessionStatusPort  `optional:"true"`
	Tracer    *observability.Service      `optional:"true"`
	Templates *workflowtemplates.Registry `optional:"true"`
}

// provideHostToolRegistry 组装 Codex 可见的 host-direct 工具 registry。
// 某项能力缺失或关闭时对应子 registry 为 nil，组合 registry 会跳过它，避免暴露半可用工具。
func provideHostToolRegistry(in hostToolRegistryIn) HostToolRegistry {
	return NewCompositeHostToolRegistry(
		NewMemoryReadHostToolRegistry(in.Reader, memoryReadHostToolOptions(in.Reader)),
		NewMemoryWriteHostToolRegistry(in.Writer, memoryWriteHostToolOptions(in.Writer)),
		NewHistoryReadHostToolRegistry(in.History),
		NewObservabilityTraceHostToolRegistry(in.Tracer),
		NewWorkflowTemplateHostToolRegistry(in.Templates),
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
// dispatcher 是生产必需依赖，缺失时让 Fx 图失败，避免 diff 事件静默丢失。
func provideDiffEmitter(dispatcher *event.Dispatcher) (difftracker.DiffEmitter, error) {
	if dispatcher == nil {
		return nil, errors.New("toolbridge: nil dispatcher for diff emitter")
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
	}, nil
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

// provideProxyTokenFn 返回可被 provider manifest builder 调用的 proxy token 读取函数。
// token 在 Handler 构造时生成，生命周期与 Handler 相同。
func provideProxyTokenFn(h *Handler) func() string {
	return func() string {
		if h == nil {
			return ""
		}
		return strings.TrimSpace(h.proxyAuthToken)
	}
}

// registerProxyLifecycle 完成 proxy 的同步装配：监听端口、发布地址并交给 ProxyRunner。
// Serve 与 listener close 由 ProxyRunner.Run 负责，这里 OnStop 只清空已发布地址。
func registerProxyLifecycle(lifecycle fx.Lifecycle, h *Handler, runner *ProxyRunner) error {
	if lifecycle == nil {
		return errors.New("toolbridge: nil lifecycle")
	}
	if h == nil {
		return errors.New("toolbridge: nil handler")
	}
	if runner == nil {
		return errors.New("toolbridge: nil proxy runner")
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
	return nil
}
