# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:d6aa754f16b3197301076f047b5def2f02ea1dbbc2ca920407d46d7ec7f87b20 AS web
WORKDIR /src/web
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /tsrelay ./cmd/tsrelay
RUN mkdir -p /out/var/lib/tsrelay

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/Radiergummi/tsrelay" \
      org.opencontainers.image.description="Deliver HTTP callbacks from your tailnet to apps on your laptop" \
      org.opencontainers.image.licenses="Apache-2.0"
ENV TSRELAY_STATE_DIR=/var/lib/tsrelay
COPY --from=builder --chown=65532:65532 /out/var/lib/tsrelay /var/lib/tsrelay
VOLUME ["/var/lib/tsrelay"]
COPY --from=builder /tsrelay /tsrelay
USER 65532:65532
ENTRYPOINT ["/tsrelay", "serve"]
