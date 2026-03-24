package mcp

// Standard protocol error codes.
const (
	ErrCodeInternal            = -32603
	ErrCodeInvalidParams       = -32602
	ErrCodeLeaseNotFound       = 4101
	ErrCodeLeaseStale          = 4102
	ErrCodeCapabilityMismatch  = 4103
	ErrCodeScopeNotAllowed     = 4104
	ErrCodeApprovalUnavailable = 4105
	ErrCodePersistFailed       = 4106
	ErrCodePeerUnavailable     = 4107
	ErrCodeAuthFailed          = 4108
	ErrCodeBusy                = 4109
	ErrCodeTimeout             = 4110

	// Deprecated aliases kept temporarily so downstream packages can migrate in
	// parallel without immediately breaking on constant renames.
	ErrCodeInvalidLease    = ErrCodeLeaseNotFound
	ErrCodeStaleLease      = ErrCodeLeaseStale
	ErrCodeUnknownScope    = ErrCodeScopeNotAllowed
	ErrCodeApprovalDenied  = ErrCodeApprovalUnavailable
	ErrCodeApprovalTimeout = ErrCodeTimeout
	ErrCodeReportConflict  = ErrCodePersistFailed
	ErrCodeNotRegistered   = ErrCodeLeaseNotFound
)
