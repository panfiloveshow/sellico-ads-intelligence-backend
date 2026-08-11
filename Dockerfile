# --- Build stage: BOTH binaries, once ---
#
# api and worker are the same Go module and differ only in which binary the
# final image carries. Previously each had its own builder stage parameterised
# by TARGET, so buildx saw two different stages and compiled the whole
# dependency tree twice — in parallel, fighting for the VPS's cores (2 × 241 s).
#
# Here TARGET is used ONLY in the final stage, which makes this stage byte-identical
# for both images: buildx builds it once and reuses it. Compiling the second
# binary costs almost nothing because every internal/* package is already built.
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./

# Cache mounts, not layers: the module cache and the compiled-package cache
# survive between deploys. `docker image prune` in scripts/deploy.sh does not
# touch them; only an explicit `docker builder prune` would.
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

# -trimpath keeps builds reproducible; -buildvcs=false skips a VCS probe that
# can only fail here (.git is not in the build context).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/worker ./cmd/worker

# --- Final stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata su-exec
RUN addgroup -S app && adduser -S -G app app

WORKDIR /app

# The only difference between the api and worker images.
ARG TARGET=api
COPY --from=builder /out/${TARGET} /app/server
COPY --from=builder /app/migrations /app/migrations
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && chown -R app:app /app

EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
