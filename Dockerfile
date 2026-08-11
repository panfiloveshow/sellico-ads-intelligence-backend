# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./

# Cache mounts, not layers: the module cache and the compiled-package cache
# survive between builds AND are shared by the api and worker builds, which
# compile the same module. Without them every deploy recompiled the whole
# dependency tree twice from scratch (~5 minutes on the prod VPS).
#
# `docker image prune` in scripts/deploy.sh does not touch the build cache, so
# these survive deploys. A `docker builder prune` wipes them and the next build
# is slow again — that is the only cost.
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

ARG TARGET=api
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/server ./cmd/${TARGET}

# Final stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata su-exec
RUN addgroup -S app && adduser -S -G app app

WORKDIR /app

COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/migrations /app/migrations
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && chown -R app:app /app

EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
