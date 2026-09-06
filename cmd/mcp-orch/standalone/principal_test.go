package standalone

import (
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestConfiguredPrincipalIssuerIssuesPerSessionSnapshot(t *testing.T) {
	cfg := Config{PrincipalRef: "operator/test", ScopeRef: "workspace/test"}
	issuer, err := NewConfiguredPrincipalIssuer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	first := issueConfiguredPrincipalSnapshot(t, issuer, firstAt)
	second := issueConfiguredPrincipalSnapshot(t, issuer, firstAt.Add(time.Minute))
	assertConfiguredPrincipalSnapshots(t, cfg, firstAt, first, second)
}

func issueConfiguredPrincipalSnapshot(t *testing.T, issuer *ConfiguredPrincipalIssuer, at time.Time) common.CallerPrincipal {
	t.Helper()
	principal, err := issuer.IssueAt(at)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func assertConfiguredPrincipalSnapshots(t *testing.T, cfg Config, firstAt time.Time, first, second common.CallerPrincipal) {
	t.Helper()
	if first.PrincipalRef != cfg.PrincipalRef || first.ScopeRef != cfg.ScopeRef || first.Profile != ConfiguredPrincipalProfile {
		t.Fatalf("first principal=%#v, want configured refs/profile", first)
	}
	if first.AuthContext == "" || first.AuthContext != second.AuthContext {
		t.Fatalf("auth contexts=(%q,%q), want same nonempty server boot context", first.AuthContext, second.AuthContext)
	}
	if !second.ExpiresAt.Equal(first.ExpiresAt.Add(time.Minute)) || !first.ExpiresAt.Equal(firstAt.Add(configuredPrincipalTTL)) {
		t.Fatalf("expiries=(%v,%v), want per-session finite snapshots", first.ExpiresAt, second.ExpiresAt)
	}
}

func TestConfiguredPrincipalIssuerFailsClosed(t *testing.T) {
	for name, cfg := range map[string]Config{
		"missing principal": {ScopeRef: "workspace/test"},
		"missing scope":     {PrincipalRef: "operator/test"},
		"untrimmed":         {PrincipalRef: " operator/test", ScopeRef: "workspace/test"},
	} {
		t.Run(name, func(t *testing.T) {
			if issuer, err := NewConfiguredPrincipalIssuer(cfg); issuer != nil || err == nil {
				t.Fatalf("NewConfiguredPrincipalIssuer()=(%#v,%v), want fail-fast", issuer, err)
			}
		})
	}
	var issuer *ConfiguredPrincipalIssuer
	if err := issuer.Validate(); err == nil {
		t.Fatal("nil issuer Validate() succeeded")
	}
}
