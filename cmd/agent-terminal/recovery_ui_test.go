package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

type fakeRecoveryApplication struct {
	run  func() error
	quit func()
}

func (app fakeRecoveryApplication) Run() error { return app.run() }
func (app fakeRecoveryApplication) Quit()      { app.quit() }

func TestRecoveryBindingContainsOnlyAllowedActions(t *testing.T) {
	typeOfBinding := reflect.TypeFor[*recoveryBinding]()
	methods := make([]string, 0, typeOfBinding.NumMethod())
	for index := 0; index < typeOfBinding.NumMethod(); index++ {
		methods = append(methods, typeOfBinding.Method(index).Name)
	}
	want := []string{"Check", "Restore", "Retry", "State"}
	if !slices.Equal(methods, want) {
		t.Fatalf("Recovery binding methods = %v, want %v", methods, want)
	}
}

func TestRecoveryApplicationCancellationQuitsOnceAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	released := make(chan struct{})
	var quitCalls atomic.Int32
	application := fakeRecoveryApplication{
		run: func() error {
			cancel()
			<-released
			return nil
		},
		quit: func() {
			quitCalls.Add(1)
			close(released)
		},
	}
	if err := runRecoveryApplication(ctx, application); !errors.Is(err, context.Canceled) {
		t.Fatalf("runRecoveryApplication() error = %v, want context.Canceled", err)
	}
	if got := quitCalls.Load(); got != 1 {
		t.Fatalf("Quit() calls = %d, want 1", got)
	}
}

func TestRecoveryApplicationNormalReturnDoesNotQuit(t *testing.T) {
	var quitCalls atomic.Int32
	application := fakeRecoveryApplication{
		run: func() error { return nil },
		quit: func() {
			quitCalls.Add(1)
		},
	}
	if err := runRecoveryApplication(context.Background(), application); err != nil {
		t.Fatalf("runRecoveryApplication() error = %v", err)
	}
	if got := quitCalls.Load(); got != 0 {
		t.Fatalf("Quit() calls = %d, want 0", got)
	}
}

func TestRecoveryBindingStateExposesTypedRecoveryMode(t *testing.T) {
	runtime, err := app.NewRecoveryRuntime(app.StartupSelection{
		Mode:       app.StartupModeRecovery,
		Projection: app.RecoveryProjection{Reason: "normal preflight failed"},
	})
	if err != nil {
		t.Fatalf("NewRecoveryRuntime() error = %v", err)
	}
	state, err := (&recoveryBinding{runtime: runtime}).State(context.Background())
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Mode != app.StartupModeRecovery || state.Projection.Reason != "normal preflight failed" || state.LastAction != "state" {
		t.Fatalf("State() = %#v", state)
	}
	if state.Actions != (recoveryActionAvailability{}) {
		t.Fatalf("normal-preflight Recovery actions = %#v, want unavailable", state.Actions)
	}
}

func TestCompleteRecoveryRestoreOrdersEffectsOnce(t *testing.T) {
	var order []string
	state, err := completeRecoveryRestore(context.Background(), recoveryRestoreOps{
		Restore: func(context.Context) (recovery.Transaction, error) {
			order = append(order, "restore")
			return recovery.Transaction{Paths: recovery.Paths{Target: "/Applications/Super Dolphin.app"}}, nil
		},
		Projection: func(context.Context) (app.RecoveryProjection, error) {
			order = append(order, "projection")
			return app.RecoveryProjection{
				TransactionID: "transaction-1", AttemptID: "attempt-1", State: recovery.StateRolledBack,
			}, nil
		},
		Quit: func() { order = append(order, "quit") },
	})
	want := []string{"restore", "projection", "quit"}
	if err != nil || !slices.Equal(order, want) {
		t.Fatalf("completeRecoveryRestore() state=%#v error=%v order=%v, want %v", state, err, order, want)
	}
	if state.LastAction != "restore" || state.Projection.State != recovery.StateRolledBack {
		t.Fatalf("completeRecoveryRestore() state = %#v", state)
	}
	if state.Actions != (recoveryActionAvailability{Restore: true}) {
		t.Fatalf("completeRecoveryRestore() actions = %#v, want retryable restore", state.Actions)
	}
}

