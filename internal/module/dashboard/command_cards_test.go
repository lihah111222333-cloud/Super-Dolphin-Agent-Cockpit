package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDashboardCommandCardsHandlerDoesNotLoadPrompts(t *testing.T) {
	t.Parallel()

	prompts := &stubPromptReader{err: errors.New("prompts should not be loaded")}
	cards := &stubCommandCardReader{
		result: []CommandCard{{CardKey: "cmd/review", Title: "Review", Enabled: true}},
	}
	server := newDashboardTestServer(t, &service{commandCards: cards, prompts: prompts})

	result, err := server.Dispatch(context.Background(), "dashboard/commandCards", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if prompts.calls != 0 {
		t.Fatalf("prompt List() calls = %d, want 0", prompts.calls)
	}
	if cards.calls != 1 {
		t.Fatalf("command card List() calls = %d, want 1", cards.calls)
	}
	var decoded struct {
		Cards []CommandCard `json:"cards"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(decoded.Cards) != 1 || decoded.Cards[0].CardKey != "cmd/review" {
		t.Fatalf("Dispatch() cards = %#v", decoded.Cards)
	}
}

type stubCommandCardReader struct {
	result     []CommandCard
	err        error
	calls      int
	lastFilter CommandCardFilter
}

var _ CommandCardReader = (*stubCommandCardReader)(nil)

func (s *stubCommandCardReader) List(_ context.Context, filter CommandCardFilter) ([]CommandCard, error) {
	s.calls++
	s.lastFilter = filter
	return s.result, s.err
}
