#!/usr/bin/env bash
# Compile casehub-manager for the real deployment (no bundled frontend —
# cmd/manager/main.go, not cmd/manager/mock), and build+push its Docker
# image. For the single-container debug/demo image that bundles the
# frontend, use build/mock-release.sh instead.
#
# Usage:
#   build/release.sh                  # builds & pushes hjmasha/casehub-manager:latest
#   build/release.sh v1.2.3           # override the tag
#   build/release.sh -p                # skip compiling/building, just `docker push` the existing local image
#   build/release.sh -p v1.2.3        # push-only, with an overridden tag
set -euo pipefail

PUSH_ONLY=false
TAG="latest"

for arg in "$@"; do
  case "$arg" in
    -p|--push-only)
      PUSH_ONLY=true
      ;;
    *)
      TAG="$arg"
      ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if [ "$PUSH_ONLY" = true ]; then
  echo "==> Push-only: pushing hjmasha/casehub-manager:$TAG"
  docker push "hjmasha/casehub-manager:$TAG"

  echo "==> Done: hjmasha/casehub-manager:$TAG"
  exit 0
fi

echo "==> Compiling out/casehub-manager (linux/amd64)"
# Builds cmd/manager/main.go, not cmd/manager/mock: the frontend-less entry
# point for the real deployment. No bundled web/ — see build/mock-release.sh
# for the single-container debug/demo image.
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o out/casehub-manager ./cmd/manager

echo "==> Building and pushing hjmasha/casehub-manager:$TAG"
docker buildx build --platform linux/amd64 -f build/Dockerfile.manager -t "hjmasha/casehub-manager:$TAG" --push .

echo "==> Done: hjmasha/casehub-manager:$TAG"
