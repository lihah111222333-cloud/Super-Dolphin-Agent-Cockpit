package mcpcontrol

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const managedOrchInstanceID = "managed:mcp-orch"

type managedAuthorityState struct {
	claimsHash [sha256.Size]byte
	tokenHash  [sha256.Size]byte
	last       *managedRegisterReceipt
}

type managedRegisterReceipt struct {
	requestID   string
	claimsHash  [sha256.Size]byte
	requestHash [sha256.Size]byte
	tokenHash   [sha256.Size]byte
	response    dto.RegisterResponse
	peer        Peer
	rebound     bool
}

// IssueManagedAuthority 签发仅供 mcp-orch shared-service 启动一次的 bootstrap token。
func (r *ToolRegistry) IssueManagedAuthority(_ context.Context, req dto.ManagedAuthorityIssueRequest) (dto.ManagedAuthorityBootstrap, error) {
	if r == nil || r.generationStore == nil {
		return dto.ManagedAuthorityBootstrap{}, errInvalidParams("managed authority registry is not configured")
	}
	if strings.TrimSpace(req.BinaryName) != "mcp-orch" {
		return dto.ManagedAuthorityBootstrap{}, errInvalidParams("managed authority is reserved for mcp-orch")
	}
	token, err := newManagedToken()
	if err != nil {
		return dto.ManagedAuthorityBootstrap{}, err
	}
	boot := dto.ManagedAuthorityBootstrap{
		InstanceID:      managedOrchInstanceID,
		BootID:          platformshared.NewID("mcp_boot"),
		Token:           token,
		ProtocolVersion: dto.ManagedAuthorityProtocolVersion,
	}
	claims := canonicalManagedClaims(boot.InstanceID, boot.BootID)
	claimsHash, err := managedClaimsHash(claims, json.Marshal)
	if err != nil {
		return dto.ManagedAuthorityBootstrap{}, err
	}
	r.authorityMu.Lock()
	r.managedAuthorities[boot.InstanceID] = &managedAuthorityState{
		claimsHash: claimsHash,
		tokenHash:  sha256.Sum256([]byte(token)),
	}
	r.authorityMu.Unlock()
	return boot, nil
}

// consumeManagedAuthority 先验证固定 claims，再原子消费 token 并缓存可重放的同请求收据。
func (r *ToolRegistry) consumeManagedAuthority(req dto.RegisterRequest, peer Peer) (dto.RegisterResponse, bool, error) {
	if err := validateManagedRegisterShape(req); err != nil {
		return dto.RegisterResponse{}, false, err
	}
	proof := req.ManagedAuthority
	requestID := strings.TrimSpace(proof.RequestID)
	claimsHash, requestHash, err := managedRegisterHashes(req)
	if err != nil {
		return dto.RegisterResponse{}, false, err
	}
	tokenHash := sha256.Sum256([]byte(proof.Token))

	r.authorityMu.Lock()
	defer r.authorityMu.Unlock()

	state := r.managedAuthorities[managedOrchInstanceID]
	if state == nil {
		return dto.RegisterResponse{}, false, errLeaseStale("managed authority is not current")
	}
	if response, replay, err := matchManagedReplay(
		state.last,
		requestID,
		claimsHash,
		requestHash,
		tokenHash,
		peer,
	); replay || err != nil {
		return response, replay, err
	}
	if subtle.ConstantTimeCompare(state.claimsHash[:], claimsHash[:]) != 1 {
		return dto.RegisterResponse{}, false, errInvalidParams("managed authority claims do not match issued bootstrap")
	}
	if subtle.ConstantTimeCompare(state.tokenHash[:], tokenHash[:]) != 1 {
		return dto.RegisterResponse{}, false, errLeaseStale("managed authority token is stale or replayed")
	}
	nextToken, err := newManagedToken()
	if err != nil {
		return dto.RegisterResponse{}, false, err
	}
	generation, err := r.generationStore.Next(req.InstanceID, req.ResumeFromGeneration)
	if err != nil {
		return dto.RegisterResponse{}, false, errInvalidParams("%v", err)
	}
	response := r.registerResponse(req, generation)
	response.ManagedAuthority = &dto.ManagedAuthorityReceipt{
		ProtocolVersion: dto.ManagedAuthorityProtocolVersion,
		RequestID:       requestID,
		NextToken:       nextToken,
	}
	state.last = &managedRegisterReceipt{
		requestID:   requestID,
		claimsHash:  claimsHash,
		requestHash: requestHash,
		tokenHash:   tokenHash,
		response:    cloneManagedRegisterResponse(response),
		peer:        peer,
	}
	state.tokenHash = sha256.Sum256([]byte(nextToken))
	return response, false, nil
}

