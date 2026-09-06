package taskcredential

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

var errTestIdentityNotCurrent = errors.New("test identity is not current")

func TestNewRequiresNonNilCurrentIdentityVerifier(t *testing.T) {
	var typedNil *verifierFixture
	for _, verifier := range []CurrentIdentityVerifier{nil, typedNil} {
		if _, err := New(verifier); !errors.Is(err, ErrRegistryInvalid) {
			t.Fatalf("New(%T) error = %v, want ErrRegistryInvalid", verifier, err)
		}
	}
	fixture := newRegistryFixture(t)
	if err := fixture.registry.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestIssuePendingKeepsRawBearerOutsideRegistryAndRedactsHolder(t *testing.T) {
	fixture := newRegistryFixture(t)
	expectedBearer := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, credentialEntropyBytes))
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-redaction", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := issued.BoundLeaseID(); got != "lease-redaction" {
		t.Fatalf("BoundLeaseID() = %q, want lease-redaction", got)
	}
	assertIssuedCredentialRedaction(t, issued, expectedBearer)
	assertRegistryDoesNotContainBearer(t, fixture.registry, issued, expectedBearer)

	bearer, err := issued.TakeBearer()
	if err != nil {
		t.Fatal(err)
	}
	if bearer != expectedBearer {
		t.Fatalf("TakeBearer() returned unexpected bearer length=%d", len(bearer))
	}
	if _, err := issued.TakeBearer(); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("second TakeBearer() error = %v, want ErrCredentialUnavailable", err)
	}
}

func TestIssuedCredentialBoundLeaseIDSurvivesTakeDiscardCloseAndCopies(t *testing.T) {
	fixture := newRegistryFixture(t)
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-binding", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	copyA, copyB := *issued, *issued
	if got := copyA.BoundLeaseID(); got != "lease-binding" {
		t.Fatalf("copied BoundLeaseID() = %q, want lease-binding", got)
	}
	if _, err := copyA.TakeBearer(); err != nil {
		t.Fatalf("TakeBearer() error = %v", err)
	}
	if got := copyB.BoundLeaseID(); got != "lease-binding" {
		t.Fatalf("BoundLeaseID() after TakeBearer = %q, want lease-binding", got)
	}
	copyB.Discard()
	if got := issued.BoundLeaseID(); got != "lease-binding" {
		t.Fatalf("BoundLeaseID() after Discard = %q, want lease-binding", got)
	}
	if err := issued.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := issued.BoundLeaseID(); got != "lease-binding" {
		t.Fatalf("BoundLeaseID() after Close = %q, want lease-binding", got)
	}
}

func TestIssuedCredentialBoundLeaseIDZeroValueIsEmpty(t *testing.T) {
	var issued IssuedCredential
	if got := issued.BoundLeaseID(); got != "" {
		t.Fatalf("zero-value BoundLeaseID() = %q, want empty", got)
	}
}

func TestIssuedCredentialBoundLeaseIDRaceWithBearerLifecycle(t *testing.T) {
	fixture := newRegistryFixture(t)
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-binding-race", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	copyA, copyB := *issued, *issued
	done := make(chan struct{}, 2)
	runtimesafe.SafeGo(t.Context(), nil, "taskcredential.test.bound-lease-race-a", func(context.Context) {
		for range 100 {
			if got := copyA.BoundLeaseID(); got != "lease-binding-race" {
				t.Errorf("copyA BoundLeaseID() = %q, want lease-binding-race", got)
			}
			_, _ = copyA.TakeBearer()
		}
		done <- struct{}{}
	})
	runtimesafe.SafeGo(t.Context(), nil, "taskcredential.test.bound-lease-race-b", func(context.Context) {
		for range 100 {
			if got := copyB.BoundLeaseID(); got != "lease-binding-race" {
				t.Errorf("copyB BoundLeaseID() = %q, want lease-binding-race", got)
			}
			copyB.Discard()
		}
		done <- struct{}{}
	})
	for range 2 {
		<-done
	}
}

