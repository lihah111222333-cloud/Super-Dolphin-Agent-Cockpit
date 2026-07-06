package main

import "testing"

func TestPackageGoBinaryCacheKeyIncludesModuleToolchainAndPlatformInputs(t *testing.T) {
	fixtures := []struct {
		name     string
		script   string
		required []string
	}{
		{
			name:   "macos",
			script: "package_macos.sh",
			required: []string{
				"go_binary_cache_paths=(",
				"\"$root/go.mod\"",
				"\"$root/go.sum\"",
				"go_binary_cache_inputs=(",
				"\"input:GOVERSION=$(go env GOVERSION)\"",
				"\"input:GOOS=$goos\"",
				"\"input:GOARCH=$goarch\"",
				"\"input:CGO_ENABLED=$macos_cgo_enabled\"",
				"\"input:MACOSX_DEPLOYMENT_TARGET=$macos_min_version\"",
				"\"input:CGO_CFLAGS=$macos_cgo_cflags\"",
				"\"input:CGO_CXXFLAGS=$macos_cgo_cxxflags\"",
				"\"input:CGO_LDFLAGS=$macos_cgo_ldflags\"",
				"phase_cache_check \"go-binaries\" \"${go_binary_cache_inputs[@]}\" \"${go_binary_cache_paths[@]}\"",
			},
		},
		{
			name:   "linux",
			script: "package_linux.sh",
			required: []string{
				"go_binary_cache_paths=(",
				"\"$root/go.mod\"",
				"\"$root/go.sum\"",
				"go_binary_cache_inputs=(",
				"\"input:GOVERSION=$(go env GOVERSION)\"",
				"\"input:GOOS=$goos\"",
				"\"input:GOARCH=$goarch\"",
				"\"input:CGO_ENABLED=$linux_cgo_enabled\"",
				"\"input:CGO_CFLAGS=${CGO_CFLAGS:-}\"",
				"\"input:CGO_CXXFLAGS=${CGO_CXXFLAGS:-}\"",
				"\"input:CGO_LDFLAGS=${CGO_LDFLAGS:-}\"",
				"phase_cache_check \"go-binaries\" \"${go_binary_cache_inputs[@]}\" \"${go_binary_cache_paths[@]}\"",
			},
		},
		{
			name:   "windows",
			script: "package_windows.ps1",
			required: []string{
				"$goBinaryCachePaths = @(",
				"(Join-Path $Root 'go.mod')",
				"(Join-Path $Root 'go.sum')",
				"$goInputs = @('GOOS=windows', \"GOARCH=$WindowsPackageArch\", \"GOVERSION=$((& go env GOVERSION).Trim())\", \"WINDOWS_GUI_LDFLAGS=$windowsGuiLdFlags\")",
				"Test-BuildPhaseCache -Name 'go-binaries' -Paths $goBinaryCachePaths -Inputs $goInputs",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			script := readScript(t, fixture.script)
			for _, required := range fixture.required {
				assertScriptContains(t, script, required)
			}
		})
	}
}
