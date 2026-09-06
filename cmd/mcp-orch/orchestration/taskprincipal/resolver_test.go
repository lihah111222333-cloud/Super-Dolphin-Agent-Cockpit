package taskprincipal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	_ "modernc.org/sqlite"
)

func TestResolverRequireCurrentReturnsVerifiedSnapshotsInFixedReadOrder(t *testing.T) {
	fixture := newResolverFixture(t)
	got, err := fixture.resolver.RequireCurrent(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, TaskPrincipal{
		Principal: fixture.principal, Attribution: fixture.attribution, Binding: fixture.reader.binding,
	}) {
		t.Fatalf("RequireCurrent() = %#v, want verified snapshots", got)
	}
	assertReaderCalls(t, fixture.reader.calls,
		"reservation:"+fixture.attribution.Source.ReservationID,
		"incarnation:"+fixture.attribution.Source.IncarnationID,
		"current:"+fixture.attribution.Source.AgentID,
	)
}

func TestResolverRejectsMissingOrExpiredTransportIdentityWithoutReads(t *testing.T) {
	fixture := newResolverFixture(t)
	principalOnly, err := common.BindCallerPrincipal(context.Background(), fixture.principal)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "missing principal", ctx: context.Background(), want: ErrTaskPrincipalUnavailable},
		{name: "operator principal without attribution", ctx: principalOnly, want: ErrTaskPrincipalUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.reader.calls = nil
			_, resolveErr := fixture.resolver.RequireCurrent(tc.ctx)
			assertResolverError(t, resolveErr, tc.want)
			assertReaderCalls(t, fixture.reader.calls)
		})
	}

	expiring, err := common.NewCallerPrincipal(
		fixture.principal.PrincipalRef,
		fixture.principal.ScopeRef,
		fixture.principal.Profile,
		fixture.principal.AuthContext,
		time.Now().UTC().Add(100*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	expiredCtx, err := common.BindCallerPrincipal(context.Background(), expiring)
	if err != nil {
		t.Fatal(err)
	}
	expiredCtx, err = common.BindCallerAttribution(expiredCtx, fixture.attribution)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	fixture.reader.calls = nil
	_, err = fixture.resolver.RequireCurrent(expiredCtx)
	assertResolverError(t, err, ErrTaskPrincipalInvalid)
	assertReaderCalls(t, fixture.reader.calls)
}

func TestResolverRejectsReservationPrincipalAndScopeMismatchBeforeLaterReads(t *testing.T) {
	for _, mutate := range []struct {
		name string
		fn   func(*identity.Reservation)
	}{
		{name: "state", fn: func(value *identity.Reservation) { value.State = "activation_pending" }},
		{name: "principal", fn: func(value *identity.Reservation) { value.CallerPrincipalRef += "-other" }},
		{name: "scope", fn: func(value *identity.Reservation) { value.ScopeRef += "-other" }},
		{name: "agent", fn: func(value *identity.Reservation) { value.AgentID += "-other" }},
		{name: "reservation", fn: func(value *identity.Reservation) { value.ReservationID += "-other" }},
		{name: "generation", fn: func(value *identity.Reservation) { value.ReservationGeneration++ }},
		{name: "source fence", fn: func(value *identity.Reservation) { value.SourceFenceRef += "-other" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			mutate.fn(&fixture.reader.reservation)
			_, err := fixture.resolver.RequireCurrent(fixture.ctx)
			assertResolverError(t, err, ErrTaskPrincipalFence)
			assertReaderCalls(t, fixture.reader.calls, "reservation:"+fixture.attribution.Source.ReservationID)
		})
	}
}

func TestResolverRejectsEveryStaleIncarnationFieldBeforeCurrentRead(t *testing.T) {
	for _, mutate := range []struct {
		name string
		fn   func(*identity.Incarnation)
	}{
		{name: "incarnation", fn: func(value *identity.Incarnation) { value.IncarnationID += "-stale" }},
		{name: "agent", fn: func(value *identity.Incarnation) { value.AgentID += "-stale" }},
		{name: "reservation", fn: func(value *identity.Incarnation) { value.ReservationID += "-stale" }},
		{name: "generation", fn: func(value *identity.Incarnation) { value.IncarnationGeneration++ }},
		{name: "source fence", fn: func(value *identity.Incarnation) { value.SourceFenceRef += "-stale" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			mutate.fn(&fixture.reader.incarnation)
			_, err := fixture.resolver.RequireCurrent(fixture.ctx)
			assertResolverError(t, err, ErrTaskPrincipalFence)
			assertReaderCalls(t, fixture.reader.calls,
				"reservation:"+fixture.attribution.Source.ReservationID,
				"incarnation:"+fixture.attribution.Source.IncarnationID,
			)
		})
	}
}

func TestResolverRejectsEveryStaleCurrentExecutionFenceField(t *testing.T) {
	typ := reflect.TypeFor[common.ExecutionFence]()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			mutateCurrentBindingField(t, &fixture.reader.binding, field.Name)
			_, err := fixture.resolver.RequireCurrent(fixture.ctx)
			assertResolverError(t, err, ErrTaskPrincipalFence)
			assertReaderCalls(t, fixture.reader.calls,
				"reservation:"+fixture.attribution.Source.ReservationID,
				"incarnation:"+fixture.attribution.Source.IncarnationID,
				"current:"+fixture.attribution.Source.AgentID,
			)
		})
	}
	fixture := newResolverFixture(t)
	fixture.reader.binding.State = "revoked"
	_, err := fixture.resolver.RequireCurrent(fixture.ctx)
	assertResolverError(t, err, ErrTaskPrincipalFence)
}

