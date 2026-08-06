package app

import (
	"context"
	"errors"
	"sync"

	uiwails "github.com/lihah111222333-cloud/super-dolphin-agent/internal/ui/wails"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type desktopShutdownState uint8

const (
	desktopShutdownStateNew desktopShutdownState = iota
	desktopShutdownStateConfigured
	desktopShutdownStateRunning
	desktopShutdownStateDone
)

// desktopShutdownCoordinator 汇合桌面退出触发器，并让所有调用方观察同一停止结果。
type desktopShutdownCoordinator struct {
	mu         sync.Mutex
	state      desktopShutdownState
	configured chan struct{}
	done       chan struct{}
	owner      *appOwnerContext
	stop       func() error
	lifecycle  *uiwails.WailsLifecycle
	causes     []error
	result     error
}

// newDesktopShutdownCoordinator 创建由应用根 context 拥有的桌面停止状态机。
func newDesktopShutdownCoordinator(owner *appOwnerContext) (*desktopShutdownCoordinator, error) {
	if owner == nil {
		return nil, errors.New("desktop shutdown owner is required")
	}
	return &desktopShutdownCoordinator{
		state:      desktopShutdownStateNew,
		configured: make(chan struct{}),
		done:       make(chan struct{}),
		owner:      owner,
	}, nil
}

// Configure 绑定 Fx 停止与 Wails handoff；仅允许成功配置一次。
func (c *desktopShutdownCoordinator) Configure(stop func() error, lifecycle *uiwails.WailsLifecycle) error {
	if c == nil || stop == nil || lifecycle == nil {
		return errors.New("desktop shutdown dependencies are required")
	}
	c.mu.Lock()
	if c.state != desktopShutdownStateNew {
		result := c.result
		c.mu.Unlock()
		if result != nil {
			return result
		}
		return errors.New("desktop shutdown coordinator is already configured")
	}
	c.stop = stop
	c.lifecycle = lifecycle
	c.state = desktopShutdownStateConfigured
	close(c.configured)
	c.mu.Unlock()

	lifecycle.SetShutdownerFunc(func() {
		if err := c.Shutdown(context.Background(), nil); err != nil {
			pkglogger.Get().Warn("desktop shutdown failed", "error", err)
		}
	})
	return nil
}

// Shutdown 执行唯一有序停止；并发和重复调用均等待并返回缓存的同一结果。
func (c *desktopShutdownCoordinator) Shutdown(ctx context.Context, cause error) error {
	if c == nil || ctx == nil {
		return errors.New("desktop shutdown coordinator and context are required")
	}
	for {
		c.mu.Lock()
		if c.state == desktopShutdownStateDone {
			result := c.result
			c.mu.Unlock()
			return result
		}
		if cause != nil {
			c.causes = append(c.causes, cause)
			cause = nil
		}
		switch c.state {
		case desktopShutdownStateNew:
			configured := c.configured
			c.mu.Unlock()
			<-configured
			continue
		case desktopShutdownStateConfigured:
			c.state = desktopShutdownStateRunning
			c.mu.Unlock()

			c.owner.Cancel()
			joinErr := preDrainDesktopRuntime(ctx, c.owner)
			stopErr := c.stop()

			c.mu.Lock()
			c.result = errors.Join(append(c.causes, joinErr, stopErr)...)
			if c.result != nil {
				c.lifecycle.NotifyBackendFailed()
			} else {
				c.lifecycle.NotifyBackendStopped()
			}
			c.state = desktopShutdownStateDone
			close(c.done)
			result := c.result
			c.mu.Unlock()
			return result
		case desktopShutdownStateRunning:
			done := c.done
			c.mu.Unlock()
			<-done
			c.mu.Lock()
			result := c.result
			c.mu.Unlock()
			return result
		}
		c.mu.Unlock()
		return errors.New("desktop shutdown coordinator has invalid state")
	}
}

// FailStartup 发布启动阶段的最终失败，并解除所有等待 Configure 的退出调用方。
func (c *desktopShutdownCoordinator) FailStartup(err error) error {
	if c == nil {
		return errors.New("desktop shutdown coordinator is required")
	}
	if err == nil {
		return errors.New("desktop startup failure is required")
	}
	c.mu.Lock()
	if c.state == desktopShutdownStateDone {
		result := c.result
		c.mu.Unlock()
		return result
	}
	if c.state == desktopShutdownStateRunning {
		done := c.done
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		result := c.result
		c.mu.Unlock()
		return result
	}
	c.causes = append(c.causes, err)
	c.result = errors.Join(c.causes...)
	if c.state == desktopShutdownStateNew {
		close(c.configured)
	}
	c.state = desktopShutdownStateDone
	close(c.done)
	result := c.result
	c.mu.Unlock()
	return result
}
