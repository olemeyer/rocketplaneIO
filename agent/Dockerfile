# rocketplane-agent — build from the repo root:
#   docker build -t rocketplane/agent:dev -f agent/Dockerfile .
# (minikube: follow with `minikube image load rocketplane/agent:dev`)
# CI builds this multi-arch via buildx: the build stage runs natively on the
# build host and cross-compiles for $TARGETARCH (pure Go, CGO off).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src/agent
COPY agent/go.mod agent/go.sum ./
RUN go mod download
COPY agent/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agent /agent
USER nonroot
ENTRYPOINT ["/agent"]
