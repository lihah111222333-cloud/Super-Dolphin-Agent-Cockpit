package storeadapter

import (
	"go.uber.org/fx"

	cronadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/cron"
	dashboardadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/dashboard"
	datasourcev2adapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/datasourcev2"
	feedbackadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/feedback"
	insightadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/insight"
	personalizationadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/personalization"
	promptadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/prompt"
	threadadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/thread"
	turnadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/turn"
	uistateadapter "github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter/uistate"
)

// Module 聚合按领域拆分的 Store adapter 子模块。
var Module = newModule()

func newModule() fx.Option {
	return fx.Options(
		cronadapter.Module,
		dashboardadapter.Module,
		datasourcev2adapter.Module,
		feedbackadapter.Module,
		insightadapter.Module,
		personalizationadapter.Module,
		promptadapter.Module,
		threadadapter.Module,
		turnadapter.Module,
		uistateadapter.Module,
	)
}
