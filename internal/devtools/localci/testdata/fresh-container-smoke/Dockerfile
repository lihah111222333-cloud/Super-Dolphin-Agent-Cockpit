FROM golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS build
WORKDIR /src
COPY main.txt ./main.go
RUN --network=none CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /out/super-dolphin-gate-executor ./main.go

FROM scratch
COPY --from=build /out/super-dolphin-gate-executor /usr/local/bin/super-dolphin-gate-executor
USER 65532:65532
