.PHONY: build build-plain build-agent-terminal build-agent-terminal-plain frontend-deps frontend-build frontend-app-deps frontend-app-build frontend-embed-verify run run-plain dev-hot run-agent-terminal-debug run-agent-terminal-debug-plain build-peer-binaries package-macos package-linux package-windows test test-deferred test-e2e test-e2e-rpc-runtime vet clean guard code-size-guard guard-shell ai-maintenance-gates protocol-sync-check rpc-regression-check codemap-check codemap-refresh project-map-check project-map-refresh capcontract-check capcontract-refresh setup-cgo ui-cover-build ui-cover-run ui-cover-report app-cover-build app-cover-run app-cover-report log-audit p2-audit ida-test-all ida-test-heavy sqlc-generate sqlc-verify sqlc-verify-worktree

# Auto-detect macOS version to avoid ld warnings about version mismatch.
# Override with: make MIN_MACOS_VERSION=15.0 build
MIN_MACOS_VERSION ?= $(shell sw_vers -productVersion 2>/dev/null | cut -d. -f1-2 || echo 11.0)
FRIDA_VERSION_FILE ?= build/frida-version.txt
FRIDA_DEVKIT_VERSION ?= $(shell cat $(FRIDA_VERSION_FILE) 2>/dev/null)
FRIDA_LDFLAGS ?= -X github.com/multi-agent/go-agent-v2/pkg/idamcp.defaultFridaVersion=$(FRIDA_DEVKIT_VERSION)
AGENT_TERMINAL_DEBUG_PORT ?= 4501
FRONTEND_APP_DIR := frontend-app
EMBEDDED_FRONTEND_DIR := cmd/agent-terminal/web-dist
ifeq ($(OS),Windows_NT)
NPM ?= npm.cmd
NPM_INSTALL ?= install --no-audit --no-fund
else
NPM ?= npm
NPM_INSTALL ?= ci
endif

# NOTE: Do NOT use 'export CGO_*FLAGS' here — it causes cache thrashing between
#       make and IDE/terminal builds (Go caches key on CGO flags).
#       Run 'make setup-cgo' once to persist flags via 'go env -w'.
setup-cgo:
ifeq ($(shell uname -s),Darwin)
	go env -w CGO_CFLAGS="-mmacosx-version-min=$(MIN_MACOS_VERSION)"
	go env -w CGO_CXXFLAGS="-mmacosx-version-min=$(MIN_MACOS_VERSION)"
	go env -w CGO_LDFLAGS="-mmacosx-version-min=$(MIN_MACOS_VERSION)"
	@echo "✅ CGO flags persisted to go env (version-min=$(MIN_MACOS_VERSION))"
else
	@echo "ℹ️  Not Darwin, skipping CGO macOS flags"
endif

build: guard frontend-build
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go build -tags frida -ldflags "$(FRIDA_LDFLAGS)" $(GO_PACKAGE_PATTERNS)
	@$(MAKE) --no-print-directory _hook_check

build-plain: guard frontend-build
	go build $(GO_PACKAGE_PATTERNS)

build-agent-terminal: frontend-build
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go build -tags frida -ldflags "$(FRIDA_LDFLAGS)" -o bin/agent-terminal ./cmd/agent-terminal

build-agent-terminal-plain: frontend-build
	go build -o bin/agent-terminal ./cmd/agent-terminal

frontend-deps: frontend-app-deps

frontend-build: frontend-app-build

frontend-app-deps:
	cd $(FRONTEND_APP_DIR) && \
	if [ ! -d node_modules ] || [ package-lock.json -nt node_modules ] || [ package.json -nt node_modules ]; then \
		$(NPM) $(NPM_INSTALL); \
	else \
		echo "frontend-app dependencies are up to date"; \
	fi

frontend-app-build: frontend-app-deps
	cd $(FRONTEND_APP_DIR) && $(NPM) run build
	test -f $(FRONTEND_APP_DIR)/dist/index.html
	node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs
	test -f $(EMBEDDED_FRONTEND_DIR)/index.html

frontend-embed-verify: frontend-app-build
	./scripts/frontend_embed_verify.sh

# The root desktop scripts are the preferred dev launchers. These make targets
# keep the minimal packaged-asset run path for CI and local checks.
run: frontend-build
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go run -tags frida -ldflags "$(FRIDA_LDFLAGS)" ./cmd/agent-terminal

run-plain: frontend-build
	go run ./cmd/agent-terminal

dev-hot:
	SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1 ./run-new-ui-desktop.sh

