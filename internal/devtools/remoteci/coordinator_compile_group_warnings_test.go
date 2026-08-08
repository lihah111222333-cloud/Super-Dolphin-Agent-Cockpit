package remoteci

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCompileGroupPlanWarningsProjectToRunWarningProjection(t *testing.T) {
	warnings := compileGroupPlanWarnings([]gate.CompileGroup{
		{GroupID: "sha256:" + strings.Repeat("a", 64), PackageTarget: "./pkg/a", ResourceClassID: "medium", BatchPlanWarning: "critical_batch_plus_compile_ms=104000 exceeds_target_ms=100000 at_max_batches=4"},
		{BatchPlanWarning: "  "},
		{GroupID: "sha256:" + strings.Repeat("b", 64), PackageTarget: "./pkg/b", ResourceClassID: "medium", BatchPlanWarning: "critical_batch_plus_compile_ms=104000 exceeds_target_ms=100000 at_max_batches=4"},
	})
	want := []string{
		"compile_group_plan group_id=sha256:" + strings.Repeat("a", 64) + " package_target=./pkg/a resource_class_id=medium warning=critical_batch_plus_compile_ms=104000 exceeds_target_ms=100000 at_max_batches=4",
		"compile_group_plan group_id=sha256:" + strings.Repeat("b", 64) + " package_target=./pkg/b resource_class_id=medium warning=critical_batch_plus_compile_ms=104000 exceeds_target_ms=100000 at_max_batches=4",
	}
	if !reflect.DeepEqual(warnings, want) {
		t.Fatalf("compile group warning projection = %#v, want %#v", warnings, want)
	}
}
