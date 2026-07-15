# rocketplane control plane — build from the repo root:
#   docker build -t rocketplane/controlplane:dev -f services/controlplane/Dockerfile .
# Single static binary; SQL migrations are embedded and applied at boot.
# CI builds this multi-arch via buildx (native build stage, cross-compile).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src/services/controlplane
COPY services/controlplane/go.mod services/controlplane/go.sum ./
RUN go mod download
COPY services/controlplane/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/controlplane ./cmd/controlplane

FROM gcr.io/distroless/static-debian12:nonroot@sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
COPY --from=build /out/controlplane /controlplane
USER nonroot
EXPOSE 8090
ENTRYPOINT ["/controlplane"]
