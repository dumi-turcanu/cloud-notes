# syntax=docker/dockerfile:1

# ---- build -----------------------------------------------------------------
# Always runs on the build host's architecture and cross-compiles (fast
# multi-arch builds: docker buildx build --platform linux/amd64,linux/arm64).
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/cloud-notes-api ./cmd/api

# ---- runtime ---------------------------------------------------------------
FROM gcr.io/distroless/static-debian13:nonroot

ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="cloud-notes-backend" \
      org.opencontainers.image.description="Cloud Notes API (Go, PostgreSQL)" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="https://github.com/dumi-turcanu/cloud-notes"

COPY --from=build /out/cloud-notes-api /cloud-notes-api

# 65532 is the distroless "nonroot" user; the numeric id works with Kubernetes runAsNonRoot.
USER 65532:65532
EXPOSE 8080 9090

# Distroless has no curl/wget: the binary probes itself (GET /readyz).
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/cloud-notes-api", "healthcheck"]

ENTRYPOINT ["/cloud-notes-api"]
CMD ["serve"]
