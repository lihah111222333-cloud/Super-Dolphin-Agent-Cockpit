package nodeexec

import "testing"

func TestResolveOnFailureStrategy_NilConfigDefaultsRetry(t *testing.T) {
	t.Parallel()
	got := ResolveOnFailureStrategy(nil, FailureClassCapability)
	if got != OnFailureRetry {
		t.Errorf("nil cfg should default to retry, got %q", got)
	}
}

func TestResolveOnFailureStrategy_ByClassHit(t *testing.T) {
	t.Parallel()
	cfg := &OnFailureConfig{
		Default: OnFailureFailFast,
		ByClass: map[FailureClass]OnFailureStrategy{
			FailureClassCapability: OnFailureEscalateModel,
			FailureClassValidation: OnFailureAppendError,
		},
	}
	cases := []struct {
		class FailureClass
		want  OnFailureStrategy
	}{
		{FailureClassCapability, OnFailureEscalateModel},
		{FailureClassValidation, OnFailureAppendError},
		{FailureClassQuota, OnFailureFailFast}, // 未在 ByClass 命中走 Default
		{FailureClassHard, OnFailureFailFast},  // 未在 ByClass 命中走 Default
		{"", OnFailureFailFast},                // 空 class 走 Default
	}
	for _, tc := range cases {
		t.Run(string(tc.class), func(t *testing.T) {
			got := ResolveOnFailureStrategy(cfg, tc.class)
			if got != tc.want {
				t.Errorf("class=%q: got %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}

func TestResolveOnFailureStrategy_DefaultEmptyFallsBackRetry(t *testing.T) {
	t.Parallel()
	cfg := &OnFailureConfig{} // 既无 Default 也无 ByClass
	got := ResolveOnFailureStrategy(cfg, FailureClassQuota)
	if got != OnFailureRetry {
		t.Errorf("empty cfg should default to retry, got %q", got)
	}
}

func TestResolveOnFailureStrategy_EmptyByClassValueFallsBackDefault(t *testing.T) {
	t.Parallel()
	// ByClass 命中但值为空字符串 → 不算命中，走 Default
	cfg := &OnFailureConfig{
		Default: OnFailureSkip,
		ByClass: map[FailureClass]OnFailureStrategy{
			FailureClassCapability: "",
		},
	}
	got := ResolveOnFailureStrategy(cfg, FailureClassCapability)
	if got != OnFailureSkip {
		t.Errorf("empty ByClass value should fall back to Default, got %q", got)
	}
}

func TestMaxAttemptsFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *OnFailureConfig
		want int
	}{
		{"nil cfg", nil, 1},
		{"zero MaxAttempts", &OnFailureConfig{MaxAttempts: 0}, 1},
		{"negative MaxAttempts", &OnFailureConfig{MaxAttempts: -1}, 1},
		{"positive", &OnFailureConfig{MaxAttempts: 5}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxAttemptsFor(tc.cfg); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEscalationModelFor_NextInChain(t *testing.T) {
	t.Parallel()
	cfg := &OnFailureConfig{EscalationChain: []string{"haiku", "sonnet", "opus"}}
	cases := []struct {
		current string
		want    string
		ok      bool
	}{
		{"haiku", "sonnet", true}, // 升级
		{"sonnet", "opus", true},  // 升级
		{"opus", "", false},       // 已在链尾
	}
	for _, tc := range cases {
		t.Run(tc.current, func(t *testing.T) {
			got, ok := EscalationModelFor(cfg, tc.current)
			if got != tc.want || ok != tc.ok {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestEscalationModelFor_CurrentNotInChainReturnsFirst(t *testing.T) {
	t.Parallel()
	cfg := &OnFailureConfig{EscalationChain: []string{"sonnet", "opus"}}
	got, ok := EscalationModelFor(cfg, "haiku-3") // 不在链中
	if !ok || got != "sonnet" {
		t.Errorf("got (%q, %v), want (sonnet, true) — fall back to chain head", got, ok)
	}
}

func TestEscalationModelFor_NilOrEmptyChain(t *testing.T) {
	t.Parallel()
	if got, ok := EscalationModelFor(nil, "sonnet"); ok || got != "" {
		t.Errorf("nil cfg: got (%q, %v), want (\"\", false)", got, ok)
	}
	if got, ok := EscalationModelFor(&OnFailureConfig{}, "sonnet"); ok || got != "" {
		t.Errorf("empty chain: got (%q, %v), want (\"\", false)", got, ok)
	}
}