# Memory subsystem defaults (override on the command line if you really
# want memory off, e.g. 'make run-agent-terminal-debug ENABLE_MEMORY_SYSTEM=0').
ENABLE_MEMORY_SYSTEM ?= 1
ENABLE_MEMORY_TOOLS ?= 1
MULTI_AGENT_MEMORY_FEATURE_TEAMMEM ?= 1
export ENABLE_MEMORY_SYSTEM
export ENABLE_MEMORY_TOOLS
export MULTI_AGENT_MEMORY_FEATURE_TEAMMEM
DEV_CONTROL_SESSION_TOKEN ?= dev-local-$(shell date +%s)-$(shell echo $$$$)

DEV_SQLITE_PATH ?= $(HOME)/.super-dolphin/super-dolphin.db
run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_SQLITE_PATH ?= $(DEV_SQLITE_PATH)
run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_RUNTIME_MODE := dev
run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR := $(CURDIR)
run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_DEV_ENTRYPOINT := make run-agent-terminal-debug

# When agent-terminal runs via `go run`, its own binary lives under a go-build
# tempdir, so spawnToolbridgePeers cannot locate mcp-orch / mcp-lsp next to it.
# Build the peers into ./bin and point GO_AGENT_PEER_BIN_DIR at that directory
# so dev runs get the same toolbridge wiring as packaged builds.
build-peer-binaries:
	@mkdir -p bin
	@tmp="$$(mktemp "bin/.mcp-orch.XXXXXX")"; \
		trap 'rm -f "$$tmp"' EXIT; \
		go build -o "$$tmp" ./cmd/mcp-orch; \
		mv -f "$$tmp" bin/mcp-orch
	@tmp="$$(mktemp "bin/.mcp-lsp.XXXXXX")"; \
		trap 'rm -f "$$tmp"' EXIT; \
		go build -o "$$tmp" ./cmd/mcp-lsp; \
		mv -f "$$tmp" bin/mcp-lsp

package-macos:
	./scripts/package_macos.sh

package-linux:
	./scripts/package_linux.sh

package-windows:
	@if command -v pwsh >/dev/null 2>&1; then \
		pwsh -NoProfile -ExecutionPolicy Bypass -File ./scripts/package_windows.ps1; \
	else \
		powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/package_windows.ps1; \
	fi

run-agent-terminal-debug: frontend-build build-peer-binaries
	GO_AGENT_CTL_SESSION_TOKEN=$(DEV_CONTROL_SESSION_TOKEN) \
		GO_AGENT_PEER_BIN_DIR=$(CURDIR)/bin \
		go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go run -tags frida -ldflags "$(FRIDA_LDFLAGS)" ./cmd/agent-terminal --debug --debug-port $(AGENT_TERMINAL_DEBUG_PORT)

run-agent-terminal-debug-plain: frontend-build build-peer-binaries
	GO_AGENT_CTL_SESSION_TOKEN=$(DEV_CONTROL_SESSION_TOKEN) \
		GO_AGENT_PEER_BIN_DIR=$(CURDIR)/bin \
		go run ./cmd/agent-terminal --debug --debug-port $(AGENT_TERMINAL_DEBUG_PORT)

mcp:
	go run ./cmd/mcp-server/main.go

# 已知并行争抢的 E2E 包（pipe/WebSocket 进程资源）：
#   internal/provider/claudecli, internal/provider/codexapp
# 先并行跑其余包，再用 -p 1 串行跑这 2 个，避免全仓并行时的 flaky failure。
DEFERRED_TEST_PKGS := ./internal/provider/claudecli ./internal/provider/codexapp
TEST_WITH_GUARD := ./scripts/test_with_guard.sh
# Explicit source-package roots keep generated package artifacts under dist/package out of Go package discovery.
GO_PACKAGE_PATTERNS := ./cmd/... ./internal/... ./pkg/... ./scripts/...

test: frontend-build
	$(TEST_WITH_GUARD) $$(go list $(GO_PACKAGE_PATTERNS) | grep -v -E '/(provider/claudecli|provider/codexapp)$$') -race -count=1
	@echo "\n=== deferred E2E packages (sequential, -p 1) ==="
	$(TEST_WITH_GUARD) $(DEFERRED_TEST_PKGS) -race -count=1 -p 1 -timeout 120s

p2-audit:
	$(TEST_WITH_GUARD) ./internal/logaudit -count=1

log-audit:
	go run ./cmd/log-audit/main.go -hours=24 -top=10 -pretty=true

test-e2e:
	$(TEST_WITH_GUARD) -tags=e2e ./cmd/rpc-test/ -v -timeout 120s -count=1

test-e2e-rpc-runtime:
	$(TEST_WITH_GUARD) -tags=e2e ./internal/e2e/rpc_runtime -v -timeout 120s -count=1

ida-test-all:
	go run ./cmd/ida-test-orchestrator

ida-test-heavy:
	go run ./cmd/ida-test-orchestrator --include-heavy-fork


