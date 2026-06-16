package personalization

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type profileGetParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type profileSaveParams struct {
	Cwd     string  `json:"cwd,omitempty"`
	Profile Profile `json:"profile"`
}

// NewHandlers 注册个性化 profile 的 JSON-RPC 接口。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"personalization/profile/get":  platformrpc.StrictHandler(profileGetHandler(svc)),
		"personalization/profile/save": platformrpc.StrictHandler(profileSaveHandler(svc)),
	}}
}

func profileGetHandler(svc Service) func(context.Context, profileGetParams) (ProfileResult, error) {
	return func(ctx context.Context, p profileGetParams) (ProfileResult, error) {
		if svc == nil {
			return ProfileResult{}, platformrpc.ErrInvalidState("personalization service is not configured")
		}
		result, err := svc.GetProfile(ctx, p.Cwd)
		if err != nil {
			return ProfileResult{}, platformrpc.ErrInvalidParams(err.Error())
		}
		return result, nil
	}
}

func profileSaveHandler(svc Service) func(context.Context, profileSaveParams) (ProfileResult, error) {
	return func(ctx context.Context, p profileSaveParams) (ProfileResult, error) {
		if svc == nil {
			return ProfileResult{}, platformrpc.ErrInvalidState("personalization service is not configured")
		}
		result, err := svc.SaveProfile(ctx, p.Cwd, p.Profile)
		if err != nil {
			return ProfileResult{}, platformrpc.ErrInvalidParams(err.Error())
		}
		return result, nil
	}
}