// matchManagedReplay 仅允许 request、claims、完整 payload 和旧 token 全部一致的收据恢复。
func matchManagedReplay(
	last *managedRegisterReceipt,
	requestID string,
	claimsHash [sha256.Size]byte,
	requestHash [sha256.Size]byte,
	tokenHash [sha256.Size]byte,
	peer Peer,
) (dto.RegisterResponse, bool, error) {
	if last == nil || last.requestID != requestID ||
		!constantTimeHashEqual(last.tokenHash, tokenHash) {
		return dto.RegisterResponse{}, false, nil
	}
	if !constantTimeHashEqual(last.claimsHash, claimsHash) {
		return dto.RegisterResponse{}, false,
			errInvalidParams("managed authority claims do not match issued bootstrap")
	}
	if !constantTimeHashEqual(last.requestHash, requestHash) {
		return dto.RegisterResponse{}, false,
			errInvalidParams("managed authority request_id conflicts with original payload")
	}
	if !samePeerConnection(last.peer, peer) {
		if last.rebound {
			return dto.RegisterResponse{}, false,
				errLeaseStale("managed authority receipt was already adopted by a replacement connection")
		}
		last.peer = peer
		last.rebound = true
	}
	return cloneManagedRegisterResponse(last.response), true, nil
}

// validateManagedRegisterShape 拒绝缺失协议、空 proof 和非保留角色形状。
func validateManagedRegisterShape(req dto.RegisterRequest) error {
	proof := req.ManagedAuthority
	if proof == nil {
		return errInvalidParams("mcp-orch requires managed authority")
	}
	if proof.ProtocolVersion != dto.ManagedAuthorityProtocolVersion {
		return errInvalidParams("managed authority protocol version is required")
	}
	if strings.TrimSpace(proof.RequestID) == "" || strings.TrimSpace(proof.Token) == "" {
		return errInvalidParams("managed authority requires request_id and token")
	}
	return nil
}

func canonicalManagedClaims(instanceID, bootID string) dto.RegisterRequest {
	return dto.RegisterRequest{
		InstanceID: instanceID,
		BootID:     bootID,
		BinaryName: "mcp-orch",
		ClientKind: dto.ClientKindOrch,
		PeerKind:   dto.PeerKindSharedService,
		Shared:     true,
	}
}

// managedRegisterHashes 顺序计算 claims 与完整请求指纹并向注册链传播序列化错误。
func managedRegisterHashes(
	req dto.RegisterRequest,
) ([sha256.Size]byte, [sha256.Size]byte, error) {
	claimsHash, err := managedClaimsHash(req, json.Marshal)
	if err != nil {
		return [sha256.Size]byte{}, [sha256.Size]byte{}, err
	}
	requestHash, err := managedRegisterFingerprint(req, json.Marshal)
	if err != nil {
		return [sha256.Size]byte{}, [sha256.Size]byte{}, err
	}
	return claimsHash, requestHash, nil
}

func managedClaimsHash(
	req dto.RegisterRequest,
	marshal func(any) ([]byte, error),
) ([sha256.Size]byte, error) {
	claims := struct {
		InstanceID string `json:"instance_id"`
		BootID     string `json:"boot_id"`
		BinaryName string `json:"binary_name"`
		AgentID    string `json:"agent_id"`
		ThreadID   string `json:"thread_id"`
		ClientKind string `json:"client_kind"`
		PeerKind   string `json:"peer_kind"`
		Shared     bool   `json:"shared"`
	}{
		InstanceID: req.InstanceID,
		BootID:     req.BootID,
		BinaryName: req.BinaryName,
		AgentID:    req.AgentID,
		ThreadID:   req.ThreadID,
		ClientKind: req.ClientKind,
		PeerKind:   req.PeerKind,
		Shared:     req.Shared,
	}
	raw, err := marshal(claims)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("marshal managed authority claims: %w", err)
	}
	return sha256.Sum256(raw), nil
}

