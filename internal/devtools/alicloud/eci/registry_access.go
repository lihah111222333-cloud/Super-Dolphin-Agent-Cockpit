package eci

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// RegistryAccess 描述一次 ECI 请求显式使用的私有镜像仓库访问方式。
// TemporaryCredential 仅限当前请求使用，调用方不得持久化。
type RegistryAccess struct {
	ACR                 *ACRRegistryInfo
	TemporaryCredential *ImageRegistryCredential
}

// ACRRegistryInfo 标识一个供 ECI 免密码拉取镜像的 ACR 企业版实例。
type ACRRegistryInfo struct {
	InstanceID     string `json:"instance_id"`
	InstanceName   string `json:"instance_name"`
	RegionID       string `json:"region_id"`
	Domain         string `json:"domain"`
	ServiceRoleARN string `json:"service_role_arn,omitempty"`
	UserRoleARN    string `json:"user_role_arn,omitempty"`
}

// ImageRegistryCredential 仅为单次请求携带短期镜像仓库凭据。
type ImageRegistryCredential struct {
	Server   string
	Username string
	Password string
}

var (
	acrIdentifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	regionIDPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)
	registryDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?(?::[0-9]{1,5})?$`)
	roleARNPattern        = regexp.MustCompile(`^acs:ram::[0-9]{1,32}:role/[A-Za-z0-9_+=,.@/-]{1,128}$`)
)

// ValidateRegistryAccess rejects mutable images and incomplete, mixed, or
// image-domain-mismatched registry access. A non-empty access method must be
// usable by every supplied image; callers cannot silently reuse one image for
// another container role.
func ValidateRegistryAccess(access RegistryAccess, images ...string) error {
	if len(images) == 0 {
		return errors.New("ECI image references are required")
	}
	if access.ACR != nil && access.TemporaryCredential != nil {
		return errors.New("ECI registry access must use exactly one private-registry method")
	}
	var domain string
	acrDomainMatched := false
	for index, image := range images {
		if !imageDigestPattern.MatchString(image) {
			return fmt.Errorf("ECI image %d must be a repository@sha256:<64 lowercase hex> digest reference", index+1)
		}
		imageDomain, ok := registryDomainFromReference(image)
		if !ok {
			return fmt.Errorf("ECI image %d registry domain is invalid", index+1)
		}
		if index == 0 {
			domain = imageDomain
		}
		if access.TemporaryCredential != nil && imageDomain != domain {
			return errors.New("ECI registry access cannot span multiple image registry domains")
		}
		if access.ACR != nil {
			if imageDomain == access.ACR.Domain {
				acrDomainMatched = true
			} else if !publicRegistryDomain(imageDomain) {
				return errors.New("ECI ACR registry access may only be combined with explicit public digest images")
			}
		}
	}
	if access.ACR == nil && access.TemporaryCredential == nil {
		return nil
	}
	if access.ACR != nil {
		if err := validateACRRegistryInfo(*access.ACR, access.ACR.Domain); err != nil {
			return err
		}
		if !acrDomainMatched {
			return errors.New("ECI ACR registry access does not match any image registry")
		}
		return nil
	}
	return validateImageRegistryCredential(*access.TemporaryCredential, domain)
}

func publicRegistryDomain(domain string) bool {
	return domain == "docker.io" || domain == "registry-1.docker.io"
}

func validateACRRegistryInfo(info ACRRegistryInfo, imageDomain string) error {
	if !acrIdentifierPattern.MatchString(info.InstanceID) || !acrIdentifierPattern.MatchString(info.InstanceName) ||
		!regionIDPattern.MatchString(info.RegionID) || !registryDomainPattern.MatchString(info.Domain) ||
		info.Domain != imageDomain {
		return errors.New("ECI ACR registry info is invalid or does not match the image registry")
	}
	if (info.ServiceRoleARN == "") != (info.UserRoleARN == "") ||
		(info.ServiceRoleARN != "" && (!roleARNPattern.MatchString(info.ServiceRoleARN) || !roleARNPattern.MatchString(info.UserRoleARN))) {
		return errors.New("ECI ACR cross-account role ARNs must be a valid pair")
	}
	return nil
}

func validateImageRegistryCredential(credential ImageRegistryCredential, imageDomain string) error {
	if credential.Server != imageDomain || !registryDomainPattern.MatchString(credential.Server) ||
		!validRegistryCredentialValue(credential.Username) || !validRegistryCredentialValue(credential.Password) {
		return errors.New("ECI temporary image registry credential is invalid or does not match the image registry")
	}
	return nil
}

func validRegistryCredentialValue(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

// ValidateRegistryAccessForRepository validates access against an OCI repository reference.
func ValidateRegistryAccessForRepository(access RegistryAccess, repository string) error {
	if access.ACR == nil && access.TemporaryCredential == nil {
		return nil
	}
	domain, ok := registryDomainFromReference(repository)
	if !ok {
		return errors.New("ECI image registry domain is invalid")
	}
	if access.ACR == nil || access.TemporaryCredential != nil {
		return errors.New("ECI registry repository access requires ACR registry info only")
	}
	return validateACRRegistryInfo(*access.ACR, domain)
}

func registryDomainFromReference(image string) (string, bool) {
	repository, _, _ := strings.Cut(image, "@")
	domain, _, ok := strings.Cut(repository, "/")
	return domain, ok && registryDomainPattern.MatchString(domain)
}

func appendRegistryAccess(args []string, access RegistryAccess) []string {
	if access.ACR != nil {
		info := access.ACR
		args = append(args,
			"--AcrRegistryInfo.1.InstanceId", info.InstanceID,
			"--AcrRegistryInfo.1.InstanceName", info.InstanceName,
			"--AcrRegistryInfo.1.RegionId", info.RegionID,
			"--AcrRegistryInfo.1.Domain", info.Domain,
		)
		if info.ServiceRoleARN != "" {
			args = append(args, "--AcrRegistryInfo.1.ArnService", info.ServiceRoleARN, "--AcrRegistryInfo.1.ArnUser", info.UserRoleARN)
		}
		return args
	}
	if access.TemporaryCredential != nil {
		credential := access.TemporaryCredential
		return append(args,
			"--ImageRegistryCredential.1.Server", credential.Server,
			"--ImageRegistryCredential.1.UserName", credential.Username,
			"--ImageRegistryCredential.1.Password", credential.Password,
		)
	}
	return args
}

func redactRegistryCredential(err error, access RegistryAccess) error {
	if err == nil || access.TemporaryCredential == nil {
		return err
	}
	values := []string{access.TemporaryCredential.Password, access.TemporaryCredential.Username}
	sort.Slice(values, func(left, right int) bool { return len(values[left]) > len(values[right]) })
	return redactedError{err: err, values: values}
}
