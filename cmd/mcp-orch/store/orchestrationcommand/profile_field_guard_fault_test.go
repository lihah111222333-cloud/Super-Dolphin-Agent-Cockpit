package orchestrationcommand

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSendProfileMapperRealOmissionsFailGuard 从真实 mapper 副本删除四类嵌套字段，验证同一守卫 RED 后恢复 GREEN。
func TestSendProfileMapperRealOmissionsFailGuard(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate profile mapper source")
	}
	sourcePath := filepath.Join(filepath.Dir(currentFile), "profile_mapping.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read real profile mapper: %v", err)
	}
	cases := []struct{ path, snippet string }{
		{"Caller.AuthContext", "CallerAuthContext: profile.Caller.AuthContext,"},
		{"Authorization.GrantRevision", "GrantRevision: row.AuthorizationGrantRevision,"},
		{"Source.ActiveProcessReceiptRef", "SourceActiveProcessReceiptRef: profile.Source.ActiveProcessReceiptRef,"},
		{"Target.BindingDigest", "BindingDigest: row.TargetBindingDigest,"},
	}
	for _, item := range cases {
		t.Run(item.path, func(t *testing.T) {
			assertProfileMapperOmissionRedGreen(t, source, item.path, item.snippet)
		})
	}
}

func assertProfileMapperOmissionRedGreen(t *testing.T, source []byte, path, snippet string) {
	t.Helper()
	if strings.Count(string(source), snippet) != 1 {
		t.Fatalf("real mapper branch %q must exist exactly once", snippet)
	}
	fixture := filepath.Join(t.TempDir(), "profile_mapping.go")
	broken := strings.Replace(string(source), snippet, "", 1)
	if err := os.WriteFile(fixture, []byte(broken), 0o600); err != nil {
		t.Fatalf("write broken profile mapper fixture: %v", err)
	}
	output, err := runProfileMapperGuardFixture(t, fixture)
	if err == nil || !strings.Contains(output, path) {
		t.Fatalf("removing %s did not produce precise RED: err=%v output=%s", path, err, output)
	}
	t.Logf("RED: removed %s from actual profile mapper source copy", path)
	if err := os.WriteFile(fixture, source, 0o600); err != nil {
		t.Fatalf("restore profile mapper fixture: %v", err)
	}
	if output, err := runProfileMapperGuardFixture(t, fixture); err != nil {
		t.Fatalf("restored %s mapper did not return GREEN: %v %s", path, err, output)
	}
	t.Logf("GREEN: restored %s in the same mapper guard", path)
}

func runProfileMapperGuardFixture(t *testing.T, fixture string) (string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSendProfileMapperCoverage$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ORCH_PROFILE_MAPPING_FIXTURE="+fixture)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("profile mapper fixture guard timeout: %v", ctx.Err())
	}
	return string(output), err
}
