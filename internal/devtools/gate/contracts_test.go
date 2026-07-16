package gate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testCommitSHA    = "1111111111111111111111111111111111111111"
	testBaseSHA      = "2222222222222222222222222222222222222222"
	testTreeSHA      = "3333333333333333333333333333333333333333"
	testSHA256Commit = "1111111111111111111111111111111111111111111111111111111111111111"
	testSHA256Tree   = "3333333333333333333333333333333333333333333333333333333333333333"
	testDigest       = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestSourceSpecValidateTaggedUnion(t *testing.T) {
	t.Parallel()

	tests := append(validSourceSpecCases(), invalidSourceSpecCases()...)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSourceSpecValidation(t, tt.spec, tt.wantErr)
		})
	}
}

type sourceSpecValidationCase struct {
	name    string
	spec    SourceSpec
	wantErr string
}

func validSourceSpecCases() []sourceSpecValidationCase {
	return []sourceSpecValidationCase{
		{
			name: "commit",
			spec: SourceSpec{Kind: SourceKindCommit, ObjectFormat: GitObjectFormatSHA1, Commit: &CommitSource{SHA: testCommitSHA}, SourceTreeSHA: testTreeSHA},
		},
		{
			name: "tree",
			spec: SourceSpec{Kind: SourceKindTree, ObjectFormat: GitObjectFormatSHA1, Tree: &TreeSource{SHA: testTreeSHA, ParentCommitSHA: testBaseSHA}, SourceTreeSHA: testTreeSHA},
		},
		{
			name: "sha256 commit",
			spec: SourceSpec{Kind: SourceKindCommit, ObjectFormat: GitObjectFormatSHA256, Commit: &CommitSource{SHA: testSHA256Commit}, SourceTreeSHA: testSHA256Tree},
		},
		{
			name: "sha256 create range",
			spec: SourceSpec{
				Kind:          SourceKindRange,
				ObjectFormat:  GitObjectFormatSHA256,
				Range:         &RangeSource{BaseKind: BaseKindEmptyTree, HeadSHA: testSHA256Commit, LocalRef: "refs/heads/topic", RemoteRef: "refs/heads/topic", ObservedRemoteSHA: strings.Repeat("0", 64), UpdateKind: UpdateKindCreate},
				SourceTreeSHA: testSHA256Tree,
			},
		},
		{
			name: "fast forward range",
			spec: validRangeSourceSpec(),
		},
	}
}

func invalidSourceSpecCases() []sourceSpecValidationCase {
	return []sourceSpecValidationCase{
		{
			name:    "missing variant",
			spec:    SourceSpec{Kind: SourceKindCommit, ObjectFormat: GitObjectFormatSHA1, SourceTreeSHA: testTreeSHA},
			wantErr: "commit source is required",
		},
		{
			name: "multiple variants",
			spec: SourceSpec{
				Kind:          SourceKindCommit,
				ObjectFormat:  GitObjectFormatSHA1,
				Commit:        &CommitSource{SHA: testCommitSHA},
				Tree:          &TreeSource{SHA: testCommitSHA},
				SourceTreeSHA: testTreeSHA,
			},
			wantErr: "exactly one source variant",
		},
		{
			name: "unknown kind",
			spec: SourceSpec{
				Kind:          SourceKind("branch"),
				ObjectFormat:  GitObjectFormatSHA1,
				Commit:        &CommitSource{SHA: testCommitSHA},
				SourceTreeSHA: testTreeSHA,
			},
			wantErr: "unsupported source kind",
		},
		{
			name: "create requires empty tree base",
			spec: SourceSpec{
				Kind:         SourceKindRange,
				ObjectFormat: GitObjectFormatSHA1,
				Range: &RangeSource{
					BaseKind:          BaseKindCommit,
					BaseSHA:           testBaseSHA,
					HeadSHA:           testCommitSHA,
					LocalRef:          "refs/heads/topic",
					RemoteRef:         "refs/heads/topic",
					ObservedRemoteSHA: strings.Repeat("0", 40),
					UpdateKind:        UpdateKindCreate,
				},
				SourceTreeSHA: testTreeSHA,
			},
			wantErr: "create update requires empty_tree base",
		},
		{
			name:    "commit object cannot masquerade as source tree",
			spec:    SourceSpec{Kind: SourceKindCommit, ObjectFormat: GitObjectFormatSHA1, Commit: &CommitSource{SHA: testCommitSHA}, SourceTreeSHA: testCommitSHA},
			wantErr: "commit sha and source_tree_sha must identify different Git object types",
		},
		{
			name:    "tree variant must bind exact source tree",
			spec:    SourceSpec{Kind: SourceKindTree, ObjectFormat: GitObjectFormatSHA1, Tree: &TreeSource{SHA: testTreeSHA}, SourceTreeSHA: testCommitSHA},
			wantErr: "tree sha must equal source_tree_sha",
		},
		{
			name:    "mixed sha1 and sha256 oid",
			spec:    SourceSpec{Kind: SourceKindCommit, ObjectFormat: GitObjectFormatSHA256, Commit: &CommitSource{SHA: testCommitSHA}, SourceTreeSHA: testSHA256Tree},
			wantErr: "64-character sha256 Git OID",
		},
		{
			name: "sha256 create rejects sha1 zero oid",
			spec: SourceSpec{
				Kind:          SourceKindRange,
				ObjectFormat:  GitObjectFormatSHA256,
				Range:         &RangeSource{BaseKind: BaseKindEmptyTree, HeadSHA: testSHA256Commit, LocalRef: "refs/heads/topic", RemoteRef: "refs/heads/topic", ObservedRemoteSHA: strings.Repeat("0", 40), UpdateKind: UpdateKindCreate},
				SourceTreeSHA: testSHA256Tree,
			},
			wantErr: "sha256 zero observed_remote_sha",
		},
	}
}

