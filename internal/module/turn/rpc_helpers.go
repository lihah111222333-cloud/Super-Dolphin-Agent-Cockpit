package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/configutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/pathutil"
)

// buildRPCPrepareInput 将 turn/start RPC 参数拆成普通输入和 skill 输入后构造 PrepareInput。
func buildRPCPrepareInput(p turnStartParams, session contract.Session, threadRuntimeConfig map[string]any) (PrepareInput, error) {
	items, inputSkills, err := buildTurnStartInputs(p.Input)
	if err != nil {
		return PrepareInput{}, err
	}
	return buildPrepareInput(prepareInputSpec{
		LocalTurnID:                  p.LocalTurnID,
		Inputs:                       items,
		Prompt:                       p.Prompt,
		Images:                       p.Images,
		Files:                        p.Files,
		ManualSkillSelection:         p.ManualSkillSelection,
		Provider:                     p.Provider,
		Model:                        p.Model,
		Effort:                       p.Effort,
		OutputSchema:                 p.OutputSchema,
		CWD:                          p.CWD,
		GitRoot:                      p.GitRoot,
		IsWorktree:                   p.IsWorktree,
		Language:                     p.Language,
		EnabledTools:                 p.EnabledTools,
		AdditionalWorkingDirectories: p.AdditionalWorkingDirectories,
		MCPSnapshot:                  p.MCPSnapshot,
		SessionFlags:                 p.SessionFlags,
		ThreadRuntimeConfig:          threadRuntimeConfig,
	}, prepareSkillSpec{
		Selected:     p.SelectedSkills,
		SelectedRefs: p.SelectedSkillRefs,
		Derived:      inputSkills,
	}, session), nil
}

// resolveTurnRPCCWD 使用线程运行时配置中的 cwd 作为权威值，拒绝请求携带的不一致 cwd。
func resolveTurnRPCCWD(requestCWD string, threadRuntimeConfig map[string]any) (string, error) {
	requestCWD = strings.TrimSpace(requestCWD)
	authoritativeCWD, err := strictRuntimeCWD(threadRuntimeConfig, "thread runtime config")
	if err != nil {
		return "", err
	}
	if authoritativeCWD == "" {
		return "", platformrpc.ErrInvalidParams("turn cwd missing: thread runtime config does not define cwd")
	}
	if requestCWD != "" && !sameTurnRPCCWD(requestCWD, authoritativeCWD) {
		return "", platformrpc.ErrInvalidParams(fmt.Sprintf("turn/start cwd mismatch: request cwd %q does not match thread cwd %q", requestCWD, authoritativeCWD))
	}
	return authoritativeCWD, nil
}

// sameTurnRPCCWD 比较请求 cwd 与权威 cwd，Windows 下按大小写不敏感路径处理。
func sameTurnRPCCWD(requestCWD, authoritativeCWD string) bool {
	if requestCWD == authoritativeCWD {
		return true
	}
	normalizedRequest, err := pathutil.NormalizeAbsolutePath(requestCWD)
	if err != nil || normalizedRequest == "" {
		return false
	}
	normalizedAuthoritative, err := pathutil.NormalizeAbsolutePath(authoritativeCWD)
	if err != nil || normalizedAuthoritative == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(normalizedRequest, normalizedAuthoritative)
	}
	return normalizedRequest == normalizedAuthoritative
}

// strictRuntimeCWD 从运行时配置读取 cwd，类型错误会转成 RPC invalid params。
func strictRuntimeCWD(cfg map[string]any, label string) (string, error) {
	cwd, err := configutil.StrictString(cfg, label, "cwd")
	if err != nil {
		return "", platformrpc.ErrInvalidParams(err.Error())
	}
	return cwd, nil
}

