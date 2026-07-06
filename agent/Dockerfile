# rocketplane-agent — build from the repo root:
#   docker build -t rocketplane/agent:dev -f agent/Dockerfile .
# (minikube: follow with `minikube image load rocketplane/agent:dev`)
FROM golang:1.25-alpine AS build
WORKDIR /src/agent
COPY agent/go.mod agent/go.sum ./
RUN go mod download
COPY agent/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agent /agent
USER nonroot
ENTRYPOINT ["/agent"]
