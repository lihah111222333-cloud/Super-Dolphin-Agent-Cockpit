package main

import (
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func TestRuntimeServerGraphQLConfigDirArgs(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		command multilsp.ServerCommand
		args    []string
		root    string
		want    []string
		wantErr bool
	}{
		{
			name:    "injects project root",
			command: multilsp.ServerCommand{Executable: "graphql-lsp"},
			args:    []string{"server", "-m", "stream"},
			root:    root,
			want:    []string{"server", "-m", "stream", "--configDir", root},
		},
		{
			name:    "preserves explicit config dir",
			command: multilsp.ServerCommand{Executable: "graphql-lsp"},
			args:    []string{"server", "--configDir", root, "-m", "stream"},
			root:    root,
			want:    []string{"server", "--configDir", root, "-m", "stream"},
		},
		{
			name:    "recognizes platform executable suffix",
			command: multilsp.ServerCommand{Executable: `C:\\tools\\graphql-lsp.cmd`},
			args:    []string{"server", "-m", "stream"},
			root:    root,
			want:    []string{"server", "-m", "stream", "--configDir", root},
		},
		{
			name:    "does not alter other servers",
			command: multilsp.ServerCommand{Executable: "gopls"},
			args:    []string{"serve"},
			root:    root,
			want:    []string{"serve"},
		},
		{
			name:    "fails without root",
			command: multilsp.ServerCommand{Executable: "graphql-lsp"},
			args:    []string{"server", "-m", "stream"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runtimeServerGraphQLConfigDirArgs(tc.command, tc.args, tc.root)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("runtimeServerGraphQLConfigDirArgs() error = nil, want failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("runtimeServerGraphQLConfigDirArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("runtimeServerGraphQLConfigDirArgs() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