// buildTurnStartInputs 把 RPC input 拆为 provider 输入项和 name-only skill 请求。
func buildTurnStartInputs(raw []turnInputItemParams) ([]InputItem, []string, error) {
	items := make([]InputItem, 0, len(raw))
	skills := make([]string, 0, len(raw))
	for i, item := range raw {
		if item.isSkillType() {
			skill := item.skillName()
			if skill == "" {
				return nil, nil, platformrpc.ErrInvalidParams(fmt.Sprintf("turn input[%d] skill name is required", i))
			}
			skills = append(skills, skill)
			continue
		}
		input, ok, err := item.inputItem()
		if err != nil {
			return nil, nil, platformrpc.ErrInvalidParams(fmt.Sprintf("turn input[%d]: %s", i, err.Error()))
		}
		if ok {
			items = append(items, input)
		}
	}
	return items, skills, nil
}

// resolveTurnSession 立即解析当前线程 session，缺失时返回可展示的 RPC 状态错误。
func resolveTurnSession(ctx context.Context, resolver contract.SessionResolver) (contract.Session, error) {
	if resolver == nil {
		return nil, platformrpc.ErrInvalidState("turn rpc: session resolver is not configured")
	}
	session, err := resolver.ResolveSession(ctx, contract.ThreadIDFrom(ctx))
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, platformrpc.ErrInvalidState("thread session is not available; start or resume the thread first")
	}
	return session, nil
}

// withTurnSession 解析 session 后执行 RPC 回调，供无需等待 launch 的接口复用。
func withTurnSession(ctx context.Context, resolver contract.SessionResolver, fn func(context.Context, contract.Session) (any, error)) (any, error) {
	session, err := resolveTurnSession(ctx, resolver)
	if err != nil {
		return nil, err
	}
	return fn(ctx, session)
}

// resolveReadyTurnSession 在 pending launch 场景等待 session 出现，避免首 turn 抢跑。
func resolveReadyTurnSession(ctx context.Context, resolver contract.SessionResolver) (contract.Session, error) {
	if resolver == nil {
		return nil, platformrpc.ErrInvalidState("turn rpc: session resolver is not configured")
	}
	threadID := contract.ThreadIDFrom(ctx)
	session, err := lookupReadyTurnSession(ctx, resolver, threadID)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, contract.ErrSessionNotFound) {
		return nil, err
	}
	waitCtx, cancel := readyTurnWaitContext(ctx)
	defer cancel()
	return waitForReadyTurnSession(waitCtx, resolver, threadID)
}

// lookupReadyTurnSession 做一次 session 查询，nil session 会规范化为 ErrSessionNotFound。
func lookupReadyTurnSession(
	ctx context.Context,
	resolver contract.SessionResolver,
	threadID string,
) (contract.Session, error) {
	session, err := resolver.ResolveSession(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, contract.ErrSessionNotFound
	}
	return session, nil
}

// readyTurnWaitContext 给 ready 等待补默认超时，调用方已有 deadline 时不覆盖。
func readyTurnWaitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeoutIfNone(ctx, ctxutil.LaunchTimeout)
}

// waitForReadyTurnSession 等待会话 ready 后再提交 turn。
// 当前使用 50ms 固定轮询；若 SessionResolver 未来暴露 ready channel，可直接替换以消除轮询延迟。
func waitForReadyTurnSession(
	waitCtx context.Context,
	resolver contract.SessionResolver,
	threadID string,
) (contract.Session, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return nil, platformrpc.ErrInvalidState("thread session is not available; start or resume the thread first")
		case <-ticker.C:
			session, err := lookupReadyTurnSession(waitCtx, resolver, threadID)
			if err == nil {
				return session, nil
			}
			if !errors.Is(err, contract.ErrSessionNotFound) {
				return nil, err
			}
		}
	}
}

// withReadyTurnSession 等待 session 可用后执行回调，用于 start/steer 这类可触发 launch 的接口。
func withReadyTurnSession(ctx context.Context, resolver contract.SessionResolver, fn func(context.Context, contract.Session) (any, error)) (any, error) {
	session, err := resolveReadyTurnSession(ctx, resolver)
	if err != nil {
		return nil, err
	}
	return fn(ctx, session)
}

