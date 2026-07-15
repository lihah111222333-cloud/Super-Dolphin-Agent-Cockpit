package typednil_test

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/typednil"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	t.Parallel()
	analysistest.Run(t, analysistest.TestData(), typednil.Analyzer, "typednilfixture")
}