func assertSourceSpecValidation(t *testing.T, spec SourceSpec, wantErr string) {
	t.Helper()
	err := spec.Validate()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("Validate() error = %v, want substring %q", err, wantErr)
	}
}

func TestCanonicalContractsStrictRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	contracts := []Validatable{
		validRangeSourceSpec(),
		validImageIdentity(),
		validTrustedRunnerIdentity(),
		validGrantRequest(now),
		validResultReceipt(now),
		validActionGrant(now),
		validAcceptedImageRecord(now),
		validPromotionRecord(now),
	}
	for _, contract := range contracts {
		t.Run(reflect.TypeOf(contract).Name(), func(t *testing.T) {
			encoded, err := json.Marshal(contract)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			decoded := reflect.New(reflect.TypeOf(contract)).Interface().(Validatable)
			if err := DecodeStrictJSON(encoded, decoded); err != nil {
				t.Fatalf("DecodeStrictJSON() error = %v", err)
			}
			if err := decoded.Validate(); err != nil {
				t.Fatalf("roundtrip Validate() error = %v", err)
			}
			withUnknown := append(encoded[:len(encoded)-1], []byte(`,"unknown_field":true}`)...)
			if err := DecodeStrictJSON(withUnknown, decoded); err == nil {
				t.Fatal("DecodeStrictJSON() accepted unknown field")
			}
		})
	}
}

func TestContractFieldCoverageDetectsMissingAndStale(t *testing.T) {
	t.Parallel()

	registrations := []struct {
		producer     reflect.Type
		registration fieldConsumerRegistration
	}{
		{producer: reflect.TypeFor[SourceSpec](), registration: sourceSpecConsumerRegistration()},
		{producer: reflect.TypeFor[ResultReceipt](), registration: resultReceiptConsumerRegistration()},
		{producer: reflect.TypeFor[AcceptedImageRecord](), registration: acceptedImageRecordConsumerRegistration()},
		{producer: reflect.TypeFor[PromotionRecord](), registration: promotionRecordConsumerRegistration()},
	}
	for _, item := range registrations {
		assertFieldConsumerRegistration(t, item.producer, item.registration)
	}

	// Fail-first proof: removing one real mapping and adding one stale mapping
	// must report both sides of the contract drift.
	producer, err := JSONFieldNames(reflect.TypeFor[AcceptedImageRecord]())
	if err != nil {
		t.Fatalf("JSONFieldNames() error = %v", err)
	}
	coverage := acceptedImageRecordConsumerRegistration().Fields
	missing, stale := FieldCoverageDiff(producer, append(coverage[1:], "stale_field"))
	if len(missing) != 1 || missing[0] != coverage[0] {
		t.Fatalf("missing = %v, want %q", missing, coverage[0])
	}
	if len(stale) != 1 || stale[0] != "stale_field" {
		t.Fatalf("stale = %v, want stale_field", stale)
	}
}

