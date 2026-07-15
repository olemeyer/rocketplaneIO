# rocketplane-agent — build from the repo root:
#   docker build -t rocketplane/agent:dev -f agent/Dockerfile .
# (minikube: follow with `minikube image load rocketplane/agent:dev`)
# CI builds this multi-arch via buildx: the build stage runs natively on the
# build host and cross-compiles for $TARGETARCH (pure Go, CGO off).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src/agent
COPY agent/go.mod agent/go.sum ./
RUN go mod download
COPY agent/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot@sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
COPY --from=build /out/agent /agent
USER nonroot
ENTRYPOINT ["/agent"]
