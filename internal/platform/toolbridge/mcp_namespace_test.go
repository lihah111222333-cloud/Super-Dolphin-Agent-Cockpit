package toolbridge

import "testing"

func TestWrapMCPToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		server string
		tool   string
		want   string
	}{
		{name: "wraps namespaced tool", server: " sqlite ", tool: " query ", want: "mcp__sqlite__query"},
		{name: "empty server returns tool", server: " ", tool: " query ", want: "query"},
		{name: "empty tool returns empty", server: "sqlite", tool: " ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := WrapMCPToolName(tt.server, tt.tool); got != tt.want {
				t.Fatalf("WrapMCPToolName(%q, %q) = %q, want %q", tt.server, tt.tool, got, tt.want)
			}
		})
	}
}

func TestSplitMCPToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want MCPToolNamespace
		ok   bool
	}{
		{name: "splits namespaced tool", in: " mcp__sqlite__query ", want: MCPToolNamespace{Server: "sqlite", Tool: "query"}, ok: true},
		{name: "keeps inner separators", in: "mcp__lsp__foo__bar", want: MCPToolNamespace{Server: "lsp", Tool: "foo__bar"}, ok: true},
		{name: "rejects plain tool", in: "query", ok: false},
		{name: "rejects empty server", in: "mcp____query", ok: false},
		{name: "rejects empty tool", in: "mcp__sqlite__", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := SplitMCPToolName(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("SplitMCPToolName(%q) = %#v, %v; want %#v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
