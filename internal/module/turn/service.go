package turn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	memorypkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type service struct {
	logger                 *slog.Logger
	assembler              *inputAssembler
	skills                 *skillResolver
	manifest               *manifestBuilder
	tracker                *turnTracker
	promptAssembly         contract.PromptAssemblyService
	prepareMemoryContext   func(context.Context, contract.Session, contract.BuildCtx, string, string) memorypkg.TurnContextPayload
	interruptSettleTimeout time.Duration
}

type steerableSession interface {
	Steer(ctx context.Context, req dto.SteerRequest) error
}

func NewService(logger *slog.Logger) Service {
	return newService(logger, nil, nil)
}

func NewServiceWithPromptAssembly(logger *slog.Logger, promptAssembly contract.PromptAssemblyService) Service {
	return newService(logger, promptAssembly, nil)
}

func NewServiceWithPromptAssemblyAndMemoryContext(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	memoryContext *memorypkg.MemoryContextProvider,
) Service {
	return newService(logger, promptAssembly, memoryContext)
}

func newService(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	memoryContext *memorypkg.MemoryContextProvider,
) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	svc := &service{
		logger:                 logger,
		assembler:              &inputAssembler{},
		skills:                 &skillResolver{},
		manifest:               newManifestBuilder(resolveBinaryDir()),
		tracker:                newTurnTracker(),
		promptAssembly:         promptAssembly,
		interruptSettleTimeout: config.InterruptSettleTimeout,
	}
	if memoryContext != nil {
		svc.prepareMemoryContext = memoryContext.PrepareTurnContext
	}
	return svc
}

func resolveBinaryDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

func (s *service) PrepareTurn(ctx context.Context, session contract.Session, input PrepareInput) (dto.TurnRequest, error) {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return dto.TurnRequest{}, err
	}
	input = hydratePrepareInput(input, session)
	candidateSkills := input.CandidateSkills
	if input.ManualSkillSelection {
		candidateSkills = nil
	}
	userText := s.assembler.PromptText(input)
	s.cleanupStaleToolResults(threadID, input)
	mcp := s.manifest.Build(input)
	synthetic := s.syntheticMemoryContext(ctx, session, input, threadID, userText, mcp)
	resolvedSkills := s.skills.Resolve(input.Skills, candidateSkills, userText)
	assembledInputs := s.assembler.Assemble(input)
	if len(synthetic.Inputs) > 0 {
		assembledInputs = append(synthetic.Inputs, assembledInputs...)
	}
	req := dto.TurnRequest{
		LocalID:              platformshared.NewID("turn"),
		ThreadID:             threadID,
		Inputs:               assembledInputs,
		Skills:               resolvedSkills,
		ManualSkillSelection: input.ManualSkillSelection,
		OutputSchema:         input.OutputSchema,
		Overrides:            s.buildOverrides(session.Capabilities(), input),
		MCP:                  mcp,
	}
	assembly, err := s.prepareTurnAssembly(ctx, threadID, input, userText, req)
	if err != nil {
		return dto.TurnRequest{}, err
	}
	if len(synthetic.Attachments) > 0 {
		assembly.Attachments = append(append([]dto.AttachmentEnvelope(nil), assembly.Attachments...), synthetic.Attachments...)
	}
	req.TurnAssembly = assembly
	return req, nil
}

func turnSkillPrompt(skills []dto.SkillRef) string {
	parts := make([]string, 0, len(skills))
	for _, skill := range skills {
		if prompt := strings.TrimSpace(skill.Prompt); prompt != "" {
			parts = append(parts, prompt)
		}
	}
	return strings.Join(parts, "\n\n")
}

