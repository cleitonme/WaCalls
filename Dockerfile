# syntax=docker/dockerfile:1

ARG VARIANT=pure

FROM node:22-bookworm-slim AS client
WORKDIR /src/client
COPY client/package.json client/package-lock.json ./
RUN npm ci
COPY client/ ./
RUN npm run build

FROM golang:1.26-bookworm AS server
ARG VARIANT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=client /src/client/dist ./client/dist

RUN <<EOF
set -e
if [ "$VARIANT" = "native" ]; then
  apt-get update && apt-get install -y --no-install-recommends libopus-dev && rm -rf /var/lib/apt/lists/*
  export CGO_ENABLED=1
  go build -trimpath -tags nativemlow -ldflags "-s -w" -o /out/wacalls ./cmd/server
else
  export CGO_ENABLED=0
  go build -trimpath -ldflags "-s -w" -o /out/wacalls ./cmd/server
fi
EOF

FROM debian:bookworm-slim
ARG VARIANT
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=server /out/wacalls ./wacalls
COPY --from=client /src/client/dist ./client/dist
EXPOSE 8080 5000
VOLUME ["/data"]
ENTRYPOINT ["/app/wacalls"]
CMD ["-addr", ":8080", "-db", "/data/wacalls.db", "-static", "client/dist"]