func TestIssuedCredentialDiscardWipesUnconsumedBearerAndIsIdempotent(t *testing.T) {
	fixture := newRegistryFixture(t)
	expectedBearer := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, credentialEntropyBytes))
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-discard", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if issued == nil || issued.holder == nil {
		t.Fatal("IssuePending returned a credential without a holder")
	}
	// Retain an alias to the backing array so the test observes the actual wipe,
	// not merely the holder's pointer being detached.
	raw := issued.holder.bearer
	if len(raw) == 0 {
		t.Fatal("IssuePending returned an empty raw bearer")
	}
	issued.Discard()
	issued.Discard()
	for index, value := range raw {
		if value != 0 {
			t.Fatalf("Discard() left raw bearer byte %d non-zero: %d", index, value)
		}
	}
	if issued.holder.bearer != nil {
		t.Fatal("Discard() retained the holder bearer slice")
	}
	if _, err := issued.TakeBearer(); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("TakeBearer() after Discard error = %v, want ErrCredentialUnavailable", err)
	}
	assertIssuedCredentialRedaction(t, issued, expectedBearer)
	if err := issued.Close(); err != nil {
		t.Fatalf("Close() after Discard error = %v", err)
	}
}

func TestIssuedCredentialCloseWipesAcrossValueCopies(t *testing.T) {
	fixture := newRegistryFixture(t)
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-close-holder", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	copyA, copyB := *issued, *issued
	if err := copyA.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := copyB.TakeBearer(); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("TakeBearer() through copied holder after Close error = %v, want ErrCredentialUnavailable", err)
	}
	if err := copyB.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestIssuedCredentialZeroValueCloseAndDiscardAreNoOps(t *testing.T) {
	var issued IssuedCredential
	issued.Discard()
	if err := issued.Close(); err != nil {
		t.Fatalf("zero-value Close() error = %v", err)
	}
	if _, err := issued.TakeBearer(); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("zero-value TakeBearer() error = %v, want ErrCredentialUnavailable", err)
	}
}

func TestIssuedCredentialTakeAndDiscardRaceHasSingleTerminalOutcome(t *testing.T) {
	fixture := newRegistryFixture(t)
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-take-discard-race", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	copyA, copyB := *issued, *issued
	result := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "taskcredential.test.take-discard-race", func(context.Context) {
		_, takeErr := copyA.TakeBearer()
		result <- takeErr
	})
	copyB.Discard()
	takeErr := <-result
	if takeErr != nil && !errors.Is(takeErr, ErrCredentialUnavailable) {
		t.Fatalf("racing TakeBearer() error = %v, want success or ErrCredentialUnavailable", takeErr)
	}
	if _, err := copyA.TakeBearer(); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("post-race TakeBearer() error = %v, want ErrCredentialUnavailable", err)
	}
}

func TestPendingCredentialCannotAuthenticateBeforeExactActivation(t *testing.T) {
	fixture := newRegistryFixture(t)
	bearer, digest := issueAndTake(t, fixture, "lease-pending", fixture.now.Add(time.Minute))
	if bearer == "" {
		t.Fatal("IssuePending returned an empty bearer")
	}
	assertAuthenticateUnavailable(t, fixture.registry, digest)

	identity := fixture.identity("task-pending", fixture.now.Add(time.Minute))
	fixture.verifier.setCurrent(identity)
	if err := fixture.registry.Activate(context.Background(), "lease-pending", identity); err != nil {
		t.Fatal(err)
	}
	got, err := fixture.registry.Authenticate(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(identity) {
		t.Fatalf("Authenticate() = %#v, want activated identity", got)
	}
	if err := fixture.registry.Activate(context.Background(), "lease-pending", identity); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("second Activate() error = %v, want ErrCredentialUnavailable", err)
	}
}

