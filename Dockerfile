# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /tsrelay ./cmd/tsrelay
RUN mkdir -p /out/var/lib/tsrelay

FROM gcr.io/distroless/static-debian12:nonroot
ENV TSRELAY_STATE_DIR=/var/lib/tsrelay
COPY --from=builder --chown=65532:65532 /out/var/lib/tsrelay /var/lib/tsrelay
VOLUME ["/var/lib/tsrelay"]
COPY --from=builder /tsrelay /tsrelay
USER 65532:65532
ENTRYPOINT ["/tsrelay", "serve"]
