package contract

import (
	"encoding/json"
	"testing"
)

// TestPromptTemplateTagsDecodesJSONArrays verifies runtime tag parsing stays stable.
func TestPromptTemplateTagsDecodesJSONArrays(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal([]string{"intent:recall", "scope.cwd:/repo"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	got := PromptTemplateTags(raw)
	if len(got) != 2 || got[0] != "intent:recall" || got[1] != "scope.cwd:/repo" {
		t.Fatalf("PromptTemplateTags() = %#v", got)
	}
	if got := PromptTemplateTags(json.RawMessage(`{"bad": true}`)); got != nil {
		t.Fatalf("PromptTemplateTags(invalid shape) = %#v, want nil", got)
	}
}

// TestIsRuntimeAssetPromptTemplateRecognizesIntentAssets verifies non-launchable assets are identified.
func TestIsRuntimeAssetPromptTemplateRecognizesIntentAssets(t *testing.T) {
	t.Parallel()

	recallTags, err := json.Marshal([]string{"intent:recall"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	cases := []struct {
		name     string
		template PromptTemplate
		want     bool
	}{
		{
			name:     "default rule agent key",
			template: PromptTemplate{AgentKey: " default_rule "},
			want:     true,
		},
		{
			name:     "recall tag",
			template: PromptTemplate{Tags: recallTags},
			want:     true,
		},
		{
			name:     "ordinary expert",
			template: PromptTemplate{AgentKey: "explore", Tags: json.RawMessage(`["scope.global"]`)},
			want:     false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRuntimeAssetPromptTemplate(tc.template); got != tc.want {
				t.Fatalf("IsRuntimeAssetPromptTemplate() = %v, want %v", got, tc.want)
			}
		})
	}
}
