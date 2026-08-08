package eci

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestConfigFileVolumeCLIEncodingUsesBase64AndReadOnlyProjection(t *testing.T) {
	const content = "#!/bin/sh\necho projected\n"
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	request := validConfigFileProjectionRequest(content)
	if _, err := newTestClient(t, runner).CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	call := runner.calls[0]
	for _, pair := range [][]string{
		{"--Volume.1.Type", "EmptyDirVolume"},
		{"--Volume.2.Type", "EmptyDirVolume"},
		{"--Volume.3.Type", "EmptyDirVolume"},
		{"--Volume.4.Name", "bootstrap-config"},
		{"--Volume.4.Type", ConfigFileVolumeType},
		{"--Volume.4.ConfigFileVolume.DefaultMode", "0555"},
		{"--Volume.4.ConfigFileVolume.ConfigFileToPath.1.Path", "bootstrap.sh"},
		{"--Volume.4.ConfigFileVolume.ConfigFileToPath.1.Content", base64.StdEncoding.EncodeToString([]byte(content))},
		{"--Volume.4.ConfigFileVolume.ConfigFileToPath.1.Mode", "0555"},
		{"--InitContainer.1.VolumeMount.4.Name", "bootstrap-config"},
		{"--InitContainer.1.VolumeMount.4.ReadOnly", "true"},
	} {
		if !containsArgumentPair(call, pair[0], pair[1]) {
			t.Fatalf("CreateContainerGroup call missing %v: %#v", pair, call)
		}
	}
	if containsArgumentPair(call, "--Container.1.VolumeMount.4.Name", "bootstrap-config") {
		t.Fatalf("main container mounted ConfigFileVolume: %#v", call)
	}
}

func TestConfigFileVolumeValidationRejectsUnsafeShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateRequest)
	}{
		{"absolute path", func(request *CreateRequest) { request.ConfigFileVolumes[0].ConfigFileToPath[0].Path = "/bootstrap.sh" }},
		{"parent path", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Path = "../bootstrap.sh"
		}},
		{"noncanonical path", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Path = "dir//bootstrap.sh"
		}},
		{"control character path", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Path = "dir/bootstrap\n.sh"
		}},
		{"unsafe volume name", func(request *CreateRequest) { request.ConfigFileVolumes[0].Name = "go-build-cache" }},
		{"unsafe default mode", func(request *CreateRequest) { request.ConfigFileVolumes[0].DefaultMode = 0o644 }},
		{"unsafe file mode", func(request *CreateRequest) { request.ConfigFileVolumes[0].ConfigFileToPath[0].Mode = 0o644 }},
		{"non-read-only mount", func(request *CreateRequest) { request.InitVolumeMounts[3].ReadOnly = false }},
		{"main config mount", func(request *CreateRequest) {
			request.MainVolumeMounts = append(request.MainVolumeMounts, VolumeMount{Name: "bootstrap-config", MountPath: "/run/config", ReadOnly: true})
		}},
		{"credential marker", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "SUPER_DOLPHIN_CI_GHCR_TOKEN=do-not-project"
		}},
		{"signed URL marker", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "https://example.invalid/object?X-Amz-Signature=secret"
		}},
		{"JWT marker", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "JWT=header.payload.signature"
		}},
		{"oversized file", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = strings.Repeat("x", ConfigFileVolumeMaxFileBytes+1)
		}},
		{"oversized aggregate", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath = []ConfigFileToPath{
				{Path: "one.sh", Content: strings.Repeat("x", 30*1024)},
				{Path: "two.sh", Content: strings.Repeat("y", 30*1024+1)},
			}
		}},
		{"too many volumes", func(request *CreateRequest) {
			for index := 1; index <= 17; index++ {
				request.ConfigFileVolumes = append(request.ConfigFileVolumes, ConfigFileVolume{
					Name:        "bootstrap-" + strconv.Itoa(index),
					DefaultMode: ConfigFileVolumeSafeMode,
					ConfigFileToPath: []ConfigFileToPath{{
						Path: "bootstrap.sh", Content: "#!/bin/sh\n", Mode: ConfigFileVolumeSafeMode,
					}},
				})
			}
		}},
		{"too many files", func(request *CreateRequest) {
			request.ConfigFileVolumes[0].ConfigFileToPath = make([]ConfigFileToPath, ConfigFileVolumeMaxFilesPerGroup+1)
			for index := range request.ConfigFileVolumes[0].ConfigFileToPath {
				request.ConfigFileVolumes[0].ConfigFileToPath[index] = ConfigFileToPath{
					Path: "bootstrap-" + strconv.Itoa(index) + ".sh", Mode: ConfigFileVolumeSafeMode,
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			request := validConfigFileProjectionRequest("#!/bin/sh\n")
			test.mutate(&request)
			if _, err := newTestClient(t, runner).CreateContainerGroup(context.Background(), request); err == nil {
				t.Fatal("CreateContainerGroup() error = nil")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none", runner.calls)
			}
		})
	}
}

