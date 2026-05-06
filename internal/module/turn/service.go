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
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	turnobservation "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	turndedupe "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// skillHydrationPort 是 turn service 平时读取 skill 元数据/正文的最小入口。
//
// p20.2 §5 step 2：PrepareTurn 前置需要把 name-only skill 补全为
// `{Prompt, Summary, Version}`。该端口已上移到 skill 模块，避免 turn
// 构造器继续依赖完整 skill.Service。
type skillHydrationPort = skillpkg.SkillHydrationSource

type service struct {
	logger                 *slog.Logger
	assembler              *inputAssembler
	skills                 *skillResolver
	manifest               *manifestBuilder
	tracker                *turnTracker
	promptAssembly         contract.PromptAssemblyService
	turnContextProvider    contract.TurnContextProvider
	skillLookup            skillHydrationPort
	observation            turnobservation.Contract
	interruptSettleTimeout time.Duration
	// dedupeStore is the optional durable mirror for dedupe_key -> local_turn_id.
	// nil when the deployment has not wired the turndedupe store; the tracker
	// alone handles same-process dedupe in that case. When set, StartTurn
	// upserts a registry row and Complete/Stall stamps terminal_at.
	dedupeStore turndedupe.Store

	// ctx/cancel bound to the service lifetime. Shutdown cancels ctx so
	// background goroutines (watchTurn) can exit instead of waiting out
	// full trackerTTL after the module stops.
	ctx       context.Context
	ctxCancel context.CancelFunc
}

// setDedupeStore is wired from fx via registerTurnDedupeStore after
// the Service is constructed. Kept as a package-private setter so
// the public constructor surface stays stable; non-fx callers can
// leave the field nil and the tracker continues to service dedupe
// lookups on its own.
func (s *service) setDedupeStore(store turndedupe.Store) {
	if s == nil {
		return
	}
	s.dedupeStore = store
}

type steerableSession interface {
	Steer(ctx context.Context, req dto.SteerRequest) error
}

func NewService(logger *slog.Logger) Service {
	return newService(logger, nil, nil, nil, nil)
}

func NewServiceWithPromptAssembly(logger *slog.Logger, promptAssembly contract.PromptAssemblyService) Service {
	return newService(logger, promptAssembly, nil, nil, nil)
}

// NewServiceWithPromptAssemblyAndTurnContext p20.2 §5 step 1：skill.Service
// 参数按 fx `optional:"true"` 注入，用于 PrepareTurn 的 name-only skill
// hydrate；observation.Contract 同样 optional，用于 P21 canonical facts。
func NewServiceWithPromptAssemblyAndTurnContext(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	turnContextProvider contract.TurnContextProvider,
	skillSvc skillpkg.SkillHydrationSource,
	observation turnobservation.Contract,
) Service {
	var lookup skillHydrationPort
	if skillSvc != nil {
		lookup = skillSvc
	}
	return newService(logger, promptAssembly, turnContextProvider, lookup, observation)
}

