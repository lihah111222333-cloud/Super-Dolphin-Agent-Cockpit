package taskcredential

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestTaskSessionIdentityFactoryMapsEveryExecutionFenceField(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	result, err := fixture.factory.Build(fixture.principal, fixture.binding, fixture.issuer)
	if err != nil {
		t.Fatal(err)
	}
	if result.Principal != fixture.principal {
		t.Fatalf("Build() principal = %#v, want exact input", result.Principal)
	}
	if result.Attribution.TaskID != fixture.binding.AgentID {
		t.Fatalf("Build() TaskID = %q, want binding AgentID", result.Attribution.TaskID)
	}
	assertExecutionFenceMappedFieldByField(t, fixture.binding, result.Attribution.Source)
	if result.Attribution.Issuer != fixture.issuer {
		t.Fatalf("Build() issuer = %#v, want exact input", result.Attribution.Issuer)
	}
}

func TestTaskSessionIdentityFactoryRejectsNonActiveOrUnsupportedBinding(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	cases := []struct {
		name   string
		mutate func(*identity.CurrentBinding)
	}{
		{name: "zero schema", mutate: func(binding *identity.CurrentBinding) { binding.SchemaVersion = 0 }},
		{name: "future schema", mutate: func(binding *identity.CurrentBinding) { binding.SchemaVersion = 2 }},
		{name: "revoked", mutate: func(binding *identity.CurrentBinding) { binding.State = "revoked" }},
		{name: "empty state", mutate: func(binding *identity.CurrentBinding) { binding.State = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binding := fixture.binding
			tc.mutate(&binding)
			assertFactoryRejects(t, fixture, fixture.principal, binding, fixture.issuer)
		})
	}
}

func TestTaskSessionIdentityFactoryRejectsEveryMissingFenceField(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	fenceType := reflect.TypeFor[common.ExecutionFence]()
	for index := range fenceType.NumField() {
		field := fenceType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			binding := fixture.binding
			bindingField := reflect.ValueOf(&binding).Elem().FieldByName(field.Name)
			if !bindingField.IsValid() || !bindingField.CanSet() || bindingField.Type() != field.Type {
				t.Fatalf("CurrentBinding.%s cannot supply ExecutionFence.%s", field.Name, field.Name)
			}
			bindingField.Set(reflect.Zero(bindingField.Type()))
			assertFactoryRejects(t, fixture, fixture.principal, binding, fixture.issuer)
		})
	}
}

func TestTaskSessionIdentityFactoryRejectsExpiredPrincipalAndInvalidIssuer(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	expired := fixture.principal
	expired.ExpiresAt = time.Now().UTC().Add(-time.Second)
	assertFactoryRejects(t, fixture, expired, fixture.binding, fixture.issuer)

	invalidIssuer := fixture.issuer
	invalidIssuer.Generation = 0
	assertFactoryRejects(t, fixture, fixture.principal, fixture.binding, invalidIssuer)
}

func TestTaskSessionIdentityFactoryDoesNotInferTaskFromPrincipalThreadOrCWD(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	principal := fixture.principal
	principal.PrincipalRef = "agent/decoy"
	principal.ScopeRef = "cwd/decoy"
	principal.AuthContext = "thread/decoy"
	result, err := fixture.factory.Build(principal, fixture.binding, fixture.issuer)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attribution.TaskID != fixture.binding.AgentID || result.Attribution.TaskID == "decoy" {
		t.Fatalf("Build() inferred task from non-binding input: %#v", result.Attribution)
	}
}

func TestNewResolverCurrentIdentityVerifierRejectsNilAndTypedNilResolver(t *testing.T) {
	var typedNil *currentResolverStub
	for _, resolver := range []CurrentTaskPrincipalResolver{nil, typedNil} {
		if _, err := NewResolverCurrentIdentityVerifier(resolver); !errors.Is(err, ErrRegistryInvalid) {
			t.Fatalf("NewResolverCurrentIdentityVerifier(%T) error = %v", resolver, err)
		}
	}
}

func TestResolverCurrentIdentityVerifierAcceptsExactPairAndBinding(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	resolver := &currentResolverStub{result: fixture.taskPrincipal()}
	verifier := newCurrentVerifier(t, resolver)
	if err := verifier.Verify(context.Background(), fixture.sessionIdentity); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || !resolver.observed.Equal(fixture.sessionIdentity) {
		t.Fatalf("resolver observed calls=%d identity=%#v", resolver.calls, resolver.observed)
	}
}

