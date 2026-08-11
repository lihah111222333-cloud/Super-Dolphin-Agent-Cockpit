package remoteci

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestExactGoTestDigestReusesAcrossUnrelatedTreeChangesForPureStandardLibraryCalls
// 验证普通标准库调用不会把单 selector 指纹错误扩大为整棵候选树。
func TestExactGoTestDigestReusesAcrossUnrelatedTreeChangesForPureStandardLibraryCalls(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testPureStandardLibraryGoTestSnapshot("baseline")
	changed := testPureStandardLibraryGoTestSnapshot("unrelated candidate change")

	want := testExactGoTestDigest(t, baseline, target)
	if got := testExactGoTestDigest(t, changed, target); got != want {
		t.Fatalf("unrelated tree change invalidated exact selector digest: got %s want %s", got, want)
	}
}

// TestExactGoTestDigestKeepsDynamicProcessObservationTreeScoped
// 验证真正动态的进程观察仍绑定整棵候选树，避免用优化制造错误命中。
func TestExactGoTestDigestKeepsDynamicProcessObservationTreeScoped(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testDynamicProcessGoTestSnapshot("baseline")
	changed := testDynamicProcessGoTestSnapshot("unrelated candidate change")

	want := testExactGoTestDigest(t, baseline, target)
	if got := testExactGoTestDigest(t, changed, target); got == want {
		t.Fatal("dynamic process observation no longer binds the candidate tree")
	}
}

// testPureStandardLibraryGoTestSnapshot 构造只调用无仓库观察能力标准库函数的 selector。
func testPureStandardLibraryGoTestSnapshot(unrelated string) *remoteGitTreeSnapshot {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "errors"
func pureStandardLibraryCall() error { return errors.New("fixed") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { _ = pureStandardLibraryCall() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "docs/unrelated.md", []byte(unrelated))
	return snapshot
}

// testDynamicProcessGoTestSnapshot 构造必须继续保守绑定整树的动态进程 selector。
func testDynamicProcessGoTestSnapshot(unrelated string) *remoteGitTreeSnapshot {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os/exec"
func dynamicProcess(command string) error { return exec.Command(command).Run() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { _ = dynamicProcess("true") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "docs/unrelated.md", []byte(unrelated))
	return snapshot
}
