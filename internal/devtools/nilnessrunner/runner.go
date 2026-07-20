package nilnessrunner

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/typednil"
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/analysis/passes/nilness"
)

// Main 运行 release 与 push 共用的离线 nilness analyzer 集合。
func Main() {
	multichecker.Main(nilness.Analyzer, typednil.Analyzer)
}
