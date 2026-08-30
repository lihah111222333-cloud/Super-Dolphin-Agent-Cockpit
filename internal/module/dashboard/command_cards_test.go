package dashboard

import (
	"context"
	"encoding/json"
	"testing"
)

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

// TestDashboardCommandCardsHandlerReturnsCards 验证命令卡片接口只读取命令卡片端口并返回其 wire 投影。
func TestDashboardCommandCardsHandlerReturnsCards(t *testing.T) {
	t.Parallel()

	cards := &stubCommandCardReader{
		result: []CommandCard{{CardKey: "cmd/review", Title: "Review", Enabled: true}},
	}
	server := newDashboardTestServer(t, &service{commandCards: cards})

	result, err := server.Dispatch(context.Background(), "dashboard/commandCards", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
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