func TestTakeBearerIsOneUseAcrossConcurrentValueCopies(t *testing.T) {
	fixture := newRegistryFixture(t)
	issued, err := fixture.registry.IssuePending(context.Background(), "lease-take-race", fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	copyA, copyB := *issued, *issued
	results := make(chan error, 2)
	for index, credential := range []IssuedCredential{copyA, copyB} {
		label := fmt.Sprintf("taskcredential.test.take-race-%d", index)
		runtimesafe.SafeGo(t.Context(), nil, label, func(context.Context) {
			_, takeErr := credential.TakeBearer()
			results <- takeErr
		})
	}
	successes := 0
	unavailable := 0
	for range 2 {
		takeErr := <-results
		if takeErr == nil {
			successes++
		} else if errors.Is(takeErr, ErrCredentialUnavailable) {
			unavailable++
		}
	}
	if successes != 1 || unavailable != 1 {
		t.Fatalf("concurrent TakeBearer results: successes=%d unavailable=%d", successes, unavailable)
	}
}

func TestActivateRejectsIdentityExpiryBeyondCredentialAndLeavesPendingUnavailable(t *testing.T) {
	fixture := newRegistryFixture(t)
	expiresAt := fixture.now.Add(time.Minute)
	_, digest := issueAndTake(t, fixture, "lease-expiry-bound", expiresAt)
	identity := fixture.identity("task-expiry-bound", expiresAt.Add(time.Nanosecond))
	fixture.verifier.setCurrent(identity)
	if err := fixture.registry.Activate(context.Background(), "lease-expiry-bound", identity); !errors.Is(err, ErrCredentialInputInvalid) {
		t.Fatalf("Activate() error = %v, want ErrCredentialInputInvalid", err)
	}
	assertAuthenticateUnavailable(t, fixture.registry, digest)
}

func TestAuthenticateUnavailabilityMatrix(t *testing.T) {
	t.Run("wrong digest", func(t *testing.T) {
		fixture := newRegistryFixture(t)
		assertAuthenticateUnavailable(t, fixture.registry, common.SessionCredentialDigest{1})
	})
	t.Run("revoked and idempotent revoke", func(t *testing.T) {
		fixture := newRegistryFixture(t)
		_, digest, identity := issueActivate(t, fixture, "lease-revoke", "task-revoke", time.Minute)
		if !identity.Equal(fixture.verifier.currentFor(identity.Attribution.TaskID)) {
			t.Fatal("fixture current identity mismatch")
		}
		if err := fixture.registry.Revoke(context.Background(), "lease-revoke"); err != nil {
			t.Fatal(err)
		}
		if err := fixture.registry.Revoke(context.Background(), "lease-revoke"); err != nil {
			t.Fatalf("idempotent Revoke() error = %v", err)
		}
		assertAuthenticateUnavailable(t, fixture.registry, digest)
	})
	t.Run("expired", func(t *testing.T) {
		fixture := newRegistryFixture(t)
		_, digest, _ := issueActivate(t, fixture, "lease-expired", "task-expired", time.Minute)
		fixture.clock.set(fixture.now.Add(time.Minute))
		assertAuthenticateUnavailable(t, fixture.registry, digest)
	})
	t.Run("rebound", func(t *testing.T) {
		fixture := newRegistryFixture(t)
		_, digest, identity := issueActivate(t, fixture, "lease-rebound", "task-rebound", time.Minute)
		rebound := fixture.identity(identity.Attribution.TaskID, identity.Principal.ExpiresAt)
		rebound.Principal.AuthContext = "auth/rebound"
		fixture.verifier.setCurrent(rebound)
		assertAuthenticateUnavailable(t, fixture.registry, digest)
	})
	t.Run("current verifier error", func(t *testing.T) {
		fixture := newRegistryFixture(t)
		_, digest, _ := issueActivate(t, fixture, "lease-verifier", "task-verifier", time.Minute)
		fixture.verifier.setError(errors.New("durable identity read failed for task-secret"))
		_, err := fixture.registry.Authenticate(context.Background(), digest)
		if !errors.Is(err, ErrCredentialUnavailable) || strings.Contains(err.Error(), "task-secret") {
			t.Fatalf("Authenticate() error = %v, want redacted ErrCredentialUnavailable", err)
		}
	})
}

func TestCredentialsRemainIsolatedAcrossLeases(t *testing.T) {
	fixture := newRegistryFixture(t)
	_, digestA, identityA := issueActivate(t, fixture, "lease-a", "task-a", time.Minute)
	_, digestB, identityB := issueActivate(t, fixture, "lease-b", "task-b", time.Minute)
	gotA, err := fixture.registry.Authenticate(context.Background(), digestA)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := fixture.registry.Authenticate(context.Background(), digestB)
	if err != nil {
		t.Fatal(err)
	}
	if !gotA.Equal(identityA) || !gotB.Equal(identityB) || gotA.Equal(gotB) {
		t.Fatal("credential digests did not preserve exact lease identity isolation")
	}
	if err := fixture.registry.Revoke(context.Background(), "lease-a"); err != nil {
		t.Fatal(err)
	}
	assertAuthenticateUnavailable(t, fixture.registry, digestA)
	if _, err := fixture.registry.Authenticate(context.Background(), digestB); err != nil {
		t.Fatalf("revoking lease-a affected lease-b: %v", err)
	}
}

func TestActivateLosesCASWhenRevokeLinearizesDuringVerification(t *testing.T) {
	fixture := newRegistryFixture(t)
	_, digest := issueAndTake(t, fixture, "lease-activate-race", fixture.now.Add(time.Minute))
	identity := fixture.identity("task-activate-race", fixture.now.Add(time.Minute))
	fixture.verifier.setCurrent(identity)
	entered, release := fixture.verifier.blockNext()
	result := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "taskcredential.test.activate-revoke-race", func(context.Context) {
		result <- fixture.registry.Activate(context.Background(), "lease-activate-race", identity)
	})
	<-entered
	if err := fixture.registry.Revoke(context.Background(), "lease-activate-race"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("racing Activate() error = %v, want ErrCredentialUnavailable", err)
	}
	assertAuthenticateUnavailable(t, fixture.registry, digest)
}

