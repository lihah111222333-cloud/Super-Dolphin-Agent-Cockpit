package main

import (
	"strings"
	"testing"
)

func TestLoadRemoteRunConfigAllowsEmptyOCIRefreshForNormalCI(t *testing.T) {
	document := strings.Replace(
		validRemoteRunConfigJSON(),
		`"oci_refresh": {"output_repository": "registry.example/super-dolphin/baseline", "builder_worker_image": "registry.example/super-dolphin/oci-builder@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
		`"oci_refresh": {}`,
		1,
	)

	if _, err := loadRemoteRunConfig(writeRemoteRunConfigFixture(t, document)); err != nil {
		t.Fatalf("normal remote config with no refresh publication boundary: %v", err)
	}
}

func TestLoadRemoteRefreshConfigFailsFastWithoutOCIRefreshBoundary(t *testing.T) {
	cases := map[string]struct {
		document string
		want     string
	}{
		"missing publication boundary": {
			document: strings.Replace(validRemoteRunConfigJSON(), `"oci_refresh": {"output_repository": "registry.example/super-dolphin/baseline", "builder_worker_image": "registry.example/super-dolphin/oci-builder@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`, `"oci_refresh": {}`, 1),
			want:     "output_repository",
		},
		"ACR output repository": {
			document: strings.Replace(validRemoteRunConfigJSON(), `"registry.example/super-dolphin/baseline"`, `"registry.cn-shenzhen.aliyuncs.com/super-dolphin/baseline"`, 1),
			want:     "output_repository",
		},
		"ACR builder image": {
			document: strings.Replace(validRemoteRunConfigJSON(), `"registry.example/super-dolphin/oci-builder@`, `"registry.cn-shenzhen.aliyuncs.com/super-dolphin/oci-builder@`, 1),
			want:     "builder_worker_image",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadRemoteRefreshConfig(writeRemoteRunConfigFixture(t, test.document)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("refresh config error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRemoteRefreshConfigAcceptsConfiguredOCIRefreshBoundary(t *testing.T) {
	if _, err := loadRemoteRefreshConfig(writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())); err != nil {
		t.Fatalf("configured refresh config: %v", err)
	}
}
