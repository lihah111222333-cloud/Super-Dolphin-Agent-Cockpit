package mcpcontrol

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestManagedRegisterRealNormalizerPreservesEveryRequestField(t *testing.T) {
	resume := uint64(3)
	baseline := managedFieldGuardRegisterRequest(resume)
	archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(t, baseline, func(input dto.RegisterRequest) map[string]any {
		normalized, err := normalizeManagedRegisterRequest(input)
		return map[string]any{
			"error":      managedTestErrorString(err),
			"normalized": normalized,
		}
	}, nil, managedNormalizerProjections())
}

func TestManagedRegisterRealFingerprintConsumesEveryNormalizedField(t *testing.T) {
	resume := uint64(3)
	baseline, err := normalizeManagedRegisterRequest(managedFieldGuardRegisterRequest(resume))
	if err != nil {
		t.Fatalf("normalizeManagedRegisterRequest() error = %v", err)
	}
	archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(t, baseline, func(input dto.RegisterRequest) map[string]any {
		return map[string]any{
			"fingerprint": fmt.Sprintf("%x", managedRegisterFingerprint(input)),
		}
	}, nil, managedFingerprintProjections())
}

func TestManagedRegisterRequiredCapabilityLegalMutationAffectsNormalizerAndFingerprint(t *testing.T) {
	resume := uint64(3)
	baseline, err := normalizeManagedRegisterRequest(managedFieldGuardRegisterRequest(resume))
	if err != nil {
		t.Fatalf("normalizeManagedRegisterRequest(baseline) error = %v", err)
	}
	mutatedInput := managedFieldGuardRegisterRequest(resume)
	mutatedInput.CapabilitiesRequired = nil
	mutated, err := normalizeManagedRegisterRequest(mutatedInput)
	if err != nil {
		t.Fatalf("normalizeManagedRegisterRequest(mutated) error = %v", err)
	}
	if fmt.Sprint(baseline.CapabilitiesOffered) != fmt.Sprint(mutated.CapabilitiesOffered) {
		t.Fatalf("offered capabilities changed: baseline=%v mutated=%v", baseline.CapabilitiesOffered, mutated.CapabilitiesOffered)
	}
	if fmt.Sprint(baseline.CapabilitiesRequired) == fmt.Sprint(mutated.CapabilitiesRequired) {
		t.Fatal("normalizer output ignored legal required-capability mutation")
	}
	if managedRegisterFingerprint(baseline) == managedRegisterFingerprint(mutated) {
		t.Fatal("fingerprint ignored legal required-capability mutation")
	}
}

func managedFieldGuardRegisterRequest(resume uint64) dto.RegisterRequest {
	return dto.RegisterRequest{
		InstanceID:           managedOrchInstanceID,
		BinaryName:           "mcp-orch",
		PID:                  100,
		SessionToken:         "session-token",
		BootID:               "boot-1",
		ClientKind:           dto.ClientKindOrch,
		PeerKind:             dto.PeerKindSharedService,
		Shared:               true,
		CapabilitiesOffered:  []string{"tools/task"},
		CapabilitiesRequired: []string{"tools/task"},
		Subscriptions:        []string{"config/agent"},
		ResumeFromGeneration: &resume,
		ManagedAuthority: &dto.ManagedAuthorityProof{
			ProtocolVersion: dto.ManagedAuthorityProtocolVersion,
			RequestID:       "request-1",
			Token:           "token-1",
		},
	}
}

func TestManagedRegisterRealNormalizerCanonicalizesFields(t *testing.T) {
	resume := uint64(3)
	input := managedFieldGuardRegisterRequest(resume)
	input.InstanceID = " " + input.InstanceID + " "
	input.BinaryName = " mcp-orch "
	input.BootID = " boot-1 "
	input.ClientKind = " orch "
	input.PeerKind = " shared-service "
	input.CapabilitiesOffered = []string{" tools/task ", "tools/task"}
	input.CapabilitiesRequired = []string{" tools/task ", "tools/task"}
	input.Subscriptions = []string{" config/agent ", "config/agent"}
	input.ManagedAuthority.ProtocolVersion = " " + dto.ManagedAuthorityProtocolVersion + " "
	input.ManagedAuthority.RequestID = " request-1 "

	normalized, err := normalizeManagedRegisterRequest(input)
	if err != nil {
		t.Fatalf("normalizeManagedRegisterRequest() error = %v", err)
	}
	assertManagedNormalizedIdentity(t, normalized)
	assertManagedNormalizedCollections(t, normalized)
	assertManagedNormalizedProof(t, normalized)
}

