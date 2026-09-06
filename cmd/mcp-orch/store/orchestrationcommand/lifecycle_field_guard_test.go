package orchestrationcommand

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func testLifecycleWireFieldGuard(t *testing.T) {
	assertGeneratedLifecycleSQLNoResidualArg(t)
	fixture := newLifecycleFixture(t, "lifecycle-wire-guard")
	prepared := prepareLifecycle(t, fixture, "prepare-operation")
	accepted := applyLifecycle(t, fixture, lifecycleRequest(prepared, "accept-operation", TriggerAccept, "", "fixture:accept"))
	dispatchPending := applyLifecycle(t, fixture, lifecycleRequest(accepted, "dispatch-operation", TriggerBeginDispatch, "attempt-wire", "fixture:dispatch"))
	row, err := fixture.store.queries.GetOrchestrationCommandJournal(t.Context(), storedb.GetOrchestrationCommandJournalParams{
		CommandID: accepted.CommandID,
	})
	if err != nil {
		t.Fatalf("GetOrchestrationCommandJournal() error = %v", err)
	}
	assertLifecycleSnapshotRow(t, dispatchPending, row)
	unknownFixture := newLifecycleFixture(t, "lifecycle-wire-unknown")
	unknown := lifecycleToTokenIssued(t, unknownFixture)
	unknown = applyLifecycle(t, unknownFixture, lifecycleRequest(unknown, "unknown-operation", TriggerMarkUnknown, "attempt-a1", "fixture:unknown"))
	unknownRow, err := unknownFixture.store.queries.GetOrchestrationCommandJournal(t.Context(), storedb.GetOrchestrationCommandJournalParams{
		CommandID: unknown.CommandID,
	})
	if err != nil {
		t.Fatalf("GetOrchestrationCommandJournal(unknown) error = %v", err)
	}
	assertLifecycleSnapshotRow(t, unknown, unknownRow)
	assertLifecycleAppendParamsCoverRequest(t)
	prepareRequest := TransitionRequest{CommandID: prepared.CommandID, OperationID: prepared.OperationID, Trigger: TriggerPrepare}
	prepareDigest, err := transitionDigest(prepareRequest)
	if err != nil {
		t.Fatalf("transitionDigest(prepare field guard) error = %v", err)
	}
	assertLifecyclePrepareParams(t, prepareRequest, prepareDigest, prepareLifecycleParams(prepareRequest, prepareDigest))
	request := lifecycleRequest(dispatchPending, "wire-operation", TriggerConfirmDispatch, "attempt-wire", "fixture:wire")
	digest, err := transitionDigest(request)
	if err != nil {
		t.Fatalf("transitionDigest(wire) error = %v", err)
	}
	plan, err := planTransition(t.Context(), dispatchPending, request, digest)
	if err != nil {
		t.Fatalf("planTransition(wire) error = %v", err)
	}
	assertLifecycleDigestBinding(t, fixture, prepareRequest, prepareDigest, plan)
	assertLifecycleRequestParamsValues(t, request, plan, appendLifecycleParams(plan))
	assertLifecycleDerivedParams(t, plan, appendLifecycleParams(plan))
	assertLifecycleStartsAttemptDerivation(t, dispatchPending)
	assertLifecycleRequestDigestSensitivity(t)
	assertLifecycleSnapshotShapeRejectsCorruption(t)

	samples := []any{
		accepted,
		unknown,
		lifecycleRequest(accepted, "wire-operation", TriggerConfirmDispatch, "attempt-wire", "fixture:wire"),
		Verification{
			Intent: fixture.intent, Current: accepted,
			Request:   lifecycleRequest(accepted, "wire-operation", TriggerConfirmDispatch, "attempt-wire", "fixture:wire"),
			NextPhase: PhaseDispatched,
		},
	}
	for _, sample := range samples {
		t.Run(reflect.TypeOf(sample).Name(), func(t *testing.T) {
			assertLifecycleJSONRoundTrip(t, sample)
		})
	}
}

