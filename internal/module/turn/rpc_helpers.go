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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

func buildRPCPrepareInput(p turnStartParams, session contract.Session, threadRuntimeConfig map[string]any) PrepareInput {
	items, inputSkills := buildTurnStartInputs(p.Input)
	return buildPrepareInput(prepareInputSpec{
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
	}, session)
}

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

// sameTurnRPCCWD 处理sameturnrpccwd。
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

func strictRuntimeCWD(cfg map[string]any, label string) (string, error) {
	cwd, err := configutil.StrictString(cfg, label, "cwd")
	if err != nil {
		return "", platformrpc.ErrInvalidParams(err.Error())
	}
	return cwd, nil
}

func buildTurnStartInputs(raw []turnInputItemParams) ([]InputItem, []string) {
	items := make([]InputItem, 0, len(raw))
	skills := make([]string, 0, len(raw))
	for _, item := range raw {
		if skill := item.skillName(); skill != "" {
			skills = append(skills, skill)
			continue
		}
		input, ok := item.inputItem()
		if ok {
			items = append(items, input)
		}
	}
	return items, skills
}

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

func withTurnSession(ctx context.Context, resolver contract.SessionResolver, fn func(context.Context, contract.Session) (any, error)) (any, error) {
	session, err := resolveTurnSession(ctx, resolver)
	if err != nil {
		return nil, err
	}
	return fn(ctx, session)
}

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

func readyTurnWaitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeoutIfNone(ctx, ctxutil.LaunchTimeout)
}

// waitForReadyTurnSession 等待会话 ready 后再提交 turn。
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

func withReadyTurnSession(ctx context.Context, resolver contract.SessionResolver, fn func(context.Context, contract.Session) (any, error)) (any, error) {
	session, err := resolveReadyTurnSession(ctx, resolver)
	if err != nil {
		return nil, err
	}
	return fn(ctx, session)
}

func applyTurnStartConfig(ctx context.Context, session contract.Session, p turnStartParams) error {
	policy := strings.TrimSpace(p.ApprovalPolicy)
	if policy == "" {
		return nil
	}
	return session.Configure(ctx, dto.ThreadConfigPatch{Approvals: &policy})
}

// P22 P4 S2: PendingLaunchSpawner was formerly defined here and exported
// as turn.PendingLaunchSpawner; that placed the owner-side contract in a
// consumer package (side-channel hidden contract). The interface now
// lives in internal/contract as contract.PendingLaunchSpawner; turn only
// consumes it.

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

// turnStartHandler 处理turn起点处理器。
func turnStartHandler(svc Service, resolver contract.SessionResolver, spawner contract.PendingLaunchSpawner, capResolver contract.CapabilityResolver, runtimeReader ThreadStateConfigReader) handler.Func {
	_ = capResolver
	return platformrpc.ThreadHandler(func(ctx context.Context, p turnStartParams) (any, error) {
		// C1: if this thread is still in pending_launch state, fork the
		// provider CLI now using the first-turn user text for router
		// evaluation. SpawnIfNeeded is a no-op for already-running
		// threads, so eager-path threads are unaffected.
		spawnRouting, err := traceSpawnIfNeeded(ctx, spawner, p)
		if err != nil {
			return nil, err
		}
		readyCtx, session, err := tracedReadyTurnSession(ctx, svc, resolver)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, session contract.Session) (any, error) {
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
			input := buildRPCPrepareInput(p, session, threadRuntimeConfig)
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
			// Forward the routing decision made by SpawnIfNeeded so the UI
			// can fill its per-thread badge. Empty fields are elided via
			// omitempty on turnStartResult; eager-path threads already got
			// their routing from thread/start's response.
			result := turnStartResult{
				TurnID:          handle.LocalID(),
				AgentKey:        spawnRouting.AgentKey,
				AgentTitle:      spawnRouting.AgentTitle,
				PromptKey:       spawnRouting.PromptKey,
				PromptVersionID: spawnRouting.PromptVersionID,
			}
			attachTurnPromptKeyStale(&result, spawnRouting.PromptKeyStale)
			return result, nil
		}(readyCtx, session)
	})
}

