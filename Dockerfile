# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:26-bookworm-slim@sha256:298992629d69bbd5b2b03ad57094d8e4fcb3a8eeb7c4e25d29de656519568913 AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
# Node 26 has no corepack; install the pnpm version package.json pins.
RUN npm install -g "pnpm@$(node -p 'require("./package.json").packageManager.split("@")[1]')" && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
# .git is not in the build context, so the revision is passed in.
ARG REVISION
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${REVISION}" -o /tsrelay ./cmd/tsrelay
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
