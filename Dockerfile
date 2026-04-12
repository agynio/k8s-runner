# syntax=docker/dockerfile:1
ARG GO_VERSION=1.25
ARG BUF_VERSION=1.64.0

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS buf
ARG BUF_VERSION
RUN apk add --no-cache curl
RUN curl -sSL \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-$(uname -s)-$(uname -m)" \
      -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY --from=buf /usr/local/bin/buf /usr/local/bin/buf
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY buf.gen.yaml ./
RUN git clone https://github.com/agynio/api.git /tmp/agynio-api && \
    git -C /tmp/agynio-api checkout ec008b1e2dfacec3e4d85776729fe1c3d5f2c42d
RUN cd /tmp/agynio-api && \
    buf generate --include-imports \
      --path proto/agynio/api/runner/v1 \
      --path proto/agynio/api/runners/v1 \
      --path proto/agynio/api/gateway/v1 \
      --template /src/buf.gen.yaml \
      -o /src
COPY . .
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -o /out/k8s-runner ./cmd/k8s-runner

FROM alpine:3.19
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
WORKDIR /app
COPY --from=build /out/k8s-runner /app/k8s-runner
USER appuser
ENTRYPOINT ["/app/k8s-runner"]