func assertManagedNormalizedIdentity(t *testing.T, normalized dto.RegisterRequest) {
	t.Helper()
	if normalized.InstanceID != managedOrchInstanceID ||
		normalized.BinaryName != "mcp-orch" ||
		normalized.BootID != "boot-1" ||
		normalized.ClientKind != dto.ClientKindOrch ||
		normalized.PeerKind != dto.PeerKindSharedService {
		t.Fatalf("normalized identity = %+v", normalized)
	}
}

func assertManagedNormalizedCollections(t *testing.T, normalized dto.RegisterRequest) {
	t.Helper()
	if fmt.Sprint(normalized.CapabilitiesOffered) != "[tools/task]" ||
		fmt.Sprint(normalized.CapabilitiesRequired) != "[tools/task]" ||
		fmt.Sprint(normalized.Subscriptions) != "[config/agent]" {
		t.Fatalf("normalized collections = %+v", normalized)
	}
}

func assertManagedNormalizedProof(t *testing.T, normalized dto.RegisterRequest) {
	t.Helper()
	if normalized.ManagedAuthority.ProtocolVersion != dto.ManagedAuthorityProtocolVersion ||
		normalized.ManagedAuthority.RequestID != "request-1" {
		t.Fatalf("normalized proof = %+v", normalized.ManagedAuthority)
	}
}

func TestManagedAuthorityProofRealValidatorConsumesEveryField(t *testing.T) {
	baseline := dto.ManagedAuthorityProof{
		ProtocolVersion: dto.ManagedAuthorityProtocolVersion,
		RequestID:       "request-1",
		Token:           "token-1",
	}
	archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(t, baseline, func(input dto.ManagedAuthorityProof) map[string]any {
		return map[string]any{
			"fingerprint": fmt.Sprintf("%x", managedAuthorityProofFingerprint(input)),
		}
	}, nil, managedAuthorityProofFingerprintProjections())
}

func managedNormalizerProjections() []archtest.WireDTOMapperProjection {
	return managedMapperProjections("normalized", managedNormalizedOutput,
		"instance_id", "binary_name", "agent_id", "thread_id", "pid", "session_token", "boot_id", "client_kind", "peer_kind", "shared",
		"capabilities_offered", "capabilities_required", "subscriptions", "resume_from_generation", "managed_authority",
	)
}

func managedFingerprintProjections() []archtest.WireDTOMapperProjection {
	return managedMapperProjections("fingerprint", managedRegisterFingerprintOutput,
		"instance_id", "binary_name", "agent_id", "thread_id", "pid", "session_token", "boot_id", "client_kind", "peer_kind", "shared",
		"capabilities_offered", "capabilities_required", "subscriptions", "resume_from_generation", "managed_authority",
	)
}

func managedAuthorityProofFingerprintProjections() []archtest.WireDTOMapperProjection {
	return managedMapperProjections("fingerprint", managedAuthorityProofFingerprintOutput, "protocol_version", "request_id", "token")
}

func managedMapperProjections(consumerKey string, expectedOutput func(any) map[string]any, fields ...string) []archtest.WireDTOMapperProjection {
	projections := make([]archtest.WireDTOMapperProjection, 0, len(fields))
	for _, field := range fields {
		projections = append(projections, archtest.WireDTOMapperProjection{Field: field, ConsumerKey: consumerKey, ExpectedOutput: expectedOutput})
	}
	return projections
}

func managedNormalizedOutput(input any) map[string]any {
	normalized, err := normalizeManagedRegisterRequest(input.(dto.RegisterRequest))
	return map[string]any{"error": managedTestErrorString(err), "normalized": normalized}
}

func managedRegisterFingerprintOutput(input any) map[string]any {
	return map[string]any{"fingerprint": fmt.Sprintf("%x", managedRegisterFingerprint(input.(dto.RegisterRequest)))}
}