func TestCompleteRecoveryRestoreFailureKeepsSurfaceOpen(t *testing.T) {
	restoreErr := errors.New("restart convergence unavailable")
	quitCalls := 0
	restoreCalls := 0
	_, err := completeRecoveryRestore(context.Background(), recoveryRestoreOps{
		Restore: func(context.Context) (recovery.Transaction, error) {
			restoreCalls++
			return recovery.Transaction{}, restoreErr
		},
		Projection: func(context.Context) (app.RecoveryProjection, error) {
			return app.RecoveryProjection{TransactionID: "transaction-1", State: recovery.StateRolledBack}, nil
		},
		Quit: func() { quitCalls++ },
	})
	if !errors.Is(err, restoreErr) {
		t.Fatalf("completeRecoveryRestore() error = %v, want %v", err, restoreErr)
	}
	if restoreCalls != 1 || quitCalls != 0 {
		t.Fatalf("restore/quit calls = %d/%d, want 1/0", restoreCalls, quitCalls)
	}
}

func TestCompleteRecoveryRestoreProjectionFailureStillQuits(t *testing.T) {
	projectionErr := errors.New("projection refresh failed")
	var order []string
	state, err := completeRecoveryRestore(context.Background(), recoveryRestoreOps{
		Restore: func(context.Context) (recovery.Transaction, error) {
			order = append(order, "restore")
			return recovery.Transaction{}, nil
		},
		Projection: func(context.Context) (app.RecoveryProjection, error) {
			order = append(order, "projection")
			return app.RecoveryProjection{}, projectionErr
		},
		Quit: func() { order = append(order, "quit") },
	})
	want := []string{"restore", "projection", "quit"}
	if !errors.Is(err, projectionErr) || state != (recoverySurfaceState{}) {
		t.Fatalf("completeRecoveryRestore() state=%#v error=%v, want zero/%v", state, err, projectionErr)
	}
	if !slices.Equal(order, want) {
		t.Fatalf("completeRecoveryRestore() order=%v, want %v", order, want)
	}
}