# protocol-sync-check: RPC smoke coverage + protocol freeze guards.
# Keeps mcp-orch launcher method aliases, report protocol, and toolbridge protocol
# from silently drifting after shared constants / split files move around.
protocol-sync-check: rpc-regression-check
	@echo "[protocol-sync-check] protocol freeze guards"
	$(TEST_WITH_GUARD) ./internal/archtest -run 'Test(OrchestrationLauncherProtocolFreeze|OrchestrationReportProtocolFreeze|ToolbridgeProtocolFreezeContractGuard)$$' -count=1

# rpc-regression-check: fast JSON-RPC package regression used by protocol gates.
rpc-regression-check:
	@echo "[rpc-regression-check] platform/rpc quick regression"
	$(TEST_WITH_GUARD) ./internal/platform/rpc/... -count=1

# codemap-check is intentionally read-only: it fails if generated codemap state
# is stale. Use codemap-refresh when docs/doc/codemap/ai-index.json should change.
codemap-check:
	go run scripts/codemap_index.go --check
	@echo "✅ codemap generated files are up to date"

# codemap-refresh rewrites docs/doc/codemap/ai-index.json from current sources.
codemap-refresh:
	go run scripts/codemap_index.go
	@echo "✅ codemap ai-index.json refreshed"

project-map-check:
	node scripts/generate_ai_project_map.js --check --strict-drift $(PROJECT_MAP_ARGS)
	@echo "✅ project map generated files are up to date"

project-map-refresh:
	node scripts/generate_ai_project_map.js $(PROJECT_MAP_ARGS)
	@echo "✅ project map refreshed"

capcontract-check:
	go run ./scripts/capcontract --check
	@echo "✅ capability contract manifest is up to date"

capcontract-refresh:
	go run ./scripts/capcontract
	@echo "✅ capability contract manifest refreshed"

vet: guard
	go vet $(GO_PACKAGE_PATTERNS)

clean:
	rm -rf bin/

guard:
	$(TEST_WITH_GUARD) ./internal/archtest -count=1

ai-maintenance-gates:
	./scripts/ai_maintenance_gates.sh $(AI_MAINTENANCE_ARGS)

code-size-guard:
	$(TEST_WITH_GUARD) --guard-only

guard-shell:
	./scripts/go_guard_shell.sh

fmt:
	goimports -w .

ui-cover-build:
	./scripts/ui-coverage.sh build

ui-cover-run:
	./scripts/ui-coverage.sh run --debug

ui-cover-report:
	./scripts/ui-coverage.sh report

app-cover-build:
	TARGET=app-server ./scripts/ui-coverage.sh build

app-cover-run:
	TARGET=app-server ./scripts/ui-coverage.sh run --listen ws://127.0.0.1:4500

app-cover-report:
	TARGET=app-server ./scripts/ui-coverage.sh report

.PHONY: ci-l0 ci-l1 ci-l2-claude ci-l3-release install-hooks _hook_check protocol-sync-check rpc-regression-check

# install-hooks: 一次性激活 .githooks/ 下的 pre-commit / commit-msg / pre-push
# 用相对路径写入 core.hooksPath，让每个 linked worktree 都解析到自己的 .githooks
# 检测到既有不同的 core.hooksPath 会先 warn，不阻断
INSTALL_HOOKS_DIR := .githooks
INSTALL_HOOKS_ABS_DIR := $(abspath $(INSTALL_HOOKS_DIR))
install-hooks:
	@CURRENT=$$(git config --get core.hooksPath 2>/dev/null || true); \
	if [ -n "$$CURRENT" ] && [ "$$CURRENT" != "$(INSTALL_HOOKS_DIR)" ]; then \
	  echo "⚠️  既有 core.hooksPath = $$CURRENT (将被覆盖为 $(INSTALL_HOOKS_DIR))"; \
	fi
	@git config core.hooksPath "$(INSTALL_HOOKS_DIR)"
	@echo "✅ git hooks installed ($(INSTALL_HOOKS_DIR) -> $(INSTALL_HOOKS_ABS_DIR))"
	@echo "   绕过仅限紧急（仓库规约 docs/1/会话习惯.md §10.12«禁止 bypass pre-commit hook»）：git commit/push --no-verify"

