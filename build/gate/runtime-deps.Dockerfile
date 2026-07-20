ARG GO_IMAGE
ARG NODE_IMAGE
ARG SQRUFF_ARCHIVE_URL_AMD64
ARG SQRUFF_ARCHIVE_SHA256_AMD64
ARG SQRUFF_ARCHIVE_URL_ARM64
ARG SQRUFF_ARCHIVE_SHA256_ARM64

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS repository-vendor
WORKDIR /src
COPY . .
RUN GOWORK=off GOTOOLCHAIN=local go mod download all && \
    GOWORK=off GOTOOLCHAIN=local go mod vendor -o /out/vendor && \
    test -f /out/vendor/golang.org/x/tools/go/analysis/multichecker/multichecker.go && \
    test -f /out/vendor/golang.org/x/tools/go/analysis/passes/nilness/nilness.go && \
    cp -a /out/vendor /src/vendor && \
    GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -mod=vendor -trimpath -buildvcs=false -o /tmp/nilness-guard ./scripts/nilness_guard.go && \
    rm -f /tmp/nilness-guard && rm -rf /src/vendor && \
    cd /src/build/gate/runtime-proxy && \
    GOWORK=off GOTOOLCHAIN=local go mod download all && \
    test -f "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/v1.5.2.info" && \
    test -f "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/v1.5.2.mod" && \
    test -f "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/v1.5.2.zip" && \
    grep -Fxq v1.5.2 "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/list" && \
    mkdir -p /out/go-proxy && \
    cp -a "$(go env GOMODCACHE)/cache/download/." /out/go-proxy/

FROM ${NODE_IMAGE} AS frontend-seed
WORKDIR /src/frontend-app
COPY frontend-app/package.json frontend-app/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund && \
    PLAYWRIGHT_BROWSERS_PATH=/src/frontend-app/node_modules/.cache/ms-playwright \
    ./node_modules/.bin/playwright install chromium && \
    mkdir -p /out/frontend && mv node_modules /out/frontend/node_modules

FROM ${NODE_IMAGE} AS lsp-seed
WORKDIR /src/runtime-lsp
COPY build/gate/runtime-lsp/package.json build/gate/runtime-lsp/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund && mkdir -p /out/lsp && mv node_modules /out/lsp/node_modules

FROM ${NODE_IMAGE} AS ripgrep-seed
RUN apt-get update && apt-get install -y --no-install-recommends ripgrep=13.0.0-4+b2 && \
    mkdir -p /out/bin && cp /usr/bin/rg /out/bin/rg

FROM ${GO_IMAGE} AS sqruff-seed
ARG TARGETARCH
ARG SQRUFF_ARCHIVE_URL_AMD64
ARG SQRUFF_ARCHIVE_SHA256_AMD64
ARG SQRUFF_ARCHIVE_URL_ARM64
ARG SQRUFF_ARCHIVE_SHA256_ARM64
RUN case "${TARGETARCH}" in \
		amd64) archive_url="${SQRUFF_ARCHIVE_URL_AMD64}"; archive_sha256="${SQRUFF_ARCHIVE_SHA256_AMD64}" ;; \
		arm64) archive_url="${SQRUFF_ARCHIVE_URL_ARM64}"; archive_sha256="${SQRUFF_ARCHIVE_SHA256_ARM64}" ;; \
		*) echo "unsupported Sqruff target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
	esac; \
	test -n "${archive_url}" && test -n "${archive_sha256}" && \
	curl -fsSL --retry 2 --connect-timeout 15 --max-time 180 -o /tmp/sqruff.tar.gz "${archive_url}" && \
	echo "${archive_sha256}  /tmp/sqruff.tar.gz" | sha256sum -c - && \
    mkdir -p /out/bin && tar -xzf /tmp/sqruff.tar.gz -C /out/bin && \
    test "$(/out/bin/sqruff --version)" = "sqruff 0.38.0"

FROM ${GO_IMAGE} AS tool-seed
WORKDIR /src/runtime-tools
COPY build/gate/runtime-tools/go.mod build/gate/runtime-tools/go.sum build/gate/runtime-tools/tools.go ./
RUN GOWORK=off GOTOOLCHAIN=local go mod download && \
    CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local go build -trimpath -buildvcs=false -o /out/gopls golang.org/x/tools/gopls && \
    CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local go build -trimpath -buildvcs=false -o /out/sqlc github.com/sqlc-dev/sqlc/cmd/sqlc

