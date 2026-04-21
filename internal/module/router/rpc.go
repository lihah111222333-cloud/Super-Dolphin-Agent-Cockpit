package router

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type classifyParams struct {
	UserInput string `json:"user_input,omitempty"`
	// Legacy camelCase alias to stay consistent with other thread payloads.
	UserInputCamel string `json:"userInput,omitempty"`
}

func NewHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"router/classify": newClassifyHandler(svc),
	}}
}

func newClassifyHandler(svc Service) handler.Func {
	return rpc.StrictHandler(func(ctx context.Context, p classifyParams) (any, error) {
		input := p.UserInput
		if input == "" {
			input = p.UserInputCamel
		}
		result, err := svc.Classify(ctx, ClassifyRequest{UserInput: input})
		if err != nil {
			return nil, err
		}
		response := map[string]any{
			"matched":    result.Matched,
			"confidence": result.Confidence,
		}
		if result.AgentKey != "" {
			response["agent_key"] = result.AgentKey
			response["agentKey"] = result.AgentKey
		}
		if result.PromptKey != "" {
			response["prompt_key"] = result.PromptKey
			response["promptKey"] = result.PromptKey
		}
		if result.Title != "" {
			response["title"] = result.Title
		}
		if result.Reason != "" {
			response["reason"] = result.Reason
		}
		return response, nil
	})
}