func TestRecoverySurfaceFieldGuard(t *testing.T) {
	frontend := readRecoveryClientSource(t)
	projection := app.RecoveryProjection{
		TransactionID: "transaction-1", AttemptID: "attempt-1", State: recovery.StateProbation,
		LeasePresent: true, LeaseOwner: "owner-1", LeaseGeneration: 2,
		CandidateSHA256: "candidate-sha", Reason: "failure",
	}
	state := newRecoverySurfaceState(projection, "state")
	guards := []struct {
		chain, terminal string
		producer        reflect.Type
		value           any
	}{
		{"recovery_state_to_wails_frontend", "RECOVERY_STATE_FIELDS", reflect.TypeFor[recoverySurfaceState](), state},
		{"recovery_actions_to_wails_frontend", "RECOVERY_ACTION_FIELDS", reflect.TypeFor[recoveryActionAvailability](), state.Actions},
		{"recovery_projection_to_wails_frontend", "RECOVERY_PROJECTION_FIELDS", reflect.TypeFor[app.RecoveryProjection](), state.Projection},
	}
	for _, guard := range guards {
		producerFields, err := jsonProducerFields(guard.producer)
		if err != nil {
			t.Fatal(err)
		}
		mapperFields, err := jsonMapperFields(guard.value)
		if err != nil {
			t.Fatal(err)
		}
		terminalFields, err := parseRecoveryFields(frontend, guard.terminal)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRecoveryFieldChain(guard.chain, guard.producer.String(), producerFields, mapperFields, terminalFields); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoveryFieldGuardsRejectProducerMutations(t *testing.T) {
	tests := []struct {
		chain, terminal, producer string
		typeOf                    reflect.Type
	}{
		{"recovery_state_to_wails_frontend", "RECOVERY_STATE_FIELDS", "main.recoverySurfaceState", reflect.TypeFor[recoverySurfaceState]()},
		{"recovery_actions_to_wails_frontend", "RECOVERY_ACTION_FIELDS", "main.recoveryActionAvailability", reflect.TypeFor[recoveryActionAvailability]()},
		{"recovery_projection_to_wails_frontend", "RECOVERY_PROJECTION_FIELDS", "app.RecoveryProjection", reflect.TypeFor[app.RecoveryProjection]()},
	}
	for _, test := range tests {
		fields := make([]reflect.StructField, test.typeOf.NumField(), test.typeOf.NumField()+1)
		for index := 0; index < test.typeOf.NumField(); index++ {
			fields[index] = test.typeOf.Field(index)
		}
		fields = append(fields, reflect.StructField{Name: "FutureField", Type: reflect.TypeFor[string](), Tag: `json:"future_field"`})
		mutated := reflect.StructOf(fields)
		producerFields, err := jsonProducerFields(mutated)
		if err != nil {
			t.Fatal(err)
		}
		mapperFields, err := jsonMapperFields(reflect.New(mutated).Elem().Interface())
		if err != nil {
			t.Fatal(err)
		}
		terminalFields, err := parseRecoveryFields(readRecoveryClientSource(t), test.terminal)
		if err != nil {
			t.Fatal(err)
		}
		err = validateRecoveryFieldChain(test.chain, test.producer, producerFields, mapperFields, terminalFields)
		for _, evidence := range []string{"chain=" + test.chain, "producer=" + test.producer, "stage=terminal", "field=future_field"} {
			if err == nil || !strings.Contains(err.Error(), evidence) {
				t.Fatalf("mutated field guard error = %v, missing evidence %q", err, evidence)
			}
		}
	}
}

func TestRecoverySurfaceActionsFollowJournalState(t *testing.T) {
	identity := app.RecoveryProjection{
		TransactionID: "transaction-1", AttemptID: "attempt-1", CandidateSHA256: "candidate-sha",
	}
	tests := []struct {
		name       string
		projection app.RecoveryProjection
		want       recoveryActionAvailability
	}{
		{name: "prepared", projection: withRecoveryState(identity, recovery.StatePrepared), want: recoveryActionAvailability{Restore: true}},
		{name: "backup pending", projection: withRecoveryState(identity, recovery.StateBackupPending), want: recoveryActionAvailability{Retry: true, Restore: true}},
		{name: "backup retained", projection: withRecoveryState(identity, recovery.StateBackupRetained), want: recoveryActionAvailability{Restore: true}},
		{name: "install pending", projection: withRecoveryState(identity, recovery.StateInstallPending), want: recoveryActionAvailability{Restore: true}},
		{name: "probation without lease", projection: withRecoveryState(identity, recovery.StateProbation), want: recoveryActionAvailability{Check: true, Restore: true}},
		{name: "probation with current lease", projection: app.RecoveryProjection{
			TransactionID: "transaction-1", AttemptID: "attempt-1", State: recovery.StateProbation,
			CandidateSHA256: "candidate-sha", LeasePresent: true, LeaseOwner: "owner-1", LeaseGeneration: 1,
		}, want: recoveryActionAvailability{Check: true, Restore: true}},
		{name: "probation with incomplete lease projection", projection: app.RecoveryProjection{
			TransactionID: "transaction-1", AttemptID: "attempt-1", State: recovery.StateProbation,
			CandidateSHA256: "candidate-sha", LeasePresent: true,
		}, want: recoveryActionAvailability{Check: true}},
		{name: "commit pending", projection: withRecoveryState(identity, recovery.StateCommitPending), want: recoveryActionAvailability{Check: true, Retry: true}},
		{name: "committed", projection: withRecoveryState(identity, recovery.StateCommitted)},
		{name: "rollback pending", projection: withRecoveryState(identity, recovery.StateRollbackPending), want: recoveryActionAvailability{Retry: true, Restore: true}},
		{name: "rolled back", projection: withRecoveryState(identity, recovery.StateRolledBack), want: recoveryActionAvailability{Restore: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := newRecoverySurfaceState(test.projection, "state").Actions; got != test.want {
				t.Fatalf("actions = %#v, want %#v", got, test.want)
			}
		})
	}
}

func withRecoveryState(projection app.RecoveryProjection, state recovery.State) app.RecoveryProjection {
	projection.State = state
	return projection
}

func TestRecoveryMethodIDsMatchBackendFQN(t *testing.T) {
	ids, err := parseRecoveryMethodIDs(readRecoveryClientSource(t))
	if err != nil {
		t.Fatalf("parse production Recovery method IDs: %v", err)
	}
	if err := validateRecoveryMethodIDs(ids); err != nil {
		t.Fatalf("validate production Recovery method IDs: %v", err)
	}
}

func TestValidateRecoveryMethodIDsRejectsMissingStaleAndUnknown(t *testing.T) {
	expected := expectedRecoveryMethodIDs(t)
	tests := []map[string]uint32{
		{"state": expected["state"]},
		{"state": expected["state"], "check": expected["check"], "retry": expected["retry"], "restore": 1},
		{"state": expected["state"], "check": expected["check"], "retry": expected["retry"], "restore": expected["restore"], "normal": 1},
	}
	for _, ids := range tests {
		if err := validateRecoveryMethodIDs(ids); err == nil {
			t.Fatalf("validateRecoveryMethodIDs(%v) succeeded, want failure", ids)
		}
	}
}

func readRecoveryClientSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "frontend-app", "src", "features", "update-recovery", "recoveryClient.js")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production Recovery client %s: %v", path, err)
	}
	return string(source)
}