func TestResolverCurrentIdentityVerifierUsesValueIsolatedContext(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	other := fixture
	other.binding.AgentID = "task-other"
	other.binding.ReservationID = "reservation-other"
	other.binding.IncarnationID = "incarnation-other"
	other.binding.SourceFenceRef = "fence-other"
	other.sessionIdentity = mustBuildIdentity(t, other.factory, other.principal, other.binding, other.issuer)
	ctx, err := common.BindTaskSessionIdentity(context.Background(), other.sessionIdentity)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &currentResolverStub{result: fixture.taskPrincipal()}
	verifier := newCurrentVerifier(t, resolver)
	if err := verifier.Verify(ctx, fixture.sessionIdentity); err != nil {
		t.Fatalf("Verify inherited caller context values: %v", err)
	}
	if !resolver.observed.Equal(fixture.sessionIdentity) {
		t.Fatal("resolver did not receive the explicitly verified identity")
	}
}

func TestResolverCurrentIdentityVerifierRejectsExpiredOrResolverFailureUniformly(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	t.Run("expired", func(t *testing.T) {
		expired := fixture.sessionIdentity
		expired.Principal.ExpiresAt = time.Now().UTC().Add(-time.Second)
		resolver := &currentResolverStub{result: fixture.taskPrincipal()}
		verifier := newCurrentVerifier(t, resolver)
		assertVerifierUnavailable(t, verifier, context.Background(), expired)
		if resolver.calls != 0 {
			t.Fatalf("expired identity reached resolver %d times", resolver.calls)
		}
	})
	t.Run("resolver error", func(t *testing.T) {
		resolver := &currentResolverStub{err: errors.New("durable current failed for task-secret")}
		verifier := newCurrentVerifier(t, resolver)
		assertVerifierUnavailable(t, verifier, context.Background(), fixture.sessionIdentity)
	})
}

func TestResolverCurrentIdentityVerifierRejectsRevokedAndStaleBinding(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	t.Run("revoked", func(t *testing.T) {
		result := fixture.taskPrincipal()
		result.Binding.State = "revoked"
		assertResolverResultUnavailable(t, fixture, result)
	})

	fenceType := reflect.TypeFor[common.ExecutionFence]()
	for index := range fenceType.NumField() {
		fieldName := fenceType.Field(index).Name
		t.Run("stale "+fieldName, func(t *testing.T) {
			result := fixture.taskPrincipal()
			mutateCurrentBindingField(t, &result.Binding, fieldName)
			assertResolverResultUnavailable(t, fixture, result)
		})
	}
}

func TestResolverCurrentIdentityVerifierRejectsCurrentRolloverAndIssuerMismatch(t *testing.T) {
	fixture := newCurrentIdentityFixture(t)
	t.Run("current rollover", func(t *testing.T) {
		rolled := fixture.binding
		rolled.BindingVersion++
		rolled.BindingDigest = strings.Repeat("c", 64)
		rolledIdentity := mustBuildIdentity(t, fixture.factory, fixture.principal, rolled, fixture.issuer)
		result := taskprincipal.TaskPrincipal{
			Principal: rolledIdentity.Principal, Attribution: rolledIdentity.Attribution, Binding: rolled,
		}
		assertResolverResultUnavailable(t, fixture, result)
	})
	t.Run("issuer mismatch", func(t *testing.T) {
		issuer := fixture.issuer
		issuer.Generation++
		otherIdentity := mustBuildIdentity(t, fixture.factory, fixture.principal, fixture.binding, issuer)
		result := taskprincipal.TaskPrincipal{
			Principal: otherIdentity.Principal, Attribution: otherIdentity.Attribution, Binding: fixture.binding,
		}
		assertResolverResultUnavailable(t, fixture, result)
	})
}

type currentIdentityFixture struct {
	factory         TaskSessionIdentityFactory
	principal       common.CallerPrincipal
	binding         identity.CurrentBinding
	issuer          common.IssuerBinding
	sessionIdentity common.TaskSessionIdentity
}