// applyTurnStartConfig 在首 turn 提交前应用审批策略补丁；空策略保持 session 当前配置。
func applyTurnStartConfig(ctx context.Context, session contract.Session, p turnStartParams) error {
	policy := strings.TrimSpace(p.ApprovalPolicy)
	if policy == "" {
		return nil
	}
	return session.Configure(ctx, dto.ThreadConfigPatch{Approvals: &policy})
}

// collectTurnStartUserInput 提取用于 pending launch 路由判断的第一段用户文本。
func collectTurnStartUserInput(p turnStartParams) string {
	if text := strings.TrimSpace(p.Prompt); text != "" {
		return text
	}
	for _, item := range p.Input {
		if text := strings.TrimSpace(item.Text); text != "" {
			return text
		}
		if text := strings.TrimSpace(item.Content); text != "" {
			return text
		}
	}
	return ""
}

// turnStartHandler 处理 turn/start：必要时启动 pending provider、校验 cwd，再提交首个 turn。
func turnStartHandler(svc Service, resolver contract.SessionResolver, spawner contract.PendingLaunchSpawner, capResolver contract.CapabilityResolver, runtimeReader ThreadStateConfigReader) handler.Func {
	_ = capResolver
	return platformrpc.ThreadHandler(func(ctx context.Context, p turnStartParams) (any, error) {
		return startTurnForRPC(ctx, svc, resolver, spawner, runtimeReader, p)
	})
}

// startTurnForRPC 串联 provider 就绪、首 turn 启动及启动后中断诊断回传。
func startTurnForRPC(ctx context.Context, svc Service, resolver contract.SessionResolver, spawner contract.PendingLaunchSpawner, runtimeReader ThreadStateConfigReader, p turnStartParams) (any, error) {
	spawnRouting, err := traceSpawnIfNeeded(ctx, spawner, p)
	if err != nil {
		return nil, err
	}
	readyCtx, session, err := tracedReadyTurnSession(ctx, svc, resolver)
	if err != nil {
		return nil, err
	}
	return startReadyTurnForRPC(readyCtx, svc, session, spawner, runtimeReader, spawnRouting, p)
}

// startReadyTurnForRPC 在已就绪 session 中执行严格配置、prepare、start 和结果投影。
func startReadyTurnForRPC(ctx context.Context, svc Service, session contract.Session, spawner contract.PendingLaunchSpawner, runtimeReader ThreadStateConfigReader, spawnRouting threaddto.SpawnRouting, p turnStartParams) (any, error) {
	if err := platformrpc.RequireSessionCapability(session, dto.CapMessageSend); err != nil {
		return nil, err
	}
	threadRuntimeConfig, err := readThreadRuntimeConfig(ctx, runtimeReader, contract.ThreadIDFrom(ctx))
	if err != nil {
		return nil, err
	}
	resolvedCWD, err := resolveTurnRPCCWD(p.CWD, threadRuntimeConfig)
	if err != nil {
		return nil, err
	}
	p.CWD = resolvedCWD
	input, err := buildRPCPrepareInput(p, session, threadRuntimeConfig)
	if err != nil {
		return nil, err
	}
	if err := applyTurnStartConfig(ctx, session, p); err != nil {
		return nil, err
	}
	req, err := svc.PrepareTurn(ctx, session, input)
	if err != nil {
		return nil, err
	}
	handle, err := svc.StartTurn(ctx, session, req)
	if err != nil {
		return nil, err
	}
	completeLaunchIntentIfAvailable(ctx, spawner)
	return buildTurnStartRPCResult(svc, handle.LocalID(), spawnRouting)
}

// buildTurnStartRPCResult 投影规范 turn ID、路由元数据及安全启动诊断。
func buildTurnStartRPCResult(svc Service, localID string, spawnRouting threaddto.SpawnRouting) (turnStartResult, error) {
	result := turnStartResult{
		TurnID:          localID,
		AgentKey:        spawnRouting.AgentKey,
		AgentTitle:      spawnRouting.AgentTitle,
		PromptKey:       spawnRouting.PromptKey,
		PromptVersionID: spawnRouting.PromptVersionID,
	}
	if err := attachTurnStartInterruptRetryable(svc, localID, &result); err != nil {
		return turnStartResult{}, err
	}
	attachTurnPromptKeyStale(&result, spawnRouting.PromptKeyStale)
	return result, nil
}

