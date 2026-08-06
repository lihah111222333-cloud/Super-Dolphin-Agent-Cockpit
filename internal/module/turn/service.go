package turn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turnobservation "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn/observation"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// skillHydrationPort 是 turn 服务解析 name-only skill 引用所需的最小依赖。
type skillHydrationPort = contract.SkillHydrationSource

// peerBinDirEnv 指定 provider manifest 查找内置 MCP peer 二进制的目录。
const peerBinDirEnv = "GO_AGENT_PEER_BIN_DIR"

// service 是 turn.Service 的核心实现，持有组装器、技能解析器、manifest 构建器和 tracker 等依赖。
// tracker 负责进程内状态收敛；dedupe store 一旦注入，StartTurn 的持久化写入失败会阻断提交。
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
	toolResults            *ToolResultRuntime
	interruptSettleTimeout time.Duration
	// dedupeStore 持久化 dedupe_key 到 local_turn_id 的镜像；未注入时只依赖进程内 tracker。
	// 注入后 StartTurn 写入 registry，Complete/Stall 路径写 terminal_at，供重启恢复避重。
	dedupeStore DedupeStore

	// ctx/cancel 绑定服务生命周期；Shutdown 取消后，watchTurn 等后台 goroutine 不必等满 trackerTTL。
	ctx       context.Context
	ctxCancel context.CancelFunc
}

// steerableSession 是支持向活跃 provider turn 追加输入的 session 能力。
type steerableSession interface {
	Steer(ctx context.Context, req dto.SteerRequest) error
}

// NewService 创建只包含默认组装能力的 turn 服务，主要用于轻量测试和旧 wiring。
func NewService(logger *slog.Logger, toolResults *ToolResultRuntime) Service {
	return newService(logger, nil, nil, nil, nil, nil, contract.BuildManifest, nil, toolResults)
}

// NewServiceWithPromptAssembly 创建带 prompt assembly 能力的 turn 服务。
func NewServiceWithPromptAssembly(logger *slog.Logger, promptAssembly contract.PromptAssemblyService, toolResults *ToolResultRuntime) Service {
	return newService(logger, promptAssembly, nil, nil, nil, nil, contract.BuildManifest, nil, toolResults)
}

// NewServiceWithPromptAssemblyAndTurnContext 创建完整 wiring 使用的 turn 服务。
// skill、observation、dedupe、MCP 和 tracing 依赖均允许 optional 注入，缺失时只跳过对应能力。
func NewServiceWithPromptAssemblyAndTurnContext(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	turnContextProvider contract.TurnContextProvider,
	skillSvc contract.SkillHydrationSource,
	observation turnobservation.Contract,
	dedupeStore DedupeStore,
	manifestBuild contract.ManifestBuildFunc,
	mcpServers contract.MCPServerConfigProvider,
	tracing *platformobs.Service,
	toolResults *ToolResultRuntime,
) Service {
	var lookup skillHydrationPort
	if skillSvc != nil {
		lookup = skillSvc
	}
	svc := newService(logger, promptAssembly, turnContextProvider, lookup, observation, dedupeStore, manifestBuild, tracing, toolResults)
	if typed, ok := svc.(*service); ok {
		typed.mcpServers = mcpServers
	}
	return svc
}