func TestResolverAcceptsRecoverReservationAndRejectsCurrentRollover(t *testing.T) {
	fixture := newResolverFixture(t)
	fixture.reader.reservation.SourceKind = "recover"
	if _, err := fixture.resolver.RequireCurrent(fixture.ctx); err != nil {
		t.Fatalf("RequireCurrent(recover source) error = %v", err)
	}

	fixture = newResolverFixture(t)
	fixture.reader.binding.BindingVersion++
	_, err := fixture.resolver.RequireCurrent(fixture.ctx)
	assertResolverError(t, err, ErrTaskPrincipalFence)
}

func TestResolverRequireCurrentTxUsesCallerTransactionAndRejectsBareDB(t *testing.T) {
	fixture := newResolverFixture(t)
	db, err := sql.Open("sqlite", "file:taskprincipal-tx?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fixture.resolver.RequireCurrentTx(fixture.ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("RequireCurrentTx() error = %v", err)
	}
	if !reflect.DeepEqual(got, TaskPrincipal{Principal: fixture.principal, Attribution: fixture.attribution, Binding: fixture.reader.binding}) {
		_ = tx.Rollback()
		t.Fatalf("RequireCurrentTx() = %#v, want verified snapshots", got)
	}
	if len(fixture.reader.txCalls) != 1 || fixture.reader.txCalls[0] != tx {
		_ = tx.Rollback()
		t.Fatalf("transaction calls = %#v, want caller transaction", fixture.reader.txCalls)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.reader.txCalls = nil
	if _, err := fixture.resolver.RequireCurrentTx(fixture.ctx, db); err == nil {
		t.Fatal("RequireCurrentTx() unexpectedly accepted bare database")
	}
	if len(fixture.reader.txCalls) != 0 {
		t.Fatalf("bare database reached reader: %#v", fixture.reader.txCalls)
	}
}

func TestResolverCollapsesIdentityReadFailuresWithoutLeakingSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*resolverReaderStub)
		wantCalls int
	}{
		{name: "reservation not found", configure: func(reader *resolverReaderStub) {
			reader.reservationErr = fmt.Errorf("secret-reservation: %w", identity.ErrIdentityNotFound)
		}, wantCalls: 1},
		{name: "incarnation unavailable", configure: func(reader *resolverReaderStub) {
			reader.incarnationErr = errors.New("secret-incarnation backend unavailable")
		}, wantCalls: 2},
		{name: "current unavailable after rollover", configure: func(reader *resolverReaderStub) {
			reader.bindingErr = fmt.Errorf("secret-current: %w", identity.ErrIdentityNotFound)
		}, wantCalls: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			tc.configure(fixture.reader)
			_, err := fixture.resolver.RequireCurrent(fixture.ctx)
			assertResolverError(t, err, ErrTaskPrincipalUnavailable)
			if err.Error() != ErrTaskPrincipalUnavailable.Error() || strings.Contains(err.Error(), "secret-") {
				t.Fatalf("RequireCurrent() leaked owner/source detail: %q", err)
			}
			if len(fixture.reader.calls) != tc.wantCalls {
				t.Fatalf("reader calls = %v, want %d ordered calls", fixture.reader.calls, tc.wantCalls)
			}
		})
	}
}

