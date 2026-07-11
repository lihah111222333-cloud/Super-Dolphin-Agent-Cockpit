package app_test

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/feedback"
)

type externalFeedbackWriter struct{}

func (externalFeedbackWriter) Insert(_ context.Context, event feedback.Event) (feedback.Event, error) {
	return event, nil
}

var _ feedback.Writer = externalFeedbackWriter{}
