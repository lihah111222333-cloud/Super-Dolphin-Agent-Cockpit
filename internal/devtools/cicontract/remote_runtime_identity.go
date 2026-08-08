package cicontract

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	// RemoteRegistryServer 是远程 CI 私有镜像和短期凭据共同绑定的唯一 registry。
	RemoteRegistryServer = "ghcr.io"
	// RemoteRegistryUsernameEnvironment 是短期 GHCR 用户名的唯一进程环境入口。
	RemoteRegistryUsernameEnvironment = "SUPER_DOLPHIN_CI_GHCR_USERNAME"
	// RemoteRegistryTokenEnvironment 是短期 GHCR token 的唯一进程环境入口。
	RemoteRegistryTokenEnvironment = "SUPER_DOLPHIN_CI_GHCR_TOKEN"
	// RemoteWorkerUID 是 ECI 主容器和工作目录的唯一非 root worker UID。
	RemoteWorkerUID = 65532
	// RemoteWorkerGID 是 ECI 主容器和工作目录的唯一非 root worker GID。
	RemoteWorkerGID = 65532
)

// ValidateRemoteRegistryCredential 校验 GHCR server 及其短期凭据的完整性。
// 该函数是命令 loader 与 ECI adapter 共用的值规则 owner；原始值不会被写入错误。
func ValidateRemoteRegistryCredential(server, username, token string) error {
	for _, value := range []string{server, username, token} {
		if !ValidRemoteRegistryCredentialValue(value) {
			return errors.New("remote registry credential is required and must use canonical values")
		}
	}
	if server != RemoteRegistryServer {
		return fmt.Errorf("remote registry server must be %q", RemoteRegistryServer)
	}
	if len(username) > 256 || len(token) > 256 {
		return errors.New("remote registry credential exceeds 256 characters")
	}
	return nil
}

// ValidRemoteRegistryCredentialValue 判断一个凭据值不能为空、含空白或控制字符。
func ValidRemoteRegistryCredentialValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

// ValidateRemoteRuntimeIdentity 校验远程 worker、registry 和环境入口的固定身份。
func ValidateRemoteRuntimeIdentity() error {
	if RemoteWorkerUID != 65532 || RemoteWorkerGID != 65532 {
		return errors.New("remote worker identity must remain 65532:65532")
	}
	if err := ValidateRemoteRegistryCredential(RemoteRegistryServer, "identity-check-user", "identity-check-token"); err != nil {
		return fmt.Errorf("remote registry identity: %w", err)
	}
	if !ValidRemoteRegistryCredentialValue(RemoteRegistryUsernameEnvironment) || !ValidRemoteRegistryCredentialValue(RemoteRegistryTokenEnvironment) || RemoteRegistryUsernameEnvironment == RemoteRegistryTokenEnvironment {
		return errors.New("remote registry credential environment identities are invalid")
	}
	return nil
}
