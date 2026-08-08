package main

import (
	"errors"
	"os"
	"strings"
	"unicode"

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
	if !usernamePresent || !tokenPresent {
		return eci.RegistryCredential{}, errors.New("remote CI GHCR username and token environment variables are required")
	}
	if !validRemoteCredentialValue(username) || !validRemoteCredentialValue(token) {
		return eci.RegistryCredential{}, errors.New("remote CI GHCR credentials must not contain leading, trailing, or control whitespace")
	}
	return eci.RegistryCredential{Server: remoteRuntimeRegistryServer, UserName: username, Password: token}, nil
}

func validRemoteCredentialValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}
