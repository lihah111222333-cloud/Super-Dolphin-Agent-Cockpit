package storeadapter

import (
	"go.uber.org/fx"

	cronadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/cron"
	dashboardadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/dashboard"
	datasourcev2adapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/datasourcev2"
	feedbackadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/feedback"
	insightadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/insight"
	memoryadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/memory"
	personalizationadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/personalization"
	promptadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/prompt"
	skilladapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/skill"
	threadadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/thread"
	turnadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/turn"
	uistateadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/uistate"
)

// Module 聚合按领域拆分的 Store adapter 子模块。
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
	threadadapter.Module,
	turnadapter.Module,
	uistateadapter.Module,
)
