#!/usr/bin/env bash
set -eu

echo "=== DevSpace startup ==="

echo "Generating protobuf types..."
buf generate buf.build/agynio/api --path agynio/api/runner/v1 --path agynio/api/ziti_management/v1 --template ./buf.gen.yaml

echo "Downloading Go modules..."
go mod download

echo "Starting dev server (air)..."
exec air
