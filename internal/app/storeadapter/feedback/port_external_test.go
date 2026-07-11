package feedbackadapter_test

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/feedback"
)

type externalFeedbackWriter struct{}

func (externalFeedbackWriter) Insert(_ context.Context, event feedback.Event) (feedback.Event, error) {
	return event, nil
}

var _ feedback.Writer = externalFeedbackWriter{}
