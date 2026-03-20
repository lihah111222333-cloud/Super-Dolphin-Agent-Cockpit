package skill

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func NewSkillHandlers(svc Service) rpc.HandlerMapResult {
	cardByKey := func(fn func(context.Context, string) (any, error)) handler.Func {
		return rpc.StrictHandler(func(ctx context.Context, p cardKeyParams) (any, error) { return fn(ctx, p.Key) })
	}
	namedContent := func(fn func(context.Context, string, string) (any, error)) handler.Func {
		return rpc.StrictHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) { return fn(ctx, p.Name, p.Content) })
	}
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"command/card/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) { return svc.ListCards(ctx) }),
		"command/card/get":  cardByKey(func(ctx context.Context, key string) (any, error) { return svc.GetCard(ctx, key) }),
		"command/card/create": rpc.StrictHandler(func(ctx context.Context, p createCardParams) (any, error) {
			return svc.CreateCard(ctx, buildCard(cardPayload(p)))
		}),
		"command/card/update": rpc.StrictHandler(func(ctx context.Context, p updateCardParams) (any, error) {
			return svc.UpdateCard(ctx, buildCard(cardPayload(p)))
		}),
		"command/card/delete":   cardByKey(func(ctx context.Context, key string) (any, error) { return nil, svc.DeleteCard(ctx, key) }),
		"command/card/run":      rpc.StrictHandler(func(ctx context.Context, p runCardParams) (any, error) { return svc.RunCard(ctx, p.Key, p.Args) }),
		"command/card/versions": cardByKey(func(ctx context.Context, key string) (any, error) { return svc.ListCardVersions(ctx, key) }),
		"command/exec": rpc.StrictHandler(func(ctx context.Context, p execParams) (any, error) {
			return svc.ExecCommand(ctx, p.Command, p.Args, p.CWD)
		}),
		"skills/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			list, err := svc.ListSkills(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"skills": list}, nil
		}),
		"skills/local/read":      rpc.StrictHandler(func(ctx context.Context, p pathParams) (any, error) { return svc.ReadLocal(ctx, p.Path) }),
		"skills/local/listFiles": rpc.StrictHandler(func(ctx context.Context, p listSkillFilesParams) (any, error) { return svc.ListLocalFiles(ctx, p) }),
		"skills/local/write":     rpc.StrictHandler(func(ctx context.Context, p contentParams) (any, error) { return svc.WriteLocal(ctx, p.Path, p.Content) }),
		"skills/local/importDir": rpc.StrictHandler(func(ctx context.Context, p importSkillDirParams) (any, error) { return svc.ImportLocalDir(ctx, p) }),
		"skills/local/delete":    rpc.StrictHandler(func(ctx context.Context, p deleteLocalSkillParams) (any, error) { return svc.DeleteLocal(ctx, p.Name) }),
		"skills/remote/list":     rpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) { return svc.ReadRemote(ctx, p.URL) }),
		"skills/remote/export": namedContent(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteRemote(ctx, name, content)
		}),
		"skills/remote/read": rpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) { return svc.ReadRemote(ctx, p.URL) }),
		"skills/remote/write": namedContent(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteRemote(ctx, name, content)
		}),
		"skills/config/read": rpc.StrictHandler(func(ctx context.Context, p skillConfigReadParams) (any, error) { return svc.ReadConfig(ctx, p.AgentID) }),
		// Legacy RPC key: V2 uses skills/config/write for saving the main skill file content.
		"skills/config/write": namedContent(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteSkillContent(ctx, name, content)
		}),
		"skills/summary/write": rpc.StrictHandler(func(ctx context.Context, p skillSummaryWriteParams) (any, error) {
			return svc.WriteSummary(ctx, p.Name, p.Summary)
		}),
		"skills/match/preview": rpc.StrictHandler(func(ctx context.Context, p skillMatchPreviewParams) (any, error) {
			return svc.MatchPreview(ctx, p.AgentID, p.ThreadID, p.Text, p.Input)
		}),
	}}
}

func buildCard(p cardPayload) Card {
	return Card{
		CardKey:         p.Key,
		Title:           p.Title,
		Description:     p.Description,
		CommandTemplate: p.CommandTemplate,
		ArgsSchema:      append(json.RawMessage(nil), p.ArgsSchema...),
		RiskLevel:       p.RiskLevel,
		Enabled:         p.Enabled == nil || *p.Enabled,
		CreatedBy:       p.CreatedBy,
		UpdatedBy:       p.UpdatedBy,
	}
}