func assertFieldConsumerRegistration(t *testing.T, producer reflect.Type, registration fieldConsumerRegistration) {
	t.Helper()
	if registration.Owner == "" || registration.Evidence == "" {
		t.Fatal("consumer registration requires owner and evidence")
	}
	fields, err := JSONFieldNames(producer)
	if err != nil {
		t.Fatalf("JSONFieldNames(%s) error = %v", producer, err)
	}
	if missing, stale := FieldCoverageDiff(fields, registration.Fields); len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s coverage missing=%v stale=%v", producer, missing, stale)
	}
}

type fieldConsumerRegistration struct {
	Fields   []string
	Owner    string
	Evidence string
}

func sourceSpecConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields:   []string{"commit", "kind", "object_format", "range", "source_tree_sha", "tree"},
		Owner:    "internal/devtools/gate source normalization boundary",
		Evidence: "tagged union Validate and strict JSON roundtrip before scheduler submission",
	}
}

func resultReceiptConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields: []string{
			"completed_at",
			"container",
			"deadline",
			"evidence",
			"gate_results",
			"generation",
			"image",
			"invocation_id",
			"plan_digest",
			"policy_digest",
			"receipt_id",
			"repo_id",
			"runner",
			"schema_version",
			"signature",
			"signer",
			"source",
			"started_at",
			"status",
		},
		Owner:    "internal/devtools/gate ResultReceipt signing boundary",
		Evidence: "strict JSON roundtrip and Validate before signing or verification",
	}
}

func acceptedImageRecordConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields: []string{
			"accepted_at",
			"generation",
			"image",
			"image_input_digest",
			"policy_digest",
			"previous_record_digest",
			"repo_id",
			"runner",
			"schema_version",
			"signature",
			"signer",
			"source_tree",
			"trusted_commit",
			"trusted_ref",
		},
		Owner:    "internal/devtools/localci accepted image authority",
		Evidence: "strict load, signature verification, canonical digest, bootstrap, and promotion CAS validation",
	}
}

func promotionRecordConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields:   []string{"expected_generation", "expected_record_digest", "next", "schema_version"},
		Owner:    "internal/devtools/localci accepted image promotion CAS boundary",
		Evidence: "strict promotion validation before lock-scoped generation and predecessor comparison",
	}
}

