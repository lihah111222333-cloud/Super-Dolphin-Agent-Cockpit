package gate

import (
	"errors"
	"fmt"
	"strings"
)

// validateReleaseBinding 校验规范发布目标及其严格有序的资源绑定。
func (r GrantRequest) validateReleaseBinding() error {
	if err := validateReleaseTarget(r); err != nil {
		return err
	}
	return validateReleaseAssets(r.ReleaseAssets)
}

// validateReleaseTarget 校验发布仓库、标签和提交摘要均为规范值。
func validateReleaseTarget(request GrantRequest) error {
	if strings.Count(request.ReleaseRepository, "/") != 1 ||
		strings.TrimSpace(request.ReleaseRepository) != request.ReleaseRepository ||
		strings.ContainsAny(request.ReleaseRepository, "\\ \t\r\n") {
		return errors.New("release_repository must be a normalized owner/name")
	}
	if strings.TrimSpace(request.ReleaseTag) == "" ||
		strings.TrimSpace(request.ReleaseTag) != request.ReleaseTag ||
		strings.ContainsAny(request.ReleaseTag, "\r\n") {
		return errors.New("release_tag must be non-empty and normalized")
	}
	return validateNonZeroActionOID("release_commit_sha", request.ReleaseCommitSHA)
}

// validateReleaseAssets 校验发布资源非空、逐项有效且按名称严格排序。
func validateReleaseAssets(assets []ReleaseAsset) error {
	if len(assets) == 0 {
		return errors.New("release_assets are required")
	}
	for index, asset := range assets {
		if err := asset.Validate(); err != nil {
			return fmt.Errorf("release_assets[%d]: %w", index, err)
		}
		if index > 0 && assets[index-1].Name >= asset.Name {
			return errors.New("release_assets must be strictly sorted by name")
		}
	}
	return nil
}

// Validate 校验发布资源的规范名称、摘要和正数字节数。
func (a ReleaseAsset) Validate() error {
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Name) != a.Name ||
		strings.ContainsAny(a.Name, "/\\\r\n") || a.Name == "." || a.Name == ".." {
		return errors.New("release asset name must be a normalized basename")
	}
	if err := validateDigest("release asset sha256", a.SHA256); err != nil {
		return err
	}
	if a.Size <= 0 {
		return errors.New("release asset size must be > 0")
	}
	return nil
}
