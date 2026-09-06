package orchestrationcommand

import (
	"errors"
	"fmt"
	"time"
)

const (
	SendMessageProfileKind       = "send_message_v1"
	AccessGrantAuthorizationKind = "access_grant_allow_v1"
	ActiveBindingSelectorKind    = "active_binding"
	SendMessageDigestDomain      = "orch.command.send-message-profile/v1"
	HMACSHA256DigestAlgorithm    = "hmac-sha256"
)

var (
	ErrInvalidProfile            = errors.New("command intent profile is invalid")
	ErrIncompleteIntent          = errors.New("command intent profile is missing")
	ErrLegacyContentProofMissing = errors.New("legacy command profile has no CONTENT owner proof")
	ErrProfileConflict           = errors.New("command intent profile conflicts with the original record")
	ErrProfileRelation           = errors.New("command intent profile relation is invalid")
	ErrBindingNotActive          = errors.New("command intent profile binding is not active")
	ErrBindingFence              = errors.New("command intent profile binding fence does not match current identity")
)

// CallerSnapshot 保存 transport owner 已验证的非秘密调用者上下文；principal/scope 在基础 Intent 中。
type CallerSnapshot struct {
	Profile     string    `json:"profile"`
	AuthContext string    `json:"auth_context"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// AuthorizationDecision 保存 access grant owner 的一次允许判决；它不是 provider permit。
type AuthorizationDecision struct {
	Kind               string    `json:"kind"`
	Decision           string    `json:"decision"`
	RequestVersion     int64     `json:"request_version"`
	RequestID          string    `json:"request_id"`
	RequestDigest      string    `json:"request_digest"`
	GrantID            string    `json:"grant_id"`
	GrantRevision      int64     `json:"grant_revision"`
	Capability         string    `json:"capability"`
	RequestedAt        time.Time `json:"requested_at"`
	DecidedAt          time.Time `json:"decided_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	GrantRequestDigest string    `json:"grant_request_digest"`
	GrantIssuedAt      time.Time `json:"grant_issued_at"`
	GrantUpdatedAt     time.Time `json:"grant_updated_at"`
}

