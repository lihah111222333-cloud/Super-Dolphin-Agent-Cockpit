ARG GO_IMAGE=golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651
FROM ${GO_IMAGE} AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/super-dolphin-gate/ ./cmd/super-dolphin-gate/
COPY internal/devtools/gate/ ./internal/devtools/gate/
RUN --network=none CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate

FROM scratch
COPY --from=build /out/super-dolphin-gate /super-dolphin-gate
ENTRYPOINT ["/super-dolphin-gate"]
