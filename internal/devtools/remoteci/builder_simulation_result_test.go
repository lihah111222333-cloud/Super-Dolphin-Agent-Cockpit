package remoteci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestBuilderSimulationResultCanonicalRoundTrip(t *testing.T) {
	result := validBuilderSimulationResult(t)
	encoded, err := result.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodeBuilderSimulationResult(encoded)
	if err != nil {
		t.Fatalf("DecodeBuilderSimulationResult() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("roundtrip = %#v, want %#v", decoded, result)
	}
	if !bytes.Equal(encoded, mustBuilderSimulationResultJSON(t, result)) {
		t.Fatal("result encoding is not canonical JSON")
	}
}

func TestBuilderSimulationResultStrictJSON(t *testing.T) {
	canonical := mustBuilderSimulationResultJSON(t, validBuilderSimulationResult(t))
	cases := map[string][]byte{
		"null":         []byte("null"),
		"unknown":      append(canonical[:len(canonical)-1], []byte(`,"unknown":1}`)...),
		"trailing":     append(append([]byte(nil), canonical...), []byte("\n{}")...),
		"noncanonical": append([]byte("\n"), canonical...),
		"null field":   bytes.Replace(canonical, []byte(`"status":"NOT_VERIFIED"`), []byte(`"status":null`), 1),
	}
	duplicate := strings.Replace(string(canonical), `"schema_version":`, `"schema_version":"remote-ci-builder-simulation-result/v1","schema_version":`, 1)
	cases["duplicate"] = []byte(duplicate)
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBuilderSimulationResult(payload); err == nil {
				t.Fatal("strict decoder accepted invalid JSON")
			}
		})
	}
	oversized := append(append([]byte(nil), canonical...), bytes.Repeat([]byte(" "), builderSimulationResultMaxBytes)...)
	if _, err := DecodeBuilderSimulationResult(oversized); err == nil {
		t.Fatal("decoder accepted oversized result")
	}
}

func TestBuilderSimulationResultExitMatrix(t *testing.T) {
	cases := map[string]func(*BuilderSimulationResult){
		"success requires validated pair": func(result *BuilderSimulationResult) {
			result.Cleanup.PublicationState = BuilderSimulationPublicationStatePartial
		},
		"success requires both absence proofs": func(result *BuilderSimulationResult) {
			result.Cleanup.InputStagingAbsent = false
		},
		"success requires confirmed cleanup": func(result *BuilderSimulationResult) {
			result.Cleanup.ConfirmedAt = ""
		},
		"failed cannot claim success": func(result *BuilderSimulationResult) {
			result.ProcessExitCode = BuilderSimulationExitCodeBuildFailure
			result.Outcome = BuilderSimulationOutcomeFailed
			result.Cleanup.PublicationState = BuilderSimulationPublicationStatePartial
			result.Cloud.Status = BuilderSimulationCloudStatusSucceeded
			result.Error = validBuilderSimulationResultError()
		},
		"exit 3 is output channel only": func(result *BuilderSimulationResult) {
			result.ProcessExitCode = BuilderSimulationExitCodeOutputChannelFailure
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			result := validBuilderSimulationResult(t)
			mutate(&result)
			if err := result.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid result matrix state")
			}
		})
	}

	validFailure := validBuilderSimulationResult(t)
	validFailure.ProcessExitCode = BuilderSimulationExitCodeBuildFailure
	validFailure.Outcome = BuilderSimulationOutcomeFailed
	validFailure.Cleanup.PublicationState = BuilderSimulationPublicationStatePartial
	validFailure.Cloud.ContainerExitCode = 1
	validFailure.Cloud.Status = BuilderSimulationCloudStatusFailed
	validFailure.Error = validBuilderSimulationResultError()
	if err := validFailure.Validate(); err != nil {
		t.Fatalf("valid exit-1 result rejected: %v", err)
	}

	validContractFailure := validBuilderSimulationResult(t)
	validContractFailure.ProcessExitCode = BuilderSimulationExitCodeContractFailure
	validContractFailure.Outcome = BuilderSimulationOutcomeFailed
	validContractFailure.Artifact = BuilderSimulationResultArtifact{}
	validContractFailure.Cloud = BuilderSimulationResultCloud{Provider: cicontract.ExecutionProviderID, Status: BuilderSimulationCloudStatusNotStarted, ContainerExitCode: -1}
	validContractFailure.Cleanup = BuilderSimulationResultCleanup{ContainerGroupAbsent: true, InputStagingAbsent: true, HostInputAbsent: true, PublicationState: BuilderSimulationPublicationStateNotApplicable, ConfirmedAt: "2026-08-20T00:00:02Z"}
	validContractFailure.Error = validBuilderSimulationResultError()
	validContractFailure.Error.Stage = BuilderSimulationEventStageRequestValidated
	if err := validContractFailure.Validate(); err != nil {
		t.Fatalf("valid exit-2 result rejected: %v", err)
	}

	validCleanupFailure := validBuilderSimulationResult(t)
	validCleanupFailure.ProcessExitCode = BuilderSimulationExitCodeCleanupFailure
	validCleanupFailure.Outcome = BuilderSimulationOutcomeFailed
	validCleanupFailure.Cleanup.ContainerGroupAbsent = false
	validCleanupFailure.Cleanup.ConfirmedAt = ""
	validCleanupFailure.Error = validBuilderSimulationResultError()
	validCleanupFailure.Error.Stage = BuilderSimulationEventStageCleanupGroupAbsent
	if err := validCleanupFailure.Validate(); err != nil {
		t.Fatalf("valid exit-4 result rejected: %v", err)
	}
}

