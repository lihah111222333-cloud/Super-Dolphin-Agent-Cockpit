package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/module/feedback"
	feedbackstore "github.com/anthropic-ai/super-agent-v3/internal/store/feedback"
)

var errFeedbackStoreAdapterMissing = errors.New("feedback: store adapter missing store")

type feedbackStoreAdapter struct {
	store feedbackstore.Store
}

var _ feedback.Writer = (*feedbackStoreAdapter)(nil)

// provideFeedbackWriter 把 Store 的读写能力收窄为 feedback 模块拥有的写端口。
func provideFeedbackWriter(store feedbackstore.Store) feedback.Writer {
	if isNilBusinessStore(store) {
		return nil
	}
	return &feedbackStoreAdapter{store: store}
}

// Insert 在 App 组合边界完成 Store DTO 与 feedback 领域 DTO 的双向转换。
func (a *feedbackStoreAdapter) Insert(ctx context.Context, event feedback.Event) (feedback.Event, error) {
	if a == nil || isNilBusinessStore(a.store) {
		return feedback.Event{}, errFeedbackStoreAdapterMissing
	}
	stored, err := a.store.Insert(ctx, feedbackEventToStore(event))
	if err != nil {
		return feedback.Event{}, err
	}
	return feedbackEventFromStore(stored), nil
}

// feedbackEventToStore 把领域事件逐字段投影到 Store DTO，并复制可变字段。
func feedbackEventToStore(event feedback.Event) feedbackstore.Event {
	return feedbackstore.Event{
		ID:              event.ID,
		ThreadID:        event.ThreadID,
		TurnID:          event.TurnID,
		AgentKey:        event.AgentKey,
		PromptVersionID: copyFeedbackPromptVersionID(event.PromptVersionID),
		EventType:       event.EventType,
		Actor:           event.Actor,
		Payload:         copyFeedbackPayload(event.Payload),
		CreatedAt:       event.CreatedAt,
	}
}

// feedbackEventFromStore 把 Store 结果逐字段投影回领域 DTO，并复制可变字段。
func feedbackEventFromStore(event feedbackstore.Event) feedback.Event {
	return feedback.Event{
		ID:              event.ID,
		ThreadID:        event.ThreadID,
		TurnID:          event.TurnID,
		AgentKey:        event.AgentKey,
		PromptVersionID: copyFeedbackPromptVersionID(event.PromptVersionID),
		EventType:       event.EventType,
		Actor:           event.Actor,
		Payload:         copyFeedbackPayload(event.Payload),
		CreatedAt:       event.CreatedAt,
	}
}

// copyFeedbackPromptVersionID 复制可选版本号，避免跨边界共享指针。
func copyFeedbackPromptVersionID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

// copyFeedbackPayload 复制 JSON payload，避免跨边界共享底层数组。
func copyFeedbackPayload(payload json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), payload...)
}
