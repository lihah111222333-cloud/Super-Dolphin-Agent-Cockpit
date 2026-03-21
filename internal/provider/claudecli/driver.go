package claudecli

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
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
}

type startSpec struct {
	agentID      string
	threadID     string
	cwd          string
	model        string
	instructions string
	manifest     dto.MCPManifest
	config       cliLaunchConfig
	historyDir   string
}

func NewDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher) contract.Driver {
	return newDriver(logger, eventDispatcher)
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher) contract.Driver {
	if logger == nil {
		logger = slog.Default()
	}
	return &driver{
		logger:          logger,
		binaryPath:      resolveBinaryPath(),
		eventDispatcher: eventDispatcher,
	}
}

func (d *driver) Name() string { return "claude" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manifest := dto.BuildManifest(dto.ManifestContext{
		AgentID:    strings.TrimSpace(req.AgentID),
		CWD:        strings.TrimSpace(req.CWD),
		ThreadCaps: copyCapabilities(claudeCapabilities),
		BinaryDir:  resolveBinaryDir(req.CWD, req.Config),
		Env:        stringMap(req.Config["env"]),
	})
	return d.start(ctx, startSpec{
		agentID:      req.AgentID,
		cwd:          req.CWD,
		model:        req.Model,
		instructions: req.Instructions,
		manifest:     manifest,
		config:       configFromMap(req.Config),
		historyDir:   configString(req.Config, "history_dir", "claude_home"),
	})
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return d.start(ctx, startSpec{
		agentID:  req.AgentID,
		threadID: req.ThreadID,
		model:    req.Model,
	})
}

func (d *driver) start(ctx context.Context, spec startSpec) (contract.Session, error) {
	if err := ctx.Err(); err != nil {
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
	s := &session{
		agentID:         strings.TrimSpace(spec.agentID),
		threadID:        initialThreadID,
		sessionID:       initialThreadID,
		threadReady:     make(chan struct{}),
		transport:       tr,
		caps:            copyCapabilities(claudeCapabilities),
		history:         &historyBackend{sessionDir: spec.historyDir},
		logger:          d.logger,
		eventDispatcher: d.eventDispatcher,
		binaryPath:      d.binaryPath,
		cwd:             strings.TrimSpace(spec.cwd),
		model:           strings.TrimSpace(spec.model),
		instructions:    strings.TrimSpace(spec.instructions),
		config:          spec.config,
		manifest:        spec.manifest,
		cleanup:         cleanup,
		suppressedTurns: map[string]struct{}{},
	}
	if !requiresResolvedThreadID(spec.threadID) {
		s.markThreadReady()
	}
	s.startReadLoop(tr)
	if err := s.awaitResolvedThreadID(ctx); err != nil {
		_ = s.stop(true)
		return nil, err
	}
	resolvedThreadID := s.ThreadID()
	s.dispatch(dto.RawProviderEvent{
		Type: "agent:launched",
		Data: map[string]any{
			"agent_id":   s.agentID,
			"thread_id":  resolvedThreadID,
			"session_id": resolvedThreadID,
			"timestamp":  time.Now().Format(time.RFC3339Nano),
			"cwd":        s.cwd,
			"model":      s.model,
		},
	})
	return s, nil
}

var _ contract.Driver = (*driver)(nil)