// managedRegisterFingerprint 绑定所有已规范化且会影响注册、租约或协商结果的请求字段。
func managedRegisterFingerprint(
	req dto.RegisterRequest,
	marshal func(any) ([]byte, error),
) ([sha256.Size]byte, error) {
	var resumeGeneration uint64
	var hasResumeGeneration bool
	if req.ResumeFromGeneration != nil {
		resumeGeneration = *req.ResumeFromGeneration
		hasResumeGeneration = true
	}
	var proofHash [sha256.Size]byte
	var hasProof bool
	if req.ManagedAuthority != nil {
		var err error
		proofHash, err = managedAuthorityProofFingerprint(*req.ManagedAuthority, marshal)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		hasProof = true
	}
	payload := struct {
		InstanceID           string            `json:"instance_id"`
		BinaryName           string            `json:"binary_name"`
		AgentID              string            `json:"agent_id"`
		ThreadID             string            `json:"thread_id"`
		PID                  int               `json:"pid"`
		SessionToken         string            `json:"session_token"`
		BootID               string            `json:"boot_id"`
		ClientKind           string            `json:"client_kind"`
		PeerKind             string            `json:"peer_kind"`
		Shared               bool              `json:"shared"`
		CapabilitiesOffered  []string          `json:"capabilities_offered"`
		CapabilitiesRequired []string          `json:"capabilities_required"`
		Subscriptions        []string          `json:"subscriptions"`
		ResumeGeneration     uint64            `json:"resume_generation"`
		HasResumeGeneration  bool              `json:"has_resume_generation"`
		ProofHash            [sha256.Size]byte `json:"proof_hash"`
		HasProof             bool              `json:"has_proof"`
	}{
		InstanceID:           req.InstanceID,
		BinaryName:           req.BinaryName,
		AgentID:              req.AgentID,
		ThreadID:             req.ThreadID,
		PID:                  req.PID,
		SessionToken:         req.SessionToken,
		BootID:               req.BootID,
		ClientKind:           req.ClientKind,
		PeerKind:             req.PeerKind,
		Shared:               req.Shared,
		CapabilitiesOffered:  req.CapabilitiesOffered,
		CapabilitiesRequired: req.CapabilitiesRequired,
		Subscriptions:        req.Subscriptions,
		ResumeGeneration:     resumeGeneration,
		HasResumeGeneration:  hasResumeGeneration,
		ProofHash:            proofHash,
		HasProof:             hasProof,
	}
	raw, err := marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("marshal managed register fingerprint: %w", err)
	}
	return sha256.Sum256(raw), nil
}

// managedAuthorityProofFingerprint 绑定 proof 的版本、幂等请求号和 token。
func managedAuthorityProofFingerprint(
	proof dto.ManagedAuthorityProof,
	marshal func(any) ([]byte, error),
) ([sha256.Size]byte, error) {
	payload := struct {
		ProtocolVersion string `json:"protocol_version"`
		RequestID       string `json:"request_id"`
		Token           string `json:"token"`
	}{
		ProtocolVersion: proof.ProtocolVersion,
		RequestID:       proof.RequestID,
		Token:           proof.Token,
	}
	raw, err := marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("marshal managed authority proof fingerprint: %w", err)
	}
	return sha256.Sum256(raw), nil
}

func constantTimeHashEqual(left, right [sha256.Size]byte) bool {
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func newManagedToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("generate managed authority token: " + err.Error())
	}
	return "sd-managed-" + hex.EncodeToString(raw[:]), nil
}

func cloneManagedRegisterResponse(in dto.RegisterResponse) dto.RegisterResponse {
	out := in
	out.CapabilitiesNegotiated = platformshared.CloneStrings(in.CapabilitiesNegotiated)
	out.CapabilitiesRejected = platformshared.CloneStrings(in.CapabilitiesRejected)
	if in.ManagedAuthority != nil {
		receipt := *in.ManagedAuthority
		out.ManagedAuthority = &receipt
	}
	return out
}
