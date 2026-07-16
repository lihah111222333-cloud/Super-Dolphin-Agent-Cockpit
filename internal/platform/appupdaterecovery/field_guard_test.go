package appupdaterecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestJournalFieldGuardEnumeratesProducerFields(t *testing.T) {
	producers := []reflect.Type{
		reflect.TypeFor[journalPayload](),
		reflect.TypeFor[journalEntry](),
		reflect.TypeFor[Identity](),
		reflect.TypeFor[ReleaseIdentity](),
		reflect.TypeFor[Paths](),
		reflect.TypeFor[TrustGeneration](),
	}
	for _, producer := range producers {
		seen := make(map[string]struct{}, producer.NumField())
		for index := 0; index < producer.NumField(); index++ {
			field := producer.Field(index)
			if field.PkgPath != "" {
				continue
			}
			if field.Tag.Get("json") == "" {
				t.Fatalf("chain=%s producer=%s field=%s has no json tag", releaseTransactionJournalChain, producer.Name(), field.Name)
			}
			name, include := producerJSONField(field)
			if !include || name == "" {
				t.Fatalf("chain=%s producer=%s field=%s is not serialized", releaseTransactionJournalChain, producer.Name(), field.Name)
			}
			if _, exists := seen[name]; exists {
				t.Fatalf("chain=%s producer=%s field=%s is duplicated", releaseTransactionJournalChain, producer.Name(), name)
			}
			seen[name] = struct{}{}
		}
	}
}

func TestJournalFieldGuardRejectsProducerFieldMutation(t *testing.T) {
	journal := fieldGuardJournal(t)
	raw, err := encodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	var envelope journalEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	identityValue, ok := payload[fieldNameForType(t, reflect.TypeFor[journalPayload](), reflect.TypeFor[Identity]())].(map[string]any)
	if !ok {
		t.Fatal("identity producer was not serialized as an object")
	}
	mutatedField := firstProducerField(t, reflect.TypeFor[Identity]())
	delete(identityValue, mutatedField)
	envelope.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(envelope.Payload)
	envelope.Checksum = hex.EncodeToString(sum[:])
	mutated, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeJournal(mutated)
	if err == nil {
		t.Fatalf("chain=%s producer=Identity field=%s mutation was accepted", releaseTransactionJournalChain, mutatedField)
	}
	for _, evidence := range []string{releaseTransactionJournalChain, "producer=Identity", mutatedField} {
		if !strings.Contains(err.Error(), evidence) {
			t.Fatalf("field guard error %q does not identify %q", err, evidence)
		}
	}
}

func fieldGuardJournal(t *testing.T) journalPayload {
	t.Helper()
	id := TransactionID("00112233445566778899aabbccddeeff")
	target := "/tmp/SuperDolphin.app"
	paths, err := PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	return newJournal(CreateRequest{
		Identity: Identity{
			TransactionID: id,
			AttemptID:     "field-guard-attempt",
			OldRelease: ReleaseIdentity{
				SHA256:         digestText("old"),
				SignerIdentity: "signer-old",
			},
			CandidateRelease: ReleaseIdentity{
				SHA256:         digestText("candidate"),
				SignerIdentity: "signer-candidate",
			},
		},
		Paths: paths,
		Trust: TrustGeneration{
			Generation:    "generation-1",
			PackageSigner: "signer-candidate",
			State:         TrustPending,
		},
	}, time.Unix(1, 0))
}

func fieldNameForType(t *testing.T, owner reflect.Type, wanted reflect.Type) string {
	t.Helper()
	for index := 0; index < owner.NumField(); index++ {
		field := owner.Field(index)
		if field.Type == wanted {
			name, include := producerJSONField(field)
			if include {
				return name
			}
		}
	}
	t.Fatalf("producer %s does not contain %s", owner.Name(), wanted.Name())
	return ""
}

func firstProducerField(t *testing.T, producer reflect.Type) string {
	t.Helper()
	for index := 0; index < producer.NumField(); index++ {
		name, include := producerJSONField(producer.Field(index))
		if include {
			return name
		}
	}
	t.Fatalf("producer %s has no serialized fields", producer.Name())
	return ""
}
