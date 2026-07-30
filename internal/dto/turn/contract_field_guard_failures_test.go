package turn

import (
	"strings"
	"testing"
)

// TestTurnContractFieldGuardFailsFirst proves real producer, consumer, and registry mutations fail closed.
func TestTurnContractFieldGuardFailsFirst(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadConsumerRegistry(t, root)
	runMissingSchemaRegistrationCase(t, root, registry)
	runMissingProductionValidatorCase(t, root, registry)
	runUnregisteredValidatorConsumerCases(t, root, registry)
	runMissingCloneFieldCase(t, root, registry)
	runStaleGoJSONFieldCase(t, root, registry)
	runMissingRawTerminalFieldCase(t, root, registry)
	runMissingCodexPublicSummaryProjectionCase(t, root, registry)
	runLegacyTerminalWireMethodCase(t, root, registry)
	runMissingCanonicalRemoteRepublishCase(t, root, registry)
	runMissingWailsTerminalPayloadCase(t, root, registry)
}

func runMissingSchemaRegistrationCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("missing schema registration", func(t *testing.T) {
		mutated := cloneConsumerRegistry(t, registry)
		delete(mutated.Schemas, "TurnRefV1")
		assertGuardFailure(t, validateConsumerRegistry(root, mutated, nil), "missing schema TurnRefV1")
	})
}

func runMissingProductionValidatorCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("missing production validator call", func(t *testing.T) {
		path := "internal/dto/turn/terminal.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "ValidateTurnTerminalV2(terminal)", "ValidateTurnRefV1(terminal)", 1)
		if mutated == source {
			t.Fatal("terminal validator mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing call ValidateTurnTerminalV2")
	})
}

func runUnregisteredValidatorConsumerCases(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	cases := []struct {
		name     string
		addition string
	}{
		{
			name:     "unregistered production validator consumer",
			addition: "\nfunc unregisteredTurnRefConsumer(value TurnRefV1) error { return ValidateTurnRefV1(value) }\n",
		},
		{
			name:     "unregistered aliased validator consumer",
			addition: "\nfunc unregisteredAliasedTurnRefConsumer(value TurnRefV1) error { validator := ValidateTurnRefV1; return validator(value) }\n",
		},
		{
			name:     "unregistered validator parameter transfer",
			addition: "\nfunc unregisteredPassedTurnRefConsumer(value TurnRefV1) error { return invokeUnregisteredTurnRefValidator(ValidateTurnRefV1, value) }\nfunc invokeUnregisteredTurnRefValidator(validator func(any) error, value any) error { return validator(value) }\n",
		},
		{
			name:     "unregistered selector validator value",
			addition: "\nfunc unregisteredSelectorTurnRefConsumer(value TurnRefV1) error { validator := canonicalValidators.ValidateTurnRefV1; return validator(value) }\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := "internal/dto/turn/terminal.go"
			source := readRepositorySource(t, root, path)
			assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: source + testCase.addition}), "TurnRefV1 Go production consumers")
		})
	}
}

func runMissingCloneFieldCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("canonical terminal clone omits a newly added reference field", func(t *testing.T) {
		terminalPath := "internal/dto/turn/terminal.go"
		schemaPath := "internal/dto/turn/schema/turn_terminal.v2.json"
		terminalSource := readRepositorySource(t, root, terminalPath)
		terminalMutated := strings.Replace(
			terminalSource,
			"\tOccurredAt           string         `json:\"occurredAt\"`",
			"\tOccurredAt           string         `json:\"occurredAt\"`\n\tFutureLabels         []string       `json:\"futureLabels,omitempty\"`",
			1,
		)
		if terminalMutated == terminalSource {
			t.Fatal("terminal future field mutation did not change production source")
		}
		schemaSource := readRepositorySource(t, root, schemaPath)
		schemaMutated := strings.Replace(
			schemaSource,
			"    \"occurredAt\": { \"type\": \"string\", \"minLength\": 1 }",
			"    \"occurredAt\": { \"type\": \"string\", \"minLength\": 1 },\n    \"futureLabels\": { \"type\": \"array\", \"items\": { \"type\": \"string\" } }",
			1,
		)
		if schemaMutated == schemaSource {
			t.Fatal("canonical schema future field mutation did not change schema source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{
			terminalPath: terminalMutated,
			schemaPath:   schemaMutated,
		}), "TurnTerminalV2 clone field coverage missing=[futureLabels]")
	})
}

func runStaleGoJSONFieldCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("stale Go JSON field", func(t *testing.T) {
		path := "internal/dto/turn/terminal.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "json:\"turnId\"", "json:\"legacyTurnId\"", 1)
		if mutated == source {
			t.Fatal("Go JSON tag mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "field coverage")
	})
}

func runMissingRawTerminalFieldCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("missing raw terminal field", func(t *testing.T) {
		path := "internal/provider/shared/terminal_outcome.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "payload[\"status\"]", "payload[\"state\"]", 1)
		if mutated == source {
			t.Fatal("raw terminal mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing string status")
	})
}

func runMissingCodexPublicSummaryProjectionCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("missing Codex trusted public summary projection", func(t *testing.T) {
		path := "internal/provider/codexapp/session_dispatch.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, `payload["summary"] = publicSummary`, `payload["private_summary"] = publicSummary`, 1)
		if mutated == source {
			t.Fatal("Codex public summary projection mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing string summary")
	})
}

func runLegacyTerminalWireMethodCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("legacy terminal wire method", func(t *testing.T) {
		path := "internal/platform/eventsurface/bind.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, `MethodTurnTerminal   = "turn/terminal"`, `MethodTurnTerminal   = "turn/completed"`, 1)
		if mutated == source {
			t.Fatal("terminal wire method mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "want \"turn/terminal\"")
	})
}

func runMissingCanonicalRemoteRepublishCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("missing canonical remote republish", func(t *testing.T) {
		path := "internal/platform/eventsurface/bind.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "CanonicalTurnTerminal(ev)", "missingCanonicalTurnTerminal(ev)", 1)
		if mutated == source {
			t.Fatal("canonical republish mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "missing call CanonicalTurnTerminal")
	})
}

func runMissingWailsTerminalPayloadCase(t *testing.T, root string, registry consumerRegistry) {
	t.Helper()
	t.Run("missing Wails terminal payload serialization", func(t *testing.T) {
		path := "internal/ui/wails/bridge.go"
		source := readRepositorySource(t, root, path)
		mutated := strings.Replace(source, "payloadToMap(notification.Payload)", "missingPayloadToMap(notification.Payload)", 1)
		if mutated == source {
			t.Fatal("Wails bridge mutation did not change production source")
		}
		assertGuardFailure(t, validateConsumerRegistry(root, registry, map[string]string{path: mutated}), "terminal-wails-bridge missing call payloadToMap")
	})
}
