#!/bin/bash

# Build and push the native CGO Docker image (libopus SMPL encoder).
# Requires the nativemlow build tag; libopus-dev is installed in the builder stage.
# Usage: ./build-native.sh [--push] [--platform linux/amd64,linux/arm64]

set -e

IMAGE_NAME="whazing/wacalls:native"
BUILDER_NAME="wacalls-builder"
PLATFORMS="${2:-linux/amd64}"
PUSH=""

if [ "$1" = "--push" ]; then
  PUSH="--push"
fi

echo "Building NATIVE variant (CGO_ENABLED=1, libopus SMPL encoder)..."

if ! docker buildx inspect "$BUILDER_NAME" > /dev/null 2>&1; then
  echo "Creating builder '$BUILDER_NAME'..."
  docker buildx create --name "$BUILDER_NAME" --driver docker-container --use
  docker buildx inspect --bootstrap
else
  docker buildx use "$BUILDER_NAME"
fi

docker buildx build \
  --platform "$PLATFORMS" \
  --build-arg VARIANT=native \
  -t "$IMAGE_NAME" \
  $PUSH \
  .

echo "Done: $IMAGE_NAME (native)"