func parseRecoveryMethodIDs(source string) (map[string]uint32, error) {
	const declaration = "const RECOVERY_METHOD_IDS = Object.freeze({"
	if count := strings.Count(source, declaration); count != 1 {
		return nil, fmt.Errorf("expected one RECOVERY_METHOD_IDS declaration, found %d", count)
	}
	bodyStart := strings.Index(source, declaration) + len(declaration)
	bodyEnd := strings.Index(source[bodyStart:], "});")
	if bodyEnd < 0 {
		return nil, fmt.Errorf("RECOVERY_METHOD_IDS declaration is not closed")
	}
	ids := make(map[string]uint32)
	for rawLine := range strings.SplitSeq(strings.TrimSpace(source[bodyStart:bodyStart+bodyEnd]), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimSuffix(rawLine, ",")), ":")
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		key = strings.TrimSpace(key)
		if !ok || err != nil || key == "" {
			return nil, fmt.Errorf("malformed RECOVERY_METHOD_IDS entry %q", rawLine)
		}
		if _, duplicate := ids[key]; duplicate {
			return nil, fmt.Errorf("duplicate RECOVERY_METHOD_IDS entry %q", key)
		}
		ids[key] = uint32(parsed)
	}
	return ids, nil
}

func validateRecoveryMethodIDs(ids map[string]uint32) error {
	expected, err := recoveryBackendMethodIDs()
	if err != nil {
		return err
	}
	for key := range ids {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("unknown RECOVERY_METHOD_IDS entry %q", key)
		}
	}
	for key, want := range expected {
		got, ok := ids[key]
		if !ok {
			return fmt.Errorf("missing RECOVERY_METHOD_IDS entry %q", key)
		}
		if got != want {
			return fmt.Errorf("stale RECOVERY_METHOD_IDS entry %q: got %d want %d", key, got, want)
		}
	}
	return nil
}

func recoveryBackendMethodIDs() (map[string]uint32, error) {
	bindingType := reflect.TypeFor[recoveryBinding]()
	pointerType := reflect.PointerTo(bindingType)
	ids := make(map[string]uint32, pointerType.NumMethod())
	for index := 0; index < pointerType.NumMethod(); index++ {
		method := pointerType.Method(index)
		key := strings.ToLower(method.Name)
		fqn := bindingType.PkgPath() + "." + bindingType.Name() + "." + method.Name
		ids[key] = recoveryMethodID(fqn)
	}
	return ids, nil
}

