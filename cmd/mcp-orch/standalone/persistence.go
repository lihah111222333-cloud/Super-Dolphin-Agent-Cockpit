package standalone

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// SessionRecord 是一次 CLI 创建完成后需要原子保存的逻辑身份，不含凭据或完整 prompt。
type SessionRecord struct {
	AgentID, ThreadID, Provider, Name, CWD, ParentID, Model string
	CreatedAt                                               time.Time
}

// SessionStore 由独立执行器消费，store 实现位于 sidecar 自有持久化层。
type SessionStore interface {
	CreateSession(context.Context, SessionRecord) error
}

// requireSessionStore 在任何 CLI 进程副作用前拒绝缺失持久化 owner。
func (e *Execution) requireSessionStore() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.store == nil {
		return errors.New("CLI session persistence is not configured")
	}
	return nil
}

// BindSessionStore 在服务启动前绑定持久 owner，缺失时不能启动新 CLI agent。
func (e *Execution) BindSessionStore(store SessionStore) error {
	if store == nil {
		return errors.New("CLI session persistence is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sessions) != 0 || e.closed {
		return errors.New("cannot change persistence after CLI execution started")
	}
	e.store = store
	return nil
}

// persistSession 在首轮提交前保存 thread/binding；失败不能发布可用会话。
func (e *Execution) persistSession(ctx context.Context, req contract.LaunchRequest, threadID, provider, model string) error {
	e.mu.Lock()
	store := e.store
	e.mu.Unlock()
	if store == nil {
		return errors.New("CLI session persistence is not configured")
	}
	return store.CreateSession(ctx, SessionRecord{
		AgentID: req.AgentID, ThreadID: threadID, Provider: provider, Name: req.Name,
		CWD: req.Cwd, ParentID: req.ParentID, Model: model, CreatedAt: time.Now().UTC(),
	})
}