func newService(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	turnContextProvider contract.TurnContextProvider,
	skillLookup skillHydrationPort,
	observation turnobservation.Contract,
) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := &service{
		logger:                 logger,
		assembler:              &inputAssembler{},
		skills:                 &skillResolver{},
		manifest:               newManifestBuilder(resolveBinaryDir()),
		tracker:                newTurnTracker(),
		promptAssembly:         promptAssembly,
		skillLookup:            skillLookup,
		observation:            observation,
		interruptSettleTimeout: config.InterruptSettleTimeout,
		ctx:                    ctx,
		ctxCancel:              cancel,
	}
	if turnContextProvider != nil {
		svc.turnContextProvider = turnContextProvider
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
	// p20.2 §5 step 2-3：在 skillResolver.Resolve 之前先把 manual/name-only
	// skill 补全为带 Prompt/Summary/Version 的实体，避免 provider 侧遇到
	// Prompt=="" 时只能走 name-list fallback。hydrate 是 optional 依赖：
	// skillLookup==nil 时（NewService / NewServiceWithPromptAssembly 或 fx 未
	// 注入 skill.Service）原路直通。
	hydrated, hydrateErr := s.hydrateSkillRefs(skillpkg.WithCWD(ctx, input.CWD), input.Skills, input.ManualSkillSelection)
	if hydrateErr != nil {
		return dto.TurnRequest{}, hydrateErr
	}
	input.Skills = hydrated
	candidateSkills := input.CandidateSkills
	if input.ManualSkillSelection {
		candidateSkills = nil
	}
	userText := s.assembler.PromptText(input)
	s.cleanupStaleToolResults(threadID, input)
	localID := platformshared.NewID("turn")
	mcp := s.manifest.Build(input, threadID)
	synthetic := s.syntheticMemoryContext(ctx, session, input, threadID, userText, mcp)
	resolvedSkills := s.skills.Resolve(input.Skills, candidateSkills, userText)
	assembledInputs := s.assembler.Assemble(input)
	if len(synthetic.Inputs) > 0 {
		assembledInputs = append(synthetic.Inputs, assembledInputs...)
	}
	req := dto.TurnRequest{
		LocalID:              localID,
		ThreadID:             threadID,
		Inputs:               assembledInputs,
		Skills:               resolvedSkills,
		ManualSkillSelection: input.ManualSkillSelection,
		OutputSchema:         input.OutputSchema,
		Overrides:            s.buildOverrides(session.Capabilities(), input),
		MCP:                  mcp,
		DedupeKey:            strings.TrimSpace(input.DedupeKey),
	}
	assembly, err := s.prepareTurnAssembly(ctx, threadID, input, userText, req)
	if err != nil {
		return dto.TurnRequest{}, err
	}
	if len(synthetic.Attachments) > 0 {
		assembly.Attachments = append(append([]dto.AttachmentEnvelope(nil), assembly.Attachments...), synthetic.Attachments...)
	}
	req.TurnAssembly = assembly
	s.recordSkillsSelected(req.LocalID, resolvedSkills)
	return req, nil
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
	// Stamp the dedupe key on the tracked turn before dispatching so a
	// concurrent LookupByDedupeKey can see this submission even if the
	// provider call is in flight. RegisterDedupeKey is a no-op when
	// req.DedupeKey is empty.
	s.tracker.RegisterDedupeKey(req.LocalID, req.DedupeKey)
	s.recordDedupeUpsert(ctx, req.DedupeKey, req.LocalID, req.ThreadID)
	handle, err := session.StartTurn(ctx, req)
	if err != nil {
		s.tracker.Complete(req.LocalID, false, err.Error())
		s.recordDedupeTerminal(ctx, req.DedupeKey)
		return nil, err
	}
	if handle == nil {
		err = errors.New("turn handle is nil")
		s.tracker.Complete(req.LocalID, false, err.Error())
		s.recordDedupeTerminal(ctx, req.DedupeKey)
		return nil, err
	}
	s.tracker.AttachHandle(req.LocalID, handle)
	providerID := handle.ProviderID()
	s.tracker.BindProviderID(req.LocalID, providerID)
	s.recordDedupeProviderID(ctx, req.DedupeKey, providerID)
	s.mapObservationTurn(req.LocalID, providerID)
	s.tracker.Update(req.LocalID, StateRunning)
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
		s.tracker.Update(active.localID, StateForceCompleting)
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

// LookupByDedupeKey resolves a dedupeKey to the in-memory tracker
// entry that registered it. See Service.LookupByDedupeKey for the
// caller contract — ok=false means "never submitted (in this
// process)", which is the scheduler's cue to proceed with a fresh
// StartTurn via the normal pending→submitting path.
func (s *service) LookupByDedupeKey(ctx context.Context, dedupeKey string) (TurnStatus, bool, error) {
	ctx = platformshared.NonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return TurnStatus{}, false, err
	}
	if status, ok := s.tracker.GetByDedupeKey(dedupeKey); ok {
		return status, true, nil
	}
	// Tracker miss. Fall back to the durable registry when wired so a
	// post-restart cron recovery can still resolve a previously-started
	// turn to its local_turn_id. Empty key / missing store returns
	// ok=false without reaching SQL.
	if s.dedupeStore == nil {
		return TurnStatus{}, false, nil
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return TurnStatus{}, false, nil
	}
	entry, err := s.dedupeStore.GetLive(ctx, key)
	if err != nil {
		if errors.Is(err, turndedupe.ErrNotFound) {
			return TurnStatus{}, false, nil
		}
		return TurnStatus{}, false, err
	}
	// Check if the registry hit is a "zombie" (the process that started it
	// died without marking it terminal). If it hasn't been updated within
	// trackerTTL, consider it expired. Returning ok=false allows the caller
	// to retry (StartTurn will upsert and overwrite the zombie row).
	if time.Since(entry.UpdatedAt) > trackerTTL {
		if s.logger != nil {
			s.logger.Warn("turn: dedupe registry hit is expired (zombie)", "dedupe_key", key, "updated_at", entry.UpdatedAt)
		}
		return TurnStatus{}, false, nil
	}

	// A registry hit is treated as "running" because terminal rows are
	// filtered at the SQL layer. Providing the tracker-shaped
	// TurnStatus here lets callers share a single code path.
	return TurnStatus{
		LocalID:    entry.LocalTurnID,
		ProviderID: entry.ProviderTurnID,
		State:      StateRunning,
	}, true, nil
}

