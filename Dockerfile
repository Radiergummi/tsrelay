# syntax=docker/dockerfile:1
FROM golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /tsrelay ./cmd/tsrelay

FROM gcr.io/distroless/static-debian12:nonroot
ENV TSRELAY_STATE_DIR=/var/lib/tsrelay
VOLUME ["/var/lib/tsrelay"]
COPY --from=builder /tsrelay /tsrelay
USER nonroot:nonroot
ENTRYPOINT ["/tsrelay", "serve"]
