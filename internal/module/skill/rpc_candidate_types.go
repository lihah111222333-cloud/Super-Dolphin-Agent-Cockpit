package skill

// JSON-RPC parameter / result types for the P0b Step 5 candidate review
// gate. These shapes live next to the rest of the RPC type definitions
// (rpc_skill_types.go) so the wire contract is reviewable in one place,
// but they are intentionally kept decoupled from the service-level
// ApproveCandidateParams so the Service interface can evolve without
// breaking the RPC schema.

// approveCandidateRPCParams accepts an optional cwd. The Step 5 brief
// states "cwd from caller ctx"; in practice the JSON-RPC layer has no
// implicit cwd, so we let the host pass it through and scopedSkillContext
// injects it into ctx before delegating to Service.ApproveCandidate.
// CreateSkill (which the approve flow invokes internally) still enforces
// ErrMissingCWD if cwd is absent.
type approveCandidateRPCParams struct {
	CandidateID int64  `json:"candidate_id"`
	ApprovedBy  string `json:"approved_by"`
	Reason      string `json:"reason,omitempty"`
	CWD         string `json:"cwd,omitempty"`
}

type rejectCandidateRPCParams struct {
	CandidateID int64  `json:"candidate_id"`
	Reason      string `json:"reason,omitempty"`
}

type listPendingCandidatesRPCParams struct {
	Limit  int32 `json:"limit,omitempty"`
	Offset int32 `json:"offset,omitempty"`
}

type listPendingCandidatesRPCResult struct {
	Candidates []Candidate `json:"candidates"`
}
