// Package feedback 提供用户反馈事件的记录能力，通过 JSON-RPC 接口接收前端事件并持久化。
package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// feedbackEvent 是 feedback 服务写入持久层所需的模块内事件投影。
type feedbackEvent struct {
	ID              int64
	ThreadID        string
	TurnID          string
	AgentKey        string
	PromptVersionID *int64
	EventType       string
	Actor           string
	Payload         json.RawMessage
	CreatedAt       time.Time
}

// feedbackWriter 提供 feedback 服务记录事件所需的最小写入口。
type feedbackWriter interface {
	Insert(ctx context.Context, ev feedbackEvent) (feedbackEvent, error)
}

// service 是 feedback 模块的内部实现，通过 feedbackWriter 持久化事件。
type service struct {
	logger *slog.Logger
	store  feedbackWriter
}

var _ Service = (*service)(nil)

// NewService 创建 feedback 事件记录服务。
// logger 缺失时使用包默认 logger；store 缺失不会在构造时兜底，Record 会 fail-fast。
func NewService(logger *slog.Logger, store feedbackWriter) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{logger: logger, store: store}
}

var errServiceDisabled = errors.New("feedback: service not wired (store is nil)")

// Record 校验并持久化一次前端 feedback 事件。
// thread_id 和 event_type 是最小必填 wire 字段；payload 作为原始 JSON 透传给 store。
func (s *service) Record(ctx context.Context, req RecordRequest) (RecordResult, error) {
	ctx = util.NonNilContext(ctx)
	if s.store == nil {
		return RecordResult{}, errServiceDisabled
	}
	threadID := strings.TrimSpace(req.ThreadID)
	eventType := strings.TrimSpace(req.EventType)
	if threadID == "" || eventType == "" {
		return RecordResult{}, errors.New("feedback/record: thread_id and event_type are required")
	}
	ev, err := s.store.Insert(ctx, feedbackEvent{
		ThreadID:        threadID,
		TurnID:          strings.TrimSpace(req.TurnID),
		AgentKey:        strings.TrimSpace(req.AgentKey),
		PromptVersionID: req.PromptVersionID,
		EventType:       eventType,
		Actor:           strings.TrimSpace(req.Actor),
		Payload:         req.Payload,
	})
	if err != nil {
		s.logger.Error("feedback/record: insert failed",
			slog.String("thread_id", threadID),
			slog.String("event_type", eventType),
			slog.String("error", err.Error()),
		)
		return RecordResult{}, err
	}
	return RecordResult{ID: ev.ID, EventType: ev.EventType, Recorded: true}, nil
}