func assertLifecycleDigestBinding(t *testing.T, fixture lifecycleFixture, prepareRequest TransitionRequest, prepareDigest string, plan verifiedTransition) {
	t.Helper()
	if err := validateLifecyclePreparation(prepareRequest, prepareDigest); err != nil {
		t.Fatalf("valid prepare digest validation error = %v", err)
	}
	wrongDigest := wrongLifecycleDigest(prepareDigest)
	if _, err := fixture.store.prepareLifecycle(t.Context(), prepareRequest, wrongDigest); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("prepare with wrong 64-hex digest error = %v, want ErrInvalidTransition", err)
	}
	if err := validateVerifiedTransition(t.Context(), plan); err != nil {
		t.Fatalf("valid append digest validation error = %v", err)
	}
	wrongPlan := plan
	wrongPlan.OperationDigest = wrongLifecycleDigest(plan.OperationDigest)
	if _, err := fixture.store.appendLifecycle(t.Context(), wrongPlan); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("append with wrong 64-hex digest error = %v, want ErrInvalidTransition", err)
	}
}

func wrongLifecycleDigest(expected string) string {
	wrong := strings.Repeat("f", 64)
	if wrong == expected {
		return strings.Repeat("e", 64)
	}
	return wrong
}

func assertLifecycleJSONRoundTrip(t *testing.T, sample any) {
	t.Helper()
	typ := reflect.TypeOf(sample)
	fields := lifecycleJSONFields(t, typ)
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("json.Marshal(%s) error = %v", typ.Name(), err)
	}
	decoded := reflect.New(typ)
	if err := json.Unmarshal(encoded, decoded.Interface()); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", typ.Name(), err)
	}
	if !reflect.DeepEqual(decoded.Elem().Interface(), sample) {
		t.Fatalf("JSON roundtrip %s = %#v, want %#v", typ.Name(), decoded.Elem().Interface(), sample)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode %s JSON object error = %v", typ.Name(), err)
	}
	if len(object) != len(fields) {
		t.Fatalf("%s JSON fields = %d, want %d", typ.Name(), len(object), len(fields))
	}
	for name := range fields {
		if _, ok := object[name]; !ok {
			t.Fatalf("%s JSON missing field %q", typ.Name(), name)
		}
	}
}

func lifecycleJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || name != lifecycleSnakeCase(field.Name) {
			t.Fatalf("%s.%s has non-enumerable or non-snake JSON tag %q", typ.Name(), field.Name, name)
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("%s has duplicate JSON field %q", typ.Name(), name)
		}
		fields[name] = field
	}
	return fields
}

func lifecycleSnakeCase(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	for index, current := range runes {
		if unicode.IsUpper(current) {
			previousLowerOrDigit := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
			acronymBoundary := index > 0 && unicode.IsUpper(runes[index-1]) && index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if previousLowerOrDigit || acronymBoundary {
				builder.WriteByte('_')
			}
			builder.WriteRune(unicode.ToLower(current))
			continue
		}
		builder.WriteRune(current)
	}
	return builder.String()
}

func assertLifecycleSnapshotRow(t *testing.T, snapshot Snapshot, row storedb.OrchestrationCommandJournal) {
	t.Helper()
	snapshotFields := lifecycleJSONFields(t, reflect.TypeFor[Snapshot]())
	rowValue := reflect.ValueOf(row)
	rowFields := make(map[string]reflect.StructField, rowValue.NumField())
	for index := 0; index < rowValue.NumField(); index++ {
		field := rowValue.Type().Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("journal row field %s has no db tag", field.Name)
		}
		if _, exists := rowFields[name]; exists {
			t.Fatalf("journal row has duplicate db field %q", name)
		}
		rowFields[name] = field
	}
	assertLifecycleExactFieldSet(t, "Snapshot/journal row", snapshotFields, rowFields)
	snapshotValue := reflect.ValueOf(snapshot)
	for name, field := range snapshotFields {
		rowField, ok := rowFields[name]
		if !ok {
			continue
		}
		got := snapshotValue.FieldByIndex(field.Index)
		stored := rowValue.FieldByIndex(rowField.Index)
		assertLifecycleFieldValue(t, name, got, stored)
	}
}

