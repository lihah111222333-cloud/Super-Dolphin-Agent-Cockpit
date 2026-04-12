package toolbridge

import (
	"context"
	"errors"
	"net"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
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
		provideProxyAddrFn,
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
	BindingStore bindingstore.Store
	Logger       *pkglogger.Logger `optional:"true"`
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

func registerProxyLifecycle(lifecycle fx.Lifecycle, h *Handler) {
	if lifecycle == nil || h == nil {
		return
	}
	var ln net.Listener
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return err
			}
			ln = listener
			proxyAddr.Store(strings.TrimSpace(listener.Addr().String()))
			logger := h.logger
			if logger == nil {
				logger = pkglogger.Get()
			}
			go func(proxyListener net.Listener) {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("proxy serve panic", "recover", r, "stack", string(debug.Stack()))
					}
				}()
				if err := h.ServeProxy(proxyListener); err != nil {
					logger.Error("proxy serve failed", "error", err)
				}
			}(listener)
			return nil
		},
		OnStop: func(context.Context) error {
			if ln == nil {
				return nil
			}
			proxyAddr.Store("")
			err := ln.Close()
			ln = nil
			if err != nil && !errors.Is(err, net.ErrClosed) {
				return err
			}
			return nil
		},
	})
}

