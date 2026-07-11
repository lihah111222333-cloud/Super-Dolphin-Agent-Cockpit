package app_test

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/insight"
)

type externalInsightPort struct{}

func (externalInsightPort) Upsert(_ context.Context, _ insight.UpsertParams) (insight.Record, error) {
	return insight.Record{}, nil
}

func (externalInsightPort) ListByThread(context.Context, string, int32) ([]insight.Record, error) {
	return nil, nil
}

func (externalInsightPort) ListRecent(context.Context, int32) ([]insight.Record, error) {
	return nil, nil
}

func (externalInsightPort) ListObservedApprovalRequests(context.Context, string, int32) ([]insight.ApprovalRow, error) {
	return nil, nil
}

var _ insight.Reader = externalInsightPort{}
var _ insight.Writer = externalInsightPort{}