func assertLifecycleFieldValue(t *testing.T, name string, got, stored reflect.Value) {
	t.Helper()
	if got.Type() == reflect.TypeFor[time.Time]() {
		assertLifecycleTimeField(t, name, got, stored)
		return
	}
	if got.Kind() == reflect.String && stored.Kind() == reflect.String {
		assertLifecycleStringField(t, name, got, stored)
		return
	}
	if got.Kind() == reflect.Int64 && stored.Kind() == reflect.Int64 {
		assertLifecycleIntField(t, name, got, stored)
		return
	}
	t.Fatalf("journal field %s has incompatible types %s/%s", name, got.Type(), stored.Type())
}

func assertLifecycleTimeField(t *testing.T, name string, got, stored reflect.Value) {
	t.Helper()
	if stored.Kind() != reflect.String {
		t.Fatalf("journal field %s has incompatible types %s/%s", name, got.Type(), stored.Type())
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, stored.String())
	if err != nil || !recordedAt.Equal(got.Interface().(time.Time)) {
		t.Fatalf("journal field %s = %q, want %s", name, stored.String(), got.Interface().(time.Time))
	}
}

func assertLifecycleStringField(t *testing.T, name string, got, stored reflect.Value) {
	t.Helper()
	if got.String() != stored.String() {
		t.Fatalf("journal field %s = %q, want %q", name, stored.String(), got.String())
	}
}

func assertLifecycleIntField(t *testing.T, name string, got, stored reflect.Value) {
	t.Helper()
	if got.Int() != stored.Int() {
		t.Fatalf("journal field %s = %d, want %d", name, stored.Int(), got.Int())
	}
}

