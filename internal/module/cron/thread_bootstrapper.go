package cron

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// ThreadServiceBootstrapper adapts contract.CronThreadStarter into the
// ThreadBootstrapper seam consumed by TurnServiceAdapter. It is
// deliberately a thin shim: translate BootstrapRequest into
// contract.CronStartThreadRequest, call CronStartThread eagerly
// (DeferSpawn=false so the provider CLI launches before the immediate
// first StartTurn), and return the ThreadID + AgentID for the scheduler
// to persist.
//
// Provider-specific config (codexHome / codexInstanceKey /
// codexModelProvider / …) is forwarded verbatim via
// CronStartThreadRequest.Config so the downstream codexapp driver can
// resolve a multi-provider binding at session-start time. An invalid
// config blob is surfaced as a plain error — we do NOT fall back to
// a default thread here because a cron job that pinned an identity
// must not silently escape to the wrong instance.
//
// bootstrap 只服务第一次触发、还没有 thread_id 的 job。
// 它把配置交给 thread 层，保存 ID 仍由 Scheduler 做。
type ThreadServiceBootstrapper struct {
	svc    contract.CronThreadStarter
	logger *pkglogger.Logger
}

// NewThreadServiceBootstrapper wires the adapter. A nil CronThreadStarter
// is a programmer error — callers should rely on the module-level fx
// factory to fall back to NoopThreadBootstrapper before reaching this
// constructor.
// NewThreadServiceBootstrapper 创建基于 thread service 的线程引导器。
func NewThreadServiceBootstrapper(logger *pkglogger.Logger, svc contract.CronThreadStarter) *ThreadServiceBootstrapper {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ThreadServiceBootstrapper{svc: svc, logger: logger}
}

var _ ThreadBootstrapper = (*ThreadServiceBootstrapper)(nil)

// BootstrapThread implements ThreadBootstrapper.
// BootstrapThread 为定时任务准备或复用目标线程。
func (b *ThreadServiceBootstrapper) BootstrapThread(ctx context.Context, req BootstrapRequest) (BootstrapResult, error) {
	if b == nil || b.svc == nil {
		return BootstrapResult{}, ErrBootstrapperNotWired
	}
	cfg, err := decodeBootstrapConfig(req.Config)
	if err != nil {
		return BootstrapResult{}, err
	}
	// 不绕过 thread.Service 的启动流程；否则下一步 StartTurn 可能拿不到可用 session。
	startReq := contract.CronStartThreadRequest{
		Provider: strings.TrimSpace(req.Provider),
		CWD:      strings.TrimSpace(req.CWD),
		Model:    strings.TrimSpace(req.Model),
		Name:     strings.TrimSpace(req.Name),
		Config:   cfg,
	}
	res, err := b.svc.CronStartThread(ctx, startReq)
	if err != nil {
		return BootstrapResult{}, err
	}
	threadID := strings.TrimSpace(res.ThreadID)
	if threadID == "" {
		return BootstrapResult{}, errors.New("cron: CronThreadStarter.CronStartThread returned empty thread id")
	}
	return BootstrapResult{
		ThreadID: threadID,
		AgentID:  strings.TrimSpace(res.AgentID),
	}, nil
}

// decodeBootstrapConfig turns the job row's raw config JSON into the
// map[string]any shape CronStartThreadRequest expects. An empty/nil input
// is legal — the thread layer interprets nil as "no overrides". A
// syntactically-invalid blob is rejected so a corrupt row surfaces as
// a bootstrap error instead of silently dropping codexHome.
//
// 坏配置要直接失败，不能悄悄丢掉 codexHome 等身份信息。
func decodeBootstrapConfig(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("cron: bootstrap config is not a JSON object")
	}
	return out, nil
}