func TestBuilderSimulationResultRejectsForbiddenFieldsAndZeroLeaves(t *testing.T) {
	result := validBuilderSimulationResult(t)
	assertBuilderSimulationResultWireFields(t)
	zeroResult := result
	zeroResult.RequestDigest = ""
	if err := zeroResult.Validate(); err == nil {
		t.Fatal("Validate() accepted zero request digest")
	}
	zeroArtifact := result
	zeroArtifact.Artifact.BytesSHA256 = ""
	if err := zeroArtifact.Validate(); err == nil {
		t.Fatal("Validate() accepted zero artifact digest")
	}
}

func assertBuilderSimulationResultWireFields(t *testing.T) {
	t.Helper()
	for _, root := range []reflect.Type{
		reflect.TypeFor[BuilderSimulationResult](), reflect.TypeFor[BuilderSimulationResultArtifact](),
		reflect.TypeFor[BuilderSimulationResultCloud](), reflect.TypeFor[BuilderSimulationResultCleanup](),
		reflect.TypeFor[BuilderSimulationResultError](),
	} {
		if root.Kind() != reflect.Struct {
			t.Fatalf("result ABI root %s is not a struct", root)
		}
		for field := range root.Fields() {
			jsonName, options, hasOptions := strings.Cut(field.Tag.Get("json"), ",")
			if hasOptions || options != "" || jsonName == "" || strings.Contains(field.Tag.Get("json"), "omitempty") {
				t.Fatalf("field %s has implicit/default JSON behavior", field.Name)
			}
			lower := strings.ToLower(field.Name + " " + jsonName)
			for _, forbidden := range []string{"path", "profile", "credential", "object_key", "raw_key", "log"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("result wire field %s contains forbidden name %q", field.Name, forbidden)
				}
			}
		}
	}
}

func TestBuilderSimulationEventJSONLAndSequence(t *testing.T) {
	first := validBuilderSimulationEvent(t, 1, BuilderSimulationEventStateStarted)
	second := validBuilderSimulationEvent(t, 2, BuilderSimulationEventStateSucceeded)
	firstLine, err := EncodeBuilderSimulationEventJSONL(first)
	if err != nil {
		t.Fatal(err)
	}
	secondLine, err := EncodeBuilderSimulationEventJSONL(second)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBuilderSimulationEventJSONL(append(firstLine, secondLine...))
	if err != nil {
		t.Fatalf("DecodeBuilderSimulationEventJSONL() error = %v", err)
	}
	if len(decoded) != 2 || decoded[0].Seq != 1 || decoded[1].Seq != 2 {
		t.Fatalf("decoded events = %#v", decoded)
	}
	if err := ValidateBuilderSimulationEventSequence(decoded); err != nil {
		t.Fatal(err)
	}
	assertBuilderSimulationEventStrictJSONL(t, firstLine)
	assertBuilderSimulationEventSequenceRejects(t, first)
}

func assertBuilderSimulationEventStrictJSONL(t *testing.T, firstLine []byte) {
	t.Helper()
	for name, payload := range map[string][]byte{
		"duplicate":    bytes.Replace(firstLine, []byte(`"schema_version":`), []byte(`"schema_version":"remote-ci-builder-simulation-event/v1","schema_version":`), 1),
		"unknown":      append(firstLine[:len(firstLine)-1], []byte(`,"unknown":1}`+"\n")...),
		"trailing":     append(append([]byte(nil), firstLine...), []byte("{}\n")...),
		"noncanonical": append([]byte(" \n"), firstLine...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBuilderSimulationEventJSONL(payload); err == nil {
				t.Fatal("JSONL decoder accepted invalid event")
			}
		})
	}
}

