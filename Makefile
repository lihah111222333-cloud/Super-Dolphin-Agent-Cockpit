.PHONY: build build-plain build-agent-terminal build-agent-terminal-plain run run-plain run-agent-terminal-debug run-agent-terminal-debug-plain test test-deferred vet clean guard protocol-sync-check codemap-check codemap-refresh setup-cgo ui-cover-build ui-cover-run ui-cover-report app-cover-build app-cover-run app-cover-report log-audit p2-audit ida-test-all ida-test-heavy

# Auto-detect macOS version to avoid ld warnings about version mismatch.
# Override with: make MIN_MACOS_VERSION=15.0 build
MIN_MACOS_VERSION ?= $(shell sw_vers -productVersion 2>/dev/null | cut -d. -f1-2 || echo 11.0)
FRIDA_VERSION_FILE ?= build/frida-version.txt
FRIDA_DEVKIT_VERSION ?= $(shell cat $(FRIDA_VERSION_FILE) 2>/dev/null)
FRIDA_LDFLAGS ?= -X github.com/multi-agent/go-agent-v2/pkg/idamcp.defaultFridaVersion=$(FRIDA_DEVKIT_VERSION)
AGENT_TERMINAL_DEBUG_PORT ?= 4501

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

build: guard
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go build -tags frida -ldflags "$(FRIDA_LDFLAGS)" ./...

build-plain: guard
	go build ./...

build-agent-terminal:
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go build -tags frida -ldflags "$(FRIDA_LDFLAGS)" -o bin/agent-terminal ./cmd/agent-terminal

build-agent-terminal-plain:
	go build -o bin/agent-terminal ./cmd/agent-terminal

run:
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go run -tags frida -ldflags "$(FRIDA_LDFLAGS)" ./cmd/server/main.go

run-plain:
	go run ./cmd/server/main.go

run-agent-terminal-debug:
	go run ./cmd/frida-bootstrap --frida-version "$(FRIDA_DEVKIT_VERSION)" -- \
		go run -tags frida -ldflags "$(FRIDA_LDFLAGS)" ./cmd/agent-terminal --debug --debug-port $(AGENT_TERMINAL_DEBUG_PORT)

run-agent-terminal-debug-plain:
	go run ./cmd/agent-terminal --debug --debug-port $(AGENT_TERMINAL_DEBUG_PORT)

mcp:
	go run ./cmd/mcp-server/main.go

# 已知并行争抢的 E2E 包（pipe/WebSocket/gopls 进程资源）：
#   pkg/agentsdk/claude, pkg/agentsdk/codex, pkg/toolsdk/lsp
# 先并行跑其余包，再用 -p 1 串行跑这 3 个，避免全仓并行时的 flaky failure。
DEFERRED_TEST_PKGS := github.com/multi-agent/go-agent-v2/pkg/agentsdk/claude github.com/multi-agent/go-agent-v2/pkg/agentsdk/codex github.com/multi-agent/go-agent-v2/pkg/toolsdk/lsp

test: guard
	go test $$(go list ./... | grep -v -E '/(agentsdk/claude|agentsdk/codex|toolsdk/lsp)$$') -race -count=1
	@echo "\n=== deferred E2E packages (sequential, -p 1) ==="
	go test $(DEFERRED_TEST_PKGS) -race -count=1 -p 1 -timeout 120s

p2-audit:
	go test ./internal/logaudit -count=1

log-audit:
	go run ./cmd/log-audit/main.go -hours=24 -top=10 -pretty=true

test-e2e:
	go test -tags=e2e ./cmd/rpc-test/ -v -timeout 120s -count=1

ida-test-all:
	go run ./cmd/ida-test-orchestrator

ida-test-heavy:
	go run ./cmd/ida-test-orchestrator --include-heavy-fork


protocol-sync-check:
	go test ./internal/protocolsync -run TestProtocolMethodCoverage_FromCodexRs -count=1
	go test ./internal/apiserver -run TestEventMethodMap_TargetMethodsKnownByProtocol -count=1

codemap-check:
	python3 docs/doc/validate_codemap.py

codemap-refresh:
	python3 docs/doc/export_docs_json.py --clean-output
	python3 docs/doc/validate_codemap.py

vet: guard
	go vet ./...

clean:
	rm -rf bin/

guard:
	go run ./scripts/code_size_guard.go

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

.PHONY: ci-l0 ci-l1 ci-l2-claude ci-l3-release

ci-l0: guard
	@echo "[ci-l0] quick gate (no real Claude CLI)"
	go test ./pkg/agentsdk/claude/... -count=1
	go test ./internal/runner/... -count=1
	go test ./internal/apiserver/... -count=1

ci-l1: guard
	@echo "[ci-l1] extended unit regression"
	go test $$(go list ./... | grep -v -E '/(agentsdk/claude|agentsdk/codex|toolsdk/lsp)$$') -count=1
	@echo "[ci-l1] deferred E2E packages (sequential)"
	go test $(DEFERRED_TEST_PKGS) -count=1 -p 1 -timeout 120s

test-deferred:
	@echo "=== deferred E2E packages only (sequential, -p 1) ==="
	go test $(DEFERRED_TEST_PKGS) -race -count=1 -p 1 -timeout 120s -v

ci-l2-claude:
	@echo "[ci-l2-claude] conditional integration (integration_claude)"
	@if [ -n "$(CLAUDE_CLI_BIN)" ] && [ -x "$(CLAUDE_CLI_BIN)" ]; then \
		echo "[ci-l2-claude] using CLAUDE_CLI_BIN=$(CLAUDE_CLI_BIN)"; \
		CLAUDE_CLI_BIN="$(CLAUDE_CLI_BIN)" go test -tags=integration_claude ./internal/apiserver/... ./internal/runner/... -count=1; \
	else \
		echo "[ci-l2-claude] SKIP: CLAUDE_CLI_BIN is empty or not executable"; \
	fi

ci-l3-release: ci-l0 ci-l1 ci-l2-claude
	@echo "[ci-l3-release] all layered gates finished"