func turnAttachmentRefs(inputs []dto.InputItem) []string {
	refs := make([]string, 0, len(inputs))
	for _, item := range inputs {
		for _, candidate := range []string{strings.TrimSpace(item.Path), strings.TrimSpace(item.URL), strings.TrimSpace(item.Name)} {
			if candidate != "" {
				refs = append(refs, candidate)
				break
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

func (s *service) cleanupStaleToolResults(threadID string, input PrepareInput) {
	result := cleanupToolResultLifecycle(threadID, input.Model, input.FRCConfig)
	if s == nil || s.logger == nil || result.Cleared == 0 {
		return
	}
	s.logger.Debug("turn tool-result lifecycle cleanup", "thread_id", threadID, "cleared", result.Cleared, "kept", result.Kept, "deleted_files", result.DeletedFiles)
}

func turnMCPSnapshot(snapshot contract.MCPSnapshot, manifest dto.MCPManifest) contract.MCPSnapshot {
	cloned := cloneMCPSnapshot(snapshot)
	servers := make([]string, 0, len(manifest.Binaries))
	for _, binary := range manifest.Binaries {
		if name := strings.TrimSpace(binary.Name); name != "" {
			servers = append(servers, name)
		}
	}
	cloned.Servers = providershared.NormalizeConfigStringSlice(servers)
	if len(cloned.Servers) == 0 {
		cloned.Servers = nil
	}
	return cloned
}

func (s *service) StartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error) {
	ctx, threadID, err := requireTurnContext(ctx, session, req.ThreadID)
	req.LocalID = ensureLocalTurnID(req.LocalID)
	if err != nil {
		return nil, err
	}
	req.ThreadID = threadID
	s.tracker.Cleanup()
	s.tracker.Start(req.LocalID, "", req.ThreadID)
	handle, err := session.StartTurn(ctx, req)
	if err != nil {
		s.tracker.Complete(req.LocalID, false, err.Error())
		return nil, err
	}
	if handle == nil {
		err = errors.New("turn handle is nil")
		s.tracker.Complete(req.LocalID, false, err.Error())
		return nil, err
	}
	s.tracker.AttachHandle(req.LocalID, handle)
	s.tracker.BindProviderID(req.LocalID, handle.ProviderID())
	s.tracker.Update(req.LocalID, "running")
	s.watchTurn(handle, req.LocalID)
	return handle, nil
}

func (s *service) SteerTurn(ctx context.Context, session contract.Session, expectedTurnID string, input PrepareInput) (contract.TurnHandle, error) {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return nil, err
	}
	active, err := s.resolveActiveSteerTurn(threadID, expectedTurnID)
	if err != nil {
		return nil, err
	}
	req, err := s.PrepareTurn(ctx, session, input)
	if err != nil {
		return nil, err
	}
	steerer, err := requireSteerableSession(session)
	if err != nil {
		return nil, err
	}
	if err := steerer.Steer(ctx, newSteerRequest(req, active.handle.ProviderID())); err != nil {
		return nil, err
	}
	return active.handle, nil
}

func (s *service) resolveActiveSteerTurn(threadID, expectedTurnID string) (activeTurn, error) {
	active, tracked := s.tracker.ActiveByThread(threadID)
	if !tracked {
		return activeTurn{}, errors.New("no active turn to steer")
	}
	if active.handle == nil {
		return activeTurn{}, errors.New("active turn handle is nil")
	}
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	if expectedTurnID != "" && !strings.EqualFold(expectedTurnID, active.localID) {
		return activeTurn{}, fmt.Errorf("expectedTurnId mismatch: expected %s, active %s", expectedTurnID, active.localID)
	}
	return active, nil
}

func requireSteerableSession(session contract.Session) (steerableSession, error) {
	steerer, ok := session.(steerableSession)
	if !ok {
		return nil, errors.New("turn steer is not supported by session")
	}
	return steerer, nil
}

func (s *service) ForceCompleteTurn(ctx context.Context, session contract.Session) error {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return err
	}
	active, tracked := s.tracker.ActiveByThread(threadID)
	req := dto.ForceCompleteRequest{ThreadID: threadID}
	if tracked {
		s.tracker.Update(active.localID, "force_completing")
		if active.handle != nil {
			req.ProviderID = strings.TrimSpace(active.handle.ProviderID())
		}
	}
	if err := session.ForceComplete(ctx, req); err != nil {
		return err
	}
	if !tracked {
		return nil
	}
	return s.waitForTurnSettle(ctx, active.localID, active.handle)
}

func (s *service) TrackTurn(ctx context.Context, localID string) (TurnStatus, error) {
	ctx = platformshared.NonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return TurnStatus{}, err
	}
	status, ok := s.tracker.Get(localID)
	if !ok {
		return TurnStatus{}, errors.New("turn not found")
	}
	return status, nil
}

func (s *service) watchTurn(handle contract.TurnHandle, localID string) {
	if handle == nil {
		return
	}
	localID = strings.TrimSpace(localID)
	if localID == "" {
		localID = strings.TrimSpace(handle.LocalID())
	}
	if localID == "" {
		return
	}
	platformshared.SafeGo(s.logger, func() {
		timer := time.NewTimer(trackerTTL)
		defer timer.Stop()
		select {
		case <-timer.C:
			s.tracker.Stall(localID, "turn watch timed out")
			s.logger.Warn("turn watcher TTL expired", "localID", localID)
			return
		case <-handle.Done():
		}
		if err := handle.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				s.tracker.Update(localID, "interrupted")
			}
			s.tracker.Complete(localID, false, err.Error())
			return
		}
		s.tracker.Complete(localID, true, "")
	})
}

func (s *service) waitForTurnSettle(ctx context.Context, localID string, handle contract.TurnHandle) error {
	deadline := time.Now().Add(s.interruptSettleTimeout)
	ctx = platformshared.NonNilContext(ctx)
	if err := waitForHandle(ctx, handle, deadline); err != nil && handle != nil {
		return err
	}
	if handle != nil {
		if err := handle.Err(); err != nil {
			s.tracker.Complete(localID, false, err.Error())
		} else {
			s.tracker.Complete(localID, true, "")
		}
	}
	_, err := s.waitForTrackedTerminal(ctx, localID, deadline)
	return err
}

func waitForHandle(ctx context.Context, handle contract.TurnHandle, deadline time.Time) error {
	if handle == nil {
		return nil
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-handle.Done():
		return nil
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func (s *service) waitForTrackedTerminal(ctx context.Context, localID string, deadline time.Time) (TurnStatus, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		if status, ok := s.tracker.Get(localID); ok && isTerminalTurnState(status.State) {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return TurnStatus{}, ctx.Err()
		case <-timer.C:
			return TurnStatus{}, context.DeadlineExceeded
		case <-ticker.C:
		}
	}
}

func (s *service) buildOverrides(caps dto.CapabilitySet, input PrepareInput) dto.TurnOverrides {
	if !caps.Has(dto.CapTurnOverride) {
		return dto.TurnOverrides{}
	}
	overrides := dto.TurnOverrides{}
	if model := strings.TrimSpace(input.Model); model != "" && caps.Has(dto.CapModelSwitch) {
		overrides.Model = model
	}
	if effort := strings.TrimSpace(input.Effort); effort != "" {
		overrides.Effort = effort
	}
	return overrides
}

func ensureLocalTurnID(localID string) string {
	if localID = strings.TrimSpace(localID); localID != "" {
		return localID
	}
	return platformshared.NewID("turn")
}

func isTerminalTurnState(state string) bool {
	switch strings.TrimSpace(state) {
	case "completed", "interrupted", "failed", "stalled":
		return true
	}
	return false
}
