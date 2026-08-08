package main

import (
	"errors"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
)

const (
	remoteRuntimeRegistryServer       = "ghcr.io"
	remoteRegistryUsernameEnvironment = "SUPER_DOLPHIN_CI_GHCR_USERNAME"
	remoteRegistryTokenEnvironment    = "SUPER_DOLPHIN_CI_GHCR_TOKEN"
)

// loadRemoteRegistryCredential 从当前进程环境读取短期 GHCR 凭据，禁止缺失或空值进入 ECI 请求。
func loadRemoteRegistryCredential() (eci.RegistryCredential, error) {
	username, usernamePresent := os.LookupEnv(remoteRegistryUsernameEnvironment)
	token, tokenPresent := os.LookupEnv(remoteRegistryTokenEnvironment)
	if !usernamePresent || !tokenPresent || strings.TrimSpace(username) == "" || strings.TrimSpace(token) == "" {
		return eci.RegistryCredential{}, errors.New("remote CI GHCR username and token environment variables are required")
	}
	return eci.RegistryCredential{Server: remoteRuntimeRegistryServer, UserName: username, Password: token}, nil
}