func assertLifecycleAppendParamsCoverRequest(t *testing.T) {
	t.Helper()
	requestFields := lifecycleJSONFields(t, reflect.TypeFor[TransitionRequest]())
	paramsType := reflect.TypeFor[storedb.AppendOrchestrationCommandJournalParams]()
	params := make(map[string]bool, paramsType.NumField())
	rawParams := make(map[string]bool, paramsType.NumField())
	for index := 0; index < paramsType.NumField(); index++ {
		name := strings.Split(paramsType.Field(index).Tag.Get("db"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("append params field %s has no db tag", paramsType.Field(index).Name)
		}
		if rawParams[name] {
			t.Fatalf("append params has duplicate db field %q", name)
		}
		rawParams[name] = true
		params[lifecycleParamCanonical(name)] = true
	}
	for name := range requestFields {
		if !params[name] {
			t.Fatalf("TransitionRequest field %q has no append params coverage", name)
		}
	}
}

func assertLifecyclePrepareParams(t *testing.T, request TransitionRequest, digest string, params storedb.PrepareOrchestrationCommandJournalParams) {
	t.Helper()
	expected := map[string]string{
		"command_id": request.CommandID, "operation_id": request.OperationID, "operation_digest": digest,
	}
	paramsValue := reflect.ValueOf(params)
	if len(expected) != paramsValue.NumField() {
		t.Fatalf("prepare params expected field count = %d, got %d", len(expected), paramsValue.NumField())
	}
	for index := 0; index < paramsValue.NumField(); index++ {
		field := paramsValue.Type().Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		want, ok := expected[name]
		if !ok || paramsValue.Field(index).Kind() != reflect.String || paramsValue.Field(index).String() != want {
			t.Fatalf("prepare params field %s = %v, want %q", field.Name, paramsValue.Field(index).Interface(), want)
		}
	}
}

func assertLifecycleRequestParamsValues(t *testing.T, request TransitionRequest, plan verifiedTransition, params storedb.AppendOrchestrationCommandJournalParams) {
	t.Helper()
	requestValue := reflect.ValueOf(request)
	paramsValue := reflect.ValueOf(params)
	paramsByDBName := make(map[string]reflect.StructField, paramsValue.NumField())
	for index := 0; index < paramsValue.NumField(); index++ {
		field := paramsValue.Type().Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		paramsByDBName[name] = field
	}
	requestType := requestValue.Type()
	for index := 0; index < requestValue.NumField(); index++ {
		assertLifecycleRequestParamValue(t, requestType.Field(index), requestValue.Field(index), plan, paramsByDBName, paramsValue)
	}
}

func assertLifecycleRequestParamValue(t *testing.T, field reflect.StructField, requestValue reflect.Value, plan verifiedTransition, paramsByDBName map[string]reflect.StructField, paramsValue reflect.Value) {
	t.Helper()
	jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
	paramName := lifecycleRequestParamName(jsonName, plan.StartsAttempt)
	paramField, ok := paramsByDBName[paramName]
	if !ok {
		t.Fatalf("TransitionRequest.%s has no parameter %q", field.Name, paramName)
	}
	paramValue := paramsValue.FieldByIndex(paramField.Index)
	matched := requestValue.Kind() == reflect.String && paramValue.Kind() == reflect.String && requestValue.String() == paramValue.String()
	matched = matched || requestValue.Kind() == reflect.Int64 && paramValue.Kind() == reflect.Int64 && requestValue.Int() == paramValue.Int()
	if !matched {
		t.Fatalf("request field %s = %v, params field %s = %v", field.Name, requestValue.Interface(), paramField.Name, paramValue.Interface())
	}
}

func lifecycleRequestParamName(jsonName string, startsAttempt bool) string {
	if jsonName != "attempt_id" {
		return jsonName
	}
	if startsAttempt {
		return "next_attempt_id"
	}
	return "expected_attempt_id"
}

func assertLifecycleDerivedParams(t *testing.T, plan verifiedTransition, params storedb.AppendOrchestrationCommandJournalParams) {
	t.Helper()
	nextAttemptID := plan.Current.AttemptID
	if plan.StartsAttempt {
		nextAttemptID = plan.Request.AttemptID
	}
	expected := map[string]any{
		"command_id":                 plan.Request.CommandID,
		"expected_version":           plan.Current.Version,
		"expected_phase":             string(plan.Current.Phase),
		"expected_attempt_id":        plan.Current.AttemptID,
		"expected_accept_resolution": plan.Current.AcceptResolution,
		"next_phase":                 string(plan.NextPhase),
		"next_accept_resolution":     plan.NextAcceptResolution,
		"next_attempt_id":            nextAttemptID,
		"uncertain_phase":            string(plan.UncertainPhase),
		"trigger":                    string(plan.Request.Trigger),
		"operation_id":               plan.Request.OperationID,
		"operation_digest":           plan.OperationDigest,
		"evidence_ref":               plan.Request.EvidenceRef,
	}
	paramsValue := reflect.ValueOf(params)
	if len(expected) != paramsValue.NumField() {
		t.Fatalf("append params expected field count = %d, got %d", len(expected), paramsValue.NumField())
	}
	for index := 0; index < paramsValue.NumField(); index++ {
		field := paramsValue.Type().Field(index)
		name := strings.Split(field.Tag.Get("db"), ",")[0]
		want, ok := expected[name]
		if !ok {
			t.Fatalf("append params field %q has no derived expectation", name)
		}
		if !lifecycleReflectValueEqual(paramsValue.Field(index), reflect.ValueOf(want)) {
			t.Fatalf("append params field %s = %v, want derived %v", field.Name, paramsValue.Field(index).Interface(), want)
		}
	}
}

func assertLifecycleStartsAttemptDerivation(t *testing.T, current Snapshot) {
	t.Helper()
	request := lifecycleRequest(current, "wire-reclaim-operation", TriggerReclaimUnsent, "attempt-wire-next", "fixture:wire-reclaim")
	digest, err := transitionDigest(request)
	if err != nil {
		t.Fatalf("transitionDigest(reclaim field guard) error = %v", err)
	}
	plan, err := planTransition(t.Context(), current, request, digest)
	if err != nil {
		t.Fatalf("planTransition(reclaim field guard) error = %v", err)
	}
	if !plan.StartsAttempt {
		t.Fatal("reclaim_unsent plan did not derive StartsAttempt=true")
	}
	params := appendLifecycleParams(plan)
	assertLifecycleRequestParamsValues(t, request, plan, params)
	assertLifecycleDerivedParams(t, plan, params)
}

func assertGeneratedLifecycleSQLNoResidualArg(t *testing.T) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating generated lifecycle SQL")
	}
	generatedPath := filepath.Join(filepath.Dir(currentFile), "..", "sqlc", "orchestration_command_journal.sql.go")
	source, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated lifecycle SQL %s: %v", generatedPath, err)
	}
	if strings.Contains(string(source), "sqlc.arg(") {
		t.Fatalf("generated lifecycle SQL contains residual sqlc.arg() expression: %s", generatedPath)
	}
}

