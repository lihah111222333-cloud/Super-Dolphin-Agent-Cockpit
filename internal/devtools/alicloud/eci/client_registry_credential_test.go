package eci

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestRegistryCredentialFieldsMapExactlyOnceToCLI 动态枚举凭据字段并证明每项都映射到唯一 ECI API 参数。
func TestRegistryCredentialFieldsMapExactlyOnceToCLI(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	if _, err := newTestClient(t, runner).CreateContainerGroup(t.Context(), validCreateRequest()); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	want := make([]string, 0, reflect.TypeFor[RegistryCredential]().NumField())
	for field := range reflect.TypeFor[RegistryCredential]().Fields() {
		want = append(want, field.Tag.Get("cli"))
	}
	got := make([]string, 0, len(want))
	const prefix = "--ImageRegistryCredential.1."
	for _, argument := range runner.calls[0] {
		if suffix, found := strings.CutPrefix(argument, prefix); found {
			got = append(got, suffix)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("registry credential CLI fields = %v, want %v", got, want)
	}
}

// TestRegistryCredentialFailsFastWithoutCredential 覆盖分片缺少短期 registry 凭据时的 fail-fast。
func TestRegistryCredentialFailsFastWithoutCredential(t *testing.T) {
	config := testConfig()
	config.RegistryCredential = RegistryCredential{}
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{}`)}}
	client, err := NewWithRunner(config, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if _, err := client.CreateContainerGroup(t.Context(), validCreateRequest()); err == nil || len(runner.calls) != 0 {
		t.Fatalf("missing credential error = %v, calls = %v", err, runner.calls)
	}
}

// TestRegistryCredentialFailsFastOnRegistryMismatch 覆盖凭据域名与不可变镜像域名错配。
func TestRegistryCredentialFailsFastOnRegistryMismatch(t *testing.T) {
	config := testConfig()
	config.RegistryCredential.Server = "other.example"
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{}`)}}
	client, err := NewWithRunner(config, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if _, err := client.CreateContainerGroup(t.Context(), validCreateRequest()); err == nil || len(runner.calls) != 0 {
		t.Fatalf("mismatched credential error = %v, calls = %v", err, runner.calls)
	}
}

// TestRegistryCredentialRedactsSecrets 覆盖 ECI CLI 错误不得回显 registry 用户名或令牌。
func TestRegistryCredentialRedactsSecrets(t *testing.T) {
	config := testConfig()
	runner := &fakeCommandRunner{err: errors.New("test-user test-token")}
	client, err := NewWithRunner(config, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	_, err = client.CreateContainerGroup(t.Context(), validCreateRequest())
	if err == nil || strings.Contains(err.Error(), "test-user") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("CreateContainerGroup() returned unredacted error %v", err)
	}
}

// TestRegistryCredentialConfigRejectsStaticAndDeferred 明确拒绝静态凭据与延迟 loader 同时存在。
func TestRegistryCredentialConfigRejectsStaticAndDeferred(t *testing.T) {
	config := testConfig()
	config.RegistryCredentialLoader = func() (RegistryCredential, error) { return RegistryCredential{}, nil }
	err := validateRegistryCredentialConfig(config)
	if err == nil || !strings.Contains(err.Error(), "either a static credential or a deferred loader") {
		t.Fatalf("validateRegistryCredentialConfig() error = %v", err)
	}
}

// TestRegistryCredentialLoaderDefersEnvironmentReadUntilCreate 覆盖凭据读取仅发生在真实 ECI 创建边界且缺凭据时零 CLI 调用。
func TestRegistryCredentialLoaderDefersEnvironmentReadUntilCreate(t *testing.T) {
	loadCalls := 0
	config := testConfig()
	config.RegistryCredential = RegistryCredential{}
	config.RegistryCredentialLoader = func() (RegistryCredential, error) {
		loadCalls++
		return RegistryCredential{}, errors.New("GHCR credential is missing")
	}
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{}`)}}
	client, err := NewWithRunner(config, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if loadCalls != 0 {
		t.Fatalf("credential loader calls after construction = %d, want 0", loadCalls)
	}
	for range 2 {
		if _, err := client.CreateContainerGroup(t.Context(), validCreateRequest()); err == nil {
			t.Fatal("CreateContainerGroup() error = nil")
		}
	}
	if loadCalls != 1 {
		t.Fatalf("credential loader calls = %d, want exactly 1", loadCalls)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("CLI calls = %d, want 0", len(runner.calls))
	}
}

// TestRegistryCredentialLoaderSuppliesCredentialAtCreate 覆盖创建边界一次加载完整凭据并完成 ECI 请求。
func TestRegistryCredentialLoaderSuppliesCredentialAtCreate(t *testing.T) {
	loadCalls := 0
	config := testConfig()
	want := config.RegistryCredential
	config.RegistryCredential = RegistryCredential{}
	config.RegistryCredentialLoader = func() (RegistryCredential, error) {
		loadCalls++
		return want, nil
	}
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-deferred"}`)}}
	client, err := NewWithRunner(config, runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if loadCalls != 0 {
		t.Fatalf("credential loader calls after construction = %d, want 0", loadCalls)
	}
	created, err := client.CreateContainerGroup(t.Context(), validCreateRequest())
	if err != nil || created.ID != "eci-deferred" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", created, err)
	}
	if loadCalls != 1 || len(runner.calls) != 1 {
		t.Fatalf("credential loader calls = %d, CLI calls = %d; want 1 and 1", loadCalls, len(runner.calls))
	}
}
