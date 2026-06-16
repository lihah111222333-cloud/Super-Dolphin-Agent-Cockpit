package titleextract

import "testing"

// TestExtractCleansMentionFillerAndParticles verifies noisy user prompts become stable thread titles.
func TestExtractCleansMentionFillerAndParticles(t *testing.T) {
	t.Parallel()

	got := Extract("@coder，请帮我修复登录失败的问题。后面还有上下文")
	if got != "修复登录失败问题" {
		t.Fatalf("Extract() = %q, want %q", got, "修复登录失败问题")
	}
}

// TestExtractRejectsPronounOnlyTitles verifies vague pronoun-only prompts do not become titles.
func TestExtractRejectsPronounOnlyTitles(t *testing.T) {
	t.Parallel()

	if got := Extract("你 这个"); got != "" {
		t.Fatalf("Extract() = %q, want empty pronoun-only title", got)
	}
}

// TestCountDisplayUnitsCountsChineseRunesAndEnglishWords verifies mixed-language display budgeting.
func TestCountDisplayUnitsCountsChineseRunesAndEnglishWords(t *testing.T) {
	t.Parallel()

	if got := CountDisplayUnits("修复 login error now"); got != 5 {
		t.Fatalf("CountDisplayUnits() = %d, want 5", got)
	}
}

// TestContinuationNameIncrementsSuffix verifies continuation names remain deterministic across resumes.
func TestContinuationNameIncrementsSuffix(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"任务":       "任务 (续)",
		"任务 (续)":   "任务 (续 2)",
		"任务 (续 9)": "任务 (续 10)",
	}
	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			if got := ContinuationName(input); got != want {
				t.Fatalf("ContinuationName() = %q, want %q", got, want)
			}
		})
	}
}
