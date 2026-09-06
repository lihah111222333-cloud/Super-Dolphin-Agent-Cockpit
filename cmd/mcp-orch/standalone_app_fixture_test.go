package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"go.uber.org/fx"
)

// newStandaloneTestApp 要求 fixture 显式身份，并以真实 SQL 来源装配生产图。
func newStandaloneTestApp(t *testing.T, cfg standalone.Config, credential, ready string, db *sql.DB, extra ...fx.Option) *fx.App {
	t.Helper()
	issuer, err := standalone.NewConfiguredPrincipalIssuer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	if err != nil {
		t.Fatal(err)
	}
	var app *fx.App
	t.Cleanup(func() {
		if app != nil {
			stopStandaloneTestApp(t, app)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := source.Close(ctx); err != nil {
			t.Errorf("close test operator source: %v", err)
		}
	})
	app = newStandaloneAppWithPrincipalIssuer(cfg, credential, ready, db, source, nil, extra...)
	return app
}