// newService 组装 turn 服务内部依赖，并创建服务级 context 供后台 watcher 统一退出。
func newService(
	logger *slog.Logger,
	promptAssembly contract.PromptAssemblyService,
	turnContextProvider contract.TurnContextProvider,
	skillLookup skillHydrationPort,
	observation turnobservation.Contract,
	dedupeStore DedupeStore,
	manifestBuild contract.ManifestBuildFunc,
	tracing *platformobs.Service,
	toolResults *ToolResultRuntime,
) Service {
	if toolResults == nil {
		// archguard:ignore panic_count -- turn 服务必须与 provider hook 共享显式 owner，缺失时不能继续运行。
		panic("tool result runtime is required")
	}
	if logger == nil {
		logger = pkglogger.Get()
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
		toolResults:            toolResults,
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
	if err := validatePrepareInputTypes(input.Inputs); err != nil {
		return dto.TurnRequest{}, err
	}
	input, err = s.hydrateMCPServerConfigs(ctx, input)
	if err != nil {
		return dto.TurnRequest{}, err
	}
	// provider-native skill discovery 只在 turn 侧补全元数据，正文由
	// Claude/Codex 从 provider-native mirror 自行发现；hydrate 是 optional 依赖，
	// skillLookup 未注入时原路直通。
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
	synthetic, err := s.syntheticMemoryContext(ctx, session, input, threadID, userText, mcp)
	if err != nil {
		return dto.TurnRequest{}, err
	}
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

// validatePrepareInputTypes 在 service 边界拒绝未知输入类型。
// RPC 入口已有同类校验；这里保护 orchestration、测试和 provider 直连等非 RPC 调用方。
func validatePrepareInputTypes(inputs []InputItem) error {
	for i, item := range inputs {
		if _, ok := shareddto.NormalizeInputType(item.Type); !ok {
			return fmt.Errorf("turn input[%d]: unsupported input type %q", i, strings.TrimSpace(item.Type))
		}
	}
	return nil
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
	// provider 调用尚未返回时也可能有并发 LookupByDedupeKey，因此先把 dedupe key 写入 tracker。
	// 空 key 在 RegisterDedupeKey 内是 no-op。
	s.tracker.RegisterDedupeKey(req.LocalID, req.DedupeKey)
	if err = s.recordDedupeUpsert(ctx, req.DedupeKey, req.LocalID, req.ThreadID); err != nil {
		s.tracker.Complete(req.LocalID, false, err.Error())
		return nil, err
	}
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
	if err = s.recordDedupeProviderID(ctx, req.DedupeKey, providerID); err != nil {
		interruptErr := session.Interrupt(ctx, dto.InterruptRequest{
			ThreadID: req.ThreadID,
			TurnID:   providerID,
			Source:   "dedupe_provider_id_bind_failed",
		})
		s.tracker.Complete(req.LocalID, false, err.Error())
		terminalErr := s.recordDedupeTerminal(ctx, req.DedupeKey)
		return nil, errors.Join(err, interruptErr, terminalErr)
	}
	s.mapObservationTurn(req.LocalID, providerID)
	s.tracker.Update(req.LocalID, StateRunning)
	s.watchTurn(ctx, handle, req.LocalID, req.ThreadID)
	return handle, nil
}

// SteerTurn 将新的输入追加到当前活跃 turn，expectedTurnID 不匹配时拒绝发送。
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

// ForceCompleteTurn 请求 provider 强制完成当前线程的活跃 turn，并等待 tracker 收敛。
// 若本地没有 tracked turn，只向 provider 发请求后返回，不制造新的 tracker 记录。
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

// TrackTurn 只读取本进程 tracker 快照；没有记录时返回明确错误，不查询 durable dedupe registry。
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

// LookupByDedupeKey 先查本进程 tracker，再查 durable registry，用于 cron recovery 避免重复提交。
// ok=false 表示可按正常 pending -> submitting 路径重新 StartTurn。
// registry 命中只代表“看起来仍在运行”，终态行会在 SQL 层被过滤。
func (s *service) LookupByDedupeKey(ctx context.Context, dedupeKey string) (TurnStatus, bool, error) {
	ctx = util.NonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return TurnStatus{}, false, err
	}
	if status, ok := s.tracker.GetByDedupeKey(dedupeKey); ok {
		return status, true, nil
	}
	// tracker 未命中时再查持久 registry，让重启后的 cron recovery 能识别已提交 turn。
	// 空 key 或未注入 store 直接返回 ok=false，不访问 SQL。
	if s.dedupeStore == nil {
		return TurnStatus{}, false, nil
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return TurnStatus{}, false, nil
	}
	entry, err := s.dedupeStore.GetLive(ctx, key)
	if err != nil {
		if errors.Is(err, ErrDedupeNotFound) {
			return TurnStatus{}, false, nil
		}
		return TurnStatus{}, false, err
	}
	// registry 命中但长时间未更新时，视为启动进程已退出且未写终态的僵尸记录。
	// 返回 ok=false 允许调用方重试，StartTurn 会 upsert 覆盖旧行。
	if time.Since(entry.UpdatedAt) > trackerTTL {
		if s.logger != nil {
			s.logger.Warn("turn: dedupe registry hit is expired (zombie)", "dedupe_key", key, "updated_at", entry.UpdatedAt)
		}
		return TurnStatus{}, false, nil
	}

	// SQL 层已过滤终态行，因此 registry 命中一律按 running 暴露成 tracker 形态。
	return TurnStatus{
		LocalID:    entry.LocalTurnID,
		ProviderID: entry.ProviderTurnID,
		State:      string(StateRunning),
	}, true, nil
}

// recordDedupeUpsert 在 StartTurn 侧把 dedupe key 写入持久 registry。
// 未注入 store 或 key 为空时直接跳过；store 写入失败时返回错误阻断提交，避免重启恢复重复启动。
func (s *service) recordDedupeUpsert(ctx context.Context, dedupeKey, localID, threadID string) error {
	if s == nil || s.dedupeStore == nil {
		return nil
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return nil
	}
	return s.dedupeStore.Upsert(ctx, DedupeUpsertParams{
		DedupeKey:   key,
		LocalTurnID: strings.TrimSpace(localID),
		ThreadID:    strings.TrimSpace(threadID),
		Now:         time.Now(),
	})
}

// recordDedupeProviderID 在 StartTurn 返回 provider turnID 后回写 durable registry。
// key 或 providerID 为空时跳过，避免把未完成的 provider 绑定写成脏行。
func (s *service) recordDedupeProviderID(ctx context.Context, dedupeKey, providerID string) error {
	if s == nil || s.dedupeStore == nil {
		return nil
	}
	key := strings.TrimSpace(dedupeKey)
	pid := strings.TrimSpace(providerID)
	if key == "" || pid == "" {
		return nil
	}
	return s.dedupeStore.BindProviderTurnID(ctx, DedupeBindProviderTurnIDParams{
		DedupeKey:      key,
		ProviderTurnID: pid,
		Now:            time.Now(),
	})
}

// recordDedupeTerminal 给 registry 行写入 terminal_at，确保后续 GetLive 不再把它当作运行中 turn。
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

// recordDedupeTerminalForLocalID 通过 tracker 反查 localID 对应的 dedupe key 并写终态。
// watchTurn 和 waitForTurnSettle 到终止点时只知道 localID，因此不能直接按 key 更新。
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

// watchTurn 启动后台 watcher 等待 provider handle 完成，并在超时或服务关闭时写入终态。
// watcher 最多等待 trackerTTL；服务关闭时会标记 stalled，让前端和 dedupe registry 都能收口。
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
			// 服务关闭时把 turn 标记为 stalled，前端才能清掉 loading 状态而不是无限等待。
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
// 它同时推进 dedupe registry 终态，保证 force/interrupt 路径和 watcher 的收敛口径一致。
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
// 当前使用 25ms 固定轮询；若 tracker 未来暴露 channel 通知机制，可直接替换 select 分支消除轮询延迟。
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

// Shutdown 取消服务级 context，让 watchTurn 等后台 goroutine 及时退出；可重复调用。
func (s *service) Shutdown() {
	if s == nil || s.ctxCancel == nil {
		return
	}
	s.ctxCancel()
}

// turnTraceSpan 保存一次 turn 追踪 span 的上下文和 begin/done 事件共享字段。
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
