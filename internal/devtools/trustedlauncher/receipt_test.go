package trustedlauncher

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/godistribution"
)

func TestReceiptValidatorRegistryCoversEveryProductionField(t *testing.T) {
	fields, err := receiptJSONFields()
	if err != nil {
		t.Fatalf("derive receipt production fields: %v", err)
	}
	validators := receiptFieldValidators()
	if len(fields) != len(validators) {
		t.Fatalf("receipt fields=%d validators=%d", len(fields), len(validators))
	}
	for _, field := range fields {
		if validators[field] == nil {
			t.Fatalf("receipt field %q has no validator", field)
		}
	}
}

func TestReceiptFieldGuardFailsWhenProductionFieldHasNoValidator(t *testing.T) {
	validators := receiptFieldValidators()
	delete(validators, "binary_sha256")

	if err := validReceiptFixture(t).validateWithValidators(validators); err == nil || !strings.Contains(err.Error(), `field "binary_sha256" has no validator`) {
		t.Fatalf("missing validator error = %v", err)
	}
}

func TestVerifyReceiptLinkedIdentityAllowsDifferentCandidateTree(t *testing.T) {
	receipt := validReceiptFixture(t)
	options := VerifyOptions{
		Tree:   strings.Repeat("c", 40),
		Linked: linkedIdentityFromReceipt(receipt),
	}
	if err := verifyReceiptLinkedIdentity(options, receipt); err != nil {
		t.Fatalf("version-compatible launcher was bound to its installation tree: %v", err)
	}
}

