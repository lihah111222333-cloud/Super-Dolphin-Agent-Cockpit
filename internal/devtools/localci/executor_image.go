package localci

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	labelPolicySHA       = "org.super-dolphin.policy-sha"
	labelSourceTreeSHA   = "org.super-dolphin.source-tree-sha"
	labelInputDigest     = "org.super-dolphin.image-input-digest"
	labelToolchainDigest = "org.super-dolphin.toolchain-digest"
	labelSchemaVersion   = "org.super-dolphin.schema-version"
)

var sha256DigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var gitObjectPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// imageIdentityReader is the narrow caller contract for Task 1A's canonical ImageIdentity.
// 依赖 task-1a：canonical shared type 落地后补充适配器。
type imageIdentityReader interface {
	OCIIndexDigest() string
	PlatformManifestDigest() string
	ConfigDigest() string
	RootFSDiffIDs() []string
	OS() string
	Architecture() string
	Variant() string
}

type expectedImageMetadata struct {
	PolicySHA       string
	SourceTreeSHA   string
	InputDigest     string
	ToolchainDigest string
	SchemaVersion   string
	OS              string
	Architecture    string
	Variant         string
}

func (expected expectedImageMetadata) labels() map[string]string {
	return map[string]string{
		labelPolicySHA:       expected.PolicySHA,
		labelSourceTreeSHA:   expected.SourceTreeSHA,
		labelInputDigest:     expected.InputDigest,
		labelToolchainDigest: expected.ToolchainDigest,
		labelSchemaVersion:   expected.SchemaVersion,
	}
}

// validateImageIdentity 校验不可变 OCI 身份、平台和镜像标签闭包。
func validateImageIdentity(identity imageIdentityReader, labels map[string]string, expected expectedImageMetadata) error {
	if identity == nil {
		return errors.New("image identity is required")
	}
	if err := validateImageDescriptorDigests(identity); err != nil {
		return err
	}
	if err := validateExpectedImageMetadata(expected); err != nil {
		return err
	}
	if identity.OS() != expected.OS || identity.Architecture() != expected.Architecture || identity.Variant() != expected.Variant {
		return fmt.Errorf("image platform %s/%s/%s does not match expected %s/%s/%s", identity.OS(), identity.Architecture(), identity.Variant(), expected.OS, expected.Architecture, expected.Variant)
	}
	return validateImageLabels(labels, expected.labels())
}

// validateImageDescriptorDigests 校验 OCI descriptor 与 rootfs diff ID 闭包。
func validateImageDescriptorDigests(identity imageIdentityReader) error {
	for name, value := range map[string]string{
		"oci index digest":         identity.OCIIndexDigest(),
		"platform manifest digest": identity.PlatformManifestDigest(),
		"config digest":            identity.ConfigDigest(),
	} {
		if err := validateDigest(name, value); err != nil {
			return err
		}
	}
	diffIDs := identity.RootFSDiffIDs()
	if len(diffIDs) == 0 {
		return errors.New("image identity rootfs diff IDs are required")
	}
	for index, diffID := range diffIDs {
		if err := validateDigest(fmt.Sprintf("rootfs diff ID %d", index), diffID); err != nil {
			return err
		}
	}
	return nil
}

// validateExpectedImageMetadata 校验调用方提供的 Git 与输入真值。
func validateExpectedImageMetadata(expected expectedImageMetadata) error {
	if !gitObjectPattern.MatchString(expected.PolicySHA) || !gitObjectPattern.MatchString(expected.SourceTreeSHA) {
		return errors.New("expected policy and source tree must be canonical Git object IDs")
	}
	if err := validateDigest("expected image input digest", expected.InputDigest); err != nil {
		return err
	}
	if err := validateDigest("expected toolchain digest", expected.ToolchainDigest); err != nil {
		return err
	}
	if expected.SchemaVersion == "" || expected.OS == "" || expected.Architecture == "" {
		return errors.New("expected schema version and platform are required")
	}
	return nil
}

func validateImageLabels(labels map[string]string, expectedLabels map[string]string) error {
	for name, wanted := range expectedLabels {
		if wanted == "" {
			return fmt.Errorf("expected image label %s is empty", name)
		}
		if actual, exists := labels[name]; !exists || actual != wanted {
			return fmt.Errorf("image label %s = %q, want %q", name, actual, wanted)
		}
	}
	return nil
}

func validateDigest(name string, value string) error {
	if !sha256DigestPattern.MatchString(value) {
		return fmt.Errorf("%s must be an immutable sha256 digest", name)
	}
	return nil
}
