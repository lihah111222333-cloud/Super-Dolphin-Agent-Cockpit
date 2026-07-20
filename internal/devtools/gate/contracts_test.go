package gate

import (
	"crypto/ed25519"
	"encoding/base64"
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
		validResultReceipt(t, now),
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
		{producer: reflect.TypeFor[GrantRequest](), registration: grantRequestConsumerRegistration()},
		{producer: reflect.TypeFor[ResultReceipt](), registration: resultReceiptConsumerRegistration()},
		{producer: reflect.TypeFor[ContainerShardReceipt](), registration: containerShardReceiptConsumerRegistration()},
		{producer: reflect.TypeFor[ActionGrant](), registration: actionGrantConsumerRegistration()},
		{producer: reflect.TypeFor[AcceptedImageRecord](), registration: acceptedImageRecordConsumerRegistration()},
		{producer: reflect.TypeFor[PromotionRecord](), registration: promotionRecordConsumerRegistration()},
	}
	for _, item := range registrations {
		assertFieldConsumerRegistration(t, item.producer, item.registration)
	}

	// Fail-first proof: removing one real mapping and adding one stale mapping
	// must report both sides of the contract drift.
	producer, err := JSONFieldNames(reflect.TypeFor[ActionGrant]())
	if err != nil {
		t.Fatalf("JSONFieldNames() error = %v", err)
	}
	coverage := actionGrantConsumerRegistration().Fields
	missing, stale := FieldCoverageDiff(producer, append(coverage[1:], "stale_field"))
	if len(missing) != 1 || missing[0] != coverage[0] {
		t.Fatalf("missing = %v, want %q", missing, coverage[0])
	}
	if len(stale) != 1 || stale[0] != "stale_field" {
		t.Fatalf("stale = %v, want stale_field", stale)
	}
}

