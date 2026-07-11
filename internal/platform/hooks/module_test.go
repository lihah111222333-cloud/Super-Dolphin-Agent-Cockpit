package hooks

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"go.uber.org/fx"
)

type lifecycleRecorder struct {
	hooks []fx.Hook
}

func (r *lifecycleRecorder) Append(hook fx.Hook) {
	r.hooks = append(r.hooks, hook)
}

// countingHookReviewStore 为 lifecycle 测试提供 pending review count 能力。
type countingHookReviewStore struct {
	*stubHookReviewStore
	countPendingReviewsFunc func(context.Context) (int64, error)
	countPendingCalls       int
}

// CountPendingReviews 记录启动恢复 count 调用并返回测试注入结果。
func (s *countingHookReviewStore) CountPendingReviews(ctx context.Context) (int64, error) {
	s.countPendingCalls++
	if s.countPendingReviewsFunc != nil {
		return s.countPendingReviewsFunc(ctx)
	}
	return 0, nil
}

// ListPendingReviewsPage 满足 HookPendingReviewPager；本测试路径不会读取 rows。
func (s *countingHookReviewStore) ListPendingReviewsPage(context.Context, contract.HookPendingReviewPageParams) (contract.HookPendingReviewPage, error) {
	return contract.HookPendingReviewPage{}, nil
}

func TestRegisterRecoveryLifecycle_OnStartCallsRecoverOnStartup(t *testing.T) {
	t.Parallel()

	store := &countingHookReviewStore{
		stubHookReviewStore: &stubHookReviewStore{},
		countPendingReviewsFunc: func(ctx context.Context) (int64, error) {
			if ctx == nil {
				t.Fatal("CountPendingReviews() ctx = nil, want non-nil")
			}
			return 1, nil
		},
	}
	resolver := mustNewHookResolver(t, store)
	registry := NewHookRegistry()
	manager := mustNewManager(t, registry, mustNewHookDispatcher(t, registry, stubPeerCallback{}), resolver)
	recorder := &lifecycleRecorder{}

	registerRecoveryLifecycle(recorder, recoveryLifecycleIn{
		Resolver: resolver,
		Manager:  manager,
		Logger:   pkglogger.New(pkglogger.NewTextHandler(io.Discard, nil)),
	})

	if len(recorder.hooks) != 1 {
		t.Fatalf("registered hooks = %d, want 1", len(recorder.hooks))
	}
	if recorder.hooks[0].OnStart == nil {
		t.Fatal("OnStart = nil, want recovery hook")
	}
	if err := recorder.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	if store.countPendingCalls != 1 {
		t.Fatalf("CountPendingReviews() calls = %d, want 1", store.countPendingCalls)
	}
}

// TestRecoverOnStartupUsesPendingReviewCount 固定启动恢复只读 pending review 数量。
func TestRecoverOnStartupUsesPendingReviewCount(t *testing.T) {
	t.Parallel()

	store := &countingHookReviewStore{
		stubHookReviewStore: &stubHookReviewStore{
			recoverOnStartupFunc: func(context.Context) ([]mcp.PendingHookReview, error) {
				t.Fatal("RecoverOnStartup() should not load pending review rows during module startup")
				return nil, nil
			},
		},
		countPendingReviewsFunc: func(ctx context.Context) (int64, error) {
			if ctx == nil {
				t.Fatal("CountPendingReviews() ctx = nil, want non-nil")
			}
			return 7, nil
		},
	}
	resolver := mustNewHookResolver(t, store)
	registry := NewHookRegistry()
	manager := mustNewManager(t, registry, mustNewHookDispatcher(t, registry, stubPeerCallback{}), resolver)
	recorder := &lifecycleRecorder{}

	registerRecoveryLifecycle(recorder, recoveryLifecycleIn{
		Resolver: resolver,
		Manager:  manager,
		Logger:   pkglogger.New(pkglogger.NewTextHandler(io.Discard, nil)),
	})

	if len(recorder.hooks) != 1 || recorder.hooks[0].OnStart == nil {
		t.Fatal("recovery lifecycle hook was not registered")
	}
	if err := recorder.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	if store.countPendingCalls != 1 {
		t.Fatalf("CountPendingReviews() calls = %d, want 1", store.countPendingCalls)
	}
	if store.recoverCalls != 0 {
		t.Fatalf("RecoverOnStartup() calls = %d, want 0", store.recoverCalls)
	}
}

// TestListPendingReviewsRequiresLimit 固定 pending review 分页入口必须显式提供 limit。
func TestListPendingReviewsRequiresLimit(t *testing.T) {
	t.Parallel()

	resolver := mustNewHookResolver(t, &stubHookReviewStore{})
	registry := NewHookRegistry()
	manager := mustNewManager(t, registry, mustNewHookDispatcher(t, registry, stubPeerCallback{}), resolver)

	_, err := manager.GetPendingReviewsPage(context.Background(), contract.HookPendingReviewPageParams{
		AgentID: "agent-missing-limit",
	})

	if err == nil || !strings.Contains(err.Error(), "limit is required") {
		t.Fatalf("GetPendingReviewsPage() error = %v, want limit is required", err)
	}
}

// TestListPendingReviewsRejectsOverLimit 固定 pending review 分页入口不能静默裁剪过大 limit。
func TestListPendingReviewsRejectsOverLimit(t *testing.T) {
	t.Parallel()

	store := &countingHookReviewStore{stubHookReviewStore: &stubHookReviewStore{}}
	resolver := mustNewHookResolver(t, store)
	registry := NewHookRegistry()
	manager := mustNewManager(t, registry, mustNewHookDispatcher(t, registry, stubPeerCallback{}), resolver)

	_, err := manager.GetPendingReviewsPage(context.Background(), contract.HookPendingReviewPageParams{
		AgentID: "agent-over-limit",
		Limit:   contract.HookPendingReviewMaxPageLimit + 1,
	})

	if err == nil || !strings.Contains(err.Error(), "limit exceeds maximum") {
		t.Fatalf("GetPendingReviewsPage() error = %v, want limit exceeds maximum", err)
	}
}
