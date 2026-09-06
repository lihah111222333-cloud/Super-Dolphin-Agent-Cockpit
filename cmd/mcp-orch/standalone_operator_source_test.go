package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type operatorTestIssuer struct {
	principal             common.CallerPrincipal
	validateErr, issueErr error
}

func (i *operatorTestIssuer) Validate() error                        { return i.validateErr }
func (i *operatorTestIssuer) Issue() (common.CallerPrincipal, error) { return i.principal, i.issueErr }

func operatorSourceFixture(t *testing.T) (*sql.DB, *operatorTestIssuer) {
	t.Helper()
	db, err := openStandaloneDatabase(t.Context(), standalone.Config{StateDir: t.TempDir(), WorkspaceRoot: t.TempDir()})
	operatorSourceRequireNoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, &operatorTestIssuer{principal: common.CallerPrincipal{PrincipalRef: "operator/test", ScopeRef: "workspace/test", Profile: standalone.ConfiguredPrincipalProfile, AuthContext: "boot/test", ExpiresAt: time.Now().UTC().Add(time.Hour)}}
}

func TestStandaloneOperatorSourceContract(t *testing.T) {
	t.Run("dependencies and validation before write", testOperatorSourceInvalidActivation)
	t.Run("snapshot fields and consumer one hot", testOperatorSourceFields)
	t.Run("revoke and replacement", testOperatorSourceReplacement)
	t.Run("issuer failure and drift", testOperatorSourceIssuerFailure)
	t.Run("startup failure revokes before database and lock close", testOperatorSourceStartupFailure)
	t.Run("lookup errors are not genesis", func(t *testing.T) {
		db, issuer := operatorSourceFixture(t)
		operatorSourceRequireNoError(t, db.Close())
		if _, err := activateStandaloneOperatorSource(t.Context(), db, issuer); err == nil {
			t.Fatal("closed database accepted as absent source")
		}
	})
}

func testOperatorSourceInvalidActivation(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	var typedNil *operatorTestIssuer
	for _, invalid := range []common.CallerPrincipalIssuer{nil, typedNil} {
		if _, err := activateStandaloneOperatorSource(t.Context(), db, invalid); err == nil {
			t.Fatal("nil issuer accepted")
		}
	}
	for _, mutate := range []func(*common.CallerPrincipal){func(p *common.CallerPrincipal) { p.Profile = "other" }, func(p *common.CallerPrincipal) { p.ExpiresAt = time.Now().Add(-time.Hour) }} {
		principal := issuer.principal
		mutate(&issuer.principal)
		if _, err := activateStandaloneOperatorSource(t.Context(), db, issuer); err == nil {
			t.Fatal("invalid principal activated")
		}
		issuer.principal = principal
	}
	store, err := operatorsource.NewStore(db)
	operatorSourceRequireNoError(t, err)
	if _, err := store.GetLatest(t.Context()); !errors.Is(err, operatorsource.ErrNotFound) {
		t.Fatalf("invalid activation wrote source: %v", err)
	}
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	operatorSourceRequireNoError(t, err)
	if source.source.Generation != 1 {
		t.Fatalf("genesis generation=%d", source.source.Generation)
	}
}