func newCurrentIdentityFixture(t *testing.T) currentIdentityFixture {
	t.Helper()
	principal := common.CallerPrincipal{
		PrincipalRef: "principal/task-current", ScopeRef: "scope/current",
		Profile: "standalone_task_v1", AuthContext: "auth/current",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	binding := identity.CurrentBinding{
		AgentID: "task-current", BindingVersion: 7,
		ReservationID: "reservation-current", IncarnationID: "incarnation-current",
		IdentityKind: "managed_agent", ReservationGeneration: 5, IncarnationGeneration: 6,
		ExternalIdentityRef: "external-current", SourceFenceRef: "fence-current",
		ActiveProcessReceiptRef: "receipt-current", BindingDigest: strings.Repeat("a", 64),
		State: currentBindingStateActive, SchemaVersion: currentBindingSchemaVersion,
		UpdatedAt: "2035-01-02T03:04:05Z",
	}
	issuer := common.IssuerBinding{
		Kind: common.CallerAttributionIssuerManagedRuntime, BindingRef: "issuer/current",
		Generation: 3, Digest: strings.Repeat("b", 64),
	}
	factory := TaskSessionIdentityFactory{}
	sessionIdentity := mustBuildIdentity(t, factory, principal, binding, issuer)
	return currentIdentityFixture{
		factory: factory, principal: principal, binding: binding, issuer: issuer, sessionIdentity: sessionIdentity,
	}
}

func (f currentIdentityFixture) taskPrincipal() taskprincipal.TaskPrincipal {
	return taskprincipal.TaskPrincipal{
		Principal:   f.sessionIdentity.Principal,
		Attribution: f.sessionIdentity.Attribution,
		Binding:     f.binding,
	}
}

func mustBuildIdentity(
	t *testing.T,
	factory TaskSessionIdentityFactory,
	principal common.CallerPrincipal,
	binding identity.CurrentBinding,
	issuer common.IssuerBinding,
) common.TaskSessionIdentity {
	t.Helper()
	result, err := factory.Build(principal, binding, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertFactoryRejects(
	t *testing.T,
	fixture currentIdentityFixture,
	principal common.CallerPrincipal,
	binding identity.CurrentBinding,
	issuer common.IssuerBinding,
) {
	t.Helper()
	result, err := fixture.factory.Build(principal, binding, issuer)
	if !errors.Is(err, ErrCredentialInputInvalid) || result != (common.TaskSessionIdentity{}) {
		t.Fatalf("Build() = %#v, %v; want zero identity and ErrCredentialInputInvalid", result, err)
	}
}

func assertExecutionFenceMappedFieldByField(
	t *testing.T,
	binding identity.CurrentBinding,
	source common.ExecutionFence,
) {
	t.Helper()
	bindingValue := reflect.ValueOf(binding)
	sourceValue := reflect.ValueOf(source)
	sourceType := sourceValue.Type()
	for index := range sourceType.NumField() {
		field := sourceType.Field(index)
		bindingField := bindingValue.FieldByName(field.Name)
		if !bindingField.IsValid() || bindingField.Type() != field.Type {
			t.Fatalf("CurrentBinding.%s cannot map ExecutionFence.%s", field.Name, field.Name)
		}
		if !reflect.DeepEqual(sourceValue.Field(index).Interface(), bindingField.Interface()) {
			t.Errorf("ExecutionFence.%s = %#v, want CurrentBinding value %#v", field.Name, sourceValue.Field(index).Interface(), bindingField.Interface())
		}
	}
}

func newCurrentVerifier(t *testing.T, resolver CurrentTaskPrincipalResolver) *ResolverCurrentIdentityVerifier {
	t.Helper()
	verifier, err := NewResolverCurrentIdentityVerifier(resolver)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func assertVerifierUnavailable(
	t *testing.T,
	verifier *ResolverCurrentIdentityVerifier,
	ctx context.Context,
	expected common.TaskSessionIdentity,
) {
	t.Helper()
	if err := verifier.Verify(ctx, expected); err != ErrCredentialUnavailable {
		t.Fatalf("Verify() error = %v, want exact ErrCredentialUnavailable", err)
	}
}

func assertResolverResultUnavailable(
	t *testing.T,
	fixture currentIdentityFixture,
	result taskprincipal.TaskPrincipal,
) {
	t.Helper()
	verifier := newCurrentVerifier(t, &currentResolverStub{result: result})
	assertVerifierUnavailable(t, verifier, context.Background(), fixture.sessionIdentity)
}

func mutateCurrentBindingField(t *testing.T, binding *identity.CurrentBinding, fieldName string) {
	t.Helper()
	field := reflect.ValueOf(binding).Elem().FieldByName(fieldName)
	if !field.IsValid() || !field.CanSet() {
		t.Fatalf("CurrentBinding.%s is not mutable in test", fieldName)
	}
	switch field.Kind() {
	case reflect.String:
		if fieldName == "BindingDigest" {
			field.SetString(strings.Repeat("c", 64))
		} else {
			field.SetString(field.String() + "-stale")
		}
	case reflect.Int64:
		field.SetInt(field.Int() + 1)
	default:
		t.Fatalf("CurrentBinding.%s has unsupported kind %s", fieldName, field.Kind())
	}
}

type currentResolverStub struct {
	result   taskprincipal.TaskPrincipal
	err      error
	calls    int
	observed common.TaskSessionIdentity
}

func (r *currentResolverStub) RequireCurrent(ctx context.Context) (taskprincipal.TaskPrincipal, error) {
	r.calls++
	principal, principalErr := common.RequireCallerPrincipal(ctx)
	attribution, attributionErr := common.RequireCallerAttribution(ctx)
	if principalErr != nil || attributionErr != nil {
		return taskprincipal.TaskPrincipal{}, errors.New("test resolver context identity missing")
	}
	r.observed = common.TaskSessionIdentity{Principal: principal, Attribution: attribution}
	if r.err != nil {
		return taskprincipal.TaskPrincipal{}, r.err
	}
	return r.result, nil
}