func TestAcceptedImageCanonicalPayloadAndDigest(t *testing.T) {
	t.Parallel()

	record := validAcceptedImageRecord(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	payload, err := AcceptedImageSigningPayload(record)
	if err != nil {
		t.Fatalf("AcceptedImageSigningPayload() error = %v", err)
	}
	record.Signature = "different-signature"
	samePayload, err := AcceptedImageSigningPayload(record)
	if err != nil {
		t.Fatalf("AcceptedImageSigningPayload(changed signature) error = %v", err)
	}
	if string(payload) != string(samePayload) {
		t.Fatal("signature value changed accepted image signing payload")
	}
	firstDigest, err := AcceptedImageRecordDigest(record)
	if err != nil {
		t.Fatalf("AcceptedImageRecordDigest() error = %v", err)
	}
	record.Signature = "third-signature"
	secondDigest, err := AcceptedImageRecordDigest(record)
	if err != nil {
		t.Fatalf("AcceptedImageRecordDigest(changed signature) error = %v", err)
	}
	if firstDigest == secondDigest {
		t.Fatal("record digest did not bind signature")
	}
}

func validRangeSourceSpec() SourceSpec {
	return SourceSpec{
		Kind:         SourceKindRange,
		ObjectFormat: GitObjectFormatSHA1,
		Range: &RangeSource{
			BaseKind:          BaseKindCommit,
			BaseSHA:           testBaseSHA,
			HeadSHA:           testCommitSHA,
			LocalRef:          "refs/heads/topic",
			RemoteRef:         "refs/heads/topic",
			ObservedRemoteSHA: testBaseSHA,
			UpdateKind:        UpdateKindFastForward,
		},
		SourceTreeSHA: testTreeSHA,
	}
}

func validImageIdentity() ImageIdentity {
	return ImageIdentity{
		Registry:               "registry.invalid/super-dolphin/gate",
		OCIIndexDigest:         testDigest,
		PlatformManifestDigest: testDigest,
		ConfigDigest:           testDigest,
		RootFSDiffIDs:          []string{testDigest},
		OS:                     "linux",
		Architecture:           "arm64",
	}
}

func validTrustedRunnerIdentity() TrustedRunnerIdentity {
	return TrustedRunnerIdentity{
		BinaryDigest: testDigest,
		Signer:       SignerIdentity{KeyID: "runner-key", KeyEpoch: 1, Algorithm: SignatureAlgorithmEd25519},
		PolicyDigest: testDigest,
	}
}

func validGrantRequest(now time.Time) GrantRequest {
	return GrantRequest{
		ReceiptID:            "receipt-1",
		ReceiptDigest:        testDigest,
		InvocationID:         "invocation-1",
		InvocationOwner:      "owner-1",
		SubscriberCapability: "subscriber-capability",
		Adapter:              TrustedAdapterIdentity{Name: "git-pre-push", BinaryDigest: testDigest, Signer: validTrustedRunnerIdentity().Signer},
		ProcessChallenge:     "process-challenge",
		Audience:             ActionAudienceGitPush,
		ActionPolicy:         "fast-forward-only",
		RemoteURL:            "ssh://git@example.invalid/repo.git",
		Ref:                  "refs/heads/topic",
		OldSHA:               testBaseSHA,
		NewSHA:               testCommitSHA,
		RequestNonce:         "request-nonce",
		RequestedAt:          now,
	}
}

func validResultReceipt(now time.Time) ResultReceipt {
	return ResultReceipt{
		SchemaVersion: 1,
		ReceiptID:     "receipt-1",
		RepoID:        "repo-1",
		InvocationID:  "invocation-1",
		Source:        validRangeSourceSpec(),
		PlanDigest:    testDigest,
		PolicyDigest:  testDigest,
		Runner:        validTrustedRunnerIdentity(),
		Image:         validImageIdentity(),
		Generation:    1,
		StartedAt:     now,
		CompletedAt:   now.Add(time.Minute),
		Deadline:      now.Add(10 * time.Minute),
		Status:        ResultStatusPassed,
		GateResults:   []GateResult{{GateID: "go-test", Status: GateStatusPassed, ExitCode: 0, StartedAt: now, CompletedAt: now.Add(time.Minute), ArgvDigest: testDigest, LogDigest: testDigest}},
		Evidence:      []Evidence{{Kind: EvidenceKindProcess, Digest: testDigest}},
		Container:     ContainerEvidence{ContainerID: "container-1", NetworkID: "network-1", HostConfigDigest: testDigest, NetworkPolicyDigest: testDigest, Removed: true, NetworkRemoved: true},
		Signer:        validTrustedRunnerIdentity().Signer,
		Signature:     "signature",
	}
}

func validActionGrant(now time.Time) ActionGrant {
	return ActionGrant{
		SchemaVersion: 1,
		GrantID:       "grant-1",
		Request:       validGrantRequest(now),
		State:         ActionGrantStateIssued,
		IssuedAt:      now,
		ExpiresAt:     now.Add(time.Minute),
		Signer:        validTrustedRunnerIdentity().Signer,
		Signature:     "signature",
	}
}

func validAcceptedImageRecord(now time.Time) AcceptedImageRecord {
	return AcceptedImageRecord{
		SchemaVersion:    AcceptedImageRecordSchemaVersion,
		RepoID:           "repo-1",
		TrustedRef:       "refs/heads/main",
		TrustedCommit:    testCommitSHA,
		SourceTree:       testTreeSHA,
		PolicyDigest:     testDigest,
		ImageInputDigest: testDigest,
		Image:            validImageIdentity(),
		Runner:           validTrustedRunnerIdentity(),
		Generation:       1,
		AcceptedAt:       now,
		Signer:           validTrustedRunnerIdentity().Signer,
		Signature:        "signature",
	}
}

func validPromotionRecord(now time.Time) PromotionRecord {
	next := validAcceptedImageRecord(now)
	next.Generation = 2
	next.PreviousRecordDigest = testDigest
	return PromotionRecord{
		SchemaVersion:        PromotionRecordSchemaVersion,
		ExpectedRecordDigest: testDigest,
		ExpectedGeneration:   1,
		Next:                 next,
	}
}