func testOperatorSourceFields(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	operatorSourceRequireNoError(t, err)
	got, err := source.Issue()
	if err != nil || !reflect.DeepEqual(got, issuer.principal) {
		t.Fatalf("snapshot lost fields: %+v %v", got, err)
	}
	operatorSourceRequireNoError(t, source.Validate())
	if _, err := source.RequireCurrent(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	// 生产字段由反射枚举；每项消费者登记必须执行真实 one-hot 拒绝测试。
	mutations := map[string]func(*common.CallerPrincipal){
		"PrincipalRef": func(p *common.CallerPrincipal) { p.PrincipalRef = "operator/other" },
		"ScopeRef":     func(p *common.CallerPrincipal) { p.ScopeRef = "workspace/other" },
		"Profile":      func(p *common.CallerPrincipal) { p.Profile = "other" },
		"AuthContext":  func(p *common.CallerPrincipal) { p.AuthContext = "boot/other" },
		"ExpiresAt":    func(p *common.CallerPrincipal) { p.ExpiresAt = time.Now().Add(-time.Hour) },
	}
	typ := reflect.TypeOf(got)
	if len(mutations) != typ.NumField() {
		t.Fatal("caller producer/consumer field coverage drift")
	}
	for index := 0; index < typ.NumField(); index++ {
		name := typ.Field(index).Name
		mutate, ok := mutations[name]
		if !ok {
			t.Fatalf("uncovered caller field %s", name)
		}
		t.Run(name, func(t *testing.T) {
			invalid := got
			mutate(&invalid)
			assertOperatorCallerOneHot(t, got, invalid)
			if _, err := source.RequireCurrent(t.Context(), invalid); err == nil {
				t.Fatalf("consumer ignored %s", name)
			}
			issuer.principal = invalid
			if _, err := source.Issue(); err == nil {
				t.Fatalf("issuer ignored %s", name)
			}
			if err := source.Validate(); err == nil {
				t.Fatalf("validator ignored %s", name)
			}
			issuer.principal = got
		})
	}
}

func testOperatorSourceReplacement(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	old, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	operatorSourceRequireNoError(t, err)
	issuer.principal.AuthContext = "boot/next"
	issuer.principal.PrincipalRef = "operator/next"
	issuer.principal.ScopeRef = "workspace/next"
	next, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	operatorSourceRequireNoError(t, err)
	if next.source.Generation != old.source.Generation+1 {
		t.Fatal("boot did not advance generation")
	}
	oldPrincipal := issuer.principal
	oldPrincipal.AuthContext = old.source.AuthContext
	oldPrincipal.PrincipalRef = old.source.PrincipalRef
	oldPrincipal.ScopeRef = old.source.ScopeRef
	if _, err := old.RequireCurrent(t.Context(), oldPrincipal); err == nil {
		t.Fatal("old boot remains current")
	}
	operatorSourceRequireNoError(t, old.Close(t.Context()))
	if _, err := next.Issue(); err != nil {
		t.Fatalf("old close revoked next: %v", err)
	}
	operatorSourceRequireNoError(t, next.Close(t.Context()))
	if err := next.Close(t.Context()); err != nil {
		t.Fatalf("close not idempotent: %v", err)
	}
	if _, err := next.Issue(); err == nil {
		t.Fatal("revoked issuer accepted")
	}
	if err := next.Validate(); err == nil {
		t.Fatal("revoked validator accepted")
	}
	if _, err := next.RequireCurrent(t.Context(), issuer.principal); err == nil {
		t.Fatal("revoked caller accepted")
	}
}

func testOperatorSourceIssuerFailure(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	operatorSourceRequireNoError(t, err)
	sentinel := errors.New("issuer failure")
	issuer.issueErr = sentinel
	if _, err := source.Issue(); !errors.Is(err, sentinel) {
		t.Fatalf("issue swallowed failure: %v", err)
	}
	issuer.issueErr = nil
	issuer.validateErr = sentinel
	if err := source.Validate(); !errors.Is(err, sentinel) {
		t.Fatalf("validate swallowed failure: %v", err)
	}
	issuer.validateErr = nil
	issuer.principal.AuthContext = "boot/drift"
	if _, err := source.Issue(); err == nil {
		t.Fatal("issuer authcontext drift accepted")
	}
}

// testOperatorSourceStartupFailure 真实入口在ready路径失败后必须撤源并释放数据库及实例锁。
func testOperatorSourceStartupFailure(t *testing.T) {
	state, options := corruptStandaloneFixture(t)
	operatorSourceRequireNoError(t, os.Remove(filepath.Join(state, "orch.sqlite")))
	// 将ready父目录设为普通文件，确保错误发生在数据库迁移及source激活之后。
	blocker := filepath.Join(state, "ready-parent-file")
	operatorSourceRequireNoError(t, os.WriteFile(blocker, []byte("not a directory"), 0600))
	options.readyPath = filepath.Join(blocker, "ready.json")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := runServe(ctx, options); err == nil {
		t.Fatal("invalid readiness path accepted")
	}
	cfg, err := standalone.LoadConfig(options.configPath)
	operatorSourceRequireNoError(t, err)
	lock, err := standalone.AcquireInstance(state)
	if err != nil {
		t.Fatalf("failed startup leaked lock: %v", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	db, err := openStandaloneDatabaseWithLock(t.Context(), cfg, lock)
	operatorSourceRequireNoError(t, err)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	store, err := operatorsource.NewStore(db)
	operatorSourceRequireNoError(t, err)
	source, err := store.GetLatest(t.Context())
	if err != nil {
		t.Fatalf("startup did not reach source activation: %v", err)
	}
	if source.State != "revoked" || source.Generation != 1 {
		t.Fatalf("failed startup left source %+v", source)
	}
}

// operatorSourceRequireNoError 聚合测试的设置和有效路径必须显式成功。
func operatorSourceRequireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// assertOperatorCallerOneHot 验证每个负测只改变一个真实生产字段。
func assertOperatorCallerOneHot(t *testing.T, got, invalid common.CallerPrincipal) {
	t.Helper()
	typ := reflect.TypeOf(got)
	differences := 0
	for i := 0; i < typ.NumField(); i++ {
		if !reflect.DeepEqual(reflect.ValueOf(got).Field(i).Interface(), reflect.ValueOf(invalid).Field(i).Interface()) {
			differences++
		}
	}
	if differences != 1 {
		t.Fatal("mutation is not one-hot")
	}

}