func expectedRecoveryMethodIDs(t *testing.T) map[string]uint32 {
	t.Helper()
	ids, err := recoveryBackendMethodIDs()
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func recoveryMethodID(fqn string) uint32 {
	const offsetBasis = uint32(2166136261)
	const prime = uint32(16777619)
	id := offsetBasis
	for index := range fqn {
		id ^= uint32(fqn[index])
		id *= prime
	}
	return id
}

func jsonProducerFields(producer reflect.Type) ([]string, error) {
	fields := make([]string, 0, producer.NumField())
	seen := make(map[string]struct{}, producer.NumField())
	for index := 0; index < producer.NumField(); index++ {
		tag, _, _ := strings.Cut(producer.Field(index).Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			return nil, fmt.Errorf("producer %s field %s has invalid JSON tag %q", producer, producer.Field(index).Name, tag)
		}
		if _, duplicate := seen[tag]; duplicate {
			return nil, fmt.Errorf("producer %s has duplicate JSON field %q", producer, tag)
		}
		seen[tag] = struct{}{}
		fields = append(fields, tag)
	}
	slices.Sort(fields)
	return fields, nil
}

func jsonMapperFields(value any) ([]string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal Recovery mapper: %w", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode Recovery mapper: %w", err)
	}
	fields := make([]string, 0, len(wire))
	for field := range wire {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	return fields, nil
}

func parseRecoveryFields(source, constant string) ([]string, error) {
	declaration := "const " + constant + " = Object.freeze(["
	if strings.Count(source, declaration) != 1 {
		return nil, fmt.Errorf("expected one %s declaration", constant)
	}
	start := strings.Index(source, declaration) + len(declaration)
	end := strings.Index(source[start:], "]);")
	if end < 0 {
		return nil, fmt.Errorf("%s declaration is not closed", constant)
	}
	fields := make([]string, 0)
	for rawLine := range strings.SplitSeq(strings.TrimSpace(source[start:start+end]), "\n") {
		field := strings.Trim(strings.TrimSpace(strings.TrimSuffix(rawLine, ",")), "'")
		if field == "" {
			return nil, fmt.Errorf("malformed %s entry %q", constant, rawLine)
		}
		fields = append(fields, field)
	}
	slices.Sort(fields)
	return fields, nil
}

func validateRecoveryFieldChain(chainID, producer string, producerFields []string, mapperFields []string, terminalFields []string) error {
	if missing, stale := fieldDifference(producerFields, mapperFields); len(missing) > 0 {
		return fmt.Errorf("chain=%s producer=%s stage=mapper field=%s status=missing", chainID, producer, missing[0])
	} else if len(stale) > 0 {
		return fmt.Errorf("chain=%s producer=%s stage=mapper field=%s status=stale", chainID, producer, stale[0])
	}
	if missing, stale := fieldDifference(producerFields, terminalFields); len(missing) > 0 {
		return fmt.Errorf("chain=%s producer=%s stage=terminal field=%s status=missing", chainID, producer, missing[0])
	} else if len(stale) > 0 {
		return fmt.Errorf("chain=%s producer=%s stage=terminal field=%s status=stale", chainID, producer, stale[0])
	}
	return nil
}

func fieldDifference(producer []string, consumer []string) (missing []string, stale []string) {
	producerSet := make(map[string]struct{}, len(producer))
	consumerSet := make(map[string]struct{}, len(consumer))
	for _, field := range producer {
		producerSet[field] = struct{}{}
	}
	for _, field := range consumer {
		consumerSet[field] = struct{}{}
	}
	for _, field := range producer {
		if _, ok := consumerSet[field]; !ok {
			missing = append(missing, field)
		}
	}
	for _, field := range consumer {
		if _, ok := producerSet[field]; !ok {
			stale = append(stale, field)
		}
	}
	return missing, stale
}
