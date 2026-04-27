# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG TARGET=api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/server ./cmd/${TARGET}

# Final stage
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata su-exec
RUN addgroup -S app && adduser -S -G app app

WORKDIR /app

COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/migrations /app/migrations
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && chown -R app:app /app

EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
