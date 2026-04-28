package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
		Selected: p.SelectedSkills,
		Derived:  inputSkills,
	}, session)
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
		return nil, errors.New("turn rpc: session resolver is not configured")
	}
	session, err := resolver.ResolveSession(ctx, rpc.ThreadIDFrom(ctx))
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, rpc.ErrInvalidState("thread session is not available; start or resume the thread first")
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
		return nil, errors.New("turn rpc: session resolver is not configured")
	}
	threadID := rpc.ThreadIDFrom(ctx)
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
	return config.WithTimeoutIfNone(ctx, config.LaunchTimeout)
}

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
			return nil, rpc.ErrInvalidState("thread session is not available; start or resume the thread first")
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

func turnStartHandler(svc Service, resolver contract.SessionResolver, spawner contract.PendingLaunchSpawner, capResolver rpc.CapabilityResolver, runtimeReader ThreadStateConfigReader) handler.Func {
	_ = capResolver
	return rpc.ThreadHandler(func(ctx context.Context, p turnStartParams) (any, error) {
		// C1: if this thread is still in pending_launch state, fork the
		// provider CLI now using the first-turn user text for router
		// classification. SpawnIfNeeded is a no-op for already-running
		// threads, so eager-path threads are unaffected.
		var spawnRouting threaddto.SpawnRouting
		if spawner != nil {
			launched, routing, err := spawner.SpawnIfNeeded(ctx, rpc.ThreadIDFrom(ctx), collectTurnStartUserInput(p))
			if err != nil {
				return nil, err
			}
			if launched {
				spawnRouting = routing
			}
		}
		return withReadyTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if !contract.HasCapability(session.Capabilities(), dto.CapMessageSend) {
				return nil, rpc.ErrCapabilityGate("capability not supported by active provider")
			}
			threadRuntimeConfig := readThreadRuntimeConfig(ctx, runtimeReader, rpc.ThreadIDFrom(ctx))
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
			// Forward the routing decision made by SpawnIfNeeded so the UI
			// can fill its per-thread badge. Empty fields are elided via
			// omitempty on turnStartResult; eager-path threads already got
			// their routing from thread/start's response.
			return turnStartResult{
				TurnID:          handle.LocalID(),
				AgentKey:        spawnRouting.AgentKey,
				AgentTitle:      spawnRouting.AgentTitle,
				PromptKey:       spawnRouting.PromptKey,
				PromptVersionID: spawnRouting.PromptVersionID,
			}, nil
		})
	})
}

func turnSteerHandler(svc Service, resolver contract.SessionResolver, capResolver rpc.CapabilityResolver, runtimeReader ThreadStateConfigReader) handler.Func {
	_ = capResolver
	return rpc.ThreadHandler(func(ctx context.Context, p turnSteerParams) (any, error) {
		return withReadyTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if !contract.HasCapability(session.Capabilities(), dto.CapMessageSend) {
				return nil, rpc.ErrCapabilityGate("capability not supported by active provider")
			}
			items, inputSkills := buildTurnStartInputs(p.Input)
			threadRuntimeConfig := readThreadRuntimeConfig(ctx, runtimeReader, rpc.ThreadIDFrom(ctx))
			handle, err := svc.SteerTurn(ctx, session, p.ExpectedTurnID, buildPrepareInput(prepareInputSpec{
				Inputs:                       items,
				Prompt:                       p.Prompt,
				ManualSkillSelection:         p.ManualSkillSelection,
				Provider:                     p.Provider,
				Model:                        p.Model,
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
				Selected: p.SelectedSkills,
				Derived:  inputSkills,
			}, session))
			if err != nil {
				return nil, err
			}
			return turnStartResult{TurnID: handle.LocalID()}, nil
		})
	})
}

func turnInterruptHandler(svc Service, resolver contract.SessionResolver) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p turnInterruptParams) (any, error) {
		return withTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			status, err := svc.InterruptTurn(ctx, session, p.Source)
			if err != nil {
				return nil, err
			}
			return buildInterruptResult(status, status.interruptEnvelope()), nil
		})
	})
}

func turnForceCompleteHandler(svc Service, resolver contract.SessionResolver) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p threadIDOnlyParams) (any, error) {
		return withTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if err := svc.ForceCompleteTurn(ctx, session); err != nil {
				return nil, err
			}
			return turnForceCompleteResult{OK: true, ForceCompleted: true}, nil
		})
	})
}

func approvalRespondHandler(approver contract.ApprovalResponder) handler.Func {
	return rpc.StrictHandler(func(ctx context.Context, p approvalRespondParams) (any, error) {
		if approver == nil {
			return nil, errors.New("turn rpc: approval responder is not configured")
		}
		if p.Approved == nil && len(p.Decision) == 0 {
			return nil, errors.New("turn rpc: approval decision is required")
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
	return shared.FirstTrimmed(p.Name, p.Text, p.Content, p.Path)
}

func (p turnInputItemParams) inputItem() (InputItem, bool) {
	item := InputItem{
		Type:    shared.FirstTrimmed(p.Type),
		Content: shared.FirstTrimmed(p.Content, p.Text),
		Path:    shared.FirstTrimmed(p.Path),
		Name:    shared.FirstTrimmed(p.Name),
		URL:     shared.FirstTrimmed(p.URL),
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
