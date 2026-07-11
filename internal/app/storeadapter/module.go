package storeadapter

import (
	"go.uber.org/fx"

	datasourcev2adapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/datasourcev2"
	feedbackadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/feedback"
	insightadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/insight"
	memoryadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/memory"
	personalizationadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/personalization"
	turnadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/turn"
	uistateadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/uistate"
)

// Module 聚合已经迁入独立包的简单 Store adapter 领域。
var Module = fx.Options(
	datasourcev2adapter.Module,
	feedbackadapter.Module,
	insightadapter.Module,
	memoryadapter.Module,
	personalizationadapter.Module,
	turnadapter.Module,
	uistateadapter.Module,
)