func managedAuthorityProofFingerprintOutput(input any) map[string]any {
	return map[string]any{"fingerprint": fmt.Sprintf("%x", managedAuthorityProofFingerprint(input.(dto.ManagedAuthorityProof)))}
}

func managedTestErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestManagedRegisterSameRequestRejectsMutatedNormalizedPayload(t *testing.T) {
	mutations := []struct {
		name string
		edit func(*dto.RegisterRequest)
	}{
		{name: "pid", edit: func(req *dto.RegisterRequest) { req.PID++ }},
		{name: "session token", edit: func(req *dto.RegisterRequest) { req.SessionToken += "-other" }},
		{name: "capabilities", edit: func(req *dto.RegisterRequest) {
			req.CapabilitiesOffered = append(req.CapabilitiesOffered, "tools/workspace")
		}},
		{name: "required capabilities", edit: func(req *dto.RegisterRequest) {
			req.CapabilitiesOffered = []string{"tools/task"}
			req.CapabilitiesRequired = []string{"tools/task"}
		}},
		{name: "subscriptions", edit: func(req *dto.RegisterRequest) {
			req.Subscriptions = append(req.Subscriptions, "config/thread")
		}},
		{name: "resume generation", edit: func(req *dto.RegisterRequest) {
			resume := uint64(1)
			req.ResumeFromGeneration = &resume
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
			bootstrap, err := registry.IssueManagedAuthority(
				context.Background(),
				dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"},
			)
			if err != nil {
				t.Fatalf("IssueManagedAuthority() error = %v", err)
			}
			original := managedRegisterRequest(bootstrap, "request-1")
			first := requireManagedRegister(t, registry, original, "first")
			mutated := original
			mutation.edit(&mutated)
			if _, err := callManagedRegister(t, registry, mutated); err == nil ||
				!strings.Contains(err.Error(), "conflicts with original payload") {
				t.Fatalf("Register(mutated same request) error = %v, want payload conflict", err)
			}
			retry := requireManagedRegister(t, registry, original, "original after conflict")
			if retry.Generation != first.Generation {
				t.Fatalf("original retry generation = %d, want %d", retry.Generation, first.Generation)
			}
		})
	}
}

func TestManagedRegisterReplayRebindsConnectionWithoutOldConnectionAuthority(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(
		context.Background(),
		dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"},
	)
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	request := managedRegisterRequest(bootstrap, "request-1")
	firstPeer := newManagedRegistryLocal(t, registry)
	var first dto.RegisterResponse
	if err := firstPeer.Client.CallResult(context.Background(), dto.MethodRegister, request, &first); err != nil {
		t.Fatalf("first connection Register() error = %v", err)
	}
	pin := pinManagedInstance(t, registry, dto.ClientKindOrch)
	secondPeer := newManagedRegistryLocal(t, registry)
	var replay dto.RegisterResponse
	if err := secondPeer.Client.CallResult(context.Background(), dto.MethodRegister, request, &replay); err != nil {
		t.Fatalf("second connection replay Register() error = %v", err)
	}
	if replay.Generation != first.Generation {
		t.Fatalf("replay generation = %d, want %d", replay.Generation, first.Generation)
	}
	if pin.Current() {
		t.Fatal("first connection pin remained current after replay replacement")
	}
	heartbeat := dto.HeartbeatRequest{InstanceID: first.InstanceID, Generation: first.Generation}
	var heartbeatResponse dto.HeartbeatResponse
	if err := firstPeer.Client.CallResult(
		context.Background(),
		dto.MethodHeartbeat,
		heartbeat,
		&heartbeatResponse,
	); err == nil {
		t.Fatal("old connection Heartbeat() error = nil")
	}
	if err := secondPeer.Client.CallResult(
		context.Background(),
		dto.MethodHeartbeat,
		heartbeat,
		&heartbeatResponse,
	); err != nil {
		t.Fatalf("replacement connection Heartbeat() error = %v", err)
	}
	heartbeat.Status = dto.StatusDisconnected
	if err := firstPeer.Client.CallResult(
		context.Background(),
		dto.MethodHeartbeat,
		heartbeat,
		&heartbeatResponse,
	); err == nil {
		t.Fatal("old connection disconnected Heartbeat() error = nil")
	}
	if _, ok := registry.GetInstance(first.LeaseKey()); !ok {
		t.Fatal("old connection disconnected current replacement")
	}
	heartbeat.Status = dto.StatusActive
	requireOldManagedReplayRejected(t, firstPeer, secondPeer, request, heartbeat)
}

