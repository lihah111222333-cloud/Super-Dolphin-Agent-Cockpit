package localci

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func candidateEntries(dockerfile string) []sourceexport.TreeEntry {
	inputs := candidateEntryInputs()
	entries := []sourceexport.TreeEntry{
		contextEntry("build/gate/runtime-deps.lock", "100644", testRuntimeDepsLock(inputs)),
		contextEntry("cmd/super-dolphin-gate/main.go", "100644", "package main\n"),
		contextEntry("build/gate/Dockerfile", "100644", dockerfile),
		contextEntry("build/gate/inputs.json", "100644", candidateManifest()+"\n"),
	}
	for _, path := range runtimeDepsTestInputPaths() {
		entries = append(entries, contextEntry(path, "100644", inputs[path]))
	}
	return entries
}

func candidateManifest() string {
	manifest := `{
  "schema_version": "2",
  "dockerfile": "build/gate/Dockerfile",
  "inputs": [
    "build/gate/Dockerfile",
    "build/gate/inputs.json",
    "build/gate/runtime-deps.Dockerfile",
    "build/gate/runtime-deps.lock",
    "build/gate/runtime-lsp/package-lock.json",
    "build/gate/runtime-proxy/go.mod",
    "build/gate/runtime-proxy/go.sum",
    "build/gate/runtime-tools/go.mod",
    "build/gate/runtime-tools/go.sum",
    "build/gate/toolchain.lock",
    "cmd/super-dolphin-gate/main.go",
    "cmd/super-dolphin-gate/remote_refresh_seed.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script_browser.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script_runtime.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script_tail.go",
    "frontend-app/package-lock.json",
    "go.mod",
    "go.sum",
    "internal/devtools/gate/executor_seed.go",
    "internal/devtools/nilnessrunner/runner.go",
    "scripts/nilness_guard.go"
  ],
  "gate_compile_inputs": [
    "cmd/super-dolphin-gate/main.go",
    "cmd/super-dolphin-gate/remote_refresh_seed.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script_browser.go",
    "cmd/super-dolphin-gate/remote_refresh_seed_script_runtime.go",
    "go.mod",
    "go.sum",
    "internal/devtools/gate/executor_seed.go"
  ]
}`
	return manifest
}

func candidateEntryInputs() map[string]string {
	toolchain := `{
  "schema_version": "1",
  "buildkit_version": "v0.26.2",
  "buildkit_image": "mirror.gcr.io/moby/buildkit@sha256:` + strings.Repeat("c", 64) + `",
  "dockerfile_frontend": "builtin:dockerfile.v1",
  "source_date_epoch": "0",
  "target_platforms": ["linux/amd64", "linux/arm64"],
  "base_images": [{"name":"GO_IMAGE","reference":"golang@sha256:` + strings.Repeat("b", 64) + `"},{"name":"NODE_IMAGE","reference":"node@sha256:` + strings.Repeat("d", 64) + `"}],
  "dependency_sources": ["go.sum"],
  "runtime_deps_lock": "build/gate/runtime-deps.lock",
  "runtime_tools": {
    "node_version": "v24.18.0",
    "npm_version": "11.16.0",
			"python_version": "3.11.2",
			"ripgrep": "/opt/super-dolphin-gate/runtime/bin/rg@13.0.0-4+b2",
			"sqruff": "/opt/super-dolphin-gate/runtime/bin/sqruff@0.38.0",
			"sqruff_artifacts": [{"platform":"linux/amd64","url":"https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-x86_64-musl.tar.gz","sha256":"d96a06daca2a214eb0b6c07b2821e9cdb1379086041bcca6f8bab031b6eb8026"},{"platform":"linux/arm64","url":"https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-aarch64-musl.tar.gz","sha256":"7e1abca59aeb3a0899a78be36dbfd4002db2ce6754835250beeea2fab95f5abf"}],
    "gopls": "golang.org/x/tools/gopls@v0.22.0",
    "sqlc": "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0",
    "npm_lsp_packages": ["bash-language-server@5.6.0"]
  },
  "network_policy": "locked-dependencies"
}`
	inputs := map[string]string{
		"build/gate/runtime-deps.Dockerfile": "FROM scratch\n",
		"build/gate/toolchain.lock":          toolchain + "\n",
		"go.mod":                             "module example.invalid/gate\n",
		"go.sum":                             "sum\n",
		"internal/devtools/nilnessrunner/runner.go":                    "package nilnessrunner\n",
		"scripts/nilness_guard.go":                                     "package main\n",
		"frontend-app/package-lock.json":                               "{}\n",
		"build/gate/runtime-lsp/package-lock.json":                     "{}\n",
		"build/gate/runtime-proxy/go.mod":                              "module example.invalid/proxy\n",
		"build/gate/runtime-proxy/go.sum":                              "proxy sum\n",
		"build/gate/runtime-tools/go.mod":                              "module example.invalid/tools\n",
		"build/gate/runtime-tools/go.sum":                              "tools sum\n",
		"internal/devtools/gate/executor_seed.go":                      "package gate\n",
		"cmd/super-dolphin-gate/remote_refresh_seed.go":                "package main\n",
		"cmd/super-dolphin-gate/remote_refresh_seed_script.go":         "package main\n",
		"cmd/super-dolphin-gate/remote_refresh_seed_script_browser.go": "package main\n",
		"cmd/super-dolphin-gate/remote_refresh_seed_script_runtime.go": "package main\n",
	}
	return inputs
}
