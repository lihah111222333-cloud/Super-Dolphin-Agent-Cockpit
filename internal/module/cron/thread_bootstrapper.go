package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ThreadServiceBootstrapper 将 contract.CronThreadStarter 适配为 ThreadBootstrapper。
// bootstrap 只服务第一次触发且还没有 thread_id 的 job；provider 配置原样交给 thread 层解析。
// 坏配置必须返回错误，不能落到默认线程或错误实例；返回 ID 的持久化仍由 Scheduler 完成。
type ThreadServiceBootstrapper struct {
	svc    contract.CronThreadStarter
	logger *slog.Logger
}

// NewThreadServiceBootstrapper 创建基于 thread service 的线程引导器。
// svc 是必填跨模块依赖，未接线时应由上层 factory 选择 NoopThreadBootstrapper。
func NewThreadServiceBootstrapper(logger *slog.Logger, svc contract.CronThreadStarter) *ThreadServiceBootstrapper {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ThreadServiceBootstrapper{svc: svc, logger: logger}
}

var _ ThreadBootstrapper = (*ThreadServiceBootstrapper)(nil)

// BootstrapThread 为定时任务准备目标线程。
// 它不绕过 thread.Service 的启动流程，确保随后的 StartTurn 能解析到可用 session。
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

// decodeBootstrapConfig 将 job 行里的原始 JSON 配置转换为 CronStartThreadRequest 需要的 map。
// 空配置表示无覆盖；坏配置要直接失败，不能悄悄丢掉 codexHome 等身份信息。
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