func TestConfigFileVolumeValidationAcceptsExactLocalLimits(t *testing.T) {
	volumes := make([]ConfigFileVolume, ConfigFileVolumeMaxVolumesPerGroup-3)
	for index := range volumes {
		volumes[index] = ConfigFileVolume{
			Name:        "bootstrap-" + strconv.Itoa(index),
			DefaultMode: ConfigFileVolumeSafeMode,
			ConfigFileToPath: []ConfigFileToPath{{
				Path: "bootstrap.sh", Mode: ConfigFileVolumeSafeMode,
			}},
		}
	}
	if err := validateConfigFileVolumes(volumes, []string{"source-data", "work-data", "temp-data"}); err != nil {
		t.Fatalf("validateConfigFileVolumes() exact volume limit error = %v", err)
	}
	files := make([]ConfigFileToPath, ConfigFileVolumeMaxFilesPerGroup)
	for index := range files {
		files[index] = ConfigFileToPath{Path: "bootstrap-" + strconv.Itoa(index) + ".sh", Mode: ConfigFileVolumeSafeMode}
	}
	if err := validateConfigFileVolumes([]ConfigFileVolume{{
		Name: "bootstrap-config", DefaultMode: ConfigFileVolumeSafeMode, ConfigFileToPath: files,
	}}, []string{"source-data", "work-data", "temp-data"}); err != nil {
		t.Fatalf("validateConfigFileVolumes() exact file limit error = %v", err)
	}
}

func TestConfigFileVolumeProjectionRedactsRawAndEncodedContentFromCLIErrors(t *testing.T) {
	const content = "#!/bin/sh\necho projected-control\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	client := newTestClient(t, &fakeCommandRunner{err: errors.New("CLI stderr raw=" + content + " encoded=" + encoded)})
	_, err := client.CreateContainerGroup(context.Background(), validConfigFileProjectionRequest(content))
	if err == nil || strings.Contains(err.Error(), content) || strings.Contains(err.Error(), encoded) {
		t.Fatalf("CreateContainerGroup() error = %v, want projection content redacted", err)
	}
	if strings.Count(err.Error(), "<redacted>") < 2 {
		t.Fatalf("CreateContainerGroup() error = %v, want raw and encoded redaction", err)
	}
}

func TestConfigFileVolumeProjectionRejectsEnvironmentAndRegistryValues(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*CreateRequest, *Config)
	}{
		{"init environment value", func(request *CreateRequest, _ *Config) {
			request.InitContainer.Environment["SUPER_DOLPHIN_CI_GHCR_TOKEN"] = "runtime-secret-value"
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "#!/bin/sh\necho runtime-secret-value\n"
		}},
		{"short init environment value", func(request *CreateRequest, _ *Config) {
			request.InitContainer.Environment["SUPER_DOLPHIN_CI_GHCR_TOKEN"] = "x"
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "#!/bin/sh\necho x\n"
		}},
		{"short environment username", func(request *CreateRequest, _ *Config) {
			request.InitContainer.Environment["SUPER_DOLPHIN_CI_GHCR_USERNAME"] = "u"
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "#!/bin/sh\necho u\n"
		}},
		{"registry password", func(request *CreateRequest, config *Config) {
			credential := testRegistryCredential()
			config.RegistryCredentialLoader = func() (RegistryCredential, error) { return credential, nil }
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "#!/bin/sh\necho " + credential.Password + "\n"
		}},
		{"encoded registry password", func(request *CreateRequest, config *Config) {
			credential := testRegistryCredential()
			config.RegistryCredentialLoader = func() (RegistryCredential, error) { return credential, nil }
			encoded := base64.RawURLEncoding.EncodeToString([]byte(credential.Password))
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "#!/bin/sh\necho " + encoded + " | base64 -d\n"
		}},
		{"short registry username", func(request *CreateRequest, config *Config) {
			credential := testRegistryCredential()
			credential.UserName = "u"
			config.RegistryCredentialLoader = func() (RegistryCredential, error) { return credential, nil }
			request.ConfigFileVolumes[0].ConfigFileToPath[0].Content = "#!/bin/sh\necho u\n"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
			request := validConfigFileProjectionRequest("#!/bin/sh\n")
			config := testConfig()
			test.configure(&request, &config)
			client, err := NewWithRunner(config, runner)
			if err != nil {
				t.Fatalf("NewWithRunner() error = %v", err)
			}
			if _, err := client.CreateContainerGroup(context.Background(), request); err == nil {
				t.Fatal("CreateContainerGroup() error = nil")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none", runner.calls)
			}
		})
	}
}

func TestConfigFileVolumeProjectionAllowsNonSensitiveEnvironmentPaths(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	request := validConfigFileProjectionRequest("#!/bin/sh\ncd /workspace/source\n")
	request.InitContainer.Environment["SUPER_DOLPHIN_SOURCE_ROOT"] = "/workspace/source"
	client, err := NewWithRunner(testConfig(), runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
}

func TestConfigFileVolumeProjectionShortCredentialDoesNotMatchInsideCommandWord(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	request := validConfigFileProjectionRequest("#!/bin/sh\nexec /bin/true\n")
	request.InitContainer.Environment["SUPER_DOLPHIN_CI_GHCR_USERNAME"] = "x"
	client, err := NewWithRunner(testConfig(), runner)
	if err != nil {
		t.Fatalf("NewWithRunner() error = %v", err)
	}
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
}

func validConfigFileProjectionRequest(content string) CreateRequest {
	request := validCreateRequest()
	request.ConfigFileVolumes = []ConfigFileVolume{{
		Name:        "bootstrap-config",
		DefaultMode: ConfigFileVolumeSafeMode,
		ConfigFileToPath: []ConfigFileToPath{{
			Path: "bootstrap.sh", Content: content,
		}},
	}}
	request.InitVolumeMounts = append(request.InitVolumeMounts, VolumeMount{
		Name: "temp-data", MountPath: "/tmp",
	}, VolumeMount{
		Name: "bootstrap-config", MountPath: "/run/super-dolphin/bootstrap", ReadOnly: true,
	})
	return request
}
