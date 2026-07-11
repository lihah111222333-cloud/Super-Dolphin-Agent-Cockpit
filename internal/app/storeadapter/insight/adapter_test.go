package insightadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/insight"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
	storeadaptertest "github.com/anthropic-ai/super-agent-v3/internal/testutil/storeadapter"
)

var _ insight.Reader = (*insightStoreAdapter)(nil)
var _ insight.Writer = (*insightStoreAdapter)(nil)

type insightStoreStub struct {
	upsert       func(context.Context, insightstore.UpsertParams) (insightstore.Insight, error)
	listThread   func(context.Context, string, int32) ([]insightstore.Insight, error)
	listRecent   func(context.Context, int32) ([]insightstore.Insight, error)
	listApproval func(context.Context, string, int32) ([]insightstore.ApprovalRow, error)
}

func (s *insightStoreStub) Upsert(ctx context.Context, params insightstore.UpsertParams) (insightstore.Insight, error) {
	if s.upsert != nil {
		return s.upsert(ctx, params)
	}
	return insightstore.Insight{}, nil
}

func (*insightStoreStub) GetByLocalTurn(context.Context, string, string) (insightstore.Insight, error) {
	return insightstore.Insight{}, nil
}

func (s *insightStoreStub) ListByThread(ctx context.Context, threadID string, limit int32) ([]insightstore.Insight, error) {
	if s.listThread != nil {
		return s.listThread(ctx, threadID, limit)
	}
	return nil, nil
}

func (s *insightStoreStub) ListRecent(ctx context.Context, limit int32) ([]insightstore.Insight, error) {
	if s.listRecent != nil {
		return s.listRecent(ctx, limit)
	}
	return nil, nil
}

func (s *insightStoreStub) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]insightstore.ApprovalRow, error) {
	if s.listApproval != nil {
		return s.listApproval(ctx, threadID, limit)
	}
	return nil, nil
}

func (*insightStoreStub) ListObservedTokenTurns(context.Context, string, int32) ([]insightstore.TokenRow, error) {
	return nil, nil
}

// TestInsightStoreAdapterConstructorsRejectNil 固定 required Store 缺失时两个端口构造器都立即报错。
func TestInsightStoreAdapterConstructorsRejectNil(t *testing.T) {
	reader, readerErr := provideInsightReader(nil)
	if readerErr == nil || reader != nil {
		t.Fatalf("expected nil reader and explicit error, got reader=%T err=%v", reader, readerErr)
	}
	writer, writerErr := provideInsightWriter(nil)
	if writerErr == nil || writer != nil {
		t.Fatalf("expected nil writer and explicit error, got writer=%T err=%v", writer, writerErr)
	}
	var typedNil *insightStoreStub
	if reader, err := provideInsightReader(typedNil); err == nil || reader != nil {
		t.Fatalf("expected typed nil reader Store to fail, got reader=%T err=%v", reader, err)
	}
}

// TestInsightStoreAdapterContract 证明同一 concrete adapter 分别满足读写端口。
func TestInsightStoreAdapterContract(t *testing.T) {
	store := &insightStoreStub{}
	reader, readerErr := provideInsightReader(store)
	writer, writerErr := provideInsightWriter(store)
	if readerErr != nil || writerErr != nil {
		t.Fatalf("construct insight ports: reader=%v writer=%v", readerErr, writerErr)
	}
	if reflect.TypeOf(reader) != reflect.TypeOf(writer) {
		t.Fatalf("reader and writer must share one concrete adapter type: %T != %T", reader, writer)
	}
}

// TestInsightStoreAdapterFieldCoverage 自动覆盖 Record、UpsertParams 与 ApprovalRow 的导出字段映射。
func TestInsightStoreAdapterFieldCoverage(t *testing.T) {
	t.Run("record_from_store", testInsightRecordFieldCoverage)
	t.Run("upsert_to_store", testInsightUpsertFieldCoverage)
	t.Run("approval_from_store", testInsightApprovalFieldCoverage)
}

func testInsightRecordFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(row insightstore.Insight) (insight.Record, error) {
		reader, err := provideInsightReader(&insightStoreStub{listRecent: func(context.Context, int32) ([]insightstore.Insight, error) {
			return []insightstore.Insight{row}, nil
		}})
		if err != nil {
			return insight.Record{}, err
		}
		rows, err := reader.ListRecent(context.Background(), 1)
		if err != nil {
			return insight.Record{}, err
		}
		return rows[0], nil
	})
}

func testInsightUpsertFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(params insight.UpsertParams) (insightstore.UpsertParams, error) {
		var captured insightstore.UpsertParams
		writer, err := provideInsightWriter(&insightStoreStub{upsert: func(_ context.Context, stored insightstore.UpsertParams) (insightstore.Insight, error) {
			captured = stored
			return insightstore.Insight{}, nil
		}})
		if err != nil {
			return insightstore.UpsertParams{}, err
		}
		_, err = writer.Upsert(context.Background(), params)
		return captured, err
	})
}

func testInsightApprovalFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(row insightstore.ApprovalRow) (insight.ApprovalRow, error) {
		reader, err := provideInsightReader(&insightStoreStub{listApproval: func(context.Context, string, int32) ([]insightstore.ApprovalRow, error) {
			return []insightstore.ApprovalRow{row}, nil
		}})
		if err != nil {
			return insight.ApprovalRow{}, err
		}
		rows, err := reader.ListObservedApprovalRequests(context.Background(), "thread-1", 1)
		if err != nil {
			return insight.ApprovalRow{}, err
		}
		return rows[0], nil
	})
}

// TestInsightStoreAdapterCopiesMutableFields 固定 Success 与 SkillsSelected 在两个边界方向均不共享内存。
func TestInsightStoreAdapterCopiesMutableFields(t *testing.T) {
	t.Run("domain_to_store", testInsightUpsertCopiesMutableFields)
	t.Run("store_to_domain", testInsightRecordCopiesMutableFields)
}

func testInsightUpsertCopiesMutableFields(t *testing.T) {
	success := true
	skills := json.RawMessage(`["skill-a"]`)
	writer, err := provideInsightWriter(&insightStoreStub{upsert: func(_ context.Context, params insightstore.UpsertParams) (insightstore.Insight, error) {
		*params.Success = false
		params.SkillsSelected[0] = '{'
		return insightstore.Insight{}, nil
	}})
	if err != nil {
		t.Fatalf("construct writer: %v", err)
	}
	if _, err := writer.Upsert(context.Background(), insight.UpsertParams{Success: &success, SkillsSelected: skills}); err != nil {
		t.Fatalf("upsert insight: %v", err)
	}
	if !success || string(skills) != `["skill-a"]` {
		t.Fatalf("domain mutable fields were shared: success=%t skills=%s", success, skills)
	}
}

func testInsightRecordCopiesMutableFields(t *testing.T) {
	success := true
	skills := json.RawMessage(`["skill-a"]`)
	reader, err := provideInsightReader(&insightStoreStub{listRecent: func(context.Context, int32) ([]insightstore.Insight, error) {
		return []insightstore.Insight{{Success: &success, SkillsSelected: skills}}, nil
	}})
	if err != nil {
		t.Fatalf("construct reader: %v", err)
	}
	rows, err := reader.ListRecent(context.Background(), 1)
	if err != nil {
		t.Fatalf("list recent insights: %v", err)
	}
	success = false
	skills[0] = '{'
	if rows[0].Success == nil || !*rows[0].Success || string(rows[0].SkillsSelected) != `["skill-a"]` {
		t.Fatalf("store mutable fields were shared: success=%v skills=%s", rows[0].Success, rows[0].SkillsSelected)
	}
}

// TestInsightStoreAdapterPreservesErrors 固定四个读写方法均原样传播 Store 错误链。
func TestInsightStoreAdapterPreservesErrors(t *testing.T) {
	sentinel := errors.New("insight store sentinel")
	storeErr := fmt.Errorf("insight store operation: %w", sentinel)
	reader, writer := newFailingInsightPorts(t, storeErr)
	tests := map[string]func() error{
		"upsert":         func() error { _, err := writer.Upsert(context.Background(), insight.UpsertParams{}); return err },
		"list_by_thread": func() error { _, err := reader.ListByThread(context.Background(), "thread-1", 1); return err },
		"list_recent":    func() error { _, err := reader.ListRecent(context.Background(), 1); return err },
		"list_approvals": func() error {
			_, err := reader.ListObservedApprovalRequests(context.Background(), "thread-1", 1)
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			assertInsightStoreErrorPreserved(t, call(), storeErr, sentinel)
		})
	}
}

func newFailingInsightPorts(t *testing.T, storeErr error) (insight.Reader, insight.Writer) {
	t.Helper()
	store := &insightStoreStub{
		upsert: func(context.Context, insightstore.UpsertParams) (insightstore.Insight, error) {
			return insightstore.Insight{}, storeErr
		},
		listThread:   func(context.Context, string, int32) ([]insightstore.Insight, error) { return nil, storeErr },
		listRecent:   func(context.Context, int32) ([]insightstore.Insight, error) { return nil, storeErr },
		listApproval: func(context.Context, string, int32) ([]insightstore.ApprovalRow, error) { return nil, storeErr },
	}
	reader, readerErr := provideInsightReader(store)
	writer, writerErr := provideInsightWriter(store)
	if readerErr != nil || writerErr != nil {
		t.Fatalf("construct failing insight ports: reader=%v writer=%v", readerErr, writerErr)
	}
	return reader, writer
}

func assertInsightStoreErrorPreserved(t *testing.T, got, storeErr, sentinel error) {
	t.Helper()
	if got != storeErr {
		t.Fatalf("expected original store error, got %v", got)
	}
	if !errors.Is(got, sentinel) {
		t.Fatalf("expected errors.Is to preserve sentinel, got %v", got)
	}
}
