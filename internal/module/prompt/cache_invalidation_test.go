package prompt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRegisterDynamicProviderRebuildsCachedSection(t *testing.T) {
	t.Parallel()

	svc := NewService(&Config{}, nil)
	v1Calls := 0
	mustRegisterDynamicTextProvider(t, svc, DynamicSectionSessionGuidance, func(context.Context, SectionContext) (*string, error) {
		v1Calls++
		text := "session guidance v1"
		return &text, nil
	})

	first := mustTurnSection(t, svc, DynamicSectionSessionGuidance)
	second := mustTurnSection(t, svc, DynamicSectionSessionGuidance)
	if v1Calls != 1 {
		t.Fatalf("v1 provider calls = %d, want 1", v1Calls)
	}
	if first != second {
		t.Fatalf("cached section mismatch: first=%q second=%q", first, second)
	}

	v2Calls := 0
	mustRegisterDynamicTextProvider(t, svc, DynamicSectionSessionGuidance, func(context.Context, SectionContext) (*string, error) {
		v2Calls++
		text := "session guidance v2"
		return &text, nil
	})

	third := mustTurnSection(t, svc, DynamicSectionSessionGuidance)
	if v2Calls != 1 {
		t.Fatalf("v2 provider calls = %d, want 1", v2Calls)
	}
	if third == first || !strings.Contains(third, "session guidance v2") {
		t.Fatalf("rebuilt section = %q, want fresh v2 content", third)
	}
}

func TestConcurrentInvalidateAndAssembleTurnRemainSafe(t *testing.T) {
	t.Parallel()

	svc := NewService(&Config{}, nil)
	var version atomic.Int64
	version.Store(1)
	mustRegisterDynamicTextProvider(t, svc, DynamicSectionSessionGuidance, func(context.Context, SectionContext) (*string, error) {
		text := fmt.Sprintf("session guidance v%d", version.Load())
		return &text, nil
	})

	errCh := make(chan error, 16)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				text, err := turnSectionText(svc, DynamicSectionSessionGuidance)
				if err != nil {
					errCh <- err
					return
				}
				if !strings.HasPrefix(text, "session guidance v") {
					errCh <- fmt.Errorf("unexpected section text %q", text)
					return
				}
			}
		})
	}

	for next := int64(2); next <= 6; next++ {
		version.Store(next)
		if err := svc.Invalidate(context.Background(), InvalidateProviderSwitch); err != nil {
			t.Fatalf("Invalidate() error = %v", err)
		}
	}
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	final := mustTurnSection(t, svc, DynamicSectionSessionGuidance)
	if !strings.Contains(final, "session guidance v6") {
		t.Fatalf("final section = %q, want latest version", final)
	}
}

func TestPersonalizationProfileCacheIsScopedByCWD(t *testing.T) {
	t.Parallel()

	svc := NewService(&Config{}, nil)
	var calls atomic.Int64
	mustRegisterDynamicTextProvider(t, svc, DynamicSectionPersonalizationProfile, func(_ context.Context, input SectionContext) (*string, error) {
		calls.Add(1)
		text := "profile for " + contract.SectionContextCWD(input)
		return &text, nil
	})

	first := mustTurnSectionWithInput(t, svc, DynamicSectionPersonalizationProfile, TurnInput{CWD: "/repo/a"})
	second := mustTurnSectionWithInput(t, svc, DynamicSectionPersonalizationProfile, TurnInput{CWD: "/repo/b"})
	third := mustTurnSectionWithInput(t, svc, DynamicSectionPersonalizationProfile, TurnInput{CWD: "/repo/a"})
	if !strings.Contains(first, "/repo/a") {
		t.Fatalf("first profile = %q, want /repo/a", first)
	}
	if strings.Contains(second, "/repo/a") || !strings.Contains(second, "/repo/b") {
		t.Fatalf("second profile = %q, want only /repo/b", second)
	}
	if third != first {
		t.Fatalf("same cwd personalization profile cache mismatch: first=%q third=%q", first, third)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want one per distinct cwd", got)
	}
}

func mustRegisterDynamicTextProvider(t *testing.T, svc Service, name string, resolve func(context.Context, SectionContext) (*string, error)) {
	t.Helper()

	registerDynamicProviderForTest(t, svc, DynamicTextProvider{Name: name, ResolveFunc: resolve})
}

func mustTurnSection(t *testing.T, svc Service, name string) string {
	t.Helper()

	text, err := turnSectionTextWithInput(svc, name, TurnInput{})
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func mustTurnSectionWithInput(t *testing.T, svc Service, name string, input TurnInput) string {
	t.Helper()

	text, err := turnSectionTextWithInput(svc, name, input)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func turnSectionText(svc Service, name string) (string, error) {
	return turnSectionTextWithInput(svc, name, TurnInput{})
}

func turnSectionTextWithInput(svc Service, name string, input TurnInput) (string, error) {
	turn, err := svc.AssembleTurn(context.Background(), input)
	if err != nil {
		return "", fmt.Errorf("AssembleTurn(): %w", err)
	}
	for _, section := range turn.ResolvedSections {
		if section.Name == name {
			return section.Content, nil
		}
	}
	return "", fmt.Errorf("resolved section %q missing", name)
}