func traceSpawnIfNeeded(ctx context.Context, spawner contract.PendingLaunchSpawner, p turnStartParams) (threaddto.SpawnRouting, error) {
	if spawner == nil {
		return threaddto.SpawnRouting{}, nil
	}
	launched, routing, err := spawner.SpawnIfNeeded(ctx, contract.ThreadIDFrom(ctx), collectTurnStartUserInput(p), p.CWD)
	if err != nil || !launched {
		return threaddto.SpawnRouting{}, err
	}
	return routing, nil
}

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

func completeLaunchIntentIfAvailable(ctx context.Context, spawner contract.PendingLaunchSpawner) {
	completer, ok := spawner.(contract.LaunchIntentCompleter)
	if !ok {
		return
	}
	completer.CompleteLaunchIntent(ctx, contract.ThreadIDFrom(ctx))
}

// turnSteerHandler 处理turnsteer处理器。
func turnSteerHandler(svc Service, resolver contract.SessionResolver, capResolver contract.CapabilityResolver, runtimeReader ThreadStateConfigReader) handler.Func {
	_ = capResolver
	return platformrpc.ThreadHandler(func(ctx context.Context, p turnSteerParams) (any, error) {
		return withReadyTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if err := platformrpc.RequireSessionCapability(session, dto.CapMessageSend); err != nil {
				return nil, err
			}
			items, inputSkills := buildTurnStartInputs(p.Input)
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

func turnInterruptHandler(svc Service, resolver contract.SessionResolver) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p turnInterruptParams) (any, error) {
		return withTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			status, err := svc.InterruptTurn(ctx, session, p.Source)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return buildInterruptFailureResult(status, status.interruptEnvelope()), nil
				}
				return nil, err
			}
			return buildInterruptResult(status, status.interruptEnvelope()), nil
		})
	})
}

func turnForceCompleteHandler(svc Service, resolver contract.SessionResolver) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p threadIDOnlyParams) (any, error) {
		return withTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if err := svc.ForceCompleteTurn(ctx, session); err != nil {
				return nil, err
			}
			return turnForceCompleteResult{OK: true, ForceCompleted: true}, nil
		})
	})
}

func approvalRespondHandler(approver contract.ApprovalResponder) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p approvalRespondParams) (any, error) {
		if approver == nil {
			return nil, platformrpc.ErrInvalidState("turn rpc: approval responder is not configured")
		}
		if p.Approved == nil && len(p.Decision) == 0 {
			return nil, platformrpc.ErrInvalidParams("turn rpc: approval decision is required")
		}
		return nil, approver.Respond(p.CallID, p.RequestID, contract.ApprovalDecision{
			Approved: p.Approved,
			Detail:   append(json.RawMessage(nil), p.Decision...),
		})
	})
}

func (p turnInputItemParams) skillName() string {
	if !strings.EqualFold(strings.TrimSpace(p.Type), "skill") {
		return ""
	}
	return util.FirstTrimmed(p.Name, p.Text, p.Content, p.Path)
}

// inputItem 处理inputitem。
func (p turnInputItemParams) inputItem() (InputItem, bool) {
	item := InputItem{
		Type:    util.FirstTrimmed(p.Type),
		Content: util.FirstTrimmed(p.Content, p.Text),
		Path:    util.FirstTrimmed(p.Path),
		Name:    util.FirstTrimmed(p.Name),
		URL:     util.FirstTrimmed(p.URL),
	}
	switch {
	case item.Type == "" && item.URL != "":
		item.Type = "image"
	case item.Type == "" && item.Path != "":
		item.Type = "mention"
	case item.Type == "" && item.Content != "":
		item.Type = "text"
	}
	if item.Type == "" {
		return InputItem{}, false
	}
	return item, item.Content != "" || item.Path != "" || item.URL != ""
}
