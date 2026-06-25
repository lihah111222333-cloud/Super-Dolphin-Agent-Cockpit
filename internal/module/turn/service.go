package turn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turnobservation "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	turndedupe "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// skillHydrationPort is the turn service's minimal dependency for resolving
// name-only skill references before provider submission.
type skillHydrationPort = contract.SkillHydrationSource

const peerBinDirEnv = "GO_AGENT_PEER_BIN_DIR"

// service 是 turn.Service 的核心实现，持有组装器、技能解析器、manifest 构建器和 tracker 等依赖。
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
	mcpServers             contract.MCPServerConfigProvider
	tracing                *platformobs.Service
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

type steerableSession interface {
	Steer(ctx context.Context, req dto.SteerRequest) error
}

// NewService 创建服务。
func NewService(logger *slog.Logger) Service {
	return newService(logger, nil, nil, nil, nil, nil, contract.BuildManifest, nil)
}

// NewServiceWithPromptAssembly 创建带promptassembly的服务。
func NewServiceWithPromptAssembly(logger *slog.Logger, promptAssembly contract.PromptAssemblyService) Service {
	return newService(logger, promptAssembly, nil, nil, nil, nil, contract.BuildManifest, nil)
}

// NewServiceWithPromptAssemblyAndTurnContext p20.2 §5 step 1：skill.Service
// 参数按 fx `optional:"true"` 注入，用于 PrepareTurn 的 name-only skill
// hydrate；observation.Contract 同样 optional，用于 P21 canonical facts。
func NewServiceWithPromptAssemblyAndTurnContext(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	turnContextProvider contract.TurnContextProvider,
	skillSvc contract.SkillHydrationSource,
	observation turnobservation.Contract,
	dedupeStore turndedupe.Store,
	manifestBuild contract.ManifestBuildFunc,
	mcpServers contract.MCPServerConfigProvider,
	tracing *platformobs.Service,
) Service {
	var lookup skillHydrationPort
	if skillSvc != nil {
		lookup = skillSvc
	}
	svc := newService(logger, promptAssembly, turnContextProvider, lookup, observation, dedupeStore, manifestBuild, tracing)
	if typed, ok := svc.(*service); ok {
		typed.mcpServers = mcpServers
	}
	return svc
}

