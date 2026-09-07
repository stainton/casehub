#!/usr/bin/env sh
set -eu
mkdir -p build
CGO_ENABLED=0 go build -trimpath -o build/casehub .
echo "Built build/casehub for $(go env GOOS)/$(go env GOARCH)"