// attachTurnStartInterruptRetryable 把安全的启动诊断与可重试取消状态显式返回给客户端。
func attachTurnStartInterruptRetryable(svc Service, localID string, result *turnStartResult) error {
	if result == nil {
		return errors.New("turn/start result is required")
	}
	status, err := svc.TrackTurn(context.Background(), localID)
	if err != nil {
		return fmt.Errorf("turn/start track started turn: %w", err)
	}
	if status.InterruptRetryable {
		result.InterruptRetryable = true
		result.InterruptRetryableCode = status.InterruptRetryableCode
	}
	result.StartDiagnosticCode = status.StartDiagnosticCode
	return nil
}

// validateRPCLocalTurnID 只接受前端生成的 turn_UUID，避免 RPC 输入覆盖 tracker 本地键。
func validateRPCLocalTurnID(localID string) error {
	if isRPCLocalTurnID(localID) {
		return nil
	}
	return platformrpc.ErrInvalidParams("turn/start localTurnId must be a turn_UUID")
}

func isRPCLocalTurnID(localID string) bool {
	const prefix = "turn_"
	const uuidLength = 36
	localID = strings.TrimSpace(localID)
	if !strings.HasPrefix(localID, prefix) || len(localID) != len(prefix)+uuidLength {
		return false
	}
	for index, char := range localID[len(prefix):] {
		if !isRPCLocalTurnIDRune(index, char) {
			return false
		}
	}
	return true
}

func isRPCLocalTurnIDRune(index int, char rune) bool {
	if index == 8 || index == 13 || index == 18 || index == 23 {
		return char == '-'
	}
	return isLowerHexRune(char)
}

func isLowerHexRune(char rune) bool {
	return char >= '0' && char <= '9' || char >= 'a' && char <= 'f'
}

// traceSpawnIfNeeded 在 pending_launch 线程上启动 provider，并返回 UI 需要展示的路由信息。
func traceSpawnIfNeeded(ctx context.Context, spawner contract.PendingLaunchSpawner, p turnStartParams) (threaddto.SpawnRouting, error) {
	if err := validateRPCLocalTurnID(p.LocalTurnID); err != nil {
		return threaddto.SpawnRouting{}, err
	}
	if spawner == nil {
		return threaddto.SpawnRouting{}, nil
	}
	launched, routing, err := spawner.SpawnIfNeeded(ctx, contract.ThreadIDFrom(ctx), collectTurnStartUserInput(p), p.CWD)
	if err != nil || !launched {
		return threaddto.SpawnRouting{}, err
	}
	return routing, nil
}

// tracedReadyTurnSession 在等待 session ready 时包裹 tracing span，方便定位首 turn 卡住阶段。
func tracedReadyTurnSession(ctx context.Context, svc Service, resolver contract.SessionResolver) (context.Context, contract.Session, error) {
	readyCtx := ctx
	var readySpan turnTraceSpan
	concrete, tracing := svc.(*service)
	if tracing {
		readySpan = concrete.beginTurnTraceSpan(ctx, "turn.ready_wait", contract.ThreadIDFrom(ctx), "", "", platformobs.NewCodeAnchor("internal/module/turn/rpc_helpers.go", "turn.tracedReadyTurnSession", 287), nil)
		readyCtx = readySpan.ctx
	}
	session, err := resolveReadyTurnSession(readyCtx, resolver)
	if tracing {
		concrete.finishTurnTraceSpan(readySpan, err)
	}
	if err != nil {
		return readyCtx, nil, err
	}
	return readyCtx, session, nil
}

// completeLaunchIntentIfAvailable 在 turn 成功提交后关闭 launch intent，避免 UI 继续显示待启动。
func completeLaunchIntentIfAvailable(ctx context.Context, spawner contract.PendingLaunchSpawner) {
	completer, ok := spawner.(contract.LaunchIntentCompleter)
	if !ok {
		return
	}
	completer.CompleteLaunchIntent(ctx, contract.ThreadIDFrom(ctx))
}

