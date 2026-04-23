package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	thread "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ThreadServiceBootstrapper adapts thread.Service into the
// ThreadBootstrapper seam consumed by TurnServiceAdapter. It is
// deliberately a thin shim: translate BootstrapRequest into
// thread.StartRequest, call Start eagerly (DeferSpawn=false so the
// provider CLI launches before the immediate first StartTurn), and
// return the ThreadID + AgentID for the scheduler to persist.
//
// Provider-specific config (codexHome / codexInstanceKey /
// codexModelProvider / \u2026) is forwarded verbatim via
// thread.StartRequest.Config so the downstream codexapp driver can
// resolve a multi-provider binding at session-start time. An invalid
// config blob is surfaced as a plain error \u2014 we do NOT fall back to
// a default thread here because a cron job that pinned an identity
// must not silently escape to the wrong instance.
type ThreadServiceBootstrapper struct {
	svc    thread.Service
	logger *slog.Logger
}

// NewThreadServiceBootstrapper wires the adapter. A nil thread.Service
// is a programmer error \u2014 callers should rely on the module-level fx
// factory to fall back to NoopThreadBootstrapper before reaching this
// constructor.
func NewThreadServiceBootstrapper(logger *slog.Logger, svc thread.Service) *ThreadServiceBootstrapper {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ThreadServiceBootstrapper{svc: svc, logger: logger}
}

var _ ThreadBootstrapper = (*ThreadServiceBootstrapper)(nil)

// BootstrapThread implements ThreadBootstrapper.
func (b *ThreadServiceBootstrapper) BootstrapThread(ctx context.Context, req BootstrapRequest) (BootstrapResult, error) {
	if b == nil || b.svc == nil {
		return BootstrapResult{}, ErrBootstrapperNotWired
	}
	cfg, err := decodeBootstrapConfig(req.Config)
	if err != nil {
		return BootstrapResult{}, err
	}
	startReq := thread.StartRequest{
		Provider: strings.TrimSpace(req.Provider),
		CWD:      strings.TrimSpace(req.CWD),
		Model:    strings.TrimSpace(req.Model),
		Name:     strings.TrimSpace(req.Name),
		Config:   cfg,
	}
	res, err := b.svc.Start(ctx, startReq)
	if err != nil {
		return BootstrapResult{}, err
	}
	threadID := strings.TrimSpace(res.ThreadID)
	if threadID == "" {
		return BootstrapResult{}, errors.New("cron: thread.Service.Start returned empty thread id")
	}
	return BootstrapResult{
		ThreadID: threadID,
		AgentID:  strings.TrimSpace(res.AgentID),
	}, nil
}

// decodeBootstrapConfig turns the job row's raw config JSON into the
// map[string]any shape thread.StartRequest expects. An empty/nil input
// is legal \u2014 the thread layer interprets nil as \"no overrides\". A
// syntactically-invalid blob is rejected so a corrupt row surfaces as
// a bootstrap error instead of silently dropping codexHome.
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
