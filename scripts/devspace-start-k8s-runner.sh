#!/usr/bin/env sh
set -eu

elapsed=0
while [ ! -f /opt/app/data/go.mod ] || [ ! -f /opt/app/data/buf.gen.yaml ]; do
  sleep 1
  elapsed=$((elapsed + 1))
  [ $elapsed -ge 120 ] && exit 1
done

echo Generating protobuf types...
buf generate --include-imports buf.build/agynio/api \
  --path agynio/api/runner/v1 \
  --path agynio/api/ziti_management/v1 \
  --template ./buf.gen.yaml

echo Downloading Go modules...
go mod download

echo Starting k8s-runner...
exec go run ./cmd/k8s-runner
