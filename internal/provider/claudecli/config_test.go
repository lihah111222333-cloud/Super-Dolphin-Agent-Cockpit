package claudecli

import (
	"reflect"
	"testing"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestConfigStringSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  map[string]any
		keys []string
		want []string
	}{
		{
			name: "string slice",
			cfg:  map[string]any{"autoApprove": []string{" tool.alpha ", "", "tool.beta"}},
			keys: []string{"autoApprove"},
			want: []string{"tool.alpha", "tool.beta"},
		},
		{
			name: "any slice",
			cfg:  map[string]any{"autoApprove": []any{" tool.alpha ", 7, "", "tool.beta"}},
			keys: []string{"autoApprove"},
			want: []string{"tool.alpha", "tool.beta"},
		},
		{
			name: "comma separated string",
			cfg:  map[string]any{"autoApprove": " tool.alpha, ,tool.beta "},
			keys: []string{"autoApprove"},
			want: []string{"tool.alpha", "tool.beta"},
		},
		{
			name: "falls back to next key when first value is empty",
			cfg: map[string]any{
				"autoApprove":  []any{0, "", false},
				"auto_approve": []string{"tool.gamma"},
			},
			keys: []string{"autoApprove", "auto_approve"},
			want: []string{"tool.gamma"},
		},
		{
			name: "no usable values",
			cfg:  map[string]any{"autoApprove": []any{0, "", false}},
			keys: []string{"autoApprove"},
			want: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := providershared.ConfigStringSlice(tt.cfg, tt.keys...); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ConfigStringSlice() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
