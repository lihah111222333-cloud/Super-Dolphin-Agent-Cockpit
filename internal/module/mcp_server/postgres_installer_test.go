package mcpserver

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestNPMPostgresInstallerSkipsInstallWhenCommandExists(t *testing.T) {
	runCalls := 0
	installer := npmPostgresInstaller{
		lookPath: func(name string) (string, error) {
			if name != defaultPostgresCommand {
				t.Fatalf("LookPath(%q), want only postgres command", name)
			}
			return "/usr/local/bin/mcp-server-postgres", nil
		},
		run: func(context.Context, string, ...string) ([]byte, error) {
			runCalls++
			return nil, nil
		},
	}

	if err := installer.EnsureInstalled(context.Background()); err != nil {
		t.Fatalf("EnsureInstalled() error = %v", err)
	}
	if runCalls != 0 {
		t.Fatalf("run calls = %d, want 0", runCalls)
	}
}

func TestNPMPostgresInstallerInstallsGloballyWhenCommandIsMissing(t *testing.T) {
	postgresAvailable := false
	var gotCommand string
	var gotArgs []string
	installer := npmPostgresInstaller{
		lookPath: func(name string) (string, error) {
			switch name {
			case defaultPostgresCommand:
				if postgresAvailable {
					return "/usr/local/bin/mcp-server-postgres", nil
				}
				return "", errors.New("missing postgres command")
			case npmCommand:
				return "/usr/local/bin/npm", nil
			default:
				return "", errors.New("unexpected command")
			}
		},
		run: func(_ context.Context, command string, args ...string) ([]byte, error) {
			gotCommand = command
			gotArgs = append([]string(nil), args...)
			postgresAvailable = true
			return []byte("installed"), nil
		},
	}

	if err := installer.EnsureInstalled(context.Background()); err != nil {
		t.Fatalf("EnsureInstalled() error = %v", err)
	}
	if gotCommand != "/usr/local/bin/npm" {
		t.Fatalf("run command = %q, want npm path", gotCommand)
	}
	wantArgs := []string{"install", "-g", defaultPostgresPackage}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("run args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestNPMPostgresInstallerFailsWhenInstallDoesNotExposeCommand(t *testing.T) {
	installer := npmPostgresInstaller{
		lookPath: func(name string) (string, error) {
			if name == npmCommand {
				return "/usr/local/bin/npm", nil
			}
			return "", errors.New("missing postgres command")
		},
		run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("installed"), nil
		},
	}

	err := installer.EnsureInstalled(context.Background())
	if err == nil {
		t.Fatalf("EnsureInstalled() error = nil, want PATH verification error")
	}
}
