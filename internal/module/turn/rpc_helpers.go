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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func buildPrepareInput(p turnStartParams, session contract.Session) PrepareInput {
	items, inputSkills := buildTurnStartInputs(p.Input)
	return PrepareInput{
		Inputs:               items,
		Prompt:               p.Prompt,
		Images:               append([]string(nil), p.Images...),
		Files:                append([]string(nil), p.Files...),
		Skills:               skillRefsFromNames(p.SelectedSkills, inputSkills),
		ManualSkillSelection: p.ManualSkillSelection,
		Model:                p.Model,
		Effort:               p.Effort,
		OutputSchema:         append([]byte(nil), p.OutputSchema...),
		CWD:                  p.CWD,
		ThreadCaps:           session.Capabilities(),
	}
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

func skillRefsFromNames(groups ...[]string) []dto.SkillRef {
	refs := make([]dto.SkillRef, 0)
	seen := map[string]struct{}{}
	for _, names := range groups {
		for _, raw := range names {
			name := strings.TrimSpace(raw)
			key := strings.ToLower(name)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, dto.SkillRef{Name: name})
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
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
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, config.LaunchTimeout)
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

func turnStartHandler(svc Service, resolver contract.SessionResolver, capResolver rpc.CapabilityResolver) handler.Func {
	_ = capResolver
	return rpc.ThreadHandler(func(ctx context.Context, p turnStartParams) (any, error) {
		return withReadyTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if !session.Capabilities().Has(dto.CapMessageSend) {
				return nil, rpc.ErrCapabilityGate("capability not supported by active provider")
			}
			input := buildPrepareInput(p, session)
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
			return turnStartResult{TurnID: handle.LocalID()}, nil
		})
	})
}

func turnSteerHandler(svc Service, resolver contract.SessionResolver, capResolver rpc.CapabilityResolver) handler.Func {
	_ = capResolver
	return rpc.ThreadHandler(func(ctx context.Context, p turnSteerParams) (any, error) {
		return withReadyTurnSession(ctx, resolver, func(ctx context.Context, session contract.Session) (any, error) {
			if !session.Capabilities().Has(dto.CapMessageSend) {
				return nil, rpc.ErrCapabilityGate("capability not supported by active provider")
			}
			handle, err := svc.SteerTurn(ctx, session, p.ExpectedTurnID, buildPrepareInput(turnStartParams{
				Prompt:               p.Prompt,
				Input:                p.Input,
				SelectedSkills:       p.SelectedSkills,
				ManualSkillSelection: p.ManualSkillSelection,
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
			return turnInterruptResult{OK: true, TurnID: status.LocalID, Status: status.State}, nil
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

func reviewStartHandler() handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p threadIDOnlyParams) (any, error) {
		return nil, rpc.ErrNotImplemented("review/start is not yet implemented")
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
	return firstTrimmed(p.Name, p.Text, p.Content, p.Path)
}

func (p turnInputItemParams) inputItem() (InputItem, bool) {
	item := InputItem{
		Type:    firstTrimmed(p.Type),
		Content: firstTrimmed(p.Content, p.Text),
		Path:    firstTrimmed(p.Path),
		Name:    firstTrimmed(p.Name),
		URL:     firstTrimmed(p.URL),
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

func firstTrimmed(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
