package storeadapter

import (
	"go.uber.org/fx"

	cronadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/cron"
	dashboardadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/dashboard"
	datasourcev2adapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/datasourcev2"
	feedbackadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/feedback"
	insightadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/insight"
	memoryadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/memory"
	personalizationadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/personalization"
	promptadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/prompt"
	skilladapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/skill"
	turnadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/turn"
	uistateadapter "github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter/uistate"
)

// Module 聚合已经迁入独立包的简单 Store adapter 领域。
var Module = fx.Options(
	cronadapter.Module,
	dashboardadapter.Module,
	datasourcev2adapter.Module,
	feedbackadapter.Module,
	insightadapter.Module,
	memoryadapter.Module,
	personalizationadapter.Module,
	promptadapter.Module,
	skilladapter.Module,
	turnadapter.Module,
	uistateadapter.Module,
)