// ActiveBindingSelector 是 ID-1 current binding 的完整逐字段快照，不接受裸 agent/thread ID。
type ActiveBindingSelector struct {
	Version                 int64     `json:"version"`
	Kind                    string    `json:"kind"`
	AgentID                 string    `json:"agent_id"`
	IdentityKind            string    `json:"identity_kind"`
	ReservationID           string    `json:"reservation_id"`
	ReservationGeneration   int64     `json:"reservation_generation"`
	IncarnationID           string    `json:"incarnation_id"`
	IncarnationGeneration   int64     `json:"incarnation_generation"`
	ExternalIdentityRef     string    `json:"external_identity_ref"`
	BindingVersion          int64     `json:"binding_version"`
	BindingDigest           string    `json:"binding_digest"`
	FenceRef                string    `json:"fence_ref"`
	ActiveProcessReceiptRef string    `json:"active_process_receipt_ref"`
	State                   string    `json:"state"`
	BindingSchemaVersion    int64     `json:"binding_schema_version"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// SendMessageProfile 只表示 send_message 的完整身份/授权 profile；它不表示 command accepted。
type SendMessageProfile struct {
	Kind                 string                `json:"kind"`
	Version              int64                 `json:"version"`
	Caller               CallerSnapshot        `json:"caller"`
	Authorization        AuthorizationDecision `json:"authorization"`
	Source               ActiveBindingSelector `json:"source"`
	Target               ActiveBindingSelector `json:"target"`
	CompleteIntentDigest string                `json:"complete_intent_digest"`
	DigestDomain         string                `json:"digest_domain"`
	DigestAlgorithm      string                `json:"digest_algorithm"`
	DigestKeyID          string                `json:"digest_key_id"`
	DigestKeyVersion     int64                 `json:"digest_key_version"`
}

// CompleteIntentRecord 是基础 intent 与一对一 send_message profile 的只读组合，不含 pending/accepted 状态。
type CompleteIntentRecord struct {
	Command       Record             `json:"command"`
	Profile       SendMessageProfile `json:"profile"`
	SchemaVersion int64              `json:"schema_version"`
	RecordedAt    time.Time          `json:"recorded_at"`
}

// Validate 校验 send_message profile 的固定判别联合、时间顺序、双端 binding 与基础 intent 绑定。
func (p SendMessageProfile) Validate(intent Intent) error {
	if err := intent.Validate(); err != nil {
		return err
	}
	if err := p.validateDiscriminator(intent); err != nil {
		return err
	}
	if err := p.validateSnapshots(); err != nil {
		return err
	}
	if err := p.validateRelations(intent); err != nil {
		return err
	}
	return p.validateDigestBinding(intent)
}

func (p SendMessageProfile) validateDiscriminator(intent Intent) error {
	if intent.CommandKind != "send_message" || p.Kind != SendMessageProfileKind || p.Version != 1 {
		return fmt.Errorf("%w: send_message profile discriminator is unsupported", ErrInvalidProfile)
	}
	return nil
}

func (p SendMessageProfile) validateSnapshots() error {
	if err := p.Caller.validate(p.Authorization.DecidedAt); err != nil {
		return err
	}
	if err := p.Authorization.validate(); err != nil {
		return err
	}
	if err := p.Source.validate("source"); err != nil {
		return err
	}
	if err := p.Target.validate("target"); err != nil {
		return err
	}
	return nil
}

func (p SendMessageProfile) validateRelations(intent Intent) error {
	if p.Source.AgentID == p.Target.AgentID {
		return fmt.Errorf("%w: source and target agents must differ", ErrInvalidProfile)
	}
	if intent.SourceFenceRef != p.Source.FenceRef || intent.TargetFenceRef != p.Target.FenceRef {
		return fmt.Errorf("%w: base intent fence refs do not match selectors", ErrProfileRelation)
	}
	return nil
}

func (c CallerSnapshot) validate(decidedAt time.Time) error {
	if err := validateProfileText("caller_profile", c.Profile, 512); err != nil {
		return err
	}
	if err := validateProfileText("caller_auth_context", c.AuthContext, 512); err != nil {
		return err
	}
	if !validUTCTime(c.ExpiresAt) {
		return fmt.Errorf("%w: caller expiry must be non-zero UTC", ErrInvalidProfile)
	}
	if !validUTCTime(decidedAt) || !c.ExpiresAt.After(decidedAt) {
		return fmt.Errorf("%w: caller expiry must be UTC and after authorization", ErrInvalidProfile)
	}
	return nil
}

func (a AuthorizationDecision) validate() error {
	if err := a.validateDiscriminator(); err != nil {
		return err
	}
	if err := a.validateIdentifiers(); err != nil {
		return err
	}
	if err := validateAuthorizationDigests(a); err != nil {
		return err
	}
	return a.validateTimes()
}

func (a AuthorizationDecision) validateDiscriminator() error {
	if a.Kind != AccessGrantAuthorizationKind || a.Decision != "allowed" {
		return fmt.Errorf("%w: authorization discriminator is unsupported", ErrInvalidProfile)
	}
	if a.Capability != "send" || a.RequestVersion != 1 {
		return fmt.Errorf("%w: authorization capability or version is unsupported", ErrInvalidProfile)
	}
	return nil
}

func (a AuthorizationDecision) validateIdentifiers() error {
	for _, item := range []struct{ name, value string }{
		{"authorization_request_id", a.RequestID}, {"authorization_grant_id", a.GrantID},
	} {
		if err := validateProfileText(item.name, item.value, 128); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthorizationDigests(a AuthorizationDecision) error {
	if err := validateDigest("authorization_request_digest", a.RequestDigest); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	if err := validateDigest("grant_request_digest", a.GrantRequestDigest); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

func (a AuthorizationDecision) validateTimes() error {
	if a.GrantRevision <= 0 {
		return fmt.Errorf("%w: authorization grant revision is invalid", ErrInvalidProfile)
	}
	if !authorizationTimesAreUTC(a) || !authorizationTimesAreOrdered(a) {
		return fmt.Errorf("%w: authorization revision or time range is invalid", ErrInvalidProfile)
	}
	return nil
}

func authorizationTimesAreUTC(a AuthorizationDecision) bool {
	for _, value := range []time.Time{a.GrantIssuedAt, a.RequestedAt, a.DecidedAt, a.ExpiresAt, a.GrantUpdatedAt} {
		if !validUTCTime(value) {
			return false
		}
	}
	return true
}

func authorizationTimesAreOrdered(a AuthorizationDecision) bool {
	if a.RequestedAt.Before(a.GrantIssuedAt) || a.DecidedAt.Before(a.RequestedAt) {
		return false
	}
	if !a.ExpiresAt.After(a.DecidedAt) || a.GrantUpdatedAt.Before(a.GrantIssuedAt) {
		return false
	}
	return true
}

func (s ActiveBindingSelector) validate(prefix string) error {
	if err := s.validateDiscriminator(prefix); err != nil {
		return err
	}
	if err := s.validateTextFields(prefix); err != nil {
		return err
	}
	if s.ReservationGeneration <= 0 || s.IncarnationGeneration <= 0 || s.BindingVersion <= 0 {
		return fmt.Errorf("%w: %s selector generation is invalid", ErrInvalidProfile, prefix)
	}
	if s.BindingSchemaVersion != 1 || !validUTCTime(s.UpdatedAt) {
		return fmt.Errorf("%w: %s binding version or timestamp is invalid", ErrInvalidProfile, prefix)
	}
	if err := validateDigest(prefix+"_binding_digest", s.BindingDigest); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

func (s ActiveBindingSelector) validateDiscriminator(prefix string) error {
	if s.Version != 1 || s.Kind != ActiveBindingSelectorKind {
		return fmt.Errorf("%w: %s selector discriminator is invalid", ErrInvalidProfile, prefix)
	}
	if !validIdentityKind(s.IdentityKind) || s.State != "active" {
		return fmt.Errorf("%w: %s binding discriminator is invalid", ErrInvalidProfile, prefix)
	}
	return nil
}

func (s ActiveBindingSelector) validateTextFields(prefix string) error {
	for _, item := range []struct{ name, value string }{
		{prefix + "_agent_id", s.AgentID}, {prefix + "_reservation_id", s.ReservationID},
		{prefix + "_incarnation_id", s.IncarnationID}, {prefix + "_external_identity_ref", s.ExternalIdentityRef},
		{prefix + "_fence_ref", s.FenceRef}, {prefix + "_active_process_receipt_ref", s.ActiveProcessReceiptRef},
	} {
		limit := 128
		if item.name == prefix+"_fence_ref" || item.name == prefix+"_external_identity_ref" || item.name == prefix+"_active_process_receipt_ref" {
			limit = 512
		}
		if err := validateProfileText(item.name, item.value, limit); err != nil {
			return err
		}
	}
	return nil
}

func (p SendMessageProfile) validateDigestBinding(intent Intent) error {
	if p.CompleteIntentDigest != intent.RequestDigest || p.DigestKeyID != intent.DigestKeyID {
		return fmt.Errorf("%w: complete digest does not match base intent", ErrProfileRelation)
	}
	if p.DigestDomain != SendMessageDigestDomain || p.DigestAlgorithm != HMACSHA256DigestAlgorithm || p.DigestKeyVersion <= 0 {
		return fmt.Errorf("%w: digest provenance is unsupported", ErrInvalidProfile)
	}
	if err := validateDigest("complete_intent_digest", p.CompleteIntentDigest); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

func validateProfileText(name, value string, limit int) error {
	if err := validateText(name, value, limit); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

func validUTCTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func validIdentityKind(kind string) bool {
	switch kind {
	case "provider_session", "native_subagent", "managed_agent", "a2a_remote_agent":
		return true
	default:
		return false
	}
}
