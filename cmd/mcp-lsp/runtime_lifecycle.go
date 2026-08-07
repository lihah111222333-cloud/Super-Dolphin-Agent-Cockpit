package main

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
)

type runtimeBinaryOverride struct {
	mu    sync.RWMutex
	value string
}

// Set 在 installer 或 bundle 解析到新二进制后更新后续 client 使用的路径。
// 空路径会被忽略，避免把已验证的可执行文件覆盖成不可启动状态。
func (b *runtimeBinaryOverride) Set(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		b.value = trimmed
	}
}

// Get 返回当前语言 server 二进制路径。
// 读锁保证并发创建 client 时能看到一致的路径字符串。
func (b *runtimeBinaryOverride) Get() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.value
}

type runtimeBinaryManager struct {
	multilsp.Manager
	binary              *runtimeBinaryOverride
	goplsRootController multilsp.GoplsRootCohortController
}

// Close 先关闭池内 workspace clients，再关闭 root cohort admission owner。
// durable controller 的 pending owner 会在自身 Close 中继续执行安全 drain，不会走 RSS/ps kill。
func (m *runtimeBinaryManager) Close() error {
	if m == nil {
		return nil
	}
	var closeErr error
	if m.Manager != nil {
		closeErr = m.Manager.Close()
	}
	if m.goplsRootController != nil {
		closeErr = errors.Join(closeErr, m.goplsRootController.Close())
	}
	return closeErr
}

// RegistryScopedResolver 暴露 runtime manager 的按工具作用域解析能力。
// nil receiver 返回 nil，registry 会继续使用非 scoped 路径而不是解引用 panic。
func (m *runtimeBinaryManager) RegistryScopedResolver() manager.ScopedManagerResolver {
	if m == nil {
		return nil
	}
	return multilsp.NewRegistryScopedResolver(m.Manager)
}

// ReleaseScope 将 runtimeBinaryManager 的释放请求转交给底层 ManagerPool。
// 底层不支持 ScopeReleaser 时返回零值结果，保持非池实现的兼容边界。
func (m *runtimeBinaryManager) ReleaseScope(req multilsp.ReleaseScopeRequest) (multilsp.ReleaseScopeResult, error) {
	if m == nil {
		return multilsp.ReleaseScopeResult{}, nil
	}
	releaser, ok := m.Manager.(multilsp.ScopeReleaser)
	if !ok {
		return multilsp.ReleaseScopeResult{}, nil
	}
	return releaser.ReleaseScope(req)
}

// SetBinaryPath 设置二进制路径。
func (m *runtimeBinaryManager) SetBinaryPath(path string) {
	if m != nil && m.binary != nil {
		m.binary.Set(path)
	}
}

// Close 关闭 LSP 管理器资源。
func (m *Manager) Close() error {
	if m.registry != nil {
		return m.registry.Close()
	}
	return nil
}

// ReleaseScope 将 MCP DTO 转换为 multilsp release 请求并广播给所有语言池。
// 每个语言池独立统计命中/关闭/忙碌租约，首个关闭错误会随聚合结果返回给调用方。
func (m *Manager) ReleaseScope(req mcp.LSPReleaseScopeRequest) (mcp.LSPReleaseScopeResult, error) {
	if m == nil {
		return mcp.LSPReleaseScopeResult{}, nil
	}
	translated := multilsp.ReleaseScopeRequest{
		ScopeKind:  req.ScopeKind,
		AgentID:    req.AgentID,
		ThreadID:   req.ThreadID,
		ManagerKey: req.ManagerKey,
		Drain:      req.Drain,
		Reason:     req.Reason,
	}
	aggregation := runtimeReleaseAggregation{allDrained: req.Drain}
	for _, releaser := range m.releaseScopes {
		if releaser == nil {
			continue
		}
		result, err := releaser.ReleaseScope(translated)
		aggregation.merge(result, err)
	}
	return aggregation.finish(req.Drain)
}

type runtimeReleaseAggregation struct {
	result     mcp.LSPReleaseScopeResult
	firstErr   error
	allDrained bool
	consulted  int
}

// merge 合并单个语言池的释放结果，并保留首个错误。
func (a *runtimeReleaseAggregation) merge(result multilsp.ReleaseScopeResult, err error) {
	a.consulted++
	if err != nil {
		a.allDrained = false
		if a.firstErr == nil {
			a.firstErr = err
		}
	}
	a.result.MatchedManagers += result.MatchedManagers
	a.result.ClosedManagers += result.ClosedManagers
	a.result.BusyLeases += result.BusyLeases
	a.allDrained = a.allDrained && result.Drained
	a.result.ScopeKeys = appendRuntimeUnique(a.result.ScopeKeys, result.ScopeKeys...)
	a.result.ManagerKeys = appendRuntimeUnique(a.result.ManagerKeys, result.ManagerKeys...)
}

// finish 只有所有已咨询语言池都成功 drain 时才返回 Drained=true。
func (a runtimeReleaseAggregation) finish(drain bool) (mcp.LSPReleaseScopeResult, error) {
	a.result.Drained = drain &&
		a.consulted > 0 &&
		a.allDrained &&
		a.result.BusyLeases == 0 &&
		a.firstErr == nil
	return a.result, a.firstErr
}

func appendRuntimeUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

type stdioRunner struct {
	server  interface{ Run(context.Context) error }
	manager interface{ Close() error }
}

func newStdioRunner(server *common.Server, manager *Manager) platformrunner.Runner {
	return stdioRunner{server: server, manager: manager}
}

// Run 启动LSP后台流程。
func (r stdioRunner) Run(ctx context.Context) (err error) {
	if r.server == nil {
		return errors.New("mcp-lsp server is not configured")
	}
	defer func() {
		if r.manager != nil {
			err = errors.Join(err, r.manager.Close())
		}
	}()
	return r.server.Run(ctx)
}
