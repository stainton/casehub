#!/usr/bin/env sh
set -eu
mkdir -p output
CGO_ENABLED=0 go build -trimpath -o output/casehub .
echo "Built output/casehub for $(go env GOOS)/$(go env GOARCH)"
