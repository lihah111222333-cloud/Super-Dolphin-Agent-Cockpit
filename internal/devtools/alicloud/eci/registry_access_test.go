package eci

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testRegistryDomain = "registry.example"

func TestRegistryAccessFieldRegistry(t *testing.T) {
	assertStructFields(t, reflect.TypeFor[RegistryAccess](), []string{"ACR", "TemporaryCredential"})
	assertStructFields(t, reflect.TypeFor[ACRRegistryInfo](), []string{"InstanceID", "InstanceName", "RegionID", "Domain", "ServiceRoleARN", "UserRoleARN"})
	assertStructFields(t, reflect.TypeFor[ImageRegistryCredential](), []string{"Server", "Username", "Password"})
}

func validACRRegistryInfo() ACRRegistryInfo {
	return ACRRegistryInfo{InstanceID: "cri-test", InstanceName: "ci-registry", RegionID: "cn-hangzhou", Domain: testRegistryDomain}
}

func validTemporaryRegistryCredential() ImageRegistryCredential {
	return ImageRegistryCredential{Server: testRegistryDomain, Username: "cr_temp_user", Password: "temporary-password"}
}

func TestClientCreateContainerGroupEncodesACRRegistryInfo(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	info := validACRRegistryInfo()
	info.ServiceRoleARN = "acs:ram::123456789:role/eci-service"
	info.UserRoleARN = "acs:ram::987654321:role/acr-owner"
	request.RegistryAccess.ACR = &info
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	for _, pair := range [][]string{
		{"--AcrRegistryInfo.1.InstanceId", "cri-test"},
		{"--AcrRegistryInfo.1.InstanceName", "ci-registry"},
		{"--AcrRegistryInfo.1.RegionId", "cn-hangzhou"},
		{"--AcrRegistryInfo.1.Domain", testRegistryDomain},
		{"--AcrRegistryInfo.1.ArnService", info.ServiceRoleARN},
		{"--AcrRegistryInfo.1.ArnUser", info.UserRoleARN},
	} {
		if !containsArgumentPair(runner.calls[0], pair[0], pair[1]) {
			t.Fatalf("CreateContainerGroup call missing %v: %#v", pair, runner.calls[0])
		}
	}
}

func TestClientCreateContainerGroupRedactsTemporaryRegistryCredential(t *testing.T) {
	credential := validTemporaryRegistryCredential()
	client := newTestClient(t, &fakeCommandRunner{err: errors.New("registry rejected " + credential.Username + ":" + credential.Password)})
	request := validCreateRequest()
	request.RegistryAccess.TemporaryCredential = &credential
	_, err := client.CreateContainerGroup(context.Background(), request)
	if err == nil || strings.Contains(err.Error(), credential.Username) || strings.Contains(err.Error(), credential.Password) || !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("CreateContainerGroup() error = %v, want redacted temporary credential", err)
	}
}

func TestClientCreateContainerGroupRejectsInvalidRegistryAccessBeforeCLI(t *testing.T) {
	tests := []struct {
		name   string
		access RegistryAccess
	}{
		{"mixed methods", RegistryAccess{ACR: &ACRRegistryInfo{InstanceID: "cri-test", InstanceName: "ci-registry", RegionID: "cn-hangzhou", Domain: testRegistryDomain}, TemporaryCredential: &ImageRegistryCredential{Server: testRegistryDomain, Username: "cr_temp_user", Password: "temporary-password"}}},
		{"wrong credential server", RegistryAccess{TemporaryCredential: &ImageRegistryCredential{Server: "other.example", Username: "user", Password: "password"}}},
		{"partial cross account role", RegistryAccess{ACR: &ACRRegistryInfo{InstanceID: "cri-test", InstanceName: "ci-registry", RegionID: "cn-hangzhou", Domain: testRegistryDomain, ServiceRoleARN: "acs:ram::123456789:role/eci-service"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			request := validCreateRequest()
			request.RegistryAccess = test.access
			if _, err := newTestClient(t, runner).CreateContainerGroup(context.Background(), request); err == nil {
				t.Fatal("CreateContainerGroup() error = nil")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none", runner.calls)
			}
		})
	}
}

func TestClientCreateContainerGroupRejectsRegistryAccessAcrossImageDomainsBeforeCLI(t *testing.T) {
	runner := &fakeCommandRunner{}
	request := validCreateRequest()
	credential := validTemporaryRegistryCredential()
	request.RegistryAccess.TemporaryCredential = &credential
	request.MainImage = "other.example/remote-builder@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := newTestClient(t, runner).CreateContainerGroup(context.Background(), request); err == nil {
		t.Fatal("CreateContainerGroup() error = nil")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
}
