package turn

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type service struct {
	logger    *slog.Logger
	assembler *inputAssembler
	skills    *skillResolver
	manifest  *manifestBuilder
	tracker   *turnTracker
}

func NewService(logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &service{
		logger:    logger,
		assembler: &inputAssembler{},
		skills:    &skillResolver{},
		manifest:  &manifestBuilder{},
		tracker:   newTurnTracker(),
	}
}

func (s *service) PrepareTurn(ctx context.Context, session contract.Session, input PrepareInput) (dto.TurnRequest, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return dto.TurnRequest{}, err
	}
	if err := requireSession(session); err != nil {
		return dto.TurnRequest{}, err
	}
	threadID, err := resolveThreadID(session, "")
	if err != nil {
		return dto.TurnRequest{}, err
	}
	resolvedSkills := s.skills.Resolve(input.Skills, input.CandidateSkills, s.assembler.PromptText(input))
	return dto.TurnRequest{
		LocalID:      shareddto.NewID("turn"),
		ThreadID:     threadID,
		Inputs:       s.assembler.Assemble(input),
		Skills:       resolvedSkills,
		OutputSchema: input.OutputSchema,
		Overrides:    s.buildOverrides(session.Capabilities(), input),
		MCP:          s.manifest.Build(input),
	}, nil
}

func (s *service) StartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := requireSession(session); err != nil {
		return nil, err
	}
	req.LocalID = ensureLocalTurnID(req.LocalID)
	threadID, err := resolveThreadID(session, req.ThreadID)
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

func (s *service) SteerTurn(ctx context.Context, session contract.Session, prompt string) (contract.TurnHandle, error) {
	req, err := s.PrepareTurn(ctx, session, PrepareInput{Prompt: prompt})
	if err != nil {
		return nil, err
	}
	return s.StartTurn(ctx, session, req)
}

func (s *service) InterruptTurn(ctx context.Context, session contract.Session, source string) error {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireSession(session); err != nil {
		return err
	}
	threadID, err := resolveThreadID(session, "")
	if err != nil {
		return err
	}
	active, tracked := s.tracker.ActiveByThread(threadID)
	err = session.Interrupt(ctx, dto.InterruptRequest{
		ThreadID: threadID,
		Source:   strings.TrimSpace(source),
	})
	if err != nil {
		return err
	}
	if !tracked || !s.tracker.MarkInterruptRequested(active.localID) {
		return nil
	}
	return s.waitForTurnSettle(ctx, active.localID, active.handle)
}

func (s *service) ForceCompleteTurn(ctx context.Context, session contract.Session) error {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireSession(session); err != nil {
		return err
	}
	threadID, err := resolveThreadID(session, "")
	if err != nil {
		return err
	}
	return session.Interrupt(ctx, dto.InterruptRequest{
		ThreadID: threadID,
		Source:   "force_complete",
	})
}

func (s *service) TrackTurn(ctx context.Context, localID string) (TurnStatus, error) {
	ctx = normalizeContext(ctx)
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
	go func() {
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
	}()
}

func (s *service) waitForTurnSettle(ctx context.Context, localID string, handle contract.TurnHandle) error {
	deadline := time.Now().Add(config.InterruptSettleTimeout)
	ctx = normalizeContext(ctx)
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

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func requireSession(session contract.Session) error {
	if session == nil {
		return errors.New("session is required")
	}
	return nil
}

func resolveThreadID(session contract.Session, requested string) (string, error) {
	threadID := strings.TrimSpace(requested)
	if threadID == "" {
		threadID = strings.TrimSpace(session.ThreadID())
	}
	if threadID == "" {
		return "", errors.New("thread id is required")
	}
	return threadID, nil
}

func ensureLocalTurnID(localID string) string {
	if localID = strings.TrimSpace(localID); localID != "" {
		return localID
	}
	return shareddto.NewID("turn")
}

func isTerminalTurnState(state string) bool {
	switch strings.TrimSpace(state) {
	case "completed", "interrupted", "failed", "stalled":
		return true
	}
	return false
}