func TestDecodeReceiptRejectsUnknownMissingAndStaleFields(t *testing.T) {
	receipt := validReceiptFixture(t)
	encoded, err := encodeReceipt(receipt)
	if err != nil {
		t.Fatalf("encode receipt fixture: %v", err)
	}
	if _, err := DecodeReceipt(encoded); err != nil {
		t.Fatalf("decode valid receipt: %v", err)
	}

	unknown := strings.Replace(string(encoded), "{", `{"unknown":true,`, 1)
	if _, err := DecodeReceipt([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	legacy := strings.Replace(string(encoded), ReceiptSchemaVersion, "trusted-gate-launcher/v1", 1)
	if _, err := DecodeReceipt([]byte(legacy)); err == nil || !strings.Contains(err.Error(), ReceiptSchemaVersion) {
		t.Fatalf("legacy receipt schema error = %v, want %s", err, ReceiptSchemaVersion)
	}

	for index := range reflect.TypeFor[Receipt]().NumField() {
		field := reflect.TypeFor[Receipt]().Field(index)
		mutated := receipt
		reflect.ValueOf(&mutated).Elem().Field(index).Set(reflect.Zero(field.Type))
		if err := mutated.Validate(); err == nil {
			t.Fatalf("zeroed receipt field %s unexpectedly passed", field.Name)
		}
	}
}

func TestProducerEmitsOnlyConsumerAcceptedLinkedGlobals(t *testing.T) {
	identity := linkedIdentityFromReceipt(validReceiptFixture(t))
	arguments := mustExpectedBuildArguments(t, identity)
	if len(arguments) != 7 {
		t.Fatalf("build argument count = %d, want 7", len(arguments))
	}
	payloadText, observedDigest := assertLauncherLinkedArguments(t, arguments[5], identity.BuildArgumentsSHA256)
	decoded, err := DecodeLinkedIdentity(payloadText, observedDigest)
	if err != nil {
		t.Fatalf("decode producer linker values: %v", err)
	}
	if decoded != identity {
		t.Fatalf("decoded identity = %+v, want %+v", decoded, identity)
	}
}

func assertLauncherLinkedArguments(t *testing.T, linked, expectedDigest string) (string, string) {
	t.Helper()
	const sourcePrefix = "-X main.gateSourceDigest="
	const digestPrefix = " -X main.gateToolchainDigest="
	if strings.Count(linked, "-X ") != 2 || !strings.HasPrefix(linked, sourcePrefix) || !strings.Contains(linked, digestPrefix) {
		t.Fatalf("linker globals must contain payload and digest only: %q", linked)
	}
	payloadText, observedDigest, ok := strings.Cut(strings.TrimPrefix(linked, sourcePrefix), digestPrefix)
	if !ok || observedDigest != expectedDigest {
		t.Fatalf("linked values = %q", linked)
	}
	return payloadText, observedDigest
}

func TestLinkedIdentityPayloadDynamicFieldCoverage(t *testing.T) {
	payload := validLauncherLinkedPayloadFixture()
	fields, err := launcherLinkedPayloadJSONFields()
	if err != nil {
		t.Fatalf("enumerate linked payload fields: %v", err)
	}
	validators := launcherLinkedPayloadValidators()
	if len(fields) != len(validators) {
		t.Fatalf("payload fields = %v, validators = %v", fields, validators)
	}
	payloadType := reflect.TypeOf(payload)
	for field := range payloadType.Fields() {
		t.Run(field.Name, func(t *testing.T) {
			missing := payload
			reflect.ValueOf(&missing).Elem().FieldByIndex(field.Index).SetZero()
			linked := encodeRawLinkedPayloadForTest(t, missing)
			digest := mustBuildArgumentsDigest(t, mustLauncherBuildArguments(t, linked, ""))
			if _, err := DecodeLinkedIdentity(linked, digest); err == nil {
				t.Fatalf("missing payload field %s was accepted", field.Name)
			}
		})
	}
}

func TestLinkedIdentityFieldGuardFailsWithoutValidator(t *testing.T) {
	validators := launcherLinkedPayloadValidators()
	delete(validators, "compiler_sha256")
	if err := validateLauncherLinkedPayloadWith(validLauncherLinkedPayloadFixture(), validators); err == nil || !strings.Contains(err.Error(), `field "compiler_sha256" has no validator`) {
		t.Fatalf("missing validator error = %v", err)
	}
}

func TestDecodeLinkedIdentityRejectsMalformedInput(t *testing.T) {
	validJSON, err := json.Marshal(validLauncherLinkedPayloadFixture())
	if err != nil {
		t.Fatalf("marshal valid linked payload: %v", err)
	}
	tests := map[string][]byte{
		"unknown field":    append(validJSON[:len(validJSON)-1], []byte(`,"unexpected":true}`)...),
		"trailing value":   append(validJSON, []byte(` {}`)...),
		"wrong version":    []byte(strings.Replace(string(validJSON), launcherLinkedPayloadSchema, "trusted-gate-launcher-linked-identity/v1", 1)),
		"uppercase digest": []byte(strings.Replace(string(validJSON), strings.Repeat("a", 64), strings.Repeat("A", 64), 1)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			linked := launcherLinkedPayloadPrefix + base64.RawURLEncoding.EncodeToString(data)
			digest := mustBuildArgumentsDigest(t, mustLauncherBuildArguments(t, linked, ""))
			if _, err := DecodeLinkedIdentity(linked, digest); err == nil {
				t.Fatalf("malformed launcher payload %q was accepted", name)
			}
		})
	}
}

func TestBuildArgumentsIdentityDigestExcludesSecondLinkerValue(t *testing.T) {
	identity := linkedIdentityFromReceipt(validReceiptFixture(t))
	digest := mustBuildArgumentsIdentityDigest(t, identity)
	payload := mustLinkedPayload(t, identity)
	if digest != mustBuildArgumentsDigest(t, mustLauncherBuildArguments(t, payload, "")) {
		t.Fatal("build-arguments digest does not bind an empty second linker value")
	}
	if digest == mustBuildArgumentsDigest(t, mustLauncherBuildArguments(t, payload, "sha256:"+strings.Repeat("f", 64))) {
		t.Fatal("build-arguments digest unexpectedly includes its own linker value")
	}
}

func validReceiptFixture(t *testing.T) Receipt {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	receipt := Receipt{
		SchemaVersion:         ReceiptSchemaVersion,
		Tree:                  strings.Repeat("b", 40),
		SourceSHA256:          digest,
		ToolchainSHA256:       "sha256:" + strings.Repeat("c", 64),
		ClosureProvenance:     "sha256:" + strings.Repeat("d", 64),
		GoVersion:             godistribution.Version,
		GOOS:                  "darwin",
		GOARCH:                "arm64",
		CompilerPath:          "/usr/local/bin/go",
		CompilerSHA256:        "sha256:" + strings.Repeat("e", 64),
		CompilerClosureSHA256: "sha256:" + strings.Repeat("f", 64),
		BuildArgumentsSHA256:  "sha256:" + strings.Repeat("0", 64),
		BinarySHA256:          "sha256:" + strings.Repeat("1", 64),
	}
	receipt.BuildArgumentsSHA256 = mustBuildArgumentsIdentityDigest(t, linkedIdentityFromReceipt(receipt))
	receipt.BuildArguments = mustExpectedBuildArguments(t, linkedIdentityFromReceipt(receipt))
	return receipt
}

func mustExpectedBuildArguments(t *testing.T, identity LinkedIdentity) []string {
	t.Helper()
	arguments, err := expectedBuildArguments(identity)
	if err != nil {
		t.Fatalf("expected launcher build arguments: %v", err)
	}
	return arguments
}

func mustLinkedPayload(t *testing.T, identity LinkedIdentity) string {
	t.Helper()
	payload, err := encodeLauncherLinkedPayload(identity)
	if err != nil {
		t.Fatalf("encode linked payload: %v", err)
	}
	return payload
}

func validLauncherLinkedPayloadFixture() launcherLinkedPayload {
	return launcherLinkedPayload{
		SchemaVersion:         launcherLinkedPayloadSchema,
		Tree:                  strings.Repeat("b", 40),
		SourceSHA256:          "sha256:" + strings.Repeat("a", 64),
		ToolchainSHA256:       "sha256:" + strings.Repeat("c", 64),
		CompilerSHA256:        "sha256:" + strings.Repeat("e", 64),
		CompilerClosureSHA256: "sha256:" + strings.Repeat("f", 64),
	}
}

func encodeRawLinkedPayloadForTest(t *testing.T, payload launcherLinkedPayload) string {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal linked payload: %v", err)
	}
	return launcherLinkedPayloadPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func mustLauncherBuildArguments(t *testing.T, payload, digest string) []string {
	t.Helper()
	arguments, err := launcherBuildArguments(payload, digest)
	if err != nil {
		t.Fatalf("launcher build arguments: %v", err)
	}
	return arguments
}

func mustBuildArgumentsDigest(t *testing.T, arguments []string) string {
	t.Helper()
	digest, err := buildArgumentsDigest(arguments)
	if err != nil {
		t.Fatalf("build arguments digest: %v", err)
	}
	return digest
}

func mustBuildArgumentsIdentityDigest(t *testing.T, identity LinkedIdentity) string {
	t.Helper()
	digest, err := buildArgumentsIdentityDigest(identity)
	if err != nil {
		t.Fatalf("build arguments identity digest: %v", err)
	}
	return digest
}