// recordDedupeUpsert is the StartTurn-side mirror write to the durable
// registry. nil dedupeStore or empty key short-circuits so callers
// that didn't opt into dedupe pay no cost. Errors are logged and
// dropped — the tracker already holds the key, so durability is
// strictly best-effort.
func (s *service) recordDedupeUpsert(ctx context.Context, dedupeKey, localID, threadID string) {
	if s == nil || s.dedupeStore == nil {
		return
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return
	}
	err := s.dedupeStore.Upsert(ctx, turndedupe.UpsertParams{
		DedupeKey:   key,
		LocalTurnID: strings.TrimSpace(localID),
		ThreadID:    strings.TrimSpace(threadID),
		Now:         time.Now(),
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("turn: dedupe registry upsert failed",
			"dedupe_key", key, "local_id", localID, "error", err.Error())
	}
}

// recordDedupeProviderID updates the registry row with the provider
// turn id once StartTurn returns. Same best-effort semantics as
// recordDedupeUpsert.
func (s *service) recordDedupeProviderID(ctx context.Context, dedupeKey, providerID string) {
	if s == nil || s.dedupeStore == nil {
		return
	}
	key := strings.TrimSpace(dedupeKey)
	pid := strings.TrimSpace(providerID)
	if key == "" || pid == "" {
		return
	}
	err := s.dedupeStore.BindProviderTurnID(ctx, turndedupe.BindProviderTurnIDParams{
		DedupeKey:      key,
		ProviderTurnID: pid,
		Now:            time.Now(),
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("turn: dedupe registry bind provider id failed",
			"dedupe_key", key, "provider_id", pid, "error", err.Error())
	}
}

// recordDedupeTerminal stamps terminal_at on the registry row so
// future GetLive calls skip it. Resolves the dedupe key from the
// tracker when called without an explicit key argument. Safe to
// invoke even when nothing was previously upserted.
func (s *service) recordDedupeTerminal(ctx context.Context, dedupeKey string) {
	if s == nil || s.dedupeStore == nil {
		return
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return
	}
	if err := s.dedupeStore.MarkTerminal(ctx, key, time.Now()); err != nil {
		if s.logger != nil {
			s.logger.Warn("turn: dedupe registry mark terminal failed",
				"dedupe_key", key, "error", err.Error())
		}
	}
}

// recordDedupeTerminalForLocalID looks up the dedupe key on the
// tracker (the canonical source inside the process) and stamps the
// registry terminal. Used from watchTurn / waitForTurnSettle which
// only know the localID at the point of termination.
func (s *service) recordDedupeTerminalForLocalID(ctx context.Context, localID string) {
	if s == nil || s.dedupeStore == nil {
		return
	}
	key := s.tracker.DedupeKeyOf(localID)
	if key == "" {
		return
	}
	s.recordDedupeTerminal(ctx, key)
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
	svcCtx := s.ctx
	if svcCtx == nil {
		svcCtx = context.Background()
	}
	runtimesafe.SafeGo(svcCtx, s.logger, "turn.watchTurn", func(ctx context.Context) {
		timer := time.NewTimer(trackerTTL)
		defer timer.Stop()
		select {
		case <-timer.C:
			s.tracker.Stall(localID, "turn watch timed out")
			s.recordDedupeTerminalForLocalID(ctx, localID)
			s.logger.Warn("turn watcher TTL expired", "localID", localID)
			return
		case <-ctx.Done():
			// service shutdown; drop the watcher without marking the turn
			// stalled so the outer tracker entry expires naturally.
			return
		case <-handle.Done():
		}
		if err := handle.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				s.tracker.Update(localID, StateInterrupted)
			}
			s.tracker.Complete(localID, false, err.Error())
			s.recordDedupeTerminalForLocalID(ctx, localID)
			return
		}
		s.tracker.Complete(localID, true, "")
		s.recordDedupeTerminalForLocalID(ctx, localID)
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
		s.recordDedupeTerminalForLocalID(ctx, localID)
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
	if !contract.HasCapability(caps, dto.CapTurnOverride) {
		return dto.TurnOverrides{}
	}
	overrides := dto.TurnOverrides{}
	if model := strings.TrimSpace(input.Model); model != "" && contract.HasCapability(caps, dto.CapModelSwitch) {
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
	case StateCompleted, StateInterrupted, StateFailed, StateStalled:
		return true
	}
	return false
}

// Shutdown cancels the service-level ctx so background goroutines
// (watchTurn) can exit promptly instead of waiting out the full
// trackerTTL. Safe to call multiple times and on a nil service.
func (s *service) Shutdown() {
	if s == nil || s.ctxCancel == nil {
		return
	}
	s.ctxCancel()
}