func TestNewAndNilResolverRejectNilReaders(t *testing.T) {
	if resolver, err := New(nil); resolver != nil || !errors.Is(err, ErrTaskPrincipalInvalid) {
		t.Fatalf("New(nil) = (%#v, %v), want ErrTaskPrincipalInvalid", resolver, err)
	}
	var typedNil *resolverReaderStub
	if resolver, err := New(typedNil); resolver != nil || !errors.Is(err, ErrTaskPrincipalInvalid) {
		t.Fatalf("New(typed nil) = (%#v, %v), want ErrTaskPrincipalInvalid", resolver, err)
	}
	var resolver *Resolver
	if _, err := resolver.RequireCurrent(context.Background()); !errors.Is(err, ErrTaskPrincipalInvalid) {
		t.Fatalf("nil Resolver.RequireCurrent() error = %v, want ErrTaskPrincipalInvalid", err)
	}
}

func TestExecutionFenceConsumerRegistryHasNoMissingOrStaleFields(t *testing.T) {
	producer := reflect.TypeFor[common.ExecutionFence]()
	binding := reflect.TypeFor[identity.CurrentBinding]()
	registry := map[string]string{
		"AgentID":               "AgentID",
		"ReservationID":         "ReservationID",
		"ReservationGeneration": "ReservationGeneration",
		"IncarnationID":         "IncarnationID",
		"IncarnationGeneration": "IncarnationGeneration",
		"BindingVersion":        "BindingVersion",
		"BindingDigest":         "BindingDigest",
		"SourceFenceRef":        "SourceFenceRef",
	}
	for index := 0; index < producer.NumField(); index++ {
		field := producer.Field(index)
		destination, ok := registry[field.Name]
		if !ok {
			t.Fatalf("ExecutionFence.%s has no CurrentBinding consumer", field.Name)
		}
		consumer, ok := binding.FieldByName(destination)
		if !ok || consumer.Type != field.Type {
			t.Fatalf("ExecutionFence.%s consumer %q is missing or has type drift", field.Name, destination)
		}
	}
	for source, destination := range registry {
		if _, ok := producer.FieldByName(source); !ok {
			t.Fatalf("stale ExecutionFence registry source %q -> %q", source, destination)
		}
		if _, ok := binding.FieldByName(destination); !ok {
			t.Fatalf("stale CurrentBinding registry destination %q", destination)
		}
	}
}

func TestTaskPrincipalPublicResultCannotAcceptMetadataOrTargetSelection(t *testing.T) {
	typ := reflect.TypeFor[TaskPrincipal]()
	want := map[string]reflect.Type{
		"Principal":   reflect.TypeFor[common.CallerPrincipal](),
		"Attribution": reflect.TypeFor[common.CallerAttribution](),
		"Binding":     reflect.TypeFor[identity.CurrentBinding](),
	}
	if typ.NumField() != len(want) {
		t.Fatalf("TaskPrincipal fields = %d, want only verified snapshots", typ.NumField())
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if want[field.Name] != field.Type {
			t.Fatalf("TaskPrincipal exposes unregistered field %s %s", field.Name, field.Type)
		}
	}
}

type resolverFixture struct {
	ctx         context.Context
	principal   common.CallerPrincipal
	attribution common.CallerAttribution
	reader      *resolverReaderStub
	resolver    *Resolver
}