# _hook_check: build 完成后的 hook 装设 + 路径有效性检查，warn-only 不阻断
# 检 hooksPath 是否使用 worktree-safe 的 .githooks 且该路径真实存在；CI 可用 MAKE_HOOK_CHECK=0 短路提示
_hook_check:
	@if [ "$(MAKE_HOOK_CHECK)" = "0" ]; then exit 0; fi; \
	git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0; \
	CURRENT=$$(git config --get core.hooksPath 2>/dev/null || true); \
	if [ -z "$$CURRENT" ]; then \
	  echo "⚠️  git hooks 未装。推荐跑：make install-hooks"; \
	elif [ "$$CURRENT" = "$(INSTALL_HOOKS_DIR)" ] && [ ! -d "$(INSTALL_HOOKS_DIR)" ]; then \
	  echo "⚠️  hooksPath 指向不存在的路径 ($(INSTALL_HOOKS_DIR)) —— 请确认在仓库根目录并重装：make install-hooks"; \
	elif [ "$$CURRENT" != "$(INSTALL_HOOKS_DIR)" ]; then \
	  echo "⚠️  hooksPath 不是 worktree-safe 的 $(INSTALL_HOOKS_DIR)（当前：$$CURRENT）。推荐跑：make install-hooks"; \
	fi

ci-l0:
	@echo "[ci-l0] quick gate (no real Claude CLI)"
	@go list ./pkg/... >/dev/null
	$(TEST_WITH_GUARD) ./internal/provider/claudecli/... -count=1
	$(TEST_WITH_GUARD) ./internal/platform/runner/... ./internal/app/... -count=1
	$(TEST_WITH_GUARD) ./internal/platform/rpc/... -count=1

ci-l1:
	@echo "[ci-l1] extended unit regression"
	$(TEST_WITH_GUARD) $$(go list $(GO_PACKAGE_PATTERNS) | grep -v -E '/(provider/claudecli|provider/codexapp)$$') -count=1
	@echo "[ci-l1] deferred E2E packages (sequential)"
	$(TEST_WITH_GUARD) $(DEFERRED_TEST_PKGS) -count=1 -p 1 -timeout 120s

test-deferred:
	@echo "=== deferred E2E packages only (sequential, -p 1) ==="
	$(TEST_WITH_GUARD) $(DEFERRED_TEST_PKGS) -race -count=1 -p 1 -timeout 120s -v

ci-l2-claude:
	@echo "[ci-l2-claude] conditional integration (integration_claude)"
	@if [ -n "$(CLAUDE_CLI_BIN)" ] && [ -x "$(CLAUDE_CLI_BIN)" ]; then \
		echo "[ci-l2-claude] using CLAUDE_CLI_BIN=$(CLAUDE_CLI_BIN)"; \
		CLAUDE_CLI_BIN="$(CLAUDE_CLI_BIN)" $(TEST_WITH_GUARD) -tags=integration_claude ./internal/platform/rpc/... ./internal/platform/runner/... ./internal/app/... -count=1; \
	else \
		echo "[ci-l2-claude] SKIP: CLAUDE_CLI_BIN is empty or not executable"; \
	fi

ci-l3-release: ci-l0 ci-l1 ci-l2-claude
	@echo "[ci-l3-release] all layered gates finished"

# ---------------------------------------------------------------------------
# sqlc — generate typed query code from each sqlc.yaml schema/query snapshot.
# Pin the CLI version so every
# contributor and CI node produces byte-identical output; drift would defeat
# the whole point of a type-safe store layer.
SQLC_VERSION ?= v1.30.0
# Hermetic invocation via 'go run' ensures every contributor / CI job produces
# byte-identical output regardless of any sqlc binary that may live on PATH
# (e.g. homebrew-managed copies at a different version). The first run hits the
# Go module cache; subsequent runs are just a local exec.
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

sqlc-generate:
	$(SQLC) generate
	$(SQLC) generate -f cmd/mcp-orch/sqlc.yaml
	@echo "✅ sqlc generate (root + cmd/mcp-orch)"

# CI gate: regenerate and fail if the working tree drifts from committed output.
sqlc-verify:
	$(SQLC) generate
	$(SQLC) generate -f cmd/mcp-orch/sqlc.yaml
	@if [ -n "$$(git status --porcelain --untracked-files=all -- internal/store/sqlc cmd/mcp-orch/store/sqlc)" ]; then \
		echo "❌ sqlc-generated code is out of date; run 'make sqlc-generate' and commit."; \
		git --no-pager diff -- internal/store/sqlc cmd/mcp-orch/store/sqlc; \
		UNTRACKED=$$(git ls-files --others --exclude-standard -- internal/store/sqlc cmd/mcp-orch/store/sqlc); \
		if [ -n "$$UNTRACKED" ]; then \
			echo "Untracked generated files:"; \
			echo "$$UNTRACKED"; \
		fi; \
		exit 1; \
	fi
	@echo "✅ sqlc-verify (generated code matches root + cmd/mcp-orch sqlc configs)"

# Worker worktree gate: generated files may already be dirty against HEAD, but
# regeneration must be stable against the worktree's current generated content.
sqlc-verify-worktree:
	bash scripts/sqlc_verify_worktree.sh