// newService 创建服务。
func newService(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	turnContextProvider contract.TurnContextProvider,
	skillLookup skillHydrationPort,
	observation turnobservation.Contract,
	dedupeStore turndedupe.Store,
	manifestBuild contract.ManifestBuildFunc,
	tracingOpt ...*platformobs.Service,
) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	var tracing *platformobs.Service
	if len(tracingOpt) > 0 {
		tracing = tracingOpt[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	if manifestBuild == nil {
		manifestBuild = contract.BuildManifest
	}
	svc := &service{
		logger:                 logger,
		assembler:              &inputAssembler{},
		skills:                 &skillResolver{},
		manifest:               newManifestBuilder(resolveBinaryDir(), manifestBuild),
		tracker:                newTurnTracker(),
		promptAssembly:         promptAssembly,
		skillLookup:            skillLookup,
		observation:            observation,
		tracing:                tracing,
		dedupeStore:            dedupeStore,
		interruptSettleTimeout: ctxutil.InterruptSettleTimeout,
		ctx:                    ctx,
		ctxCancel:              cancel,
	}
	if turnContextProvider != nil {
		svc.turnContextProvider = turnContextProvider
	}
	return svc
}

// PrepareTurn 把用户输入、技能、MCP 和上下文组装成 provider turn 请求。
func (s *service) PrepareTurn(ctx context.Context, session contract.Session, input PrepareInput) (req dto.TurnRequest, err error) {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return dto.TurnRequest{}, err
	}
	span := s.beginTurnTraceSpan(ctx, "turn.prepare", threadID, input.AgentID, "", platformobs.NewCodeAnchor("internal/module/turn/service.go", "turn.(*service).PrepareTurn", 177), nil)
	ctx = span.ctx
	defer func() { s.finishTurnTraceSpan(span, err) }()
	input = hydratePrepareInput(input, session)
	input, err = s.hydrateMCPServerConfigs(ctx, input)
	if err != nil {
		return dto.TurnRequest{}, err
	}
	// V1 provider-native skill discovery 只在 turn 侧补全元数据，正文由
	// Claude/Codex 从 provider-native mirror 自行发现；hydrate 是 optional
	// 依赖：skillLookup==nil 时（NewService / NewServiceWithPromptAssembly
	// 或 fx 未注入 skill.Service）原路直通。
	hydrated, hydrateErr := s.hydrateSkillRefs(contract.WithSkillCWD(ctx, input.CWD), input.Skills, input.ManualSkillSelection)
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
	localID := idgen.NewID("turn")
	span.turnID = localID
	mcp := s.manifest.Build(input, threadID)
	span.metadata = turnPrepareTraceMetadata(input, mcp)
	synthetic := s.syntheticMemoryContext(ctx, session, input, threadID, userText, mcp)
	resolvedSkills := s.skills.Resolve(input.Skills, candidateSkills, userText)
	assembledInputs := s.assembler.Assemble(input)
	if len(synthetic.Inputs) > 0 {
		assembledInputs = append(synthetic.Inputs, assembledInputs...)
	}
	req = dto.TurnRequest{
		LocalID:                      localID,
		ThreadID:                     threadID,
		CWD:                          strings.TrimSpace(input.CWD),
		Inputs:                       assembledInputs,
		Skills:                       resolvedSkills,
		ManualSkillSelection:         input.ManualSkillSelection,
		OutputSchema:                 input.OutputSchema,
		Overrides:                    s.buildOverrides(session.Capabilities(), input),
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		MCP:                          mcp,
		DedupeKey:                    strings.TrimSpace(input.DedupeKey),
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

// StartTurn 提交已准备好的 turn，并把本地跟踪状态接到 provider handle 上。
func (s *service) StartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (handle contract.TurnHandle, err error) {
	ctx, threadID, err := requireTurnContext(ctx, session, req.ThreadID)
	req.LocalID = ensureLocalTurnID(req.LocalID)
	if err != nil {
		return nil, err
	}
	span := s.beginTurnTraceSpan(ctx, "turn.start", threadID, "", req.LocalID, platformobs.NewCodeAnchor("internal/module/turn/service.go", "turn.(*service).StartTurn", 254), nil)
	ctx = span.ctx
	defer func() { s.finishTurnTraceSpan(span, err) }()
	req.ThreadID = threadID
	s.tracker.Cleanup()
	s.tracker.Start(req.LocalID, "", req.ThreadID)
	// Stamp the dedupe key on the tracked turn before dispatching so a
	// concurrent LookupByDedupeKey can see this submission even if the
	// provider call is in flight. RegisterDedupeKey is a no-op when
	// req.DedupeKey is empty.
	s.tracker.RegisterDedupeKey(req.LocalID, req.DedupeKey)
	s.recordDedupeUpsert(ctx, req.DedupeKey, req.LocalID, req.ThreadID)
	handle, err = session.StartTurn(ctx, req)
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
	s.watchTurn(ctx, handle, req.LocalID, req.ThreadID)
	return handle, nil
}

// SteerTurn 处理steerturn。
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

// resolveActiveSteerTurn 查找当前线程的活跃 turn 并校验 expectedTurnID，不匹配时返回错误。
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

// requireSteerableSession 断言 session 实现了 steerableSession，不支持时返回错误。
func requireSteerableSession(session contract.Session) (steerableSession, error) {
	steerer, ok := session.(steerableSession)
	if !ok {
		return nil, errors.New("turn steer is not supported by session")
	}
	return steerer, nil
}

// ForceCompleteTurn 处理强制completeturn。
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

// TrackTurn 跟踪turn。
func (s *service) TrackTurn(ctx context.Context, localID string) (TurnStatus, error) {
	ctx = util.NonNilContext(ctx)
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
// LookupByDedupeKey 按去重键处理lookup。
func (s *service) LookupByDedupeKey(ctx context.Context, dedupeKey string) (TurnStatus, bool, error) {
	ctx = util.NonNilContext(ctx)
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
		State:      string(StateRunning),
	}, true, nil
}

// recordDedupeUpsert is the StartTurn-side mirror write to the durable
// registry. nil dedupeStore or empty key short-circuits so callers
// that didn't opt into dedupe pay no cost. Errors are logged and
// dropped — the tracker already holds the key, so durability is
// strictly best-effort.
func (s *service) recordDedupeUpsert(ctx context.Context, dedupeKey, localID, threadID string) error {
	if s == nil || s.dedupeStore == nil {
		return nil
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return nil
	}
	return s.dedupeStore.Upsert(ctx, turndedupe.UpsertParams{
		DedupeKey:   key,
		LocalTurnID: strings.TrimSpace(localID),
		ThreadID:    strings.TrimSpace(threadID),
		Now:         time.Now(),
	})
}

// recordDedupeProviderID updates the registry row with the provider
// turn id once StartTurn returns. Same best-effort semantics as
// recordDedupeUpsert.
func (s *service) recordDedupeProviderID(ctx context.Context, dedupeKey, providerID string) error {
	if s == nil || s.dedupeStore == nil {
		return nil
	}
	key := strings.TrimSpace(dedupeKey)
	pid := strings.TrimSpace(providerID)
	if key == "" || pid == "" {
		return nil
	}
	return s.dedupeStore.BindProviderTurnID(ctx, turndedupe.BindProviderTurnIDParams{
		DedupeKey:      key,
		ProviderTurnID: pid,
		Now:            time.Now(),
	})
}

// recordDedupeTerminal stamps terminal_at on the registry row so
// future GetLive calls skip it. Resolves the dedupe key from the
// tracker when called without an explicit key argument. Safe to
// invoke even when nothing was previously upserted.
func (s *service) recordDedupeTerminal(ctx context.Context, dedupeKey string) error {
	if s == nil || s.dedupeStore == nil {
		return nil
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return nil
	}
	return s.dedupeStore.MarkTerminal(ctx, key, time.Now())
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

// watchTurn 监听turn。
func (s *service) watchTurn(parentCtx context.Context, handle contract.TurnHandle, localID string, threadID string) {
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
	span := s.beginTurnTraceSpan(parentCtx, "turn.watch.completed", threadID, "", localID, platformobs.NewCodeAnchor("internal/module/turn/service.go", "turn.(*service).watchTurn", 518), nil)
	svcCtx := s.ctx
	if svcCtx == nil {
		svcCtx = context.Background()
	}
	safego.Go(svcCtx, s.logger, "turn.watchTurn", func(ctx context.Context) {
		timer := time.NewTimer(trackerTTL)
		defer timer.Stop()
		select {
		case <-timer.C:
			s.tracker.Stall(localID, "turn watch timed out")
			s.recordDedupeTerminalForLocalID(ctx, localID)
			s.logger.Warn("turn watcher TTL expired", "localID", localID)
			s.finishTurnTraceSpan(span, errors.New("turn watch timed out"))
			return
		case <-ctx.Done():
			// service shutdown; mark the turn stalled so the frontend can
			// clear the loading state instead of hanging indefinitely.
			s.tracker.Stall(localID, "service_shutdown")
			s.recordDedupeTerminalForLocalID(context.Background(), localID)
			s.finishTurnTraceSpan(span, ctx.Err())
			return
		case <-handle.Done():
		}
		if err := handle.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				s.tracker.Update(localID, StateInterrupted)
			}
			s.tracker.Complete(localID, false, err.Error())
			s.recordDedupeTerminalForLocalID(ctx, localID)
			s.finishTurnTraceSpan(span, err)
			return
		}
		s.tracker.Complete(localID, true, "")
		s.recordDedupeTerminalForLocalID(ctx, localID)
		s.finishTurnTraceSpan(span, nil)
	})
}

// waitForTurnSettle 等待 handle 完成并更新 tracker 状态，超时后返回 DeadlineExceeded。
func (s *service) waitForTurnSettle(ctx context.Context, localID string, handle contract.TurnHandle) error {
	deadline := time.Now().Add(s.interruptSettleTimeout)
	ctx = util.NonNilContext(ctx)
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

// waitForTrackedTerminal 等待已追踪 turn 进入终态。
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

// buildOverrides 根据会话能力集构造 TurnOverrides，仅在有 CapTurnOverride 能力时填充 model/effort。
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

// Shutdown cancels the service-level ctx so background goroutines
// (watchTurn) can exit promptly instead of waiting out the full
// trackerTTL. Safe to call multiple times and on a nil service.
// Shutdown 发送 LSP 关闭请求。
func (s *service) Shutdown() {
	if s == nil || s.ctxCancel == nil {
		return
	}
	s.ctxCancel()
}

type turnTraceSpan struct {
	ctx       context.Context
	trace     platformobs.TraceContext
	kind      string
	threadID  string
	agentID   string
	turnID    string
	code      platformobs.CodeAnchor
	metadata  map[string]any
	startedAt time.Time
}

// beginTurnTraceSpan 创建链路追踪 span 并向 tracing service 发送 begin 事件。
func (s *service) beginTurnTraceSpan(ctx context.Context, kind, threadID, agentID, turnID string, code platformobs.CodeAnchor, metadata map[string]any) turnTraceSpan {
	ctx = util.NonNilContext(ctx)
	trace, ok := platformobs.TraceFromContext(ctx)
	parentSpanID := ""
	if ok {
		parentSpanID = trace.SpanID
	}
	if trace.TraceID == "" {
		trace.TraceID = idgen.NewID("trace")
	}
	trace.ParentSpanID = parentSpanID
	trace.SpanID = idgen.NewID("span")
	span := turnTraceSpan{ctx: platformobs.ContextWithTrace(ctx, trace), trace: trace, kind: kind, threadID: strings.TrimSpace(threadID), agentID: strings.TrimSpace(agentID), turnID: strings.TrimSpace(turnID), code: code, metadata: metadata, startedAt: time.Now()}
	s.recordTurnTraceEvent(span, "begin", platformobs.StatusOK, 0, "")
	return span
}

// finishTurnTraceSpan 向 tracing service 发送 done 或 error 事件以结束 span。
func (s *service) finishTurnTraceSpan(span turnTraceSpan, err error) {
	status := platformobs.StatusOK
	message := ""
	phase := "done"
	if err != nil {
		status = platformobs.StatusError
		message = err.Error()
		phase = "error"
	}
	s.recordTurnTraceEvent(span, phase, status, time.Since(span.startedAt).Milliseconds(), message)
}

// recordTurnTraceEvent 向 tracing service 写入追踪事件，service 或 tracing 为 nil 时静默跳过。
func (s *service) recordTurnTraceEvent(span turnTraceSpan, phase string, status platformobs.Status, durationMS int64, message string) {
	if s == nil || s.tracing == nil {
		return
	}
	event := platformobs.TraceEvent{SchemaVersion: platformobs.SchemaVersion, Timestamp: time.Now(), TraceID: span.trace.TraceID, SpanID: span.trace.SpanID, ParentSpanID: span.trace.ParentSpanID, Kind: span.kind, Phase: phase, Method: span.kind, ThreadID: span.threadID, AgentID: span.agentID, TurnID: span.turnID, DurationMS: durationMS, Status: status, Error: message, Code: span.code, Metadata: span.metadata}
	if err := s.tracing.Record(span.ctx, event); err != nil && s.logger != nil {
		s.logger.Warn("turn trace record failed", "kind", span.kind, "phase", phase, "error", err)
	}
}

// turnPrepareTraceMetadata 构造 PrepareTurn 追踪事件的 metadata map，记录输入数量和工具数量。
func turnPrepareTraceMetadata(input PrepareInput, manifest dto.MCPManifest) map[string]any {
	return map[string]any{"input_count": len(input.Inputs), "file_count": len(input.Files), "image_count": len(input.Images), "skill_count": len(input.Skills) + len(input.CandidateSkills), "manifest_tool_count": len(manifest.Binaries)}
}