func newResolverFixture(t *testing.T) resolverFixture {
	t.Helper()
	source := common.ExecutionFence{
		AgentID: "agent-a", ReservationID: "reservation-a", ReservationGeneration: 3,
		IncarnationID: "incarnation-a", IncarnationGeneration: 4,
		BindingVersion: 5, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "source-fence-a",
	}
	principal, err := common.NewCallerPrincipal(
		"principal-a", "scope-a", "provider-bridge", "auth-context-a", time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := common.NewCallerAttribution("agent-a", source, common.IssuerBinding{
		Kind: common.CallerAttributionIssuerProviderBridge, BindingRef: "issuer-a",
		Generation: 2, Digest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := common.BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = common.BindCallerAttribution(ctx, attribution)
	if err != nil {
		t.Fatal(err)
	}
	reader := &resolverReaderStub{
		reservation: identity.Reservation{
			ReservationID: source.ReservationID, AgentID: source.AgentID, SourceKind: "first_launch",
			CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef,
			SourceFenceRef: source.SourceFenceRef, ReservationGeneration: source.ReservationGeneration, State: "activated",
		},
		incarnation: identity.Incarnation{
			IncarnationID: source.IncarnationID, ReservationID: source.ReservationID, AgentID: source.AgentID,
			SourceFenceRef: source.SourceFenceRef, IncarnationGeneration: source.IncarnationGeneration,
		},
		binding: identity.CurrentBinding{
			AgentID: source.AgentID, BindingVersion: source.BindingVersion,
			ReservationID: source.ReservationID, IncarnationID: source.IncarnationID,
			ReservationGeneration: source.ReservationGeneration, IncarnationGeneration: source.IncarnationGeneration,
			SourceFenceRef: source.SourceFenceRef, BindingDigest: source.BindingDigest, State: "active",
		},
	}
	resolver, err := New(reader)
	if err != nil {
		t.Fatal(err)
	}
	return resolverFixture{ctx: ctx, principal: principal, attribution: attribution, reader: reader, resolver: resolver}
}

type resolverReaderStub struct {
	reservation    identity.Reservation
	incarnation    identity.Incarnation
	binding        identity.CurrentBinding
	reservationErr error
	incarnationErr error
	bindingErr     error
	calls          []string
	txCalls        []storedb.DBTX
}

func (r *resolverReaderStub) Get(_ context.Context, reservationID string) (identity.Reservation, error) {
	r.calls = append(r.calls, "reservation:"+reservationID)
	return r.reservation, r.reservationErr
}

func (r *resolverReaderStub) GetIncarnation(_ context.Context, incarnationID string) (identity.Incarnation, error) {
	r.calls = append(r.calls, "incarnation:"+incarnationID)
	return r.incarnation, r.incarnationErr
}

func (r *resolverReaderStub) GetCurrent(_ context.Context, agentID string) (identity.CurrentBinding, error) {
	r.calls = append(r.calls, "current:"+agentID)
	return r.binding, r.bindingErr
}

func (r *resolverReaderStub) GetActiveSnapshotTx(_ context.Context, dbtx storedb.DBTX, _ string) (identity.ActiveSnapshot, error) {
	r.txCalls = append(r.txCalls, dbtx)
	return identity.ActiveSnapshot{Reservation: r.reservation, Incarnation: r.incarnation, Binding: r.binding}, nil
}

func mutateCurrentBindingField(t *testing.T, binding *identity.CurrentBinding, fieldName string) {
	t.Helper()
	value := reflect.ValueOf(binding).Elem().FieldByName(fieldName)
	if !value.IsValid() {
		t.Fatalf("CurrentBinding.%s is missing", fieldName)
	}
	switch value.Kind() {
	case reflect.String:
		value.SetString(value.String() + "-stale")
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		t.Fatalf("CurrentBinding.%s has unsupported kind %s", fieldName, value.Kind())
	}
}

func assertResolverError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func assertReaderCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reader calls = %v, want %v", got, want)
	}
}

var _ IdentityChainReader = (*resolverReaderStub)(nil)
