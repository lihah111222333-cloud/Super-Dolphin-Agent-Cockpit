package common

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCallerPrincipalConstructorAndContextSnapshot(t *testing.T) {
	now := time.Now().UTC()
	principal, err := NewCallerPrincipal("subject/alice", "run/one", "standalone_http_local_v1", "auth/session-1", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewCallerPrincipal() error = %v", err)
	}
	if err := principal.ValidateAt(now); err != nil {
		t.Fatalf("ValidateAt() error = %v", err)
	}

	ctx, err := BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatalf("BindCallerPrincipal() error = %v", err)
	}
	principal.PrincipalRef = "forged"
	got, ok := CallerPrincipalFromContext(ctx)
	if !ok {
		t.Fatal("CallerPrincipalFromContext() missing bound principal")
	}
	if got.PrincipalRef != "subject/alice" {
		t.Fatalf("context principal ref = %q, want immutable snapshot", got.PrincipalRef)
	}
}

func TestCallerPrincipalValidationRejectsNonCanonicalOrExpiredValues(t *testing.T) {
	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	valid := CallerPrincipal{
		PrincipalRef: "subject/alice",
		ScopeRef:     "run/one",
		Profile:      "profile/v1",
		AuthContext:  "auth/session-1",
		ExpiresAt:    now.Add(time.Minute),
	}
	cases := map[string]CallerPrincipal{
		"missing principal": func() CallerPrincipal { p := valid; p.PrincipalRef = ""; return p }(),
		"trimmed scope":     func() CallerPrincipal { p := valid; p.ScopeRef = "run/one "; return p }(),
		"nul profile":       func() CallerPrincipal { p := valid; p.Profile = "profile\x00v1"; return p }(),
		"oversized auth": func() CallerPrincipal {
			p := valid
			p.AuthContext = strings.Repeat("a", callerPrincipalTextLimit+1)
			return p
		}(),
		"non-UTC expiry": func() CallerPrincipal { p := valid; p.ExpiresAt = now.In(time.FixedZone("test", 3600)); return p }(),
		"expired":        func() CallerPrincipal { p := valid; p.ExpiresAt = now; return p }(),
	}
	for name, principal := range cases {
		t.Run(name, func(t *testing.T) {
			if err := principal.ValidateAt(now); !errors.Is(err, ErrInvalidCallerPrincipal) {
				t.Fatalf("ValidateAt() error = %v, want ErrInvalidCallerPrincipal", err)
			}
		})
	}
}

func TestCallerPrincipalContextHelpersFailClosed(t *testing.T) {
	if _, ok := CallerPrincipalFromContext(context.Background()); ok {
		t.Fatal("CallerPrincipalFromContext() ok = true for missing principal")
	}
	if _, err := RequireCallerPrincipal(context.Background()); !errors.Is(err, ErrMissingCallerPrincipal) {
		t.Fatalf("RequireCallerPrincipal() error = %v, want missing sentinel", err)
	}
	if _, err := WithCallerPrincipal(context.Background(), CallerPrincipal{}); !errors.Is(err, ErrInvalidCallerPrincipal) {
		t.Fatalf("WithCallerPrincipal() error=%v, want ErrInvalidCallerPrincipal", err)
	}
	var nilContext context.Context
	if ctx, err := BindCallerPrincipal(nilContext, validCallerPrincipal(time.Now().UTC().Add(time.Minute))); ctx != nil || !errors.Is(err, ErrInvalidCallerPrincipal) {
		t.Fatalf("BindCallerPrincipal(nil)=(%v,%v), want nil context and invalid sentinel", ctx, err)
	}
}

func validCallerPrincipal(expiresAt time.Time) CallerPrincipal {
	return CallerPrincipal{PrincipalRef: "subject/alice", ScopeRef: "run/one", Profile: "profile/v1", AuthContext: "auth/session-1", ExpiresAt: expiresAt}
}

func TestCallerPrincipalFromContextRejectsExpiry(t *testing.T) {
	p := CallerPrincipal{
		PrincipalRef: "subject/alice",
		ScopeRef:     "run/one",
		Profile:      "profile/v1",
		AuthContext:  "auth/session-1",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute),
	}
	// 直接构造过期快照只用于验证读取 guard；正常写入由 BindCallerPrincipal 拒绝。
	ctx := context.WithValue(context.Background(), callerPrincipalContextKey{}, p)
	if _, ok := CallerPrincipalFromContext(ctx); ok {
		t.Fatal("CallerPrincipalFromContext() accepted expired principal")
	}
	if _, err := RequireCallerPrincipal(ctx); !errors.Is(err, ErrInvalidCallerPrincipal) {
		t.Fatalf("RequireCallerPrincipal() error = %v, want invalid sentinel", err)
	}
}
