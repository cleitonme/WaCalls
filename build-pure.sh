#!/bin/bash

# Build and push the pure-Go Docker image (no CGO, no libopus).
# Usage: ./build-pure.sh [--push] [--platform linux/amd64,linux/arm64]

set -e

IMAGE_NAME="whazing/wacalls:latest"
BUILDER_NAME="wacalls-builder"
PLATFORMS="${2:-linux/amd64}"
PUSH=""

if [ "$1" = "--push" ]; then
  PUSH="--push"
fi

echo "Building PURE-GO variant (CGO_ENABLED=0, no native encoder)..."

if ! docker buildx inspect "$BUILDER_NAME" > /dev/null 2>&1; then
  echo "Creating builder '$BUILDER_NAME'..."
  docker buildx create --name "$BUILDER_NAME" --driver docker-container --use
  docker buildx inspect --bootstrap
else
  docker buildx use "$BUILDER_NAME"
fi

docker buildx build \
  --platform "$PLATFORMS" \
  --build-arg VARIANT=pure \
  -t "$IMAGE_NAME" \
  $PUSH \
  .

echo "Done: $IMAGE_NAME (pure-go)"
