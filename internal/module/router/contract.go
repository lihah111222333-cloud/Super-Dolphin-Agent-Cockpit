// Package router exposes read-only router RPCs to the frontend. The heavy
// lifting (candidate loading + classification + prompt_versions
// materialization) lives in internal/module/thread/router_resolve.go when a
// thread is actually being started; this module reuses the same
// router.Backend for *preview* calls where the frontend wants to show "this
// input will be routed to X" before the user hits send.
//
// Contract notes:
//
//   - No side effects. Does not insert into prompt_versions, does not launch
//     anything. Pure read + classify.
//   - Same Risk 1 (c)+(b) fallback as thread.Start: if the promptStore is
//     down we log and return Matched=false, we never error out the preview.
package router

import "context"

type Service interface {
	Classify(ctx context.Context, req ClassifyRequest) (ClassifyResult, error)
}

type ClassifyRequest struct {
	UserInput string
}

type ClassifyResult struct {
	Matched    bool    `json:"matched"`
	PromptKey  string  `json:"prompt_key,omitempty"`
	AgentKey   string  `json:"agent_key,omitempty"`
	Title      string  `json:"title,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}
