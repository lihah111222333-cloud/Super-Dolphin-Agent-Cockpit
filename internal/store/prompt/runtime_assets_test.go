package prompt

import "testing"

func TestIsRuntimeAssetTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template PromptTemplate
		want     bool
	}{
		{
			name:     "normal expert",
			template: PromptTemplate{PromptKey: "main/expert", AgentKey: "main", Tags: []byte(`["intent:expert"]`)},
		},
		{
			name:     "recall tag",
			template: PromptTemplate{PromptKey: "main/knowledge/sqlc", AgentKey: "main", Tags: []byte(`["intent:recall"]`)},
			want:     true,
		},
		{
			name:     "default rule tag",
			template: PromptTemplate{PromptKey: "main/default-rule/sqlc", AgentKey: "main", Tags: []byte(`["intent:default_rule"]`)},
			want:     true,
		},
		{
			name:     "default rule agent key",
			template: PromptTemplate{PromptKey: "main/default-rule/sqlc", AgentKey: "default_rule", Tags: []byte(`[]`)},
			want:     true,
		},
		{
			name:     "invalid tags are ignored",
			template: PromptTemplate{PromptKey: "main/expert", AgentKey: "main", Tags: []byte(`{"bad":true}`)},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRuntimeAssetTemplate(tt.template); got != tt.want {
				t.Fatalf("IsRuntimeAssetTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTemplateTags(t *testing.T) {
	t.Parallel()

	got := TemplateTags([]byte(`["scope.cwd:/repo/a","intent:recall"]`))
	if len(got) != 2 || got[0] != "scope.cwd:/repo/a" || got[1] != "intent:recall" {
		t.Fatalf("TemplateTags() = %#v, want two tags", got)
	}
	if got := TemplateTags([]byte(`{"bad":true}`)); got != nil {
		t.Fatalf("TemplateTags() invalid shape = %#v, want nil", got)
	}
}
