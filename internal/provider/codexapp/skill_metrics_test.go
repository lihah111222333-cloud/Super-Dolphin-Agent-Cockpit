package codexapp

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

func testSkillMetrics(t *testing.T) *skillmetrics.Registry {
	t.Helper()
	return skillmetrics.NewRegistry()
}
