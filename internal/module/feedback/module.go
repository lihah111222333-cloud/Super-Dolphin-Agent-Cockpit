// Package feedback 提供用户反馈事件的记录能力，通过 JSON-RPC 接口接收前端事件并持久化。
package feedback

import (
	"context"
	"errors"

	"go.uber.org/fx"

	feedbackstore "github.com/anthropic-ai/super-agent-v3/internal/store/feedback"
)

var Module = fx.Module("feedback",
	fx.Provide(provideFeedbackWriter),
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)

var errFeedbackStoreAdapterMissing = errors.New("feedback: store adapter missing store")

type feedbackStoreAdapter struct {
	store feedbackstore.Store
}

// provideFeedbackWriter 把 store/feedback 的读写 store 收窄为 feedback 模块只需要的写入口。
func provideFeedbackWriter(store feedbackstore.Store) feedbackWriter {
	if store == nil {
		return nil
	}
	return feedbackStoreAdapter{store: store}
}

// Insert 将 feedback 模块的事件投影转换为 store/feedback DTO 并返回稳定结果。
func (a feedbackStoreAdapter) Insert(ctx context.Context, ev feedbackEvent) (feedbackEvent, error) {
	if a.store == nil {
		return feedbackEvent{}, errFeedbackStoreAdapterMissing
	}
	stored, err := a.store.Insert(ctx, feedbackstore.Event{
		ID:              ev.ID,
		ThreadID:        ev.ThreadID,
		TurnID:          ev.TurnID,
		AgentKey:        ev.AgentKey,
		PromptVersionID: ev.PromptVersionID,
		EventType:       ev.EventType,
		Actor:           ev.Actor,
		Payload:         ev.Payload,
		CreatedAt:       ev.CreatedAt,
	})
	if err != nil {
		return feedbackEvent{}, err
	}
	return feedbackEvent{
		ID:              stored.ID,
		ThreadID:        stored.ThreadID,
		TurnID:          stored.TurnID,
		AgentKey:        stored.AgentKey,
		PromptVersionID: stored.PromptVersionID,
		EventType:       stored.EventType,
		Actor:           stored.Actor,
		Payload:         stored.Payload,
		CreatedAt:       stored.CreatedAt,
	}, nil
}
