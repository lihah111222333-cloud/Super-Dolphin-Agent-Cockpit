package main

import (
	"errors"
	"strings"
	"testing"
)

func TestConfigureDefaultDesktopDatabaseURLSetsLocalPostgresDefault(t *testing.T) {
	env := map[string]string{
		"SUPER_DOLPHIN_PROCESS_ROLE": "desktop",
	}
	var setKey, setValue string

	err := configureDefaultDesktopDatabaseURL(
		func(key string) string { return env[key] },
		func(key, value string) error {
			setKey = key
			setValue = value
			env[key] = value
			return nil
		},
	)
	if err != nil {
		t.Fatalf("configureDefaultDesktopDatabaseURL() error = %v", err)
	}
	if setKey != "DATABASE_URL" {
		t.Fatalf("set key = %q, want DATABASE_URL", setKey)
	}
	want := "postgres://postgres:agent@127.0.0.1:5432/super_dolphin?sslmode=disable"
	if setValue != want {
		t.Fatalf("DATABASE_URL = %q, want %q", setValue, want)
	}
}

func TestConfigureDefaultDesktopDatabaseURLPreservesExplicitDatabaseURL(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":                    "postgres://custom@127.0.0.1:5433/custom?sslmode=disable",
		"SUPER_DOLPHIN_PROCESS_ROLE":      "desktop",
		"SUPER_DOLPHIN_RUNTIME_MODE":      "dev",
		"SUPER_DOLPHIN_EMBEDDED_POSTGRES": "true",
	}

	err := configureDefaultDesktopDatabaseURL(
		func(key string) string { return env[key] },
		func(key, value string) error {
			t.Fatalf("unexpected Setenv(%q, %q)", key, value)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("configureDefaultDesktopDatabaseURL() error = %v", err)
	}
}

func TestConfigureDefaultDesktopDatabaseURLPreservesCompatDatabaseURL(t *testing.T) {
	env := map[string]string{
		"POSTGRES_CONNECTION_STRING": "postgres://compat@127.0.0.1:5433/compat?sslmode=disable",
		"SUPER_DOLPHIN_PROCESS_ROLE": "desktop",
	}

	err := configureDefaultDesktopDatabaseURL(
		func(key string) string { return env[key] },
		func(key, value string) error {
			t.Fatalf("unexpected Setenv(%q, %q)", key, value)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("configureDefaultDesktopDatabaseURL() error = %v", err)
	}
}

func TestConfigureDefaultDesktopDatabaseURLSkipsPackagedRuntime(t *testing.T) {
	env := map[string]string{
		"SUPER_DOLPHIN_PROCESS_ROLE": "desktop",
		"SUPER_DOLPHIN_RUNTIME_MODE": "packaged",
	}

	err := configureDefaultDesktopDatabaseURL(
		func(key string) string { return env[key] },
		func(key, value string) error {
			t.Fatalf("unexpected Setenv(%q, %q)", key, value)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("configureDefaultDesktopDatabaseURL() error = %v", err)
	}
}

func TestConfigureDefaultDesktopDatabaseURLSkipsEmbeddedPostgresRequest(t *testing.T) {
	env := map[string]string{
		"SUPER_DOLPHIN_PROCESS_ROLE":      "desktop",
		"SUPER_DOLPHIN_EMBEDDED_POSTGRES": "true",
	}

	err := configureDefaultDesktopDatabaseURL(
		func(key string) string { return env[key] },
		func(key, value string) error {
			t.Fatalf("unexpected Setenv(%q, %q)", key, value)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("configureDefaultDesktopDatabaseURL() error = %v", err)
	}
}

func TestConfigureDefaultDesktopDatabaseURLReturnsSetenvError(t *testing.T) {
	injected := errors.New("injected setenv failure")
	env := map[string]string{
		"SUPER_DOLPHIN_PROCESS_ROLE": "desktop",
	}

	err := configureDefaultDesktopDatabaseURL(
		func(key string) string { return env[key] },
		func(key, value string) error { return injected },
	)
	if err == nil {
		t.Fatal("configureDefaultDesktopDatabaseURL() error = nil, want setenv failure")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") || !errors.Is(err, injected) {
		t.Fatalf("configureDefaultDesktopDatabaseURL() error = %v, want DATABASE_URL wrapping injected error", err)
	}
}
