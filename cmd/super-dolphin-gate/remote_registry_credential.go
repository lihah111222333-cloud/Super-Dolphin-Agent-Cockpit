package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	remoteRegistryUsernameEnvironment = cicontract.RemoteRegistryUsernameEnvironment
	remoteRegistryTokenEnvironment    = cicontract.RemoteRegistryTokenEnvironment
)

// loadRemoteRegistryCredential 从当前进程环境读取短期 GHCR 凭据，禁止缺失或空值进入 ECI 请求。
func loadRemoteRegistryCredential() (eci.RegistryCredential, error) {
	username, usernamePresent := os.LookupEnv(remoteRegistryUsernameEnvironment)
	token, tokenPresent := os.LookupEnv(remoteRegistryTokenEnvironment)
	if !usernamePresent || !tokenPresent {
		return eci.RegistryCredential{}, errors.New("remote CI GHCR username and token environment variables are required")
	}
	if err := cicontract.ValidateRemoteRegistryCredential(cicontract.RemoteRegistryServer, username, token); err != nil {
		return eci.RegistryCredential{}, fmt.Errorf("remote CI GHCR credential: %w", err)
	}
	return eci.RegistryCredential{Server: cicontract.RemoteRegistryServer, UserName: username, Password: token}, nil
}
