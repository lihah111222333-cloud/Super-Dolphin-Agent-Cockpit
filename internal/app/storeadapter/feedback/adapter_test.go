package feedbackadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/feedback"
	feedbackstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/feedback"
	storeadaptertest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/storeadapter"
)

var _ feedback.Writer = (*feedbackStoreAdapter)(nil)

type feedbackStoreStub struct {
	insert func(context.Context, feedbackstore.Event) (feedbackstore.Event, error)
}

func (s feedbackStoreStub) Insert(ctx context.Context, event feedbackstore.Event) (feedbackstore.Event, error) {
	return s.insert(ctx, event)
}

func (feedbackStoreStub) ListByThread(context.Context, string, int32) ([]feedbackstore.Event, error) {
	return nil, nil
}

func (feedbackStoreStub) ListByAgentKey(context.Context, string, int32) ([]feedbackstore.Event, error) {
	return nil, nil
}

// TestFeedbackStoreAdapterContract 先固定 App 构造器必须返回 feedback 自有的写端口。
func TestFeedbackStoreAdapterContract(t *testing.T) {
	store := feedbackStoreStub{insert: func(_ context.Context, event feedbackstore.Event) (feedbackstore.Event, error) {
		return event, nil
	}}
	writer := provideFeedbackWriter(store)
	if writer == nil {
		t.Fatal("expected feedback writer")
	}
	if _, err := writer.Insert(context.Background(), feedback.Event{ThreadID: "thread-1", EventType: "thumbs_up"}); err != nil {
		t.Fatalf("insert feedback event: %v", err)
	}
}

// TestFeedbackStoreAdapterFieldCoverage 用 one-hot 输入自动证明两侧全部导出字段都被双向映射。
func TestFeedbackStoreAdapterFieldCoverage(t *testing.T) {
	t.Run("domain_to_store", func(t *testing.T) {
		storeadaptertest.AssertFieldsMapE(t, func(event feedback.Event) (feedbackstore.Event, error) {
			var captured feedbackstore.Event
			writer := provideFeedbackWriter(feedbackStoreStub{insert: func(_ context.Context, stored feedbackstore.Event) (feedbackstore.Event, error) {
				captured = stored
				return feedbackstore.Event{}, nil
			}})
			_, err := writer.Insert(context.Background(), event)
			return captured, err
		})
	})
	t.Run("store_to_domain", func(t *testing.T) {
		storeadaptertest.AssertFieldsMapE(t, func(stored feedbackstore.Event) (feedback.Event, error) {
			writer := provideFeedbackWriter(feedbackStoreStub{insert: func(context.Context, feedbackstore.Event) (feedbackstore.Event, error) {
				return stored, nil
			}})
			return writer.Insert(context.Background(), feedback.Event{})
		})
	})
}

// TestFeedbackStoreAdapterPreservesErrors 固定 Store 错误链不得在 App 边界被替换或吞掉。
func TestFeedbackStoreAdapterPreservesErrors(t *testing.T) {
	sentinel := errors.New("feedback store sentinel")
	storeErr := fmt.Errorf("insert feedback: %w", sentinel)
	writer := provideFeedbackWriter(feedbackStoreStub{insert: func(context.Context, feedbackstore.Event) (feedbackstore.Event, error) {
		return feedbackstore.Event{}, storeErr
	}})
	_, err := writer.Insert(context.Background(), feedback.Event{})
	if err != storeErr {
		t.Fatalf("expected original store error, got %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected errors.Is to preserve sentinel, got %v", err)
	}
}

// TestFeedbackStoreAdapterCopiesMutableFields 固定指针和 JSON payload 在边界两侧均不共享内存。
func TestFeedbackStoreAdapterCopiesMutableFields(t *testing.T) {
	t.Run("domain_to_store", func(t *testing.T) {
		promptVersionID := int64(41)
		payload := json.RawMessage(`{"rating":1}`)
		writer := provideFeedbackWriter(feedbackStoreStub{insert: func(_ context.Context, stored feedbackstore.Event) (feedbackstore.Event, error) {
			*stored.PromptVersionID = 99
			stored.Payload[0] = '['
			return stored, nil
		}})
		_, err := writer.Insert(context.Background(), feedback.Event{PromptVersionID: &promptVersionID, Payload: payload})
		if err != nil {
			t.Fatalf("insert feedback event: %v", err)
		}
		if promptVersionID != 41 || string(payload) != `{"rating":1}` {
			t.Fatalf("domain mutable fields were shared: prompt=%d payload=%s", promptVersionID, payload)
		}
	})
	t.Run("store_to_domain", func(t *testing.T) {
		promptVersionID := int64(41)
		payload := json.RawMessage(`{"rating":1}`)
		writer := provideFeedbackWriter(feedbackStoreStub{insert: func(context.Context, feedbackstore.Event) (feedbackstore.Event, error) {
			return feedbackstore.Event{PromptVersionID: &promptVersionID, Payload: payload}, nil
		}})
		got, err := writer.Insert(context.Background(), feedback.Event{})
		if err != nil {
			t.Fatalf("insert feedback event: %v", err)
		}
		promptVersionID = 99
		payload[0] = '['
		if got.PromptVersionID == nil || *got.PromptVersionID != 41 || string(got.Payload) != `{"rating":1}` {
			t.Fatalf("store mutable fields were shared: prompt=%v payload=%s", got.PromptVersionID, got.Payload)
		}
	})
}

// TestFeedbackStoreAdapterNilSemantics 固定 optional provider 与调用期 fail-fast 语义。
func TestFeedbackStoreAdapterNilSemantics(t *testing.T) {
	if writer := provideFeedbackWriter(nil); writer != nil {
		t.Fatalf("expected nil writer, got %T", writer)
	}
	var typedNil *feedbackStoreStub
	if writer := provideFeedbackWriter(typedNil); writer != nil {
		t.Fatalf("expected typed nil Store to produce nil writer, got %T", writer)
	}
	if _, err := (&feedbackStoreAdapter{}).Insert(context.Background(), feedback.Event{}); err == nil {
		t.Fatal("expected nil adapter store to fail")
	}
	if _, err := (&feedbackStoreAdapter{store: typedNil}).Insert(context.Background(), feedback.Event{}); !errors.Is(err, errFeedbackStoreAdapterMissing) {
		t.Fatalf("expected typed nil adapter Store to fail explicitly, got %v", err)
	}
	service := feedback.NewService(nil, nil)
	if _, err := service.Record(context.Background(), feedback.RecordRequest{ThreadID: "thread-1", EventType: "thumbs_up"}); err == nil {
		t.Fatal("expected nil service writer to fail")
	}
}