func lifecycleReflectValueEqual(got, want reflect.Value) bool {
	if got.Type() == want.Type() {
		return reflect.DeepEqual(got.Interface(), want.Interface())
	}
	if got.Kind() == reflect.String && want.Kind() == reflect.String {
		return got.String() == want.String()
	}
	if got.Kind() == reflect.Int64 && want.Kind() == reflect.Int64 {
		return got.Int() == want.Int()
	}
	return false
}

func assertLifecycleSnapshotShapeRejectsCorruption(t *testing.T) {
	t.Helper()
	fixture := newLifecycleFixture(t, "lifecycle-wire-corruption")
	completed := lifecycleToCompleted(t, fixture)
	row, err := fixture.store.queries.GetOrchestrationCommandJournal(t.Context(), storedb.GetOrchestrationCommandJournalParams{
		CommandID: completed.CommandID,
	})
	if err != nil {
		t.Fatalf("GetOrchestrationCommandJournal(corruption) error = %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*storedb.OrchestrationCommandJournal)
	}{
		{"dispatch_aborted_without_attempt", func(value *storedb.OrchestrationCommandJournal) {
			value.Phase = string(PhaseDispatchAborted)
			value.Trigger = string(TriggerAbortDispatch)
			value.AttemptID = ""
		}},
		{"completed_with_accept_trigger", func(value *storedb.OrchestrationCommandJournal) {
			value.Trigger = string(TriggerAccept)
		}},
		{"completed_without_attempt", func(value *storedb.OrchestrationCommandJournal) {
			value.AttemptID = ""
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := row
			testCase.mutate(&candidate)
			if _, err := snapshotFromRow(candidate); err == nil {
				t.Fatalf("snapshotFromRow(%s) unexpectedly accepted corrupted row", testCase.name)
			}
		})
	}
}

func lifecycleParamCanonical(name string) string {
	switch name {
	case "next_attempt_id", "expected_attempt_id":
		return "attempt_id"
	default:
		return name
	}
}

func assertLifecycleExactFieldSet(t *testing.T, name string, want map[string]reflect.StructField, got map[string]reflect.StructField) {
	t.Helper()
	var missing, stale []string
	for field := range want {
		if _, ok := got[field]; !ok {
			missing = append(missing, field)
		}
	}
	for field := range got {
		if _, ok := want[field]; !ok {
			stale = append(stale, field)
		}
	}
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s field drift: missing=%v stale=%v", name, missing, stale)
	}
}

func assertLifecycleRequestDigestSensitivity(t *testing.T) {
	t.Helper()
	base := TransitionRequest{
		CommandID: "digest-command", OperationID: "digest-operation", ExpectedVersion: 1,
		Trigger: TriggerAccept, EvidenceRef: "fixture:digest",
	}
	baseDigest, err := transitionDigest(base)
	if err != nil {
		t.Fatalf("transitionDigest(base) error = %v", err)
	}
	seen := map[string]string{baseDigest: "base"}
	typ := reflect.TypeFor[TransitionRequest]()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		mutated := mutateLifecycleRequestField(t, base, field)
		digest, err := transitionDigest(mutated)
		if err != nil {
			t.Fatalf("transitionDigest(%s) error = %v", field.Name, err)
		}
		if digest == baseDigest {
			t.Fatalf("transition digest ignored field %s", field.Name)
		}
		if prior, exists := seen[digest]; exists {
			t.Fatalf("transition digest collision: %s and %s", field.Name, prior)
		}
		seen[digest] = field.Name
	}
}

func mutateLifecycleRequestField(t *testing.T, base TransitionRequest, field reflect.StructField) TransitionRequest {
	t.Helper()
	mutated := base
	value := reflect.ValueOf(&mutated).Elem().FieldByIndex(field.Index)
	jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
	switch value.Kind() {
	case reflect.String:
		if value.Type() == reflect.TypeFor[Trigger]() {
			value.SetString(string(TriggerAbortAccept))
		} else {
			value.SetString("mutated-" + jsonName)
		}
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		t.Fatalf("TransitionRequest.%s has unsupported digest test type %s", field.Name, value.Type())
	}
	if reflect.DeepEqual(mutated, base) {
		t.Fatalf("digest mutation for %s did not change request", field.Name)
	}
	return mutated
}