FROM ${GO_IMAGE} AS manifest-builder
WORKDIR /src
COPY . .
COPY --from=repository-vendor /out/vendor /runtime/vendor
COPY --from=repository-vendor /out/vendor /src/vendor
COPY --from=repository-vendor /out/go-proxy /runtime/go-proxy
COPY --from=frontend-seed /out/frontend/node_modules /runtime/frontend/node_modules
COPY --from=ripgrep-seed /out/bin/rg /runtime/bin/rg
COPY --from=sqruff-seed /out/bin/sqruff /runtime/bin/sqruff
RUN GOWORK=off GOTOOLCHAIN=local go build -mod=vendor -trimpath -buildvcs=false -o /out/super-dolphin-runtime-seed ./build/gate/cmd/runtime-seed-manifest && \
    /out/super-dolphin-runtime-seed write /src /runtime

FROM ${GO_IMAGE}
USER root
COPY --from=lsp-seed /usr/local/bin/node /usr/local/bin/node
COPY --from=lsp-seed /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
COPY --from=repository-vendor /out/vendor /opt/super-dolphin-gate/runtime/vendor
COPY --from=repository-vendor /out/go-proxy /opt/super-dolphin-gate/runtime/go-proxy
COPY --from=frontend-seed /out/frontend/node_modules /opt/super-dolphin-gate/runtime/frontend/node_modules
COPY --from=lsp-seed /out/lsp/node_modules /opt/super-dolphin-gate/runtime/lsp/node_modules
COPY --from=manifest-builder /out/super-dolphin-runtime-seed /usr/local/bin/super-dolphin-runtime-seed
COPY --from=tool-seed /out/gopls /usr/local/bin/gopls
COPY --from=tool-seed /out/sqlc /opt/super-dolphin-gate/runtime/bin/sqlc
COPY --from=ripgrep-seed /out/bin/rg /opt/super-dolphin-gate/runtime/bin/rg
COPY --from=sqruff-seed /out/bin/sqruff /opt/super-dolphin-gate/runtime/bin/sqruff
RUN set -eu; \
    retry_command() { \
      attempts=0; \
      until "$@"; do \
        attempts=$((attempts + 1)); \
        if [ "$attempts" -ge 5 ]; then return 1; fi; \
        sleep $((attempts * 3)); \
      done; \
    }; \
    ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm; \
    ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/typescript-language-server /usr/local/bin/typescript-language-server; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/vscode-css-language-server /usr/local/bin/vscode-css-language-server; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/pyright-langserver /usr/local/bin/pyright-langserver; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/bash-language-server /usr/local/bin/bash-language-server; \
    printf 'Acquire::Retries "10";\nAcquire::http::No-Cache "true";\nAcquire::http::Pipeline-Depth "0";\n' > /etc/apt/apt.conf.d/80-super-dolphin-retries; \
    if [ -f /etc/apt/sources.list ]; then sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list; fi; \
    find /etc/apt/sources.list.d -type f \( -name '*.list' -o -name '*.sources' \) -exec sed -i 's|http://deb.debian.org|https://deb.debian.org|g' {} +; \
    retry_command env PLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright \
      /opt/super-dolphin-gate/runtime/frontend/node_modules/.bin/playwright install-deps chromium; \
    retry_command sh -c 'apt-get update && apt-get install -y --no-install-recommends libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev pkg-config procps'; \
    rm -rf /var/lib/apt/lists/*; \
    pkg-config --exists gtk+-3.0 webkit2gtk-4.1 gio-unix-2.0 libsoup-3.0; \
    ln -s /opt/super-dolphin-gate/runtime/bin/rg /usr/local/bin/rg; \
    ln -s /opt/super-dolphin-gate/runtime/bin/sqruff /usr/local/bin/sqruff; \
    chmod -R a+rX /opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright; \
    test -x /usr/bin/git && test -x /usr/bin/make && test -x /usr/bin/python3 && test -x /usr/bin/ps && test -x /usr/local/bin/node && test -x /usr/local/bin/npm; \
    test -x /usr/local/go/bin/go && test -x /usr/local/bin/gopls && test -x /opt/super-dolphin-gate/runtime/bin/sqlc && test -x /opt/super-dolphin-gate/runtime/bin/rg && test -x /opt/super-dolphin-gate/runtime/bin/sqruff; \
    test "$(rg --version | head -n 1)" = "ripgrep 13.0.0" && test "$(sqruff --version)" = "sqruff 0.38.0"
COPY --from=manifest-builder /runtime/manifest.json /opt/super-dolphin-gate/runtime/manifest.json
ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
    PLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright \
    GOTOOLCHAIN=local GOPROXY=file:///opt/super-dolphin-gate/runtime/go-proxy GOSUMDB=off
USER 65532:65532
RUN --network=none node -e 'const { chromium } = require("/opt/super-dolphin-gate/runtime/frontend/node_modules/playwright"); chromium.launch({headless:true}).then(async browser => { await browser.close(); }).catch(error => { console.error(error); process.exit(1); });'
RUN --network=none sqruff --version | grep -Fx 'sqruff 0.38.0'