func TestAuthenticateLosesCASWhenRevokeLinearizesDuringVerification(t *testing.T) {
	fixture := newRegistryFixture(t)
	_, digest, _ := issueActivate(t, fixture, "lease-auth-race", "task-auth-race", time.Minute)
	entered, release := fixture.verifier.blockNext()
	result := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "taskcredential.test.authenticate-revoke-race", func(context.Context) {
		_, err := fixture.registry.Authenticate(context.Background(), digest)
		result <- err
	})
	<-entered
	if err := fixture.registry.Revoke(context.Background(), "lease-auth-race"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("racing Authenticate() error = %v, want ErrCredentialUnavailable", err)
	}
}

func TestCloseRevokesAllAndRejectsFutureUse(t *testing.T) {
	fixture := newRegistryFixture(t)
	_, digestA, _ := issueActivate(t, fixture, "lease-close-a", "task-close-a", time.Minute)
	_, digestB := issueAndTake(t, fixture, "lease-close-b", fixture.now.Add(time.Minute))
	if err := fixture.registry.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.Close(); err != nil {
		t.Fatalf("idempotent Close() error = %v", err)
	}
	assertAuthenticateUnavailable(t, fixture.registry, digestA)
	assertAuthenticateUnavailable(t, fixture.registry, digestB)
	if _, err := fixture.registry.IssuePending(context.Background(), "lease-after-close", fixture.now.Add(time.Minute)); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("IssuePending() after Close error = %v, want ErrCredentialUnavailable", err)
	}
	if err := fixture.registry.Revoke(context.Background(), "lease-close-a"); err != nil {
		t.Fatalf("Revoke() after Close must remain idempotent for a known lease: %v", err)
	}
}