func assertBuilderSimulationEventSequenceRejects(t *testing.T, first BuilderSimulationEvent) {
	t.Helper()
	second := first
	second.Seq = 1
	if err := ValidateBuilderSimulationEventSequence([]BuilderSimulationEvent{first, second}); err == nil {
		t.Fatal("sequence validator accepted a duplicate")
	}
	badTime := first
	badTime.Time = "2026-08-20T00:00:00+09:00"
	if err := badTime.Validate(); err == nil {
		t.Fatal("event validator accepted non-UTC time")
	}
}

func TestBuilderSimulationEventStagesAndForbiddenFields(t *testing.T) {
	for _, stage := range builderSimulationEventStages() {
		event := validBuilderSimulationEvent(t, 1, BuilderSimulationEventStateStarted)
		event.Stage = stage
		if err := event.Validate(); err != nil {
			t.Fatalf("stage %q rejected: %v", stage, err)
		}
	}
	invalid := validBuilderSimulationEvent(t, 1, BuilderSimulationEventStateStarted)
	invalid.Stage = "free-form provider log"
	if err := invalid.Validate(); err == nil {
		t.Fatal("event validator accepted free-form stage")
	}
	invalid = validBuilderSimulationEvent(t, 1, BuilderSimulationEventStateStarted)
	invalid.ResourceDigest = "sha256:ABC"
	if err := invalid.Validate(); err == nil {
		t.Fatal("event validator accepted noncanonical digest")
	}
}

func TestBuilderSimulationResultAndEventInProtocolABI(t *testing.T) {
	material, err := builderSimulationProtocolABIMaterial()
	if err != nil {
		t.Fatal(err)
	}
	text := string(material)
	for _, marker := range []string{
		BuilderSimulationResultSchema, BuilderSimulationEventSchema,
		BuilderSimulationOutcomeSucceeded, BuilderSimulationOutcomeFailed,
		BuilderSimulationEventStageRequestValidated, BuilderSimulationEventStageTerminal,
		reflect.TypeFor[BuilderSimulationResult]().Name(), reflect.TypeFor[BuilderSimulationEvent]().Name(),
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("protocol ABI material missing %q", marker)
		}
	}
	first, err := BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuilderSimulationProtocolABIDigest()
	if err != nil || first != second || !validPortableDigest(first) {
		t.Fatalf("protocol ABI digest = %q/%q error=%v", first, second, err)
	}
}

func validBuilderSimulationResult(t *testing.T) BuilderSimulationResult {
	t.Helper()
	return BuilderSimulationResult{
		SchemaVersion: BuilderSimulationResultSchema, Status: BuilderSimulationResultStatusNotVerified,
		Outcome: BuilderSimulationOutcomeSucceeded, ProcessExitCode: BuilderSimulationExitCodeSuccess,
		OperationID: "operation-1", RequestDigest: builderSimulationDigest("1"),
		EngineBindingDigest: builderSimulationDigest("2"), TargetBindingDigest: builderSimulationDigest("3"),
		Artifact:         BuilderSimulationResultArtifact{Kind: BuilderSimulationArtifactKindPortableRuntime, BytesSHA256: builderSimulationDigest("4"), IdentityDigest: builderSimulationDigest("5"), SizeBytes: 128, ReceiptDigest: builderSimulationDigest("6")},
		Cloud:            BuilderSimulationResultCloud{Provider: cicontract.ExecutionProviderID, Group: "group-1", Status: BuilderSimulationCloudStatusSucceeded, ContainerExitCode: 0, StartedAt: "2026-08-20T00:00:00Z", FinishedAt: "2026-08-20T00:00:01Z"},
		Cleanup:          BuilderSimulationResultCleanup{ContainerGroupAbsent: true, InputStagingAbsent: true, HostInputAbsent: true, PublicationState: BuilderSimulationPublicationStateValidatedPair, ConfirmedAt: "2026-08-20T00:00:01Z"},
		AuthorityEffects: BuilderSimulationAuthorityEffectsNone,
		Error:            BuilderSimulationResultError{},
	}
}

func validBuilderSimulationResultError() BuilderSimulationResultError {
	return BuilderSimulationResultError{Code: "CONTRACT_INVALID", Stage: BuilderSimulationEventStageRequestValidated, Retryable: false}
}

func validBuilderSimulationEvent(t *testing.T, seq uint64, state string) BuilderSimulationEvent {
	t.Helper()
	return BuilderSimulationEvent{
		SchemaVersion: BuilderSimulationEventSchema, OperationID: "operation-1", Seq: seq,
		Time: "2026-08-20T00:00:00Z", Stage: BuilderSimulationEventStageRequestValidated, State: state,
		RequestDigest: builderSimulationDigest("1"), ResourceID: "group-1", ResourceDigest: builderSimulationDigest("2"), ErrorCode: "",
	}
}

func mustBuilderSimulationResultJSON(t *testing.T, result BuilderSimulationResult) []byte {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