func TestResultReceiptCanonicalEd25519VerificationRejectsTampering(t *testing.T) {
	t.Parallel()
	receipt := validResultReceipt(t, time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ResultReceiptSigningPayload(receipt)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ResultReceiptSigningPayload(receipt)
	if err != nil || !reflect.DeepEqual(payload, again) {
		t.Fatalf("canonical payload drifted: error=%v", err)
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	if err := VerifyResultReceipt(receipt, publicKey); err != nil {
		t.Fatalf("VerifyResultReceipt() error = %v", err)
	}
	digest, err := ResultReceiptDigest(receipt)
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("ResultReceiptDigest() digest=%q error=%v", digest, err)
	}

	tests := []struct {
		name   string
		mutate func(*ResultReceipt)
	}{
		{name: "source", mutate: func(value *ResultReceipt) {
			value.Source.SourceTreeSHA = strings.Repeat("9", 40)
		}},
		{name: "generation", mutate: func(value *ResultReceipt) { value.Generation++ }},
		{name: "gate_result", mutate: func(value *ResultReceipt) {
			value.GateResults[0].ExitCode = 1
		}},
		{name: "evidence", mutate: func(value *ResultReceipt) {
			value.Evidence[0].Digest = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "container", mutate: func(value *ResultReceipt) {
			value.Container.HostConfigDigest = "sha256:" + strings.Repeat("c", 64)
		}},
		{name: "resource_witness", mutate: func(value *ResultReceipt) {
			value.Container.ResourceWitness.MemoryBytes++
		}},
		{name: "resource_witness_digest", mutate: func(value *ResultReceipt) {
			value.Container.ResourceWitnessDigest = "sha256:" + strings.Repeat("d", 64)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := receipt
			tampered.GateResults = append([]GateResult(nil), receipt.GateResults...)
			tampered.Evidence = append([]Evidence(nil), receipt.Evidence...)
			test.mutate(&tampered)
			if err := VerifyResultReceipt(tampered, publicKey); err == nil {
				t.Fatal("VerifyResultReceipt() accepted tampered receipt")
			}
		})
	}
}

func TestActionGrantCanonicalEd25519VerificationRejectsTampering(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	grant := validActionGrant(now)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ActionGrantSigningPayload(grant)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ActionGrantSigningPayload(grant)
	if err != nil || !reflect.DeepEqual(payload, again) {
		t.Fatalf("canonical payload drifted: error=%v", err)
	}
	grant.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	if err := VerifyActionGrant(grant, publicKey); err != nil {
		t.Fatalf("VerifyActionGrant() error = %v", err)
	}
	digest, err := ActionGrantDigest(grant)
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("ActionGrantDigest() digest=%q error=%v", digest, err)
	}

	tests := []struct {
		name   string
		mutate func(*ActionGrant)
	}{
		{name: "audience", mutate: func(value *ActionGrant) { value.Request.Audience = ActionAudienceRelease }},
		{name: "ref", mutate: func(value *ActionGrant) { value.Request.Ref = "refs/heads/other" }},
		{name: "tree", mutate: func(value *ActionGrant) { value.Request.SourceTreeSHA = strings.Repeat("9", 40) }},
		{name: "generation", mutate: func(value *ActionGrant) { value.Request.Generation++ }},
		{name: "attempt", mutate: func(value *ActionGrant) {
			value.Request.ActionAttemptID = "attempt:v1:" + strings.Repeat("9", 64)
		}},
		{name: "state", mutate: func(value *ActionGrant) {
			consumedAt := now.Add(30 * time.Second)
			value.State = ActionGrantStateConsumed
			value.ConsumedAt = &consumedAt
		}},
		{name: "signature", mutate: func(value *ActionGrant) {
			value.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := grant
			test.mutate(&tampered)
			if err := VerifyActionGrant(tampered, publicKey); err == nil {
				t.Fatal("VerifyActionGrant() accepted tampered grant")
			}
		})
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

func grantRequestConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields: []string{
			"action_attempt_id", "action_policy", "adapter", "audience", "expires_at", "generation", "invocation_id",
			"invocation_owner", "new_sha", "old_sha", "process_challenge", "receipt_digest",
			"receipt_id", "ref", "remote_url", "repo_id", "request_nonce", "requested_at",
			"source_tree_sha", "subscriber_capability",
		},
		Owner:    "cmd/super-dolphin-gate ActionGrant issuance and consumption boundary",
		Evidence: "canonical signing plus receipt, generation, tree, audience, ref, nonce, and expiry verification",
	}
}

func resultReceiptConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields: []string{
			"authority_attestation",
			"authority_owner",
			"completed_at",
			"container",
			"deadline",
			"evidence",
			"entrypoint",
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
			"shard_receipts",
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

func containerShardReceiptConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields: []string{
			"completed_at", "container", "container_id", "deadline", "exited_at", "gate_executions",
			"removal_proof_digest", "removed", "resource_witness", "resource_witness_digest",
			"shard", "started_at", "status",
		},
		Owner:    "internal/devtools/gate ResultReceipt shard signing boundary",
		Evidence: "stable JSON fields, canonical shard validation, aggregation, and Ed25519 tamper rejection",
	}
}

func actionGrantConsumerRegistration() fieldConsumerRegistration {
	return fieldConsumerRegistration{
		Fields: []string{
			"consumed_at", "expires_at", "grant_id", "issued_at", "request", "revoked_at",
			"schema_version", "signature", "signer", "state",
		},
		Owner:    "cmd/super-dolphin-gate ActionGrant authority and durable CAS store",
		Evidence: "strict canonical signature verification and issued-to-terminal SQLite compare-and-swap",
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
		RepoID:               "repo-1",
		InvocationID:         "invocation-1",
		InvocationOwner:      "owner-1",
		SubscriberCapability: "subscriber-capability",
		Adapter:              TrustedAdapterIdentity{Name: "git-pre-push", BinaryDigest: testDigest, Signer: validTrustedRunnerIdentity().Signer},
		ProcessChallenge:     "process-challenge",
		SourceTreeSHA:        testTreeSHA,
		Generation:           1,
		Audience:             ActionAudienceGitPush,
		ActionPolicy:         "fast-forward-only",
		RemoteURL:            "ssh://git@example.invalid/repo.git",
		Ref:                  "refs/heads/topic",
		OldSHA:               testBaseSHA,
		NewSHA:               testCommitSHA,
		ActionAttemptID:      "attempt:v1:" + strings.Repeat("a", 64),
		RequestNonce:         "request-nonce",
		RequestedAt:          now,
		ExpiresAt:            now.Add(time.Minute),
	}
}

