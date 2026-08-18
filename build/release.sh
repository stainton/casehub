#!/usr/bin/env bash
# Compile casehub-api and casehub-manager for the real split deployment (no
# bundled frontend — cmd/manager/main.go, not cmd/manager/mock), and
# build+push their Docker images in one step. For the single-container
# debug/demo image that bundles the frontend, use build/mock-release.sh
# instead.
#
# Usage:
#   build/release.sh                  # builds & pushes hjmasha/casehub-api:latest and hjmasha/casehub-manager:latest
#   build/release.sh v1.2.3           # override the tag (applies to both images)
#   build/release.sh -p                # skip compiling/building, just `docker push` the existing local images
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
  echo "==> Push-only: pushing hjmasha/casehub-api:$TAG"
  docker push "hjmasha/casehub-api:$TAG"

  echo "==> Push-only: pushing hjmasha/casehub-manager:$TAG"
  docker push "hjmasha/casehub-manager:$TAG"

  echo "==> Done: hjmasha/casehub-api:$TAG, hjmasha/casehub-manager:$TAG"
  exit 0
fi

echo "==> Compiling out/casehub-api (linux/amd64)"
# cmd/api/main.go is API-only, no frontend — not exposed externally, manager
# is the only caller (over HTTP).
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o out/casehub-api ./cmd/api

echo "==> Compiling out/casehub-manager (linux/amd64)"
# Builds cmd/manager/main.go, not cmd/manager/mock: the frontend-less entry
# point for the real split deployment. No bundled web/ — see
# build/mock-release.sh for the single-container debug/demo image.
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o out/casehub-manager ./cmd/manager

echo "==> Building and pushing hjmasha/casehub-api:$TAG"
docker buildx build --platform linux/amd64 -f build/Dockerfile.api -t "hjmasha/casehub-api:$TAG" --push .

echo "==> Building and pushing hjmasha/casehub-manager:$TAG"
docker buildx build --platform linux/amd64 -f build/Dockerfile.manager -t "hjmasha/casehub-manager:$TAG" --push .

echo "==> Done: hjmasha/casehub-api:$TAG, hjmasha/casehub-manager:$TAG"
