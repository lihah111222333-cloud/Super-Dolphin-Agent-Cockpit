package claudecli

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var claudeCapabilities = dto.CapabilitySet{
	dto.CapMessageSend:  true,
	dto.CapModelSwitch:  true,
	dto.CapTurnOverride: true,
}

type driver struct {
	logger          *slog.Logger
	binaryPath      string
	eventDispatcher *unified.EventDispatcher
	reporter        contract.RuntimeReporter
}

type startSpec struct {
	agentID      string
	threadID     string
	publicThread string
	cwd          string
	model        string
	instructions string
	manifest     dto.MCPManifest
	config       cliLaunchConfig
	rawConfig    map[string]any
	historyDir   string
}

func NewDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, reporter contract.RuntimeReporter) contract.Driver {
	return newDriver(logger, eventDispatcher, reporter)
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, reporter contract.RuntimeReporter) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &driver{
		logger:          logger,
		binaryPath:      resolveBinaryPath(),
		eventDispatcher: eventDispatcher,
		reporter:        reporter,
	}
}

func (d *driver) Name() string { return "claude" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	manifest := dto.BuildManifest(dto.ManifestContext{
		AgentID:     strings.TrimSpace(req.AgentID),
		CWD:         strings.TrimSpace(req.CWD),
		ThreadCaps:  copyCapabilities(claudeCapabilities),
		BinaryDir:   providershared.ResolveBinaryDir(req.CWD, req.Config),
		Env:         providershared.StringMap(req.Config["env"]),
		AutoApprove: providershared.ConfigStringSlice(req.Config, "auto_approve", "autoApprove"),
	})
	return d.start(ctx, startSpec{
		agentID:      req.AgentID,
		cwd:          req.CWD,
		model:        req.Model,
		instructions: req.Instructions,
		manifest:     manifest,
		config:       configFromMap(req.Config),
		rawConfig:    cloneConfigMap(req.Config),
		publicThread: req.AgentID,
		historyDir:   providershared.ConfigString(req.Config, "history_dir", "claude_home"),
	})
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	manifest := dto.BuildManifest(dto.ManifestContext{
		AgentID:    strings.TrimSpace(req.AgentID),
		CWD:        strings.TrimSpace(req.CWD),
		ThreadCaps: copyCapabilities(claudeCapabilities),
		BinaryDir:  providershared.ResolveBinaryDir(req.CWD, nil),
	})
	return d.start(ctx, startSpec{
		agentID:      req.AgentID,
		threadID:     shared.FirstNonEmpty(req.ProviderThreadID, req.ThreadID),
		publicThread: req.ThreadID,
		cwd:          req.CWD,
		model:        req.Model,
		manifest:     manifest,
	})
}

func (d *driver) start(ctx context.Context, spec startSpec) (contract.Session, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	tr, cleanup, err := launchCLI(
		d.binaryPath,
		spec.cwd,
		spec.model,
		spec.instructions,
		spec.config,
		spec.manifest,
		spec.threadID,
	)
	if err != nil {
		return nil, err
	}
	initialThreadID := fallbackThreadID(spec.agentID, spec.threadID)
	publicThreadID := shared.FirstNonEmpty(spec.publicThread, spec.agentID, initialThreadID)
	s := &session{
		agentID:         strings.TrimSpace(spec.agentID),
		threadID:        initialThreadID,
		publicThreadID:  strings.TrimSpace(publicThreadID),
		sessionID:       initialThreadID,
		threadReady:     make(chan struct{}),
		transport:       tr,
		caps:            copyCapabilities(claudeCapabilities),
		history:         &historyBackend{sessionDir: spec.historyDir},
		logger:          d.logger,
		eventDispatcher: d.eventDispatcher,
		binaryPath:      d.binaryPath,
		cwd:             resolveAbsCWD(spec.cwd),
		model:           strings.TrimSpace(spec.model),
		instructions:    strings.TrimSpace(spec.instructions),
		config:          spec.config,
		rawConfig:       cloneConfigMap(spec.rawConfig),
		manifest:        spec.manifest,
		cleanup:         cleanup,
		suppressedTurns: map[string]struct{}{},
	}
	if shouldMarkThreadReady(spec.threadID, publicThreadID) {
		s.markThreadReady()
	}
	s.startReadLoop(tr)
	if err := s.awaitResolvedThreadID(ctx); err != nil {
		shared.LogIgnoredError(d.logger, "stop failed on start error", s.stop(true))
		return nil, err
	}
	resolvedThreadID := s.ThreadID()
	eventThreadID := s.EventThreadID()
	s.dispatch(dto.RawProviderEvent{
		EventType: "agent:launched",
		Data: map[string]any{
			"agent_id":   s.agentID,
			"thread_id":  eventThreadID,
			"session_id": resolvedThreadID,
			"timestamp":  time.Now().Format(time.RFC3339Nano),
			"cwd":        s.cwd,
			"model":      s.model,
		},
	})
	d.reportRuntime(s.agentID)
	return s, nil
}

func (d *driver) reportRuntime(agentID string) {
	if d == nil || d.reporter == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// TODO: Claude CLI is stdio-backed today. Report the real runtime/control
	// port once the provider protocol exposes a stable port or side channel.
	if err := d.reporter.ReportRuntime(ctx, contract.RuntimeReport{
		AgentID:  agentID,
		Provider: d.Name(),
	}); err != nil {
		d.logger.Warn("claudecli: report runtime failed", "agent_id", agentID, "error", err)
	}
}

var _ contract.Driver = (*driver)(nil)