// turnSteerHandler 处理 turn/steer：复用 start 输入解析，但要求 provider 支持消息发送能力。
func turnSteerHandler(svc Service, resolver contract.SessionResolver, capResolver contract.CapabilityResolver, runtimeReader ThreadStateConfigReader) handler.Func {
	_ = capResolver
	return platformrpc.ThreadHandler(func(ctx context.Context, p turnSteerParams) (any, error) {
		return withReadyTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if err := platformrpc.RequireSessionCapability(session, dto.CapMessageSend); err != nil {
				return nil, err
			}
			items, inputSkills, err := buildTurnStartInputs(p.Input)
			if err != nil {
				return nil, err
			}
			threadRuntimeConfig, err := readThreadRuntimeConfig(ctx, runtimeReader, contract.ThreadIDFrom(ctx))
			if err != nil {
				return nil, err
			}
			resolvedCWD, err := resolveTurnRPCCWD(p.CWD, threadRuntimeConfig)
			if err != nil {
				return nil, err
			}
			handle, err := svc.SteerTurn(ctx, session, p.ExpectedTurnID, buildPrepareInput(prepareInputSpec{
				Inputs:                       items,
				Prompt:                       p.Prompt,
				ManualSkillSelection:         p.ManualSkillSelection,
				Provider:                     p.Provider,
				Model:                        p.Model,
				CWD:                          resolvedCWD,
				GitRoot:                      p.GitRoot,
				IsWorktree:                   p.IsWorktree,
				Language:                     p.Language,
				EnabledTools:                 p.EnabledTools,
				AdditionalWorkingDirectories: p.AdditionalWorkingDirectories,
				MCPSnapshot:                  p.MCPSnapshot,
				SessionFlags:                 p.SessionFlags,
				ThreadRuntimeConfig:          threadRuntimeConfig,
			}, prepareSkillSpec{
				Selected:     p.SelectedSkills,
				SelectedRefs: p.SelectedSkillRefs,
				Derived:      inputSkills,
			}, session))
			if err != nil {
				return nil, err
			}
			return turnStartResult{TurnID: handle.LocalID()}, nil
		})
	})
}

// turnInterruptHandler 处理 turn/interrupt，并把本地等待超时转换成 ok=false 的中断结果。
func turnInterruptHandler(svc Service, resolver contract.SessionResolver) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p turnInterruptParams) (any, error) {
		return withTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			targeted, ok := svc.(interface {
				InterruptTurnForTarget(context.Context, contract.Session, string, string, string) (TurnStatus, bool, error)
			})
			if !ok {
				return nil, errors.New("turn/interrupt: target-aware interrupt service is required")
			}
			status, accepted, err := targeted.InterruptTurnForTarget(ctx, session, p.Source, p.ExpectedTurnID, p.RequestID)
			responseRequestID := interruptResponseRequestID(status, p.RequestID)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return buildInterruptFailureResult(status, status.interruptEnvelope(), p.ExpectedTurnID, responseRequestID, accepted), nil
				}
				return nil, err
			}
			if !accepted {
				if status.interruptEnvelope().mode == "not_applied" {
					return buildInterruptNotAppliedResult(status, status.interruptEnvelope(), p.ExpectedTurnID, responseRequestID), nil
				}
				return buildInterruptTargetChangedResult(status, p.ExpectedTurnID, responseRequestID), nil
			}
			return buildInterruptResult(status, status.interruptEnvelope(), p.ExpectedTurnID, responseRequestID, true), nil
		})
	})
}

// interruptResponseRequestID 只在 service 尚未决议 Stop identity 时沿用请求 ID。
func interruptResponseRequestID(status TurnStatus, fallback string) string {
	envelope := status.interruptEnvelope()
	if envelope.requestIDKnown {
		return strings.TrimSpace(envelope.requestID)
	}
	return strings.TrimSpace(fallback)
}

