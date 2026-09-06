package admissionproof

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

// Gate 在不产生外部effect的情况下，把两个owner proof收口成custody submission request。
type Gate struct {
	config           Config
	intentVerifier   orchestrationcommand.SendProfileDigestVerifier
	contentResolver  ContentResolver
	contentVerifier  ContentVerifier
	resourceResolver ResourceResolver
	resourceVerifier ResourceVerifier
}

// NewGate 构造proof gate；缺配置或typed-nil owner依赖均立即失败。
func NewGate(config Config, intentVerifier orchestrationcommand.SendProfileDigestVerifier, contentResolver ContentResolver, contentVerifier ContentVerifier, resourceResolver ResourceResolver, resourceVerifier ResourceVerifier) (*Gate, error) {
	if err := validateGateConfig(config); err != nil {
		return nil, err
	}
	for _, dependency := range []struct {
		name  string
		value any
	}{
		{"intent verifier", intentVerifier}, {"content resolver", contentResolver},
		{"content verifier", contentVerifier}, {"resource resolver", resourceResolver},
		{"resource verifier", resourceVerifier},
	} {
		if nilProofDependency(dependency.value) {
			return nil, fmt.Errorf("%s is required", dependency.name)
		}
	}
	return &Gate{config: config, intentVerifier: intentVerifier, contentResolver: contentResolver,
		contentVerifier: contentVerifier, resourceResolver: resourceResolver, resourceVerifier: resourceVerifier}, nil
}

// BuildSubmission 重新验证profile与两个owner proof，只构造non-authorizing custody request。
func (g *Gate) BuildSubmission(ctx context.Context, record orchestrationcommand.CompleteIntentRecord) (recoverycustody.SubmissionRequest, error) {
	if err := g.ready(ctx); err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	if err := validateCompleteIntentRecord(record); err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	if err := g.intentVerifier.VerifySendProfileDigest(ctx, record.Command.Intent, record.Profile); err != nil {
		return recoverycustody.SubmissionRequest{}, fmt.Errorf("verify command profile: %w", err)
	}
	authorizationDigest, identityDigest, err := computeProfileProofDigests(record.Profile)
	if err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	content, err := g.resolveContent(ctx, record)
	if err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	resource, err := g.resolveResource(ctx, record, identityDigest, content.SnapshotDigest)
	if err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	return buildCustodyRequest(g.config, record, authorizationDigest, identityDigest, content.SnapshotDigest, resource.SnapshotDigest)
}

func (g *Gate) resolveContent(ctx context.Context, record orchestrationcommand.CompleteIntentRecord) (ContentSnapshot, error) {
	key := contentResolveKey(record)
	snapshot, err := g.contentResolver.ResolveContentSnapshot(ctx, key)
	if err != nil {
		return ContentSnapshot{}, err
	}
	if err := snapshot.validate(); err != nil {
		return ContentSnapshot{}, err
	}
	if !contentMatchesRecord(key, snapshot, record) {
		return ContentSnapshot{}, ErrContentSnapshotMismatch
	}
	if err := g.contentVerifier.VerifyContentSnapshot(ctx, key, snapshot); err != nil {
		return ContentSnapshot{}, fmt.Errorf("%w: %w", ErrContentVerificationFailed, err)
	}
	return snapshot, nil
}

func (g *Gate) resolveResource(ctx context.Context, record orchestrationcommand.CompleteIntentRecord, identityDigest, contentDigest string) (ResourceSnapshot, error) {
	key := resourceResolveKey(record, identityDigest, contentDigest)
	snapshot, err := g.resourceResolver.ResolveResourceSnapshot(ctx, key)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	if err := snapshot.validate(); err != nil {
		return ResourceSnapshot{}, err
	}
	if !resourceMatchesRecord(key, snapshot, record) {
		return ResourceSnapshot{}, ErrResourceSnapshotMismatch
	}
	if err := g.resourceVerifier.VerifyResourceSnapshot(ctx, key, snapshot); err != nil {
		return ResourceSnapshot{}, fmt.Errorf("%w: %w", ErrResourceVerificationFailed, err)
	}
	return snapshot, nil
}

// ready 校验gate的五个owner/verifier依赖和调用context。
func (g *Gate) ready(ctx context.Context) error {
	if g == nil || nilProofDependency(g.intentVerifier) || nilProofDependency(g.contentResolver) || nilProofDependency(g.contentVerifier) || nilProofDependency(g.resourceResolver) || nilProofDependency(g.resourceVerifier) {
		return errors.New("admission proof gate is not initialized")
	}
	return ctx.Err()
}

func validateGateConfig(config Config) error {
	if err := validateProofText(ErrInvalidConfig, "security_domain_id", config.SecurityDomainID, 256); err != nil {
		return err
	}
	return validateProofText(ErrInvalidConfig, "authority_profile", config.AuthorityProfile, 128)
}

// validateCompleteIntentRecord 拒绝非send、坏版本、缺时间或profile关系损坏的记录。
func validateCompleteIntentRecord(record orchestrationcommand.CompleteIntentRecord) error {
	if record.SchemaVersion != 1 || record.Command.SchemaVersion != 1 || record.Command.CommandVersion != 1 {
		return ErrInvalidCommandRecord
	}
	if record.RecordedAt.IsZero() || record.Command.RecordedAt.IsZero() || record.Command.Intent.CommandKind != "send_message" {
		return ErrInvalidCommandRecord
	}
	if err := record.Profile.Validate(record.Command.Intent); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCommandRecord, err)
	}
	return nil
}

func computeProfileProofDigests(profile orchestrationcommand.SendMessageProfile) (string, string, error) {
	authorizationDigest, err := computeAuthorizationDigest(profile.Authorization)
	if err != nil {
		return "", "", err
	}
	identityDigest, err := computeIdentityDigest(profile.Source, profile.Target)
	if err != nil {
		return "", "", err
	}
	return authorizationDigest, identityDigest, nil
}

func nilProofDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
