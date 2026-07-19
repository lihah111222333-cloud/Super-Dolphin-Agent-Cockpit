package main

import "testing"

type packageCacheFixture struct {
	name     string
	script   string
	required []string
}

func macOSPackageCacheFixture() packageCacheFixture {
	return packageCacheFixture{
		name:   "macos",
		script: "package_macos.sh",
		required: []string{
			"go_binary_cache_paths=(",
			"\"$root/go.mod\"",
			"\"$root/go.sum\"",
			"\"$root/scripts/package_macos.sh\"",
			"\"$root/scripts/build_phase_cache/main.go\"",
			"go_binary_cache_inputs=(",
			"\"input:GOVERSION=$(go env GOVERSION)\"",
			"\"input:GOFLAGS=$(go env GOFLAGS)\"",
			"\"input:GOEXPERIMENT=$(go env GOEXPERIMENT)\"",
			"\"input:GOOS=$goos\"",
			"\"input:GOARCH=$goarch\"",
			"\"input:GOAMD64=$(go env GOAMD64)\"",
			"\"input:GOARM64=$(go env GOARM64)\"",
			"\"input:APP_COMMIT=$app_commit\"",
			"\"input:VCS_MODIFIED=$(go_vcs_modified_input)\"",
			"\"input:CGO_ENABLED=$macos_cgo_enabled\"",
			"\"input:CC=$(go env CC)\"",
			"\"input:CXX=$(go env CXX)\"",
			"\"input:MACOSX_DEPLOYMENT_TARGET=$macos_min_version\"",
			"\"input:CGO_CFLAGS=$macos_cgo_cflags\"",
			"\"input:CGO_CXXFLAGS=$macos_cgo_cxxflags\"",
			"\"input:CGO_LDFLAGS=$macos_cgo_ldflags\"",
			"go_binary_cache_outputs=(",
			"phase_cache_restore \"go-binaries\" \"${go_binary_cache_outputs[@]}\" -- \"${go_binary_cache_inputs[@]}\" \"${go_binary_cache_paths[@]}\"",
			"phase_cache_save \"go-binaries\" \"${go_binary_cache_outputs[@]}\" -- \"${go_binary_cache_inputs[@]}\" \"${go_binary_cache_paths[@]}\"",
		},
	}
}

func linuxPackageCacheFixture() packageCacheFixture {
	return packageCacheFixture{
		name:   "linux",
		script: "package_linux.sh",
		required: []string{
			"go_binary_cache_paths=(",
			"\"$root/go.mod\"",
			"\"$root/go.sum\"",
			"\"$root/scripts/package_linux.sh\"",
			"\"$root/scripts/build_phase_cache/main.go\"",
			"go_binary_cache_inputs=(",
			"\"input:GOVERSION=$(go env GOVERSION)\"",
			"\"input:GOFLAGS=$(go env GOFLAGS)\"",
			"\"input:GOEXPERIMENT=$(go env GOEXPERIMENT)\"",
			"\"input:GOOS=$goos\"",
			"\"input:GOARCH=$goarch\"",
			"\"input:GOAMD64=$(go env GOAMD64)\"",
			"\"input:GOARM64=$(go env GOARM64)\"",
			"\"input:APP_COMMIT=$app_commit\"",
			"\"input:VCS_MODIFIED=$(go_vcs_modified_input)\"",
			"\"input:CGO_ENABLED=$linux_cgo_enabled\"",
			"\"input:CC=$(go env CC)\"",
			"\"input:CXX=$(go env CXX)\"",
			"\"input:CGO_CFLAGS=${CGO_CFLAGS:-}\"",
			"\"input:CGO_CXXFLAGS=${CGO_CXXFLAGS:-}\"",
			"\"input:CGO_LDFLAGS=${CGO_LDFLAGS:-}\"",
			"go_binary_cache_outputs=(",
			"phase_cache_restore \"go-binaries\" \"${go_binary_cache_outputs[@]}\" -- \"${go_binary_cache_inputs[@]}\" \"${go_binary_cache_paths[@]}\"",
			"phase_cache_save \"go-binaries\" \"${go_binary_cache_outputs[@]}\" -- \"${go_binary_cache_inputs[@]}\" \"${go_binary_cache_paths[@]}\"",
		},
	}
}

func windowsPackageCacheFixture() packageCacheFixture {
	return packageCacheFixture{
		name:   "windows",
		script: "package_windows.ps1",
		required: []string{
			"$goBinaryCachePaths = @(",
			"(Join-Path $Root 'go.mod')",
			"(Join-Path $Root 'go.sum')",
			"(Join-Path $Root 'scripts/package_windows.ps1')",
			"(Join-Path $Root 'scripts/build_phase_cache/main.go')",
			"$goInputs = @(",
			"\"GOFLAGS=$((& go env GOFLAGS).Trim())\"",
			"\"GOEXPERIMENT=$((& go env GOEXPERIMENT).Trim())\"",
			"\"GOAMD64=$((& go env GOAMD64).Trim())\"",
			"\"GOARM64=$((& go env GOARM64).Trim())\"",
			"\"CC=$((& go env CC).Trim())\"",
			"\"CXX=$((& go env CXX).Trim())\"",
			"\"VCS_MODIFIED=$(Get-GitWorktreeModifiedInput)\"",
			"$goBinaryCacheOutputs = @(",
			"Test-BuildPhaseCache -Name 'go-binaries' -Paths $goBinaryCachePaths -Inputs $goInputs -Outputs $goBinaryCacheOutputs",
			"Save-BuildPhaseCache -Name 'go-binaries' -Paths $goBinaryCachePaths -Inputs $goInputs -Outputs $goBinaryCacheOutputs",
		},
	}
}

func TestPackageGoBinaryCacheKeyIncludesModuleToolchainAndPlatformInputs(t *testing.T) {
	fixtures := []packageCacheFixture{
		macOSPackageCacheFixture(),
		linuxPackageCacheFixture(),
		windowsPackageCacheFixture(),
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
