package standalone

import (
	"context"
	"errors"
	"fmt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"reflect"
	"slices"
	"testing"
)

type recoveryArchiveRPCFixture struct {
	t            *testing.T
	thread       recoveryNativeThread
	pages        []recoveryThreadListReply
	calls        []string
	page         int
	unarchiveErr error
}

// call 记录官方RPC顺序并严格检查managed来源、cwd和分页过滤，不启动provider或模型。
func (f *recoveryArchiveRPCFixture) call(_ context.Context, method string, params, result any) error {
	f.calls = append(f.calls, method)
	switch method {
	case "thread/read":
		*result.(*recoveryThreadReply) = recoveryThreadReply{Thread: f.thread}
	case "thread/list":
		p := params.(recoveryThreadListParams)
		assertRecoveryArchiveFilters(f.t, p, f.thread.CWD)
		if f.page >= len(f.pages) {
			f.t.Fatal("unexpected extra archive page")
		}
		*result.(*recoveryThreadListReply) = f.pages[f.page]
		f.page++
	case "thread/unarchive":
		if f.unarchiveErr != nil {
			return f.unarchiveErr
		}
		*result.(*recoveryThreadReply) = recoveryThreadReply{Thread: f.thread}
	default:
		f.t.Fatalf("unexpected RPC %s", method)
	}
	return nil
}

// TestNativeRecoveryArchiveProtocol 验证归档判断、完整分页、owner fence及竞态错误均不兜底。
func TestNativeRecoveryArchiveProtocol(t *testing.T) {
	t.Run("archived-and-active", testNativeRecoveryArchiveSuccess)
	t.Run("malformed-pages", testNativeRecoveryArchiveMalformed)
	t.Run("foreign-read", testNativeRecoveryForeignRead)
	t.Run("unarchive-race", testNativeRecoveryUnarchiveRace)
	t.Run("page-bound", testNativeRecoveryPageBound)
	t.Run("invalid-context", testNativeRecoveryInvalidContext)
}

// recoveryPage 构造显式data页面，空列表与缺失data在测试中严格区分。
func recoveryPage(threads []recoveryNativeThread, cursor *string) recoveryThreadListReply {
	return recoveryThreadListReply{Data: &threads, NextCursor: cursor}
}

// testNativeRecoveryArchiveSuccess 即使第一页找到目标也遍历剩余页，active线程从不unarchive。
func testNativeRecoveryArchiveSuccess(t *testing.T) {
	for _, archived := range []bool{false, true} {
		cwd := t.TempDir()
		thread := recoveryNativeThread{ID: "target", CWD: cwd}
		first := []recoveryNativeThread{}
		if archived {
			first = append(first, thread)
		}
		cursor := "next"
		f := &recoveryArchiveRPCFixture{t: t, thread: thread, pages: []recoveryThreadListReply{recoveryPage(first, &cursor), recoveryPage([]recoveryNativeThread{}, nil)}}
		if err := prepareArchivedCodexRecovery(context.Background(), f.call, "target", cwd); err != nil {
			t.Fatal(err)
		}
		expected := []string{"thread/read", "thread/list", "thread/list"}
		if archived {
			expected = append(expected, "thread/unarchive")
		}
		if !reflect.DeepEqual(f.calls, expected) {
			t.Fatalf("RPC sequence=%v", f.calls)
		}
	}
}