func validResultReceipt(t *testing.T, now time.Time) ResultReceipt {
	t.Helper()
	return validResultReceiptForProfile(t, now, ProfileLocalFast)
}

func validResultReceiptForProfile(t *testing.T, now time.Time, profile Profile) ResultReceipt {
	t.Helper()
	plan, err := BuildGatePlan(profile, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	image := validImageIdentity()
	image.PlatformManifestDigest = shardTestDigest('a')
	image.ConfigDigest = shardTestDigest('b')
	set, err := BuildContainerShardSet(plan, image.PlatformManifestDigest, image.ConfigDigest)
	if err != nil {
		t.Fatal(err)
	}
	shards := successfulShardReceipts(t, set)
	retimeSuccessfulShardReceipts(shards, now)
	aggregated, err := aggregateFixtureShardResults(set, shards, now)
	if err != nil {
		t.Fatal(err)
	}
	container, err := aggregateResultReceiptContainer(shards)
	if err != nil {
		t.Fatal(err)
	}
	return ResultReceipt{
		SchemaVersion:  ResultReceiptSchemaVersion,
		ReceiptID:      "receipt-1",
		RepoID:         "repo-1",
		InvocationID:   "invocation-1",
		Entrypoint:     CIEntrypointManualCLI,
		AuthorityOwner: CIEntrypointOwnerManualCLI,
		Source:         plan.Source,
		PlanDigest:     plan.PlanDigest,
		PolicyDigest:   plan.PolicyDigest,
		Runner:         validTrustedRunnerIdentity(),
		Image:          image,
		Generation:     1,
		StartedAt:      now,
		CompletedAt:    now.Add(time.Minute),
		Deadline:       now.Add(10 * time.Minute),
		Status:         ResultStatusPassed,
		GateResults:    fixtureGateResults(aggregated),
		ShardReceipts:  shards,
		Evidence:       []Evidence{{Kind: EvidenceKindProcess, Digest: testDigest}},
		Container:      container,
		Signer:         validTrustedRunnerIdentity().Signer,
		Signature:      "signature",
	}
}

func retimeSuccessfulShardReceipts(receipts []ContainerShardReceipt, now time.Time) {
	for shardIndex := range receipts {
		receipts[shardIndex].StartedAt = now
		receipts[shardIndex].ExitedAt = now.Add(30 * time.Second)
		receipts[shardIndex].CompletedAt = now.Add(time.Minute)
		receipts[shardIndex].Deadline = now.Add(10 * time.Minute)
		for gateIndex := range receipts[shardIndex].GateExecutions {
			receipts[shardIndex].GateExecutions[gateIndex].StartedAt = now
			receipts[shardIndex].GateExecutions[gateIndex].CompletedAt = now.Add(time.Second)
		}
	}
}

func aggregateFixtureShardResults(set ContainerShardSet, receipts []ContainerShardReceipt, now time.Time) ([]PlanGateExecution, error) {
	calls := 0
	clock := func() time.Time {
		calls++
		return now.Add(2*time.Minute + time.Duration(calls-1)*time.Nanosecond)
	}
	return aggregateContainerShardsWithClock(set, receipts, clock)
}

func fixtureGateResults(executions []PlanGateExecution) []GateResult {
	results := make([]GateResult, len(executions))
	for index, execution := range executions {
		results[index] = GateResult{
			GateID: string(execution.GateID), Status: GateStatusPassed, ExitCode: execution.ExitCode,
			StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt,
			ArgvDigest: execution.ArgvDigest, LogDigest: execution.LogDigest,
		}
	}
	return results
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