func TestManagedReceiptAdoptionFenceRejectsDelayedOldConnection(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(
		context.Background(),
		dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"},
	)
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	request, err := normalizeManagedRegisterRequest(managedRegisterRequest(bootstrap, "request-1"))
	if err != nil {
		t.Fatalf("normalizeManagedRegisterRequest() error = %v", err)
	}
	firstPeer := jrpcPeer{server: new(jrpc2.Server)}
	secondPeer := jrpcPeer{server: new(jrpc2.Server)}
	first := requireManagedAuthorityConsume(t, registry, request, firstPeer, false)
	_ = requireManagedAuthorityConsume(t, registry, request, firstPeer, true)
	second := requireManagedAuthorityConsume(t, registry, request, secondPeer, true)
	registry.authorityMu.Lock()
	secondErr := registry.validateManagedInstallPeerLocked(second, secondPeer)
	firstErr := registry.validateManagedInstallPeerLocked(first, firstPeer)
	registry.authorityMu.Unlock()
	if secondErr != nil {
		t.Fatalf("replacement install fence error = %v", secondErr)
	}
	if firstErr == nil {
		t.Fatal("delayed old connection install fence error = nil")
	}
	if _, _, err := registry.consumeManagedAuthority(request, firstPeer); err == nil {
		t.Fatal("old connection receipt readoption error = nil")
	}
}

func requireManagedAuthorityConsume(
	t *testing.T,
	registry *ToolRegistry,
	request dto.RegisterRequest,
	peer Peer,
	wantReplay bool,
) dto.RegisterResponse {
	t.Helper()
	response, replay, err := registry.consumeManagedAuthority(request, peer)
	if err != nil || replay != wantReplay {
		t.Fatalf("consumeManagedAuthority() replay = %v, error = %v", replay, err)
	}
	return response
}

func requireOldManagedReplayRejected(
	t *testing.T,
	firstPeer jrpcserver.Local,
	secondPeer jrpcserver.Local,
	request dto.RegisterRequest,
	heartbeat dto.HeartbeatRequest,
) {
	t.Helper()
	var staleReplay dto.RegisterResponse
	if err := firstPeer.Client.CallResult(
		context.Background(),
		dto.MethodRegister,
		request,
		&staleReplay,
	); err == nil {
		t.Fatal("old connection replay Register() error = nil")
	}
	var heartbeatResponse dto.HeartbeatResponse
	if err := secondPeer.Client.CallResult(
		context.Background(),
		dto.MethodHeartbeat,
		heartbeat,
		&heartbeatResponse,
	); err != nil {
		t.Fatalf("replacement connection Heartbeat() after stale replay error = %v", err)
	}
}

func pinManagedInstance(t *testing.T, registry *ToolRegistry, kind string) *LeasePin {
	t.Helper()
	active := registry.FindActiveByKind(kind)
	if len(active) != 1 {
		t.Fatalf("active managed instances = %d, want 1", len(active))
	}
	pin, err := active[0].Pin()
	if err != nil {
		t.Fatalf("managed instance Pin() error = %v", err)
	}
	t.Cleanup(func() {
		if err := pin.Release(); err != nil {
			t.Errorf("managed instance pin Release() error = %v", err)
		}
	})
	return pin
}

func newManagedRegistryLocal(t *testing.T, registry *ToolRegistry) jrpcserver.Local {
	t.Helper()
	local := jrpcserver.NewLocal(handler.Map{
		dto.MethodRegister: platformrpc.StrictHandler(
			func(ctx context.Context, request dto.RegisterRequest) (dto.RegisterResponse, error) {
				return registry.Register(ctx, request)
			},
		),
		dto.MethodHeartbeat: platformrpc.StrictHandler(
			func(ctx context.Context, request dto.HeartbeatRequest) (dto.HeartbeatResponse, error) {
				return registry.Heartbeat(ctx, request)
			},
		),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	t.Cleanup(func() { _ = local.Close() })
	return local
}