// testNativeRecoveryArchiveMalformed 对缺失data、超量、重复目标和cursor循环立即报错。
func testNativeRecoveryArchiveMalformed(t *testing.T) {
	cwd := t.TempDir()
	thread := recoveryNativeThread{ID: "target", CWD: cwd}
	cursor, empty := "loop", ""
	oversized := make([]recoveryNativeThread, 101)
	for _, tc := range []struct {
		name  string
		pages []recoveryThreadListReply
	}{
		{"missing", []recoveryThreadListReply{{}}},
		{"oversized", []recoveryThreadListReply{recoveryPage(oversized, nil)}},
		{"duplicate", []recoveryThreadListReply{recoveryPage([]recoveryNativeThread{thread, thread}, nil)}},
		{"empty-cursor", []recoveryThreadListReply{recoveryPage([]recoveryNativeThread{}, &empty)}},
		{"cycle", []recoveryThreadListReply{recoveryPage([]recoveryNativeThread{}, &cursor), recoveryPage([]recoveryNativeThread{}, &cursor)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &recoveryArchiveRPCFixture{t: t, thread: thread, pages: tc.pages}
			if err := prepareArchivedCodexRecovery(context.Background(), f.call, "target", cwd); err == nil {
				t.Fatal("malformed archive evidence accepted")
			}
			if slices.Contains(f.calls, "thread/unarchive") {
				t.Fatal("malformed evidence unarchived thread")
			}
		})
	}
}

// testNativeRecoveryForeignRead 拒绝错误thread及真实cwd越界，不发送归档查询。
func testNativeRecoveryForeignRead(t *testing.T) {
	cwd := t.TempDir()
	for _, thread := range []recoveryNativeThread{{ID: "foreign", CWD: cwd}, {ID: "target", CWD: t.TempDir()}} {
		f := &recoveryArchiveRPCFixture{t: t, thread: thread}
		if err := prepareArchivedCodexRecovery(context.Background(), f.call, "target", cwd); err == nil {
			t.Fatal("foreign thread accepted")
		}
		if len(f.calls) != 1 {
			t.Fatal("foreign owner queried archive list")
		}
	}
}

// testNativeRecoveryUnarchiveRace provider归档竞态错误必须返回，不能吞错或自动重发。
func testNativeRecoveryUnarchiveRace(t *testing.T) {
	cwd := t.TempDir()
	thread := recoveryNativeThread{ID: "target", CWD: cwd}
	failure := errors.New("archive raced")
	f := &recoveryArchiveRPCFixture{t: t, thread: thread, pages: []recoveryThreadListReply{recoveryPage([]recoveryNativeThread{thread}, nil)}, unarchiveErr: failure}
	if err := prepareArchivedCodexRecovery(context.Background(), f.call, "target", cwd); !errors.Is(err, failure) {
		t.Fatalf("race error=%v", err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("race retried RPC: %v", f.calls)
	}
}

// testNativeRecoveryPageBound 无限有效cursor不能制造无限查询或默认未归档。
func testNativeRecoveryPageBound(t *testing.T) {
	cwd := t.TempDir()
	f := &recoveryArchiveRPCFixture{t: t, thread: recoveryNativeThread{ID: "target", CWD: cwd}}
	for i := 0; i < 128; i++ {
		cursor := fmt.Sprint(i)
		f.pages = append(f.pages, recoveryPage([]recoveryNativeThread{}, &cursor))
	}
	if err := prepareArchivedCodexRecovery(context.Background(), f.call, "target", cwd); err == nil {
		t.Fatal("unbounded pagination accepted")
	}
	if f.page != 128 {
		t.Fatalf("page count=%d", f.page)
	}
}

// assertRecoveryArchiveFilters 验证查询显式涵盖managed来源，不依赖provider默认交互来源。
func assertRecoveryArchiveFilters(t *testing.T, p recoveryThreadListParams, cwd string) {
	if !p.Archived || p.CWD != cwd || p.Limit != 100 || p.ModelProviders == nil || !slices.Contains(p.SourceKinds, "appServer") || !slices.Contains(p.SourceKinds, "subAgentThreadSpawn") {
		t.Fatal("archive query omitted managed sources or owner filters")
	}
}

// testNativeRecoveryInvalidContext 在读取owner代际前拒绝nil、typed-nil和已取消上下文。
func testNativeRecoveryInvalidContext(t *testing.T) {
	execution := NewExecution(Config{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, context.Context((*nilExecutionContext)(nil)), canceled} {
		if _, err := execution.Recover(ctx, orchestration.PersistedRuntimeActivation{}); err == nil {
			t.Fatal("invalid recovery context accepted")
		}
	}
	if len(execution.sessions) != 0 {
		t.Fatal("invalid context reserved a session")
	}
}
