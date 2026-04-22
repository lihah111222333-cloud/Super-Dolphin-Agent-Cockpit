package classifier

import "context"

// NoopClassifier is returned by NewService when the feature is disabled or
// when the concrete backend (claude CLI, Anthropic API, etc.) is unavailable.
// It reports Enabled=false so the router can skip candidate collection
// entirely.
type NoopClassifier struct{}

func (NoopClassifier) Classify(context.Context, Input) (Result, error) {
	return Result{}, nil
}

func (NoopClassifier) Enabled() bool { return false }