type registryFixture struct {
	now      time.Time
	clock    *fixedClock
	verifier *verifierFixture
	registry *Registry
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	clock := &fixedClock{value: now}
	verifier := &verifierFixture{current: make(map[string]common.TaskSessionIdentity)}
	entropy := append(bytes.Repeat([]byte{0x11}, credentialEntropyBytes), bytes.Repeat([]byte{0x22}, credentialEntropyBytes)...)
	entropy = append(entropy, bytes.Repeat([]byte{0x33}, credentialEntropyBytes)...)
	registry, err := newRegistry(verifier, bytes.NewReader(entropy), clock.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return &registryFixture{now: now, clock: clock, verifier: verifier, registry: registry}
}

func (f *registryFixture) identity(taskID string, expiresAt time.Time) common.TaskSessionIdentity {
	principal := common.CallerPrincipal{
		PrincipalRef: "principal/" + taskID,
		ScopeRef:     "scope/test",
		Profile:      "standalone_task_v1",
		AuthContext:  "auth/" + taskID,
		ExpiresAt:    expiresAt,
	}
	attribution := common.CallerAttribution{
		SchemaVersion: common.CallerAttributionSchemaVersion,
		SubjectKind:   common.CallerAttributionSubjectKindTask,
		TaskID:        taskID,
		Source: common.ExecutionFence{
			AgentID: taskID, ReservationID: "reservation/" + taskID, ReservationGeneration: 1,
			IncarnationID: "incarnation/" + taskID, IncarnationGeneration: 1,
			BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence/" + taskID,
		},
		Issuer: common.IssuerBinding{
			Kind: common.CallerAttributionIssuerManagedRuntime, BindingRef: "issuer/test",
			Generation: 1, Digest: strings.Repeat("b", 64),
		},
		Audience: common.CallerAttributionAudience,
	}
	return common.TaskSessionIdentity{Principal: principal, Attribution: attribution}
}

func issueAndTake(
	t *testing.T,
	fixture *registryFixture,
	leaseID string,
	expiresAt time.Time,
) (string, common.SessionCredentialDigest) {
	t.Helper()
	issued, err := fixture.registry.IssuePending(context.Background(), leaseID, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := issued.TakeBearer()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := common.DigestSessionCredential(bearer)
	if err != nil {
		t.Fatal(err)
	}
	return bearer, digest
}

func issueActivate(
	t *testing.T,
	fixture *registryFixture,
	leaseID string,
	taskID string,
	ttl time.Duration,
) (string, common.SessionCredentialDigest, common.TaskSessionIdentity) {
	t.Helper()
	expiresAt := fixture.now.Add(ttl)
	bearer, digest := issueAndTake(t, fixture, leaseID, expiresAt)
	identity := fixture.identity(taskID, expiresAt)
	fixture.verifier.setCurrent(identity)
	if err := fixture.registry.Activate(context.Background(), leaseID, identity); err != nil {
		t.Fatal(err)
	}
	return bearer, digest, identity
}

func assertAuthenticateUnavailable(t *testing.T, registry *Registry, digest common.SessionCredentialDigest) {
	t.Helper()
	identity, err := registry.Authenticate(context.Background(), digest)
	if err != ErrCredentialUnavailable {
		t.Fatalf("Authenticate() error = %v, want ErrCredentialUnavailable", err)
	}
	if identity != (common.TaskSessionIdentity{}) {
		t.Fatalf("Authenticate() returned identity on failure: %#v", identity)
	}
}

func assertRedactedView(t *testing.T, view, bearer string) {
	t.Helper()
	if strings.Contains(view, bearer) || !strings.Contains(view, credentialRedaction) {
		t.Fatalf("credential view is not fixed-redacted: %q", view)
	}
}

func assertIssuedCredentialRedaction(t *testing.T, issued *IssuedCredential, bearer string) {
	t.Helper()
	encoded, err := json.Marshal(issued)
	if err != nil {
		t.Fatal(err)
	}
	value := *issued
	encodedValue, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	views := []string{
		issued.String(), issued.GoString(), fmt.Sprint(issued), fmt.Sprintf("%+v", issued),
		fmt.Sprintf("%#v", issued), fmt.Sprintf("%x", issued), string(encoded),
		value.String(), value.GoString(), fmt.Sprint(value), fmt.Sprintf("%+v", value),
		fmt.Sprintf("%#v", value), fmt.Sprintf("%x", value), string(encodedValue),
	}
	for _, view := range views {
		assertRedactedView(t, view, bearer)
	}
}

func assertRegistryDoesNotContainBearer(
	t *testing.T,
	registry *Registry,
	issued *IssuedCredential,
	bearer string,
) {
	t.Helper()
	encoded, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	views := []string{
		fmt.Sprintf("%v", registry), fmt.Sprintf("%+v", registry), fmt.Sprintf("%#v", registry),
		string(encoded), fmt.Errorf("issued=%v registry=%#v", issued, registry).Error(),
	}
	for _, view := range views {
		if strings.Contains(view, bearer) {
			t.Fatalf("registry representation leaked raw bearer: %q", view)
		}
	}
	if reflectContainsBearer(reflect.ValueOf(registry), bearer, make(map[visit]struct{})) {
		t.Fatal("registry reflection graph retained raw bearer")
	}
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

func reflectContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	if !value.IsValid() {
		return false
	}
	switch value.Kind() {
	case reflect.Interface:
		return reflectInterfaceContainsBearer(value, bearer, seen)
	case reflect.Pointer:
		return reflectPointerContainsBearer(value, bearer, seen)
	case reflect.String:
		return strings.Contains(value.String(), bearer)
	case reflect.Slice:
		return reflectSliceContainsBearer(value, bearer, seen)
	case reflect.Array:
		return reflectSequenceContainsBearer(value, bearer, seen)
	case reflect.Struct:
		return reflectStructContainsBearer(value, bearer, seen)
	case reflect.Map:
		return reflectMapContainsBearer(value, bearer, seen)
	}
	return false
}

func reflectInterfaceContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	if value.IsNil() {
		return false
	}
	return reflectContainsBearer(value.Elem(), bearer, seen)
}

func reflectPointerContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	if value.IsNil() {
		return false
	}
	key := visit{typ: value.Type(), ptr: value.Pointer()}
	if _, ok := seen[key]; ok {
		return false
	}
	seen[key] = struct{}{}
	return reflectContainsBearer(value.Elem(), bearer, seen)
}

func reflectSliceContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	if value.Type().Elem().Kind() == reflect.Uint8 {
		return bytes.Contains(value.Bytes(), []byte(bearer))
	}
	return reflectSequenceContainsBearer(value, bearer, seen)
}

func reflectSequenceContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	for index := range value.Len() {
		if reflectContainsBearer(value.Index(index), bearer, seen) {
			return true
		}
	}
	return false
}

func reflectStructContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	for index := range value.NumField() {
		if reflectContainsBearer(value.Field(index), bearer, seen) {
			return true
		}
	}
	return false
}

func reflectMapContainsBearer(value reflect.Value, bearer string, seen map[visit]struct{}) bool {
	iterator := value.MapRange()
	for iterator.Next() {
		if reflectContainsBearer(iterator.Key(), bearer, seen) {
			return true
		}
		if reflectContainsBearer(iterator.Value(), bearer, seen) {
			return true
		}
	}
	return false
}

type fixedClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *fixedClock) set(value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
}

type verifierFixture struct {
	mu      sync.Mutex
	current map[string]common.TaskSessionIdentity
	err     error
	block   bool
	entered chan struct{}
	release chan struct{}
}

func (v *verifierFixture) Verify(ctx context.Context, identity common.TaskSessionIdentity) error {
	v.mu.Lock()
	shouldBlock := v.block
	entered := v.entered
	release := v.release
	if shouldBlock {
		v.block = false
	}
	v.mu.Unlock()
	if shouldBlock {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return v.err
	}
	current, ok := v.current[identity.Attribution.TaskID]
	if !ok || !current.Equal(identity) {
		return errTestIdentityNotCurrent
	}
	return nil
}

func (v *verifierFixture) setCurrent(identity common.TaskSessionIdentity) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.current[identity.Attribution.TaskID] = identity
}

func (v *verifierFixture) currentFor(taskID string) common.TaskSessionIdentity {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.current[taskID]
}

func (v *verifierFixture) setError(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.err = err
}

func (v *verifierFixture) blockNext() (<-chan struct{}, chan struct{}) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.block = true
	v.entered = make(chan struct{})
	v.release = make(chan struct{})
	return v.entered, v.release
}