// turnForceCompleteHandler 处理强制完成请求，只负责把 service 成功结果映射为 RPC payload。
func turnForceCompleteHandler(svc Service, resolver contract.SessionResolver) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p threadIDOnlyParams) (any, error) {
		return withTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if err := svc.ForceCompleteTurn(ctx, session); err != nil {
				if isForceCompleteTargetNotFound(err) {
					return turnForceCompleteResult{OK: false, ForceCompleted: false, ErrorCode: "force_complete_target_not_found"}, nil
				}
				return nil, err
			}
			return turnForceCompleteResult{OK: true, ForceCompleted: true}, nil
		})
	})
}

// forceCompleteTargetNotFound identifies provider no-target errors without importing provider packages.
type forceCompleteTargetNotFound interface {
	ForceCompleteTargetNotFound() bool
}

// isForceCompleteTargetNotFound reports whether err should map to a no-target force-complete envelope.
func isForceCompleteTargetNotFound(err error) bool {
	var marker forceCompleteTargetNotFound
	return errors.As(err, &marker) && marker.ForceCompleteTargetNotFound()
}

// approvalRespondHandler 将 UI 审批响应转交给 provider approval responder，并要求显式决策。
func approvalRespondHandler(approver contract.ApprovalResponder) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p approvalRespondParams) (any, error) {
		if approver == nil {
			return nil, platformrpc.ErrInvalidState("turn rpc: approval responder is not configured")
		}
		if p.Approved == nil && len(p.Decision) == 0 {
			return nil, platformrpc.ErrInvalidParams("turn rpc: approval decision is required")
		}
		requestID := int64(0)
		if p.RequestID != nil {
			requestID = *p.RequestID
		}
		sessionScope := strings.TrimSpace(p.SessionScope)
		callID := strings.TrimSpace(p.CallID)
		if sessionScope == "" {
			return nil, platformrpc.ErrInvalidParams("turn rpc: approval session scope is required")
		}
		if callID == "" {
			return nil, platformrpc.ErrInvalidParams("turn rpc: approval call id is required")
		}
		if requestID <= 0 {
			return nil, platformrpc.ErrInvalidParams("turn rpc: approval request id must be positive")
		}
		return nil, approver.Respond(contract.ApprovalIdentity{
			SessionScope: sessionScope,
			CallID:       callID,
			RequestID:    requestID,
		}, contract.ApprovalDecision{
			Approved: p.Approved,
			Detail:   append(json.RawMessage(nil), p.Decision...),
		})
	})
}

// skillName 从兼容输入项中提取 skill 名称，非 skill 类型返回空。
func (p turnInputItemParams) skillName() string {
	return util.FirstTrimmed(p.Name, p.Text, p.Content, p.Path)
}

func (p turnInputItemParams) isSkillType() bool {
	return strings.EqualFold(strings.TrimSpace(p.Type), "skill")
}

// inputItem 将兼容 text/content/path/url 形态归一化为 provider 输入项。
func (p turnInputItemParams) inputItem() (InputItem, bool, error) {
	item := InputItem{
		Type:    inferTurnInputType(p),
		Content: util.FirstTrimmed(p.Content, p.Text),
		Path:    util.FirstTrimmed(p.Path),
		Name:    util.FirstTrimmed(p.Name),
		URL:     util.FirstTrimmed(p.URL),
	}
	if normalized, ok := shareddto.NormalizeInputType(item.Type); !ok {
		return InputItem{}, false, fmt.Errorf("unsupported input type %q", strings.TrimSpace(item.Type))
	} else {
		item.Type = normalized
	}
	if item.Type == "" {
		return InputItem{}, false, nil
	}
	return item, item.Content != "" || item.Path != "" || item.URL != "", nil
}

func inferTurnInputType(p turnInputItemParams) string {
	if typ := util.FirstTrimmed(p.Type); typ != "" {
		return typ
	}
	switch {
	case util.FirstTrimmed(p.URL) != "":
		return "image"
	case util.FirstTrimmed(p.Path) != "":
		return "mention"
	case util.FirstTrimmed(p.Content, p.Text) != "":
		return "text"
	default:
		return ""
	}
}
